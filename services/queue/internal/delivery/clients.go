package delivery

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/healthcheck"
	queuepg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/postgres"
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

	audit *queuepg.AsyncEntryAudit // best-effort entry_audit writer; runs until Close (D-C8.5)
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
		c.audit = startEntryAudit(pg.Pool())
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

// Close releases everything. The audit writer is flushed first, while the pool it writes through is still open.
func (c *Clients) Close() {
	if c.audit != nil {
		ctx, cancel := context.WithTimeout(context.Background(), entryAuditDrainTimeout)
		if err := c.audit.Close(ctx); err != nil {
			slog.Warn("entry_audit: flush saat shutdown tidak lengkap", "error", err)
		}
		cancel()
	}
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
