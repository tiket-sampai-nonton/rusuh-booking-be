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
| `make cover` | sama dengan stage `case-coverage` CI (ambang 80%, tanpa pengecualian paket) |
| `make test-e2e` | E2E lintas service: membangun dan menjalankan 5 service di host terhadap infra Compose (`make infra`), lalu `tests/e2e` (login OTP → event → antre → klaim → sesi → hold → token Snap Sandbox → webhook → PAID → tiket → email) dan memvalidasi setiap respons terhadap OpenAPI. `E2E_SLOW=1` menambah skenario expiry (±5 menit). Butuh `MIDTRANS_SERVER_KEY` di `services/payment/.env` |
| `make docs` | Swagger UI untuk semua spesifikasi di `docs/contracts/openapi` (Booking, Queue, Payment, User, Notification, internal/admin) di http://localhost:8089 |
| `make mock` | generate mock (`go tool mockgen`, versi dikunci `go.mod`) ke `_mock/` |
| `make tidy` | `go mod tidy` semua modul |
| `make isolation` | build tiap service tanpa `go.work` |
| `make migrate-<svc>` / `make run-<svc>` | migrasi atau jalankan API satu service lokal |
| `make ci` | semua pemeriksaan CI lokal |

## Konfigurasi keamanan yang wajib (di luar `APP_ENV=local`)

| Variabel | Service | Fungsi |
|---|---|---|
| `ADMIN_EMAILS` | booking | email (atau `@domain`) pemilik token pembeli yang boleh memakai `/internal/v1/admin/*`; kosong = tidak ada |
| `TICKET_LINK_KEY` | booking | kunci HMAC token tautan e-tiket (32 byte hex) |
| `PII_HASH_KEY`, `PII_ENC_KEY` | queue, booking, notification, user | **sama di semua service**: hash NIK cocok, dan email pembeli yang dienkripsi Queue bisa dibuka Notification (ADR-026) |
| `RATE_LIMIT_PER_IP`, `RATE_LIMIT_WINDOW`, `RATE_LIMIT_WEBHOOK_PER_IP` | booking, payment (webhook hanya payment) | batas request per IP per jendela (default 300 / 1m; webhook 600), dalam-memori per instance, 0 = nonaktif; lewat batas → 429 `RATE_LIMITED` + `Retry-After`. `/health`, `/metrics` dikecualikan |
| `IMAGE_STORAGE`, `IMAGE_LOCAL_DIR`, `IMAGE_S3_BUCKET`, `IMAGE_S3_ENDPOINT`, `IMAGE_PUBLIC_BASE_URL` | booking | penyimpanan gambar event: `local` (default; direktori git-ignored, dilayani di `/media`, hanya `APP_ENV=local`) atau `s3` (bucket dari Terraform `images_bucket`; `IMAGE_PUBLIC_BASE_URL` menunjuk CloudFront). Upload: `POST /internal/v1/admin/events/{eventId}/images?kind=image\|banner\|thumbnail` (admin, multipart `file`, JPEG/PNG/WebP ≤ 5 MB) |
| `TICKET_LINK_BASE_URL` | notification | halaman e-tiket; email memuat `{base}/{bookingId}?t={token}` (kosong = tanpa tautan) |
| `MIDTRANS_SERVER_KEY`, `MIDTRANS_SNAP_BASE_URL`, `PAYMENT_SUCCESS_RETURN_URL`, `PAYMENT_FAILED_RETURN_URL` | payment | Snap Sandbox/produksi dan halaman FE tujuan |
| `SERVICE_TOKEN_PRIVATE_KEY_PATH`, `PAYMENT_TOKEN_PUBLIC_KEY_PATH` | booking | panggilan Booking → Payment (expiry, batal) |

Migrasi Booking 0005 menghapus kolom email plaintext dan menolak berjalan bila masih ada baris plaintext (data dari sebelum PDP-1 harus dienkripsi dulu dengan versi sebelum 0005; perintah `pii-backfill` sudah dihapus).

Detail dan alasan: ADR-025/026 (`docs/analisa/05-ADR-RBT.md`). Hasil audit: `docs/engineering/AUDIT-FINAL.md`.

