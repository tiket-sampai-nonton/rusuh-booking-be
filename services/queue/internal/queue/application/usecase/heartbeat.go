package usecase

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Heartbeat implements inbound.IHeartbeatUsecase (ADR-030, R03): one atomic script records the signal, revives a
// STALE entry at its rank, and answers 401 for a rotated-out token and 404 for an ended entry.
type Heartbeat struct {
	store  outbound.IQueueStore
	clock  clock.IClock
	policy HeartbeatPolicy
	limit  visitorLimit
}

// NewHeartbeat accepts interfaces and returns the concrete usecase.
func NewHeartbeat(store outbound.IQueueStore, clk clock.IClock, policy HeartbeatPolicy) *Heartbeat {
	return &Heartbeat{store: store, clock: clk, policy: policy}
}

// WithLimiter caps heartbeats per visitor (ADR-030 R03: low frequency; a client that pings faster gets 429).
func (u *Heartbeat) WithLimiter(l outbound.IRateLimiter, limit int, window time.Duration) *Heartbeat {
	u.limit = visitorLimit{limiter: l, limit: limit, window: window}
	return u
}

// Heartbeat records the signal and returns the state and the next interval. Nothing PII-bearing is read or logged.
func (u *Heartbeat) Heartbeat(ctx context.Context, in inbound.HeartbeatInput) (dto.HeartbeatStatus, error) {
	if err := u.limit.allow(ctx, "hb", in.EventID, in.VisitorID); err != nil {
		return dto.HeartbeatStatus{}, err
	}
	res, err := u.store.Heartbeat(ctx, in.EventID, in.VisitorID, in.Gen, u.clock.Now().UnixMilli())
	if err != nil {
		return dto.HeartbeatStatus{}, err
	}
	switch res.Tag {
	case outbound.HeartbeatInvalid:
		return dto.HeartbeatStatus{}, entity.ErrVisitorTokenInvalid
	case outbound.HeartbeatNotFound:
		return dto.HeartbeatStatus{}, entity.ErrEntryNotFound
	}
	out := dto.HeartbeatStatus{EntryID: res.EntryID, State: res.Status}
	if res.Seq > 0 {
		r := res.Seq
		out.Rank = &r
	}
	doc, _ := readDoc(ctx, u.store, in.EventID)
	seq := int64(0)
	if out.Rank != nil {
		seq = *out.Rank
	}
	out.NextHeartbeatMs = u.policy.next(seq, doc)
	return out, nil
}
