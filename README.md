# rusuh-booking-be

Backend **Rusuh Booking Tiket**: 5 microservice Go (Queue, Booking, Payment, Notification, User) dalam satu monorepo, dengan struktur standar proyek organisasi.
Disusun oleh Tim PT. Tiket Sampai Nonton Sentosa untuk PT. Tiga Sekawan Angin Ribut.

> Status: **boilerplate siap Sprint 1**. Semua service punya verb Cobra (`api`, `worker`, `migrate`, `healthcheck`), konfigurasi tervalidasi, client PostgreSQL/Valkey/RabbitMQ, migrasi DDL lengkap, dan `GET /health`. User Service (login OTP opsional) sudah berfungsi penuh. Logika bisnis Queue, Booking, Payment, Notification dibangun mulai 20 Okt sesuai `docs/` dan kontrak `docs/contracts/`.

## Service

| Service | Path | Port | Verb | Dependency | Schema | Owner |
|---|---|---|---|---|---|---|
| Queue | `services/queue` | 8081 | api, worker | postgres, valkey, rabbitmq | `queue` | BE-1 |
| Booking | `services/booking` | 8082 | api, worker | postgres, valkey, rabbitmq | `booking` (event sourcing) | BE-2 |
| Payment | `services/payment` | 8083 | api, worker | postgres, rabbitmq | `payment` (event sourcing) | BE-2 |
| Notification | `services/notification` | 8084 | api, worker | postgres, rabbitmq | `notification` | BE-1 |
| User | `services/user` | 8085 | api | postgres, valkey, SMTP | `users` | BE-1 |

Satu image per service, satu verb per container (`booking api`, `booking worker`, `booking migrate up`). Setiap service punya role database sendiri (`svc_<service>`) yang hanya melihat schema miliknya.

## Mulai cepat

Prasyarat: Go 1.26, Docker dengan Compose, `golangci-lint` v2, `openssl`.

```bash
make tidy        # sekali setelah clone/patch: isi go.sum, lalu commit go.mod dan go.sum
make up          # kunci token lokal + PostgreSQL, Valkey, RabbitMQ, Mailpit, migrasi, semua service
make ps
curl localhost:8085/health
make down
```

Tanpa Docker untuk service (infra tetap via compose):

```bash
make infra keys
cd services/user && cp .env.example .env && go run ./cmd migrate up && go run ./cmd api
```

Coba login OTP: `POST localhost:8085/api/v1/auth/otp/request {"email":"kamu@contoh.id"}`, buka Mailpit http://localhost:8025 untuk kodenya, lalu `POST /api/v1/auth/otp/verify`.

## Perintah

| Target | Fungsi |
|---|---|
| `make build` / `make test` / `make lint` | build, unit test (race), lint semua modul |
| `make test-integration` | unit + integration test (PostgreSQL, Valkey, RabbitMQ dari `make infra`) |
| `make cover` | sama dengan stage `case-coverage` CI (ambang 80%) |
| `make mock` | generate mock (`go tool mockgen`, versi dikunci `go.mod`) ke `_mock/` |
| `make tidy` | `go mod tidy` semua modul |
| `make isolation` | build tiap service tanpa `go.work` |
| `make migrate-<svc>` / `make run-<svc>` | migrasi atau jalankan API satu service lokal |
| `make ci` | semua pemeriksaan CI lokal |

## Struktur

```text
services/<svc>/        satu proyek Go per service: cmd (Cobra), internal/<domain>/{port,domain,application,infrastructure},
                       internal/{config,delivery,shared}, _mock, deployments/{Dockerfile,migrations}, assets, docs
pkg/                   utilitas lintas service tanpa logika bisnis: apperror, httpx, dotenv, clock, logger, postgres,
                       valkey, rabbitmq (confirm, retry berjenjang, DLQ), migrate (advisory lock), eventstore, token (EdDSA), crypto
deployments/           compose (init schema/role), terraform, observability
docs/                  PRD, SRS, HLD, SDD, ADR, Test Plan, Handbook, kontrak OpenAPI dan event, evidence, ai-log
.kiro/steering, specs/ steering dan spec Kiro
tests/                 test lintas service (BDD, k6, contract)
```

Aturan struktur: `.kiro/steering/structure.md` dan Handbook §0. Lapisan ditegakkan `depguard` di `.golangci.yml`; import lintas service dicegah Go `internal/`.

## CI/CD

Satu pipeline `.github/workflows/ci.yml` dengan stage berurutan (matrix per modul/service; yang dijalankan hanya modul yang berubah, perubahan `pkg/` atau konfigurasi bersama menjalankan semua):

| Stage | Isi |
|---|---|
| `code-quality` | `go.mod`/`go.sum` rapi, `_mock/` sesuai port, golangci-lint (+ depguard), go vet, gosec, govulncheck, hadolint, gitleaks |
| `verify-code-quality` | gerbang: gagal bila gosec HIGH, kerentanan govulncheck, hadolint error, atau rahasia bocor |
| `case-coverage` | unit + integration test `-race` (PostgreSQL, Valkey, RabbitMQ sebagai service container) + ambang coverage logika **80%** (`COVERAGE_MIN`) |
| `code-rank` | SonarCloud (quality gate) |
| `build` | compile + isolation build `GOWORK=off` |
| `package` | docker build per service, **linux/arm64** (runner ARM GitHub; ganti lewat variable `ARM_RUNNER`) |
| `scan-package` | Trivy scan image + **SBOM CycloneDX** (artifact `sbom-<service>`), lalu `scan-package-verify` (gagal bila severity `IMAGE_FAIL_SEVERITY`, default CRITICAL) |
| `publish` | push image ke GHCR, hanya dari `main`; digest dicatat di ringkasan run |
| `deployment-dev`, `deployment-prod` | **manual** (Run workflow, pilih `deploy_env`); sementara placeholder |

`ci-ok` adalah satu-satunya check yang perlu diwajibkan di ruleset `main`; ia lulus bila semua stage lulus atau dilewati.

Integration test adapter (build tag `integration`) berjalan di `case-coverage`. Test end-to-end lintas service dan BDD (godog) ditambahkan saat alur pertama Queue → Booking → Payment siap (Handbook §2).

Coverage dihitung atas kode logika. `cmd/`, `internal/delivery/`, `internal/config/`, dan `_mock/` dikecualikan karena hanya bootstrap dan wiring (dibuktikan lewat run dan health check). Lokal: `make cover`.

### Yang perlu diisi (placeholder)

| Nilai | Jenis | Dipakai oleh |
|---|---|---|
| `SONAR_ENABLED=true`, `SONAR_ORGANIZATION`, `SONAR_PROJECT_KEY` | repo variables | `code-rank` (dilewati bila `SONAR_ENABLED` bukan `true`) |
| `SONAR_TOKEN` | repo secret | `code-rank` |
| `AWS_REGION`, `AWS_ROLE_ARN_DEV`, `AWS_ROLE_ARN_PROD` | repo variables (OIDC) | `deployment-*` |
| Environment `dev` dan `prod` (prod: Required reviewers) | Settings > Environments | `deployment-*` |
| Perintah migrasi dan deploy | langkah bertanda `GANTI` di `ci.yml` | `deployment-*` |

Deployment belum melakukan deploy nyata: langkahnya hanya mencetak peringatan `PLACEHOLDER` sampai Terraform dan role AWS siap.

## Repo terkait

- `tiket-sampai-nonton/rusuh-booking-web` — Next.js (termasuk payment page `/pay/{bookingId}`)
- `tiket-sampai-nonton/rusuh-booking-mobile` — React Native
