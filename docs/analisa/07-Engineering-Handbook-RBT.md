# Engineering Handbook — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | EHB-RBT |
| **Version** | 1.3 |
| **Date** | 2026-10-19 |
| **Owner** | BE-1 (+ semua owner) |
| **Source** | PRD v4.6 §18.4, §18.5, §21.3, Lampiran G, H, K; standar proyek organisasi (ADR-016) |

> Dokumen kerja tim: repository, pipeline, setup lokal, praktik engineering, dan aturan delivery dengan AI. Ini bukan dokumen standar ISO; dipisah agar PRD, SRS, HLD, dan SDD tetap fokus. Rujukan § atau Lampiran di kutipan diberi penanda `[v4.6 → dokumen §x]`.

## Change History

| Version | Date | Author | Summary of change |
|---------|------|--------|-------------------|
| 1.0 | 2026-10-17 | Tim PT. Tiket Sampai Nonton Sentosa | Dipecah dari PRD v4.6 |
| 1.1 | 2026-10-17 | Tim PT. Tiket Sampai Nonton Sentosa | Ditambah standar struktur proyek (§0); path disesuaikan; deviasi CI dicatat |
| 1.2 | 2026-10-18 | Tim PT. Tiket Sampai Nonton Sentosa | Tiga repository di organisasi; ruleset yang berlaku; pipeline bertahap dengan image arm64 dan SBOM; SonarCloud menggantikan SonarCloud |
| 1.3 | 2026-10-19 | Tim PT. Tiket Sampai Nonton Sentosa | Keputusan bersama BE-2: verb Cobra, Go 1.26, `go tool mockgen`, testify, migrasi tertanam (§0, §3.0); nama env D-24 menggantikan kutipan v4.6 (§3.0); alert email dan runbook (§6); penjelasan arsitektur untuk junior (§7) |

---

## 0. Standar Struktur Proyek

Semua service **wajib** mengikuti standar proyek organisasi. Struktur lengkap, pemetaan per service, dan deviasi ada di SDD-RBT-001 §4.1; aturan dependency di §4.3; prinsip desain kode di §4.5.1; keputusan di ADR-016.

| Aturan | Ketentuan |
|---|---|
| Lokasi domain | `services/<svc>/internal/<domain>/` (satu hexagon per service) |
| Port | `port/inbound` (usecase, event handler) dan `port/outbound` (repository, client, gateway) |
| Penamaan interface | Prefix `I`: `IBookingUsecase`, `IPaymentGateway`, `IQueueStore` |
| Domain | `domain/entity` (objek bisnis, konstanta, error code), `domain/valueobject` (immutable), `domain/dto` (request/response terkonsolidasi), `domain/mapper` (transformasi murni) |
| Usecase | `application/usecase/`, satu usecase per file |
| Adapter | `infrastructure/<teknologi>/` (`http`, `postgres`, `valkey`, `rabbitmq`, `midtrans`, `smtp`, `fcm`, client internal) |
| Entry point | `cmd/` (package main, Cobra): `cmd/main.go` + satu file per verb — `api`, `worker`, `migrate up\|down\|status`, `healthcheck`. Satu image per service, satu verb per container |
| Wiring | `internal/delivery/server.go` (HTTP), `worker.go` (job berkala/consumer), `clients.go` (membuka PostgreSQL/Valkey/RabbitMQ sesuai kebutuhan verb); konfigurasi di `internal/config/config.go` (struct + `Validate`) dan `load.go` (membaca env) |
| Shared | `internal/shared/entity` (ValidationError) per service; `pkg/` di root untuk utilitas lintas service |
| Mock | `_mock/<domain>/port/{inbound,outbound}` dan `_mock/pkg/`, dihasilkan `make mock` (`go tool mockgen`, versi dikunci di `go.mod` lewat directive `tool`); tidak diedit manual; CI menolak bila hasil generate berbeda |
| Test | `testify` (`assert`/`require`) + `gomock`. Setiap fungsi publik punya test yang bisa diulang (regression). Integration test memakai build tag `integration` dan env `TEST_*` |
| Migrasi | `deployments/migrations/NNNN_nama.up.sql` dan `.down.sql`, di-embed ke binary (`go:embed`) dan dijalankan `<svc> migrate up`. Advisory lock per schema; checksum dicatat di `<schema>.schema_migrations`, file yang sudah dijalankan tidak boleh diubah |
| Lainnya | `assets/` (config JSON statis, template email), `deployments/` (Dockerfile, migrations), `docs/` |

