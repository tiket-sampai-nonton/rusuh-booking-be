# Requirements — Payment Service

| Field | Value |
|-------|-------|
| **Service** | Payment Service |
| **Spec ID** | SPEC-PAYMENT-REQ-001 |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-PAYMENT-001.md` |
| **Kontrak** | `docs/contracts/openapi/payment.yaml`, `docs/contracts/openapi/internal-payment.yaml`, `docs/contracts/events/{payment.succeeded,payment.failed,booking.review}.v1.schema.json` |
| **SRS** | §3.2.13–§3.2.19 (SRS-F-13..19), Keamanan (SRS-C-21, SRS-Q-SEC-*) |
| **Port** | 8083 |
| **Schema / Role** | `payment` / `svc_payment` |
| **Owner** | BE-2 |
| **Arsitektur** | Hexagonal + Event Sourcing (ADR-019) |
| **Status implementasi** | **Boilerplate Sprint 1** — lihat §Status Implementasi |

> **Aturan grounding:** jika SDD prosa berbeda dari kontrak OpenAPI/skema event atau migrasi DDL, **kontrak dan migrasi yang berlaku** (file migrasi terterapkan tidak boleh diubah — `structure.md`). Spec ini sudah direkonsiliasi ke kontrak nyata (mis. enum metode `VA_BCA/VA_BNI/VA_BRI/VA_PERMATA/QRIS/GOPAY`, envelope `{meta,data}`, resolve `mode`/`outcome`, error code `IDEMPOTENCY_KEY_REUSED`/`PAYMENT_PROVIDER_UNAVAILABLE`).

---

## Status Implementasi (grounding ke kode nyata)

Pembacaan `services/payment/` (19 Okt) menunjukkan service masih **boilerplate**. Tabel delta per komponen nyata:

| Komponen | Berkas nyata | Status | Catatan |
|----------|--------------|--------|---------|
| Verb Cobra | `cmd/{main,root,api,worker,migrate,healthcheck}.go` | **IMPL** | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` |
| Config loader | `internal/config/{config.go,load.go}` | **IMPL (subset)** | Memuat `APP_*`, `LOG_*`, `HTTP_*`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL`, `POSTGRES_*`, `RABBITMQ_*`. **Belum**: `PAYMENT_PROVIDER`, `MIDTRANS_*`, `BOOKING_SERVICE_URL`, `TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL*`, CB config |
| Composition root | `internal/delivery/{server.go,worker.go,clients.go,serve.go}` | **IMPL** | Hanya mendaftarkan `GET /health`; worker loop kosong (interval saja) |
| Domain `payment` | `internal/payment/**` | **IMPL (health only)** | Hanya slice health (entity/usecase/handler/mapper/vo health). Tidak ada usecase/handler/adapter bisnis |
| Migrasi `0001_init` | `deployments/migrations/0001_init.up.sql` | **IMPL** | `payment_attempts`, `payment_callbacks`, `payment_page_tokens`, `refund_cases`, `idempotency_records`, `outbox_events` + semua index/constraint |
| Migrasi `0002_event_store` | `deployments/migrations/0002_event_store.up.sql` | **IMPL** | `payment_events` + trigger append-only + `trg_payment_events_no_truncate` + kolom `payment_attempts.stream_version` |
| Mock | `_mock/payment/port/{inbound,outbound}/*` | **IMPL (health only)** | Hanya mock health |
| Lib resilience | `pkg/resilience/breaker.go` | **IMPL (shared)** | `Breaker` (Closed/Open/HalfOpen) + `Bulkhead`; TC-RES-01/02 PASS |
| Lib event store | `pkg/eventstore/eventstore.go` | **IMPL (shared)** | Append + optimistic concurrency; tolak payload PII/secret |
| Error mapping | `pkg/apperror/apperror.go` | **IMPL (shared)** | domain error → `errorCode` |

**Belum ada (target spec ini):** seluruh endpoint publik/internal payment, usecase (12), adapter Midtrans/simulator, verifikasi webhook, event sourcing attempt, projeksi read model, outbox relay, consumer `booking.review`, circuit breaker gateway, worker expiry sweeper, DTO/mapper/VO bisnis.

> Tanda **[IMPL]** = sudah ada di kode; **[TARGET]** = belum, harus dibangun. Kriteria ditulis sebagai target yang di-trace ke SDD/SRS/kontrak.

---

## Ringkasan

Payment Service mengintegrasikan Midtrans (atau simulator lokal), menangani payment page, charge (attempt), webhook callback, settlement, expiry/resolve, dan refund case. Siklus **PaymentAttempt** direkam dengan **event sourcing**: `payment_events` adalah sumber kebenaran, `payment_attempts` adalah read model yang diproyeksikan **sinkron** dalam transaksi yang sama dengan append event (ADR-019). Prinsip inti (SDD §1, invarian repo):

1. **Verifikasi signature adalah satu-satunya bukti pembayaran** — redirect/WebView/bridge/klien bukan bukti.
2. **At-most-once charge** — paling banyak satu attempt terbuka per booking (`uq_attempt_open_per_booking`); retry via `Idempotency-Key`.
3. **Webhook idempoten** — callback bisa diterima berkali-kali tanpa efek ganda (`UNIQUE(provider,fingerprint)`), out-of-order aman (status final tidak di-downgrade).
4. **Audit trail lengkap** — event store append-only untuk compliance.

---

## Kebutuhan

### R1 — Payment Page (SRS-F-14)

**User story:** Sebagai pembeli yang booking-nya terkunci, saya ingin melihat halaman pembayaran (jumlah, batas waktu, metode tersedia, attempt saat ini) agar bisa membayar sebelum TTL habis.

Kriteria (EARS):
- **R1.1** WHEN klien `GET /api/v1/payments/{bookingId}/page` dengan token visitor valid (`rb_visitor` cookie / `X-Visitor-Token` header / `rb_pay` cookie), THE service SHALL mengembalikan 200 envelope `{meta,data}` dengan `bookingId`, `methods[]`, `amountMinor`, `paymentExpiresAt`, `serverTime`, `canEdit`, `editableUntil`, `currentAttempt`. _(payment.yaml `PaymentPage`; SDD §3.1; TC-PAY-01, TC-PAY-10)_ **[TARGET]**
- **R1.2** WHERE booking tidak dalam keadaan payable (bukan HELD/LOCKED), THE service SHALL menolak 409 `BOOKING_NOT_PAYABLE`. _(payment.yaml; TC-PAY-10)_ **[TARGET]**
- **R1.3** WHERE TTL booking telah habis, THE service SHALL mengembalikan 410 `BOOKING_EXPIRED`. _(SDD App.B)_ **[TARGET]**
- **R1.4** IF token visitor tidak cocok dengan booking, THEN THE service SHALL menolak 403 `VISITOR_MISMATCH`. _(SDD App.B; TC-SEC-03)_ **[TARGET]**
- **R1.5** THE service SHALL tidak pernah memuat NIK plaintext pada respons page/status; data pembeli terbatas pada yang diperlukan untuk membayar. _(security-privacy steering; SRS-Q-SEC-*; TC-SEC-03)_ **[TARGET]**

### R2 — Page Token untuk Mobile WebView (SRS-F-18)

**User story:** Sebagai pengguna mobile, saya ingin menukar token sekali pakai menjadi sesi halaman bayar agar WebView dapat membuka halaman dengan aman tanpa membocorkan token visitor.

- **R2.1** WHEN `POST /api/v1/payments/{bookingId}/page-tokens` dengan visitor valid, THE service SHALL menerbitkan 201 `{pageToken, pageUrl, expiresAt}`, token sekali pakai ber-TTL (`PAYMENT_PAGE_TOKEN_TTL`, default 60s), menyimpan **hanya** `token_hash` (BYTEA) dan `visitor_id`. _(payment.yaml `PageToken`; migrasi `payment_page_tokens`; SDD §3.2; TC-MOB-01)_ **[IMPL: tabel]** **[TARGET: usecase]**
- **R2.2** WHEN `POST /api/v1/payment-page-sessions` dengan `pageToken` valid & belum dipakai & belum kedaluwarsa, THE service SHALL mengembalikan 204 dan menyetel cookie `rb_pay` (HttpOnly, Secure, SameSite=Lax) serta menandai `used_at`. _(payment.yaml; SDD §3.3; TC-MOB-01)_ **[TARGET]**
- **R2.3** IF token sudah dipakai / kedaluwarsa / tidak dikenal, THEN THE service SHALL menolak 403 `PAYMENT_PAGE_TOKEN_INVALID`. _(payment.yaml; TC-SEC-01 replay)_ **[TARGET]**

### R3 — Create Payment Attempt / Charge (SRS-F-14, SRS §3.2.24 idempotency)

**User story:** Sebagai pembeli, saya ingin memilih metode dan memulai pembayaran agar mendapat instruksi (VA / QR / deeplink).

- **R3.1** WHEN `POST /api/v1/payments/{bookingId}/attempts` dengan header `Idempotency-Key` (16–64 char) dan `method ∈ {VA_BCA,VA_BNI,VA_BRI,VA_PERMATA,QRIS,GOPAY}`, THE service SHALL memanggil payment-lock Booking, meng-charge provider, dan mengembalikan 201 `{attemptNo, method, status, bookingVersion, expiresAt, instruction}`. _(payment.yaml `CreateAttemptRequest`/`Attempt`; SDD §3.4; TC-PAY-01)_ **[TARGET]**
- **R3.2** THE service SHALL memastikan **at-most-once charge**: paling banyak satu attempt terbuka (`status IN (CREATED,PENDING,UNKNOWN)`) per booking, dijaga index parsial `uq_attempt_open_per_booking`. _(migrasi; invariant #5)_ **[IMPL: constraint]** **[TARGET: usecase]**
- **R3.3** WHEN request diterima dengan `Idempotency-Key` yang sama dan `request_hash` cocok, THE service SHALL mengembalikan respons tersimpan tanpa charge ulang; IF key sama tetapi `request_hash` berbeda, THEN tolak 409 `IDEMPOTENCY_KEY_REUSED`. _(payment.yaml; migrasi `idempotency_records`; idempotency steering)_ **[IMPL: tabel]** **[TARGET: usecase]**
- **R3.4** IF `method` tidak aktif/didukung, THEN THE service SHALL menolak 422 `PAYMENT_METHOD_UNAVAILABLE`. _(payment.yaml)_ **[TARGET]**
- **R3.5** WHILE circuit breaker gateway OPEN, THE service SHALL menolak charge dengan 503 `PAYMENT_PROVIDER_UNAVAILABLE` tanpa memanggil gateway (`pkg/resilience.ErrOpen`). _(payment.yaml; SDD App.C; SRS-F-19; TC-PAY-08, TC-RES-01)_ **[TARGET]**
- **R3.6** IF provider mengembalikan error/timeout, THEN THE service SHALL mengembalikan 503 `PAYMENT_PROVIDER_UNAVAILABLE`, menaikkan hitung kegagalan breaker, dan menyimpan attempt sebagai `UNKNOWN` (bukan di-charge-ulang membabi buta). _(payment.yaml; SDD App.C)_ **[TARGET]**
- **R3.7** WHEN pembeli mengganti metode (TC-PAY-10), THE service SHALL membatalkan attempt terbuka lama di gateway lalu membuat attempt baru **tanpa mereset TTL booking**. _(SDD §15 TC-PAY-10)_ **[TARGET]**

### R4 — Webhook Midtrans (SRS-F-15, SRS-C-21)

**User story:** Sebagai sistem, saya ingin memproses callback Midtrans secara aman dan idempoten agar status pembayaran akurat.

- **R4.1** WHEN `POST /api/v1/webhooks/midtrans`, THE service SHALL memverifikasi signature `SHA512(order_id + status_code + gross_amount + server_key)` **sebelum** memproses. IF signature tidak cocok atau kosong, THEN tolak 401 `WEBHOOK_SIGNATURE_INVALID` dan simpan callback dengan `signature_valid=false, processing_result=REJECTED`. _(payment.yaml; SDD §8.4; SRS-C-21; TC-PAY-11)_ **[IMPL: kolom callback]** **[TARGET: usecase]**
- **R4.2** THE service SHALL memperlakukan **verifikasi signature sebagai satu-satunya bukti pembayaran**; tidak ada jalur lain (redirect/WebView/bridge/klien) yang boleh mengubah status menjadi SUCCEEDED. _(security-privacy steering; invariant #5)_ **[TARGET]**
- **R4.3** WHEN callback dengan `(provider, fingerprint)` yang sudah pernah diproses diterima, THE service SHALL idempoten: `INSERT ... ON CONFLICT DO NOTHING`, tidak ada efek ganda, kembalikan 200, tandai `processing_result=DUPLICATE`. _(migrasi `UNIQUE(provider,fingerprint)`; TC-PAY-05 duplicate)_ **[IMPL: constraint]** **[TARGET: usecase]**
- **R4.4** WHEN `transaction_status` dipetakan ke status attempt (`settlement|capture→SUCCEEDED`, `pending→PENDING`, `deny|cancel|failure→FAILED`, `expire→EXPIRED`), THE service SHALL meng-append event dan memproyeksikan read model dalam **satu transaksi**, serta menulis outbox bila ada perubahan uang. _(SDD App.A.4; §7)_ **[TARGET]**
- **R4.5** WHEN callback datang out-of-order (mis. settlement sebelum pending, atau callback setelah status final), THE service SHALL tidak men-downgrade status final (`SUCCEEDED/FAILED/EXPIRED/REFUNDED`). _(SDD §15 matrix; TC-PAY-05)_ **[TARGET]**
- **R4.6** THE service SHALL mencatat callback ke `payment_callbacks` **durable sebelum** mengembalikan 2xx. _(payment.yaml 200 desc)_ **[TARGET]**

### R5 — Settlement & Event Publishing (SRS-F-16)

**User story:** Sebagai Booking Service, saya ingin menerima `payment.succeeded` saat pembayaran settle agar booking diselesaikan dan tiket diterbitkan.

- **R5.1** WHEN attempt menjadi SUCCEEDED, THE service SHALL menulis event `AttemptSettled` + outbox `payment.succeeded` `{bookingId, attemptId, settlementTime, amountMinor, method}` dalam transaksi yang sama. _(payment.succeeded.v1 schema; SDD §9.1; TC-PAY-03, TC-PAY-06)_ **[IMPL: outbox table]** **[TARGET: usecase]**
- **R5.2** WHEN attempt menjadi FAILED, THE service SHALL menulis event `AttemptFailed` + outbox `payment.failed` `{bookingId, attemptId, reason}`. _(payment.failed.v1 schema; SDD §9.1)_ **[TARGET]**
- **R5.3** THE outbox relay worker SHALL mempublikasikan `outbox_events` yang belum terbit ke RabbitMQ exchange `rusuh.events` (topic) dengan **publisher confirm**, menandai `published_at`, naik `publish_attempts`, dan mencatat `last_error` saat gagal (retry berjenjang `RABBITMQ_RETRY_DELAYS`, DLQ setelah `RABBITMQ_MAX_RETRIES`). _(migrasi `outbox_events`; SDD §10.1)_ **[IMPL: table]** **[TARGET: worker]**
- **R5.4** WHEN pembayaran settle tepat sebelum expiry (race), THE service SHALL tetap memproses settlement berdasarkan `settlement_time` dan memublikasikan `payment.succeeded` sehingga Booking ditandai PAID. _(SDD §15 TC-PAY-06; RISK-T-02/03)_ **[TARGET]**

### R6 — Payment Status (SRS-F-14)

- **R6.1** WHEN `GET /api/v1/payments/{bookingId}/status`, THE service SHALL mengembalikan 200 `{bookingId, bookingStatus, attempt, serverTime}` dari read model; status pembayaran **tidak pernah** diturunkan dari aksi klien. _(payment.yaml `PaymentStatus`; SDD §3.5; invariant #4)_ **[TARGET]**

### R7 — Expiry & Resolve (SRS-F-17)

**User story:** Sebagai Booking Service saat expiry sweep, saya ingin meminta Payment memeriksa (dan opsional membatalkan) attempt agar tidak ada pembayaran menggantung.

- **R7.1** WHEN `POST /internal/v1/payments/{bookingId}/resolve` dengan `mode ∈ {CHECK, CHECK_AND_CANCEL}` (service bearer token), THE service SHALL memeriksa gateway dan mengembalikan 200 `{outcome ∈ {SUCCEEDED,NOT_PAID,NO_ATTEMPT,UNKNOWN}, settlementTime?, attemptId?}`. _(internal-payment.yaml `ResolveRequest`/`ResolveResponse`; SDD §4.1; TC-PAY-04, TC-PAY-07)_ **[TARGET]**
- **R7.2** WHEN attempt PENDING melewati `expires_at` tanpa webhook, THE expiry sweeper SHALL menandai EXPIRED (append `AttemptExpired`), membatalkan di gateway bila perlu. _(SDD §10.2; TC-PAY-04)_ **[TARGET]**
- **R7.3** IF `CHECK_AND_CANCEL` menemukan pembayaran settle setelah booking dibatalkan/expired, THEN THE service SHALL tidak membatalkannya, membuka `refund_cases` (`SETTLED_AFTER_CANCEL`), menandai attempt `REFUND_REQUIRED`, dan kembalikan `outcome=SUCCEEDED`. _(migrasi `refund_cases`; SDD §4.1; TC-PAY-07)_ **[IMPL: table]** **[TARGET: usecase]**
- **R7.4** WHILE gateway degraded saat resolve, THE service SHALL mengembalikan `outcome=UNKNOWN` dan membiarkan breaker OPEN (Booking menahan stok/VERIFYING dan retry nanti) alih-alih memblokir. _(SDD §19; TC-PAY-08, TC-RES-01/02)_ **[TARGET]**

### R8 — Refund Cases via `booking.review` (SRS-F-17)

**User story:** Sebagai sistem rekonsiliasi, saya ingin membuka refund case idempoten saat Booking meminta review (pembayaran telat).

- **R8.1** WHEN mengkonsumsi `booking.review` dari Booking `{bookingId, attemptHint?, buyerEmail, amountMinor}`, THE service SHALL membuka refund case **idempoten tanpa tabel inbox**, mengandalkan `refund_cases.attempt_id UNIQUE` + `ON CONFLICT DO NOTHING`. _(booking.review.v1 schema; migrasi; code-principles consumer idempoten)_ **[IMPL: constraint]** **[TARGET: consumer]**
- **R8.2** IF payload rusak/tak terparse, THEN THE consumer SHALL mengembalikan `rabbitmq.Permanent(err)` agar dikirim ke DLQ, bukan retry tak terbatas. _(code-principles)_ **[TARGET]**

### R9 — Circuit Breaker / Degradation (SRS-F-19, D-40)

- **R9.1** THE service SHALL membungkus setiap panggilan sinkron ke Midtrans (charge, status check, cancel) dengan `pkg/resilience.Breaker` (default `FailureThreshold=3`, `Cooldown=60s`, `HalfOpenProbes=1`, `Timeout` dari config). WHILE OPEN, panggilan ditolak cepat (`ErrOpen`). _(pkg/resilience; SDD App.C; TC-RES-01)_ **[IMPL: lib]** **[TARGET: wiring]**
- **R9.2** THE breaker SHALL CLOSED→OPEN pada kegagalan beruntun ≥ threshold, OPEN→HALF_OPEN setelah cooldown, HALF_OPEN→CLOSED pada probe sukses atau →OPEN pada probe gagal. _(pkg/resilience; TC-RES-01)_ **[IMPL]**
- **R9.3** THE service MAY membungkus gateway dengan `Bulkhead` untuk membatasi konkurensi; penuh ⇒ `ErrBulkheadFull`. _(pkg/resilience; TC-RES-02)_ **[TARGET, SHOULD]**

### R10 — Cancel (SRS-F-13)

- **R10.1** WHEN pembeli membatalkan sebelum bayar, THE service SHALL membatalkan attempt di gateway; IF sudah SUCCEEDED, THEN pertahankan PAID; ELSE tandai CANCELLED dan picu Booking melepas stok. _(SDD §15; TC-PAY-09)_ **[TARGET]**

### R11 — Keamanan & Isolasi (SRS-Q-SEC-*, SRS-C-21)

- **R11.1** THE endpoint `/internal/v1/*` SHALL hanya menerima `Authorization: Bearer <service token>` (EdDSA, ADR-011) dan ditolak dari jaringan publik. _(internal-payment.yaml `serviceBearer`; TC-SEC-02)_ **[TARGET]**
- **R11.2** THE service SHALL menolak replay page token (TC-SEC-01, 403) dan bypass origin check (TC-SEC-03, 403). **[TARGET]**
- **R11.3** THE payload `payment_events`, `outbox_events`, dan log SHALL tidak memuat data kartu, `MIDTRANS_SERVER_KEY`, atau token plaintext (ditolak di `pkg/eventstore`; komentar DDL `payload jsonb -- tanpa data kartu, server key, atau token`). _(migrasi 0002; code-principles)_ **[IMPL: DDL+lib]** **[TARGET: enforcement di usecase]**

### R12 — Health & Operasional (D-23)

- **R12.1** WHEN `GET /health`, THE service SHALL mengembalikan 200 UP / 503 DOWN berdasarkan ping Postgres & RabbitMQ dengan batas `HEALTH_TIMEOUT`. _(payment.yaml `Health`; SDD §13)_ **[IMPL]**
- **R12.2** THE konfigurasi SHALL divalidasi saat start (fail fast) — sudah untuk `APP_*/LOG_*/HTTP_*/POSTGRES_*/RABBITMQ_*`; harus diperluas untuk `PAYMENT_PROVIDER`, `MIDTRANS_*`, `BOOKING_SERVICE_URL`, `TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL*`, CB config. _(config.go; SDD §11)_ **[IMPL: subset]** **[TARGET: Midtrans vars]**

---

## Invarian (tidak boleh dilanggar)

| # | Invarian | Penegak |
|---|----------|---------|
| I-1 | **Verifikasi signature = satu-satunya bukti pembayaran** (R4.1/R4.2). Redirect/WebView/bridge bukan bukti. | Usecase webhook + `payment_callbacks.signature_valid` |
| I-2 | **At-most-once charge** — ≤ 1 attempt terbuka per booking; retry via idempotency key, bukan charge baru (R3.2/R3.3). | `uq_attempt_open_per_booking`, `idempotency_records` |
| I-3 | **Webhook idempoten & out-of-order aman** — callback ganda tanpa efek ganda; status final tidak di-downgrade (R4.3/R4.5). | `UNIQUE(provider,fingerprint)`, state machine |
| I-4 | **Status final hanya dari webhook/resolve** — bukan dari aksi klien (R6.1, invariant repo #5). | Read model projeksi; handler status read-only |
| I-5 | **Event store append-only** — tidak ada UPDATE/DELETE/TRUNCATE `payment_events` (R11.3). | Trigger `trg_payment_events_append_only`/`_no_truncate` |
| I-6 | **Perubahan uang ⇒ outbox dalam transaksi yang sama** (R5.1/R5.2; code-principles). | Transaksi usecase settlement/failed |
| I-7 | **Tanpa PII/secret di event/outbox/log** (R11.3). | `pkg/eventstore`, review usecase |
| I-8 | **Refund case idempoten tanpa inbox** (R8.1). | `refund_cases.attempt_id UNIQUE` + `ON CONFLICT` |

---

## Build & Verify

### Build (jalankan lokal)
```bash
make infra keys
cd services/payment && cp .env.example .env    # set PAYMENT_PROVIDER=simulator (TARGET var)
go run ./cmd migrate up
go run ./cmd api &
go run ./cmd worker &
curl -fsS localhost:8083/health
```

Mode simulator memakai `POST /api/v1/local-simulator/payments/{attemptId}/scenario` (hanya `APP_ENV=local`/`ci`) dengan skenario `SETTLE|SETTLE_LATE|EXPIRE|DENY|TIMEOUT|DUPLICATE_WEBHOOK|OUT_OF_ORDER` untuk mendorong webhook tanpa Midtrans nyata.

### Verify
```bash
make build && make lint                         # modul payment (+ depguard lapisan)
go test ./... -race                             # unit (payment)
make test-integration                           # adapter: postgres, rabbitmq (build tag integration)
make cover                                       # ambang logika 80% (COVERAGE_MIN)
make ci                                          # gerbang akhir sebelum PR
```
Coverage dihitung atas kode logika; `cmd/`, `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan. Verifikasi lulus bila `make ci` hijau dan TC kunci di bawah terpenuhi.

