package usecase

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgnik "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const idemScopeHold = "hold"

// IDGen produces new identifiers (UUID, public ref). Injected for determinism in tests.
type IDGen interface {
	NewID() string
	NewPublicRef() string
}

// HoldPolicy carries the buyer-NIK attempt budget and throttle (R10.6), set from config.
type HoldPolicy struct {
	RefusalBudget int
	ThrottleBase  time.Duration
	ThrottleMax   time.Duration
}

// Hold performs the atomic hold: validate → idempotency guard (replay before any state read) → plan → UoW.Hold
// (one tx, lock ordered). It enforces invariant #1 (no oversell) via the DB CHECKs inside the unit of work and
// binds the buyer NIK/email to the admission inside that same transaction (ADR-030).
type Hold struct {
	admissions outbound.IAdmissionRepo
	events     outbound.IEventRepo
	idem       outbound.IIdempotencyStore
	uow        outbound.IUnitOfWork
	pii        outbound.IBuyerPII
	ids        IDGen
	clock      clock.IClock
	policy     HoldPolicy
	idemTTL    time.Duration
}

// NewHold wires the hold usecase.
func NewHold(admissions outbound.IAdmissionRepo, events outbound.IEventRepo, idem outbound.IIdempotencyStore,
	uow outbound.IUnitOfWork, pii outbound.IBuyerPII, ids IDGen, clk clock.IClock, policy HoldPolicy) *Hold {
	return &Hold{
		admissions: admissions, events: events, idem: idem, uow: uow,
		pii: pii, ids: ids, clock: clk, policy: policy, idemTTL: 24 * time.Hour,
	}
}

// validated is the output of the pure validation step: no database or event read happened to produce it.
type validated struct {
	buyerNIK    vo.NIK
	buyerEmail  string
	holders     []outbound.HoldLine // category/price filled by plan()
	requestHash []byte
}

// Hold runs the full hold flow and returns the created booking.
func (uc *Hold) Hold(ctx context.Context, cmd inbound.HoldCommand) (dto.Booking, error) {
	if err := validateIdempotencyKey(cmd.IdempotencyKey); err != nil {
		return dto.Booking{}, err
	}
	v, err := uc.validate(cmd.Request)
	if err != nil {
		return dto.Booking{}, err
	}

	// Replay must not depend on admission state, which changes after the first success.
	rec, found, err := uc.idem.Begin(ctx, idemScopeHold, cmd.VisitorID, cmd.IdempotencyKey, v.requestHash, uc.idemTTL)
	if err != nil {
		return dto.Booking{}, err
	}
	if found {
		return uc.replay(rec, v.requestHash)
	}

	booking, err := uc.execute(ctx, cmd, v)
	if err != nil {
		uc.abortOnRefusal(ctx, cmd, v.requestHash, err)
		return dto.Booking{}, err
	}
	body, _ := json.Marshal(booking)
	if cerr := uc.idem.Complete(ctx, idemScopeHold, cmd.VisitorID, cmd.IdempotencyKey, 201, body); cerr != nil {
		return dto.Booking{}, cerr
	}
	return booking, nil
}

