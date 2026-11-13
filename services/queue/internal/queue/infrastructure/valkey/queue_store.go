// Package valkey implements the Queue hot-state store and rate limiter on Valkey.
// Lua scripts provide atomicity for join/promote/claim/leave/end (design §4); all other
// business logic stays in Go (.kiro/steering/code-principles.md).
package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	vk "github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/queueshard"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/assets/lua"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Keyer applies the environment namespace (implemented by pkg/valkey.Client).
type Keyer interface {
	Key(k string) string
}

// QueueStore implements outbound.IQueueStore. It caches Lua scripts as valkey-go Script helpers
// (EVALSHA with EVAL fallback on NOSCRIPT).
type QueueStore struct {
	c      vk.Client
	keyer  Keyer
	clk    clock.IClock
	shards int

	join       *vk.Lua
	promote    *vk.Lua
	claim      *vk.Lua
	leave      *vk.Lua
	endEntry   *vk.Lua
	endSoldOut *vk.Lua

	releaseLease *vk.Lua

	heartbeat    *vk.Lua
	liveness     *vk.Lua
	livenessTick *vk.Lua

	tokenGen *vk.Lua

	retireEvent  *vk.Lua
	notifyWindow *vk.Lua
}

// NewQueueStore returns the store with the embedded scripts loaded. shards is the liveness-index shard count
// (QUEUE_LIVENESS_SHARDS); values <= 0 collapse to a single shard (pre-sharding behaviour).
func NewQueueStore(c vk.Client, keyer Keyer, clk clock.IClock, shards int) *QueueStore {
	if shards < 1 {
		shards = 1
	}
	return &QueueStore{
		c: c, keyer: keyer, clk: clk, shards: shards,
		join:       vk.NewLuaScript(lua.Join),
		promote:    vk.NewLuaScript(lua.Promote),
		claim:      vk.NewLuaScript(lua.Claim),
		leave:      vk.NewLuaScript(lua.Leave),
		endEntry:   vk.NewLuaScript(lua.EndEntry),
		endSoldOut: vk.NewLuaScript(lua.EndSoldOut),

		releaseLease: vk.NewLuaScript(lua.ReleaseLease),

		heartbeat:    vk.NewLuaScript(lua.Heartbeat),
		liveness:     vk.NewLuaScript(lua.Liveness),
		livenessTick: vk.NewLuaScript(lua.LivenessTick),

		tokenGen: vk.NewLuaScript(lua.TokenGen),

		retireEvent:  vk.NewLuaScript(lua.RetireEvent),
		notifyWindow: vk.NewLuaScript(lua.NotifyWindow),
	}
}

// --- key builders (design §3.1; hash tag {e:<eventId>} keeps a script's keys in one slot) ---

func (s *QueueStore) tag(eventID string) string { return "q:{e:" + eventID + "}" }

// prefix is the per-event key prefix scripts use to build the keys they touch (all in the event's slot).
func (s *QueueStore) prefix(e string) string      { return s.keyer.Key(s.tag(e) + ":") }
func (s *QueueStore) k(e, name string) string     { return s.prefix(e) + name }
func (s *QueueStore) kSeq(e string) string        { return s.keyer.Key(s.tag(e) + ":seq") }
func (s *QueueStore) kWaiting(e string) string    { return s.keyer.Key(s.tag(e) + ":waiting") }
func (s *QueueStore) kActive(e string) string     { return s.keyer.Key(s.tag(e) + ":active") }
func (s *QueueStore) kEntry(e, id string) string  { return s.keyer.Key(s.tag(e) + ":entry:" + id) }
func (s *QueueStore) kAccount(e, a string) string { return s.keyer.Key(s.tag(e) + ":account:" + a) }
func (s *QueueStore) kVisitor(e, v string) string { return s.keyer.Key(s.tag(e) + ":visitor:" + v) }

// kUnused names an inert slot of the leave/end scripts (the old email constraint key; ADR-030 has no email).
func (s *QueueStore) kUnused(e string) string { return s.keyer.Key(s.tag(e) + ":unused") }
func (s *QueueStore) kStatus(e string) string { return s.keyer.Key(s.tag(e) + ":status") }
func (s *QueueStore) kSale(e string) string   { return s.keyer.Key(s.tag(e) + ":sale") }
func (s *QueueStore) kLease(e string) string  { return s.keyer.Key(s.tag(e) + ":promoter:lock") }

// kNotifySeq is the pre-turn notify watermark (C8.3): the highest seq whose waiting entries were announced.
func (s *QueueStore) kNotifySeq(e string) string { return s.keyer.Key(s.tag(e) + ":notify_seq") }

// shardOf returns the entry's liveness shard index as a string, deterministic per entry id.
func (s *QueueStore) shardOf(entryID string) string {
	return strconv.Itoa(queueshard.Shard(entryID, s.shards))
}

