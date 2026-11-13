package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

//go:generate go tool mockgen -source=queue_store.go -destination=../../../../_mock/queue/port/outbound/queue_store_mock.go -package=mock_outbound

// IQueueStore is the Valkey-backed hot state of the queue. Lua scripts provide atomicity.
type IQueueStore interface {
	Join(ctx context.Context, p JoinParams) (JoinResult, error)
	Promote(ctx context.Context, eventID string, cands []PromoteCandidate, capacity int, nowMs int64, opts PromoteOpts) (PromoteResult, error)
	// Claim sets the CLAIMED slot's upper bound to nowMs+claimMaxMs (C6.5, P2-15); past it EndEntry ends the slot.
	Claim(ctx context.Context, eventID, entryID, visitorID string, nowMs, claimMaxMs int64) (ClaimResult, error)
	Leave(ctx context.Context, eventID, entryID, visitorID string, nowMs int64) (LeaveResult, error)
	EndEntry(ctx context.Context, eventID, entryID, outcome string, nowMs int64) (EndResult, error)
	// EndWaitingSoldOut ends up to batch WAITING/STALE entries with status SOLD_OUT (booking.sold_out, SRS-F-10-02).
	// ADMITTED/CLAIMED are never touched. Idempotent; Scanned == batch means more may remain.
	EndWaitingSoldOut(ctx context.Context, eventID string, nowMs int64, batch int) (SoldOutBatch, error)
	GetEntry(ctx context.Context, eventID, entryID string) (entity.Entry, bool, error)
	// GetEntries reads the entry hashes of ids in one round trip (C8.8, PF-P2-2). An id without a hash is omitted.
	GetEntries(ctx context.Context, eventID string, ids []string) (map[string]entity.Entry, error)
	FindByVisitor(ctx context.Context, eventID, visitorID string) (entity.Entry, bool, error)
	ActiveCount(ctx context.Context, eventID string) (int, error)
	TopWaiting(ctx context.Context, eventID string, n int) ([]string, error)
	ExpiredActive(ctx context.Context, eventID string, nowMs int64, limit int) ([]string, error)
	SetSale(ctx context.Context, eventID, saleState string, soldOut bool) error
	GetSale(ctx context.Context, eventID string) (SaleState, error)
	// SetSaleWindow caches the event's sale window (epoch ms) in the sale hash without touching saleState/soldOut.
	SetSaleWindow(ctx context.Context, eventID string, startsMs, endsMs int64) error
	SetStatusDoc(ctx context.Context, eventID string, doc []byte, ttl time.Duration) error
	GetStatusDoc(ctx context.Context, eventID string) ([]byte, bool, error)
	// AdvanceNotifyWatermark moves the event's pre-turn notify watermark up to upToSeq (never back) and returns the value it
	// had before. The caller announces the live waiting entries with seq in (previous, upToSeq] (D-C8.3, PF-P1-5).
	AdvanceNotifyWatermark(ctx context.Context, eventID string, upToSeq int64) (int64, error)
	// WaitingBetween returns up to limit live waiting ids with afterSeq < seq <= upToSeq, FIFO by seq.
	WaitingBetween(ctx context.Context, eventID string, afterSeq, upToSeq int64, limit int) ([]string, error)
	// AcquirePromoterLease tries to acquire a per-event distributed lock (SET NX PX) so only one
	// worker instance promotes a given event per tick (spec 11.R3, req 6.12, Property 7).
	// Returns true if the lease was acquired, false if another instance holds it.
	AcquirePromoterLease(ctx context.Context, eventID string, ttl time.Duration) (token string, acquired bool, err error)
	// ReleasePromoterLease releases the lease only when token still owns it (compare-and-delete).
	ReleasePromoterLease(ctx context.Context, eventID, token string) error

	// --- ADR-030: liveness, token generation and FIFO-by-seq positions ---

	// Heartbeat records a liveness signal for the visitor's current entry, reviving a STALE entry at its seq.
	Heartbeat(ctx context.Context, eventID, visitorID string, gen, nowMs int64) (HeartbeatResult, error)
	// CheckTokenGen checks a visitor token generation on the event (D-C6.1, token_gen.lua): TokenGenOK when gen equals
	// or exceeds the stored one (a higher gen is stored, a ratchet), TokenGenInvalid when gen is lower or not positive,
	// TokenGenAbsent when the visitor has no record for the event (never joined).
	CheckTokenGen(ctx context.Context, eventID, visitorID string, gen int64) (TokenGenCheck, error)
	// FindLastByVisitor returns the visitor's most recent entry in any state (me on an ENDED entry).
	FindLastByVisitor(ctx context.Context, eventID, visitorID string) (entity.Entry, bool, error)
	// SweepLiveness marks silent WAITING entries STALE and ends long-silent ones, fenced by the lease token. It
	// sweeps the sharded liveness index hb:{s}/hbstale:{s} (ADR-030 Amendment) in a single EVAL over all shards.
	SweepLiveness(ctx context.Context, eventID, leaseToken string, staleBeforeMs, endBeforeMs int64, batch int, nowMs int64) (LivenessResult, error)
	// SweepLivenessTick acquires the promoter lease, reads the top-N waiting candidates, runs the same sharded sweep,
	// and releases the lease in ONE round-trip (liveness_tick.lua). The liveness worker uses it (topN=0) so the
	// promoter tick issues a single command under heartbeat load, keeping the tick within +20 % of base
	// (design-change §4.3). ttl bounds the lease the script holds for its own execution; the returned slice is the
	// top-N live WAITING ids by seq (nil when topN=0 or LEASE_LOST). batch is the budget of the WHOLE tick (entries
	// staled plus ended, across all shards); startShard is the shard the sweep begins at (C8.7).
	SweepLivenessTick(ctx context.Context, eventID string, staleBeforeMs, endBeforeMs int64, batch int, nowMs int64, ttl time.Duration, topN int, startShard int) (LivenessResult, []string, error)
	// WaitingSeqAt returns the seq of the live (WAITING, non-STALE) entry at 0-based position idx.
	WaitingSeqAt(ctx context.Context, eventID string, idx int64) (int64, bool, error)
	// WaitingCount returns the number of live waiting entries.
	WaitingCount(ctx context.Context, eventID string) (int64, error)
	// RetireEvent gives the fixed keys of a finished event (seq, waiting, stale, pending, active, hb/hbstale shards,
	// status, sale, unused, promoter lock, notify watermark) a TTL in one atomic step, and only while nothing in it is
	// live: no active, waiting or stale entry. It reports whether it did (D-C8.2, P3-13). A key keeps its first deadline,
	// so calling it on every status tick is harmless.
	RetireEvent(ctx context.Context, eventID string, ttl time.Duration) (bool, error)
	// LivePosition returns 1 + the number of live entries with a smaller seq.
	LivePosition(ctx context.Context, eventID string, seq int64) (int64, error)
}

// TokenGenCheck is the token_gen.lua outcome. The zero value is deliberately not an outcome.
type TokenGenCheck int

// Token generation outcomes.
const (
	TokenGenOK      TokenGenCheck = iota + 1 // gen is the stored generation or newer (a newer one is stored)
	TokenGenInvalid                          // gen is older than the stored one, or not positive -> 401
	TokenGenAbsent                           // no generation record for this event: the visitor never joined -> 404
)
