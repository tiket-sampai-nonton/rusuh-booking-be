package worker_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// fakeStore is an in-memory outbound.IQueueStore faithful to the Lua semantics proven by the real T02-T07 scripts
// (fence, requireHead, skip report, waiting ZREM on end, LIMIT). It exists to drive the promoter fast and under
// -race; it is NOT the proof of INV-1/INV-2 against the real Lua (that is the integration suite).
type fakeStore struct {
	mu sync.Mutex

	entries map[string]*entity.Entry // keyed by entryID; status/allocationId mutate in place
	waiting []string                 // ordered by seq ascending
	active  map[string]bool

	leaseToken string // "" when free
	seq        int64

	// fault injection
	unreadable map[string]int
	orphanSeq  map[string]int64

	promoteErr   error
	forcedFull   bool
	leaseLostNow bool

	windowCalls []saleWindowCall // SetSaleWindow calls, in order
	windowErr   error            // injected SetSaleWindow failure
}

// saleWindowCall records one SetSaleWindow invocation.
type saleWindowCall struct {
	eventID          string
	startsMs, endsMs int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string]*entity.Entry{}, active: map[string]bool{}, unreadable: map[string]int{}, orphanSeq: map[string]int64{}}
}

// addWaitingAt adds a WAITING entry with an explicit seq (to put an entry back at the head).
func (s *fakeStore) addWaitingAt(id string, seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = &entity.Entry{ID: id, VisitorID: "v-" + id, Seq: valueobject.Seq(seq), Status: valueobject.StatusWaiting}
	s.waiting = append(s.waiting, id)
}

// addOrphanWaiting puts an id in waiting without any entry hash (C5).
func (s *fakeStore) addOrphanWaiting(id string, seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiting = append(s.waiting, id)
	s.orphanSeq[id] = seq
}

func (s *fakeStore) addWaiting(id string, nikHash byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.entries[id] = &entity.Entry{ID: id, VisitorID: "v-" + id, Seq: valueobject.Seq(s.seq), Status: valueobject.StatusWaiting}
	s.waiting = append(s.waiting, id)
}

// freeSlot releases an admitted entry's active slot directly (test helper, models leave/claim completion).
func (s *fakeStore) freeSlot(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, id)
	delete(s.entries, id)
}

func (s *fakeStore) removeOrphanFromWaiting(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeWaitingLocked(id)
	delete(s.entries, id)
}

func (s *fakeStore) removeWaitingLocked(id string) {
	for i, w := range s.waiting {
		if w == id {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			return
		}
	}
}

// Join mirrors join.lua's effect on the entry and waiting set (constraint keys are not modelled: the differential
// scripts never rejoin an identity).
func (s *fakeStore) Join(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.entries[p.EntryID] = &entity.Entry{ID: p.EntryID, VisitorID: p.NewVisitorID, Seq: valueobject.Seq(s.seq), Status: valueobject.StatusWaiting}
	s.waiting = append(s.waiting, p.EntryID)
	return outbound.JoinResult{Outcome: outbound.JoinCreated, EntryID: p.EntryID, Seq: s.seq, VisitorID: p.NewVisitorID, Status: "WAITING"}, nil
}

func (s *fakeStore) Promote(_ context.Context, _ string, cands []outbound.PromoteCandidate, capacity int, nowMs int64, opts outbound.PromoteOpts) (outbound.PromoteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promoteErr != nil {
		return outbound.PromoteResult{}, s.promoteErr
	}
	if s.leaseLostNow || opts.LeaseToken == "" || opts.LeaseToken != s.leaseToken {
		return outbound.PromoteResult{LeaseLost: true}, nil
	}
	free := capacity - len(s.active)
	if s.forcedFull || free <= 0 {
		return outbound.PromoteResult{Full: true}, nil
	}
	if opts.RequireHead && len(cands) > 0 {
		if len(s.waiting) == 0 || s.waiting[0] != cands[0].EntryID {
			return outbound.PromoteResult{Skipped: []outbound.PromoteSkip{{EntryID: cands[0].EntryID, Status: "NOT_HEAD"}}}, nil
		}
	}
	res := outbound.PromoteResult{}
	for _, c := range cands {
		if free <= 0 {
			break // unreached: not reported
		}
		e, ok := s.entries[c.EntryID]
		if !ok {
			res.Skipped = append(res.Skipped, outbound.PromoteSkip{EntryID: c.EntryID, Status: "NO_ENTRY"})
			continue
		}
		switch e.Status {
		case valueobject.StatusWaiting:
			s.removeWaitingLocked(c.EntryID)
			s.active[c.EntryID] = true
			e.Status = valueobject.StatusAdmitted
			e.AllocationID = c.AllocationID
			e.ClaimDeadlineMs = c.ClaimDeadlineMs
			res.Promoted = append(res.Promoted, c.EntryID)
			free--
		case "LEFT", "ENDED", "COMPLETED", "MISSED":
			res.Skipped = append(res.Skipped, outbound.PromoteSkip{EntryID: c.EntryID, Status: string(e.Status)})
		case valueobject.StatusAdmitted, "CLAIMED":
			if e.AllocationID == c.AllocationID {
				res.Skipped = append(res.Skipped, outbound.PromoteSkip{EntryID: c.EntryID, Status: "ADMITTED_SAME"})
			} else {
				res.Skipped = append(res.Skipped, outbound.PromoteSkip{EntryID: c.EntryID, Status: "ADMITTED_OTHER", AllocationID: e.AllocationID})
			}
		default:
			res.Skipped = append(res.Skipped, outbound.PromoteSkip{EntryID: c.EntryID, Status: string(e.Status)})
		}
	}
	return res, nil
}

