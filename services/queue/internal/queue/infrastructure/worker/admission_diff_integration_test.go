//go:build integration

package worker_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const diffNow = int64(1_700_000_000_000)

// op is one step of a differential script. n/ns name entries "d-<n>".
type op struct {
	kind     string // join | promote | leave | end
	n        int
	ns       []int
	reqHead  bool
	stale    bool   // promote with a token that does not own the lease
	alloc    string // override allocation id (ADMITTED_OTHER)
	past     bool   // promote with an already-lapsed deadline
	cap      int
	outcome  string
	stranger bool // leave as a visitor that does not own the entry
}

type script struct {
	name string
	ops  []op
}

func id(n int) string { return fmt.Sprintf("d-%d", n) }

var diffScripts = []script{
	{"capacity_limits_promotion", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "join", n: 3}, {kind: "promote", ns: []int{1, 2, 3}, cap: 2}}},
	{"require_head_not_head_then_head", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "promote", ns: []int{2}, reqHead: true, cap: 5}, {kind: "promote", ns: []int{1}, reqHead: true, cap: 1}}},
	{"stale_token_is_lease_lost", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, stale: true, cap: 5}}},
	{"full_after_slot_taken", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "promote", ns: []int{1}, cap: 1}, {kind: "promote", ns: []int{2}, cap: 1}}},
	{"left_entry_is_terminal_skip", []op{{kind: "join", n: 1}, {kind: "leave", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}}},
	{"ended_entry_is_terminal_skip", []op{{kind: "join", n: 1}, {kind: "end", n: 1, outcome: "ALREADY_ACTIVE"}, {kind: "promote", ns: []int{1}, cap: 5}}},
	{"completed_entry_is_terminal_skip", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}, {kind: "end", n: 1, outcome: "PAID"}, {kind: "promote", ns: []int{1}, cap: 5}}},
	{"admitted_same_allocation", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}, {kind: "promote", ns: []int{1}, cap: 5}}},
	{"admitted_other_allocation", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}, {kind: "promote", ns: []int{1}, alloc: "alloc-other", cap: 5}}},
	{"leave_admitted_returns_allocation", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "promote", ns: []int{1}, cap: 1}, {kind: "leave", n: 1}}},
	{"sweep_missed_expired_then_repeat", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, past: true, cap: 5}, {kind: "end", n: 1, outcome: "SWEEP_MISSED"}, {kind: "end", n: 1, outcome: "SWEEP_MISSED"}, {kind: "promote", ns: []int{1}, cap: 5}}},
	{"sweep_missed_live_is_ignored", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}, {kind: "end", n: 1, outcome: "SWEEP_MISSED"}}},
	{"sweep_missed_waiting_is_ignored", []op{{kind: "join", n: 1}, {kind: "end", n: 1, outcome: "SWEEP_MISSED"}}},
	{"booking_unclaimed_frees_slot", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, cap: 5}, {kind: "end", n: 1, outcome: "UNCLAIMED"}, {kind: "end", n: 1, outcome: "UNCLAIMED"}}},
	{"paid_on_waiting_is_ignored", []op{{kind: "join", n: 1}, {kind: "end", n: 1, outcome: "PAID"}}},
	{"promote_unknown_entry_is_no_entry", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{99}, cap: 5}}},
	{"leave_stranger_and_unknown", []op{{kind: "join", n: 1}, {kind: "leave", n: 1, stranger: true}, {kind: "leave", n: 77}}},
	{"promote_past_the_head_then_full", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "join", n: 3}, {kind: "join", n: 4}, {kind: "promote", ns: []int{2, 3}, cap: 2}, {kind: "promote", ns: []int{1}, cap: 2}}},
	{"quota_exhausted_end_then_batch", []op{{kind: "join", n: 1}, {kind: "join", n: 2}, {kind: "end", n: 1, outcome: "BUYER_QUOTA_EXHAUSTED"}, {kind: "promote", ns: []int{1, 2}, cap: 5}}},
	{"stale_then_valid", []op{{kind: "join", n: 1}, {kind: "promote", ns: []int{1}, stale: true, cap: 5}, {kind: "promote", ns: []int{1}, cap: 5}}},
}

func joinParamsFor(n int) outbound.JoinParams {
	return outbound.JoinParams{
		EventID: "ev", EntryID: id(n), VisitorID: fmt.Sprintf("dv-%d", n), NikHash: fmt.Sprintf("%064x", 100+n),
		EmailHashHex: fmt.Sprintf("%064x", 200+n), EmailEnc: []byte("sealed"), NikMasked: "3201********0001",
		NowMs: diffNow, OneActivePerVisitor: true, OneActivePerEmail: true,
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, entity.ErrNotFound):
		return "not_found"
	default:
		return "other:" + err.Error()
	}
}

