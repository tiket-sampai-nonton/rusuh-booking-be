// Package cache holds the in-process read cache for the public catalog. It only adds staleness of at most the TTL
// (2 s, D-C7.7): every value it serves is what the repository returned moments earlier, so the SQL rules that derive
// status, stock and availability (AC-C4.2, AC-C5.1) run unchanged on each load.
package cache

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// maxCachedKeys bounds each map. List keys come from public query strings, so without a cap the number of entries
// would grow with every distinct city or date a visitor types.
const maxCachedKeys = 1024

// catalogLoadTimeout bounds one shared load. The load runs detached from the caller that started it (see
// EventReadCache.GetDetail), so this timeout is what stops a hung query from blocking later callers forever.
const catalogLoadTimeout = 10 * time.Second

// EventReadCache serves ListEvents and GetDetail from memory for ttl and merges concurrent misses for the same key
// into one repository call. Every other IEventRepo method passes through to the embedded source unchanged.
type EventReadCache struct {
	outbound.IEventRepo

	ttl time.Duration
	now func() time.Time

	lists        ttlStore[[]entity.Event]
	details      ttlStore[entity.EventDetail]
	listFlight   singleflight.Group
	detailFlight singleflight.Group
}

// NewEventReadCache wraps src. A zero or negative ttl makes every read a miss that still shares in-flight loads.
func NewEventReadCache(src outbound.IEventRepo, ttl time.Duration, now func() time.Time) *EventReadCache {
	return &EventReadCache{IEventRepo: src, ttl: ttl, now: now}
}

// ListEvents returns the catalog for q, reusing a load of the same normalised query for ttl.
func (c *EventReadCache) ListEvents(ctx context.Context, q dto.EventListQuery) ([]entity.Event, error) {
	key := listKey(q)
	if v, ok := c.lists.get(key, c.now()); ok {
		return v, nil
	}
	ch := c.listFlight.DoChan(key, func() (any, error) {
		lctx, cancel := loadContext(ctx)
		defer cancel()
		events, err := c.IEventRepo.ListEvents(lctx, q)
		if err != nil {
			return nil, err
		}
		c.lists.put(key, events, c.now().Add(c.ttl), c.now())
		return events, nil
	})
	return waitFor[[]entity.Event](ctx, ch)
}

// GetDetail returns one event with its categories, reusing a load of the same id for ttl.
func (c *EventReadCache) GetDetail(ctx context.Context, eventID string) (entity.EventDetail, error) {
	if v, ok := c.details.get(eventID, c.now()); ok {
		return v, nil
	}
	ch := c.detailFlight.DoChan(eventID, func() (any, error) {
		lctx, cancel := loadContext(ctx)
		defer cancel()
		detail, err := c.IEventRepo.GetDetail(lctx, eventID)
		if err != nil {
			return nil, err
		}
		c.details.put(eventID, detail, c.now().Add(c.ttl), c.now())
		return detail, nil
	})
	return waitFor[entity.EventDetail](ctx, ch)
}

// loadContext keeps the caller's values but not its cancellation: one client that disconnects must not fail the load
// that other callers are waiting on. The timeout keeps a stuck query from holding the key forever.
func loadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), catalogLoadTimeout)
}

// waitFor returns the shared result, or gives up when this caller's own context ends first.
func waitFor[V any](ctx context.Context, ch <-chan singleflight.Result) (V, error) {
	var zero V
	select {
	case r := <-ch:
		if r.Err != nil {
			return zero, r.Err
		}
		return r.Val.(V), nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// listKey is the cache key of a catalog query: every field that changes the result, with genres sorted (the filter is
// an overlap test, so order does not matter) and times as UTC values, never pointers. JSON encoding keeps fields
// unambiguous, so a city containing a separator cannot collide with another query.
func listKey(q dto.EventListQuery) string {
	genres := append([]string(nil), q.Genres...)
	sort.Strings(genres)
	b, _ := json.Marshal(listKeyParts{
		Status:    q.Status,
		City:      q.City,
		Timeframe: q.Timeframe,
		Sort:      q.Sort,
		Genres:    genres,
		StartUTC:  formatInstant(q.StartUTC),
		EndUTC:    formatInstant(q.EndUTC),
	})
	return string(b)
}

type listKeyParts struct {
	Status    string   `json:"status"`
	City      string   `json:"city"`
	Timeframe string   `json:"timeframe"`
	Sort      string   `json:"sort"`
	Genres    []string `json:"genres"`
	StartUTC  string   `json:"startUtc"`
	EndUTC    string   `json:"endUtc"`
}

// formatInstant renders an optional instant as RFC 3339 in UTC (fractional seconds kept, so two instants never share a
// key), or "" when absent.
func formatInstant(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type ttlEntry[V any] struct {
	val V
	exp time.Time
}

// ttlStore is a mutex-guarded map with expiry. Expired entries are dropped on access, and when the map is full the
// expired ones are purged; if it is still full it is reset, which keeps memory bounded without an LRU.
type ttlStore[V any] struct {
	mu sync.Mutex
	m  map[string]ttlEntry[V]
}

func (s *ttlStore[V]) get(key string, now time.Time) (V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !now.Before(e.exp) {
		delete(s.m, key)
		var zero V
		return zero, false
	}
	return e.val, true
}

func (s *ttlStore[V]) put(key string, val V, exp, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]ttlEntry[V])
	}
	if _, exists := s.m[key]; !exists && len(s.m) >= maxCachedKeys {
		for k, e := range s.m {
			if !now.Before(e.exp) {
				delete(s.m, k)
			}
		}
		if len(s.m) >= maxCachedKeys {
			clear(s.m)
		}
	}
	s.m[key] = ttlEntry[V]{val: val, exp: exp}
}