Checklist ini juga ditulis di `.kiro/steering/structure.md` dan diperiksa saat code review.

Repository di **GitHub** (organisasi `tiket-sampai-nonton`, D-26); tidak ada binary hasil build yang di-commit (D-18).

## 1. Repository Management

*Sumber: PRD v4.6 §21.3*

**GitHub, tiga repository public di organisasi `tiket-sampai-nonton`: `rusuh-booking-be`, `rusuh-booking-web`, `rusuh-booking-mobile` (ADR-017).**

| Aspek | Keputusan |
|---|---|
| Struktur | `rusuh-booking-be`: `services/{queue,booking,payment,notification}` (struktur standar per service), `pkg/`, `deployments/`, `docs/` (termasuk `docs/contracts/`), `tests/`, `.kiro/`, `specs/`. Web dan mobile di `rusuh-booking-web` dan `rusuh-booking-mobile` (ADR-017) |
| Branching | Trunk-based; `feat/<REQ-ID>-<slug>` |
| Ruleset `main` | PR wajib dengan ≥1 approval; force push dan penghapusan branch diblokir; bypass hanya admin organisasi. Check `ci-ok` dapat diwajibkan setelah pipeline stabil |
| CODEOWNERS | Per repository. Backend: `services/queue`, `services/notification`, `deployments/` → BE-1; `services/booking`, `services/payment` → BE-2; `pkg/` → BE-1 + BE-2; `docs/contracts/` → BE-1 + BE-2 + satu FE. Web → WEB-1/WEB-2, halaman `/pay` → WEB-2 + BE-2. Mobile → MOB-1 |
| Commit | Conventional Commits + Requirement ID |
| Security | Secret scanning + push protection, Dependabot, SonarCloud, gosec, govulncheck, gitleaks, Trivy |
| Tidak di-commit | PDF requirement, notulen grooming, `.env`, Terraform state |
| Wajib di-commit | `.kiro/steering`, `specs/`, `docs/ai-log/` (aturan assessment) |

---


## 2. Pipelines (GitHub Actions)

> **Deviasi dari standar:** standar proyek memakai `.gitlab-ci.yml`. Repository RBT wajib GitHub public (aturan assessment), sehingga pipeline memakai GitHub Actions dengan tahapan yang sama. Bila repository dipindah ke GitLab, setiap job di bawah dipetakan satu-satu ke stage `.gitlab-ci.yml` (ADR-016).
>
> Placeholder (SonarCloud, AWS) dan job `ci-ok` dijelaskan di README `rusuh-booking-be` bagian "CI/CD".

*Sumber: PRD v4.6 §18.4*

- Backend (`rusuh-booking-be`, satu `ci.yml`, matrix per service yang berubah): code-quality (golangci-lint + depguard, go vet, gosec, govulncheck, hadolint, gitleaks) → verify-code-quality → case-coverage (unit + race, coverage logika ≥ 80%) → code-rank (SonarCloud) → build (+ isolation build) → package (image arm64) → scan-package (Trivy + SBOM CycloneDX) → publish (push, digest dicatat) → deployment-dev → deployment-prod (manual).
- Integration test (Compose) dan BDD (godog): dijalankan lokal terhadap Docker Compose; masuk pipeline sebagai stage sesudah case-coverage saat fitur pertama dengan PostgreSQL, Valkey, dan RabbitMQ siap.
- Contract: validasi OpenAPI + JSON Schema event dan contract test producer/consumer; masuk pipeline bersama integration test setelah kontrak final.
- Architecture: depguard memastikan tidak ada import lintas service.
- Infra (saat `deployments/terraform` dibuat): fmt → validate → tflint/checkov → plan (komentar PR) → approval → apply → smoke.
- Web dan mobile: pipeline di repository masing-masing (typecheck → lint → unit → build); Katalon/Appium smoke sebelum rilis kandidat.


