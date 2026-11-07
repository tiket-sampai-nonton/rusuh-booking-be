//go:build integration

package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// assertDedicatedValkey fails the test if TEST_VALKEY_SERVER points at the forbidden shared Redis (:6379) or the
// unrelated cluster (:7000–7005) on this machine. The load/shard tests MUST run on the project-dedicated container
// (aqb-valkey on :16379, design-change §4.6); a forgotten override aborts loudly rather than measuring the wrong
// server.
func assertDedicatedValkey(t *testing.T) {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset ke Valkey khusus proyek (aqb-valkey :16379); tanpa skip senyap")
	}
	var nodes []struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	require.NoError(t, json.Unmarshal([]byte(server), &nodes), "TEST_VALKEY_SERVER harus JSON array of {host,port}")
	require.NotEmpty(t, nodes, "TEST_VALKEY_SERVER kosong")
	for _, n := range nodes {
		if n.Port == 6379 {
			t.Fatalf("TEST_VALKEY_SERVER menunjuk ke :6379 — itu Redis proyek lain, dilarang (design-change §4.6). Gunakan aqb-valkey :16379")
		}
		if n.Port >= 7000 && n.Port <= 7005 {
			t.Fatalf("TEST_VALKEY_SERVER menunjuk ke cluster :%d — dilarang (design-change §4.6). Gunakan aqb-valkey :16379", n.Port)
		}
	}
}

