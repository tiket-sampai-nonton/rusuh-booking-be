package usecase

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// JoinDeps groups the collaborators of the Join usecase (keeps the constructor small).
type JoinDeps struct {
	Store        outbound.IQueueStore
	Settings     outbound.IEventSettingsRepo
	Booking      outbound.IBookingClient
	Limiter      outbound.IRateLimiter
	Tokens       outbound.ITokenIssuer
	UserVerifier outbound.IUserTokenVerifier
	PII          outbound.IPIIProtector
	Audit        outbound.IEntryAuditRepo
	Clock        clock.IClock
}

// JoinSettings holds the rate-limit and claim-window tuning read from config.
type JoinSettings struct {
	JoinPerEmail       int
	JoinPerEmailWindow time.Duration
	JoinPerIP          int
	JoinPerIPWindow    time.Duration
	ClaimWindow        time.Duration
	StatusURLTemplate  string // e.g. "/api/v1/queue-status/"
}

// JoinQueue implements inbound.IJoinUsecase (design §2.1, §8.1).
type JoinQueue struct {
	d JoinDeps
	s JoinSettings
}

// NewJoinQueue accepts interfaces and returns the concrete usecase.
func NewJoinQueue(d JoinDeps, s JoinSettings) *JoinQueue {
	return &JoinQueue{d: d, s: s}
}

// Join validates, resolves auth, rate-limits, joins atomically, then attempts direct admission.
func (u *JoinQueue) Join(ctx context.Context, in inbound.JoinInput) (dto.QueueEntry, error) {
	now := u.d.Clock.Now()

	if err := validateJoinBody(in.Body); err != nil {
		return dto.QueueEntry{}, err
	}

	nikHashHex, accountID, nikMasked, err := u.resolveIdentity(in, now)
	if err != nil {
		return dto.QueueEntry{}, err
	}

	if err := u.rateLimit(ctx, in); err != nil {
		return dto.QueueEntry{}, err
	}

	emailHash := hex.EncodeToString(u.d.PII.HashEmail(strings.ToLower(in.Body.Email)))
	emailEnc, err := u.d.PII.SealEmail(strings.ToLower(in.Body.Email))
	if err != nil {
		return dto.QueueEntry{}, err
	}

	settings, found, err := u.d.Settings.Get(ctx, in.EventID)
	if err != nil {
		return dto.QueueEntry{}, err
	}
	if !found {
		// First join for this event: only a real event (known to Booking) gets queue settings; unknown ids are refused.
		if _, err := u.d.Booking.Supply(ctx, in.EventID); err != nil {
			return dto.QueueEntry{}, err
		}
		if settings, err = u.d.Settings.Ensure(ctx, in.EventID); err != nil {
			return dto.QueueEntry{}, err
		}
	}

	visitorID := in.VisitorID
	if visitorID == "" {
		visitorID = valueobject.NewUUID()
	}
	entryID := valueobject.NewUUID()

	res, err := u.d.Store.Join(ctx, outbound.JoinParams{
		EventID:             in.EventID,
		EntryID:             entryID,
		VisitorID:           visitorID,
		NikHash:             nikHashHex,
		EmailHashHex:        emailHash,
		EmailEnc:            emailEnc,
		NikMasked:           nikMasked,
		AccountID:           accountID,
		NowMs:               now.UnixMilli(),
		OneActivePerVisitor: settings.OneActivePerVisitor,
		OneActivePerEmail:   settings.OneActivePerEmail,
	})
	if err != nil {
		return dto.QueueEntry{}, err
	}

	switch res.Outcome {
	case outbound.JoinExistingNIK:
		return dto.QueueEntry{}, entity.ErrNikInQueue
	case outbound.JoinExistingVisit:
		return dto.QueueEntry{}, entity.ErrVisitorInQueue
	case outbound.JoinExistingEmail:
		return dto.QueueEntry{}, entity.ErrEmailInQueue
	case outbound.JoinQuotaExhausted:
		return dto.QueueEntry{}, entity.ErrQuotaExhausted
	}

	ent := entity.Entry{
		ID: entryID, VisitorID: visitorID, AccountID: accountID,
		Seq: valueobject.Seq(res.Seq), Status: valueobject.StatusWaiting, JoinedAtMs: now.UnixMilli(),
	}
	nikBytes, _ := hex.DecodeString(nikHashHex)
	u.audit(ctx, in.EventID, ent, nikBytes, "JOINED", now)

	ent.BuyerNikMasked, ent.AccountID = nikMasked, accountID
	admitted := u.tryDirectAdmission(ctx, in.EventID, &ent, nikHashHex, strings.ToLower(in.Body.Email), settings, now)
	if admitted {
		u.audit(ctx, in.EventID, ent, nikBytes, "ADMITTED", now)
	}

	return u.buildResponse(in, ent, visitorID, now), nil
}

func validateJoinBody(b dto.JoinRequest) error {
	if !b.ConsentNik {
		return entity.ErrConsentNik
	}
	if b.Email == "" || b.EmailConfirmation == "" {
		return entity.ErrEmailMismatch
	}
	if !strings.EqualFold(b.Email, b.EmailConfirmation) {
		return entity.ErrEmailMismatch
	}
	return nil
}