## 3. Local Setup

### 3.0 Boilerplate v1.5 (berlaku)

Bagian ini **menggantikan** nama env, port, dan perintah pada kutipan v4.6 di §3 dan §3.1 bila berbeda. Kutipan tetap dipertahankan untuk jejak.

| Kebutuhan | Perintah |
|---|---|
| Prasyarat | Go 1.26, Docker + Compose v2, `make`, `openssl` |
| Kunci token lokal (sekali) | `make keys` → `deployments/compose/keys/` (di-gitignore) |
| Semua jalan di Docker | `make up` (infra → `*-migrate` → api + worker); `make ps`, `make logs`, `make down` |
| Infra saja, service dari IDE | `make infra`, lalu `cp services/<svc>/.env.example services/<svc>/.env`, `make migrate-<svc>`, `make run-<svc>` |
| Test | `make test` (unit), `make test-integration` (butuh `make infra`), `make cover` (sama dengan stage CI) |
| Setelah menambah dependency | `make tidy`, `make mock`, commit `go.mod`, `go.sum`, `_mock/` |
| Verb per service | `go run ./cmd api`, `go run ./cmd worker`, `go run ./cmd migrate up\|down [--steps N]\|status`, `go run ./cmd healthcheck` |

| Service | Port | Schema / role DB | Job worker (didaftarkan di `jobs()` saat fitur dibangun) |
|---|---|---|---|
| queue | 8081 | `queue` / `svc_queue` | promotor admission, sweeper slot |
| booking | 8082 | `booking` / `svc_booking` | sweeper hold kedaluwarsa, outbox relay |
| payment | 8083 | `payment` / `svc_payment` | sweeper attempt, outbox relay, rekonsiliasi |
| notification | 8084 | `notification` / `svc_notification` | consumer email/push |
| user | 8085 | `users` / `svc_user` | — |

| Grup env (D-24) | Variabel |
|---|---|
| Aplikasi | `APP_ENV`, `APP_NAME`, `APP_PORT`, `LOG_LEVEL`, `LOG_FORMAT`, `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL` |
| PostgreSQL | `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE`, `POSTGRES_SSLMODE`, `POSTGRES_SCHEMA`, `POSTGRES_MAX_CONNS` |
| Valkey | `VALKEY_SERVER` (JSON `[{"host","port"}]`), `VALKEY_USERNAME`, `VALKEY_PASSWORD`, `VALKEY_TLS`, `VALKEY_DB`, `VALKEY_PREFIX_ENV` |
| RabbitMQ | `RABBITMQ_URL`, `RABBITMQ_EXCHANGE`, `RABBITMQ_EXCHANGE_TYPE`, `RABBITMQ_WORKERS`, `RABBITMQ_PREFETCH`, `RABBITMQ_MAX_RETRIES`, `RABBITMQ_RETRY_DELAYS`, `RABBITMQ_CONFIRM_TIMEOUT` |
| User Service | `TOKEN_*`, `OTP_*`, `PII_ENC_KEY`, `PII_HASH_KEY`, `OTP_HASH_KEY`, `SMTP_*` (nilai default di SDD §6.1) |

Health: satu endpoint `GET /health` (200 `UP` / 503 `DOWN`, D-23); endpoint *live* dan *ready* terpisah pada kutipan v4.6 tidak dipakai. Mail lokal terlihat di Mailpit `http://localhost:8025`; RabbitMQ management di `http://localhost:15672`.

*Sumber: PRD v4.6 §18.5*

```bash
cp .env.example .env            # isi MIDTRANS_SERVER_KEY dan CLIENT_KEY sandbox
docker compose up -d --build
docker compose run --rm seed
cloudflared tunnel --url http://localhost:8080   # agar webhook Midtrans sampai ke lokal
docker compose run --rm smoke
```

Detail Compose di Lampiran H [v4.6 → Handbook §3.1].

---


### 3.1 Docker Compose Clean-Room

