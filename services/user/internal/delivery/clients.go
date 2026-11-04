package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/infrastructure/healthcheck"
)

// Need lists the clients a verb builds; setiap verb hanya membuka koneksi yang dipakai.
type Need struct {
	Postgres bool
	Valkey   bool
}

// Clients holds the infrastructure clients of one process.
type Clients struct {
	PG      *postgres.Client
	VK      *valkey.Client
	closers []namedCloser
}

type namedCloser struct {
	name string
	fn   func() error
}

// AddCloser registers a resource (mis. koneksi RabbitMQ) to be closed by Close.
func (c *Clients) AddCloser(name string, fn func() error) {
	c.closers = append(c.closers, namedCloser{name: name, fn: fn})
}

// NewClients connects (and pings) every needed dependency. Gagal satu, semua yang sudah terbuka ditutup.
func NewClients(ctx context.Context, cfg config.Config, need Need) (*Clients, error) {
	c := &Clients{}

	if need.Postgres {
		pg, err := postgres.New(ctx, cfg.Postgres)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		c.PG = pg
	}

	if need.Valkey {
		vk, err := valkey.New(ctx, cfg.Valkey)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		c.VK = vk
	}

	return c, nil
}

// Pingers returns the opened clients for GET /health.
func (c *Clients) Pingers() []healthcheck.Pinger {
	var out []healthcheck.Pinger
	if c.PG != nil {
		out = append(out, c.PG)
	}
	if c.VK != nil {
		out = append(out, c.VK)
	}
	return out
}

// Close releases everything: closers terdaftar dalam urutan terbalik, lalu Postgres dan Valkey. Tiap closer
// dijalankan paling banyak sekali; error digabung (tanpa nilai kredensial: hanya nama resource dan error-nya).
func (c *Clients) Close() error {
	var errs []error
	for i := len(c.closers) - 1; i >= 0; i-- {
		if err := c.closers[i].fn(); err != nil {
			errs = append(errs, fmt.Errorf("tutup %s: %w", c.closers[i].name, err))
		}
	}
	c.closers = nil
	if c.PG != nil {
		c.PG.Close()
	}
	if c.VK != nil {
		c.VK.Close()
	}
	return errors.Join(errs...)
}
