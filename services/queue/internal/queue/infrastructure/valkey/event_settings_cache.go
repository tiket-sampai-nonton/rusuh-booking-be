package valkey

import (
	"context"
	"encoding/json"
	"time"

	vk "github.com/valkey-io/valkey-go"

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
}

// NewEventSettingsCache returns a caching decorator around source.
func NewEventSettingsCache(c vk.Client, keyer Keyer, source outbound.IEventSettingsRepo) *EventSettingsCache {
	return &EventSettingsCache{c: c, keyer: keyer, source: source}
}

func (r *EventSettingsCache) cacheKey(eventID string) string {
	return r.keyer.Key("q:{e:" + eventID + "}:settings")
}

// Get reads from Valkey first; on miss or any Valkey error it falls through to Postgres and populates the cache.
func (r *EventSettingsCache) Get(ctx context.Context, eventID string) (entity.EventSettings, bool, error) {
	if raw, err := r.c.Do(ctx, r.c.B().Get().Key(r.cacheKey(eventID)).Build()).AsBytes(); err == nil {
		var s entity.EventSettings
		if json.Unmarshal(raw, &s) == nil {
			return s, true, nil
		}
	}
	s, found, err := r.source.Get(ctx, eventID)
	if err != nil || !found {
		return s, found, err
	}
	if b, jerr := json.Marshal(s); jerr == nil {
		_ = r.c.Do(ctx, r.c.B().Set().Key(r.cacheKey(eventID)).Value(string(b)).Ex(eventSettingsCacheTTL).Build()).Error()
	}
	return s, true, nil
}

// ListEventIDs delegates to Postgres; this method is only used by workers to iterate events, not on the hot path.
func (r *EventSettingsCache) ListEventIDs(ctx context.Context) ([]string, error) {
	return r.source.ListEventIDs(ctx)
}