// resolveIdentity returns (nikHashHex, accountID) per the three auth states (design §2.1).
// resolveIdentity returns the NIK hash, the account id, and the masked NIK shown back to the buyer. The masked NIK is
// taken from the login token's nik_masked claim when the NIK is stored on the account.
func (u *JoinQueue) resolveIdentity(in inbound.JoinInput, now time.Time) (nikHash, accountID, nikMasked string, err error) {
	if in.Bearer != "" {
		id, err := u.d.UserVerifier.Verify(in.Bearer)
		if err == nil {
			if id.HasNIK && id.NIKHash != "" {
				return id.NIKHash, id.AccountID, id.NIKMasked, nil // logged-in with stored NIK: body.buyerNik ignored
			}
			// logged-in without NIK: body.buyerNik required
			if err := validateBuyerNik(in.Body.BuyerNik, now); err != nil {
				return "", id.AccountID, "", err
			}
			return hex.EncodeToString(u.d.PII.HashNIK(in.Body.BuyerNik)), id.AccountID, crypto.MaskNIK(in.Body.BuyerNik), nil
		}
		// invalid bearer → treat as guest
	}
	// guest: body.buyerNik required
	if err := validateBuyerNik(in.Body.BuyerNik, now); err != nil {
		return "", "", "", err
	}
	return hex.EncodeToString(u.d.PII.HashNIK(in.Body.BuyerNik)), "", crypto.MaskNIK(in.Body.BuyerNik), nil
}

func validateBuyerNik(nikStr string, now time.Time) error {
	if nikStr == "" {
		return entity.ErrNikRequired
	}
	// Fast structural pre-check (domain layer, stdlib-only).
	if err := valueobject.ValidateNIKFormat(nikStr); err != nil {
		return err
	}
	// Full Kemendagri structural validation: province, kab/kec, birth date, serial (spec 6.R1, platform R1).
	if err := nik.Validate(nikStr, now); err != nil {
		return entity.ErrNikInvalid
	}
	return nil
}

func (u *JoinQueue) rateLimit(ctx context.Context, in inbound.JoinInput) error {
	emailKey := "join:email:" + hex.EncodeToString(u.d.PII.HashEmail(strings.ToLower(in.Body.Email)))
	ok, err := u.d.Limiter.Allow(ctx, emailKey, u.s.JoinPerEmail, u.s.JoinPerEmailWindow)
	if err != nil {
		return err
	}
	if !ok {
		return apperrorRateLimited(u.s.JoinPerEmailWindow)
	}
	if in.ClientIP != "" {
		ok, err := u.d.Limiter.Allow(ctx, "join:ip:"+in.ClientIP, u.s.JoinPerIP, u.s.JoinPerIPWindow)
		if err != nil {
			return err
		}
		if !ok {
			return apperrorRateLimited(u.s.JoinPerIPWindow)
		}
	}
	return nil
}

// tryDirectAdmission reserves + promotes immediately when a slot is free (design §8.1). Best-effort:
// on any dependency hiccup the entry simply stays WAITING for the Promotor.
func (u *JoinQueue) tryDirectAdmission(ctx context.Context, eventID string, ent *entity.Entry, nikHashHex, email string, settings entity.EventSettings, now time.Time) bool {
	if settings.ActiveSlotCapacity <= 0 {
		return false
	}
	active, err := u.d.Store.ActiveCount(ctx, eventID)
	if err != nil || active >= settings.ActiveSlotCapacity {
		return false
	}
	emailEnc, err := u.d.PII.SealBuyerEmail(email)
	if err != nil {
		return false // stays WAITING; the Promotor retries
	}
	resp, err := u.d.Booking.ReserveBatch(ctx, outbound.AllocationBatchRequest{
		EventID: eventID,
		Entries: []outbound.AllocationEntryRequest{{
			QueueEntryID: ent.ID, Seq: int64(ent.Seq), VisitorID: ent.VisitorID, BuyerNikHash: nikHashHex, Units: 1,
			BuyerNikMasked: ent.BuyerNikMasked, BuyerEmailEnc: emailEnc, AccountID: ent.AccountID,
		}},
	})
	if err != nil || len(resp.Allocations) == 0 {
		return false
	}
	a := resp.Allocations[0]
	if a.Status != "GRANTED" {
		return false
	}
	deadline := now.Add(u.s.ClaimWindow)
	if dl, derr := time.Parse(time.RFC3339, a.ClaimDeadline); derr == nil {
		deadline = dl
	}
	promoted, err := u.d.Store.Promote(ctx, eventID,
		[]outbound.PromoteCandidate{{EntryID: ent.ID, AllocationID: a.AllocationID, ClaimDeadlineMs: deadline.UnixMilli()}},
		settings.ActiveSlotCapacity, now.UnixMilli())
	if err != nil || len(promoted) == 0 {
		return false
	}
	ent.Status = valueobject.StatusAdmitted
	ent.AllocationID = a.AllocationID
	ent.AdmittedAtMs = now.UnixMilli()
	ent.ClaimDeadlineMs = deadline.UnixMilli()
	return true
}

func (u *JoinQueue) audit(ctx context.Context, eventID string, ent entity.Entry, nikHash []byte, action string, now time.Time) {
	_ = u.d.Audit.Append(ctx, outbound.AuditRecord{
		EventID: eventID, EntryID: ent.ID, Seq: ent.Seq.Int64(), BuyerNikHash: nikHash,
		Action: action, OccurredAt: now, AccountID: ent.AccountID,
	})
}

func (u *JoinQueue) buildResponse(in inbound.JoinInput, ent entity.Entry, visitorID string, now time.Time) dto.QueueEntry {
	resp := mapper.ToQueueEntry(ent, now)
	resp.StatusURL = u.s.StatusURLTemplate + in.EventID
	if qt, err := u.d.Tokens.IssueQueue(ent.ID, visitorID, now); err == nil {
		resp.QueueToken = qt
	}
	// A freshly-minted visitor gets a visitor token. The handler returns it in the body for mobile,
	// or moves it into an HttpOnly cookie for web.
	if in.VisitorID == "" {
		if vt, err := u.d.Tokens.IssueVisitor(visitorID, now); err == nil {
			resp.VisitorToken = vt
		}
	}
	return resp
}
