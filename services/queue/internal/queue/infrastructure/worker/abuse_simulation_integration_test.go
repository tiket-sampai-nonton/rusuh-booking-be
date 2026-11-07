//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-ANON-19 (AQB-R15.4): abuse-limit simulation in VIRTUAL time against the real Valkey scripts, the real Promoter,
// Liveness and Sweeper workers, and a Booking stub that accounts in UNITS (3 per admission, partial grant, release at
// claim-window expiry or, for claimed rows, at claim + selection_ttl).
//
// SCALE: the requirement text pins S=100, stock 3,000, 10,000 ghosts (hours of virtual time, millions of heartbeats).
// The default run uses the SAME formulas with S=10, stock 300, 200 ghosts (20 waves, same wave arithmetic), so it
// finishes in tens of seconds. RUN_ABUSE_FULL=1 runs the full-size parameters. Which size ran is logged.

const (
	simClaimWindow  = 180 * time.Second
	simSelectionTTL = 300 * time.Second
	simStaleAfter   = 90 * time.Second
	simEndAfter     = 300 * time.Second
	simHBInterval   = 30 * time.Second
	simUnits        = 3
)

type simParams struct {
	slots, ghosts int // S and the ghost population
}

func (p simParams) stock() int { return p.slots * simUnits * 10 }

func simSize() simParams {
	if os.Getenv("RUN_ABUSE_FULL") == "1" {
		return simParams{slots: 100, ghosts: 10000}
	}
	return simParams{slots: 10, ghosts: 200}
}

// unitBooking accounts stock in units exactly like Booking's reserveOne (ADR-030 decision 3).
type unitBooking struct {
	mu      sync.Mutex
	clk     *clock.Fixed
	start   time.Time
	free    int
	total   int
	rows    map[string]*unitRow
	grants  map[string]int // grants per queue entry id (an entry must be admitted at most once)
	byAlloc map[string]string
	seq     int
}

type unitRow struct {
	alloc     string
	units     int
	status    string // RESERVED | CLAIMED | EXPIRED | RELEASED
	expiresAt time.Time
	claimedAt time.Time
}

func newUnitBooking(clk *clock.Fixed, start time.Time, total int) *unitBooking {
	return &unitBooking{clk: clk, start: start, free: total, total: total, rows: map[string]*unitRow{},
		grants: map[string]int{}, byAlloc: map[string]string{}}
}

func (b *unitBooking) ReserveBatch(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out outbound.AllocationBatchResponse
	for _, e := range req.Entries {
		row, ok := b.rows[e.QueueEntryID]
		switch {
		case ok && row.status == "RESERVED":
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, AllocationID: row.alloc,
				Status: "GRANTED", ClaimDeadline: row.expiresAt.Format(time.RFC3339)})
		case ok:
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, Status: "ALREADY_ACTIVE"})
		case b.free <= 0:
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, Status: "NO_SUPPLY_NOW"})
		default:
			n := e.Units
			if n > b.free {
				n = b.free
			}
			b.seq++
			alloc := fmt.Sprintf("alloc-%d", b.seq)
			exp := b.clk.Now().Add(simClaimWindow)
			b.rows[e.QueueEntryID] = &unitRow{alloc: alloc, units: n, status: "RESERVED", expiresAt: exp}
			b.byAlloc[alloc] = e.QueueEntryID
			b.free -= n
			b.grants[e.QueueEntryID]++
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, AllocationID: alloc,
				Status: "GRANTED", ClaimDeadline: exp.Format(time.RFC3339)})
		}
	}
	return out, nil
}

func (b *unitBooking) ReleaseAllocation(_ context.Context, allocationID, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, ok := b.byAlloc[allocationID]; ok && b.rows[id].status == "RESERVED" {
		b.rows[id].status = "RELEASED"
		b.free += b.rows[id].units
	}
	return nil
}

func (b *unitBooking) Supply(context.Context, string) (outbound.SupplySnapshot, error) {
	return outbound.SupplySnapshot{SalesStartsAt: b.start, MaxTicketsPerOrder: simUnits}, nil
}

// expire models Booking's session sweeper: an unclaimed row frees its units at the claim deadline.
func (b *unitBooking) expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	for _, r := range b.rows {
		if r.status == "RESERVED" && !r.expiresAt.After(now) {
			r.status = "EXPIRED"
			b.free += r.units
		}
	}
}