## Struktur

```text
services/<svc>/        satu proyek Go per service: cmd (Cobra), internal/<domain>/{port,domain,application,infrastructure},
                       internal/{config,delivery,shared}, _mock, deployments/{Dockerfile,migrations}, assets, docs
pkg/                   utilitas lintas service tanpa logika bisnis: apperror, httpx, dotenv, clock, logger, postgres,
                       valkey, rabbitmq (confirm, retry berjenjang, DLQ), migrate (advisory lock), eventstore, token (EdDSA), crypto
deployments/           compose (init schema/role), terraform, observability
docs/                  PRD, SRS, HLD, SDD, ADR, Test Plan, Handbook, kontrak OpenAPI dan event, evidence, ai-log
.kiro/steering, specs/ steering dan spec Kiro
tests/                 test lintas service: e2e (Go, `-tags e2e`), BDD, k6, contract
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
| `package` | docker build per service dan per arsitektur, native tanpa emulasi: **linux/amd64** (runner `ubuntu-latest`, VPS dev) dan **linux/arm64** (runner ARM GitHub; ganti lewat variable `ARM_RUNNER`; AWS Graviton) |
| `scan-package` | Trivy scan image + **SBOM CycloneDX** (artifact `sbom-<service>`), lalu `scan-package-verify` (gagal bila severity `IMAGE_FAIL_SEVERITY`, default CRITICAL) |
| `publish` | push image per arsitektur ke GHCR lalu gabung jadi manifest multi-arch (`<sha>` dan `main`), hanya dari `main`; digest dicatat di ringkasan run |
| `deployment-dev` | **manual** (Run workflow, `deploy_env=dev`): SSH ke VPS dev, kirim `deployments/dev`, lalu `deploy.sh` (pull image, migrasi, `up -d`, smoke test). Dilewati dengan peringatan bila environment `dev` belum lengkap. Lihat `deployments/dev/README.md` |
| `deployment-prod` | **manual**; masih placeholder (menunggu Terraform/AWS) |

`ci-ok` adalah satu-satunya check yang perlu diwajibkan di ruleset `main`; ia lulus bila semua stage lulus atau dilewati.

Integration test adapter (build tag `integration`) berjalan di `case-coverage`. Test end-to-end lintas service dan BDD (godog) ditambahkan saat alur pertama Queue → Booking → Payment siap (Handbook §2).

Coverage dihitung atas kode logika. `cmd/`, `internal/delivery/`, `internal/config/`, dan `_mock/` dikecualikan karena hanya bootstrap dan wiring (dibuktikan lewat run dan health check). Lokal: `make cover`.

### Yang perlu diisi (placeholder)

| Nilai | Jenis | Dipakai oleh |
|---|---|---|
| `SONAR_ENABLED=true`, `SONAR_ORGANIZATION`, `SONAR_PROJECT_KEY` | repo variables | `code-rank` (dilewati bila `SONAR_ENABLED` bukan `true`) |
| `SONAR_TOKEN` | repo secret | `code-rank` |
| `DEV_HOST`, `DEV_USER` | variables environment `dev` | `deployment-dev` |
| `DEV_SSH_KEY`, `DEV_SSH_KNOWN_HOSTS` | secrets environment `dev` | `deployment-dev` |
| `AWS_REGION`, `AWS_ROLE_ARN_PROD` | repo variables (OIDC) | `deployment-prod` |
| Environment `dev` dan `prod` (prod: Required reviewers) | Settings > Environments | `deployment-*` |
| Perintah migrasi dan deploy | langkah bertanda `GANTI` di `ci.yml` | `deployment-*` |

`deployment-dev` melakukan deploy nyata ke VPS begitu environment `dev` terisi. `deployment-prod` belum melakukan deploy nyata: langkahnya hanya mencetak peringatan `PLACEHOLDER` sampai Terraform dan role AWS siap.

## Repo terkait

- `tiket-sampai-nonton/rusuh-booking-web` — Next.js (termasuk payment page `/pay/{bookingId}`)
- `tiket-sampai-nonton/rusuh-booking-mobile` — React Native
