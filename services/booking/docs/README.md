# Booking Service

Event dan kategori (modul katalog read-only), alokasi, sesi pilih, hold, ubah pesanan, kunci bayar, settlement, e-tiket. Agregat Booking memakai event sourcing (booking_events).

- Port: `8082` (`APP_PORT`)
- Schema PostgreSQL: `booking` (role `svc_booking`)
- Verb: api, worker (sweeper TTL sesi dan pembayaran, outbox relay, consumer payment.*), `migrate up|down|status`, `healthcheck`

## Menjalankan lokal

```bash
make infra keys                     # dari root repo: PostgreSQL, Valkey, RabbitMQ, Mailpit + kunci token lokal
cd services/booking && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api
curl localhost:8082/health
```

Desain lengkap: `docs/analisa/04-SDD-RBT-001.md`. Struktur folder mengikuti standar organisasi (ADR-016).