func (b *unitBooking) markClaimed(entryID string, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r := b.rows[entryID]; r != nil && r.status == "RESERVED" {
		r.status, r.claimedAt = "CLAIMED", at
	}
}

// releaseClaimed frees claimed rows whose selection window ended and returns their entry ids (journey.ended).
func (b *unitBooking) releaseClaimed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ended []string
	for id, r := range b.rows {
		if r.status == "CLAIMED" && !r.claimedAt.Add(simSelectionTTL).After(b.clk.Now()) {
			r.status = "RELEASED"
			b.free += r.units
			ended = append(ended, id)
		}
	}
	return ended
}

// reservedIDs lists entries whose row is reserved and not yet claimed.
func (b *unitBooking) reservedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []string
	for id, r := range b.rows {
		if r.status == "RESERVED" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (b *unitBooking) freeUnits() int { b.mu.Lock(); defer b.mu.Unlock(); return b.free }

func (b *unitBooking) maxGrantsPerEntry() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := 0
	for _, n := range b.grants {
		if n > m {
			m = n
		}
	}
	return m
}

// sim wires the real workers over one real store with a virtual clock.
type sim struct {
	t        *testing.T
	store    *queuevalkey.QueueStore
	clk      *clock.Fixed
	start    time.Time
	book     *unitBooking
	settings *fakeSettings
	promoter *worker.Promoter
	liveness *worker.Liveness
	sweeper  *worker.Sweeper
	visitors map[string]string // entry id -> visitor id
	gen      map[string]int64
	claimAll bool
	admitted map[string]time.Time // entry id -> first tick at which it was seen ADMITTED/CLAIMED
	minFree  int
	maxLive  int
}

func newSim(t *testing.T, p simParams, claimAll bool) *sim {
	t.Helper()
	// Liveness logs through slog.Default(): one line per virtual second drowns the test output.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(discard{}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	store, _ := twoStores(t)
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	clk := &clock.Fixed{T: start}
	settings := &fakeSettings{s: entity.EventSettings{EventID: "ev", ActiveSlotCapacity: p.slots, OneActivePerVisitor: true}}
	book := newUnitBooking(clk, start, p.stock())
	pr := worker.NewPromoter(store, settings, book, &fakePub{}, itAudit{}, clk, nil, worker.PromoterTuning{BatchMax: 200})
	lv := worker.NewLiveness(store, settings, clk, worker.LivenessTuning{StaleAfter: simStaleAfter, EndAfter: simEndAfter, BatchMax: 1000, LeaseTTL: 5 * time.Second})
	return &sim{t: t, store: store, clk: clk, start: start, book: book, settings: settings,
		promoter: pr, liveness: lv, sweeper: worker.NewSweeper(store, settings, clk, 1000),
		visitors: map[string]string{}, gen: map[string]int64{}, claimAll: claimAll,
		admitted: map[string]time.Time{}, minFree: p.stock(), maxLive: 0}
}

func (s *sim) at(sec int) time.Time { return s.start.Add(time.Duration(sec) * time.Second) }

func (s *sim) join(id string) {
	s.t.Helper()
	vid := "v-" + id
	r, err := s.store.Join(context.Background(), outbound.JoinParams{EventID: "ev", EntryID: id, NewVisitorID: vid,
		NowMs: s.clk.Now().UnixMilli(), OneActivePerVisitor: true, TokenTTL: 24 * time.Hour})
	require.NoError(s.t, err)
	require.Equal(s.t, outbound.JoinCreated, r.Outcome)
	s.visitors[id], s.gen[id] = vid, r.Gen
}

func (s *sim) heartbeat(id string) {
	_, err := s.store.Heartbeat(context.Background(), "ev", s.visitors[id], s.gen[id], s.clk.Now().UnixMilli())
	require.NoError(s.t, err)
}