*Sumber: PRD v4.6 Lampiran H*

Compose menjalankan seluruh sistem lokal tanpa AWS dan private registry. Service Go dijalankan dari source (`go run`) agar assessor dapat membaca dan memodifikasi kode langsung.

Mode payment:
- `PAYMENT_PROVIDER=midtrans` (**default**): butuh kredensial Midtrans Sandbox di `.env` dan tunnel publik untuk webhook.
- `PAYMENT_PROVIDER=simulator` + `APP_ENV=local-offline`: tanpa kredensial/internet; hasil diberi label SIMULATED.

```yaml
name: rusuh-booking-tiket

x-go: &go
  image: golang:1.26-alpine          # pin ke rilis stabil terbaru setelah verifikasi
  working_dir: /src
  volumes: [ "./:/src", "go_mod:/go/pkg/mod", "go_build:/root/.cache/go-build" ]
  networks: [backend]
  restart: unless-stopped

x-common-env: &common-env
  APP_ENV: ${APP_ENV:-local}
  LOG_FORMAT: json
  OTEL_EXPORTER_OTLP_ENDPOINT: http://jaeger:4318
  RABBITMQ_URL: amqp://${RABBITMQ_USER:-rusuh}:${RABBITMQ_PASSWORD:-local_only}@rabbitmq:5672/rusuh
  NIK_HMAC_KEY: ${NIK_HMAC_KEY:-local-only-hmac-key}
  TOKEN_PUBLIC_KEYS_DIR: /src/deployments/compose/keys/public
  INTERNAL_SERVICE_TOKEN_KEY: ${INTERNAL_SERVICE_TOKEN_KEY:-local-only-service-key}

services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: rusuh
      POSTGRES_USER: rusuh_admin
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-local_only}
    volumes:
      - pg_data:/var/lib/postgresql/data
      - ./deployments/compose/postgres-init.sql:/docker-entrypoint-initdb.d/000_roles_and_schemas.sql:ro
    ports: ["127.0.0.1:5432:5432"]
    healthcheck: { test: ["CMD-SHELL", "pg_isready -U rusuh_admin -d rusuh"], interval: 5s, retries: 20 }
    networks: [backend]

  valkey:
    image: valkey/valkey:8.1-alpine
    command: ["valkey-server", "--appendonly", "yes", "--appendfsync", "everysec", "--maxmemory-policy", "noeviction"]
    volumes: [ "valkey_data:/data" ]
    ports: ["127.0.0.1:6379:6379"]
    healthcheck: { test: ["CMD", "valkey-cli", "ping"], interval: 5s, retries: 20 }
    networks: [backend]

  rabbitmq:
    image: rabbitmq:3.13-management-alpine
    environment:
      RABBITMQ_DEFAULT_USER: ${RABBITMQ_USER:-rusuh}
      RABBITMQ_DEFAULT_PASS: ${RABBITMQ_PASSWORD:-local_only}
      RABBITMQ_DEFAULT_VHOST: rusuh
    ports: ["127.0.0.1:15672:15672"]
    volumes: [ "rabbitmq_data:/var/lib/rabbitmq" ]
    healthcheck: { test: ["CMD", "rabbitmq-diagnostics", "-q", "ping"], interval: 10s, retries: 20 }
    networks: [backend]

  mailpit:
    image: axllent/mailpit:v1.21
    ports: ["127.0.0.1:8025:8025"]
    networks: [backend]

  jaeger:
    image: jaegertracing/all-in-one:1.62
    environment: { COLLECTOR_OTLP_ENABLED: "true" }
    ports: ["127.0.0.1:16686:16686"]
    networks: [backend]

  migrate:
    <<: *go
    restart: "no"
    environment:
      ADMIN_DATABASE_URL: postgres://rusuh_admin:${POSTGRES_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable
    command: ["go", "run", "./tools/migrate", "--all"]   # menjalankan deployments/migrations/ tiap service dengan role miliknya
    depends_on: { postgres: { condition: service_healthy } }

  seed:
    <<: *go
    restart: "no"
    profiles: ["tools"]
    environment:
      ADMIN_DATABASE_URL: postgres://rusuh_admin:${POSTGRES_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable
    command: ["go", "run", "./tools/seed", "--scenario", "${SEED_SCENARIO:-demo}"]   # demo: event kecil (10 tiket) agar habis/antre terlihat

  queue:
    <<: *go
    environment:
      <<: *common-env
      HTTP_ADDR: ":8081"
      DATABASE_URL: postgres://svc_queue:${SVC_QUEUE_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable&search_path=queue
      VALKEY_ADDR: valkey:6379
      BOOKING_INTERNAL_URL: http://booking:8082
      TOKEN_SIGNING_KEY_FILE: /src/deployments/compose/keys/queue.ed25519
    command: ["go", "run", "./services/queue/cmd", "api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      valkey: { condition: service_healthy }
      rabbitmq: { condition: service_healthy }

  booking:
    <<: *go
    environment: &booking-env
      <<: *common-env
      HTTP_ADDR: ":8082"
      DATABASE_URL: postgres://svc_booking:${SVC_BOOKING_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable&search_path=booking
      PAYMENT_INTERNAL_URL: http://payment:8083
      NIK_ENCRYPTION_KEY: ${NIK_ENCRYPTION_KEY:-local-only-aes-key-32-bytes-long!}
      TOKEN_SIGNING_KEY_FILE: /src/deployments/compose/keys/booking.ed25519
    command: ["go", "run", "./services/booking/cmd", "api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      rabbitmq: { condition: service_healthy }

  booking-worker:
    <<: *go
    environment: *booking-env
    command: ["go", "run", "./services/booking/cmd", "worker"]
    depends_on: { booking: { condition: service_started } }

  payment:
    <<: *go
    environment: &payment-env
      <<: *common-env
      HTTP_ADDR: ":8083"
      DATABASE_URL: postgres://svc_payment:${SVC_PAYMENT_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable&search_path=payment
      BOOKING_INTERNAL_URL: http://booking:8082
      PAYMENT_PROVIDER: ${PAYMENT_PROVIDER:-midtrans}
      MIDTRANS_IS_PRODUCTION: "false"
      MIDTRANS_SERVER_KEY: ${MIDTRANS_SERVER_KEY:-}
      MIDTRANS_CLIENT_KEY: ${MIDTRANS_CLIENT_KEY:-}
      PUBLIC_BASE_URL: ${PUBLIC_BASE_URL:-http://localhost:8080}
      TOKEN_SIGNING_KEY_FILE: /src/deployments/compose/keys/payment.ed25519
    command: ["go", "run", "./services/payment/cmd", "api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      rabbitmq: { condition: service_healthy }

  payment-worker:
    <<: *go
    environment: *payment-env
    command: ["go", "run", "./services/payment/cmd", "worker"]
    depends_on: { payment: { condition: service_started } }

  notification:
    <<: *go
    environment:
      <<: *common-env
      HTTP_ADDR: ":8084"
      DATABASE_URL: postgres://svc_notification:${SVC_NOTIFICATION_PASSWORD:-local_only}@postgres:5432/rusuh?sslmode=disable&search_path=notification
      SMTP_HOST: mailpit
      SMTP_PORT: "1025"
      MAIL_FROM: "Rusuh Tiket <no-reply@rusuh.local>"
    command: ["go", "run", "./services/notification/cmd", "api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      rabbitmq: { condition: service_healthy }

  web:
    image: node:24-alpine
    working_dir: /src/web
    volumes: [ "./:/src", "web_node_modules:/src/web/node_modules" ]
    environment:
      NEXT_PUBLIC_API_BASE_PATH: /api/v1
    command: ["sh", "-c", "npm ci && npm run dev -- -H 0.0.0.0 -p 3000"]
    networks: [backend]

  edge:
    image: nginx:1.27-alpine
    volumes: [ "./deployments/edge/nginx.conf:/etc/nginx/conf.d/default.conf:ro" ]
    ports: ["0.0.0.0:8080:8080"]          # 0.0.0.0 agar HP di jaringan yang sama dapat mengakses
    depends_on: [queue, booking, payment, web]
    networks: [backend]

  prometheus:
    image: prom/prometheus:v2.55.0
    volumes: [ "./deployments/observability/prometheus.yml:/etc/prometheus/prometheus.yml:ro" ]
    ports: ["127.0.0.1:9090:9090"]
    networks: [backend]

  grafana:
    image: grafana/grafana:11.3.0
    environment: { GF_SECURITY_ADMIN_PASSWORD: "${GRAFANA_PASSWORD:-local_only}" }
    volumes: [ "./deployments/observability/grafana:/etc/grafana/provisioning:ro" ]
    ports: ["127.0.0.1:3001:3000"]
    networks: [backend]

networks: { backend: { driver: bridge } }
volumes: { pg_data: {}, valkey_data: {}, rabbitmq_data: {}, go_mod: {}, go_build: {}, web_node_modules: {} }
```

