# Notification Service

Email e-tiket/expired/review dan push giliran dari event booking.* dan queue.turn.*.

- Port: `8084` (`APP_PORT`)
- Schema PostgreSQL: `notification` (role `svc_notification`)
- Verb: api (push subscription), worker (consumer), `migrate up|down|status`, `healthcheck`

## Menjalankan lokal

```bash
make infra keys                     # dari root repo: PostgreSQL, Valkey, RabbitMQ, Mailpit + kunci token lokal
cd services/notification && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api
curl localhost:8084/health
```

Desain lengkap: `docs/sdd/04-SDD-RBT-001.md`. Struktur folder mengikuti standar organisasi (ADR-016).
