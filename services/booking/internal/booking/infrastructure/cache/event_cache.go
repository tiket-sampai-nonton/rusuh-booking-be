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

// maxCachedKeys bounds the number of keys in each map. List keys come from public query strings, so without a cap the
// number of entries would grow with every distinct city or date a visitor types.
const maxCachedKeys = 1024

// maxCachedListEvents bounds the list cache by rows, not keys: a key cap alone would let 1024 queries each hold the
// largest result (500 rows) and that is still a lot of memory per instance.
const maxCachedListEvents = 20000

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
	c := &EventReadCache{IEventRepo: src, ttl: ttl, now: now}
	c.lists.maxKeys, c.lists.maxWeight, c.lists.weigh = maxCachedKeys, maxCachedListEvents, lenEvents
	c.details.maxKeys = maxCachedKeys
	return c
}

func lenEvents(events []entity.Event) int { return len(events) }

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
	seq uint64 // insertion order: the oldest entry has the smallest seq, even when expiries tie
}

// ttlStore is a mutex-guarded map with expiry and two bounds: a key count, and optionally a total weight (the list
// cache weighs an entry by its number of events, so the budget is about rows held, not keys). Expired entries go
// first, then the oldest by insertion.
type ttlStore[V any] struct {
	mu        sync.Mutex
	m         map[string]ttlEntry[V]
	next      uint64 // sequence of the last insert
	maxKeys   int
	maxWeight int         // 0 means no weight bound
	weigh     func(V) int // nil means every entry weighs 0
	weight    int         // sum of weigh over the entries held
}

func (s *ttlStore[V]) get(key string, now time.Time) (V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if ok && now.Before(e.exp) {
		return e.val, true
	}
	if ok {
		s.remove(key)
	}
	var zero V
	return zero, false
}

func (s *ttlStore[V]) put(key string, val V, exp, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]ttlEntry[V])
	}
	if _, ok := s.m[key]; ok {
		s.remove(key)
	}
	w := s.weightOf(val)
	if s.maxWeight > 0 && w > s.maxWeight {
		return // larger than the whole budget: serve it, never hold it
	}
	s.makeRoom(w, now)
	s.next++
	s.m[key] = ttlEntry[V]{val: val, exp: exp, seq: s.next}
	s.weight += w
}

// size reports the number of keys and the total weight currently held.
func (s *ttlStore[V]) size() (keys, weight int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m), s.weight
}

// makeRoom evicts until one more entry of weight w fits: expired entries first, then the oldest.
func (s *ttlStore[V]) makeRoom(w int, now time.Time) {
	if s.fits(w) {
		return
	}
	for k, e := range s.m {
		if !now.Before(e.exp) {
			s.remove(k)
		}
	}
	if s.fits(w) {
		return
	}
	order := make([]string, 0, len(s.m))
	for k := range s.m {
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool { return s.m[order[i]].seq < s.m[order[j]].seq })
	for _, k := range order {
		if s.fits(w) {
			return
		}
		s.remove(k)
	}
}

func (s *ttlStore[V]) fits(w int) bool {
	return (s.maxKeys <= 0 || len(s.m) < s.maxKeys) && (s.maxWeight == 0 || s.weight+w <= s.maxWeight)
}

func (s *ttlStore[V]) remove(key string) {
	e, ok := s.m[key]
	if !ok {
		return
	}
	s.weight -= s.weightOf(e.val)
	delete(s.m, key)
}

func (s *ttlStore[V]) weightOf(v V) int {
	if s.weigh == nil {
		return 0
	}
	return s.weigh(v)
}