`deployments/edge/nginx.conf` (meniru path rule ALB dan cache CloudFront):

```nginx
proxy_cache_path /tmp/nginx_cache levels=1 keys_zone=status:1m max_size=10m inactive=10s;

server {
  listen 8080;
  client_max_body_size 1m;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $remote_addr;
  proxy_set_header X-Origin-Verify "local-only";

  location ~ ^/api/v1/queue-status/ {
    proxy_cache status; proxy_cache_valid 200 1s; proxy_cache_lock on;
    proxy_ignore_headers Set-Cookie; proxy_pass http://queue:8081;
  }
  location ~ ^/api/v1/(events/[^/]+/queue-entries|queue-entries) { proxy_pass http://queue:8081; }
  location ~ ^/api/v1/(payments|payment-page-sessions|webhooks|local-simulator) { proxy_pass http://payment:8083; }
  location ~ ^/api/v1/(events|booking-sessions|bookings) { proxy_pass http://booking:8082; }
  location / {
    proxy_pass http://web:3000;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
  }
}
```

##### Clean-room commands

```bash
cp .env.example .env                       # isi kredensial Midtrans Sandbox; ganti nilai local_only
make keys                                  # membuat pasangan kunci Ed25519 lokal di deployments/compose/keys
docker compose up -d --build
docker compose run --rm seed               # SEED_SCENARIO=demo|load
cloudflared tunnel --url http://localhost:8080   # salin URL ke Midtrans Sandbox > Notification URL
# Web:       http://localhost:8080
# Mailpit:   http://127.0.0.1:8025
# RabbitMQ:  http://127.0.0.1:15672
# Grafana:   http://127.0.0.1:3001
# Jaeger:    http://127.0.0.1:16686
# Mobile:    arahkan base URL ke http://<IP-LAN>:8080 atau URL tunnel
docker compose run --rm --entrypoint "go test ./tests/smoke/..." queue
docker compose down        # reset total: docker compose down -v
```