// kHbShard / kHbStaleShard are the per-shard liveness index keys (ADR-030 Amendment: hb/hbstale are sharded so the
// promoter tick and the heartbeat writes no longer contend on one key).
func (s *QueueStore) kHbShard(e, shard string) string {
	return s.keyer.Key(s.tag(e) + ":hb:" + shard)
}
func (s *QueueStore) kHbStaleShard(e, shard string) string {
	return s.keyer.Key(s.tag(e) + ":hbstale:" + shard)
}

func bstr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// retentionMs is the retention ARGV the terminal-writing scripts take as their last argument (D-C8.2).
func retentionMs() string { return strconv.FormatInt(outbound.EntryRetention.Milliseconds(), 10) }

// Join runs join.lua atomically and maps the array reply to a JoinResult (ADR-030: anonymous join).
func (s *QueueStore) Join(ctx context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
	// Idempotency scope (C6.2, P1-13): an account or a verified visitor token scopes by its id; a tokenless guest scopes
	// by its device hash or keyed client IP. A guest without a scope gets no idempotency key, so nothing is replayed.
	idem := ""
	if p.IdemKey != "" {
		switch {
		case p.AccountID != "":
			idem = "a:" + p.AccountID + ":" + p.IdemKey
		case p.TokenVisitorID != "":
			idem = "v:" + p.TokenVisitorID + ":" + p.IdemKey
		case p.GuestScope != "":
			idem = "n:" + p.GuestScope + ":" + p.IdemKey
		}
	}
	idemTTL := int(p.IdemTTL.Seconds())
	if idemTTL < 1 {
		idemTTL = 600
	}
	tokTTL := int(p.TokenTTL.Seconds())
	if tokTTL < 1 {
		tokTTL = 24 * 3600
	}
	shard := s.shardOf(p.EntryID)
	args := []string{
		s.prefix(p.EventID), p.EntryID, p.NewVisitorID, p.TokenVisitorID, strconv.FormatInt(p.TokenGen, 10), p.AccountID,
		idem, strconv.FormatInt(p.NowMs, 10), bstr(p.OneActivePerVisitor), strconv.Itoa(idemTTL), strconv.Itoa(tokTTL),
		shard, s.kHbShard(p.EventID, shard),
	}
	// The declared key keeps the script routed to the event's slot; the script builds the rest from the prefix.
	arr, err := s.join.Exec(ctx, s.c, []string{s.kSeq(p.EventID)}, args).ToArray()
	if err != nil {
		return outbound.JoinResult{}, fmt.Errorf("valkey: join.lua: %w", err)
	}
	return mapJoin(arr)
}

func mapJoin(arr []vk.ValkeyMessage) (outbound.JoinResult, error) {
	if len(arr) < 6 {
		return outbound.JoinResult{}, fmt.Errorf("valkey: join.lua balasan tak terduga (%d elemen)", len(arr))
	}
	gen, _ := arr[5].AsInt64()
	tag, _ := arr[0].ToString()
	res := outbound.JoinResult{Outcome: outbound.JoinOutcome(tag)}
	res.EntryID, _ = arr[1].ToString()
	seq, _ := arr[2].ToString()
	res.Seq, _ = strconv.ParseInt(seq, 10, 64)
	res.VisitorID, _ = arr[3].ToString()
	res.Status, _ = arr[4].ToString()
	res.Gen = gen
	switch res.Outcome {
	case outbound.JoinCreated, outbound.JoinExisting, outbound.JoinReattach, outbound.JoinAccountInQueue:
		return res, nil
	}
	return outbound.JoinResult{}, fmt.Errorf("valkey: join.lua tag tak dikenal %q", tag)
}

// Promote moves reserved candidates waiting→active atomically, fenced by the promoter lease token
// (design §5.4, ADR-029). An empty token is an error before any Valkey call: an unfenced promote is a defect.
func (s *QueueStore) Promote(ctx context.Context, eventID string, cands []outbound.PromoteCandidate, capacity int, nowMs int64, opts outbound.PromoteOpts) (outbound.PromoteResult, error) {
	if opts.LeaseToken == "" {
		return outbound.PromoteResult{}, errors.New("valkey: promote: LeaseToken wajib (fence promoter)")
	}
	if len(cands) == 0 {
		return outbound.PromoteResult{}, nil
	}
	keys := []string{s.kWaiting(eventID), s.kActive(eventID), s.kLease(eventID)}
	for _, c := range cands {
		keys = append(keys, s.kEntry(eventID, c.EntryID))
	}
	args := []string{strconv.Itoa(capacity), strconv.FormatInt(nowMs, 10), bstr(opts.RequireHead), opts.LeaseToken}
	for _, c := range cands {
		shard := s.shardOf(c.EntryID)
		args = append(args, c.EntryID, c.AllocationID, strconv.FormatInt(c.ClaimDeadlineMs, 10), s.kHbShard(eventID, shard))
	}
	arr, err := s.promote.Exec(ctx, s.c, keys, args).ToArray()
	if err != nil {
		return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua: %w", err)
	}
	return mapPromote(arr)
}

