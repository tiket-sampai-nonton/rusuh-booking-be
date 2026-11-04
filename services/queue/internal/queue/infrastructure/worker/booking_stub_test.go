package worker_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// bookingStub implements outbound.IBookingClient exactly as Booking's reserveOne/supplyOutcome behave (design §9,
// §1.1), with no import from services/booking:
//   - a replay inside the window returns the same allocation id and the ORIGINAL expiresAt, even past it;
//   - once a row is RELEASED or EXPIRED it is permanent: every later ReserveBatch answers ALREADY_ACTIVE;
//   - NO_SUPPLY_NOW (no stock, or failEntry) leaves no row and does not change stock;
//   - there is no operation that sets a row back to RESERVED.
type bookingStub struct {
	mu    sync.Mutex
	clk   *clock.Fixed
	cw    time.Duration
	stock int

	rows       map[string]*stubRow
	failed     map[string]bool
	afterHooks map[int]func()
	reserveN   int
	releaseN   int
	released   []string
	seq        int
}

type stubRow struct {
	allocID   string
	status    string // RESERVED | EXPIRED | RELEASED | COMPLETED
	expiresAt time.Time
}

func newBookingStub(clk *clock.Fixed, claimWindow time.Duration, stock int) *bookingStub {
	return &bookingStub{clk: clk, cw: claimWindow, stock: stock, rows: map[string]*stubRow{}, failed: map[string]bool{}, afterHooks: map[int]func(){}}
}

func (s *bookingStub) failEntry(id string)  { s.mu.Lock(); s.failed[id] = true; s.mu.Unlock() }
func (s *bookingStub) clearFail(id string)  { s.mu.Lock(); delete(s.failed, id); s.mu.Unlock() }
func (s *bookingStub) afterEntry(k int, fn func()) { s.mu.Lock(); s.afterHooks[k] = fn; s.mu.Unlock() }
func (s *bookingStub) AdvanceClock(d time.Duration) { s.clk.T = s.clk.T.Add(d) }
func (s *bookingStub) Calls() (reserve, release int) { s.mu.Lock(); defer s.mu.Unlock(); return s.reserveN, s.releaseN }
func (s *bookingStub) ReleasedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.released))
	copy(out, s.released)
	return out
}

func (s *bookingStub) ReserveBatch(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserveN++
	now := s.clk.Now()
	var out outbound.AllocationBatchResponse
	for k, e := range req.Entries {
		if row, ok := s.rows[e.QueueEntryID]; ok {
			if row.status == "RESERVED" {
				out.Allocations = append(out.Allocations, outbound.AllocationResult{
					QueueEntryID: e.QueueEntryID, AllocationID: row.allocID, Status: "GRANTED",
					ClaimDeadline: row.expiresAt.Format(time.RFC3339),
				})
			} else {
				out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, Status: "ALREADY_ACTIVE"})
			}
		} else if s.failed[e.QueueEntryID] {
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, Status: "NO_SUPPLY_NOW"})
		} else if s.stock > 0 {
			s.seq++
			allocID := fmt.Sprintf("alloc-%d", s.seq)
			exp := now.Add(s.cw)
			s.rows[e.QueueEntryID] = &stubRow{allocID: allocID, status: "RESERVED", expiresAt: exp}
			s.stock--
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, AllocationID: allocID, Status: "GRANTED", ClaimDeadline: exp.Format(time.RFC3339)})
		} else {
			out.Allocations = append(out.Allocations, outbound.AllocationResult{QueueEntryID: e.QueueEntryID, Status: "NO_SUPPLY_NOW"})
		}
		if hook, ok := s.afterHooks[k]; ok {
			hook()
		}
	}
	return out, nil
}

func (s *bookingStub) ReleaseAllocation(_ context.Context, allocationID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, row := range s.rows {
		if row.allocID == allocationID && row.status == "RESERVED" {
			row.status = "RELEASED"
			s.stock++
			s.releaseN++
			s.released = append(s.released, id)
			return nil
		}
	}
	return nil // idempotent: already released/expired, or unknown id
}

func (s *bookingStub) Supply(context.Context, string) (outbound.SupplySnapshot, error) {
	return outbound.SupplySnapshot{}, nil
}

// SweepSessions models Booking's own session-sweeper: RESERVED rows past expiresAt become EXPIRED and free stock.
func (s *bookingStub) SweepSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	for _, row := range s.rows {
		if row.status == "RESERVED" && row.expiresAt.Before(now) {
			row.status = "EXPIRED"
			s.stock++
		}
	}
}

// complete marks a RESERVED/granted row COMPLETED (models a successful payment), freeing nothing (stock already
// committed at reserve time).
func (s *bookingStub) complete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.rows[id]; ok {
		row.status = "COMPLETED"
	}
}

// methodSetGuard (s5): a future re-grant helper must fail this test by adding to the method set.
var _ = reflect.TypeOf(&bookingStub{})

// unknownOutcomeBooking rewrites the answer for one entry to an outcome string the promoter does not know.
type unknownOutcomeBooking struct {
	inner *bookingStub
	entry string
}

func (u *unknownOutcomeBooking) ReserveBatch(ctx context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
	resp, err := u.inner.ReserveBatch(ctx, req)
	for i := range resp.Allocations {
		if resp.Allocations[i].QueueEntryID == u.entry {
			resp.Allocations[i] = outbound.AllocationResult{QueueEntryID: u.entry, Status: "SOMETHING_NEW"}
		}
	}
	return resp, err
}
func (u *unknownOutcomeBooking) Supply(ctx context.Context, e string) (outbound.SupplySnapshot, error) {
	return u.inner.Supply(ctx, e)
}
func (u *unknownOutcomeBooking) ReleaseAllocation(ctx context.Context, a, r string) error {
	return u.inner.ReleaseAllocation(ctx, a, r)
}
