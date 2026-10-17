# rusuh-booking-be

Backend **Rusuh Booking Tiket**: 4 service Go (Queue, Booking, Payment, Notification) dengan struktur standar proyek organisasi.
Disusun oleh Tim PT. Tiket Sampai Nonton Sentosa untuk PT. Tiga Sekawan Angin Ribut.

> Status: **skeleton**. Setiap service sudah punya wiring lengkap (config, delivery, port, usecase, adapter) dengan contoh `/healthz` dan `/readyz`. Logika bisnis dibangun mulai sprint dev sesuai `docs/`.

## Service

| Service | Path | Port | Mode | Dependency | Owner |
|---|---|---|---|---|---|
| Queue | `services/queue` | 8081 | api | valkey, postgres, rabbitmq | BE-1 |
| Booking | `services/booking` | 8082 | api, worker | postgres, rabbitmq, valkey | BE-2 |
| Payment | `services/payment` | 8083 | api, worker | postgres, rabbitmq | BE-2 |
| Notification | `services/notification` | 8084 | api | postgres, rabbitmq | BE-1 |

Booking dan Payment berjalan sebagai dua container (`api` dan `worker`); port worker di host = port api + 10.

## Mulai cepat

Prasyarat: Go 1.24+, Docker dengan Compose, `golangci-lint` v2.

```bash
make up          # PostgreSQL, Valkey, RabbitMQ, dan semua service
make ps
curl localhost:8082/readyz
make down
```

Tanpa Docker untuk service (infra tetap via compose):

```bash
docker compose up -d postgres valkey rabbitmq
cd services/booking && cp .env.example .env && go run ./cmd --mode=api
```

## Perintah

| Target | Fungsi |
|---|---|
| `make build` / `make test` / `make lint` | build, test (race), lint semua modul |
| `make mock` | generate mock dari interface `port/` ke `_mock/` (mockgen, versi dikunci di `//go:generate`) |
| `make isolation` | build tiap service tanpa `go.work` |
| `make ci` | semua pemeriksaan CI |
| `make run-<svc>` | jalankan satu service |

## Struktur

```text
services/<svc>/        satu proyek Go per service (cmd, internal/<domain>/{port,domain,application,infrastructure}, delivery, config, _mock, deployments)
pkg/                   utilitas lintas service (apperror, httpx, dotenv, clock)
deployments/           compose, terraform, observability
docs/                  PRD, SRS, HLD, SDD, ADR, Test Plan, Handbook, kontrak API/event, evidence, ai-log
.kiro/steering, specs/ steering dan spec Kiro
tests/                 test lintas service (BDD, k6, contract)
```

Aturan struktur: `.kiro/steering/structure.md` dan Handbook §0. Lapisan ditegakkan `depguard` di `.golangci.yml`.

## CI/CD

GitHub Actions per service (`.github/workflows/ci-<svc>.yml`), jalan hanya bila path service atau `pkg/` berubah: generate mock, lint, vet, test (race), isolation build, docker build.

## Repo terkait

- `tiket-sampai-nonton/rusuh-booking-web` — Next.js (termasuk payment page `/pay/{bookingId}`)
- `tiket-sampai-nonton/rusuh-booking-mobile` — React Native