// mapPromote parses the promote.lua reply. Anything that is not a documented shape is an error: swallowing it
// as "nothing promoted" would strand a RESERVED allocation without anyone noticing (Q-03).
func mapPromote(arr []vk.ValkeyMessage) (outbound.PromoteResult, error) {
	if len(arr) == 0 {
		return outbound.PromoteResult{}, errors.New("valkey: promote.lua balasan kosong")
	}
	tag, _ := arr[0].ToString()
	switch tag {
	case "LEASE_LOST":
		return outbound.PromoteResult{LeaseLost: true}, nil
	case "FULL":
		return outbound.PromoteResult{Full: true}, nil
	case "OK":
	default:
		return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua tag tak dikenal %q", tag)
	}
	if len(arr) < 3 {
		return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua balasan OK terlalu pendek (%d)", len(arr))
	}
	promotedArr, err := arr[1].ToArray()
	if err != nil {
		return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua daftar promoted: %w", err)
	}
	skippedArr, err := arr[2].ToArray()
	if err != nil {
		return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua daftar skipped: %w", err)
	}
	res := outbound.PromoteResult{}
	for _, m := range promotedArr {
		id, err := m.ToString()
		if err != nil {
			return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua id promoted: %w", err)
		}
		res.Promoted = append(res.Promoted, id)
	}
	for _, m := range skippedArr {
		f, err := m.ToArray()
		if err != nil || len(f) < 2 {
			return outbound.PromoteResult{}, fmt.Errorf("valkey: promote.lua baris skipped tidak valid")
		}
		sk := outbound.PromoteSkip{}
		sk.EntryID, _ = f[0].ToString()
		sk.Status, _ = f[1].ToString()
		if len(f) >= 3 {
			sk.AllocationID, _ = f[2].ToString()
		}
		res.Skipped = append(res.Skipped, sk)
	}
	return res, nil
}

// Claim runs claim.lua (design §5.3). Maps ERR tags to domain errors; GONE (terminal) is reported as not found.
// claimMaxMs bounds the CLAIMED slot (C6.5, P2-15).
func (s *QueueStore) Claim(ctx context.Context, eventID, entryID, visitorID string, nowMs, claimMaxMs int64) (outbound.ClaimResult, error) {
	keys := []string{s.kEntry(eventID, entryID), s.kActive(eventID)}
	args := []string{entryID, strconv.FormatInt(nowMs, 10), visitorID, strconv.FormatInt(claimMaxMs, 10), retentionMs()}
	arr, err := s.claim.Exec(ctx, s.c, keys, args).ToArray()
	if err != nil {
		return outbound.ClaimResult{}, fmt.Errorf("valkey: claim.lua: %w", err)
	}
	if len(arr) < 2 {
		return outbound.ClaimResult{}, fmt.Errorf("valkey: claim.lua balasan tak terduga")
	}
	tag, _ := arr[0].ToString()
	detail, _ := arr[1].ToString()
	if tag == "ERR" {
		switch detail {
		case "NOT_FOUND", "GONE":
			return outbound.ClaimResult{}, entity.ErrNotFound
		case "NOT_ADMITTED":
			return outbound.ClaimResult{}, entity.ErrNotAdmitted
		case "TURN_MISSED":
			return outbound.ClaimResult{}, entity.ErrTurnMissed
		case "SOLD_OUT":
			return outbound.ClaimResult{}, entity.ErrSoldOut
		}
		return outbound.ClaimResult{}, fmt.Errorf("valkey: claim.lua err %q", detail)
	}
	res := outbound.ClaimResult{Status: detail}
	if len(arr) >= 3 {
		res.AllocationID, _ = arr[2].ToString()
	}
	return res, nil
}

