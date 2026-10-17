# Engineering Handbook — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | EHB-RBT |
| **Version** | 1.1 |
| **Date** | 2026-10-17 |
| **Owner** | BE-1 (+ semua owner) |
| **Source** | PRD v4.6 §18.4, §18.5, §21.3, Lampiran G, H, K; standar proyek organisasi (ADR-016) |

> Dokumen kerja tim: repository, pipeline, setup lokal, praktik engineering, dan aturan delivery dengan AI. Ini bukan dokumen standar ISO; dipisah agar PRD, SRS, HLD, dan SDD tetap fokus. Rujukan § atau Lampiran di kutipan diberi penanda `[v4.6 → dokumen §x]`.

## Change History

| Version | Date | Author | Summary of change |
|---------|------|--------|-------------------|
| 1.0 | 2026-10-17 | Tim PT. Tiket Sampai Nonton Sentosa | Dipecah dari PRD v4.6 |
| 1.1 | 2026-10-17 | Tim PT. Tiket Sampai Nonton Sentosa | Ditambah standar struktur proyek (§0); path disesuaikan; deviasi CI dicatat |

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
| Wiring | `internal/delivery/server.go` (dan `worker.go` untuk mode worker); konfigurasi di `internal/config/{config.go,load.go}` |
| Shared | `internal/shared/entity` (ValidationError) per service; `pkg/` di root untuk utilitas lintas service |
| Mock | `_mock/<domain>/port/{inbound,outbound}` dan `_mock/pkg/`, dihasilkan `make mock` (mockgen); tidak diedit manual |
| Lainnya | `assets/` (config JSON statis, template email), `deployments/` (Dockerfile, migrations), `docs/` |

Checklist ini juga ditulis di `.kiro/steering/structure.md` dan diperiksa saat code review.

## 1. Repository Management

*Sumber: PRD v4.6 §21.3*

**GitHub, repository public di akun pribadi.**

| Aspek | Keputusan |
|---|---|
| Struktur | `services/{queue,booking,payment,notification}` (struktur standar per service), `pkg/`, `web/`, `mobile/`, `deployments/`, `docs/` (termasuk `docs/contracts/`), `tests/`, `.kiro/`, `specs/` |
| Branching | Trunk-based; `feat/<REQ-ID>-<slug>` |
| Ruleset `main` | PR wajib, ≥1 approval (≥2 untuk `services/booking`, `services/payment`, `docs/contracts/`), CODEOWNERS, CI hijau, linear history |
| CODEOWNERS | `services/queue`, `services/notification`, `pkg/`, `deployments/` → BE-1; `services/booking`, `services/payment` → BE-2; `docs/contracts/` → BE-1 + BE-2 + satu FE; `web/` → WEB-1/WEB-2; `web/app/pay/` → WEB-2 + BE-2; `mobile/` → MOB-1 |
| Commit | Conventional Commits + Requirement ID |
| Security | Secret scanning + push protection, Dependabot, CodeQL |
| Tidak di-commit | PDF requirement, notulen grooming, `.env`, Terraform state |
| Wajib di-commit | `.kiro/steering`, `specs/`, `docs/ai-log/` (aturan assessment) |

---


## 2. Pipelines (GitHub Actions)

> **Deviasi dari standar:** standar proyek memakai `.gitlab-ci.yml`. Repository RBT wajib GitHub public (aturan assessment), sehingga pipeline memakai GitHub Actions dengan tahapan yang sama. Bila repository dipindah ke GitLab, setiap job di bawah dipetakan satu-satu ke stage `.gitlab-ci.yml` (ADR-016).
>
> **Implementasi di `rusuh-booking-be`:** satu pipeline `.github/workflows/ci.yml` (matrix per modul/service, hanya modul yang berubah) dengan stage berurutan code-quality → verify-code-quality → case-coverage → code-rank → build → package → scan-package → publish → deployment-dev → deployment-prod. Ambang coverage logika 80%; `code-rank` memakai SonarCloud; `deployment-*` dijalankan manual dan masih berupa placeholder sampai Terraform dan role AWS siap. Satu check `ci-ok` dipakai sebagai required check di ruleset `main`. Daftar rinci di README repo BE bagian "CI/CD".

*Sumber: PRD v4.6 §18.4*

- Per service (path filter `services/<name>/**`): lint → unit + race + coverage → integration (Compose) → godog → gosec/govulncheck/CodeQL → build image arm64 → SBOM + Trivy → push digest ECR.
- Contract: validasi OpenAPI + JSON Schema event; contract test producer/consumer.
- Architecture: depguard memastikan tidak ada import lintas service.
- Infra: fmt → validate → tflint/checkov → plan (komentar PR) → approval → apply → smoke.
- Web/mobile: typecheck → lint → unit → build → Katalon/Appium smoke.


## 3. Local Setup

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
    command: ["go", "run", "./services/queue/cmd"]
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
    command: ["go", "run", "./services/booking/cmd", "--mode=api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      rabbitmq: { condition: service_healthy }

  booking-worker:
    <<: *go
    environment: *booking-env
    command: ["go", "run", "./services/booking/cmd", "--mode=worker"]
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
    command: ["go", "run", "./services/payment/cmd", "--mode=api"]
    depends_on:
      migrate: { condition: service_completed_successfully }
      rabbitmq: { condition: service_healthy }

  payment-worker:
    <<: *go
    environment: *payment-env
    command: ["go", "run", "./services/payment/cmd", "--mode=worker"]
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
    command: ["go", "run", "./services/notification/cmd"]
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