// tick runs one virtual second of every worker and the Booking stub, then records the invariants.
func (s *sim) tick(watch ...string) {
	s.t.Helper()
	ctx := context.Background()
	s.book.expire()
	for _, id := range s.book.releaseClaimed() {
		_, err := s.store.EndEntry(ctx, "ev", id, "UNCLAIMED_JOURNEY_ENDED", s.clk.Now().UnixMilli())
		require.NoError(s.t, err)
	}
	require.NoError(s.t, s.liveness.Run(ctx))
	require.NoError(s.t, s.sweeper.Run(ctx))
	require.NoError(s.t, s.promoter.Run(ctx))
	live, err := s.store.ActiveCount(ctx, "ev")
	require.NoError(s.t, err)
	if live > s.maxLive {
		s.maxLive = live
	}
	if f := s.book.freeUnits(); f < s.minFree {
		s.minFree = f
	}
	for _, id := range watch {
		if _, seen := s.admitted[id]; seen {
			continue
		}
		if e, ok, _ := s.store.GetEntry(ctx, "ev", id); ok && (e.Status == "ADMITTED" || e.Status == "CLAIMED") {
			s.admitted[id] = s.clk.Now()
		}
	}
	if s.claimAll {
		s.claimAdmitted()
	}
}

// claimAdmitted makes every ADMITTED ghost claim at once (claim-then-idle ghosts).
func (s *sim) claimAdmitted() {
	ctx := context.Background()
	for _, id := range s.book.reservedIDs() {
		e, ok, _ := s.store.GetEntry(ctx, "ev", id)
		if !ok || e.Status != "ADMITTED" {
			continue
		}
		if _, err := s.store.Claim(ctx, "ev", id, e.VisitorID, s.clk.Now().UnixMilli()); err == nil {
			s.book.markClaimed(id, s.clk.Now())
		}
	}
}

func (s *sim) drained() bool {
	ctx := context.Background()
	w, _ := s.store.WaitingCount(ctx, "ev")
	live, _ := s.store.ActiveCount(ctx, "ev")
	return w == 0 && live == 0
}

func ghostID(i int) string { return fmt.Sprintf("g%05d", i) }

// run advances one second at a time until stop() or limitSec; ghosts listed in hb heartbeat every 30 s.
func (s *sim) run(limitSec int, hb func(sec int), stop func() bool, watch ...string) int {
	s.t.Helper()
	for sec := 0; sec <= limitSec; sec++ {
		s.clk.T = s.at(sec)
		if hb != nil {
			hb(sec)
		}
		s.tick(watch...)
		if stop != nil && stop() {
			return sec
		}
	}
	return limitSec + 1
}

