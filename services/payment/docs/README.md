# Payment Service

Halaman pembayaran, attempt Midtrans, webhook, rekonsiliasi, refund case. Agregat Payment Attempt memakai event sourcing (payment_events).

- Port: `8083` (`APP_PORT`)
- Schema PostgreSQL: `payment` (role `svc_payment`)
- Verb: api, worker (rekonsiliasi UNKNOWN, outbox relay, consumer booking.review), `migrate up|down|status`, `healthcheck`

## Menjalankan lokal

```bash
make infra keys                     # dari root repo: PostgreSQL, Valkey, RabbitMQ, Mailpit + kunci token lokal
cd services/payment && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api
curl localhost:8083/health
```

Desain lengkap: `docs/analisa/04-SDD-RBT-001.md`. Struktur folder mengikuti standar organisasi (ADR-016).
