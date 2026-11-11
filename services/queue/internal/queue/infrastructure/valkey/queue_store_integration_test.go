//go:build integration

package valkey

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// defaultTestShards is the liveness-index shard count used by the default test store (matches the
// QUEUE_LIVENESS_SHARDS production default). Shard-specific tests use newTestStoreShards.
const defaultTestShards = 16

func newTestStore(t *testing.T) *QueueStore { return newTestStoreShards(t, defaultTestShards) }

// newTestStoreShards returns a QueueStore on the real Valkey named by TEST_VALKEY_SERVER with the given
// liveness shard count (b=1 reproduces the pre-sharding single-key behaviour).
func newTestStoreShards(t *testing.T, b int) *QueueStore {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	// Use a unique prefix per test run to avoid cross-test pollution.
	prefix := fmt.Sprintf("it%d", time.Now().UnixNano()%1_000_000_000_000)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return NewQueueStore(c.VK(), c, clock.System{}, b)
}

// TestQueueStore_JoinGaplessSeq proves that join.lua maintains a gapless monotonic seq under 10,000 concurrent
// anonymous joins (TC-QUE-08, Property 1).
func TestQueueStore_JoinGaplessSeq(t *testing.T) {
	const n = 10_000
	store := newTestStore(t)
	ctx := context.Background()
	eventID := "evt-integration-seq"

	seqs := make(chan int64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			res, err := store.Join(ctx, outbound.JoinParams{
				EventID: eventID, EntryID: fmt.Sprintf("entry-%d", i), NewVisitorID: fmt.Sprintf("visitor-%d", i),
				NowMs: time.Now().UnixMilli(), OneActivePerVisitor: true,
			})
			if err == nil && res.Outcome == outbound.JoinCreated {
				seqs <- res.Seq
			}
		}(i)
	}
	wg.Wait()
	close(seqs)
	seen := make(map[int64]bool, n)
	for sq := range seqs {
		require.False(t, seen[sq], "seq %d diberikan dua kali", sq)
		seen[sq] = true
	}
	require.Len(t, seen, n)
	for i := int64(1); i <= n; i++ {
		require.True(t, seen[i], "seq %d hilang (tidak gapless)", i)
	}
}

func join(t *testing.T, s *QueueStore, ev string, p outbound.JoinParams) outbound.JoinResult {
	t.Helper()
	p.EventID = ev
	if p.EntryID == "" {
		p.EntryID = fmt.Sprintf("e-%d", time.Now().UnixNano())
	}
	if p.NewVisitorID == "" {
		p.NewVisitorID = fmt.Sprintf("nv-%d", time.Now().UnixNano())
	}
	p.NowMs, p.OneActivePerVisitor = time.Now().UnixMilli(), true
	if p.TokenVisitorID != "" && p.TokenGen == 0 {
		p.TokenGen = 1 // every token in the decision table is the visitor's first (still valid) generation
	}
	res, err := s.Join(context.Background(), p)
	require.NoError(t, err)
	return res
}

func endEntryNow(t *testing.T, s *QueueStore, ev, id string) {
	t.Helper()
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Hset().Key(s.kEntry(ev, id)).FieldValue().FieldValue("status", "ENDED").Build()).Error())
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Zrem().Key(s.kWaiting(ev)).Member(id).Build()).Error())
}

