# Implementation Plan: Platform

## Overview

Melengkapi `pkg/` dan artefak repo yang dibutuhkan semua service. Task 1–5 adalah **prasyarat** bagi
spec service (Booking/Queue/Payment/Notification/User memakai `pkg/nik`, middleware auth, `pkg/outbox`,
telemetry, dan kontrak event baru). Task 6–11 dapat berjalan paralel dengan implementasi service.

Bahasa: Go 1.26, module `pkg/`. Ikuti `.kiro/steering/{structure,code-principles,testing-review}.md`.
Setiap task selesai bila `make ci` hijau.

## Tasks

- [x] 0. Fondasi yang sudah ada (audit 2026-10-21)
  - `pkg/{apperror,clock,crypto,dotenv,eventstore,httpx(server,RequestID,Recover,strict JSON),logger,migrate,postgres,rabbitmq,resilience,token,valkey}` dengan test; evidence `docs/evidence/2026-10-19-boilerplate-v1.5.md`
  - CI `ci.yml` (build, lint+depguard, coverage ≥80%, isolation, scan), compose infra + 5 service, `make keys` untuk User, Queue, Payment, Booking
  - _Requirements: 2.7, 2.8, 4.5, 6.2, 7.1, 8.1_

- [x] 1. Implement `pkg/nik` validator struktur NIK
  - [x] 1.1 Buat `pkg/nik/nik.go` dengan `Validate(s, now)` dan error sentinel sesuai SDD-RBT §4.9
    - Daftar provinsi sebagai `map[int]bool`; cek kab/kec ≠ `00`; tanggal kalender (DD+40), tidak di masa depan; urut ≠ `0000`
    - _Requirements: 1.1, 1.2, 1.3, 1.4_
    - _Design: §1_
  - [x] 1.2 Test table-driven korpus SDD §4.9 (laki-laki, perempuan, 30 Feb, 0000, provinsi tak dikenal, kab 00, panjang, non-digit, abad sebelumnya)
    - **Property 1: NIK validator determinism**
    - _Requirements: 1.5_
  - [x] 1.3 Dokumentasikan pemakaian di usecase (bukan domain) di godoc paket
    - _Requirements: 1.1_

- [ ] 2. Implement middleware autentikasi & proteksi di `pkg/httpx`
  - [ ] 2.1 `VisitorAuth`: cookie `rb_visitor` → fallback `X-Visitor-Token`; verifikasi `pkg/token.Verifier`; context `Visitor{ID,BuyerNikHash,Email}`
    - _Requirements: 2.1_
    - _Design: §2_
  - [ ] 2.2 `UserAuth` (Bearer user access token) → context `User{AccountID,HasNIK,NIKHash}`; `401` + `WWW-Authenticate`
    - _Requirements: 2.2_
  - [ ] 2.3 `ServiceAuth(allowedIssuers)` untuk `/internal/v1/*`; `ServiceTokenSource` (TTL 5 m, re-sign < 30 s sisa) + helper klien internal yang menambah `Authorization` & `X-Request-ID`
    - _Requirements: 2.3, 2.4_
  - [ ] 2.4 `RateLimit` berbasis Valkey (`IRateStore`) dengan `Retry-After`; `ClientIP(trustProxy)`
    - _Requirements: 2.5, 2.6_
  - [ ] 2.5 Unit test: cookie vs header menghasilkan identitas sama; token kedaluwarsa; issuer tak dikenal; `aud` salah
    - **Property 4: Internal API closed**, **Property 5: Same auth for web and mobile**
    - _Requirements: 2.1, 2.3_
  - [x] 2.6 Format error standar: `pkg/apperror` mengirim hanya `{meta:{code, message}}` dengan `message = "KODE: pesan"` (atau pesan umum untuk 5xx); `requestId` di header `X-Request-ID`; `httpx.WriteError` menulis kode error, detail, error asli, dan payload request (diredaksi lewat 4.4) ke log. Satu perubahan di `pkg/` berlaku untuk lima service; beri tahu tim web/mobile. Selesai 2026-10-22 (format meta saja): `apperror.Meta` hanya `code`+`message` (+`requestId` pada respons sukses), `ClientMessage()` = `KODE: pesan`, error internal selalu `INTERNAL_ERROR: terjadi kesalahan internal`; `WriteError` melog `requestId/status/errorCode/details/error` (level Error untuk 5xx, Info untuk 4xx) dan mengirim `Retry-After` untuk 429 dari `Details[retryAfterSeconds|retryAfter]`; kontrak OpenAPI: `errorCode`/`details` ditandai `deprecated`; 2 tes DEV-2 (booking, user) kini membaca kode dari awalan message. Belum: log payload request yang diredaksi (perlu menyimpan body di `DecodeJSON`) -> sub-task 2.6a
    - _Requirements: 2.8, 2.9_
    - _Depends on: 4.4_
  - [ ] 2.6a Log payload request (diredaksi lewat `logger.Redact`, maks 4 KiB) pada `WriteError`: `RequestID()` menaruh pemegang di context, `DecodeJSON` menyalin bytes yang dibaca; hanya dipakai di jalur error
    - _Requirements: 2.8_
    - _Depends on: 2.6_
  - [ ] 2.7 **Temuan:** `httpx.CORS()` (DEV-2) dipasang tanpa syarat di `delivery/server.go` kelima service, jadi `Access-Control-Allow-Origin: *` aktif di semua lingkungan, padahal komentarnya "never enable in prod". Pasang hanya bila `APP_ENV=local`
    - _Requirements: 2.10_
  - Catatan: visitor auth sekarang ditulis ulang di tiap service (`security/visitor_auth.go`). Setelah 2.1 selesai, service bisa pindah ke versi `pkg/httpx`; tidak wajib sebelum demo
  - _Depends on: 6.1 (kunci Queue & service lokal untuk test integrasi)_

