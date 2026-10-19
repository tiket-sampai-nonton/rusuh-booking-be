---
inclusion: always
---

# Struktur Proyek (WAJIB)

Setiap `services/<svc>/` mengikuti standar proyek organisasi (ADR-016, SDD §4.1, Handbook §0):

- `cmd/main.go` — entry point Cobra; verb: `api`, `worker` (Queue, Booking, Payment, Notification), `migrate up|down|status`, `healthcheck` (D-14)
- `internal/<domain>/port/inbound` — interface usecase; `port/outbound` — interface repository/client/gateway
- `internal/<domain>/domain/{entity,valueobject,dto,mapper}` — domain murni, hanya standard library
- `internal/<domain>/application/usecase` — satu usecase per file
- `internal/<domain>/infrastructure/<teknologi>` — adapter (http, postgres, valkey, rabbitmq, midtrans, smtp, fcm)
- `internal/shared/entity`, `internal/config/{config.go,load.go}`, `internal/delivery/{server.go,worker.go}` (composition root)
- `_mock/` — hasil `make mock` (mockgen), tidak diedit manual
- `assets/`, `deployments/{Dockerfile,migrations}`, `docs/`, `go.mod`
- Migrasi: `deployments/migrations/NNNN_nama.up.sql` + `.down.sql`, tertanam di binary (`embed.go`); file yang sudah diterapkan tidak boleh diubah
- Route registrar per domain: setiap handler punya `Register(mux)`; composition root hanya memasang
- 5 service: queue, booking, payment, notification, user (D-03, ADR-020)
- Interface selalu berawalan `I` dan hanya didefinisikan di `port/` (atau `pkg/`).
- Utilitas lintas service di `pkg/` (tanpa logika bisnis). Tidak ada import antar `services/*`.