---

## Traceability Matrix

| Req | SRS | Test Case | Status |
|-----|-----|-----------|--------|
| R1 Payment Page | SRS-F-14 | TC-PAY-01, TC-PAY-02, TC-PAY-10 | TARGET |
| R2 Page Token/WebView | SRS-F-18 | TC-MOB-01, TC-MOB-02, TC-MOB-03, TC-SEC-01 | TARGET (DB impl) |
| R3 Create Attempt | SRS-F-14, §3.2.24 | TC-PAY-01, TC-PAY-08, TC-PAY-10 | TARGET (DB impl) |
| R4 Webhook + Signature | SRS-F-15, SRS-C-21 | TC-PAY-05, TC-PAY-11 | TARGET (DB impl) |
| R5 Settlement/Publish | SRS-F-16 | TC-PAY-03, TC-PAY-06 | TARGET (DB impl) |
| R6 Status | SRS-F-14 | TC-PAY-01 | TARGET |
| R7 Expiry/Resolve | SRS-F-17 | TC-PAY-04, TC-PAY-07, TC-PAY-08 | TARGET (DB impl) |
| R8 Refund via booking.review | SRS-F-17 | TC-PAY-07 | TARGET (DB impl) |
| R9 Circuit Breaker | SRS-F-19, D-40 | TC-RES-01, TC-RES-02, TC-PAY-08 | IMPL (lib) / TARGET (wiring) |
| R10 Cancel | SRS-F-13 | TC-PAY-09 | TARGET |
| R11 Security/Isolasi | SRS-Q-SEC-*, SRS-C-21 | TC-SEC-01, TC-SEC-02, TC-SEC-03 | TARGET (DB impl) |
| R12 Health/Config | D-23, SDD §11 | TC-OPS-01 | IMPL (subset) |