// runScript executes ops against one store and returns the ordered result log plus the final observable state.
func runScript(t *testing.T, st outbound.IQueueStore, sc script) (log []string, state string) {
	t.Helper()
	ctx := context.Background()
	token, ok, err := st.AcquirePromoterLease(ctx, "ev", 30*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	seen := map[int]bool{}
	for _, o := range sc.ops {
		switch o.kind {
		case "join":
			seen[o.n] = true
			res, err := st.Join(ctx, joinParamsFor(o.n))
			log = append(log, fmt.Sprintf("join %d -> %s seq=%d err=%s", o.n, res.Outcome, res.Seq, errClass(err)))
		case "promote":
			var cands []outbound.PromoteCandidate
			for _, n := range o.ns {
				seen[n] = true
				alloc := "alloc-" + id(n)
				if o.alloc != "" {
					alloc = o.alloc
				}
				dl := diffNow + 60_000
				if o.past {
					dl = diffNow - 1_000
				}
				cands = append(cands, outbound.PromoteCandidate{EntryID: id(n), AllocationID: alloc, ClaimDeadlineMs: dl})
			}
			tok := token
			if o.stale {
				tok = "token-basi"
			}
			res, err := st.Promote(ctx, "ev", cands, o.cap, diffNow, outbound.PromoteOpts{RequireHead: o.reqHead, LeaseToken: tok})
			log = append(log, fmt.Sprintf("promote %v -> promoted=%v skipped=%v full=%v leaseLost=%v err=%s", o.ns, res.Promoted, res.Skipped, res.Full, res.LeaseLost, errClass(err)))
		case "leave":
			seen[o.n] = true
			vis := fmt.Sprintf("dv-%d", o.n)
			if o.stranger {
				vis = "orang-lain"
			}
			res, err := st.Leave(ctx, "ev", id(o.n), vis, diffNow)
			log = append(log, fmt.Sprintf("leave %d -> %+v err=%s", o.n, res, errClass(err)))
		case "end":
			seen[o.n] = true
			res, err := st.EndEntry(ctx, "ev", id(o.n), o.outcome, diffNow)
			log = append(log, fmt.Sprintf("end %d %s -> %+v err=%s", o.n, o.outcome, res, errClass(err)))
		default:
			t.Fatalf("op tidak dikenal %q", o.kind)
		}
	}
	// final observable state
	var ns []int
	for n := range seen {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	waiting, err := st.TopWaiting(ctx, "ev", 1000)
	require.NoError(t, err)
	inW := map[string]bool{}
	for _, w := range waiting {
		inW[w] = true
	}
	activeIDs, err := st.ExpiredActive(ctx, "ev", 1<<60, 1000)
	require.NoError(t, err)
	inA := map[string]bool{}
	for _, a := range activeIDs {
		inA[a] = true
	}
	cnt, err := st.ActiveCount(ctx, "ev")
	require.NoError(t, err)
	var b strings.Builder
	fmt.Fprintf(&b, "activeCount=%d\n", cnt)
	for _, n := range ns {
		e, found, err := st.GetEntry(ctx, "ev", id(n))
		require.NoError(t, err)
		status := "<none>"
		alloc := ""
		if found {
			status, alloc = string(e.Status), e.AllocationID
		}
		fmt.Fprintf(&b, "%s status=%s alloc=%s waiting=%v active=%v\n", id(n), status, alloc, inW[id(n)], inA[id(n)])
	}
	return log, b.String()
}

// TC-QUE-19c-DIFF (R9-2): the same operation script runs against the in-test fake store and against the REAL store
// with the REAL Lua; any disagreement means the fake (used by the fast promoter tests) has drifted from the scripts.
func TestAdmissionDiff(t *testing.T) {
	compared := 0
	for _, sc := range diffScripts {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			real, _ := twoStores(t)
			fake := newFakeStore()
			realLog, realState := runScript(t, real, sc)
			fakeLog, fakeState := runScript(t, fake, sc)
			if !assert.Equal(t, realLog, fakeLog, "hasil per-op berbeda antara fake dan Lua nyata") {
				t.Logf("nyata:\n%s\nfake:\n%s", strings.Join(realLog, "\n"), strings.Join(fakeLog, "\n"))
			}
			if !assert.Equal(t, realState, fakeState, "keadaan akhir berbeda antara fake dan Lua nyata") {
				t.Logf("nyata:\n%s\nfake:\n%s", realState, fakeState)
			}
			compared++
		})
	}
	require.Equal(t, 20, compared, "tepat 20 skrip harus dibandingkan; 0 perbandingan tidak boleh lolos")
}
