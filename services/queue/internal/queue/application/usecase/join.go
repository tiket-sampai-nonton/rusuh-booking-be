package usecase

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

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
	Audit        outbound.IEntryAuditRepo
	Clock        clock.IClock
	// Sleep waits d or until ctx ends; Jitter returns the wait before the single lease retry. Both are injectable so
	// tests never really wait. Zero values get defaults (a ctx-aware timer and 15-35 ms).
	Sleep  func(ctx context.Context, d time.Duration) error
	Jitter func() time.Duration
}

// JoinSettings holds the rate-limit and claim-window tuning read from config.
type JoinSettings struct {
	JoinPerIP         int
	JoinPerIPWindow   time.Duration
	ClaimWindow       time.Duration
	StatusURLTemplate string // e.g. "/api/v1/queue-status/"
	// AdmissionLeaseTTL is the TTL of the per-event admission lease taken around reserve+promote (default 10s).
	AdmissionLeaseTTL time.Duration
	// VisitorTokenTTL is the lifetime of the visitor token and of its server-side generation record.
	VisitorTokenTTL time.Duration
}

// JoinQueue implements inbound.IJoinUsecase (design §2.1, §8.1).
type JoinQueue struct {
	d       JoinDeps
	s       JoinSettings
	metrics outbound.IPromoterMetrics
	logger  *slog.Logger
}

type noopMetrics struct{}

func (noopMetrics) Inc(string, ...string) {}

// NewJoinQueue accepts interfaces and returns the concrete usecase. Zero-valued tuning gets defaults here so the
// composition root does not need to change until the config is wired.
func NewJoinQueue(d JoinDeps, s JoinSettings) *JoinQueue {
	if s.AdmissionLeaseTTL <= 0 {
		s.AdmissionLeaseTTL = 10 * time.Second
	}
	if d.Sleep == nil {
		d.Sleep = sleepCtx
	}
	if d.Jitter == nil {
		d.Jitter = defaultJitter
	}
	return &JoinQueue{d: d, s: s, metrics: noopMetrics{}, logger: slog.Default()}
}

// WithMetrics sets the counter sink (default: no-op).
func (u *JoinQueue) WithMetrics(m outbound.IPromoterMetrics) *JoinQueue {
	if m != nil {
		u.metrics = m
	}
	return u
}