### TC kunci (dari `docs/test/06-TP-RBT-001.md`)

| TC ID | Skenario | Ekspektasi | Tool |
|-------|----------|-----------|------|
| TC-PAY-01 | Pilih metode & bayar | Instruksi dari Midtrans Sandbox aktual | Katalon |
| TC-PAY-03 | Sukses tepat waktu | PAID, tiket, email, slot dibebaskan | Katalon + Mailpit |
| TC-PAY-04 | Tidak dibayar | Resolve → batal di gateway → EXPIRED | Go integration |
| TC-PAY-05 | Callback idempoten | Duplikat, out-of-order, signature salah | Go integration, Apidog |
| TC-PAY-06 | Webhook telat tapi bayar tepat waktu | PAID (berdasarkan `settlement_time`) | Go integration |
| TC-PAY-07 | Pembayaran setelah EXPIRED | REVIEW, tanpa tiket, kasus refund, email | Go integration |
| TC-PAY-08 | Gateway down saat TTL habis | VERIFYING, stok ditahan, hasil benar setelah pulih | Go integration (failure injection) |
| TC-PAY-09 | Cancel | Sudah bayar → PAID; belum → CANCELLED + slot dibebaskan | godog |
| TC-PAY-10 | Ganti metode | Attempt lama dibatalkan; TTL tidak reset | godog |
| TC-PAY-11 | Webhook signature valid/salah/kosong | Valid 200; salah/kosong 401 `WEBHOOK_SIGNATURE_INVALID` | Integration |
| TC-MOB-02 | WebView payment | Sukses & expired; deeplink e-wallet | Appium |
| TC-SEC-01 | Replay token | Ditolak 403 | godog, ZAP |
| TC-SEC-02 | Bypass origin | Request langsung ke ALB → 403 | Script |
| TC-SEC-03 | PII | Tidak ada NIK/email/token di log & metrik | Log scan |
| TC-RES-01 | Breaker trip | OPEN → gagal cepat; HALF_OPEN setelah cooldown; satu probe | Unit (`pkg/resilience`) — PASS |
| TC-RES-02 | Bulkhead penuh | `ErrBulkheadFull`; slot kembali | Unit — PASS |
