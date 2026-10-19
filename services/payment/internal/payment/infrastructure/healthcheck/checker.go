// Package healthcheck adapts infrastructure clients (pkg/postgres, pkg/valkey, pkg/rabbitmq) to IDependencyChecker.
package healthcheck

import (
	"context"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

// Pinger is implemented by every infrastructure client.
type Pinger interface {
	Name() string
	Ping(ctx context.Context) error
}

// Checker pings all dependencies in parallel within timeout.
type Checker struct {
	pingers []Pinger
	timeout time.Duration
}

// New returns a checker.
func New(pingers []Pinger, timeout time.Duration) *Checker {
	return &Checker{pingers: pingers, timeout: timeout}
}

// CheckAll implements outbound.IDependencyChecker. Urutan hasil sama dengan urutan pinger.
func (c *Checker) CheckAll(ctx context.Context) []entity.DependencyStatus {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out := make([]entity.DependencyStatus, len(c.pingers))
	var wg sync.WaitGroup
	for i, p := range c.pingers {
		wg.Add(1)
		go func(i int, p Pinger) {
			defer wg.Done()
			name, err := valueobject.NewDependencyName(p.Name())
			if err != nil {
				out[i] = entity.DependencyStatus{Reason: "invalid dependency name"}
				return
			}
			start := time.Now()
			perr := p.Ping(ctx)
			st := entity.DependencyStatus{Name: name, Up: perr == nil, Latency: time.Since(start)}
			if perr != nil {
				st.Reason = perr.Error()
			}
			out[i] = st
		}(i, p)
	}
	wg.Wait()
	return out
}
