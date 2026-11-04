// Package worker holds the Queue background jobs: Promotor, Sweeper, Status Publisher (design §7).
// Each Run is idempotent per tick so it can be driven by the shared worker loop.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/compensation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Counter names live in the outbound port (the one home shared with the join usecase); these aliases keep the worker
// code and its tests short.
const (
	MetricFullAfterGrant = outbound.MetricFullAfterGrant
	MetricLeaseLost      = outbound.MetricLeaseLost
	MetricEntryEnded     = outbound.MetricEntryEnded
	MetricLapsedReplay   = outbound.MetricLapsedReplay
	MetricFifoAdopted    = outbound.MetricFifoAdopted
	MetricHeadNoSupply   = outbound.MetricHeadNoSupply
	MetricUnknownOutcome = outbound.MetricUnknownOutcome
	MetricJoinLeaseRetry = outbound.MetricJoinLeaseRetry
	MetricUnsealFailed   = outbound.MetricUnsealFailed
)

// entryUnits is the number of units the Queue requests per entry. Jumper adoption (design §5.4) is only correct for
// Units == 1, so a test pins this constant.
const entryUnits = 1

// Promoter promotes waiting entries to active after Booking reserves their allocation (design §7.1).
// It never admits an entry that is not RESERVED ("Jangan PHP" invariant).
type Promoter struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	booking  outbound.IBookingClient
	pub      outbound.IEventPublisher
	audit    outbound.IEntryAuditRepo
	pii      outbound.IPIIProtector
	clock    clock.IClock
	logger   *slog.Logger
	tuning   PromoterTuning
	metrics  outbound.IPromoterMetrics

	mu   sync.Mutex
	recs map[string]*headRec // per-event poison-head record (C8); detection only
}

// PromoterTuning holds the promoter knobs. Zero values keep today's behaviour (PD-2).
type PromoterTuning struct {
	BatchMax             int           // max candidates per tick (default 200)
	LeaseTTL             time.Duration // promoter lease TTL (default 10s)
	UnsealFailMax        int           // consecutive failed ticks before an unreadable-PII head is ended (default 3)
	UnsealProbe          int           // extra ids probed to tell an entry-scoped from a systemic unseal failure (default 3)
	NoSupplySuspectTicks int           // head NO_SUPPLY_NOW ticks before it is labelled suspect (default 50, min 2)
}