// Leave runs leave.lua (design §5.2). Ownership and status are decided inside the script; the Go pre-read only
// derives the immutable constraint-key names. NOT_FOUND (also for a wrong owner) is entity.ErrNotFound.
func (s *QueueStore) Leave(ctx context.Context, eventID, entryID, visitorID string, nowMs int64) (outbound.LeaveResult, error) {
	ent, ok, err := s.GetEntry(ctx, eventID, entryID)
	if err != nil {
		return outbound.LeaveResult{}, err
	}
	// leave.lua deletes each constraint key only while it still points at this entry. The NIK/email slots are gone
	// (ADR-030); they now carry the account key and an inert placeholder so the script stays unchanged.
	nikKey, emailKey := s.kAccount(eventID, ""), s.kUnused(eventID)
	if ok {
		nikKey = s.kAccount(eventID, ent.AccountID)
	}
	keys := []string{
		s.kEntry(eventID, entryID), s.kWaiting(eventID), s.kActive(eventID),
		nikKey, s.kVisitor(eventID, visitorID), emailKey,
	}
	shard := s.shardOf(entryID)
	args := []string{
		entryID, visitorID, strconv.FormatInt(nowMs, 10), s.prefix(eventID),
		s.kHbShard(eventID, shard), s.kHbStaleShard(eventID, shard), retentionMs(),
	}
	arr, err := s.leave.Exec(ctx, s.c, keys, args).ToArray()
	if err != nil {
		return outbound.LeaveResult{}, fmt.Errorf("valkey: leave.lua: %w", err)
	}
	if len(arr) < 2 {
		return outbound.LeaveResult{}, fmt.Errorf("valkey: leave.lua balasan tak terduga")
	}
	tag, _ := arr[0].ToString()
	second, _ := arr[1].ToString()
	switch tag {
	case "ERR":
		return outbound.LeaveResult{}, entity.ErrNotFound
	case "IGNORED":
		return outbound.LeaveResult{Tag: "IGNORED", PrevStatus: second}, nil
	case "OK":
		res := outbound.LeaveResult{Tag: "OK", PrevStatus: second}
		if len(arr) >= 3 {
			res.AllocationID, _ = arr[2].ToString()
		}
		return res, nil
	}
	return outbound.LeaveResult{}, fmt.Errorf("valkey: leave.lua tag tak dikenal %q", tag)
}

// EndEntry runs end_entry.lua for the booking.journey.ended consumer (design §4.5). Idempotent.
func (s *QueueStore) EndEntry(ctx context.Context, eventID, entryID, outcome string, nowMs int64) (outbound.EndResult, error) {
	ent, _, err := s.GetEntry(ctx, eventID, entryID)
	if err != nil {
		return outbound.EndResult{}, err
	}
	keys := []string{
		s.kEntry(eventID, entryID), s.kActive(eventID),
		s.kAccount(eventID, ent.AccountID), s.kVisitor(eventID, ent.VisitorID), s.kUnused(eventID),
		s.kWaiting(eventID),
	}
	shard := s.shardOf(entryID)
	args := []string{
		entryID, outcome, strconv.FormatInt(nowMs, 10), s.prefix(eventID),
		s.kHbShard(eventID, shard), s.kHbStaleShard(eventID, shard), retentionMs(),
	}
	arr, err := s.endEntry.Exec(ctx, s.c, keys, args).ToArray()
	if err != nil {
		return outbound.EndResult{}, fmt.Errorf("valkey: end_entry.lua: %w", err)
	}
	if len(arr) < 2 {
		return outbound.EndResult{}, nil
	}
	tag, _ := arr[0].ToString()
	final, _ := arr[1].ToString()
	return outbound.EndResult{Tag: tag, Status: final}, nil
}

// EndWaitingSoldOut ends up to batch WAITING/STALE entries of the event with status SOLD_OUT (end_sold_out.lua).
// It returns how many ids were scanned (the batch size when more may remain) and how many entries changed.
// ADMITTED/CLAIMED entries are never touched. Idempotent: ended entries leave the waiting/stale sets.
func (s *QueueStore) EndWaitingSoldOut(ctx context.Context, eventID string, nowMs int64, batch int) (outbound.SoldOutBatch, error) {
	if batch <= 0 {
		return outbound.SoldOutBatch{}, fmt.Errorf("valkey: EndWaitingSoldOut batch harus positif, dapat %d", batch)
	}
	ids, err := s.TopWaiting(ctx, eventID, batch)
	if err != nil {
		return outbound.SoldOutBatch{}, err
	}
	if rest := batch - len(ids); rest > 0 {
		stale, err := s.c.Do(ctx, s.c.B().Zrange().Key(s.k(eventID, "stale")).Min("0").Max("+inf").
			Byscore().Limit(0, int64(rest)).Build()).AsStrSlice()
		if err != nil {
			return outbound.SoldOutBatch{}, fmt.Errorf("valkey: zrangebyscore stale: %w", err)
		}
		ids = append(ids, stale...)
	}
	if len(ids) == 0 {
		return outbound.SoldOutBatch{}, nil
	}
	args := []string{s.prefix(eventID), strconv.FormatInt(nowMs, 10)}
	for _, id := range ids {
		shard := s.shardOf(id)
		args = append(args, id, s.kHbShard(eventID, shard), s.kHbStaleShard(eventID, shard))
	}
	args = append(args, retentionMs())
	arr, err := s.endSoldOut.Exec(ctx, s.c, []string{s.kSeq(eventID)}, args).ToArray()
	if err != nil {
		return outbound.SoldOutBatch{}, fmt.Errorf("valkey: end_sold_out.lua: %w", err)
	}
	ended := 0
	if len(arr) > 0 {
		n, _ := arr[0].AsInt64()
		ended = int(n)
	}
	return outbound.SoldOutBatch{Scanned: len(ids), Ended: ended}, nil
}

