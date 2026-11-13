package valkey

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	vk "github.com/valkey-io/valkey-go"
	"golang.org/x/sync/singleflight"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const (
	eventSettingsCacheTTL = 30 * time.Second
	// activeEventsTTL bounds how long the in-process active-event list is reused (D-C8.10). The four worker jobs read it
	// once per window instead of once each.
	activeEventsTTL = 5 * time.Second
)

// EventSettingsCache wraps IEventSettingsRepo with a Valkey read-through cache (TTL 30s).
// All instances share the same key so a PUT by admin is consistent within one TTL window.
type EventSettingsCache struct {
	c      vk.Client
	keyer  Keyer
	source outbound.IEventSettingsRepo
	// fill collapses concurrent Postgres reads of one event, so a cold key under load costs one read (PF-P3-2).
	fill singleflight.Group

	activeMu  sync.Mutex // serializes the read of the active list, so concurrent jobs share one Postgres query
	activeIDs []string
	activeAt  time.Time // the now of the read that filled the cache
	activeOK  bool
}

// settingsFill is the shared result of one Postgres read.
type settingsFill struct {
	s     entity.EventSettings
	found bool
}

// NewEventSettingsCache returns a caching decorator around source.
func NewEventSettingsCache(c vk.Client, keyer Keyer, source outbound.IEventSettingsRepo) *EventSettingsCache {
	return &EventSettingsCache{c: c, keyer: keyer, source: source}
}

func (r *EventSettingsCache) cacheKey(eventID string) string {
	return r.keyer.Key("q:{e:" + eventID + "}:settings")
}

// Get reads from Valkey first; on miss or any Valkey error it falls through to Postgres and populates the cache.
// Concurrent misses for one event share a single Postgres read.
func (r *EventSettingsCache) Get(ctx context.Context, eventID string) (entity.EventSettings, bool, error) {
	if raw, err := r.c.Do(ctx, r.c.B().Get().Key(r.cacheKey(eventID)).Build()).AsBytes(); err == nil {
		var s entity.EventSettings
		if json.Unmarshal(raw, &s) == nil {
			return s, true, nil
		}
	}
	v, err, _ := r.fill.Do(eventID, func() (any, error) {
		// The shared read outlives the caller that started it: one cancelled request must not fail the others.
		fctx := context.WithoutCancel(ctx)
		s, found, err := r.source.Get(fctx, eventID)
		if err == nil && found {
			if b, jerr := json.Marshal(s); jerr == nil {
				_ = r.c.Do(fctx, r.c.B().Set().Key(r.cacheKey(eventID)).Value(string(b)).Ex(eventSettingsCacheTTL).Build()).Error()
			}
		}
		return settingsFill{s: s, found: found}, err
	})
	res, _ := v.(settingsFill)
	return res.s, res.found, err
}

// Invalidate deletes the cached row of eventID; the next Get reads Postgres. DEL of a missing key is not an error.
func (r *EventSettingsCache) Invalidate(ctx context.Context, eventID string) error {
	return r.c.Do(ctx, r.c.B().Del().Key(r.cacheKey(eventID)).Build()).Error()
}

// ListEventIDs delegates to Postgres; this method is only used by workers to iterate events, not on the hot path.
func (r *EventSettingsCache) ListEventIDs(ctx context.Context) ([]string, error) {
	return r.source.ListEventIDs(ctx)
}

// ListActiveEventIDs returns the active-event list from an in-process cache for up to activeEventsTTL (D-C8.10). The
// cache is per process and keyed by the caller's now: a read at or after the cached now within the TTL reuses it.
// Callers get a copy, so they cannot change the cached list.
func (r *EventSettingsCache) ListActiveEventIDs(ctx context.Context, now time.Time) ([]string, error) {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if !r.activeOK || now.Before(r.activeAt) || now.Sub(r.activeAt) >= activeEventsTTL {
		ids, err := r.source.ListActiveEventIDs(ctx, now)
		if err != nil {
			return nil, err
		}
		r.activeIDs, r.activeAt, r.activeOK = ids, now, true
	}
	return append([]string(nil), r.activeIDs...), nil
}

// UpdateSaleWindow delegates to Postgres. The cached list is not dropped: the next list read after the TTL sees the change.
func (r *EventSettingsCache) UpdateSaleWindow(ctx context.Context, eventID string, starts, ends time.Time) error {
	return r.source.UpdateSaleWindow(ctx, eventID, starts, ends)
}