Skenario seed `demo`: satu event dengan 10 tiket (VIP 4, Festival 6) dan kapasitas slot aktif 3, sehingga antrean, zona peluang, sold-out closure, dan expiry dapat didemokan langsung dengan beberapa browser/HP.

---


## 4. Git, CI/CD, Security, Logging, dan Incident

*Sumber: PRD v4.6 Lampiran G*

### Git dan Release

- Trunk-based; branch `feat/<REQ-ID>-<slug>`, `fix/…`, `docs/…`, `infra/…`, `hotfix/<incident-id>`.
- PR wajib menyebut Requirement ID, ADR, perubahan kontrak/migration, test, risiko rollback, evidence.
- Conventional Commits: `feat(queue): …`, `fix(booking): …`, `test(payment): …`.
- Tag `vMAJOR.MINOR.PATCH`; image per service dibangun sekali dan di-deploy berdasarkan digest.
- Migration expand → migrate → contract; rollback aplikasi tidak mengasumsikan schema dapat diturunkan.

### CI Quality Gates

1. `gofmt`, `go vet`, `golangci-lint` (termasuk depguard).
2. Unit test + coverage ≥ 80% per service; `go test -race`.
3. Integration test per service dengan dependency nyata (Compose).
4. Contract test: OpenAPI, JSON Schema event, producer/consumer.
5. godog acceptance.
6. SAST, SCA/CVE, secret scan, image scan; DAST di staging.
7. SBOM, image digest, release notes.
8. Release gate: race stok/kuota/NIK pemegang, callback duplikat/telat, expiry dengan resolve, sold-out closure, broker outage, DLQ redrive, breaker, backup/restore, clean-room, Midtrans Sandbox aktual.
9. `NOT_RUN` bukan `PASS`; `VERIFIED` wajib punya command, timestamp, commit SHA, laporan test, reviewer.

