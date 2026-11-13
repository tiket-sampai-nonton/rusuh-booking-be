package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// StatusDocRefresh is how long a Status Document read from Valkey is reused in process (D-C8.9).
const StatusDocRefresh = 250 * time.Millisecond

// statusDocIdle is how long a snapshot may go unrefreshed before the cache drops it (memory bound over many events).
const statusDocIdle = time.Minute

// statusDocReadTimeout bounds the one shared Valkey read of a flight. The read runs under context.WithoutCancel, so only
// this timeout stops a stalled GET. No shared Valkey read timeout exists in this service (the lease releases use inline
// 2 s bounds), so 1 s is set here: a status read is answered from memory on the next 250 ms window anyway.
const statusDocReadTimeout = time.Second

// StatusDocCache implements outbound.IStatusDocReader (D-C8.9). A document read from Valkey is reused for ttl, so a
// burst of status and `me` reads for one event costs one GET. The refresh is lazy: the first read after the TTL goes to
// Valkey, so the cache starts no goroutine and has nothing to stop on shutdown. Concurrent misses for one event share one
// Valkey read (singleflight). An event with no document is not cached, and neither is an error: the next read asks Valkey.
type StatusDocCache struct {
	store     outbound.IQueueStore
	ttl       time.Duration
	now       func() time.Time
	slots     sync.Map // eventID -> *atomic.Pointer[statusDocSnapshot]
	group     singleflight.Group
	lastSweep atomic.Int64 // unix nanos of the last idle-eviction pass
}

type statusDocSnapshot struct {
	raw []byte
	doc dto.StatusDocument
	at  time.Time // when the document was read from Valkey
}

var _ outbound.IStatusDocReader = (*StatusDocCache)(nil)

// NewStatusDocCache returns a cache over store that reuses a read document for ttl, measured by now.
func NewStatusDocCache(store outbound.IQueueStore, ttl time.Duration, now func() time.Time) *StatusDocCache {
	return &StatusDocCache{store: store, ttl: ttl, now: now}
}

// Get returns the event's Status Document: from memory while the snapshot is younger than ttl, else from Valkey. ok is
// false when the event has no document. A document that does not parse is an error (as the status endpoint always was).
func (c *StatusDocCache) Get(ctx context.Context, eventID string) ([]byte, dto.StatusDocument, bool, error) {
	slot := c.slot(eventID)
	if snap := slot.Load(); snap != nil && c.now().Sub(snap.at) < c.ttl {
		return snap.raw, snap.doc, true, nil
	}
	v, err, _ := c.group.Do(eventID, func() (any, error) {
		// The read is shared by every caller that joined this flight, so it must not die with the leader's request.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusDocReadTimeout)
		defer cancel()
		return c.refresh(rctx, eventID, slot)
	})
	if err != nil {
		return nil, dto.StatusDocument{}, false, err
	}
	snap, _ := v.(*statusDocSnapshot)
	if snap == nil {
		return nil, dto.StatusDocument{}, false, nil
	}
	return snap.raw, snap.doc, true, nil
}

// refresh reads the document from Valkey and stores the snapshot in the event's slot (nil when there is none). An absent
// document or a failed read drops the slot instead, so it keeps no memory behind.
func (c *StatusDocCache) refresh(ctx context.Context, eventID string, slot *atomic.Pointer[statusDocSnapshot]) (*statusDocSnapshot, error) {
	raw, ok, err := c.store.GetStatusDoc(ctx, eventID)
	if err != nil {
		c.drop(eventID, slot)
		return nil, err
	}
	if !ok {
		c.drop(eventID, slot)
		return nil, nil
	}
	var doc dto.StatusDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		c.drop(eventID, slot)
		return nil, fmt.Errorf("status: unmarshal document: %w", err)
	}
	snap := &statusDocSnapshot{raw: raw, doc: doc, at: c.now()}
	slot.Store(snap)
	c.sweep(snap.at)
	return snap, nil
}

// drop removes the event's slot if it still holds this slot (a later slot for the event is left alone), then runs the
// idle sweep, so reads of events with no document leave no slot behind.
func (c *StatusDocCache) drop(eventID string, slot *atomic.Pointer[statusDocSnapshot]) {
	c.slots.CompareAndDelete(eventID, slot)
	c.sweep(c.now())
}

// slot returns the event's snapshot holder, creating it on first use.
func (c *StatusDocCache) slot(eventID string) *atomic.Pointer[statusDocSnapshot] {
	if p, ok := c.slots.Load(eventID); ok {
		return p.(*atomic.Pointer[statusDocSnapshot])
	}
	p, _ := c.slots.LoadOrStore(eventID, new(atomic.Pointer[statusDocSnapshot]))
	return p.(*atomic.Pointer[statusDocSnapshot])
}

// sweep drops the slots whose snapshot is empty or was not refreshed for statusDocIdle. It runs at most once a minute,
// so the cache holds the events read recently rather than every event ever read.
func (c *StatusDocCache) sweep(now time.Time) {
	last := c.lastSweep.Load()
	if now.UnixNano()-last < int64(statusDocIdle) || !c.lastSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	c.slots.Range(func(k, v any) bool {
		if snap := v.(*atomic.Pointer[statusDocSnapshot]).Load(); snap == nil || now.Sub(snap.at) > statusDocIdle {
			c.slots.Delete(k)
		}
		return true
	})
}