// WithLogger sets the logger (default: slog.Default()).
func (u *JoinQueue) WithLogger(l *slog.Logger) *JoinQueue {
	if l != nil {
		u.logger = l
	}
	return u
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// defaultJitter returns 15-35 ms: long enough for an idle promoter tick (it holds the lease only across a few
// Valkey reads and no Booking call) to finish, far below BOOKING_TIMEOUT.
func defaultJitter() time.Duration {
	return 15*time.Millisecond + time.Duration(rand.Int64N(int64(20*time.Millisecond)))
}

// defaultAdmissionUnits is the quantity guarantee per admission (decision 3): Booking reserves this many units
// for the event with no buyer NIK and clamps it to the event's max_tickets_per_order and to free stock.
const defaultAdmissionUnits = 3

// Join resolves the principal, rate-limits softly, joins atomically and then attempts direct admission. The
// request carries no NIK, email or consent (ADR-030); a bearer only identifies the account.
func (u *JoinQueue) Join(ctx context.Context, in inbound.JoinInput) (dto.QueueEntry, error) {
	now := u.d.Clock.Now()

	accountID := u.resolveAccount(in)

	if err := u.rateLimit(ctx, in); err != nil {
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

	res, err := u.d.Store.Join(ctx, outbound.JoinParams{
		EventID:             in.EventID,
		EntryID:             valueobject.NewUUID(),
		NewVisitorID:        valueobject.NewUUID(),
		TokenVisitorID:      in.VisitorID,
		AccountID:           accountID,
		IdemKey:             in.IdempotencyKey,
		NowMs:               now.UnixMilli(),
		IdemTTL:             10 * time.Minute,
		OneActivePerVisitor: settings.OneActivePerVisitor,
		TokenGen:            in.TokenGen,
		TokenTTL:            u.s.VisitorTokenTTL,
	})
	if err != nil {
		return dto.QueueEntry{}, err
	}
	if res.Outcome == outbound.JoinAccountInQueue {
		return dto.QueueEntry{}, entity.ErrAccountInQueue
	}

	ent := entity.Entry{
		ID: res.EntryID, VisitorID: res.VisitorID, AccountID: accountID,
		Seq: valueobject.Seq(res.Seq), Status: valueobject.Status(res.Status), JoinedAtMs: now.UnixMilli(),
	}
	if res.Outcome == outbound.JoinCreated {
		u.audit(ctx, in.EventID, ent, "JOINED", now)
		if u.tryDirectAdmission(ctx, in.EventID, &ent, settings, now) {
			u.audit(ctx, in.EventID, ent, "ADMITTED", now)
		}
	} else if cur, ok, gerr := u.d.Store.GetEntry(ctx, in.EventID, res.EntryID); gerr == nil && ok {
		// An existing entry is returned as it is now (any state), never re-admitted here.
		ent = cur
	}

	out := u.buildResponse(in, ent, outbound.VisitorClaim{VisitorID: res.VisitorID, Gen: res.Gen, DeviceHash: in.DeviceHash}, now)
	out.Existing = res.Outcome != outbound.JoinCreated
	return out, nil
}

// resolveAccount returns the account id of a valid User bearer, or "" for a guest or an invalid bearer. The
// bearer's NIK claims are ignored: the account is an identity for dedup only.
func (u *JoinQueue) resolveAccount(in inbound.JoinInput) string {
	if in.Bearer == "" || u.d.UserVerifier == nil {
		return ""
	}
	id, err := u.d.UserVerifier.Verify(in.Bearer)
	if err != nil {
		return "" // invalid bearer: treated as a guest
	}
	return id.AccountID
}

// rateLimit applies the SOFT per-IP cap (mobile carriers share NAT addresses, so it is a brake, never a block of
// a person). The per-email limit is gone with the email.
func (u *JoinQueue) rateLimit(ctx context.Context, in inbound.JoinInput) error {
	if in.ClientIP == "" {
		// Not rejected (a proxy misconfiguration must not stop sales) but visible: the per-IP limit is skipped.
		u.logger.Warn("join dengan IP klien kosong; batas per-IP dilewati", "eventId", in.EventID)
		return nil
	}
	ok, err := u.d.Limiter.Allow(ctx, "join:ip:"+in.ClientIP, u.s.JoinPerIP, u.s.JoinPerIPWindow)
	if err != nil {
		return err
	}
	if !ok {
		return apperrorRateLimited(u.s.JoinPerIPWindow)
	}
	return nil
}

// tryDirectAdmission reserves + promotes immediately when a slot is free and the entry is the head (design §8.1,
// §5.4a). Best-effort: on any dependency hiccup the entry simply stays WAITING for the promoter.
//
// Order (rule 1): cheap lock-free pre-checks -> promoter lease (one jittered retry) -> re-check under the lease ->
// ReserveBatch -> Promote(RequireHead, LeaseToken) -> release the lease. Capacity and head are verified BEFORE any
// Booking call, so an ordinary deferral (slot busy, not head, lease busy) leaves no RESERVED row that could later
// age into a permanent ALREADY_ACTIVE for a live buyer.
func (u *JoinQueue) tryDirectAdmission(ctx context.Context, eventID string, ent *entity.Entry, settings entity.EventSettings, now time.Time) bool {
	if settings.ActiveSlotCapacity <= 0 {
		return false
	}
	if !u.slotAndHeadOK(ctx, eventID, ent.ID, settings.ActiveSlotCapacity) {
		return false // pre-check without a lock; no Booking call, no lease
	}
	token, ok := u.acquireAdmissionLease(ctx, eventID)
	if !ok {
		return false
	}
	defer func() {
		// compare-and-delete by token; must run even if the request context was cancelled mid-flight
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := u.d.Store.ReleasePromoterLease(rctx, eventID, token); err != nil {
			u.logger.Warn("lease admisi tidak bisa dilepas; kedaluwarsa lewat TTL", "eventId", eventID, "error", err)
		}
	}()
	// Re-check under the lease: between the pre-check and now another path may have taken the slot or a lower seq
	// may have appeared. This is what makes FULL/NOT_HEAD after a grant unreachable (L1).
	if !u.slotAndHeadOK(ctx, eventID, ent.ID, settings.ActiveSlotCapacity) {
		return false
	}
	resp, err := u.d.Booking.ReserveBatch(ctx, outbound.AllocationBatchRequest{
		EventID: eventID,
		Entries: []outbound.AllocationEntryRequest{{
			QueueEntryID: ent.ID, Seq: int64(ent.Seq), VisitorID: ent.VisitorID, Units: defaultAdmissionUnits,
			AccountID: ent.AccountID,
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
	if !deadline.After(now) {
		// Lapsed replay (rule 3a): never hand the buyer a turn whose Booking deadline has already passed.
		return false
	}
	pres, err := u.d.Store.Promote(ctx, eventID,
		[]outbound.PromoteCandidate{{EntryID: ent.ID, AllocationID: a.AllocationID, ClaimDeadlineMs: deadline.UnixMilli()}},
		settings.ActiveSlotCapacity, now.UnixMilli(), outbound.PromoteOpts{RequireHead: true, LeaseToken: token})
	if err != nil {
		return false // possibly committed, possibly not: never release; the RESERVED row is replayed by the promoter
	}
	if pres.LeaseLost {
		u.metrics.Inc(outbound.MetricLeaseLost)
		return false
	}
	if pres.Full {
		u.metrics.Inc(outbound.MetricFullAfterGrant)
		u.logger.Error("Promote FULL setelah alokasi diberikan pada admisi langsung; baris dipertahankan", "eventId", eventID, "entryId", ent.ID)
		return false
	}
	outbound.ReleaseTerminal(ctx, u.d.Booking, u.logger, pres.Skipped, map[string]string{ent.ID: a.AllocationID})
	if len(pres.Promoted) == 0 {
		return false
	}
	ent.Status = valueobject.StatusAdmitted
	ent.AllocationID = a.AllocationID
	ent.AdmittedAtMs = now.UnixMilli()
	ent.ClaimDeadlineMs = deadline.UnixMilli()
	return true
}

// slotAndHeadOK reports whether a slot is free and the entry is the head of waiting (strict FIFO).
func (u *JoinQueue) slotAndHeadOK(ctx context.Context, eventID, entryID string, capacity int) bool {
	active, err := u.d.Store.ActiveCount(ctx, eventID)
	if err != nil || active >= capacity {
		return false
	}
	head, err := u.d.Store.TopWaiting(ctx, eventID, 1)
	return err == nil && len(head) == 1 && head[0] == entryID
}

// acquireAdmissionLease takes the per-event promoter lease. If it is held it waits once (jittered, 15-35 ms) and
// tries once more; still held -> false and the entry stays WAITING with no Booking call (REQ-QSH-03.2 amendment).
func (u *JoinQueue) acquireAdmissionLease(ctx context.Context, eventID string) (string, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		token, acquired, err := u.d.Store.AcquirePromoterLease(ctx, eventID, u.s.AdmissionLeaseTTL)
		if err != nil {
			return "", false
		}
		if acquired {
			return token, true
		}
		if attempt == 0 {
			u.metrics.Inc(outbound.MetricJoinLeaseRetry)
			if err := u.d.Sleep(ctx, u.d.Jitter()); err != nil {
				return "", false
			}
		}
	}
	return "", false
}

func (u *JoinQueue) audit(ctx context.Context, eventID string, ent entity.Entry, action string, now time.Time) {
	_ = u.d.Audit.Append(ctx, outbound.AuditRecord{
		EventID: eventID, EntryID: ent.ID, Seq: ent.Seq.Int64(),
		Action: action, OccurredAt: now, AccountID: ent.AccountID,
	})
}

func (u *JoinQueue) buildResponse(in inbound.JoinInput, ent entity.Entry, claim outbound.VisitorClaim, now time.Time) dto.QueueEntry {
	resp := mapper.ToQueueEntry(ent, now)
	resp.StatusURL = u.s.StatusURLTemplate + in.EventID
	if qt, err := u.d.Tokens.IssueQueue(ent.ID, claim.VisitorID, now); err == nil {
		resp.QueueToken = qt
	}
	// Every join answers with a visitor token for the CURRENT generation (R01.4): after a rotation the earlier token is
	// revoked, so the caller must receive the new one. The handler returns it in the body for mobile, or moves it into
	// an HttpOnly cookie for web.
	if vt, err := u.d.Tokens.IssueVisitor(claim, now); err == nil {
		resp.VisitorToken = vt
	}
	return resp
}
