package usecase

import (
	"context"
	"encoding/json"
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

// GetMyEntry finds the visitor's entry and maps it to the EntryStatus DTO (no PII).
func (u *GetMyEntry) GetMyEntry(ctx context.Context, in inbound.MyEntryInput) (dto.EntryStatus, error) {
	if err := checkTokenGen(ctx, u.store, in.EventID, in.VisitorID, in.Gen); err != nil {
		return dto.EntryStatus{}, err
	}
	if err := u.limit.allow(ctx, "me", in.EventID, in.VisitorID); err != nil {
		return dto.EntryStatus{}, err
	}
	ent, ok, err := u.store.FindByVisitor(ctx, in.EventID, in.VisitorID)
	if err != nil {
		return dto.EntryStatus{}, err
	}
	if !ok {
		if ent, ok, err = u.store.FindLastByVisitor(ctx, in.EventID, in.VisitorID); err != nil {
			return dto.EntryStatus{}, err
		}
		if !ok {
			return dto.EntryStatus{}, entity.ErrNotFound
		}
	}
	out := mapper.ToEntryStatus(ent)
	if ent.IsTerminal() {
		out.Reason = endedReason(ent)
		return out, nil
	}
	doc, _ := readDoc(ctx, u.store, in.EventID)
	if ent.Status == valueobject.StatusWaiting || ent.Status == valueobject.StatusStale {
		pos, perr := u.store.LivePosition(ctx, in.EventID, ent.Seq.Int64())
		if perr == nil {
			out.ExactPosition = pos
		}
		if doc.GuaranteedUntilSeq != nil && doc.PossibleUntilSeq != nil {
			z := string(valueobject.ZoneForSeq(ent.Seq.Int64(), *doc.GuaranteedUntilSeq, *doc.PossibleUntilSeq))
			out.Zone = &z
		}
		out.NextHeartbeatMs = u.policy.next(ent.Seq.Int64(), doc)
	}
	return out, nil
}

// checkTokenGen is the generation check shared by claim, leave and me (R01.4/R01.6). It runs before any state change
// and does not depend on the entry id, so a rotated token cannot probe entries. Outcomes:
//   - empty eventId                       -> ErrVisitorTokenInvalid (401, unchanged)
//   - no generation record for the event  -> entity.ErrNotFound (404: the visitor never joined this event)
//   - record present, gen <= 0 or differs -> ErrVisitorTokenInvalid (401: rotated out / legacy token)
//   - match                               -> nil
func checkTokenGen(ctx context.Context, store outbound.IQueueStore, eventID, visitorID string, gen int64) error {
	if eventID == "" {
		return entity.ErrVisitorTokenInvalid
	}
	cur, ok, err := store.TokenGen(ctx, eventID, visitorID)
	if err != nil {
		return err
	}
	if !ok {
		return entity.ErrNotFound
	}
	if gen <= 0 || cur != gen {
		return entity.ErrVisitorTokenInvalid
	}
	return nil
}

func readDoc(ctx context.Context, store outbound.IQueueStore, eventID string) (dto.StatusDocument, bool) {
	raw, has, err := store.GetStatusDoc(ctx, eventID)
	if err != nil || !has {
		return dto.StatusDocument{}, false
	}
	var doc dto.StatusDocument
	if json.Unmarshal(raw, &doc) != nil {
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
