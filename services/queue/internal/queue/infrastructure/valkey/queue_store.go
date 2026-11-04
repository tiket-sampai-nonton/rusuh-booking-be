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
	c     vk.Client
	keyer Keyer
	clk   clock.IClock

	join     *vk.Lua
	promote  *vk.Lua
	claim    *vk.Lua
	leave    *vk.Lua
	endEntry *vk.Lua

	releaseLease   *vk.Lua
	noteUnreadable *vk.Lua
}

// NewQueueStore returns the store with the embedded scripts loaded.
func NewQueueStore(c vk.Client, keyer Keyer, clk clock.IClock) *QueueStore {
	return &QueueStore{
		c: c, keyer: keyer, clk: clk,
		join:     vk.NewLuaScript(lua.Join),
		promote:  vk.NewLuaScript(lua.Promote),
		claim:    vk.NewLuaScript(lua.Claim),
		leave:    vk.NewLuaScript(lua.Leave),
		endEntry: vk.NewLuaScript(lua.EndEntry),

		releaseLease:   vk.NewLuaScript(lua.ReleaseLease),
		noteUnreadable: vk.NewLuaScript(lua.NoteUnreadable),
	}
}

// --- key builders (design §3.1; hash tag {e:<eventId>} keeps a script's keys in one slot) ---

func (s *QueueStore) tag(eventID string) string   { return "q:{e:" + eventID + "}" }
func (s *QueueStore) kSeq(e string) string        { return s.keyer.Key(s.tag(e) + ":seq") }
func (s *QueueStore) kWaiting(e string) string    { return s.keyer.Key(s.tag(e) + ":waiting") }
func (s *QueueStore) kActive(e string) string     { return s.keyer.Key(s.tag(e) + ":active") }
func (s *QueueStore) kEntry(e, id string) string  { return s.keyer.Key(s.tag(e) + ":entry:" + id) }
func (s *QueueStore) kNik(e, h string) string     { return s.keyer.Key(s.tag(e) + ":nik:" + h) }
func (s *QueueStore) kVisitor(e, v string) string { return s.keyer.Key(s.tag(e) + ":visitor:" + v) }
func (s *QueueStore) kEmail(e, h string) string   { return s.keyer.Key(s.tag(e) + ":email:" + h) }
func (s *QueueStore) kQuota(e string) string      { return s.keyer.Key(s.tag(e) + ":quota_exhausted") }
func (s *QueueStore) kStatus(e string) string     { return s.keyer.Key(s.tag(e) + ":status") }
func (s *QueueStore) kSale(e string) string       { return s.keyer.Key(s.tag(e) + ":sale") }
func (s *QueueStore) kLease(e string) string      { return s.keyer.Key(s.tag(e) + ":promoter:lock") }

func bstr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Join runs join.lua atomically and maps the array reply to a JoinResult (design §4.1).
func (s *QueueStore) Join(ctx context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
	keys := []string{
		s.kSeq(p.EventID), s.kWaiting(p.EventID), s.kEntry(p.EventID, p.EntryID),
		s.kNik(p.EventID, p.NikHash), s.kVisitor(p.EventID, p.VisitorID),
		s.kEmail(p.EventID, p.EmailHash()), s.kQuota(p.EventID),
	}
	args := []string{
		p.EntryID, p.VisitorID, p.NikHash, string(p.EmailEnc), p.AccountID,
		strconv.FormatInt(p.NowMs, 10), bstr(p.OneActivePerVisitor), bstr(p.OneActivePerEmail), p.EmailHashHex, p.NikMasked,
	}
	arr, err := s.join.Exec(ctx, s.c, keys, args).ToArray()
	if err != nil {
		return outbound.JoinResult{}, fmt.Errorf("valkey: join.lua: %w", err)
	}
	return mapJoin(arr)
}

