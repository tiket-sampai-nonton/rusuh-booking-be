package valkey

import (
	"context"
	"encoding/json"
	"time"

	vk "github.com/valkey-io/valkey-go"
	"golang.org/x/sync/singleflight"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const eventSettingsCacheTTL = 30 * time.Second

// EventSettingsCache wraps IEventSettingsRepo with a Valkey read-through cache (TTL 30s).
// All instances share the same key so a PUT by admin is consistent within one TTL window.
type EventSettingsCache struct {
	c      vk.Client
	keyer  Keyer
	source outbound.IEventSettingsRepo
	// fill collapses concurrent Postgres reads of one event, so a cold key under load costs one read (PF-P3-2).
	fill singleflight.Group
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
