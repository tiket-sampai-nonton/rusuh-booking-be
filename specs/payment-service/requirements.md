# Requirements Document — Payment Service

| Field | Value |
|-------|-------|
| **Spec** | payment-service |
| **Service** | `services/payment` |
| **Port** | 8083 |
| **Schema / Role** | `payment` / `svc_payment` |
| **Owner** | BE-2 |
| **Arsitektur** | Hexagonal + Event Sourcing agregat PaymentAttempt (ADR-019) |
| **Sumber (urut prioritas)** | migrasi `0001_init`, `0002_event_store` → kontrak `docs/contracts/openapi/{payment,internal-payment,internal-booking}.yaml`, `docs/contracts/events/{payment.succeeded,payment.failed,booking.review}.v1.schema.json` → SRS §3.1.3, §3.2.13–3.2.19 → **SDD-RBT-001 §4.6.2–4.6.3** (breaker, aturan transaksi Midtrans, resolve) → `docs/sdd/SDD-PAYMENT-001.md` |

## Introduction

Payment Service mengintegrasikan Midtrans Sandbox (atau simulator lokal), menangani payment page
(web + mobile WebView), charge (attempt), webhook, status, resolve untuk Booking, dan refund case.
Siklus **PaymentAttempt** direkam dengan event sourcing: `payment_events` adalah sumber kebenaran,
`payment_attempts` read model yang diproyeksikan sinkron dalam transaksi yang sama (ADR-019).

Prinsip inti: (1) **signature webhook terverifikasi adalah satu-satunya bukti pembayaran**;
(2) **paling banyak satu attempt terbuka per booking**; (3) **webhook idempoten dan out-of-order aman**;
(4) **tidak ada transaksi gateway sebelum `payment-lock` Booking berhasil** — nominal = versi terkunci;
(5) **Payment tidak memutuskan PAID** — Booking yang memutuskan berdasarkan `settlement_time`.

### Status Implementasi (audit kode 2026-10-21)

