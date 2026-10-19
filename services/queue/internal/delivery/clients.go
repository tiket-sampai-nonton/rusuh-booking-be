package delivery

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/healthcheck"
)

// Need lists the clients a verb builds; setiap verb hanya membuka koneksi yang dipakai.
type Need struct {
	Postgres bool
	Valkey   bool
	RabbitMQ bool
}

// Clients holds the infrastructure clients of one process.
type Clients struct {
	PG *postgres.Client
	VK *valkey.Client
	MQ *rabbitmq.Connection
}

// NewClients connects (and pings) every needed dependency. Gagal satu, semua yang sudah terbuka ditutup.
func NewClients(ctx context.Context, cfg config.Config, need Need) (*Clients, error) {
	c := &Clients{}

	if need.Postgres {
		pg, err := postgres.New(ctx, cfg.Postgres)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.PG = pg
	}

	if need.Valkey {
		vk, err := valkey.New(ctx, cfg.Valkey)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.VK = vk
	}

	if need.RabbitMQ {
		mq, err := rabbitmq.Dial(cfg.RabbitMQ)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.MQ = mq
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
	if c.MQ != nil {
		out = append(out, c.MQ)
	}
	return out
}

// Close releases everything (urutan kebalikan pembukaan tidak penting: tiap client independen).
func (c *Clients) Close() {
	if c.PG != nil {
		c.PG.Close()
	}
	if c.VK != nil {
		c.VK.Close()
	}
	if c.MQ != nil {
		_ = c.MQ.Close()
	}
}