- [ ] 3. Implement `pkg/outbox` (Write + Relay)
  - Catatan: Booking sudah punya relay sendiri (`booking/infrastructure/rabbitmq` `NewOutboxRelay`). Cara termudah: pindahkan relay itu ke `pkg/outbox`, lalu pakai juga di Payment
  - [ ] 3.1 `Write(ctx, tx, Message)` menyisipkan ke `outbox_events` dengan tx pemanggil
    - _Requirements: 3.1_
    - _Design: §3_
  - [ ] 3.2 `Relay.RunOnce`: `FOR UPDATE SKIP LOCKED`, envelope (`messageId = outbox_events.id`), publish confirm, update `published_at` / `publish_attempts` + `last_error` + backoff `available_at`
    - _Requirements: 3.2, 3.3, 3.4_
  - [ ] 3.3 Metrik lag outbox (umur pending tertua) lewat `pkg/telemetry`
    - _Requirements: 3.6_
    - _Depends on: 4.1_
  - [ ] 3.4 Integration test (PostgreSQL + RabbitMQ): publish sukses; confirm gagal → pending + attempts++; broker down lalu pulih → lag 0; dua relay paralel tanpa publish ganda
    - **Property 2: No lost outbox event**, **Property 3: Outbox single publisher per cycle**
    - _Requirements: 3.4, 3.5_