// GetEntry reads the entry hash (design §3.2).
func (s *QueueStore) GetEntry(ctx context.Context, eventID, entryID string) (entity.Entry, bool, error) {
	m, err := s.c.Do(ctx, s.c.B().Hgetall().Key(s.kEntry(eventID, entryID)).Build()).AsStrMap()
	if err != nil {
		return entity.Entry{}, false, fmt.Errorf("valkey: hgetall entry: %w", err)
	}
	if len(m) == 0 {
		return entity.Entry{}, false, nil
	}
	return hashToEntry(entryID, m), true, nil
}

// FindByVisitor returns the active entry for a visitor, if the constraint key points to one.
func (s *QueueStore) FindByVisitor(ctx context.Context, eventID, visitorID string) (entity.Entry, bool, error) {
	id, err := s.c.Do(ctx, s.c.B().Get().Key(s.kVisitor(eventID, visitorID)).Build()).ToString()
	if vk.IsValkeyNil(err) {
		return entity.Entry{}, false, nil
	}
	if err != nil {
		return entity.Entry{}, false, fmt.Errorf("valkey: get visitor: %w", err)
	}
	return s.GetEntry(ctx, eventID, id)
}

// ActiveCount returns the number of entries currently in the active set.
func (s *QueueStore) ActiveCount(ctx context.Context, eventID string) (int, error) {
	n, err := s.c.Do(ctx, s.c.B().Zcard().Key(s.kActive(eventID)).Build()).ToInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: zcard active: %w", err)
	}
	return int(n), nil
}

// TopWaiting returns up to n waiting entry ids ordered by seq ascending (FIFO).
func (s *QueueStore) TopWaiting(ctx context.Context, eventID string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	ids, err := s.c.Do(ctx, s.c.B().Zrange().Key(s.kWaiting(eventID)).Min("0").Max("+inf").
		Byscore().Limit(0, int64(n)).Build()).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("valkey: zrangebyscore waiting: %w", err)
	}
	return ids, nil
}

// ExpiredActive returns at most limit active entry ids whose claim deadline is at or before nowMs, oldest first.
// limit must be positive (REQ-QSH-11.2): an unbounded scan would let a poison page grow without bound.
func (s *QueueStore) ExpiredActive(ctx context.Context, eventID string, nowMs int64, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("valkey: ExpiredActive limit harus positif, dapat %d", limit)
	}
	ids, err := s.c.Do(ctx, s.c.B().Zrange().Key(s.kActive(eventID)).Min("0").
		Max(strconv.FormatInt(nowMs, 10)).Byscore().Limit(0, int64(limit)).Build()).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("valkey: zrangebyscore active expired: %w", err)
	}
	return ids, nil
}

// AdvanceNotifyWatermark moves the event's pre-turn notify watermark up to upToSeq and returns the value it had before
// (D-C8.3). The caller announces the live waiting entries with seq in (previous, upToSeq]. notify_window.lua reads and
// writes in one step, so concurrent advances hand out disjoint windows: each seq is announced at most once.
func (s *QueueStore) AdvanceNotifyWatermark(ctx context.Context, eventID string, upToSeq int64) (int64, error) {
	prev, err := s.notifyWindow.Exec(ctx, s.c, []string{s.kNotifySeq(eventID)}, []string{strconv.FormatInt(upToSeq, 10)}).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: notify_window.lua: %w", err)
	}
	return prev, nil
}

// WaitingBetween returns up to limit live waiting ids whose seq is in (afterSeq, upToSeq], FIFO by seq.
func (s *QueueStore) WaitingBetween(ctx context.Context, eventID string, afterSeq, upToSeq int64, limit int) ([]string, error) {
	if limit <= 0 || upToSeq <= afterSeq {
		return nil, nil
	}
	ids, err := s.c.Do(ctx, s.c.B().Zrange().Key(s.kWaiting(eventID)).
		Min("("+strconv.FormatInt(afterSeq, 10)).Max(strconv.FormatInt(upToSeq, 10)).
		Byscore().Limit(0, int64(limit)).Build()).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("valkey: zrangebyscore waiting window: %w", err)
	}
	return ids, nil
}

// SetSale writes the sale-state hash.
func (s *QueueStore) SetSale(ctx context.Context, eventID, saleState string, soldOut bool) error {
	return s.c.Do(ctx, s.c.B().Hset().Key(s.kSale(eventID)).FieldValue().
		FieldValue("saleState", saleState).FieldValue("soldOut", bstr(soldOut)).Build()).Error()
}