func heartbeatingGhosts(s *sim, n int) func(int) {
	return func(sec int) {
		for i := 0; i < n; i++ {
			if sec%int(simHBInterval/time.Second) == i%int(simHBInterval/time.Second) && sec > 0 {
				s.heartbeat(ghostID(i))
			}
		}
	}
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// (a), (b): heartbeating ghosts that never claim, then one real buyer behind all of them.
func TestAbuseSim_HeartbeatingNeverClaimGhostsBoundStockAndDelay(t *testing.T) {
	p := simSize()
	t.Logf("TC-ANON-19 (a)(b) size: S=%d ghosts=%d stock=%d (RUN_ABUSE_FULL=1 for the pinned 100/10000/3000)", p.slots, p.ghosts, p.stock())
	s := newSim(t, p, false)
	for i := 0; i < p.ghosts; i++ {
		s.clk.T = s.at(0)
		s.join(ghostID(i))
	}
	s.clk.T = s.at(1)
	s.join("real")
	hbGhosts := heartbeatingGhosts(s, p.ghosts)
	hb := func(sec int) {
		hbGhosts(sec)
		if sec > 1 && sec%int(simHBInterval/time.Second) == 1 {
			s.heartbeat("real")
		}
	}
	waves := ceilDiv(p.ghosts, p.slots)
	bound := (waves+1)*int(simClaimWindow/time.Second+2) + 1
	end := s.run(bound+int(simClaimWindow/time.Second), hb, func() bool { return s.drained() }, "real")

	// (a) stock bound at every step, and the queue drains within the pinned bound
	assert.GreaterOrEqual(t, s.minFree, p.stock()-p.slots*simUnits, "free stock never below stock - S*3")
	assert.LessOrEqual(t, s.maxLive, p.slots, "un-held admissions never exceed S")
	assert.Equal(t, p.slots, s.maxLive, "the bound is reached (the check is not vacuous)")
	assert.Equal(t, p.stock()-p.slots*simUnits, s.minFree, "the stock bound is tight: S admissions hold exactly 3 units each")
	assert.LessOrEqual(t, end, bound, "drained no later than the pinned bound")
	assert.Equal(t, p.stock(), s.book.freeUnits(), "all units are back once drained")
	assert.Equal(t, 1, s.book.maxGrantsPerEntry(), "each ghost is admitted exactly once")
	assert.Len(t, s.book.grants, p.ghosts+1)

	// (b) the real buyer waited behind exactly `waves` waves of ghosts: not faster than (waves-1) windows, not slower
	// than waves*(claim window + 2) + 1
	at, ok := s.admitted["real"]
	require.True(t, ok, "the real buyer is admitted")
	waited := int(at.Sub(s.at(1)).Seconds())
	assert.LessOrEqual(t, waited, waves*int(simClaimWindow/time.Second+2)+1)
	assert.GreaterOrEqual(t, waited, (waves-1)*int(simClaimWindow/time.Second), "waves cannot be skipped")
}

// (a2): ghosts that claim at once and then idle with heartbeats: slots are freed at claim + selection_ttl.
func TestAbuseSim_ClaimThenIdleGhostsBoundStock(t *testing.T) {
	p := simSize()
	if os.Getenv("RUN_ABUSE_FULL") != "1" {
		p.ghosts = 100 // 10 waves of 300 s keeps the default run short
	}
	t.Logf("TC-ANON-19 (a2) size: S=%d ghosts=%d", p.slots, p.ghosts)
	s := newSim(t, p, true)
	for i := 0; i < p.ghosts; i++ {
		s.clk.T = s.at(0)
		s.join(ghostID(i))
	}
	waves := ceilDiv(p.ghosts, p.slots)
	bound := (waves + 1) * int((simClaimWindow+simSelectionTTL)/time.Second+2)
	end := s.run(bound, heartbeatingGhosts(s, p.ghosts), func() bool { return s.drained() })

	assert.GreaterOrEqual(t, s.minFree, p.stock()-p.slots*simUnits, "CLAIMED admissions are counted in S")
	assert.LessOrEqual(t, s.maxLive, p.slots)
	assert.Equal(t, p.slots, s.maxLive, "the bound is reached")
	assert.LessOrEqual(t, end, bound)
	assert.Equal(t, p.stock(), s.book.freeUnits())
	assert.Equal(t, 1, s.book.maxGrantsPerEntry())
	assert.Len(t, s.book.grants, p.ghosts)
}

// (c): a real buyer AHEAD of the ghosts is admitted on the first promoter tick.
func TestAbuseSim_RealBuyerAheadOfGhostsIsAdmittedFirstTick(t *testing.T) {
	p := simSize()
	s := newSim(t, p, false)
	s.clk.T = s.at(0)
	s.join("real")
	for i := 0; i < p.ghosts; i++ {
		s.join(ghostID(i))
	}
	s.tick("real")
	at, ok := s.admitted["real"]
	require.True(t, ok)
	assert.Equal(t, s.at(0), at, "admitted in the first tick")
}

// (d): STALE ghosts (never heartbeat) are skipped; the heartbeating real buyer waits only for the first S ghosts.
func TestAbuseSim_StaleGhostsAreSkipped(t *testing.T) {
	p := simSize()
	s := newSim(t, p, false)
	s.clk.T = s.at(0)
	for i := 0; i < p.ghosts; i++ {
		s.join(ghostID(i))
	}
	s.clk.T = s.at(1)
	s.join("real")
	hb := func(sec int) {
		if sec > 1 && sec%int(simHBInterval/time.Second) == 1 {
			s.heartbeat("real")
		}
	}
	watch := make([]string, 0, p.ghosts+1)
	for i := 0; i < p.ghosts; i++ {
		watch = append(watch, ghostID(i))
	}
	watch = append(watch, "real")
	s.run(200, hb, nil, watch...)

	at, ok := s.admitted["real"]
	require.True(t, ok, "the real buyer is admitted")
	waited := at.Sub(s.at(1))
	assert.LessOrEqual(t, waited, simClaimWindow+2*time.Second, "waits only for the first S live ghosts to lapse")
	admittedGhosts := 0
	for i := 0; i < p.ghosts; i++ {
		if when, ok := s.admitted[ghostID(i)]; ok {
			admittedGhosts++
			assert.False(t, when.After(s.at(90)), "no STALE ghost is admitted after t=90 s: %s at %s", ghostID(i), when)
		}
	}
	assert.Equal(t, p.slots, admittedGhosts, "only the first S live ghosts were admitted")
}