// ADR-030 decision table (TC-ANON-02) against the real script: T1, T2, T3, T5, T6, T9, T10, T11, T12, T14, T15, T16, T17, T18, T19, T19b.
func TestJoinLua_DecisionTable(t *testing.T) {
	s := newTestStore(t)

	t.Run("T1 anonymous first join creates an entry with a fresh visitor id", func(t *testing.T) {
		r := join(t, s, "ev-t1", outbound.JoinParams{EntryID: "e1", NewVisitorID: "nv1"})
		assert.Equal(t, outbound.JoinCreated, r.Outcome)
		assert.Equal(t, "nv1", r.VisitorID)
		assert.EqualValues(t, 1, r.Seq)
		raw, err := s.c.Do(context.Background(), s.c.B().Hgetall().Key(s.kEntry("ev-t1", "e1")).Build()).AsStrMap()
		require.NoError(t, err)
		for _, f := range []string{"buyerNikHash", "nikMasked", "emailEnc", "emailHash"} {
			assert.NotContains(t, raw, f, "no buyer data is stored")
		}
	})

	t.Run("T2 valid token of an active entry returns it, never a second entry", func(t *testing.T) {
		first := join(t, s, "ev-t2", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a"})
		again := join(t, s, "ev-t2", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", TokenVisitorID: "v-a"})
		assert.Equal(t, outbound.JoinExisting, again.Outcome)
		assert.Equal(t, first.EntryID, again.EntryID)
		z, err := s.c.Do(context.Background(), s.c.B().Zcard().Key(s.kWaiting("ev-t2")).Build()).ToInt64()
		require.NoError(t, err)
		assert.EqualValues(t, 1, z)
	})

	t.Run("T3 token of an ENDED entry creates a new entry that reuses the visitor id", func(t *testing.T) {
		first := join(t, s, "ev-t3", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a"})
		endEntryNow(t, s, "ev-t3", first.EntryID)
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.kVisitor("ev-t3", "v-a")).Build()).Error())
		again := join(t, s, "ev-t3", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", TokenVisitorID: "v-a"})
		assert.Equal(t, outbound.JoinCreated, again.Outcome)
		assert.Equal(t, "v-a", again.VisitorID)
		assert.NotEqual(t, first.EntryID, again.EntryID)
	})

	t.Run("T5/T6 account: first creates, a second device re-attaches to the same entry", func(t *testing.T) {
		a := join(t, s, "ev-t6", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a", AccountID: "acc"})
		assert.Equal(t, outbound.JoinCreated, a.Outcome)
		b := join(t, s, "ev-t6", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", AccountID: "acc"})
		assert.Equal(t, outbound.JoinReattach, b.Outcome)
		assert.Equal(t, a.EntryID, b.EntryID)
		assert.Equal(t, "v-a", b.VisitorID, "re-attach returns the entry's own visitor id")
		assert.Equal(t, a.Seq, b.Seq)
	})

	t.Run("T9 same token and same account is plain EXISTING", func(t *testing.T) {
		a := join(t, s, "ev-t9", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a", AccountID: "acc"})
		b := join(t, s, "ev-t9", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", AccountID: "acc", TokenVisitorID: "v-a"})
		assert.Equal(t, outbound.JoinExisting, b.Outcome)
		assert.Equal(t, a.EntryID, b.EntryID)
	})

	t.Run("T10 anonymous entry is associated with the account of a later bearer join", func(t *testing.T) {
		a := join(t, s, "ev-t10", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a"})
		b := join(t, s, "ev-t10", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", AccountID: "acc", TokenVisitorID: "v-a"})
		assert.Equal(t, outbound.JoinExisting, b.Outcome)
		assert.Equal(t, a.EntryID, b.EntryID)
		ent, _, _ := s.GetEntry(context.Background(), "ev-t10", a.EntryID)
		assert.Equal(t, "acc", ent.AccountID)
		// the account now owns exactly this entry
		c := join(t, s, "ev-t10", outbound.JoinParams{EntryID: "c", NewVisitorID: "v-c", AccountID: "acc"})
		assert.Equal(t, a.EntryID, c.EntryID)
	})

	t.Run("T11 account already queued and a different anonymous token: conflict, nothing changes", func(t *testing.T) {
		acc := join(t, s, "ev-t11", outbound.JoinParams{EntryID: "acc-entry", NewVisitorID: "v-acc", AccountID: "acc"})
		other := join(t, s, "ev-t11", outbound.JoinParams{EntryID: "anon-entry", NewVisitorID: "v-anon"})
		r := join(t, s, "ev-t11", outbound.JoinParams{EntryID: "x", NewVisitorID: "v-x", AccountID: "acc", TokenVisitorID: "v-anon"})
		assert.Equal(t, outbound.JoinAccountInQueue, r.Outcome)
		z, _ := s.c.Do(context.Background(), s.c.B().Zcard().Key(s.kWaiting("ev-t11")).Build()).ToInt64()
		assert.EqualValues(t, 2, z, "no entry created or removed")
		_, _ = acc, other
	})

	t.Run("T12 token of another account's entry is ignored (foreign), a new entry is created", func(t *testing.T) {
		b := join(t, s, "ev-t12", outbound.JoinParams{EntryID: "eb", NewVisitorID: "v-b", AccountID: "acc-b"})
		a := join(t, s, "ev-t12", outbound.JoinParams{EntryID: "ea", NewVisitorID: "v-a-new", AccountID: "acc-a", TokenVisitorID: "v-b"})
		assert.Equal(t, outbound.JoinCreated, a.Outcome)
		assert.NotEqual(t, b.EntryID, a.EntryID)
		assert.Equal(t, "v-a-new", a.VisitorID, "the foreign visitor id is not adopted")
		eb, _, _ := s.GetEntry(context.Background(), "ev-t12", b.EntryID)
		assert.Equal(t, "acc-b", eb.AccountID, "the other account's entry is untouched")
	})

	t.Run("T14 anonymous caller holding the token of an account-bound entry gets it back", func(t *testing.T) {
		b := join(t, s, "ev-t14", outbound.JoinParams{EntryID: "eb", NewVisitorID: "v-b", AccountID: "acc-b"})
		r := join(t, s, "ev-t14", outbound.JoinParams{EntryID: "x", NewVisitorID: "v-x", TokenVisitorID: "v-b"})
		assert.Equal(t, outbound.JoinExisting, r.Outcome)
		assert.Equal(t, b.EntryID, r.EntryID)
	})

	t.Run("T15 ended token plus bearer whose account has an active entry returns that entry", func(t *testing.T) {
		old := join(t, s, "ev-t15", outbound.JoinParams{EntryID: "old", NewVisitorID: "v-old"})
		endEntryNow(t, s, "ev-t15", old.EntryID)
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.kVisitor("ev-t15", "v-old")).Build()).Error())
		acc := join(t, s, "ev-t15", outbound.JoinParams{EntryID: "acc", NewVisitorID: "v-acc", AccountID: "acc"})
		r := join(t, s, "ev-t15", outbound.JoinParams{EntryID: "x", NewVisitorID: "v-x", AccountID: "acc", TokenVisitorID: "v-old"})
		assert.Equal(t, acc.EntryID, r.EntryID, "same entryId, never a second entry")
		z, _ := s.c.Do(context.Background(), s.c.B().Zcard().Key(s.kWaiting("ev-t15")).Build()).ToInt64()
		assert.EqualValues(t, 1, z)
	})

	t.Run("T16 Idempotency-Key replay returns the same entry", func(t *testing.T) {
		first := join(t, s, "ev-t16", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a", IdemKey: "idem-test-t16-replay", IdemTTL: time.Minute})
		again := join(t, s, "ev-t16", outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", IdemKey: "idem-test-t16-replay", IdemTTL: time.Minute})
		assert.Equal(t, outbound.JoinExisting, again.Outcome)
		assert.Equal(t, first.EntryID, again.EntryID)
		assert.Equal(t, first.VisitorID, again.VisitorID)
	})

	t.Run("T17 token dari event lain diterima di event baru; visitor id dan gen tetap sama", func(t *testing.T) {
		a := join(t, s, "ev-t17-A", outbound.JoinParams{EntryID: "eA", NewVisitorID: "v-t17"})
		assert.Equal(t, outbound.JoinCreated, a.Outcome)
		assert.EqualValues(t, 1, a.Gen)

		b := join(t, s, "ev-t17-B", outbound.JoinParams{EntryID: "eB", NewVisitorID: "v-t17-new", TokenVisitorID: "v-t17"})
		assert.Equal(t, outbound.JoinCreated, b.Outcome)
		assert.Equal(t, "v-t17", b.VisitorID, "visitor id harus sama, bukan visitor ID baru")
		assert.EqualValues(t, 1, b.Gen, "gen di Event B harus = tokGen, bukan hasil rotate")

		again := join(t, s, "ev-t17-A", outbound.JoinParams{EntryID: "eA2", NewVisitorID: "v-t17-x", TokenVisitorID: "v-t17"})
		assert.Equal(t, outbound.JoinExisting, again.Outcome)
		assert.Equal(t, a.EntryID, again.EntryID, "entry Event A masih dapat ditemukan")
	})

	t.Run("T18 token gen > 1 dari event lain diterima di event baru; gen di event baru = tokGen", func(t *testing.T) {
		a1 := join(t, s, "ev-t18-A", outbound.JoinParams{EntryID: "eA1", NewVisitorID: "v-t18"})
		assert.Equal(t, outbound.JoinCreated, a1.Outcome)
		assert.EqualValues(t, 1, a1.Gen)

		// rotasi gen di Event A ke 2: end entry lalu rejoin dengan token yang sama
		endEntryNow(t, s, "ev-t18-A", "eA1")
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.kVisitor("ev-t18-A", "v-t18")).Build()).Error())
		a2 := join(t, s, "ev-t18-A", outbound.JoinParams{EntryID: "eA2", NewVisitorID: "v-t18-x", TokenVisitorID: "v-t18"})
		assert.Equal(t, outbound.JoinCreated, a2.Outcome)
		assert.EqualValues(t, 2, a2.Gen, "gen di Event A seharusnya sudah dirotasi ke 2")

		// join Event B dengan token gen=2 dari Event A
		b := join(t, s, "ev-t18-B", outbound.JoinParams{EntryID: "eB", NewVisitorID: "v-t18-new", TokenVisitorID: "v-t18", TokenGen: 2})
		assert.Equal(t, outbound.JoinCreated, b.Outcome)
		assert.Equal(t, "v-t18", b.VisitorID, "visitor id harus sama")
		assert.EqualValues(t, 2, b.Gen, "gen di Event B harus = tokGen (2), bukan 1 atau hasil rotate")
	})

	t.Run("T19 guard: vtok+visitor expired tapi vlast masih ada -> EXISTING dikembalikan, bukan duplikat", func(t *testing.T) {
		join(t, s, "ev-t19-A", outbound.JoinParams{EntryID: "eA", NewVisitorID: "v-t19"})
		b1 := join(t, s, "ev-t19-B", outbound.JoinParams{EntryID: "eB1", NewVisitorID: "v-t19-new", TokenVisitorID: "v-t19"})
		assert.Equal(t, outbound.JoinCreated, b1.Outcome)

		// simulasi vtok + visitor key expired; vlast masih ada (TTL sama, tapi belum expired)
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.k("ev-t19-B", "vtok:v-t19")).Build()).Error())
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.kVisitor("ev-t19-B", "v-t19")).Build()).Error())

		// guard via vlast -> harus EXISTING, bukan entry baru
		b2 := join(t, s, "ev-t19-B", outbound.JoinParams{EntryID: "eB2", NewVisitorID: "v-t19-new2", TokenVisitorID: "v-t19"})
		assert.Equal(t, outbound.JoinExisting, b2.Outcome, "guard mencegah duplikat")
		assert.Equal(t, b1.EntryID, b2.EntryID, "entry yang sama dikembalikan")
		z, _ := s.c.Do(context.Background(), s.c.B().Zcard().Key(s.kWaiting("ev-t19-B")).Build()).ToInt64()
		assert.EqualValues(t, 1, z, "hanya satu entry aktif")
	})

	t.Run("T19b known-limitation: vtok+visitor+vlast semua expired -> entry baru dibuat", func(t *testing.T) {
		join(t, s, "ev-t19b-A", outbound.JoinParams{EntryID: "eA", NewVisitorID: "v-t19b"})
		b1 := join(t, s, "ev-t19b-B", outbound.JoinParams{EntryID: "eB1", NewVisitorID: "v-t19b-new", TokenVisitorID: "v-t19b"})
		assert.Equal(t, outbound.JoinCreated, b1.Outcome)

		// simulasi semua key expired bersamaan (window sangat sempit di produksi)
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.k("ev-t19b-B", "vtok:v-t19b")).Build()).Error())
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.kVisitor("ev-t19b-B", "v-t19b")).Build()).Error())
		require.NoError(t, s.c.Do(context.Background(), s.c.B().Del().Key(s.k("ev-t19b-B", "vlast:v-t19b")).Build()).Error())

		// remaining limitation: semua key expired -> entry baru (vtok dan vlast TTL-nya sama, sangat sempit terjadi)
		b2 := join(t, s, "ev-t19b-B", outbound.JoinParams{EntryID: "eB2", NewVisitorID: "v-t19b-new2", TokenVisitorID: "v-t19b"})
		assert.Equal(t, outbound.JoinCreated, b2.Outcome, "remaining limitation: semua key expired bersamaan")
		assert.NotEqual(t, b1.EntryID, b2.EntryID)
		z, _ := s.c.Do(context.Background(), s.c.B().Zcard().Key(s.kWaiting("ev-t19b-B")).Build()).ToInt64()
		assert.EqualValues(t, 2, z, "dua entry aktif — limitation tersisa (vtok+vlast harus expired bersamaan)")
	})
}

