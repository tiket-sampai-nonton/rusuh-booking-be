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
)

// entryUnits is the quantity guarantee per admission (ADR-030, decision 3): Booking reserves min(3, free) units for
// the event with no buyer NIK and clamps to the event's max_tickets_per_order. Jumper adoption (design §5.4) stays
// correct with 3: a partial grant is honoured, an adopted row is never released, and the head still holds what it
// was reserved. Tests pin the constant and the adoption rules.
const entryUnits = 3

// Promoter promotes waiting entries to active after Booking reserves their allocation (design §7.1).
// It never admits an entry that is not RESERVED ("Jangan PHP" invariant).
type Promoter struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	booking  outbound.IBookingClient
	pub      outbound.IEventPublisher
	audit    outbound.IEntryAuditRepo
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
	NoSupplySuspectTicks int           // head NO_SUPPLY_NOW ticks before it is labelled suspect (default 50, min 2)
}

// NewPromoter accepts interfaces and returns the concrete worker.
func NewPromoter(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, booking outbound.IBookingClient, pub outbound.IEventPublisher, audit outbound.IEntryAuditRepo, clk clock.IClock, logger *slog.Logger, tuning PromoterTuning) *Promoter {
	if tuning.BatchMax <= 0 {
		tuning.BatchMax = 200
	}
	if tuning.LeaseTTL <= 0 {
		tuning.LeaseTTL = 10 * time.Second
	}
	if tuning.NoSupplySuspectTicks < 2 {
		tuning.NoSupplySuspectTicks = 50
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Promoter{store: store, settings: settings, booking: booking, pub: pub, audit: audit, clock: clk, logger: logger, tuning: tuning, metrics: noopMetrics{}, recs: map[string]*headRec{}}
}

// WithMetrics sets the counter sink (default: no-op).
func (p *Promoter) WithMetrics(m outbound.IPromoterMetrics) *Promoter {
	if m != nil {
		p.metrics = m
	}
	return p
}

// Run promotes one batch for every active event (D-C8.10). A failing event never stops the others: the errors are joined.
// The turn.arrived announcements and the pre-turn notify of an event run after its lease is released (C8.3, PF-P1-5;
// C8.8, PF-P2-2), never inside the lease. The announcements are bounded (R36, see announce).
func (p *Promoter) Run(ctx context.Context) error {
	ids, err := p.settings.ListActiveEventIDs(ctx, p.clock.Now())
	if err != nil {
		return err
	}
	var errs []error
	for _, eventID := range ids {
		notifyPosition, arrivals, err := p.runEvent(ctx, eventID)
		if err != nil {
			errs = append(errs, fmt.Errorf("promoter event %s: %w", eventID, err))
		}
		p.announce(ctx, eventID, arrivals)
		p.notifyPreTurn(ctx, eventID, notifyPosition)
	}
	return errors.Join(errs...)
}

// announce publishes turn.arrived for the entries an event promoted this tick. It runs after the lease is released, but a
// broker stall must still not hold the job for N x ConfirmWait, so it is bounded like the tick (R36): once the bound
// passes, the event's remaining arrivals are not attempted. A failed publish is logged and never fails the tick (INV-6).
func (p *Promoter) announce(ctx context.Context, eventID string, arrivals []arrival) {
	if len(arrivals) == 0 {
		return
	}
	actx, cancel := context.WithTimeout(ctx, p.leaseBound())
	defer cancel()
	for _, a := range arrivals {
		if err := actx.Err(); errors.Is(err, context.DeadlineExceeded) {
			p.logger.Warn("batas waktu turn.arrived habis; sisa entry tidak diumumkan tick ini", "eventId", eventID, "entryId", a.entryID, "error", err)
			return
		}
		if err := p.pub.PublishTurnArrived(actx, eventID, a.entryID, a.visitorID, a.deadline); err != nil {
			p.logger.Warn("gagal mempublish turn.arrived; tidak diulang", "eventId", eventID, "entryId", a.entryID, "error", err)
		}
	}
}

// leaseBound is how long one tick may run, and how long its announcements may take: the lease TTL less a second, so work
// stops before the lease can lapse (LeaseTTL/2 when the TTL is under a second).
func (p *Promoter) leaseBound() time.Duration {
	bound := p.tuning.LeaseTTL - time.Second
	if bound <= 0 {
		bound = p.tuning.LeaseTTL / 2
	}
	return bound
}

// arrival is one entry this tick promoted; Run announces it with queue.turn.arrived once the lease is released.
type arrival struct {
	entryID, visitorID string
	deadline           time.Time
}

func (p *Promoter) inc(name string, labels ...string) { p.metrics.Inc(name, labels...) }

// runEvent runs one promoter tick for an event under the per-event lease (design §5.4a, ADR-029). It returns the pre-turn
// notify position (0 when notify is off, the lease is held elsewhere, or the settings are unknown) and the entries it
// promoted. Run announces the arrivals and notifies once the lease is released, which is when runEvent returns.
func (p *Promoter) runEvent(ctx context.Context, eventID string) (int, []arrival, error) {
	token, acquired, err := p.store.AcquirePromoterLease(ctx, eventID, p.tuning.LeaseTTL)
	if err != nil {
		return 0, nil, err
	}
	if !acquired {
		return 0, nil, nil // another instance is promoting this event this tick
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
	runCtx, cancel := context.WithTimeout(ctx, p.leaseBound())
	defer cancel()

	settings, ok, err := p.settings.Get(runCtx, eventID)
	if err != nil || !ok {
		return 0, nil, err
	}
	// Pre-turn notifications are attempted on every return path after this point (TC-QUE-31); a failure never changes the
	// result of the tick (INV-6).
	notifyPosition := 0
	if settings.PreTurnNotifyEnabled && settings.NearFrontMultiplier > 0 {
		notifyPosition = settings.ActiveSlotCapacity * settings.NearFrontMultiplier
	}
	// free and the candidate list are read under the lease, and the same capacity is passed to Promote (L1).
	active, err := p.store.ActiveCount(runCtx, eventID)
	if err != nil {
		return notifyPosition, nil, err
	}
	free := settings.ActiveSlotCapacity - active
	if free <= 0 {
		return notifyPosition, nil, nil
	}
	if free > p.tuning.BatchMax {
		free = p.tuning.BatchMax
	}
	candidates, err := p.store.TopWaiting(runCtx, eventID, free)
	if err != nil {
		return notifyPosition, nil, err
	}
	if len(candidates) == 0 {
		p.dropRec(eventID) // C8 row 0
		return notifyPosition, nil, nil
	}
	headID := candidates[0]
	now := p.clock.Now()

	entries, visitorByEntry := p.buildEntries(runCtx, eventID, candidates, now)
	if len(entries) == 0 {
		return notifyPosition, nil, nil
	}
	resp, err := p.booking.ReserveBatch(runCtx, outbound.AllocationBatchRequest{EventID: eventID, Entries: entries})
	if err != nil {
		return notifyPosition, nil, err // Booking down: entries stay waiting, retry next tick (record untouched, C8 row 2)
	}

	out := p.classify(runCtx, eventID, resp, now)

	if len(out.promoteCands) == 0 {
		p.observeHead(eventID, headID, out, nil)
		return notifyPosition, nil, nil
	}
	res, err := p.store.Promote(runCtx, eventID, out.promoteCands, settings.ActiveSlotCapacity, now.UnixMilli(), outbound.PromoteOpts{LeaseToken: token})
	if err != nil {
		// Possibly committed (ambiguous timeout) or not: never release. The RESERVED rows are replayed next tick.
		return notifyPosition, nil, err
	}
	if res.LeaseLost {
		p.inc(MetricLeaseLost)
		p.logger.Warn("lease promoter hilang sebelum Promote; tidak ada yang dipromosikan", "eventId", eventID)
		return notifyPosition, nil, nil
	}
	if res.Full {
		// L1 says this is unreachable under a valid lease: a defect signal. Keep the rows (never release).
		p.inc(MetricFullAfterGrant)
		p.logger.Error("Promote FULL setelah alokasi diberikan; baris RESERVED dipertahankan untuk replay", "eventId", eventID)
		return notifyPosition, nil, nil
	}
	outbound.ReleaseTerminal(runCtx, p.booking, p.logger, res.Skipped, out.grantedAlloc)

	promoted := map[string]bool{}
	arrivals := make([]arrival, 0, len(res.Promoted))
	for _, entryID := range res.Promoted {
		promoted[entryID] = true
		arrivals = append(arrivals, arrival{entryID: entryID, visitorID: visitorByEntry[entryID], deadline: time.UnixMilli(out.deadlineByEntry[entryID]).UTC()})
	}
	p.observeHead(eventID, headID, out, promoted)
	return notifyPosition, arrivals, nil
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
				// Jumper path: adopt, never release (design §5.4, ADR-029 amendment). The row at Booking is real and
				// releasing it would turn a live WAITING entry into a permanent ALREADY_ACTIVE.
				out.adoptedAfterNoSupply = append(out.adoptedAfterNoSupply, a.QueueEntryID)
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

// buildEntries reads all candidates in one GetEntries call (C8.8, PF-P2-2) and prepares the anonymous Booking request.
// Orphans are ended at once (C5). A failed read skips the whole tick: a transient failure is never an orphan.
func (p *Promoter) buildEntries(ctx context.Context, eventID string, candidates []string, now time.Time) ([]outbound.AllocationEntryRequest, map[string]string) {
	entries := make([]outbound.AllocationEntryRequest, 0, len(candidates))
	visitorByEntry := map[string]string{}
	found, gerr := p.store.GetEntries(ctx, eventID, candidates)
	if gerr != nil {
		p.logger.Warn("GetEntries gagal; kandidat dilewati tick ini", "eventId", eventID, "error", gerr)
		return entries, visitorByEntry // transient Valkey failure: skipped, never ended
	}
	for _, entryID := range candidates {
		ent, ok := found[entryID]
		if !ok {
			// C5: an id in waiting without a hash can never be admitted; end it so it stops being the head.
			if _, err := p.store.EndEntry(ctx, eventID, entryID, outbound.EndPromoterOrphan, now.UnixMilli()); err != nil {
				p.logger.Warn("gagal membersihkan id yatim di waiting", "eventId", eventID, "entryId", entryID, "error", err)
			} else {
				p.inc(MetricEntryEnded, "outcome", outbound.EndPromoterOrphan)
			}
			continue
		}
		visitorByEntry[entryID] = ent.VisitorID
		// No buyer fields (ADR-030): the admission is anonymous; the buyer NIK/email are bound at hold.
		entries = append(entries, outbound.AllocationEntryRequest{
			QueueEntryID: entryID, VisitorID: ent.VisitorID, Units: entryUnits,
			Seq: int64(ent.Seq), AccountID: ent.AccountID,
		})
	}
	return entries, visitorByEntry
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

// notifyPreTurn publishes queue.turn.soon for the live waiting entries inside the notify position that no earlier tick
// announced (TC-QUE-13, SRS-F-25, D-C8.3). The per-event seq watermark advances before anything is published, so each seq
// is announced at most once: a crash between the advance and a publish loses those messages (best effort, INV-6), and a
// failed publish is not retried. It runs outside the promoter lease.
func (p *Promoter) notifyPreTurn(ctx context.Context, eventID string, notifyPosition int) {
	if notifyPosition <= 0 {
		return
	}
	count, err := p.store.WaitingCount(ctx, eventID)
	if err != nil || count == 0 {
		return
	}
	// upTo is the seq of the last live waiting entry inside the notify position (the last one when fewer are waiting).
	upTo, ok, err := p.store.WaitingSeqAt(ctx, eventID, min(int64(notifyPosition), count)-1)
	if err != nil || !ok {
		return
	}
	prev, err := p.store.AdvanceNotifyWatermark(ctx, eventID, upTo)
	if err != nil {
		p.logger.Warn("pre-turn: watermark gagal dimajukan; tidak ada yang diumumkan tick ini", "eventId", eventID, "error", err)
		return
	}
	if upTo <= prev {
		return
	}
	nearFront, err := p.store.WaitingBetween(ctx, eventID, prev, upTo, notifyPosition)
	if err != nil {
		p.logger.Warn("pre-turn: jendela seq gagal dibaca; seq ini tidak diumumkan", "eventId", eventID, "error", err)
		return
	}
	for _, entryID := range nearFront {
		ent, found, err := p.store.GetEntry(ctx, eventID, entryID)
		if err != nil || !found {
			continue
		}
		if pubErr := p.pub.PublishTurnSoon(ctx, eventID, entryID, ent.VisitorID); pubErr != nil {
			p.logger.Warn("pre-turn: queue.turn.soon gagal dipublish; tidak diulang", "eventId", eventID, "entryId", entryID, "error", pubErr)
		}
	}
}
