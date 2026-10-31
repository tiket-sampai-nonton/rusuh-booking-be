// Package valkey implements the Booking SSE availability cache on Valkey. The stock projection
// writes the per-event availability snapshot; SSE readers only GET it (code-principles.md: business
// logic stays in Go, Valkey is just hot state).
package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	vk "github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const (
	availKeyFmt = "booking:avail:%s"
	connKeyFmt  = "booking:sse:conn:%s"
	connTTLSec  = int64(60)
)

// AvailabilityCache reads SSE availability data and tracks the per-instance connection count.
type AvailabilityCache struct{ c vk.Client }

// NewAvailabilityCache wires the cache on the raw valkey-go client.
func NewAvailabilityCache(c vk.Client) *AvailabilityCache { return &AvailabilityCache{c: c} }

// GetAvailability returns the latest availability snapshot for an event, or nil when none is cached.
func (a *AvailabilityCache) GetAvailability(ctx context.Context, eventID string) ([]outbound.CategoryAvailabilitySnap, error) {
	key := fmt.Sprintf(availKeyFmt, eventID)
	res, err := a.c.Do(ctx, a.c.B().Get().Key(key).Build()).ToString()
	if err != nil {
		if vk.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("valkey: get availability: %w", err)
	}
	var snaps []outbound.CategoryAvailabilitySnap
	if err := json.Unmarshal([]byte(res), &snaps); err != nil {
		return nil, fmt.Errorf("valkey: decode availability: %w", err)
	}
	return snaps, nil
}

// SetAvailability stores the snapshot with a TTL so a stopped projector never leaves stale stock
// on the stream for long.
func (a *AvailabilityCache) SetAvailability(ctx context.Context, eventID string, snaps []outbound.CategoryAvailabilitySnap, ttl time.Duration) error {
	raw, err := json.Marshal(snaps)
	if err != nil {
		return fmt.Errorf("valkey: encode availability: %w", err)
	}
	key := fmt.Sprintf(availKeyFmt, eventID)
	if err := a.c.Do(ctx, a.c.B().Set().Key(key).Value(string(raw)).Ex(ttl).Build()).Error(); err != nil {
		return fmt.Errorf("valkey: set availability: %w", err)
	}
	return nil
}

// IncrConn increments the per-instance SSE connection counter and refreshes its TTL (self-healing
// against leaked counts if a process dies). It returns the post-increment count.
func (a *AvailabilityCache) IncrConn(ctx context.Context, instance string) (int, error) {
	key := fmt.Sprintf(connKeyFmt, instance)
	n, err := a.c.Do(ctx, a.c.B().Incr().Key(key).Build()).ToInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey: incr sse conn: %w", err)
	}
	_ = a.c.Do(ctx, a.c.B().Expire().Key(key).Seconds(connTTLSec).Build())
	return int(n), nil
}

// DecrConn decrements the per-instance SSE connection counter on disconnect.
func (a *AvailabilityCache) DecrConn(ctx context.Context, instance string) error {
	key := fmt.Sprintf(connKeyFmt, instance)
	return a.c.Do(ctx, a.c.B().Decr().Key(key).Build()).Error()
}