// S1: 1,000 parallel joins with the same valid token produce one entry; S3: 100 anonymous tokens plus one bearer
// associate exactly one and refuse the other 99 with a conflict; untokened joins are not deduplicated.
func TestJoinLua_ConcurrencyProofs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	t.Run("S1 same token", func(t *testing.T) {
		first := join(t, s, "ev-s1", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a"})
		var wg sync.WaitGroup
		out := make([]outbound.JoinResult, 1000)
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = join(t, s, "ev-s1", outbound.JoinParams{EntryID: fmt.Sprintf("x%d", i), NewVisitorID: fmt.Sprintf("nv%d", i), TokenVisitorID: "v-a"})
			}()
		}
		wg.Wait()
		for _, r := range out {
			assert.Equal(t, first.EntryID, r.EntryID)
		}
		z, _ := s.c.Do(ctx, s.c.B().Zcard().Key(s.kWaiting("ev-s1")).Build()).ToInt64()
		assert.EqualValues(t, 1, z)
	})

	t.Run("S2 untokened joins with the same bearer: one creates, the rest re-attach", func(t *testing.T) {
		var wg sync.WaitGroup
		out := make([]outbound.JoinResult, 100)
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = join(t, s, "ev-s2", outbound.JoinParams{EntryID: fmt.Sprintf("x%d", i), NewVisitorID: fmt.Sprintf("nv%d", i), AccountID: "acc"})
			}()
		}
		wg.Wait()
		created, reattach := 0, 0
		ids := map[string]bool{}
		for _, r := range out {
			ids[r.EntryID] = true
			switch r.Outcome {
			case outbound.JoinCreated:
				created++
			case outbound.JoinReattach:
				reattach++
			}
		}
		assert.Equal(t, 1, created)
		assert.Equal(t, 99, reattach)
		assert.Len(t, ids, 1, "everyone ends up on the same entry")
	})

	t.Run("S3 100 anonymous tokens plus one bearer", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			join(t, s, "ev-s3", outbound.JoinParams{EntryID: fmt.Sprintf("a%d", i), NewVisitorID: fmt.Sprintf("v%d", i)})
		}
		var wg sync.WaitGroup
		out := make([]outbound.JoinResult, 100)
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = join(t, s, "ev-s3", outbound.JoinParams{EntryID: fmt.Sprintf("x%d", i), NewVisitorID: fmt.Sprintf("nv%d", i), AccountID: "acc", TokenVisitorID: fmt.Sprintf("v%d", i)})
			}()
		}
		wg.Wait()
		conflict, existing := 0, 0
		for _, r := range out {
			switch r.Outcome {
			case outbound.JoinAccountInQueue:
				conflict++
			case outbound.JoinExisting:
				existing++
			}
		}
		assert.Equal(t, 1, existing, "exactly one entry is associated with the account")
		assert.Equal(t, 99, conflict)
		z, _ := s.c.Do(ctx, s.c.B().Zcard().Key(s.kWaiting("ev-s3")).Build()).ToInt64()
		assert.EqualValues(t, 100, z, "still 100 active entries")
	})

	t.Run("untokened joins are not deduplicated", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			r := join(t, s, "ev-nodedupe", outbound.JoinParams{EntryID: fmt.Sprintf("x%d", i), NewVisitorID: fmt.Sprintf("nv%d", i)})
			assert.Equal(t, outbound.JoinCreated, r.Outcome)
		}
	})
}