| Komponen | Berkas | Status |
|----------|--------|--------|
| Verb Cobra, config subset (`APP_*`, `LOG_*`, `HTTP_*`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL`, `POSTGRES_*`, `RABBITMQ_*`), composition root, `GET /health` | `cmd/*`, `internal/config/*`, `internal/delivery/*` | ✅ |
| Migrasi `payment_attempts`, `payment_callbacks`, `payment_page_tokens`, `refund_cases`, `idempotency_records`, `outbox_events`, `payment_events` (+ trigger append-only) | `deployments/migrations/0001_init`, `0002_event_store` | ✅ |
| `pkg/resilience` (Breaker + Bulkhead, tanpa nilai default), `pkg/eventstore`, `pkg/apperror` | `pkg/` | ✅ (shared) |
| Endpoint publik/internal, usecase, adapter Midtrans/simulator, webhook, event sourcing attempt, outbox relay, consumer `booking.review`, worker | — | ❌ |

### Delta terhadap spec/SDD lama (ditandai Δ)

| # | Lama | Yang dipakai |
|---|------|--------------|
| Δ1 | Booking payable bila "HELD/LOCKED" | `PENDING_PAYMENT` dan `now < paymentExpiresAt` (Booking `payment-lock` outcome) |
| Δ2 | Default breaker `FailureThreshold=3, Cooldown=60s` | `pkg/resilience` tanpa default [kode]; nilai PROPOSED SDD-RBT §4.6.2: charge timeout 3 s / ambang 5 / cooldown 20 s / bulkhead 20; status timeout 2 s / ambang 5 / cooldown 15 s / bulkhead 10 / retry maks 2 (K9) |
| Δ3 | `order_id = RBT-{bookingId}-{attemptNo}` | `RB-{publicRef}-{attemptNo}` (SDD-RBT §4.6.3) |
| Δ4 | Resolve membuka refund case bila menemukan settle | Resolve hanya melaporkan `SUCCEEDED + settlementTime`; refund case dibuka dari `booking.review` (Booking yang menilai tepat waktu/terlambat) |
| Δ5 | `custom_expiry` VA tetap 900 s | Diturunkan dari `paymentExpiresAt` booking (SRS-IF-21, SRS-F-26-05) |
| Δ6 | Attempt baru disimpan setelah charge | Attempt `CREATED` (order ID deterministik) disimpan **sebelum** charge sehingga timeout ambigu dapat direkonsiliasi (SRS-F-14-05) |
| Δ7 | Envelope `eventId`, routing key `payment.succeeded` | Envelope `messageId`; routing key berversi `payment.succeeded.v1` (K8) |
| Δ8 | Error `BOOKING_EXPIRED` (410), `VISITOR_MISMATCH` (403), `ATTEMPT_IN_PROGRESS` (409) | Tidak ada di `payment.yaml` maupun SRS §3.1.4 — **OQ-19** |

## Glossary

- **Attempt** — satu upaya bayar dengan satu `method`; `attempt_no` naik per booking; status `CREATED → PENDING → SUCCEEDED/FAILED/CANCELLED/EXPIRED`, transien `UNKNOWN`, lanjutan `REFUND_REQUIRED → REFUNDED`.
- **Attempt terbuka** — status `CREATED`, `PENDING`, atau `UNKNOWN` (`uq_attempt_open_per_booking`).
- **Payment page token** — JWT/opaque sekali pakai TTL ≈ 60 s untuk membuka `/pay/{bookingId}` di WebView; ditukar menjadi cookie `rb_pay`.
- **Resolve** — permintaan Booking untuk memastikan status pembayaran: `CHECK` atau `CHECK_AND_CANCEL`.
- **Refund case** — kasus Ops untuk pembayaran yang diterima tetapi tidak menghasilkan tiket (`LATE_SETTLEMENT`, `SETTLED_AFTER_CANCEL`, `DUPLICATE_PAYMENT`).

---

## Requirement 1 — Payment Page

**User Story:** Sebagai pembeli yang punya pesanan menunggu pembayaran, saya ingin melihat total, batas waktu, dan metode yang tersedia, agar dapat membayar sebelum TTL habis.

Trace: SRS-F-14-01, SRS-F-14-02, SRS-F-19-02, SRS-Q-SEC-01 · TC-PAY-01, TC-PAY-10, TC-SEC-03

### Acceptance Criteria (EARS)

1. WHEN klien `GET /api/v1/payments/{bookingId}/page` dengan visitor token (`rb_visitor` / `X-Visitor-Token`) atau cookie `rb_pay` milik booking itu, THE service SHALL mengembalikan `200 PaymentPage{ bookingId, methods[], amountMinor, paymentExpiresAt, serverTime, canEdit, editableUntil, currentAttempt }` dengan data booking dari `GET /internal/v1/bookings/{id}/payable`.
2. THE `methods[]` SHALL berisi metode di `PAYMENT_METHODS_ENABLED` (default `VA_BCA,VA_BNI,VA_BRI,QRIS,GOPAY`; kartu kredit tidak pernah ada) dan menandai metode yang breaker-nya OPEN sebagai sedang gangguan (SRS-F-14-02, SRS-F-19-02). *(Bentuk penanda di kontrak `PaymentPage` perlu diverifikasi saat implementasi.)*
3. IF booking tidak menunggu pembayaran, THEN THE service SHALL menolak `409 BOOKING_NOT_PAYABLE`.
4. IF token bukan milik booking, THEN THE service SHALL menolak tanpa membocorkan data booking. *(Kode error: OQ-19.)*
5. THE respons SHALL tidak memuat NIK plaintext, token, atau payload provider.

## Requirement 2 — Page Token untuk Mobile WebView

**User Story:** Sebagai pengguna mobile, saya ingin membuka halaman bayar web di WebView dengan token sekali pakai, agar visitor token tidak bocor ke URL.

Trace: SRS-F-18-01, SRS-F-18-02, SRS-Q-SEC-05 · TC-MOB-01, TC-MOB-02, TC-SEC-01

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/payments/{bookingId}/page-tokens` dengan visitor token pemilik dan booking menunggu pembayaran, THE service SHALL menerbitkan `201 { pageToken, pageUrl, expiresAt }` (TTL `PAYMENT_PAGE_TOKEN_TTL_SECONDS`, default 60, rentang 30–120) dan menyimpan **hanya** `token_hash` + `visitor_id` di `payment_page_tokens`.
2. WHEN `POST /api/v1/payment-page-sessions` dengan `pageToken` yang valid, belum dipakai, dan belum kedaluwarsa, THE service SHALL menandai `used_at`, menyetel cookie `rb_pay` (HttpOnly, Secure, SameSite=Lax), dan membalas `204`.
3. IF token sudah dipakai / kedaluwarsa / tidak dikenal, THEN THE service SHALL menolak `403 PAYMENT_PAGE_TOKEN_INVALID` (replay ditolak).
4. IF booking tidak menunggu pembayaran saat token diminta, THEN `409 BOOKING_NOT_PAYABLE`.

## Requirement 3 — Create Payment Attempt (Charge)

**User Story:** Sebagai pembeli, saya ingin memilih metode lalu menekan "Bayar Sekarang", agar mendapat instruksi pembayaran untuk nominal pesanan yang terkunci.

Trace: SRS-F-14-03..07, SRS-F-19-01, SRS-IF-20, SRS-IF-21, SRS-F-26-05 · TC-PAY-01, TC-PAY-02, TC-PAY-05, TC-PAY-08, TC-PAY-10, TC-EDT-04 · invarian backend #11

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/payments/{bookingId}/attempts` dengan `Idempotency-Key` (16–64) dan `method ∈ {VA_BCA, VA_BNI, VA_BRI, VA_PERMATA, QRIS, GOPAY}`, THE service SHALL memanggil `POST /internal/v1/bookings/{id}/payment-lock` **lebih dulu**, dan hanya bila `outcome = OK` membuat transaksi di gateway dengan `gross_amount = amountMinor` versi terkunci (SRS-F-14-03/04, invarian #11).
2. IF `payment-lock` mengembalikan `BOOKING_NOT_PAYABLE` atau `PAYMENT_EXPIRED`, THEN THE service SHALL menolak `409 BOOKING_NOT_PAYABLE` tanpa memanggil gateway.
3. THE service SHALL menyimpan attempt `CREATED` (append `AttemptCreated`) dengan `provider_order_id = RB-{publicRef}-{attemptNo}` **sebelum** charge; paling banyak satu attempt terbuka per booking (`uq_attempt_open_per_booking`).
4. THE expiry transaksi di gateway SHALL tidak melewati `paymentExpiresAt` booking (`custom_expiry` dihitung dari sisa waktu) (SRS-IF-21, TC-PAY-02).
5. WHEN charge berhasil, THE service SHALL append `InstructionIssued` (status `PENDING`, instruksi `VA`/`QR`/`DEEPLINK`) dan membalas `201 { attemptNo, method, status, bookingVersion, expiresAt, instruction }`.
6. WHEN request diulang dengan `Idempotency-Key` sama dan `request_hash` sama, THE service SHALL mengembalikan respons tersimpan tanpa charge ulang; key sama dengan payload berbeda → `409 IDEMPOTENCY_KEY_REUSED`.
7. IF `method` tidak aktif, THEN `422 PAYMENT_METHOD_UNAVAILABLE`.
8. IF gateway timeout/error ambigu, THEN THE service SHALL menandai attempt `UNKNOWN`, **tidak** membuat order baru, merekonsiliasi order yang sama lewat status check, dan membalas `503 PAYMENT_PROVIDER_UNAVAILABLE` (SRS-F-14-05, TC-PAY-05).
9. WHILE breaker charge OPEN, THE service SHALL menolak cepat `503 PAYMENT_PROVIDER_UNAVAILABLE` tanpa memanggil gateway; IF booking belum pernah menerima instruksi pembayaran, THEN THE service SHALL meminta `POST /internal/v1/bookings/{id}/deadline-extension` sekali (`reason = PROVIDER_UNAVAILABLE`) (SRS-F-19-01, TC-PAY-08).
10. WHEN pembeli mengganti metode saat ada attempt terbuka, THE service SHALL membatalkan attempt lama di gateway (`cancel`/`expire`, status `CANCELLED`) lalu membuat attempt baru; TTL booking tidak di-reset (SRS-F-14-06, TC-PAY-10).
11. Memilih metode saja (tanpa menekan bayar / tanpa POST attempts) SHALL tidak mengunci pesanan (SRS-F-14-04).

## Requirement 4 — Webhook Midtrans

**User Story:** Sebagai sistem, saya ingin memproses notifikasi Midtrans secara aman dan idempoten, agar status pembayaran akurat dan tidak pernah mundur.

Trace: SRS-F-15-01..03, SRS-C-21, SRS-IF-20 · TC-PAY-05, TC-PAY-11, TC-MOB-03

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/webhooks/midtrans`, THE service SHALL memverifikasi `signature_key = SHA512(order_id + status_code + gross_amount + MIDTRANS_SERVER_KEY)` secara constant-time **sebelum** memproses; salah/kosong → simpan callback (`signature_valid=false`, `processing_result=REJECTED`) dan `401 WEBHOOK_SIGNATURE_INVALID`.
2. THE service SHALL juga memverifikasi order dikenal, `gross_amount` = `amount_minor` attempt, lingkungan (sandbox/production) cocok, dan transisi status legal; kegagalan → `REJECTED`/`ERROR` tanpa perubahan state (SRS-F-15-01).
3. THE service SHALL memperlakukan signature terverifikasi sebagai **satu-satunya** bukti pembayaran; redirect, WebView return URL, dan bridge event tidak mengubah state (SRS-F-15-03).
4. WHEN callback `(provider, fingerprint)` sudah pernah diterima, THE service SHALL `INSERT … ON CONFLICT DO NOTHING`, menandai `DUPLICATE`, membalas `200`, tanpa efek ganda — tetapi progres status yang sah tetap diterapkan (SRS-F-15-02).
5. THE service SHALL memetakan status: `pending → PENDING`; `settlement` (dan `capture` + `fraud_status=accept` bila kartu aktif — di luar baseline) `→ SUCCEEDED` + `settlement_time` (dikonversi ke UTC); `deny`/`failure → FAILED`; `cancel → CANCELLED`; `expire → EXPIRED`; `refund`/`partial_refund`/`chargeback` → dicatat + alert Ops (di luar MVP).
6. WHEN callback datang out-of-order, THE service SHALL tidak menurunkan status final (`SUCCEEDED`, `FAILED`, `CANCELLED`, `EXPIRED`, `REFUNDED`).
7. THE service SHALL menyimpan callback ke `payment_callbacks` secara durable **sebelum** membalas 2xx.

## Requirement 5 — Settlement & Publikasi Event

**User Story:** Sebagai Booking Service, saya ingin menerima `payment.succeeded` saat pembayaran settle, agar booking dapat diselesaikan berdasarkan waktu settlement.

Trace: SRS-F-16-01, SRS-F-16-03, SRS-Q-REL-04 · TC-PAY-03, TC-PAY-06, TC-CAP-03

### Acceptance Criteria (EARS)

1. WHEN attempt menjadi `SUCCEEDED`, THE service SHALL dalam satu transaksi append `SettlementReceived` (`settlementTime`, `providerTxId`), memproyeksikan read model, dan menulis outbox `payment.succeeded.v1 { bookingId, attemptId, settlementTime, amountMinor, method }` (SRS-F-16-01).
2. WHEN attempt menjadi `FAILED`, THE service SHALL menulis outbox `payment.failed.v1 { bookingId, attemptId, reason }`.
3. THE service SHALL tidak menilai ketepatan waktu; `payment.succeeded` tetap diterbitkan untuk settlement terlambat sehingga Booking dapat membuat `REVIEW` (SRS-F-16-03, TC-PAY-06/07).
4. THE outbox relay SHALL menerbitkan baris pending ke `rusuh.events` dengan publisher confirm (`pkg/outbox`, platform R3); broker down tidak menghilangkan event.
5. THE payload event, outbox, dan log SHALL tidak memuat data kartu, `MIDTRANS_SERVER_KEY`, token, atau payload provider mentah (ditolak `pkg/eventstore` untuk event store).

## Requirement 6 — Status Pembayaran

**User Story:** Sebagai pembeli di halaman bayar, saya ingin melihat status attempt terkini, agar tahu kapan pembayaran berhasil.

Trace: SRS-F-14-01, SRS-F-25-08 · TC-PAY-01

### Acceptance Criteria (EARS)

1. WHEN `GET /api/v1/payments/{bookingId}/status` oleh pemilik, THE service SHALL mengembalikan `200 { bookingId, bookingStatus, attempt, serverTime }` dari read model (klien polling tiap 3 s).
2. THE endpoint SHALL read-only; status tidak pernah berubah karena aksi klien.

## Requirement 7 — Expiry Attempt & Resolve untuk Booking

**User Story:** Sebagai Booking Service saat TTL Pembayaran habis atau pembeli membatalkan, saya ingin Payment memastikan dan membatalkan transaksi di gateway, agar stok tidak dilepas sebelum status pembayaran pasti.

Trace: SRS-F-13-02, SRS-F-17-01..03, SRS-Q-REL-05, SRS-IF-34 · TC-PAY-04, TC-PAY-08, TC-PAY-09

### Acceptance Criteria (EARS)

1. WHEN `POST /internal/v1/payments/{bookingId}/resolve` dengan service token dan `mode = CHECK`, THE service SHALL mengambil status attempt terbuka/terakhir dari gateway dan membalas `{ outcome ∈ {SUCCEEDED, NOT_PAID, NO_ATTEMPT, UNKNOWN}, settlementTime?, attemptId? }`.
2. WHEN `mode = CHECK_AND_CANCEL` dan belum dibayar, THE service SHALL membatalkan di gateway (`cancel`/`expire`), lalu `CHECK` ulang: `SUCCEEDED` bila ternyata terbayar di sela proses; `NOT_PAID` bila pembatalan terkonfirmasi; `UNKNOWN` bila gateway tidak menjawab.
3. WHEN resolve menemukan `SUCCEEDED`, THE service SHALL menerapkan settlement seperti R5.1 (bila belum) dan mengembalikan `settlementTime`; **tidak** membuka refund case (Booking yang menilai tepat waktu; refund case lewat R8) (Δ4).
4. WHILE breaker status OPEN atau gateway timeout, THE service SHALL mengembalikan `outcome = UNKNOWN` cepat (Booking menahan stok `VERIFYING` dan mencoba lagi).
5. IF booking tidak punya attempt, THEN `outcome = NO_ATTEMPT`.
6. WHEN attempt `PENDING` melewati `expires_at` tanpa webhook, THE expiry sweeper SHALL `CHECK` gateway lebih dulu; settle → R5.1; belum → append `AttemptExpired` dan batalkan di gateway (lewat breaker); attempt final di-skip.
7. THE endpoint `/internal/v1/*` SHALL menolak request tanpa service token valid (`401`) dan tidak dapat diakses dari ALB publik.

## Requirement 8 — Refund Case dari `booking.review`

**User Story:** Sebagai Ops, saya ingin kasus refund dibuka otomatis saat pembayaran diterima tetapi tidak menghasilkan tiket, agar dana dapat dikembalikan manual lewat dashboard Midtrans.

Trace: SRS-F-17-05, SRS-F-90-03 · TC-PAY-07

### Acceptance Criteria (EARS)

1. WHEN mengonsumsi `booking.review.v1 { bookingId, attemptHint?, buyerEmail, amountMinor }` (queue `payment.booking-review`), THE service SHALL membuka `refund_cases` untuk attempt `SUCCEEDED` booking itu dan menandai attempt `REFUND_REQUIRED`, idempoten via `refund_cases.attempt_id UNIQUE` + `ON CONFLICT DO NOTHING` (tanpa tabel inbox).
2. THE `reason` SHALL `LATE_SETTLEMENT` bila `settlement_time` > deadline booking, `SETTLED_AFTER_CANCEL` bila booking sudah `CANCELLED`/`EXPIRED`, `DUPLICATE_PAYMENT` bila booking sudah `PAID` oleh attempt lain. *(Pemetaan alasan dari payload yang tersedia — `booking.review.v1` tidak membawa alasan; Payment menurunkannya dari datanya sendiri [asumsi desain, lihat design §8.2].)*
3. IF payload rusak, THEN consumer SHALL `rabbitmq.Permanent(err)` → DLQ.
4. THE refund case SHALL dapat dilihat Ops dan ditutup manual (`REFUNDED`/`CLOSED`, `resolved_by`); refund dilakukan di dashboard Midtrans, bukan oleh service (SRS-F-90-03).

## Requirement 9 — Circuit Breaker & Degradasi

**User Story:** Sebagai sistem, saya ingin setiap panggilan ke Midtrans dilindungi breaker dan bulkhead, agar gangguan gateway tidak menumpuk request dan pembeli mendapat pesan yang jelas.

Trace: SRS-F-19-01..03, D-40 · TC-RES-01, TC-RES-02, TC-PAY-08

### Acceptance Criteria (EARS)

1. THE service SHALL membungkus charge, status check, dan cancel Midtrans dengan `pkg/resilience.Breaker` + `Bulkhead` memakai konfigurasi awal PROPOSED SDD-RBT §4.6.2 (charge: timeout 3 s, ambang 5, cooldown 20 s, probe 1, bulkhead 20, tanpa blind retry; status: timeout 2 s, ambang 5, cooldown 15 s, probe 1, bulkhead 10, retry maks 2), dapat diubah lewat env.
2. Penolakan bisnis 4xx dari gateway SHALL tidak dihitung sebagai kegagalan breaker; `context.Canceled` SHALL tidak membuka breaker.
3. WHILE breaker OPEN, THE panggilan SHALL gagal cepat dengan fallback terdefinisi (R3.9 untuk charge, R7.4 untuk resolve).
4. Perubahan state breaker SHALL dilaporkan sebagai metrik `breaker_state{name}` (platform R4.6).

## Requirement 10 — Simulator Pembayaran

**User Story:** Sebagai developer/QA, saya ingin mensimulasikan hasil gateway tanpa Midtrans nyata, agar CI dan uji offline dapat menjalankan semua skenario pembayaran.

Trace: SRS-IF-24, SRS-C-03 · TC-PAY-03..11 (CI)

### Acceptance Criteria (EARS)

1. WHERE `PAYMENT_PROVIDER = simulator`, THE service SHALL memakai adapter `IPaymentGateway` simulator dengan kontrak perilaku sama seperti Midtrans (charge, status, cancel, webhook bertanda tangan).
2. WHEN `POST /api/v1/local-simulator/payments/{attemptId}/scenario` dengan `scenario ∈ {SETTLE, SETTLE_LATE, EXPIRE, DENY, TIMEOUT, DUPLICATE_WEBHOOK, OUT_OF_ORDER}`, THE simulator SHALL mendorong webhook sintetis dan membalas `202`.
3. THE simulator SHALL hanya aktif pada lingkungan yang diizinkan; di lingkungan lain endpoint membalas `404 NOT_FOUND`. *(Kontrak: `APP_ENV=local|ci`; SRS-IF-24: `ci|local-offline` — OQ-20.)*
4. THE hasil dari simulator SHALL berlabel `SIMULATED` (mis. `provider = SIMULATOR` di attempt) dan tidak pernah dipakai di demo Midtrans (SRS-C-03).

## Requirement 11 — Health, Konfigurasi & Observability

**User Story:** Sebagai operator, saya ingin endpoint health, konfigurasi tervalidasi, dan metrik pembayaran, agar gangguan gateway cepat terdeteksi.

Trace: D-23, D-24, SRS-Q-MNT-02, SRS-Q-SEC-03 · TC-OBS-01, TC-SEC-03

### Acceptance Criteria (EARS)

1. WHEN `GET /health`, THE service SHALL mengembalikan `200 UP`/`503 DOWN` berdasarkan PostgreSQL & RabbitMQ dalam `HEALTH_TIMEOUT`. ✅ *Terimplementasi.*
2. THE config SHALL divalidasi saat start (fail fast) — subset sudah ✅; harus diperluas untuk `PAYMENT_PROVIDER`, `MIDTRANS_*`, `PAYMENT_METHODS_ENABLED`, `PAYMENT_PAGE_TOKEN_TTL_SECONDS`, `BOOKING_SERVICE_URL`, kunci token, nilai breaker.
3. THE service SHALL mengekspos metrik `rbt_payment_attempts_total{method,status}`, `rbt_payment_webhooks_total{status,result}`, `rbt_payment_settlements_total{method}`, latensi Midtrans per operasi, dan state breaker; alert `PaymentCircuitOpen`, `PaymentWebhookErrorRate`, `PaymentSettlementLag`, `PaymentRefundCasesOpen`.
4. Log SHALL tidak memuat NIK, email, server key, token, signature, atau payload provider mentah.

---

## Invarian (tidak boleh dilanggar)

| # | Invarian | Penegak |
|---|----------|---------|
| I-1 | Signature terverifikasi = satu-satunya bukti pembayaran | R4.1–4.3 |
| I-2 | ≤ 1 attempt terbuka per booking; retry via Idempotency-Key, bukan charge baru | `uq_attempt_open_per_booking`, R3.3, R3.6, R3.8 |
| I-3 | Webhook idempoten & status final tidak mundur | `UNIQUE(provider,fingerprint)`, R4.4–4.6 |
| I-4 | Status hanya dari webhook/resolve, bukan klien | R6.2 |
| I-5 | Event store append-only | trigger `trg_payment_events_*` |
| I-6 | Perubahan uang ⇒ outbox dalam transaksi yang sama | R5.1, R5.2 |
| I-7 | Tanpa PII/secret di event/outbox/log | R5.5, R11.4 |
| I-8 | Refund case idempoten tanpa inbox | R8.1 |
| I-9 | Tidak ada transaksi gateway sebelum `payment-lock` OK; nominal = versi terkunci | R3.1, R3.2 (invarian backend #11) |

## Build & Verify

```bash
make infra keys
cd services/payment && cp .env.example .env    # PAYMENT_PROVIDER=simulator
go run ./cmd migrate up && go run ./cmd api & go run ./cmd worker &
curl -fsS localhost:8083/health
make build && make lint && make test && make test-integration && make cover && make ci
```

## Traceability Matrix

| Req | SRS | Test Case |
|-----|-----|-----------|
| 1 Payment Page | F-14-01/02, F-19-02, Q-SEC-01 | TC-PAY-01, TC-PAY-10, TC-SEC-03 |
| 2 Page Token | F-18-01/02, Q-SEC-05 | TC-MOB-01/02, TC-SEC-01 |
| 3 Create Attempt | F-14-03..07, F-19-01, IF-20/21 | TC-PAY-01/02/05/08/10, TC-EDT-04 |
| 4 Webhook | F-15, C-21 | TC-PAY-05, TC-PAY-11, TC-MOB-03 |
| 5 Settlement/Publish | F-16-01/03, Q-REL-04 | TC-PAY-03, TC-PAY-06, TC-CAP-03 |
| 6 Status | F-14-01, F-25-08 | TC-PAY-01 |
| 7 Expiry/Resolve | F-13-02, F-17-01..03, Q-REL-05 | TC-PAY-04, TC-PAY-08, TC-PAY-09 |
| 8 Refund case | F-17-05, F-90-03 | TC-PAY-07 |
| 9 Breaker | F-19, D-40 | TC-RES-01/02, TC-PAY-08 |
| 10 Simulator | IF-24, C-03 | CI |
| 11 Health/Obs | D-23, Q-MNT-02 | TC-OBS-01, TC-SEC-03 |