// GetSale reads the sale-state hash. Missing hash → zero SaleState (caller decides defaults).
func (s *QueueStore) GetSale(ctx context.Context, eventID string) (outbound.SaleState, error) {
	m, err := s.c.Do(ctx, s.c.B().Hgetall().Key(s.kSale(eventID)).Build()).AsStrMap()
	if err != nil {
		return outbound.SaleState{}, fmt.Errorf("valkey: hgetall sale: %w", err)
	}
	st := outbound.SaleState{SaleState: m["saleState"], SoldOut: m["soldOut"] == "1"}
	st.SalesStartsMs = parseMs(m["salesStartsMs"])
	st.SalesEndsMs = parseMs(m["salesEndsMs"])
	return st, nil
}

// SetSaleWindow caches the sale window in the same hash as SetSale. It writes only its own two fields, so neither
// writer can clobber the other.
func (s *QueueStore) SetSaleWindow(ctx context.Context, eventID string, startsMs, endsMs int64) error {
	return s.c.Do(ctx, s.c.B().Hset().Key(s.kSale(eventID)).FieldValue().
		FieldValue("salesStartsMs", strconv.FormatInt(startsMs, 10)).
		FieldValue("salesEndsMs", strconv.FormatInt(endsMs, 10)).Build()).Error()
}

// SetStatusDoc writes the Status Document JSON with a short TTL (design §3.1: 1s).
func (s *QueueStore) SetStatusDoc(ctx context.Context, eventID string, doc []byte, ttl time.Duration) error {
	return s.c.Do(ctx, s.c.B().Set().Key(s.kStatus(eventID)).Value(string(doc)).Px(ttl).Build()).Error()
}

// GetStatusDoc reads the cached Status Document JSON.
func (s *QueueStore) GetStatusDoc(ctx context.Context, eventID string) ([]byte, bool, error) {
	v, err := s.c.Do(ctx, s.c.B().Get().Key(s.kStatus(eventID)).Build()).AsBytes()
	if vk.IsValkeyNil(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("valkey: get status: %w", err)
	}
	return v, true, nil
}

func hashToEntry(id string, m map[string]string) entity.Entry {
	e := entity.Entry{ID: id, VisitorID: m["visitorId"], AccountID: m["accountId"], AllocationID: m["allocationId"]}
	if seq, err := strconv.ParseInt(m["seq"], 10, 64); err == nil {
		e.Seq = valueobject.Seq(seq)
	}
	e.Status = valueobject.Status(m["status"])
	e.LastSignalMs = parseMs(m["lastSignalMs"])
	e.EndReason = m["endReason"]
	e.JoinedAtMs = parseMs(m["joinedAtMs"])
	e.AdmittedAtMs = parseMs(m["admittedAtMs"])
	e.ClaimedAtMs = parseMs(m["claimedAtMs"])
	e.ClaimDeadlineMs = parseMs(m["claimDeadlineMs"])
	return e
}