// TC-QUE-40 (P15): the sale window and the sold-out flag share one hash but neither writer clobbers the other.
func TestQueueStore_SaleWindow(t *testing.T) {
	ctx := context.Background()
	const starts, ends = int64(1_700_000_000_000), int64(1_700_003_600_000)

	t.Run("missing hash gives unknown window", func(t *testing.T) {
		s := newTestStore(t)
		got, err := s.GetSale(ctx, evID(t))
		require.NoError(t, err)
		assert.Equal(t, outbound.SaleState{}, got)
	})
	t.Run("window then SetSale: both survive", func(t *testing.T) {
		s := newTestStore(t)
		ev := evID(t)
		require.NoError(t, s.SetSaleWindow(ctx, ev, starts, ends))
		require.NoError(t, s.SetSale(ctx, ev, "CLOSED", true))
		got, err := s.GetSale(ctx, ev)
		require.NoError(t, err)
		assert.Equal(t, outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: starts, SalesEndsMs: ends}, got)
	})
	t.Run("SetSale then window: both survive", func(t *testing.T) {
		s := newTestStore(t)
		ev := evID(t)
		require.NoError(t, s.SetSale(ctx, ev, "CLOSED", true))
		require.NoError(t, s.SetSaleWindow(ctx, ev, starts, ends))
		got, err := s.GetSale(ctx, ev)
		require.NoError(t, err)
		assert.Equal(t, outbound.SaleState{SaleState: "CLOSED", SoldOut: true, SalesStartsMs: starts, SalesEndsMs: ends}, got)
	})
	t.Run("window rewrite replaces only the window", func(t *testing.T) {
		s := newTestStore(t)
		ev := evID(t)
		require.NoError(t, s.SetSale(ctx, ev, "OPEN", false))
		require.NoError(t, s.SetSaleWindow(ctx, ev, starts, ends))
		require.NoError(t, s.SetSaleWindow(ctx, ev, starts+1, ends+1))
		got, err := s.GetSale(ctx, ev)
		require.NoError(t, err)
		assert.Equal(t, outbound.SaleState{SaleState: "OPEN", SalesStartsMs: starts + 1, SalesEndsMs: ends + 1}, got)
	})
}
