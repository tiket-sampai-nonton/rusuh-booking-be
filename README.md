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

Satu pipeline `.github/workflows/ci.yml` dengan stage berurutan (matrix per modul/service; yang dijalankan hanya modul yang berubah, perubahan `pkg/` atau konfigurasi bersama menjalankan semua):

| Stage | Isi |
|---|---|
| `code-quality` | golangci-lint (+ depguard), go vet, gosec, govulncheck, hadolint, gitleaks |
| `verify-code-quality` | gerbang: gagal bila gosec HIGH, kerentanan govulncheck, hadolint error, atau rahasia bocor |
| `case-coverage` | unit test `-race` + ambang coverage logika **80%** (`COVERAGE_MIN`) |
| `code-rank` | SonarCloud (quality gate) |
| `build` | compile + isolation build `GOWORK=off` |
| `package` | docker build per service, **linux/arm64** (runner ARM GitHub; ganti lewat variable `ARM_RUNNER`) |
| `scan-package` | Trivy scan image + **SBOM CycloneDX** (artifact `sbom-<service>`), lalu `scan-package-verify` (gagal bila severity `IMAGE_FAIL_SEVERITY`, default CRITICAL) |
| `publish` | push image ke GHCR, hanya dari `main`; digest dicatat di ringkasan run |
| `deployment-dev`, `deployment-prod` | **manual** (Run workflow, pilih `deploy_env`); sementara placeholder |

`ci-ok` adalah satu-satunya check yang perlu diwajibkan di ruleset `main`; ia lulus bila semua stage lulus atau dilewati.

Integration test (Compose) dan BDD (godog) belum masuk pipeline; keduanya dijalankan lokal dan ditambahkan sebagai stage sesudah `case-coverage` saat fitur pertama dengan PostgreSQL, Valkey, dan RabbitMQ siap (Handbook §2).

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
