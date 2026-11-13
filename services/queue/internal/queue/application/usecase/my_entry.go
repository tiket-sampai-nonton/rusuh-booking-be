package usecase

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// HeartbeatPolicy is the server-controlled heartbeat interval (R03.1/R03.8).
type HeartbeatPolicy struct {
	BaseMs int64
	NearMs int64
	Jitter func() float64 // [0,1); default math/rand
}

func (p HeartbeatPolicy) next(seq int64, doc dto.StatusDocument) int64 {
	if p.BaseMs <= 0 {
		return 0
	}
	j := p.Jitter
	if j == nil {
		j = rand.Float64
	}
	var head int64
	if doc.HeadSeq != nil {
		head = *doc.HeadSeq
	}
	return valueobject.NextHeartbeatMs(p.BaseMs, p.NearMs, int64(doc.PreTurnNotifyPosition), seq, head, j())
}

// GetMyEntry implements inbound.IMyEntryUsecase: the caller's own entry, rank-based position and server zone
// (ADR-030). An ENDED entry is returned with state ENDED and a reason (addendum 2); 404 when the visitor never
// joined this event (no generation record) or had no entry; 401 for a rotated-out token generation.
type GetMyEntry struct {
	store  outbound.IQueueStore
	docs   outbound.IStatusDocReader
	policy HeartbeatPolicy
	limit  visitorLimit
}

// NewGetMyEntry accepts interfaces and returns the concrete usecase.
func NewGetMyEntry(store outbound.IQueueStore) *GetMyEntry {
	return &GetMyEntry{store: store}
}

// WithHeartbeat sets the heartbeat interval policy returned as nextHeartbeatMs.
func (u *GetMyEntry) WithHeartbeat(p HeartbeatPolicy) *GetMyEntry {
	u.policy = p
	return u
}

// WithLimiter caps `me` calls per visitor (design §2.3).
func (u *GetMyEntry) WithLimiter(l outbound.IRateLimiter, limit int, window time.Duration) *GetMyEntry {
	u.limit = visitorLimit{limiter: l, limit: limit, window: window}
	return u
}

// WithStatusDocs reads the Status Document for the rank and zone through docs (the process cache, D-C8.9).
func (u *GetMyEntry) WithStatusDocs(docs outbound.IStatusDocReader) *GetMyEntry {
	u.docs = docs
	return u
}

// GetMyEntry finds the visitor's entry and maps it to the EntryStatus DTO (no PII). The token generation is checked
// before the rate limit, and the rate limit before the entry is reported: the same order as the multi-call path, now
// answered by one store call (me.lua, C8.9).
func (u *GetMyEntry) GetMyEntry(ctx context.Context, in inbound.MyEntryInput) (dto.EntryStatus, error) {
	if in.EventID == "" {
		return dto.EntryStatus{}, entity.ErrVisitorTokenInvalid
	}
	res, err := u.store.MyEntry(ctx, in.EventID, in.VisitorID, in.Gen)
	if err != nil {
		return dto.EntryStatus{}, err
	}
	switch res.Tag {
	case outbound.MyEntryAbsent:
		return dto.EntryStatus{}, entity.ErrNotFound
	case outbound.MyEntryInvalid:
		return dto.EntryStatus{}, entity.ErrVisitorTokenInvalid
	}
	if err := u.limit.allow(ctx, "me", in.EventID, in.VisitorID); err != nil {
		return dto.EntryStatus{}, err
	}
	if res.Tag == outbound.MyEntryNotFound {
		return dto.EntryStatus{}, entity.ErrNotFound
	}
	ent := res.Entry
	out := mapper.ToEntryStatus(ent)
	if ent.IsTerminal() {
		out.Reason = endedReason(ent)
		return out, nil
	}
	doc, _ := readDoc(ctx, u.docs, in.EventID)
	if ent.Status == valueobject.StatusWaiting || ent.Status == valueobject.StatusStale {
		out.ExactPosition = res.Position
		if doc.GuaranteedUntilSeq != nil && doc.PossibleUntilSeq != nil {
			z := string(valueobject.ZoneForSeq(ent.Seq.Int64(), *doc.GuaranteedUntilSeq, *doc.PossibleUntilSeq))
			out.Zone = &z
		}
		out.NextHeartbeatMs = u.policy.next(ent.Seq.Int64(), doc)
	}
	out.NextPollAfterMs = nextPollMs(out.ExactPosition, doc.Poll)
	return out, nil
}

