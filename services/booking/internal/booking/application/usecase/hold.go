package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

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

// Hold performs the atomic hold: validate → idempotency guard → UoW.Hold (one tx, lock ordered). It
// enforces invariant #1 (no oversell) via the DB CHECKs inside the unit of work.
type Hold struct {
	admissions outbound.IAdmissionRepo
	events     outbound.IEventRepo
	idem       outbound.IIdempotencyStore
	uow        outbound.IUnitOfWork
	nik        vo.NIKProtector
	ids        IDGen
	clock      clock.IClock
	idemTTL    time.Duration
}

// NewHold wires the hold usecase.
func NewHold(admissions outbound.IAdmissionRepo, events outbound.IEventRepo, idem outbound.IIdempotencyStore,
	uow outbound.IUnitOfWork, nik vo.NIKProtector, ids IDGen, clk clock.IClock) *Hold {
	return &Hold{
		admissions: admissions, events: events, idem: idem, uow: uow,
		nik: nik, ids: ids, clock: clk, idemTTL: 24 * time.Hour,
	}
}

// Hold runs the full hold flow and returns the created booking.
func (uc *Hold) Hold(ctx context.Context, cmd inbound.HoldCommand) (dto.Booking, error) {
	if err := validateIdempotencyKey(cmd.IdempotencyKey); err != nil {
		return dto.Booking{}, err
	}
	admission, lines, amount, err := uc.decide(ctx, cmd)
	if err != nil {
		return dto.Booking{}, err
	}

	hash := requestHash(cmd.Request)
	rec, found, err := uc.idem.Begin(ctx, idemScopeHold, cmd.VisitorID, cmd.IdempotencyKey, hash, uc.idemTTL)
	if err != nil {
		return dto.Booking{}, err
	}
	if found {
		return uc.replay(rec, hash)
	}

	now := uc.clock.Now()
	spec := outbound.HoldSpec{
		BookingID:        uc.ids.NewID(),
		PublicRef:        uc.ids.NewPublicRef(),
		EventID:          admission.EventID,
		AdmissionID:      admission.ID,
		VisitorID:        cmd.VisitorID,
		BuyerNIKHash:     admission.BuyerNIKHash,
		BuyerEmail:       admission.BuyerEmail,
		AccountID:        admission.AccountID,
		AmountMinor:      amount,
		PaymentExpiresAt: now.Add(time.Duration(admission.CfgPaymentTTLSeconds) * time.Second),
		Lines:            lines,
		CorrelationID:    cmd.CorrelationID,
		CfgEditEnabled:   admission.CfgEditEnabled,
		AdmissionUnits:   admission.Units,
	}
	res, err := uc.uow.Hold(ctx, spec)
	if err != nil {
		return dto.Booking{}, err
	}

	booking := dto.Booking{
		BookingID:        res.BookingID,
		PublicRef:        res.PublicRef,
		Status:           res.Status,
		AmountMinor:      res.AmountMinor,
		Currency:         vo.Currency,
		PaymentExpiresAt: res.PaymentExpiresAt,
		ServerTime:       now,
		Version:          res.Version,
		CanEdit:          admission.CanEdit(),
		EditsRemaining:   admission.EditsRemaining(),
		Items:            holdItemViews(lines),
	}
	body, _ := json.Marshal(booking)
	if cerr := uc.idem.Complete(ctx, idemScopeHold, cmd.VisitorID, cmd.IdempotencyKey, 201, body); cerr != nil {
		return dto.Booking{}, cerr
	}
	return booking, nil
}

// decide performs the pure validation and derives the hold lines + amount. It reads the admission
// (session) and the event detail (categories + prices + config) but performs no stock mutation.
func (uc *Hold) decide(ctx context.Context, cmd inbound.HoldCommand) (entity.Admission, []outbound.HoldLine, int64, error) {
	req := cmd.Request
	if !isUUID(req.SessionID) {
		return entity.Admission{}, nil, 0, inbound.Validation("sessionId", "sessionId harus UUID")
	}
	if len(req.Items) == 0 {
		return entity.Admission{}, nil, 0, inbound.Validation("items", "minimal satu item")
	}

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
	if len(req.Items) > detail.Event.MaxTicketsPerOrder {
		return entity.Admission{}, nil, 0, inbound.ErrQuantityExceeds
	}

	prices := make(map[string]int64, len(detail.Categories))
	for _, c := range detail.Categories {
		prices[c.Category.ID] = c.Category.PriceMinor
	}

	seenCat := map[string]bool{}
	seenNIK := map[string]bool{}
	lines := make([]outbound.HoldLine, 0, len(req.Items))
	var amount int64
	for _, it := range req.Items {
		if !isUUID(it.CategoryID) {
			return entity.Admission{}, nil, 0, inbound.Validation("categoryId", "categoryId harus UUID")
		}
		price, ok := prices[it.CategoryID]
		if !ok {
			return entity.Admission{}, nil, 0, inbound.ErrCategoryUnavailable
		}
		seenCat[it.CategoryID] = true
		name := it.Attendee.FullName
		if len(name) < 2 || len(name) > 100 {
			return entity.Admission{}, nil, 0, inbound.Validation("fullName", "fullName harus 2..100 karakter")
		}
		if err := pkgnik.Validate(it.Attendee.NIK, uc.clock.Now()); err != nil {
			return entity.Admission{}, nil, 0, inbound.ErrNIKInvalid
		}
		nik, perr := vo.ParseNIK(it.Attendee.NIK, uc.nik)
		if perr != nil {
			return entity.Admission{}, nil, 0, inbound.ErrNIKInvalid
		}
		key := string(nik.Hash())
		if seenNIK[key] {
			return entity.Admission{}, nil, 0, inbound.ErrNikDuplicate
		}
		seenNIK[key] = true
		lines = append(lines, outbound.HoldLine{
			CategoryID:        it.CategoryID,
			UnitPriceMinor:    price,
			AttendeeNIKHash:   nik.Hash(),
			AttendeeNIKEnc:    nik.Enc(),
			AttendeeNIKMasked: nik.Masked(),
			AttendeeName:      name,
		})
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

func requestHash(req dto.HoldRequest) []byte {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return sum[:]
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

func holdItemViews(lines []outbound.HoldLine) []dto.BookingItemView {
	out := make([]dto.BookingItemView, 0, len(lines))
	for _, l := range lines {
		out = append(out, dto.BookingItemView{
			CategoryID:        l.CategoryID,
			AttendeeNIKMasked: l.AttendeeNIKMasked,
			AttendeeName:      l.AttendeeName,
		})
	}
	return out
}
