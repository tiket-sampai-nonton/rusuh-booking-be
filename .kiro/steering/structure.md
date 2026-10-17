---
inclusion: always
---

# Struktur Proyek (WAJIB)

Setiap `services/<svc>/` mengikuti standar proyek organisasi (ADR-016, SDD §4.1, Handbook §0):

- `cmd/main.go` — entry point; `--mode=api|worker` untuk Booking dan Payment
- `internal/<domain>/port/inbound` — interface usecase; `port/outbound` — interface repository/client/gateway
- `internal/<domain>/domain/{entity,valueobject,dto,mapper}` — domain murni, hanya standard library
- `internal/<domain>/application/usecase` — satu usecase per file
- `internal/<domain>/infrastructure/<teknologi>` — adapter (http, postgres, valkey, rabbitmq, midtrans, smtp, fcm)
- `internal/shared/entity`, `internal/config/{config.go,load.go}`, `internal/delivery/{server.go,worker.go}` (composition root)
- `_mock/` — hasil `make mock` (mockgen), tidak diedit manual
- `assets/`, `deployments/{Dockerfile,migrations}`, `docs/`, `go.mod`
- Interface selalu berawalan `I` dan hanya didefinisikan di `port/` (atau `pkg/`).
- Utilitas lintas service di `pkg/` (tanpa logika bisnis). Tidak ada import antar `services/*`.
