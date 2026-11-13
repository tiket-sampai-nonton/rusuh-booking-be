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

const availKeyFmt = "booking:avail:%s"

// AvailabilityCache reads and writes the SSE availability snapshot.
type AvailabilityCache struct{ c vk.Client }

// NewAvailabilityCache wires the cache on the raw valkey-go client.
func NewAvailabilityCache(c vk.Client) *AvailabilityCache { return &AvailabilityCache{c: c} }

// GetAvailability returns the latest availability snapshot for an event, or the zero snapshot when none is cached.
func (a *AvailabilityCache) GetAvailability(ctx context.Context, eventID string) (outbound.AvailabilitySnapshot, error) {
	key := fmt.Sprintf(availKeyFmt, eventID)
	res, err := a.c.Do(ctx, a.c.B().Get().Key(key).Build()).ToString()
	if err != nil {
		if vk.IsValkeyNil(err) {
			return outbound.AvailabilitySnapshot{}, nil
		}
		return outbound.AvailabilitySnapshot{}, fmt.Errorf("valkey: get availability: %w", err)
	}
	var snap outbound.AvailabilitySnapshot
	if err := json.Unmarshal([]byte(res), &snap); err != nil {
		return outbound.AvailabilitySnapshot{}, fmt.Errorf("valkey: decode availability: %w", err)
	}
	return snap, nil
}

// SetAvailability stores the snapshot with a TTL so a stopped projector never leaves stale stock
// on the stream for long.
func (a *AvailabilityCache) SetAvailability(ctx context.Context, eventID string, snap outbound.AvailabilitySnapshot, ttl time.Duration) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("valkey: encode availability: %w", err)
	}
	key := fmt.Sprintf(availKeyFmt, eventID)
	if err := a.c.Do(ctx, a.c.B().Set().Key(key).Value(string(raw)).Ex(ttl).Build()).Error(); err != nil {
		return fmt.Errorf("valkey: set availability: %w", err)
	}
	return nil
}