// NewPromoter accepts interfaces and returns the concrete worker.
func NewPromoter(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, booking outbound.IBookingClient, pub outbound.IEventPublisher, audit outbound.IEntryAuditRepo, pii outbound.IPIIProtector, clk clock.IClock, logger *slog.Logger, tuning PromoterTuning) *Promoter {
	if tuning.BatchMax <= 0 {
		tuning.BatchMax = 200
	}
	if tuning.LeaseTTL <= 0 {
		tuning.LeaseTTL = 10 * time.Second
	}
	if tuning.UnsealFailMax <= 0 {
		tuning.UnsealFailMax = 3
	}
	if tuning.UnsealProbe <= 0 {
		tuning.UnsealProbe = 3
	}
	if tuning.NoSupplySuspectTicks < 2 {
		tuning.NoSupplySuspectTicks = 50
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Promoter{store: store, settings: settings, booking: booking, pub: pub, audit: audit, pii: pii, clock: clk, logger: logger, tuning: tuning, metrics: noopMetrics{}, recs: map[string]*headRec{}}
}

// WithMetrics sets the counter sink (default: no-op).
func (p *Promoter) WithMetrics(m outbound.IPromoterMetrics) *Promoter {
	if m != nil {
		p.metrics = m
	}
	return p
}

// Run promotes one batch for every configured event. A failing event never stops the others: the errors are joined.
func (p *Promoter) Run(ctx context.Context) error {
	ids, err := p.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, eventID := range ids {
		if err := p.runEvent(ctx, eventID); err != nil {
			errs = append(errs, fmt.Errorf("promoter event %s: %w", eventID, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Promoter) inc(name string, labels ...string) { p.metrics.Inc(name, labels...) }

// runEvent runs one promoter tick for an event under the per-event lease (design §5.4a, ADR-028).
func (p *Promoter) runEvent(ctx context.Context, eventID string) error {
	token, acquired, err := p.store.AcquirePromoterLease(ctx, eventID, p.tuning.LeaseTTL)
	if err != nil {
		return err
	}
	if !acquired {
		return nil // another instance is promoting this event this tick
	}
	// The release is compare-and-delete by token and must run even when ctx was cancelled mid-tick.
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if rerr := p.store.ReleasePromoterLease(rctx, eventID, token); rerr != nil {
			p.logger.Warn("lease promoter tidak bisa dilepas; kedaluwarsa lewat TTL", "eventId", eventID, "error", rerr)
		}
	}()
	// Run bound: stop working before the lease can lapse, so a slow Booking cannot outlive the lease (the fence in
	// promote.lua is the second line of defence).
	bound := p.tuning.LeaseTTL - time.Second
	if bound <= 0 {
		bound = p.tuning.LeaseTTL / 2
	}
	runCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	settings, ok, err := p.settings.Get(runCtx, eventID)
	if err != nil || !ok {
		return err
	}
	// Pre-turn notifications are attempted on every return path from here on (TC-QUE-31); a failure never changes the
	// result of the tick (INV-6).
	if settings.PreTurnNotifyEnabled && settings.NearFrontMultiplier > 0 {
		defer p.notifyPreTurn(runCtx, eventID, settings.ActiveSlotCapacity*settings.NearFrontMultiplier)
	}
	// free and the candidate list are read under the lease, and the same capacity is passed to Promote (L1).
	active, err := p.store.ActiveCount(runCtx, eventID)
	if err != nil {
		return err
	}
	free := settings.ActiveSlotCapacity - active
	if free <= 0 {
		return nil
	}
	if free > p.tuning.BatchMax {
		free = p.tuning.BatchMax
	}
	candidates, err := p.store.TopWaiting(runCtx, eventID, free)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		p.dropRec(eventID) // C8 row 0
		return nil
	}
	headID := candidates[0]
	now := p.clock.Now()

	entries, visitorByEntry := p.buildEntries(runCtx, eventID, candidates, free, now)
	if len(entries) == 0 {
		return nil
	}
	resp, err := p.booking.ReserveBatch(runCtx, outbound.AllocationBatchRequest{EventID: eventID, Entries: entries})
	if err != nil {
		return err // Booking down: entries stay waiting, retry next tick (record untouched, C8 row 2)
	}

	out := p.classify(runCtx, eventID, resp, now)

	if len(out.promoteCands) == 0 {
		p.observeHead(eventID, headID, out, nil)
		return nil
	}
	res, err := p.store.Promote(runCtx, eventID, out.promoteCands, settings.ActiveSlotCapacity, now.UnixMilli(), outbound.PromoteOpts{LeaseToken: token})
	if err != nil {
		// Possibly committed (ambiguous timeout) or not: never release. The RESERVED rows are replayed next tick.
		return err
	}
	if res.LeaseLost {
		p.inc(MetricLeaseLost)
		p.logger.Warn("lease promoter hilang sebelum Promote; tidak ada yang dipromosikan", "eventId", eventID)
		return nil
	}
	if res.Full {
		// L1 says this is unreachable under a valid lease: a defect signal. Keep the rows (never release).
		p.inc(MetricFullAfterGrant)
		p.logger.Error("Promote FULL setelah alokasi diberikan; baris RESERVED dipertahankan untuk replay", "eventId", eventID)
		return nil
	}
	compensation.ReleaseTerminal(runCtx, p.booking, p.logger, res.Skipped, out.grantedAlloc)

	promoted := map[string]bool{}
	for _, entryID := range res.Promoted {
		promoted[entryID] = true
		_ = p.pub.PublishTurnArrived(runCtx, eventID, entryID, visitorByEntry[entryID], time.UnixMilli(out.deadlineByEntry[entryID]).UTC())
	}
	p.observeHead(eventID, headID, out, promoted)
	return nil
}

// batchOutcome is the promoter's reading of one ReserveBatch answer.
type batchOutcome struct {
	promoteCands    []outbound.PromoteCandidate
	grantedAlloc    map[string]string // entryID -> allocation id granted in this attempt
	deadlineByEntry map[string]int64
	replyByEntry    map[string]string
	// laterGrantAfterNoSupply holds, in order, the GRANTED entries that follow a NO_SUPPLY_NOW in the batch.
	adoptedAfterNoSupply []string
}

// classify applies the per-outcome rules of design §5.4 to a ReserveBatch answer. It never releases an allocation.
func (p *Promoter) classify(ctx context.Context, eventID string, resp outbound.AllocationBatchResponse, now time.Time) batchOutcome {
	out := batchOutcome{grantedAlloc: map[string]string{}, deadlineByEntry: map[string]int64{}, replyByEntry: map[string]string{}}
	sawNoSupply := false
	for _, a := range resp.Allocations {
		out.replyByEntry[a.QueueEntryID] = a.Status
		switch a.Status {
		case "GRANTED":
			deadline := now.Add(time.Minute)
			if dl, perr := time.Parse(time.RFC3339, a.ClaimDeadline); perr == nil {
				deadline = dl
			}
			if !deadline.After(now) {
				// Lapsed replay (rule 3a, C7): skip with no mutation and no FIFO flag; never release.
				p.inc(MetricLapsedReplay)
				p.logger.Warn("replay GRANTED dengan deadline lampau dilewati; menunggu sweeper Booking", "eventId", eventID, "entryId", a.QueueEntryID)
				continue
			}
			out.promoteCands = append(out.promoteCands, outbound.PromoteCandidate{
				EntryID: a.QueueEntryID, AllocationID: a.AllocationID, ClaimDeadlineMs: deadline.UnixMilli(),
			})
			out.grantedAlloc[a.QueueEntryID] = a.AllocationID
			out.deadlineByEntry[a.QueueEntryID] = deadline.UnixMilli()
			if sawNoSupply {
				// Jumper path: adopt, never release (design §5.4, ADR-028 amendment). The row at Booking is real and
				// releasing it would turn a live WAITING entry into a permanent ALREADY_ACTIVE.
				out.adoptedAfterNoSupply = append(out.adoptedAfterNoSupply, a.QueueEntryID)
			}
		case "BUYER_QUOTA_EXHAUSTED", "QUOTA_EXHAUSTED":
			if ent, found, gerr := p.store.GetEntry(ctx, eventID, a.QueueEntryID); gerr == nil && found {
				_ = p.store.AddQuotaExhausted(ctx, eventID, ent.BuyerNikHashHex())
			}
			// End the entry so it does not permanently block the queue head (spec 11.R1).
			if _, err := p.store.EndEntry(ctx, eventID, a.QueueEntryID, outbound.EndQuotaExhaust, now.UnixMilli()); err != nil {
				p.logger.Warn("gagal mengakhiri entry BUYER_QUOTA_EXHAUSTED", "eventId", eventID, "entryId", a.QueueEntryID, "error", err)
			} else {
				p.inc(MetricEntryEnded, "outcome", outbound.EndQuotaExhaust)
			}
		case "SOLD_OUT":
			_ = p.store.SetSale(ctx, eventID, "CLOSED", true)
		case "NO_SUPPLY_NOW":
			// No row at Booking and nothing to release. It is not always a stock event (C8), so the Queue never ends
			// the entry on it; it stays WAITING and is retried next tick.
			sawNoSupply = true
		case "ALREADY_ACTIVE":
			// The Booking row for this WAITING entry is dead (RELEASED/EXPIRED) and Booking can never grant it again
			// (§1.1): terminal by construction, so end it instead of letting it block the head (T2).
			if _, err := p.store.EndEntry(ctx, eventID, a.QueueEntryID, outbound.EndAlreadyActive, now.UnixMilli()); err != nil {
				p.logger.Warn("gagal mengakhiri entry ALREADY_ACTIVE", "eventId", eventID, "entryId", a.QueueEntryID, "error", err)
			} else {
				p.inc(MetricEntryEnded, "outcome", outbound.EndAlreadyActive)
			}
			p.logger.Warn("ALREADY_ACTIVE dari Booking untuk entry WAITING; baris Booking sudah mati",
				"eventId", eventID, "entryId", a.QueueEntryID)
		default:
			p.inc(MetricUnknownOutcome)
			p.logger.Warn("outcome Booking tak dikenal; tidak ada mutasi", "eventId", eventID, "entryId", a.QueueEntryID, "outcome", a.Status)
		}
	}
	return out
}

// buildEntries reads each candidate and prepares the Booking request. Orphans are ended at once (C5); entries whose
// email cannot be opened follow C6 (entry-scoped vs systemic).
func (p *Promoter) buildEntries(ctx context.Context, eventID string, candidates []string, free int, now time.Time) ([]outbound.AllocationEntryRequest, map[string]string) {
	entries := make([]outbound.AllocationEntryRequest, 0, len(candidates))
	visitorByEntry := map[string]string{}
	var bad []string
	ok := 0
	for _, entryID := range candidates {
		ent, found, gerr := p.store.GetEntry(ctx, eventID, entryID)
		if gerr != nil {
			p.logger.Warn("GetEntry gagal; entry dilewati tick ini", "eventId", eventID, "entryId", entryID, "error", gerr)
			continue // transient Valkey failure: skipped, never ended
		}
		if !found {
			// C5: an id in waiting without a hash can never be admitted; end it so it stops being the head.
			if _, err := p.store.EndEntry(ctx, eventID, entryID, outbound.EndPromoterOrphan, now.UnixMilli()); err != nil {
				p.logger.Warn("gagal membersihkan id yatim di waiting", "eventId", eventID, "entryId", entryID, "error", err)
			} else {
				p.inc(MetricEntryEnded, "outcome", outbound.EndPromoterOrphan)
			}
			continue
		}
		emailEnc, uerr := p.sealForBooking(ent.EmailEnc)
		if uerr != nil {
			bad = append(bad, entryID)
			continue
		}
		ok++
		visitorByEntry[entryID] = ent.VisitorID
		entries = append(entries, outbound.AllocationEntryRequest{
			QueueEntryID: entryID, VisitorID: ent.VisitorID, BuyerNikHash: ent.BuyerNikHashHex(), Units: entryUnits,
			Seq: int64(ent.Seq), BuyerNikMasked: ent.BuyerNikMasked, BuyerEmailEnc: emailEnc, AccountID: ent.AccountID,
		})
	}
	if len(bad) > 0 {
		p.handleUnreadable(ctx, eventID, candidates, bad, ok, free, now)
	}
	return entries, visitorByEntry
}

func (p *Promoter) sealForBooking(emailEnc []byte) (string, error) {
	email, err := p.pii.UnsealEmail(emailEnc)
	if err != nil {
		return "", err
	}
	return p.pii.SealBuyerEmail(email)
}

// handleUnreadable implements C6. An unseal failure is deterministic per stored blob and key, so retrying does not
// help; the danger is ending the whole queue because a key is mis-set. It is therefore only entry-scoped (a sibling
// or probe id opens fine) failures that count towards ending an entry.
func (p *Promoter) handleUnreadable(ctx context.Context, eventID string, candidates, bad []string, ok, free int, now time.Time) {
	entryScoped := ok > 0
	if !entryScoped {
		entryScoped = p.probeOpens(ctx, eventID, candidates, free)
	}
	if !entryScoped {
		p.inc(MetricUnsealFailed, "scope", "systemic")
		p.logger.Error("tidak ada email antrean yang bisa dibuka; kemungkinan kunci PII salah. Tidak ada entry yang diakhiri", "eventId", eventID)
		return
	}
	for _, entryID := range bad {
		p.inc(MetricUnsealFailed, "scope", "entry")
		n, err := p.store.NoteUnreadable(ctx, eventID, entryID)
		if err != nil {
			p.logger.Warn("NoteUnreadable gagal", "eventId", eventID, "entryId", entryID, "error", err)
			continue
		}
		p.logger.Error("email antrean tidak bisa dibuka; entry dilewati", "eventId", eventID, "entryId", entryID, "count", n)
		if n >= p.tuning.UnsealFailMax {
			if _, err := p.store.EndEntry(ctx, eventID, entryID, outbound.EndUnreadablePII, now.UnixMilli()); err != nil {
				p.logger.Warn("gagal mengakhiri entry UNREADABLE_PII", "eventId", eventID, "entryId", entryID, "error", err)
			} else {
				p.inc(MetricEntryEnded, "outcome", outbound.EndUnreadablePII)
			}
		}
	}
}

// probeOpens reads up to UnsealProbe waiting ids beyond the candidate window (read-only) and reports whether any of
// them opens: if one does the failure is entry-scoped, otherwise it is systemic.
func (p *Promoter) probeOpens(ctx context.Context, eventID string, candidates []string, free int) bool {
	wider, err := p.store.TopWaiting(ctx, eventID, free+p.tuning.UnsealProbe)
	if err != nil {
		return false
	}
	inWindow := map[string]bool{}
	for _, id := range candidates {
		inWindow[id] = true
	}
	probed := 0
	for _, id := range wider {
		if inWindow[id] {
			continue
		}
		if probed >= p.tuning.UnsealProbe {
			break
		}
		probed++
		ent, found, gerr := p.store.GetEntry(ctx, eventID, id)
		if gerr != nil || !found {
			continue
		}
		if _, uerr := p.sealForBooking(ent.EmailEnc); uerr == nil {
			return true
		}
	}
	return false
}

func (p *Promoter) dropRec(eventID string) {
	p.mu.Lock()
	delete(p.recs, eventID)
	p.mu.Unlock()
}

// observeHead updates the C8 record and emits the adoption/repeat counters. It never changes a decision (P20).
// promoted is nil when no Promote call happened (nothing to adopt).
func (p *Promoter) observeHead(eventID, headID string, out batchOutcome, promoted map[string]bool) {
	reply, reached := out.replyByEntry[headID]
	obs := tickObs{headID: headID, reached: reached, noSupply: reply == "NO_SUPPLY_NOW"}
	adopted := 0
	for _, id := range out.adoptedAfterNoSupply {
		if promoted[id] {
			adopted++
		}
	}
	obs.adopted = adopted > 0

	p.mu.Lock()
	next, eff := nextRec(p.recs[eventID], obs, p.tuning.NoSupplySuspectTicks)
	p.recs[eventID] = next
	p.mu.Unlock()

	if adopted > 0 {
		suspect := "false"
		if eff.Suspect {
			suspect = "true"
		}
		for i := 0; i < adopted; i++ {
			p.inc(MetricFifoAdopted, "suspect", suspect)
		}
		p.logger.Warn("entry di belakang NO_SUPPLY_NOW diadopsi (inversi FIFO terbatas)", "eventId", eventID, "adopted", adopted, "headId", headID)
	}
	if eff.Repeat {
		a := "false"
		if eff.RepeatAdopted {
			a = "true"
		}
		p.inc(MetricHeadNoSupply, "adopted", a)
	}
	if eff.Warn {
		p.logger.Warn("head antrean menjawab NO_SUPPLY_NOW berturut-turut", "eventId", eventID, "entryId", headID, "ticks", next.noSupplyTicks)
	}
}

// notifyPreTurn publishes queue.turn.soon for waiting entries near the front (TC-QUE-13, SRS-F-25).
func (p *Promoter) notifyPreTurn(ctx context.Context, eventID string, notifyPosition int) {
	if notifyPosition <= 0 {
		return
	}
	nearFront, err := p.store.TopWaiting(ctx, eventID, notifyPosition)
	if err != nil || len(nearFront) == 0 {
		return
	}
	for _, entryID := range nearFront {
		notified, err := p.store.WasNotifiedSoon(ctx, eventID, entryID)
		if err != nil || notified {
			continue
		}
		ent, found, err := p.store.GetEntry(ctx, eventID, entryID)
		if err != nil || !found {
			continue
		}
		if pubErr := p.pub.PublishTurnSoon(ctx, eventID, entryID, ent.VisitorID); pubErr == nil {
			_ = p.store.MarkNotifiedSoon(ctx, eventID, entryID)
		}
	}
}