### Security (OWASP)

- Query parameterized; allowlist sort/filter.
- IDOR: ownership diperiksa per booking/sesi/tiket berdasarkan `visitorId` dari token; UUID bukan kontrol akses.
- Token bertanda tangan EdDSA dengan `aud`, `exp`, `jti`; klaim sekali pakai atomik.
- Tidak menerima URL callback arbitrer; egress allowlist ke Midtrans, SES, ECR, SSM.
- AES-256-GCM untuk NIK pemegang dengan key terpisah dan versioned; HMAC-SHA256 dengan key terpisah untuk lookup; rotasi HMAC dengan dual lookup.
- NIK, email, dan token tidak pernah ada di log, URL, label metrik, DLQ, prompt AI, screenshot, atau fixture nyata.
- Webhook: signature constant-time, order, amount, environment, transisi legal.
- Audit append-only untuk admission, hold, settlement, REVIEW, refund, redrive.

### Logging, Metrics, Tracing

Log JSON (`slog`) memuat `timestamp`, `level`, `service`, `env`, `request_id`, `trace_id`, `operation`, `outcome`, `duration_ms`, `error_code`. Trace mengalir lintas service lewat header W3C `traceparent` dan atribut `correlationId` pada event.

Metrik minimum per service: lihat §16.2 [v4.6 → SDD §7]. Alert: pertumbuhan antrean abnormal, umur Status Document > 5 detik, admission berhenti saat masih ada supply, slot aktif di Valkey tidak sesuai Booking, outbox lag, DLQ > 0, breaker open berkepanjangan, VERIFYING menumpuk, REVIEW > 0, pelanggaran invariant, backup gagal.

### Template Post-Mortem

```markdown
# Incident: <judul>
- Incident ID / Severity:
- Waktu mulai, terdeteksi, pulih (UTC):
- Dampak customer (berapa yang terdampak, apakah ada yang di-PHP):
- Sumber deteksi:
- Timeline:
- Root cause:
- Faktor pendukung:
- Review integritas data (stok, alokasi, kuota, NIK pemegang, pembayaran, tiket):
- Dampak privasi:
- Tindakan pemulihan:
- Evidence:
- Tindakan korektif (owner, tenggat, verifikasi):
```

---


## 5. Aturan Delivery dengan AI/Kiro

*Sumber: PRD v4.6 Lampiran K*

AI adalah alat bantu implementasi, bukan otoritas yang menyatakan pekerjaan benar.

- **Aturan assessment:** steering, spec, dokumentasi, dan data penggunaan AI tidak boleh dihapus; semuanya di-commit (`.kiro/`, `specs/`, `docs/ai-log/`).
- Spec per service (`specs/<service>/{requirements,design,tasks}.md`); setiap task mencantumkan Requirement ID, file yang boleh diubah, acceptance test, command verifikasi, risiko, dan lokasi evidence.
- Bagian kritis (alokasi/hold/kuota/NIK pemegang, settlement/expiry/resolve, Lua antrean, token, kripto) wajib human review oleh BE yang bukan penulisnya.
- Kontrak (`docs/contracts/`) adalah sumber generasi tipe untuk web dan mobile; perubahan kontrak lewat PR yang di-review BE dan minimal satu FE.
- Jangan mengirim secret, NIK nyata, dokumen berlabel internal, atau notulen ke AI tool publik.
- Catat biaya AI, waktu generate/review/test/rework, kategori defect, dan hasil verifikasi.
- Bila deadline berisiko, eskalasi scope; jangan menghapus fungsi wajib atau menurunkan gate diam-diam.