// abortOnRefusal releases the IN_PROGRESS record on a typed 4xx business refusal so the client may retry with
// the same key (decision 6). Server errors, dependency failures and cancellations keep the record.
func (uc *Hold) abortOnRefusal(ctx context.Context, cmd inbound.HoldCommand, hash []byte, cause error) {
	if !isRetryableRefusal(cause) {
		return
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = uc.idem.Abort(actx, idemScopeHold, cmd.VisitorID, cmd.IdempotencyKey, hash)
}

func isRetryableRefusal(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *apperror.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.HTTPStatus >= 400 && ae.HTTPStatus < 500 && ae.Code != inbound.CodeIdemReused
}

func (uc *Hold) execute(ctx context.Context, cmd inbound.HoldCommand, v validated) (dto.Booking, error) {
	admission, lines, amount, err := uc.plan(ctx, cmd, v)
	if err != nil {
		return dto.Booking{}, err
	}
	emailEnc, err := uc.pii.SealBuyerEmail(v.buyerEmail)
	if err != nil {
		return dto.Booking{}, inbound.ErrDependency
	}

	now := uc.clock.Now()
	accountID := admission.AccountID // the principal that queued; a bearer on hold only fills the gap
	if accountID == "" {
		accountID = cmd.AccountID
	}
	spec := outbound.HoldSpec{
		BookingID:        uc.ids.NewID(),
		PublicRef:        uc.ids.NewPublicRef(),
		EventID:          admission.EventID,
		AdmissionID:      admission.ID,
		VisitorID:        cmd.VisitorID,
		BuyerNIKHash:     v.buyerNIK.Hash(),
		BuyerNIKMasked:   v.buyerNIK.Masked(),
		BuyerEmailEnc:    emailEnc,
		AccountID:        accountID,
		AmountMinor:      amount,
		PaymentExpiresAt: now.Add(time.Duration(admission.CfgPaymentTTLSeconds) * time.Second),
		Lines:            lines,
		CorrelationID:    cmd.CorrelationID,
		CfgEditEnabled:   admission.CfgEditEnabled,
		Now:              now,
		RefusalBudget:    uc.policy.RefusalBudget,
		ThrottleBase:     uc.policy.ThrottleBase,
		ThrottleMax:      uc.policy.ThrottleMax,
	}
	res, err := uc.uow.Hold(ctx, spec)
	if err != nil {
		return dto.Booking{}, err
	}
	if res.Refused == outbound.RefusedBuyerNikLimit {
		return dto.Booking{}, inbound.ErrBuyerNikLimitReached
	}

	return dto.Booking{
		BookingID:        res.BookingID,
		PublicRef:        res.PublicRef,
		OrderID:          res.PublicRef,
		Status:           res.Status,
		AmountMinor:      res.AmountMinor,
		TicketQuantity:   len(lines),
		BuyerNIKMasked:   v.buyerNIK.Masked(),
		Currency:         vo.Currency,
		PaymentExpiresAt: res.PaymentExpiresAt,
		ServerTime:       now,
		Version:          res.Version,
		CanEdit:          admission.CanEdit(),
		EditsRemaining:   admission.EditsRemaining(),
		Items:            holdItemViews(lines, res.ItemIDs),
	}, nil
}

// validate performs the pure checks in the documented order. It does no I/O, so a malformed request never
// creates an idempotency record and a replay never re-reads state. The max_tickets_per_order check lives in
// plan(), because the limit sits on the event row.
func (uc *Hold) validate(req dto.HoldRequest) (validated, error) {
	if !isUUID(req.SessionID) {
		return validated{}, inbound.Validation("sessionId", "sessionId harus UUID")
	}
	for _, it := range req.Items {
		if !isUUID(it.CategoryID) {
			return validated{}, inbound.Validation("categoryId", "categoryId harus UUID")
		}
	}
	if len(req.Items) == 0 {
		return validated{}, inbound.ErrQuantityInvalid
	}
	now := uc.clock.Now()
	if err := pkgnik.Validate(req.Buyer.NIK, now); err != nil {
		return validated{}, inbound.ErrNIKInvalid
	}
	buyerNIK, perr := vo.ParseNIKHashOnly(req.Buyer.NIK, uc.pii)
	if perr != nil {
		return validated{}, inbound.ErrNIKInvalid
	}
	if !vo.EmailsMatch(req.Buyer.Email, req.Buyer.EmailConfirmation) {
		return validated{}, inbound.ErrEmailMismatch
	}
	email, eerr := vo.ParseEmail(req.Buyer.Email)
	if eerr != nil {
		return validated{}, inbound.Validation("email", "format email tidak valid")
	}
	if !req.Buyer.Consent {
		return validated{}, inbound.ErrConsentRequired
	}

	seenNIK := map[string]bool{}
	holders := make([]outbound.HoldLine, 0, len(req.Items))
	canon := make([]map[string]string, 0, len(req.Items))
	for _, it := range req.Items {
		name := it.Attendee.FullName
		if len(name) < 2 || len(name) > 100 {
			return validated{}, inbound.Validation("fullName", "fullName harus 2..100 karakter")
		}
		if err := pkgnik.Validate(it.Attendee.NIK, now); err != nil {
			return validated{}, inbound.ErrNIKInvalid
		}
		nik, perr := vo.ParseNIK(it.Attendee.NIK, uc.pii)
		if perr != nil {
			return validated{}, inbound.ErrNIKInvalid
		}
		key := string(nik.Hash())
		if seenNIK[key] {
			return validated{}, inbound.ErrNikDuplicate
		}
		seenNIK[key] = true
		holders = append(holders, outbound.HoldLine{
			CategoryID:        it.CategoryID,
			AttendeeNIKHash:   nik.Hash(),
			AttendeeNIKEnc:    nik.Enc(),
			AttendeeNIKMasked: nik.Masked(),
			AttendeeName:      name,
		})
		canon = append(canon, map[string]string{
			"c": it.CategoryID, "h": hex.EncodeToString(nik.Hash()), "n": name,
		})
	}
	canonical, _ := json.Marshal(map[string]any{
		"s": req.SessionID, "b": hex.EncodeToString(buyerNIK.Hash()),
		"e": hex.EncodeToString(uc.pii.Fingerprint([]byte(strings.ToLower(email)))), "c": req.Buyer.Consent, "i": canon,
	})
	return validated{buyerNIK: buyerNIK, buyerEmail: email, holders: holders, requestHash: uc.pii.Fingerprint(canonical)}, nil
}

// plan reads the admission and event (after the idempotency record exists) and prices the lines. It performs
// no stock mutation. Every typed refusal here aborts the idempotency record via Hold().
func (uc *Hold) plan(ctx context.Context, cmd inbound.HoldCommand, v validated) (entity.Admission, []outbound.HoldLine, int64, error) {
	req := cmd.Request
	admission, err := uc.admissions.GetByID(ctx, req.SessionID)
	if err != nil {
		return entity.Admission{}, nil, 0, err
	}
	if admission.VisitorID != cmd.VisitorID {
		return entity.Admission{}, nil, 0, inbound.ErrVisitorMismatch
	}
	if admission.IsExpired(uc.clock.Now()) || !admission.IsActive() {
		return entity.Admission{}, nil, 0, inbound.ErrSessionExpired
	}
	detail, err := uc.events.GetDetail(ctx, admission.EventID)
	if err != nil {
		return entity.Admission{}, nil, 0, err
	}
	if detail.Event.Status == entity.EventCancelled {
		return entity.Admission{}, nil, 0, inbound.ErrEventCancelled
	}
	n := len(v.holders)
	if n > detail.Event.MaxTicketsPerOrder {
		return entity.Admission{}, nil, 0, inbound.ErrQuantityExceeds
	}
	if n > admission.OutstandingUnits {
		return entity.Admission{}, nil, 0, inbound.ErrQuantityInvalid
	}

	prices := make(map[string]int64, len(detail.Categories))
	for _, c := range detail.Categories {
		prices[c.Category.ID] = c.Category.PriceMinor
	}
	seenCat := map[string]bool{}
	lines := make([]outbound.HoldLine, 0, n)
	var amount int64
	for _, h := range v.holders {
		price, ok := prices[h.CategoryID]
		if !ok {
			return entity.Admission{}, nil, 0, inbound.CategoryUnavailable([]string{h.CategoryID})
		}
		seenCat[h.CategoryID] = true
		h.UnitPriceMinor = price
		lines = append(lines, h)
		amount += price
	}
	if !detail.Event.AllowMixedCategories && len(seenCat) > 1 {
		return entity.Admission{}, nil, 0, inbound.ErrMixedNotAllowed
	}
	return admission, lines, amount, nil
}

func (uc *Hold) replay(rec outbound.IdempotencyRecord, hash []byte) (dto.Booking, error) {
	if !bytesEqual(rec.RequestHash, hash) {
		return dto.Booking{}, inbound.ErrIdemReused
	}
	if rec.State != "COMPLETED" {
		// Another request with the same key is still in flight; treat as transient so the client retries.
		return dto.Booking{}, inbound.ErrDependency
	}
	var b dto.Booking
	if err := json.Unmarshal(rec.ResponseBody, &b); err != nil {
		return dto.Booking{}, inbound.ErrDependency
	}
	return b, nil
}

func validateIdempotencyKey(k string) error {
	if len(k) < 16 || len(k) > 64 {
		return inbound.Validation("Idempotency-Key", "Idempotency-Key harus 16..64 karakter")
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// holdItemViews maps each hold line to its BookingItemView, attaching the DB-generated booking_items.id.
// Precondition: itemIDs[i] is the id created for lines[i] (same request-line order), so len(itemIDs) must
// equal len(lines); a shorter slice leaves later items with an empty ItemID (defensive — the UoW guarantees
// the lengths match).
func holdItemViews(lines []outbound.HoldLine, itemIDs []string) []dto.BookingItemView {
	out := make([]dto.BookingItemView, 0, len(lines))
	for i, l := range lines {
		var id string
		if i < len(itemIDs) {
			id = itemIDs[i]
		}
		out = append(out, dto.BookingItemView{
			ItemID:            id,
			CategoryID:        l.CategoryID,
			AttendeeNIKMasked: l.AttendeeNIKMasked,
			AttendeeName:      l.AttendeeName,
		})
	}
	return out
}
