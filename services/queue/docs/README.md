# Queue Service

Waiting room: join, posisi, klaim giliran, promotor admission, Status Document (CDN).

- Port: `8081` (`APP_PORT`)
- Schema PostgreSQL: `queue` (role `svc_queue`)
- Verb: api, worker (promotor + publisher Status Document + consumer booking.*), `migrate up|down|status`, `healthcheck`

## Menjalankan lokal

```bash
make infra keys                     # dari root repo: PostgreSQL, Valkey, RabbitMQ, Mailpit + kunci token lokal
cd services/queue && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api
curl localhost:8081/health
```

Desain lengkap: `docs/analisa/04-SDD-RBT-001.md`. Struktur folder mengikuti standar organisasi (ADR-016).