func parseMs(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// AcquirePromoterLease tries to set the per-event promoter lease (SET NX PX) with a fresh random token as the
// value, so release and the promote.lua fence can tell the holder from a later holder (design §5.5, ADR-029).
func (s *QueueStore) AcquirePromoterLease(ctx context.Context, eventID string, ttl time.Duration) (string, bool, error) {
	tok, err := newLeaseToken()
	if err != nil {
		return "", false, err
	}
	res, err := s.c.Do(ctx, s.c.B().Set().Key(s.kLease(eventID)).Value(tok).Nx().Px(ttl).Build()).ToString()
	if vk.IsValkeyNil(err) {
		return "", false, nil // another instance holds the lease
	}
	if err != nil {
		return "", false, fmt.Errorf("valkey: acquire_promoter_lease: %w", err)
	}
	if res != "OK" {
		return "", false, nil
	}
	return tok, true, nil
}

// ReleasePromoterLease deletes the lease only if token still owns it (compare-and-delete). A stale holder whose TTL
// lapsed must never delete the lease of whoever acquired it afterwards.
func (s *QueueStore) ReleasePromoterLease(ctx context.Context, eventID, token string) error {
	if token == "" {
		return errors.New("valkey: release_promoter_lease: token kosong")
	}
	if err := s.releaseLease.Exec(ctx, s.c, []string{s.kLease(eventID)}, []string{token}).Error(); err != nil {
		return fmt.Errorf("valkey: release_promoter_lease: %w", err)
	}
	return nil
}

func newLeaseToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("valkey: lease token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- ADR-030: liveness, token generation and FIFO-by-seq positions ---

// Heartbeat runs heartbeat.lua. The script resolves the entry from vlast and addresses hb/hbstale by the shard stored
// on that entry, so a shard-count change never strands a live entry. Go still reads vlast to derive the legacy
// fallback keys (ARGV[5]/[6]) for an entry joined before the shard field existed; the script ignores them otherwise.
func (s *QueueStore) Heartbeat(ctx context.Context, eventID, visitorID string, gen, nowMs int64) (outbound.HeartbeatResult, error) {
	entryID, err := s.c.Do(ctx, s.c.B().Get().Key(s.k(eventID, "vlast:"+visitorID)).Build()).ToString()
	if err != nil && !vk.IsValkeyNil(err) {
		return outbound.HeartbeatResult{}, fmt.Errorf("valkey: get vlast: %w", err)
	}
	shard := s.shardOf(entryID)
	arr, err := s.heartbeat.Exec(ctx, s.c, []string{s.kSeq(eventID)},
		[]string{
			s.prefix(eventID), visitorID, strconv.FormatInt(gen, 10), strconv.FormatInt(nowMs, 10),
			s.kHbShard(eventID, shard), s.kHbStaleShard(eventID, shard),
		}).ToArray()
	if err != nil {
		return outbound.HeartbeatResult{}, fmt.Errorf("valkey: heartbeat.lua: %w", err)
	}
	tag, _ := arr[0].ToString()
	res := outbound.HeartbeatResult{Tag: outbound.HeartbeatTag(tag)}
	if res.Tag == outbound.HeartbeatOK && len(arr) >= 5 {
		res.EntryID, _ = arr[1].ToString()
		res.Status, _ = arr[2].ToString()
		res.Seq, _ = arr[3].AsInt64()
		rv, _ := arr[4].AsInt64()
		res.Revived = rv == 1
	}
	return res, nil
}

// CheckTokenGen runs token_gen.lua on the visitor's vtok key of the event (D-C6.1): the key is in the event's hash slot,
// and the script ratchets a higher generation up (KEEPTTL) or refuses a lower one.
func (s *QueueStore) CheckTokenGen(ctx context.Context, eventID, visitorID string, gen int64) (outbound.TokenGenCheck, error) {
	n, err := s.tokenGen.Exec(ctx, s.c, []string{s.k(eventID, "vtok:"+visitorID)}, []string{strconv.FormatInt(gen, 10)}).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: token_gen.lua: %w", err)
	}
	switch n {
	case 1:
		return outbound.TokenGenOK, nil
	case 0:
		return outbound.TokenGenInvalid, nil
	case -1:
		return outbound.TokenGenAbsent, nil
	}
	return 0, fmt.Errorf("valkey: token_gen.lua: unexpected reply %d", n)
}

// FindLastByVisitor follows vlast:<visitor> to the most recent entry in any state.
func (s *QueueStore) FindLastByVisitor(ctx context.Context, eventID, visitorID string) (entity.Entry, bool, error) {
	id, err := s.c.Do(ctx, s.c.B().Get().Key(s.k(eventID, "vlast:"+visitorID)).Build()).ToString()
	if vk.IsValkeyNil(err) {
		return entity.Entry{}, false, nil
	}
	if err != nil {
		return entity.Entry{}, false, fmt.Errorf("valkey: get vlast: %w", err)
	}
	return s.GetEntry(ctx, eventID, id)
}

// SweepLiveness runs liveness.lua once per shard under the lease token (ADR-030 Amendment: the hb/hbstale index is
// sharded, so the per-tick sweep fans out over all shards instead of contending on one key). The lease fence is
// re-checked at the start of every shard call; a lost lease aborts the whole fan-out. batch bounds the work PER
// SHARD (so up to shards*batch entries can be swept per tick).
func (s *QueueStore) SweepLiveness(ctx context.Context, eventID, leaseToken string, staleBeforeMs, endBeforeMs int64, batch int, nowMs int64) (outbound.LivenessResult, error) {
	// One EVAL sweeps ALL shards (the script loops s=0..B-1). Fanning out as B separate EVALs multiplied the
	// promoter-tick latency because every round-trip queues behind the heartbeat traffic on Valkey's single command
	// thread (design-change §4.3); a single round-trip keeps the tick within +20 % of base under load.
	arr, err := s.liveness.Exec(ctx, s.c, []string{s.kSeq(eventID)}, []string{
		s.prefix(eventID), leaseToken, strconv.FormatInt(staleBeforeMs, 10), strconv.FormatInt(endBeforeMs, 10),
		strconv.Itoa(batch), strconv.FormatInt(nowMs, 10), strconv.Itoa(s.shards), retentionMs(),
	}).ToArray()
	if err != nil {
		return outbound.LivenessResult{}, fmt.Errorf("valkey: liveness.lua (B=%d): %w", s.shards, err)
	}
	tag, _ := arr[0].ToString()
	if tag == "LEASE_LOST" {
		return outbound.LivenessResult{LeaseLost: true}, nil
	}
	st, _ := arr[1].AsInt64()
	en, _ := arr[2].AsInt64()
	return outbound.LivenessResult{Staled: int(st), Ended: int(en)}, nil
}

// SweepLivenessTick acquires the promoter lease, sweeps all shards and releases the lease in ONE round-trip
// (liveness_tick.lua). The liveness worker uses this instead of the acquire → SweepLiveness → release triple so the
// promoter tick issues a single command under heartbeat load: on Valkey's single command thread each extra round-trip
// queues behind the heartbeat traffic, so collapsing three round-trips into one keeps the tick within +20 % of base
// (design-change §4.3). ttl bounds the lease the script takes for the duration of its own execution. The entry
// liveness rules (STALE/revive/end) are identical to SweepLiveness.
func (s *QueueStore) SweepLivenessTick(ctx context.Context, eventID string, staleBeforeMs, endBeforeMs int64, batch int, nowMs int64, ttl time.Duration, topN int) (outbound.LivenessResult, []string, error) {
	tok, err := newLeaseToken()
	if err != nil {
		return outbound.LivenessResult{}, nil, err
	}
	arr, err := s.livenessTick.Exec(ctx, s.c, []string{s.kLease(eventID)}, []string{
		s.prefix(eventID), tok, strconv.FormatInt(staleBeforeMs, 10), strconv.FormatInt(endBeforeMs, 10),
		strconv.Itoa(batch), strconv.FormatInt(nowMs, 10), strconv.Itoa(s.shards), strconv.FormatInt(ttl.Milliseconds(), 10),
		strconv.Itoa(topN), retentionMs(),
	}).ToArray()
	if err != nil {
		return outbound.LivenessResult{}, nil, fmt.Errorf("valkey: liveness_tick.lua (B=%d): %w", s.shards, err)
	}
	tag, _ := arr[0].ToString()
	if tag == "LEASE_LOST" {
		return outbound.LivenessResult{LeaseLost: true}, nil, nil
	}
	st, _ := arr[1].AsInt64()
	en, _ := arr[2].AsInt64()
	var top []string
	if len(arr) >= 4 {
		top, _ = arr[3].AsStrSlice()
	}
	return outbound.LivenessResult{Staled: int(st), Ended: int(en)}, top, nil
}

// WaitingSeqAt returns the seq (score) of the live waiting entry at 0-based index idx.
func (s *QueueStore) WaitingSeqAt(ctx context.Context, eventID string, idx int64) (int64, bool, error) {
	zs, err := s.c.Do(ctx, s.c.B().Zrange().Key(s.kWaiting(eventID)).Min(strconv.FormatInt(idx, 10)).Max(strconv.FormatInt(idx, 10)).Withscores().Build()).AsZScores()
	if err != nil {
		return 0, false, fmt.Errorf("valkey: zrange waiting: %w", err)
	}
	if len(zs) == 0 {
		return 0, false, nil
	}
	return int64(zs[0].Score), true, nil
}

// WaitingCount is ZCARD waiting (live entries only; STALE are in their own set).
func (s *QueueStore) WaitingCount(ctx context.Context, eventID string) (int64, error) {
	n, err := s.c.Do(ctx, s.c.B().Zcard().Key(s.kWaiting(eventID)).Build()).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: zcard waiting: %w", err)
	}
	return n, nil
}