- [ ] 4. Implement telemetry & redaksi log
  - [x] 4.1 Keputusan library: Prometheus client (`client_golang`) — dipilih karena paling sederhana dijelaskan dan langsung cocok dengan Prometheus/Grafana lokal; tracing penuh (OpenTelemetry) ditunda, korelasi cukup lewat `trace_id` di log. Tercatat sebagai ADR-023 di `docs/analisa/05-ADR-RBT.md`
    - _Requirements: 4.1_
  - [ ] 4.2 `pkg/telemetry`: registry Prometheus, handler `GET /metrics`, middleware HTTP (`rbt_http_requests_total`, `rbt_http_request_duration_seconds`, route template), adaptor `OnStateChange` breaker → `breaker_state{name}`; pasang `/metrics` + middleware di `delivery/server.go` kelima service (sama dengan 4.5). Setelah selesai, dashboard "RBT — HTTP & Runtime" otomatis terisi
    - _Requirements: 4.1, 4.2, 4.6_
  - [ ] 4.3 Lint/test yang menolak label berkardinalitas tinggi (allowlist label)
    - _Requirements: 4.3_
  - [x] 4.4 `pkg/logger` `ReplaceAttr` redaksi kunci sensitif (termasuk group bersarang) + test NIK/email tidak muncul. Selesai 2026-10-22: `pkg/logger/redact.go` (`replaceAttr` dipasang di `logger.New`; kunci R4.4 dicocokkan tanpa membedakan huruf/`_`/`-`, akhiran token/secret/password/email/nik ikut, `code` hanya persis) dan `logger.Redact()` untuk payload `map[string]any`/`[]any` yang dipakai 2.6; test ~80 kasus (kunci x json/text x datar/group/With/WithGroup/map) gagal bila `ReplaceAttr` dicabut. Belum: scrub nilai string (email di teks error) dan struct mentah di `slog.Any` tidak ditelusuri, aturannya log skalar/map saja
    - **Property 6: PII never logged**
    - _Requirements: 4.4_
  - [ ] 4.5 Middleware access log di `pkg/httpx` (field R4.7; lewati `/health`, `/metrics`) + `trace_id` dari `X-Amzn-Trace-Id` / `traceparent` dan diteruskan ke klien HTTP internal; log memuat `env`
    - Pemasangan: satu baris di `internal/delivery/server.go` tiap service (`httpx.Chain(mux, …)`) dan field `env` di `cmd/root.go` (`logger.New`) — ini bagian task ini, tidak ada task logging terpisah per service. Menyentuh service nuzirwan (perubahan kecil, koordinasikan)
    - _Requirements: 4.7, 4.8_
    - _Depends on: 4.4_
  - [ ] 4.6 `LOG_FORMAT=gcp` di `pkg/logger.ReplaceAttr` (`level→severity`, `msg→message`, `time→timestamp`, `trace_id→logging.googleapis.com/trace`); validasi `LOG_FORMAT` menerima `json|gcp|text`; test keluaran
    - _Requirements: 4.9_
  - [x] 4.7 Stack lokal `make obs`: Prometheus, Loki, Alloy, Grafana + 2 dashboard (Logs, HTTP & Runtime) — lihat 7.5
    - _Requirements: 4.10_