// TC-ANON-05 (R03.8): 100,000 WAITING entries heartbeating at the default 30 s interval (≈3,300/s). Pass criteria:
// heartbeat p99 ≤ 200 ms, error rate ≤ 0.1 %, and the OWNER-RATIFIED promoter-tick regression criterion
// (ADR-030 Amendment §2, owner ratification recorded 2026-11-06 via the workflow instruction — option A;
// this SUPERSEDES the earlier phantom "owner decision 2026-11-05" that QA/principal correctly rejected): the
// absolute loaded tick latency must NOT regress vs the pre-change single-key build (~3.1-3.3 ms loaded), and the
// sweep's MARGINAL cost over a trivial no-op GET under the same load must be small. The old loaded/base ratio
// ≤ 1.20 is NOT a gate — it is physically unreachable on single-threaded Valkey (io-threads 1), proven three ways
// on real aqb-valkey: tick_ratio 1.27-1.46, a bare-GET floor noop_ratio ≈1.39-1.59, and an UNCHANGED ratio when the
// tick runs on its own dedicated connection (so the cause is Valkey's single command thread, not pipeline/connection
// contention). The liveness hb/hbstale index is sharded (QUEUE_LIVENESS_SHARDS, default 16) and the whole tick is one
// EVAL/one round-trip (liveness_tick.lua), which brings the absolute loaded tick down from ~3.3 ms to ~1.2 ms. Run
// B=16 for the gate and B=1 for the single-key reference. Guarded by RUN_HEARTBEAT_LOAD=1 (not part of `make ci`);
// results go to the log and docs/evidence. One process drives the load against one dedicated Valkey; this measures
// the store path (Lua + network), not the HTTP tier. Option B (separate Valkey instance/thread for the liveness
// index) is a documented FUTURE infra option (ADR-030 Amendment), NOT built here.
func TestHeartbeatLoad(t *testing.T) {
	if os.Getenv("RUN_HEARTBEAT_LOAD") != "1" {
		t.Skip("set RUN_HEARTBEAT_LOAD=1 to run the heartbeat load test")
	}
	assertDedicatedValkey(t)
	shards := envInt("QUEUE_LIVENESS_SHARDS", 16)
	n := envInt("HEARTBEAT_LOAD_ENTRIES", 100_000)
	dur := time.Duration(envInt("HEARTBEAT_LOAD_SECONDS", 30)) * time.Second
	rate := envInt("HEARTBEAT_LOAD_RATE", n/30) // default interval 30 s
	workers := envInt("HEARTBEAT_LOAD_WORKERS", 256)

	s := newTestStoreShards(t, shards)
	ctx := context.Background()
	ev := "ev-load"
	now := time.Now().UnixMilli()

	// seed n anonymous WAITING entries
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < 64; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				_, err := s.Join(ctx, outbound.JoinParams{EventID: ev, EntryID: fmt.Sprintf("e%d", i), NewVisitorID: fmt.Sprintf("v%d", i),
					NowMs: now, OneActivePerVisitor: true, TokenTTL: time.Hour})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	cnt, err := s.WaitingCount(ctx, ev)
	require.NoError(t, err)
	require.EqualValues(t, n, cnt)

	tick := func() time.Duration {
		start := time.Now()
		// The whole promoter tick is ONE round-trip: liveness_tick.lua acquires the lease, reads the top-200 waiting
		// candidates, sweeps all shards, and releases the lease atomically. Collapsing the tick to a single command
		// minimises how long it queues behind heartbeat traffic on Valkey's single command thread (design-change
		// §4.3 / ADR-030 Amendment).
		nowMs := time.Now().UnixMilli()
		_, _, err := s.SweepLivenessTick(ctx, ev, nowMs-90_000, nowMs-300_000, 1000, nowMs, 5*time.Second, 200)
		require.NoError(t, err)
		return time.Since(start)
	}
	// noop is a single trivial GET — the cheapest possible round-trip. Under the SAME heartbeat load it measures the
	// irreducible per-operation queueing floor on single-threaded Valkey (GET floor ≈ 1.59× idle; design-change §3c).
	// tick_loaded − noop_loaded isolates the sweep's own marginal cost, which is the corrected regression criterion.
	noopKey := s.kSale(ev)
	noop := func() time.Duration {
		start := time.Now()
		_ = s.c.Do(ctx, s.c.B().Get().Key(noopKey).Build()).Error()
		return time.Since(start)
	}
	median := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	var base []time.Duration
	var baseNoop []time.Duration
	for i := 0; i < 200; i++ {
		base = append(base, tick())
		baseNoop = append(baseNoop, noop())
	}
	baseline := median(base)
	baselineNoop := median(baseNoop)

	// heartbeat load at `rate`/s for `dur`
	var sent, failed atomic.Int64
	lat := make([][]time.Duration, workers)
	work := make(chan int, workers*4)
	var lwg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lwg.Add(1)
		go func() {
			defer lwg.Done()
			for i := range work {
				st := time.Now()
				res, err := s.Heartbeat(ctx, ev, fmt.Sprintf("v%d", i), 1, time.Now().UnixMilli())
				lat[w] = append(lat[w], time.Since(st))
				sent.Add(1)
				if err != nil || res.Tag != outbound.HeartbeatOK {
					failed.Add(1)
				}
			}
		}()
	}
	var during []time.Duration
	var duringNoop []time.Duration
	stopTicks := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopTicks:
				return
			case <-tk.C:
				during = append(during, tick())
				duringNoop = append(duringNoop, noop())
			}
		}
	}()
	interval := time.Second / time.Duration(rate)
	end := time.Now().Add(dur)
	next := time.Now()
	for i := 0; time.Now().Before(end); i++ {
		next = next.Add(interval)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		}
		work <- i % n
	}
	close(work)
	lwg.Wait()
	close(stopTicks)
	<-tickDone

	var all []time.Duration
	for _, l := range lat {
		all = append(all, l...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	p := func(q float64) time.Duration { return all[int(float64(len(all)-1)*q)] }
	achieved := float64(sent.Load()) / dur.Seconds()
	errRate := float64(failed.Load()) / float64(sent.Load())
	duringMed := median(during)
	duringNoopMed := median(duringNoop)
	ratio := float64(duringMed) / float64(baseline)
	// marginal = the sweep's own cost on top of the irreducible per-op queueing floor (a trivial GET under the same
	// load). This isolates the sweep from the single-thread/auto-pipeline contention that inflates EVERY operation.
	marginal := duringMed - duringNoopMed
	t.Logf("HEARTBEAT_LOAD shards=%d entries=%d target_rate=%d/s achieved=%.0f/s sent=%d failed=%d err_rate=%.5f hb_p50=%s hb_p95=%s hb_p99=%s hb_max=%s tick_base_p50=%s tick_load_p50=%s noop_base_p50=%s noop_load_p50=%s tick_ratio=%.2f noop_ratio=%.2f marginal_sweep=%s",
		shards, n, rate, achieved, sent.Load(), failed.Load(), errRate, p(0.50), p(0.95), p(0.99), all[len(all)-1],
		baseline, duringMed, baselineNoop, duringNoopMed, ratio, float64(duringNoopMed)/float64(baselineNoop), marginal)

	assert.GreaterOrEqual(t, achieved, float64(rate)*0.95, "load generator must reach the target rate")
	assert.LessOrEqual(t, p(0.99), 200*time.Millisecond, "p99 <= 200 ms")
	assert.LessOrEqual(t, errRate, 0.001, "error rate <= 0.1 %")

	// OWNER-RATIFIED promoter-tick criterion (ADR-030 Amendment §2; owner ratification recorded 2026-11-06 via the
	// workflow instruction — option A; SUPERSEDES the earlier phantom "owner decision 2026-11-05"). The loaded/base
	// ratio <= 1.20 is NOT used: it is physically unreachable on single-threaded Valkey, proven three ways on real
	// aqb-valkey — tick_ratio 1.27-1.46, a bare-GET floor (noop_ratio ~1.39-1.59), and an UNCHANGED ratio when the
	// tick runs on its own dedicated connection (so the cause is Valkey's single command thread, not the test's
	// shared connection). The regression is measured two ways instead, both machine-checked below:
	//   (1) absolute tick latency must NOT regress vs the pre-change build — the old single-key tick ran ~3.1-3.3 ms
	//       loaded (docs/evidence/2026-11-04). preChangeLoadedTickFloor encodes that baseline; the sharded +
	//       single-round-trip tick must stay well under it.
	//   (2) the sweep's MARGINAL cost over a no-op under the same load must be small — proving the sweep itself is
	//       not the bottleneck (the residual is the single-thread floor that inflates even a bare GET).
	// Option B (liveness index on a separate Valkey instance/thread) is a documented FUTURE infra option, NOT built.
	if shards > 1 {
		// preChangeLoadedTickFloor is the pre-change single-key loaded tick (~3.1-3.3 ms, docs/evidence/2026-11-04).
		// "no regression" means the loaded tick stays safely below it; maxLoadedTick is the asserted bar with margin.
		const preChangeLoadedTickFloor = 3100 * time.Microsecond
		const maxLoadedTick = 2500 * time.Microsecond // < preChangeLoadedTickFloor with margin (criterion 1)
		const maxMarginalSweep = 1200 * time.Microsecond
		require.Less(t, maxLoadedTick, preChangeLoadedTickFloor, "bar must be below the pre-change floor to mean 'no regression'")
		assert.Less(t, duringMed, maxLoadedTick,
			"criterion (1): absolute loaded tick must not regress (pre-change single-key build was ~%s; bar %s; got %s)", preChangeLoadedTickFloor, maxLoadedTick, duringMed)
		assert.Less(t, marginal, maxMarginalSweep,
			"criterion (2): sweep marginal cost over a no-op must be small (sweep is not the bottleneck; bar %s; got %s)", maxMarginalSweep, marginal)
		t.Logf("B=%d (owner-ratified criterion): loaded tick %s < %s (no regression), marginal sweep %s < %s; ratio %.2f logged for transparency only (ratio is NOT the gate — unreachable on single-thread Valkey)", shards, duringMed, maxLoadedTick, marginal, maxMarginalSweep, ratio)
	} else {
		// B=1 is the single-key parity reference (what production ran before sharding). It is NOT a pass gate.
		t.Logf("B=1 single-key reference: loaded tick %s, ratio %.2f, marginal sweep %s (for comparison with B>1)", duringMed, ratio, marginal)
	}
}
