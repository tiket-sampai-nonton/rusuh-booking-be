// Package resilience holds the circuit breaker and bulkhead used on every synchronous call to another
// service or provider (SDD §4.6.2 "Konfigurasi Circuit Breaker Awal", D-40: MUST).
package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrOpen is returned without calling the dependency while the breaker is open.
var ErrOpen = errors.New("resilience: circuit breaker open")

// ErrBulkheadFull is returned when the concurrency limit is reached.
var ErrBulkheadFull = errors.New("resilience: bulkhead penuh")

// State of a breaker.
type State int

// States.
const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	return [...]string{"closed", "open", "half_open"}[s]
}

// BreakerConfig mirrors the SDD table: ambang gagal beruntun, cooldown OPEN, probe half-open, timeout.
type BreakerConfig struct {
	Name             string
	FailureThreshold int           // gagal beruntun sebelum OPEN
	Cooldown         time.Duration // lama OPEN sebelum HALF_OPEN
	HalfOpenProbes   int           // panggilan percobaan saat HALF_OPEN
	Timeout          time.Duration // batas waktu tiap panggilan (0 = tanpa batas tambahan)
	// IsFailure decides which errors count; default: semua error kecuali context.Canceled.
	IsFailure func(error) bool
	// OnStateChange is called after each transition (untuk metrik/log).
	OnStateChange func(name string, from, to State)
}

// Breaker is a consecutive-failure circuit breaker, safe for concurrent use. Lokal per instance.
type Breaker struct {
	cfg      BreakerConfig
	now      func() time.Time
	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
	inFlight int // probe yang sedang berjalan saat HALF_OPEN
}

// NewBreaker validates cfg and returns a closed breaker.
func NewBreaker(cfg BreakerConfig, now func() time.Time) (*Breaker, error) {
	if cfg.FailureThreshold < 1 || cfg.Cooldown <= 0 || cfg.HalfOpenProbes < 1 || cfg.Timeout < 0 {
		return nil, fmt.Errorf("resilience: konfigurasi breaker %q tidak valid", cfg.Name)
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(err error) bool { return err != nil && !errors.Is(err, context.Canceled) }
	}
	if now == nil {
		now = time.Now
	}
	return &Breaker{cfg: cfg, now: now}, nil
}

// State returns the current state (OPEN berubah menjadi HALF_OPEN setelah cooldown).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	return b.state
}

func (b *Breaker) advance() {
	if b.state == Open && b.now().Sub(b.openedAt) >= b.cfg.Cooldown {
		b.transition(HalfOpen)
	}
}

func (b *Breaker) transition(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	b.inFlight = 0
	if to == Open {
		b.openedAt = b.now()
	}
	if to == Closed {
		b.failures = 0
	}
	if b.cfg.OnStateChange != nil {
		b.cfg.OnStateChange(b.cfg.Name, from, to)
	}
}

// Do calls fn unless the breaker is open. fn menerima context dengan Timeout bila diset.
func (b *Breaker) Do(ctx context.Context, fn func(context.Context) error) error {
	b.mu.Lock()
	b.advance()
	switch b.state {
	case Open:
		b.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrOpen, b.cfg.Name)
	case HalfOpen:
		if b.inFlight >= b.cfg.HalfOpenProbes {
			b.mu.Unlock()
			return fmt.Errorf("%w: %s (probe sedang berjalan)", ErrOpen, b.cfg.Name)
		}
		b.inFlight++
	}
	b.mu.Unlock()

	callCtx := ctx
	if b.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, b.cfg.Timeout)
		defer cancel()
	}
	err := fn(callCtx)

	b.mu.Lock()
	defer b.mu.Unlock()
	failed := b.cfg.IsFailure(err)
	switch b.state {
	case HalfOpen:
		if failed {
			b.transition(Open)
		} else {
			b.transition(Closed)
		}
	case Closed:
		if failed {
			b.failures++
			if b.failures >= b.cfg.FailureThreshold {
				b.transition(Open)
			}
		} else {
			b.failures = 0
		}
	}
	return err
}

// Bulkhead limits concurrent calls to one dependency.
type Bulkhead struct {
	slots chan struct{}
	wait  time.Duration
}

// NewBulkhead returns a bulkhead with max concurrent calls; wait = berapa lama menunggu slot (0 = langsung tolak).
func NewBulkhead(max int, wait time.Duration) (*Bulkhead, error) {
	if max < 1 || wait < 0 {
		return nil, errors.New("resilience: bulkhead tidak valid")
	}
	return &Bulkhead{slots: make(chan struct{}, max), wait: wait}, nil
}

// Do runs fn when a slot is free.
func (b *Bulkhead) Do(ctx context.Context, fn func(context.Context) error) error {
	select {
	case b.slots <- struct{}{}:
	default:
		if b.wait == 0 {
			return ErrBulkheadFull
		}
		t := time.NewTimer(b.wait)
		defer t.Stop()
		select {
		case b.slots <- struct{}{}:
		case <-t.C:
			return ErrBulkheadFull
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { <-b.slots }()
	return fn(ctx)
}
