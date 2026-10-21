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
  - CI `ci.yml` (build, lint+depguard, coverage ≥80%, isolation, scan), compose infra + 5 service, `make keys` untuk User
  - _Requirements: 2.7, 2.8, 4.5, 6.2, 7.1, 8.1_

- [ ] 1. Implement `pkg/nik` validator struktur NIK
  - [ ] 1.1 Buat `pkg/nik/nik.go` dengan `Validate(s, now)` dan error sentinel sesuai SDD-RBT §4.9
    - Daftar provinsi sebagai `map[int]bool`; cek kab/kec ≠ `00`; tanggal kalender (DD+40), tidak di masa depan; urut ≠ `0000`
    - _Requirements: 1.1, 1.2, 1.3, 1.4_
    - _Design: §1_
  - [ ] 1.2 Test table-driven korpus SDD §4.9 (laki-laki, perempuan, 30 Feb, 0000, provinsi tak dikenal, kab 00, panjang, non-digit, abad sebelumnya)
    - **Property 1: NIK validator determinism**
    - _Requirements: 1.5_
  - [ ] 1.3 Dokumentasikan pemakaian di usecase (bukan domain) di godoc paket
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
  - [ ] 2.6 Format error standar: `pkg/apperror` mengirim hanya `{meta:{code, message}}` dengan `message = "KODE: pesan"` (atau pesan umum untuk 5xx); `requestId` di header `X-Request-ID`; `httpx.WriteError` menulis kode error, detail, error asli, dan payload request (diredaksi lewat 4.4) ke log. Satu perubahan di `pkg/` berlaku untuk lima service; beri tahu tim web/mobile
    - _Requirements: 2.8, 2.9_
    - _Depends on: 4.4_
  - [x] 2.7 `httpx.CORS()` untuk lokal (DEV-2); pastikan hanya dipasang saat `APP_ENV=local`
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
  - [ ] 4.1 ADR pemilihan library metrik (Prometheus client vs OpenTelemetry) sesuai tech radar — **blocked: OQ-5**
    - _Requirements: 4.1_
  - [ ] 4.2 `pkg/telemetry`: registry, handler endpoint metrik, middleware HTTP (`method`, route template, `status`, durasi), adaptor `OnStateChange` breaker → `breaker_state{name}`
    - _Requirements: 4.1, 4.2, 4.6_
    - _Depends on: 4.1_
  - [ ] 4.3 Lint/test yang menolak label berkardinalitas tinggi (allowlist label)
    - _Requirements: 4.3_
  - [ ] 4.4 `pkg/logger` `ReplaceAttr` redaksi kunci sensitif (termasuk group bersarang) + test NIK/email tidak muncul
    - **Property 6: PII never logged**
    - _Requirements: 4.4_

- [ ] 5. Lengkapi dan gerbangi kontrak
  - [ ] 5.1 CI job `contracts`: validasi 7 OpenAPI 3.1 (kin-openapi) dan JSON Schema 2020-12, dipicu perubahan `docs/contracts/`
    - _Requirements: 5.1_
  - [ ] 5.2 Schema `otp.requested.v1.schema.json` sudah ada (DEV-2, checkpoint #1) ✅. Tersisa: tambah `user-service` ke enum `producer` di `envelope.schema.json` (additive) — tanpa ini envelope OTP dari User gagal validasi kontrak — **perlu persetujuan pemilik kontrak (OQ-4)**
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
  - [ ] 6.1 Perluas `make keys`: kunci Ed25519 untuk Queue (visitor/queue/admission), Payment (page token), Booking (ticket link), dan service token per pemanggil; mount ke compose; tetap di `.gitignore`
    - _Requirements: 6.1, 6.3_
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

- [ ] 11. Deployment cloud — **prioritas rendah** (setelah demo lokal jalan)
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