func mapJoin(arr []vk.ValkeyMessage) (outbound.JoinResult, error) {
	if len(arr) == 0 {
		return outbound.JoinResult{}, fmt.Errorf("valkey: join.lua balasan kosong")
	}
	tag, _ := arr[0].ToString()
	switch tag {
	case "OK":
		seq, _ := arr[1].ToInt64()
		return outbound.JoinResult{Outcome: outbound.JoinOK, Seq: seq}, nil
	case "EXISTING":
		kind, _ := arr[1].ToString()
		id, _ := arr[2].ToString()
		switch kind {
		case "NIK":
			return outbound.JoinResult{Outcome: outbound.JoinExistingNIK, ExistingEntryID: id}, nil
		case "VISITOR":
			return outbound.JoinResult{Outcome: outbound.JoinExistingVisit, ExistingEntryID: id}, nil
		case "EMAIL":
			return outbound.JoinResult{Outcome: outbound.JoinExistingEmail, ExistingEntryID: id}, nil
		}
	case "ERR":
		return outbound.JoinResult{Outcome: outbound.JoinQuotaExhausted}, nil
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
		args = append(args, c.EntryID, c.AllocationID, strconv.FormatInt(c.ClaimDeadlineMs, 10))
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
func (s *QueueStore) Claim(ctx context.Context, eventID, entryID, visitorID string, nowMs int64) (outbound.ClaimResult, error) {
	keys := []string{s.kEntry(eventID, entryID), s.kActive(eventID)}
	args := []string{entryID, strconv.FormatInt(nowMs, 10), visitorID}
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
	nikKey, emailKey := s.kNik(eventID, ""), s.kEmail(eventID, "")
	if ok {
		nikKey = s.kNik(eventID, ent.BuyerNikHashHex())
		emailKey = s.kEmail(eventID, ent.EmailHashHex())
	}
	keys := []string{
		s.kEntry(eventID, entryID), s.kWaiting(eventID), s.kActive(eventID),
		nikKey, s.kVisitor(eventID, visitorID), emailKey,
	}
	args := []string{entryID, visitorID, strconv.FormatInt(nowMs, 10)}
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
		s.kNik(eventID, ent.BuyerNikHashHex()), s.kVisitor(eventID, ent.VisitorID), s.kEmail(eventID, ent.EmailHashHex()),
		s.kWaiting(eventID),
	}
	args := []string{entryID, outcome, strconv.FormatInt(nowMs, 10)}
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

// WasNotifiedSoon reports whether a pre-turn notification was already sent for this entry.
func (s *QueueStore) WasNotifiedSoon(ctx context.Context, eventID, entryID string) (bool, error) {
	key := s.keyer.Key(s.tag(eventID) + ":notified_soon:" + entryID)
	exists, err := s.c.Do(ctx, s.c.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey: was_notified_soon: %w", err)
	}
	return exists > 0, nil
}

// MarkNotifiedSoon records that a pre-turn notification was sent (idempotent SETNX, TTL=600s).
func (s *QueueStore) MarkNotifiedSoon(ctx context.Context, eventID, entryID string) error {
	key := s.keyer.Key(s.tag(eventID) + ":notified_soon:" + entryID)
	err := s.c.Do(ctx, s.c.B().Set().Key(key).Value("1").Nx().Ex(600*time.Second).Build()).Error()
	if vk.IsValkeyNil(err) {
		return nil // SET NX answers nil when the mark already exists: marking twice is idempotent
	}
	return err
}

// AddQuotaExhausted records a NIK hash in the quota_exhausted set (idempotent SADD).
func (s *QueueStore) AddQuotaExhausted(ctx context.Context, eventID, nikHash string) error {
	return s.c.Do(ctx, s.c.B().Sadd().Key(s.kQuota(eventID)).Member(nikHash).Build()).Error()
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
	if v, ok := m["servingSeq"]; ok {
		st.ServingSeq, _ = strconv.ParseInt(v, 10, 64)
	}
	return st, nil
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
	if b, err := hex.DecodeString(m["buyerNikHash"]); err == nil {
		e.BuyerNikHash = b
	}
	if b, err := hex.DecodeString(m["emailHash"]); err == nil {
		e.EmailHash = b
	}
	e.EmailEnc = []byte(m["emailEnc"])
	e.BuyerNikMasked = m["nikMasked"]
	e.Status = valueobject.Status(m["status"])
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

// NoteUnreadable counts one failed email unseal for a WAITING entry (note_unreadable.lua, C6). It returns -1 when
// the entry is not WAITING (or gone) and never creates a hash.
func (s *QueueStore) NoteUnreadable(ctx context.Context, eventID, entryID string) (int, error) {
	n, err := s.noteUnreadable.Exec(ctx, s.c, []string{s.kEntry(eventID, entryID)}, nil).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: note_unreadable.lua: %w", err)
	}
	return int(n), nil
}

func newLeaseToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("valkey: lease token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