func (s *fakeStore) Claim(context.Context, string, string, string, int64) (outbound.ClaimResult, error) {
	return outbound.ClaimResult{}, errors.New("unused")
}

// Leave mirrors leave.lua: owner and status are decided together; terminal/CLAIMED are IGNORED and untouched.
func (s *fakeStore) Leave(_ context.Context, _, entryID, visitorID string, _ int64) (outbound.LeaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok || e.VisitorID != visitorID {
		return outbound.LeaveResult{}, entity.ErrNotFound
	}
	switch string(e.Status) {
	case "COMPLETED", "ENDED", "LEFT", "MISSED", "CLAIMED":
		return outbound.LeaveResult{Tag: "IGNORED", PrevStatus: string(e.Status)}, nil
	}
	prev := string(e.Status)
	alloc := ""
	if prev == "ADMITTED" {
		alloc = e.AllocationID
	}
	s.removeWaitingLocked(entryID)
	delete(s.active, entryID)
	e.Status = "LEFT"
	return outbound.LeaveResult{Tag: "OK", PrevStatus: prev, AllocationID: alloc}, nil
}

// EndEntry mirrors end_entry.lua (design §5.1): outcome classes A (SWEEP_MISSED), B (promoter, WAITING only) and C
// (Booking outcomes), the MISSED cleanup, and the orphan self-heal.
func (s *fakeStore) EndEntry(_ context.Context, _, entryID, outcome string, nowMs int64) (outbound.EndResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		delete(s.active, entryID)
		s.removeWaitingLocked(entryID)
		return outbound.EndResult{Tag: "IGNORED", Status: "NO_ENTRY"}, nil
	}
	st := string(e.Status)
	switch st {
	case "COMPLETED", "ENDED", "LEFT":
		if outcome == outbound.EndSweepMissed {
			delete(s.active, entryID)
		}
		return outbound.EndResult{Tag: "IGNORED", Status: st}, nil
	case "MISSED":
		cleaned := s.active[entryID]
		delete(s.active, entryID)
		before := len(s.waiting)
		s.removeWaitingLocked(entryID)
		if cleaned || len(s.waiting) != before {
			return outbound.EndResult{Tag: "OK_CLEANUP", Status: "MISSED"}, nil
		}
		return outbound.EndResult{Tag: "IGNORED", Status: "MISSED"}, nil
	}
	final := ""
	switch outcome {
	case outbound.EndSweepMissed:
		switch st {
		case "ADMITTED":
			if e.ClaimDeadlineMs != 0 && nowMs < e.ClaimDeadlineMs {
				return outbound.EndResult{Tag: "IGNORED", Status: "ADMITTED"}, nil
			}
			final = "MISSED"
		case "CLAIMED":
			return outbound.EndResult{Tag: "IGNORED", Status: "CLAIMED"}, nil
		case "WAITING":
			delete(s.active, entryID)
			return outbound.EndResult{Tag: "IGNORED", Status: "WAITING"}, nil
		default:
			return outbound.EndResult{Tag: "IGNORED", Status: st}, nil
		}
	case outbound.EndAlreadyActive:
		if st != "WAITING" {
			return outbound.EndResult{Tag: "IGNORED", Status: st}, nil
		}
		final = "ENDED"
	default:
		if st != "ADMITTED" && st != "CLAIMED" {
			return outbound.EndResult{Tag: "IGNORED", Status: st}, nil
		}
		switch outcome {
		case "UNCLAIMED":
			final = "MISSED"
		case "PAID":
			final = "COMPLETED"
		default:
			final = "ENDED"
		}
	}
	delete(s.active, entryID)
	s.removeWaitingLocked(entryID)
	e.Status = valueobject.Status(final)
	return outbound.EndResult{Tag: "OK", Status: final}, nil
}