// RetireEvent retires a finished event's fixed keys in one atomic script (retire_event.lua, D-C8.2, P3-13). It reports
// false, with nothing written, while an active, waiting or stale entry (or a heartbeat index entry) still exists: the
// check and the EXPIRE NX calls share one EVAL, so a join cannot slip in between. EXPIRE NX keeps a key's first deadline,
// so a repeat call on a later status tick never pushes it forward.
func (s *QueueStore) RetireEvent(ctx context.Context, eventID string, ttl time.Duration) (bool, error) {
	keys := []string{s.kActive(eventID), s.kWaiting(eventID), s.k(eventID, "stale")}
	for sh := 0; sh < s.shards; sh++ {
		shard := strconv.Itoa(sh)
		keys = append(keys, s.kHbShard(eventID, shard), s.kHbStaleShard(eventID, shard))
	}
	zsets := len(keys)
	keys = append(keys, s.kSeq(eventID), s.k(eventID, "pending"), s.kStatus(eventID), s.kSale(eventID),
		s.kUnused(eventID), s.kLease(eventID), s.kNotifySeq(eventID))
	args := []string{strconv.FormatInt(int64(ttl/time.Second), 10), strconv.Itoa(zsets)}
	n, err := s.retireEvent.Exec(ctx, s.c, keys, args).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: retire_event.lua: %w", err)
	}
	return n == 1, nil
}

// LivePosition returns 1 + ZCOUNT waiting -inf (seq.
func (s *QueueStore) LivePosition(ctx context.Context, eventID string, seq int64) (int64, error) {
	n, err := s.c.Do(ctx, s.c.B().Zcount().Key(s.kWaiting(eventID)).Min("-inf").Max("("+strconv.FormatInt(seq, 10)).Build()).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: zcount waiting: %w", err)
	}
	return n + 1, nil
}