---


## 6. Alert dan Runbook (D-41)

Alert Grafana dikirim ke contact point **email** tim `rbt-team-email` (alamat diisi saat setup). Telegram dan kanal lain dicatat sebagai pengembangan sesudah PoC. Setiap rule memuat tautan ke baris tabel ini.

| Alert | Pemicu | Langkah pertama | Eskalasi |
|---|---|---|---|
| `ServiceDown` | `GET /health` 503 > 2 menit | Lihat log service (`make logs` / CloudWatch); cek dependency yang `DOWN` di body respons | Owner service |
| `OutboxBacklog` | Baris outbox `pending` > 5 menit | Pastikan worker jalan dan RabbitMQ terhubung; relay idempoten, aman di-restart | BE-1 |
| `DLQNotEmpty` | `<queue>.dlq` > 0 | Baca header `x-rbt-last-error`; perbaiki penyebab; redrive dengan tool yang diaudit | Owner consumer |
| `BreakerOpen` | Circuit breaker OPEN > 1 menit (gateway, SMTP, Booking) | Cek service tujuan; breaker pulih sendiri lewat HALF_OPEN | Owner pemanggil |
| `ReconMismatch` | Job rekonsiliasi menemukan selisih stok/uang | **Jangan** ubah data manual; jalankan tool koreksi mode dry-run, minta review | Tech lead |
| `OTPAbuse` | 429 OTP per IP melonjak | Cek WAF rate rule; login opsional, penjualan guest tetap jalan | BE-1 |

## 7. Penjelasan Arsitektur untuk Junior (D-35)

Format: pertanyaan yang biasanya muncul → jawaban pendek → di mana kodenya. Isi mengikuti desain v4.6 + perubahan v1.5.

| Pertanyaan | Jawaban singkat | Lihat |
|---|---|---|
| Kenapa ada 5 service? | Tiap service punya data dan beban sendiri: Queue menahan lonjakan, Booking menjaga stok, Payment mengurus uang, Notification mengirim email/push, User mengurus login opsional. Satu service mati tidak menjatuhkan yang lain | HLD §4.2, ADR-001, ADR-020 |
| Bagaimana stok tidak oversell? | Stok hanya diubah di Booking, dalam satu transaksi PostgreSQL dengan kunci baris. Valkey tidak pernah menjadi sumber stok | SDD §4.5, ADR-008 |
| Apa itu event sourcing di sini? | Untuk booking dan payment, setiap perubahan disimpan sebagai event yang tidak boleh diubah (`booking_events`). Tabel `bookings` adalah ringkasan (read model) yang diperbarui di transaksi yang sama. Kalau ada sengketa, riwayat lengkap bisa diputar ulang | ADR-019, `pkg/eventstore` |
| Kenapa ada outbox? | Agar "simpan data" dan "kirim pesan ke RabbitMQ" tidak bisa setengah jadi. Pesan ditulis dulu ke tabel outbox di transaksi yang sama, lalu worker mengirimnya | SDD §4.5.4 |
| Pesan gagal diproses, lalu? | Dicoba ulang 5 detik, 30 detik, 2 menit (antrean retry). Setelah 3 kali masuk DLQ dan memicu alert. Consumer wajib idempoten | `pkg/rabbitmq/retry.go` |
| Apa itu circuit breaker? | "Sekring": kalau tujuan terus gagal, panggilan berhenti sebentar (OPEN) supaya tidak menumpuk, lalu dicoba sedikit (HALF_OPEN) | `pkg/resilience` |
| Login wajib? | Tidak. Guest tetap jalur utama. Login hanya memakai kode OTP di email dan mengisi data otomatis | SRS §3.2.27 |
| Di mana menaruh kode baru? | Aturan bisnis di `domain/`, alur di `application/usecase/`, akses DB/HTTP di `infrastructure/`. Domain tidak boleh import library luar (dicek depguard) | §0, SDD §4.3 |
| Bagaimana menjalankan satu service? | `make infra`, salin `.env.example`, `make migrate-<svc>`, `make run-<svc>` | §3.0 |