func (s *fakeStore) GetEntry(_ context.Context, _, entryID string) (entity.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		return entity.Entry{}, false, nil
	}
	return *e, true, nil
}

func (s *fakeStore) FindByVisitor(context.Context, string, string) (entity.Entry, bool, error) {
	return entity.Entry{}, false, errors.New("unused")
}

func (s *fakeStore) ActiveCount(context.Context, string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active), nil
}

func (s *fakeStore) TopWaiting(_ context.Context, _ string, n int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || len(s.waiting) == 0 {
		return nil, nil
	}
	cp := append([]string{}, s.waiting...)
	seqOf := func(id string) int64 {
		if e, ok := s.entries[id]; ok {
			return int64(e.Seq)
		}
		return s.orphanSeq[id]
	}
	sort.SliceStable(cp, func(i, j int) bool { return seqOf(cp[i]) < seqOf(cp[j]) })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp, nil
}

// ExpiredActive returns the active ids whose claim deadline is at or before nowMs, oldest first (limit applied).
func (s *fakeStore) ExpiredActive(_ context.Context, _ string, nowMs int64, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := func(id string) int64 {
		if e, ok := s.entries[id]; ok {
			return e.ClaimDeadlineMs
		}
		return 0
	}
	var ids []string
	for id := range s.active {
		if deadline(id) <= nowMs {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if di, dj := deadline(ids[i]), deadline(ids[j]); di != dj {
			return di < dj
		}
		return ids[i] < ids[j]
	})
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (s *fakeStore) SetSale(context.Context, string, string, bool) error { return nil }
func (s *fakeStore) SetSaleWindow(_ context.Context, ev string, startsMs, endsMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowCalls = append(s.windowCalls, saleWindowCall{eventID: ev, startsMs: startsMs, endsMs: endsMs})
	return s.windowErr
}
func (s *fakeStore) GetSale(context.Context, string) (outbound.SaleState, error) {
	return outbound.SaleState{}, nil
}
func (s *fakeStore) SetStatusDoc(context.Context, string, []byte, time.Duration) error { return nil }
func (s *fakeStore) GetStatusDoc(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (s *fakeStore) WasNotifiedSoon(context.Context, string, string) (bool, error) { return true, nil } // not under test
func (s *fakeStore) MarkNotifiedSoon(context.Context, string, string) error        { return nil }
func (s *fakeStore) AcquirePromoterLease(_ context.Context, _ string, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaseToken != "" {
		return "", false, nil
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	s.leaseToken = hex.EncodeToString(b)
	return s.leaseToken, true, nil
}

func (s *fakeStore) ReleasePromoterLease(_ context.Context, _, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaseToken == token {
		s.leaseToken = ""
	}
	return nil
}

// --- ADR-030 surface: the fake models W = 0 (rank = seq, always drawn) and no liveness; those paths are proven on
// the real store in the valkey integration tests. ---

func (s *fakeStore) Heartbeat(context.Context, string, string, int64, int64) (outbound.HeartbeatResult, error) {
	return outbound.HeartbeatResult{Tag: outbound.HeartbeatOK, Status: "WAITING"}, nil
}
func (s *fakeStore) TokenGen(context.Context, string, string) (int64, bool, error) {
	return 1, true, nil
}
func (s *fakeStore) FindLastByVisitor(context.Context, string, string) (entity.Entry, bool, error) {
	return entity.Entry{}, false, nil
}
func (s *fakeStore) SweepLiveness(context.Context, string, string, int64, int64, int, int64) (outbound.LivenessResult, error) {
	return outbound.LivenessResult{}, nil
}
func (s *fakeStore) SweepLivenessTick(context.Context, string, int64, int64, int, int64, time.Duration, int) (outbound.LivenessResult, []string, error) {
	return outbound.LivenessResult{}, nil, nil
}
func (s *fakeStore) WaitingSeqAt(ctx context.Context, ev string, idx int64) (int64, bool, error) {
	ids, _ := s.TopWaiting(ctx, ev, int(idx)+1)
	if int(idx) >= len(ids) {
		return 0, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(s.entries[ids[idx]].Seq), true, nil
}
func (s *fakeStore) WaitingCount(context.Context, string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.waiting)), nil
}
func (s *fakeStore) LivePosition(context.Context, string, int64) (int64, error) { return 1, nil }