// nextPollMs tiers the poll interval on the live position by the event's poll policy from the Status Document. A
// document without a policy (none published yet, or unreadable) falls back to the default settings. ADMITTED and
// CLAIMED entries have position 0, so they poll at the near interval.
func nextPollMs(position int64, p dto.Poll) int64 {
	if p.FarMs <= 0 || p.MidMs <= 0 || p.NearMs <= 0 {
		d := entity.DefaultSettings("")
		p = dto.Poll{FarMs: d.PollFarMs, MidMs: d.PollMidMs, NearMs: d.PollNearMs, FarAbove: d.PollFarAbove, MidAbove: d.PollMidAbove}
	}
	return valueobject.NextPollMs(position, int64(p.FarMs), int64(p.MidMs), int64(p.NearMs), p.FarAbove, p.MidAbove)
}

// checkTokenGen is the generation check of claim and leave (R01.4/R01.6); `me` runs the same check inside me.lua. It
// runs before any state change and does not depend on the entry id, so a rotated token cannot probe entries. Outcomes:
//   - empty eventId                       -> ErrVisitorTokenInvalid (401, unchanged)
//   - no generation record for the event  -> entity.ErrNotFound (404: the visitor never joined this event)
//   - gen older than the stored one, or <= 0 -> ErrVisitorTokenInvalid (401: rotated out / legacy token)
//   - gen equal to or newer than the stored one -> nil (a newer gen is ratcheted up by the store, D-C6.1)
func checkTokenGen(ctx context.Context, store outbound.IQueueStore, eventID, visitorID string, gen int64) error {
	if eventID == "" {
		return entity.ErrVisitorTokenInvalid
	}
	res, err := store.CheckTokenGen(ctx, eventID, visitorID, gen)
	if err != nil {
		return err
	}
	switch res {
	case outbound.TokenGenOK:
		return nil
	case outbound.TokenGenAbsent:
		return entity.ErrNotFound
	default:
		return entity.ErrVisitorTokenInvalid
	}
}

// readDoc returns the event's Status Document through docs. Without a reader, or on a miss or a read error, the rank
// logic runs on an empty document (no zone, the policy's default interval).
func readDoc(ctx context.Context, docs outbound.IStatusDocReader, eventID string) (dto.StatusDocument, bool) {
	if docs == nil {
		return dto.StatusDocument{}, false
	}
	_, doc, ok, err := docs.Get(ctx, eventID)
	if err != nil || !ok {
		return dto.StatusDocument{}, false
	}
	return doc, true
}

// endedReason maps the stored terminal status/outcome to the machine-readable reason of addendum 2.
func endedReason(e entity.Entry) string {
	switch {
	case e.Status == valueobject.StatusLeft:
		return "LEFT"
	case e.Status == valueobject.StatusSoldOut:
		return "SOLD_OUT"
	case e.EndReason == "STALE_TIMEOUT":
		return "STALE_TIMEOUT"
	case e.Status == valueobject.StatusMissed, e.EndReason == "UNCLAIMED", e.EndReason == "SWEEP_MISSED":
		return "CLAIM_WINDOW_EXPIRED"
	case e.EndReason == "EXPIRED", e.EndReason == "SESSION_EXPIRED":
		return "PAGE_TIMER_EXPIRED"
	default:
		return "JOURNEY_ENDED"
	}
}
