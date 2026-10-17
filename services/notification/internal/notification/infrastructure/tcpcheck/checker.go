// Package tcpcheck implements outbound.IDependencyChecker with a TCP dial.
// Pemeriksaan ini sengaja sederhana; ganti dengan ping driver (pgx, valkey-go, amqp091-go) saat adapter data ditambahkan.
package tcpcheck

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/valueobject"
)

// Target is one dependency endpoint.
type Target struct {
	Name valueobject.DependencyName
	Addr string
}

// Checker dials every target concurrently.
type Checker struct {
	targets []Target
	timeout time.Duration
}

// New creates a Checker.
func New(targets []Target, timeout time.Duration) *Checker {
	return &Checker{targets: targets, timeout: timeout}
}

// CheckAll dials all targets and returns their status in the configured order.
func (c *Checker) CheckAll(ctx context.Context) []entity.DependencyStatus {
	out := make([]entity.DependencyStatus, len(c.targets))
	var wg sync.WaitGroup
	for i, t := range c.targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			out[i] = c.check(ctx, t)
		}(i, t)
	}
	wg.Wait()
	return out
}

func (c *Checker) check(ctx context.Context, t Target) entity.DependencyStatus {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", t.Addr)
	st := entity.DependencyStatus{Name: t.Name, Latency: time.Since(start)}
	if err != nil {
		st.Reason = "tidak terjangkau"
		return st
	}
	_ = conn.Close()
	st.Reachable = true
	return st
}