- [ ] 5. Lengkapi dan gerbangi kontrak
  - [ ] 5.1 CI job `contracts`: validasi 7 OpenAPI 3.1 (kin-openapi) dan JSON Schema 2020-12, dipicu perubahan `docs/contracts/`
    - _Requirements: 5.1_
  - [ ] 5.2 Schema `otp.requested.v1.schema.json` sudah ada (DEV-2, checkpoint #1) ✅. Tersisa: tambah `user-service` ke enum `producer` di `envelope.schema.json` (additive) — tanpa ini envelope OTP dari User gagal validasi kontrak — **perlu persetujuan pemilik kontrak (OQ-4)**
    - Catatan: `user-service` sudah ditambahkan ke enum `producer` di `envelope.schema.json` ✅
    - _Requirements: 5.3_
  - [ ] 5.3 Breaking-change check terhadap `main` (path/field/enum dihapus atau dipersempit → gagal)
    - **Property 7: Additive contracts**
    - _Requirements: 5.2_
  - [ ] 5.4 Validasi payload terhadap schema sebelum publish (hook di `pkg/outbox.Write` / `pkg/rabbitmq.Publisher`), aktif di `local`/`ci`, mode log-only di `prod` bila dinilai mahal
    - _Requirements: 5.4_
  - [ ] 5.5 `tests/contract/`: runner yang memanggil service di compose dan memvalidasi respons + error envelope (TC-API-01/02)
    - _Requirements: 5.5_
    - _Depends on: service spec endpoint selesai_

- [ ] 6. Kunci & secret
  - [x] 6.1 Perluas `make keys`: kunci Ed25519 untuk Queue (visitor/queue/admission), Payment (page token), Booking (ticket link), dan service token per pemanggil; mount ke compose; tetap di `.gitignore`. Selesai 2026-10-22: `make keys` idempoten membuat `{user,queue,payment,booking}-token(.pub).pem` (0644 untuk container nonroot); folder dimount `/run/keys` ke Queue, Notification, User. Kunci Payment/Booking dibuat tetapi belum dipakai kode (belum ada konsumen); service token per pemanggil ditunda sampai ada pemanggilnya
    - _Requirements: 6.1, 6.3_
  - [ ] 6.4 Seragamkan nama variabel kunci: `TOKEN_PUBLIC_KEY_FILE` (Queue, Notification) vs `TOKEN_PUBLIC_KEY_PATH` (Booking), `PII_HMAC_KEY` (Queue) vs `PII_HASH_KEY` (lainnya); satu nama untuk satu arti, koordinasikan dengan nuzirwan
    - _Requirements: 6.3_
  - [ ] 6.2 Dokumentasikan prosedur rotasi `kid` di `README.md` deployment
    - _Requirements: 6.2_
  - [ ] 6.3 Verifikasi stage `secret-detection` memindai `specs/` dan `docs/` juga
    - _Requirements: 6.4_

- [ ] 7. Lingkungan lokal clean-room
  - [ ] 7.1 `deployments/edge/nginx.conf` + layanan `edge` di compose sesuai tabel routing design §7 (cache 1 s / 2 s, SSE tanpa buffering, `/internal/*` dan `/metrics` → 403)
    - **Property 4: Internal API closed**
    - _Requirements: 7.2_
  - [ ] 7.2 `tools/seed` (data event sintetis lewat `POST /internal/v1/admin/events`) + target `make seed`
    - _Requirements: 7.3_
    - _Depends on: booking tasks 13_
  - [ ] 7.3 Profil compose `local-offline` dengan `PAYMENT_PROVIDER=simulator`, `APP_ENV=local-offline`
    - _Requirements: 7.4_
  - [x] 7.5 Observability lokal (`make obs`): `deployments/observability/{prometheus,loki,alloy,grafana}`, profile `observability` di compose, target `make obs`, folder `logs/` di-ignore. Terverifikasi 2026-10-21: keempat komponen sehat; log file dan container RabbitMQ masuk Loki; pencarian `| json | requestId=…` berjalan; datasource + 2 dashboard ter-provision. Cara pakai: catatan lokal pemilik platform (tidak di-commit)
    - _Requirements: 7.6, 4.10_
  - [x] 7.6 Perbaiki temuan uji lokal 2026-10-21: (a) `make up` — hanya User yang punya kunci di compose, jadi Queue/Booking/Notification berhenti saat start (config fail-fast); isi env kunci/PII untuk semuanya dan lengkapi `.env.example`; (b) worker Notification dan Payment ikut membuka HTTP di `APP_PORT` sehingga bentrok dengan `api` saat dijalankan dari source dengan `.env` yang sama; beri `APP_PORT` terpisah atau jangan buka HTTP di worker. Selesai 2026-10-22: compose memberi Queue/Notification env kunci, PII (anchor `x-pii`, `PII_HMAC_KEY` Queue = `PII_HASH_KEY` User), `BOOKING_SERVICE_URL`; `.env.example` Queue/Notification dilengkapi; target `make worker-<svc>` menjalankan worker di `APP_PORT`+100 (api + worker dari source berjalan bersamaan, diuji notification 8084/8184; di Docker tidak bentrok karena container terpisah). Hasil `make up`: queue, payment, notification, user healthy; booking restart-loop karena panic route ServeMux milik DEV-2 (booking 4.R3), bukan konfigurasi
    - _Requirements: 7.1, 6.1_
    - _Depends on: 6.1_
  - [ ] 7.4 `make smoke`: alur bahagia lewat edge
    - _Requirements: 7.5_
    - _Depends on: booking 1–10, queue 1–7, payment 1–6_

- [ ] 8. Gerbang CI tambahan
  - [ ] 8.1 Job `docs-diagrams`: render semua blok Mermaid di `docs/` dan `specs/` (mermaid-cli), gagal bila error
    - _Requirements: 8.2_
  - [ ] 8.2 Cek `make mock` tanpa diff dan `go mod tidy` tanpa diff di `verify-code-quality`
    - _Requirements: 8.3_
  - [ ] 8.3 Pastikan `scan-package` gagal pada temuan critical/high tanpa file risk-acceptance
    - _Requirements: 8.4_

- [ ] 9. Tooling operasional — **prioritas rendah** (setelah demo lokal jalan)
  - [ ] 9.1 `tools/reconcile` (Cobra): kerangka pemeriksaan per service + lintas service via endpoint internal; metrik `rbt_reconcile_violations{check}`; laporan JSON
    - _Requirements: 9.1_
    - _Depends on: 4.2, booking task 16, payment task 9_
  - [ ] 9.2 Mode `--apply` dengan dry-run wajib, audit aktor + diff
    - _Requirements: 9.2_
  - [ ] 9.3 `tools/redrive`: DLQ → queue utama dengan batas jumlah dan audit
    - _Requirements: 9.3_

- [ ] 10. Test lintas service & evidence — **prioritas rendah** (setelah demo lokal jalan)
  - [ ] 10.1 `tests/features/*.feature` (godog): alur bahagia, expiry, cancel, sold out, edit, navigasi pemulihan
    - _Requirements: 10.1_
  - [ ] 10.2 `tests/k6/`: TC-PERF-01..05
    - _Requirements: 10.2_
  - [ ] 10.3 Failure injection TC-CAP-01..03 (stop PostgreSQL primary / Valkey / RabbitMQ di compose)
    - _Requirements: 10.3_
  - [ ] 10.4 Simpan hasil ke `docs/evidence/YYYY-MM-DD-<topik>.md`
    - _Requirements: 10.4_
  - _Depends on: service specs selesai_

- [ ] 11. Deployment cloud — **prioritas rendah** (setelah demo lokal jalan). Log ke CloudWatch: agent/FireLens membaca stdout container (format `json`); Cloud Logging di GCP: `LOG_FORMAT=gcp` + Ops Agent; metrik: Amazon Managed Prometheus / Cloud Monitoring scrape `/metrics` dari jaringan privat
  - [ ] 11.1 Modul Terraform `network`, `edge` (CloudFront + WAF), `alb`, `asg`, `stateful`, `iam-oidc`, `budget`; environment `staging` `ap-southeast-3`
    - _Requirements: 11.1, 11.5_
  - [ ] 11.2 Header rahasia CloudFront → ALB; ALB tolak tanpa header (403); `/internal/*` tidak dirutekan
    - _Requirements: 11.2_
  - [ ] 11.3 Security group privat untuk DB/Valkey/broker/dashboard; akses SSM saja
    - _Requirements: 11.3_
  - [ ] 11.4 Scheduled scale-out ASG Queue; uji TC-ASG-01
    - _Requirements: 11.4_
  - [ ] 11.5 Demonstrasi backup/restore PostgreSQL + evidence
    - _Requirements: 11.6_

- [ ] 12. Penyelarasan event lintas service
  - [ ] 12.1 Konstanta bersama routing key + nama queue di `pkg/rabbitmq`, lalu ganti string di kelima service dalam satu PR (sekalian pindah ke `.v1`). Koordinasikan dengan DEV-2 karena menyentuh Booking, Queue, User
    - _Requirements: 12.1, 12.2_
  - [ ] 12.2 Test kontrak payload di CI untuk semua event; harus menangkap defect `booking.journey.ended` (booking 12.R1)
    - _Requirements: 12.3_
    - _Depends on: 5.1_

- [ ] 13. Retensi data pribadi
  - [ ] 13.1 Tetapkan env bersama: `PII_RETENTION_DAYS_AFTER_EVENT=30`, `OUTBOX_RETENTION_DAYS=7` + validasi; dokumentasikan kebijakan di README deployment
    - _Requirements: 13.1, 13.5_
  - [ ] 13.2 Retensi log 30 hari di konfigurasi deployment (compose: rotasi log driver; cloud: retensi log group)
    - _Requirements: 13.1_
  - [ ] 13.3 Pastikan job per service ada: booking 15.2 & 15.3, queue 17.1, notification 12.1, payment 14.1 (outbox) — semua idempoten, ber-metrik, tanpa PII di log
    - _Requirements: 13.2, 13.3, 13.4_

- [ ] 14. Checkpoint — platform siap dipakai service
  - `make ci` hijau; `pkg/nik`, middleware, `pkg/outbox`, redaksi logger tersedia dengan coverage ≥ 80%
  - Kontrak tervalidasi di CI; OQ-4 dan OQ-5 sudah diputuskan atau dicatat sebagai blocker di spec service
  - Tanyakan ke user bila ada keputusan yang tertunda

## Notes
- Urutan kerja yang disarankan untuk owner platform: 2.6 (format error) → 4.4 (redaksi log) → 5.2 (schema OTP) → 1 (`pkg/nik`) → 3 (`pkg/outbox`) → 7 (local) → 12 → 13. Task 9, 10, 11 menyusul setelah alur lokal jalan end-to-end.

- Task bertanda **blocked** menunggu open question di `specs/README.md`; jangan diimplementasikan dengan asumsi.
- Task 5.2 mengubah `docs/contracts/` — perubahan kontrak harus additive dan direview pemilik kontrak (ADR-017: klien mengunci ref).
- `pkg/` tidak boleh berisi aturan bisnis: batas kuota, TTL, dan state machine tetap di service.
