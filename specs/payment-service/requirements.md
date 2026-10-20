# Requirements — Payment Service

| Field | Value |
|-------|-------|
| **Service** | Payment Service |
| **Spec ID** | SPEC-PAYMENT-REQ-001 |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-PAYMENT-001.md` |
| **SRS** | §3.2.13–§3.2.19 (SRS-F-13..19), §Keamanan (SRS-C-21, SRS-Q-SEC-*) |
| **Port** | 8083 |
| **Schema / Role** | `payment` / `svc_payment` |
| **Status implementasi** | **Boilerplate** — lihat §Status Implementasi |

## Status Implementasi (grounding ke kode nyata)

Pembacaan `services/payment/` menunjukkan service masih **boilerplate Sprint 1**. Yang **sudah ada**:

- `cmd/` verb Cobra (`api`, `worker`, `migrate up|down|status`, `healthcheck`).
- `internal/config/{config.go,load.go}` — konfigurasi `APP_*`, `LOG_*`, `POSTGRES_*`, `RABBITMQ_*` tervalidasi fail-fast. **Belum** memuat `MIDTRANS_*`, `PAYMENT_PROVIDER`, `BOOKING_SERVICE_URL`, `TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL`, CB config.
- `internal/delivery/{server.go,worker.go,clients.go}` — composition root; **hanya** mendaftarkan `GET /health`.
- Domain `payment` **hanya** berisi slice health (entity/usecase/handler health). Tidak ada usecase/handler/adapter bisnis payment.
- Migrasi DDL **lengkap dan lebih kaya dari contoh di SDD**: `payment_attempts`, `payment_callbacks`, `payment_page_tokens`, `refund_cases`, `idempotency_records`, `outbox_events` (0001) dan event store `payment_events` + trigger append-only (0002).

Yang **belum ada (target spec ini)**: seluruh endpoint publik/internal payment, usecase, adapter Midtrans/simulator, verifikasi webhook, event sourcing attempt, outbox relay, circuit breaker, worker expiry.

> Karena kode masih boilerplate, kriteria di bawah ditulis sebagai **target** yang di-trace ke SDD/SRS. Tanda **[IMPL]** = sudah ada di kode; **[TARGET]** = belum, harus dibangun.

---

## Ringkasan

Payment Service mengintegrasikan Midtrans (atau simulator lokal), menangani payment page, charge, webhook callback, settlement, expiry, dan refund case. Siklus attempt direkam dengan **event sourcing** (`payment_events` sumber kebenaran, `payment_attempts` read model). Prinsip inti: idempotent webhook, verifikasi signature sebagai **satu-satunya bukti pembayaran**, at-most-once charge, audit trail lengkap.

---

## Kebutuhan

### R1 — Payment Page (SRS-F-14)

**User story:** Sebagai pembeli yang booking-nya terkunci, saya ingin melihat halaman pembayaran (jumlah, batas waktu, item, metode tersedia) agar bisa membayar sebelum TTL habis.

Kriteria (EARS):
- **R1.1** WHEN klien `GET /api/v1/payments/{bookingId}/page` dengan token visitor valid (`X-Visitor-Token` / cookie `rb_visitor` / `rb_pay`), THE service SHALL mengembalikan 200 dengan `amount`, `currency`, `expiresAt`, `serverTime`, `status`, `items`, dan `availableMethods`. _(SDD §3.1; TC-PAY-01, TC-PAY-10)_ **[TARGET]**
- **R1.2** WHERE booking tidak dalam keadaan payable (bukan HELD/LOCKED), THE service SHALL menolak dengan 409 `BOOKING_NOT_PAYABLE`. _(SDD App.B; TC-PAY-10)_ **[TARGET]**
- **R1.3** WHERE TTL booking telah habis, THE service SHALL mengembalikan 410 `BOOKING_EXPIRED`. _(SDD App.B)_ **[TARGET]**
- **R1.4** IF token visitor tidak cocok dengan booking, THEN THE service SHALL menolak 403 `VISITOR_MISMATCH`. _(SDD App.B; TC-SEC-03)_ **[TARGET]**
- **R1.5** THE service SHALL menyamarkan NIK attendee pada item (`attendeeNikMasked`) dan tidak pernah memuat NIK plaintext. _(security-privacy steering; SRS-Q-SEC-*)_ **[TARGET]**

### R2 — Page Token untuk Mobile WebView (SRS-F-18)

**User story:** Sebagai pengguna mobile, saya ingin menukar token sekali pakai menjadi sesi halaman bayar agar WebView dapat membuka halaman dengan aman.

- **R2.1** WHEN `POST /api/v1/payments/{bookingId}/page-tokens`, THE service SHALL menerbitkan token sekali pakai ber-TTL (`PAYMENT_PAGE_TOKEN_TTL`, default 60s) dan menyimpan hanya `token_hash`. _(SDD §3.2, §6.1; TC-MOB-01)_ **[TARGET]**
- **R2.2** WHEN `POST /api/v1/payment-page-sessions` dengan token valid & belum dipakai & belum kedaluwarsa, THE service SHALL menukarnya menjadi cookie `rb_pay` dan menandai `used_at`. _(SDD §3.3; TC-MOB-01)_ **[TARGET]**
- **R2.3** IF token sudah dipakai / kedaluwarsa / tidak dikenal, THEN THE service SHALL menolak 403 `PAYMENT_PAGE_TOKEN_INVALID`. _(SDD App.B; TC-SEC-01 replay)_ **[TARGET]**

### R3 — Create Payment Attempt / Charge (SRS-F-14)

**User story:** Sebagai pembeli, saya ingin memilih metode dan memulai pembayaran agar mendapat instruksi (redirect / QR / VA).

- **R3.1** WHEN `POST /api/v1/payments/{bookingId}/attempts` dengan header `Idempotency-Key` dan `method` yang didukung, THE service SHALL meng-charge provider dan mengembalikan 201 dengan `attemptId`, `status=PENDING`, `instructions`. _(SDD §3.4, §8.1; TC-PAY-01)_ **[TARGET]**
- **R3.2** THE service SHALL memastikan **at-most-once charge**: paling banyak satu attempt terbuka (`CREATED|PENDING|UNKNOWN`) per booking (dijaga `uq_attempt_open_per_booking`). IF sudah ada attempt terbuka, THEN tolak 409 `ATTEMPT_IN_PROGRESS`. _(SDD §6.1, App.B; invariants #5)_ **[IMPL: constraint DB]** **[TARGET: usecase]**
- **R3.3** WHEN request diterima dengan `Idempotency-Key` yang sama dan request-hash cocok, THE service SHALL mengembalikan respons tersimpan tanpa charge ulang. _(SDD §6.3; idempotency steering)_ **[IMPL: tabel]** **[TARGET: usecase]**
- **R3.4** IF `method` tidak didukung, THEN THE service SHALL menolak 400 `PAYMENT_METHOD_UNAVAILABLE`. _(SDD App.B)_ **[TARGET]**
- **R3.5** WHILE sirkuit Midtrans OPEN, THE service SHALL menolak charge dengan `GATEWAY_ERROR`/`ErrCircuitOpen` tanpa memanggil gateway. _(SDD §19, App.C; SRS-F-19; TC-PAY-08)_ **[TARGET]**
- **R3.6** IF provider mengembalikan error/timeout, THEN THE service SHALL mengembalikan 502 `GATEWAY_ERROR` / 504 `GATEWAY_TIMEOUT` dan menaikkan hitung kegagalan circuit breaker. _(SDD App.B,C)_ **[TARGET]**

### R4 — Webhook Midtrans (SRS-F-15, SRS-C-21)

**User story:** Sebagai sistem, saya ingin memproses callback Midtrans secara aman dan idempoten agar status pembayaran akurat.

- **R4.1** WHEN `POST /api/v1/webhooks/midtrans`, THE service SHALL memverifikasi signature `SHA512(order_id + status_code + gross_amount + server_key)` **sebelum** memproses. IF signature tidak cocok/kosong, THEN tolak dengan `WEBHOOK_SIGNATURE_INVALID` dan catat sebagai `signature_valid=false`. _(SDD §3.6, §8.4; SRS-C-21; TC-PAY-11)_ **[TARGET]**
- **R4.2** THE service SHALL memperlakukan **verifikasi signature sebagai satu-satunya bukti pembayaran**; redirect/WebView/bridge bukan bukti. _(security-privacy steering; invariants #5)_ **[TARGET]**
- **R4.3** WHEN callback dengan `fingerprint` yang sudah pernah diproses diterima, THE service SHALL idempoten: tidak ada efek ganda, kembalikan 200, tandai `processing_result=DUPLICATE` (dijaga `UNIQUE(provider,fingerprint)`). _(SDD §1, §6.1; TC-PAY-05 duplicate)_ **[IMPL: constraint]** **[TARGET: usecase]**
- **R4.4** WHEN `transaction_status` dipetakan ke status attempt sesuai App.A.4 (`settlement|capture→SETTLED`, `pending→PENDING`, `deny|cancel|failure→FAILED`, `expire→EXPIRED`), THE service SHALL meng-append event dan memproyeksikan read model dalam satu transaksi. _(SDD §7, App.A.4)_ **[TARGET]**
- **R4.5** WHEN callback datang out-of-order (mis. settlement sebelum pending), THE service SHALL menangani dengan benar (status final tidak di-downgrade). _(SDD §15 matrix)_ **[TARGET]**

### R5 — Settlement & Event Publishing (SRS-F-16)

**User story:** Sebagai Booking Service, saya ingin menerima `payment.succeeded` saat pembayaran settle agar booking diselesaikan.

- **R5.1** WHEN attempt menjadi SETTLED, THE service SHALL menulis event domain + outbox `payment.succeeded` `{bookingId, paymentId, amount, method, paidAt}` dalam transaksi yang sama. _(SDD §9.1, §7; TC-PAY-03, TC-PAY-06)_ **[IMPL: outbox table]** **[TARGET: usecase]**
- **R5.2** WHEN attempt menjadi FAILED, THE service SHALL menulis outbox `payment.failed` `{bookingId, paymentId, reason}`. _(SDD §9.1)_ **[TARGET]**
- **R5.3** THE outbox relay worker SHALL mempublikasikan `outbox_events` yang belum terbit ke RabbitMQ (interval 1s) dengan publisher confirm; status final pembayaran **hanya** berasal dari webhook/rekonsiliasi, bukan dari aksi klien. _(SDD §10.1; invariants #5)_ **[IMPL: table]** **[TARGET: worker]**
- **R5.4** WHEN pembayaran settle tepat sebelum expiry (race), THE service SHALL tetap memproses settlement dan Booking ditandai PAID. _(SDD §15 TC-PAY-06)_ **[TARGET]**

### R6 — Payment Status (SRS-F-14)

- **R6.1** WHEN `GET /api/v1/payments/{bookingId}/status`, THE service SHALL mengembalikan `status`, `paidAt`, `method`, `ticketUrl` dari read model. _(SDD §3.5)_ **[TARGET]**

### R7 — Expiry & Resolve (SRS-F-17)

**User story:** Sebagai Booking Service saat expiry sweep, saya ingin meminta Payment memeriksa & membatalkan attempt agar tidak ada pembayaran menggantung.

- **R7.1** WHEN `POST /internal/v1/payments/{bookingId}/resolve` dengan `action=CHECK_AND_CANCEL`, THE service SHALL memeriksa gateway dan mengembalikan `resolution ∈ {NO_PAYMENT, PAYMENT_FOUND, REFUND_REQUIRED}`. _(SDD §4.1; TC-PAY-04, TC-PAY-07)_ **[TARGET]**
- **R7.2** WHEN attempt PENDING melewati `expires_at` tanpa webhook, THE sweeper SHALL menandai EXPIRED (append `AttemptExpired`). _(SDD §10.2; TC-PAY-04)_ **[TARGET]**
- **R7.3** IF ditemukan pembayaran setelah cancel, THEN THE service SHALL membuka `refund_cases` (`SETTLED_AFTER_CANCEL`) dan kembalikan `REFUND_REQUIRED`. _(SDD §4.1, §6.1; TC-PAY-07)_ **[IMPL: table]** **[TARGET: usecase]**
- **R7.4** WHILE gateway degraded saat resolve, THE service SHALL membuka circuit dan menunda retry alih-alih memblokir. _(SDD §19; TC-PAY-08, TC-RES-01/02)_ **[TARGET]**

### R8 — Refund Cases (SRS-F-17)

- **R8.1** WHEN mengkonsumsi `booking.review` dari Booking, THE service SHALL membuka refund case idempoten (dijaga `refund_cases.attempt_id UNIQUE`, tanpa inbox). _(SDD §9.2, §6.1; code-principles consumer idempoten)_ **[IMPL: constraint]** **[TARGET: consumer]**

### R9 — Circuit Breaker / Degradation (SRS-F-19)

- **R9.1** THE service SHALL membungkus panggilan Midtrans dengan circuit breaker (threshold 3, recovery 60s, half-open 1). WHILE OPEN, panggilan ditolak cepat. _(SDD §19, App.C; TC-RES-01/02)_ **[TARGET]**
- **R9.2** THE service MAY meminta `deadline-extension` ke Booking saat gateway degraded. _(SDD §5.3)_ **[TARGET, SHOULD]**

### R10 — Cancel (SRS-F-13)

- **R10.1** WHEN pembeli membatalkan sebelum bayar, THE service SHALL membatalkan attempt di gateway dan memicu Booking melepas stok. _(SDD §15; TC-PAY-09)_ **[TARGET]**

### R11 — Keamanan & Isolasi (SRS-Q-SEC-*)

- **R11.1** THE endpoint `/internal/v1/*` SHALL hanya dapat diakses dari jaringan internal; akses dari publik ditolak 403. _(TC-SEC-02)_ **[TARGET]**
- **R11.2** THE service SHALL menolak replay token (TC-SEC-01) dan bypass origin check (TC-SEC-03) dengan 403. **[TARGET]**
- **R11.3** THE payload event (`payment_events`, outbox) SHALL tidak memuat data kartu, server key, atau token plaintext (ditolak di layer eventstore). _(SDD §6.2; code-principles)_ **[IMPL: komentar DDL]** **[TARGET: enforcement]**

### R12 — Health & Operasional (D-23)

- **R12.1** WHEN `GET /health`, THE service SHALL mengembalikan 200 UP / 503 DOWN berdasarkan ping Postgres & RabbitMQ. _(SDD §13; D-23)_ **[IMPL]**
- **R12.2** THE konfigurasi SHALL divalidasi saat start (fail fast) untuk `APP_*`, `LOG_*`, `POSTGRES_*`, `RABBITMQ_*`, dan (target) `MIDTRANS_*`/`PAYMENT_PROVIDER`. _(SDD §11)_ **[IMPL: subset]** **[TARGET: Midtrans vars]**

---

## Build & Verify

### Build (run)
```bash
make infra keys
cd services/payment && cp .env.example .env    # set PAYMENT_PROVIDER=simulator
go run ./cmd migrate up
go run ./cmd api &
go run ./cmd worker &
curl -fsS localhost:8083/health
```

### Verify
```bash
make build && make lint                         # modul payment
go test ./... -race                             # unit (payment)
make test-integration                           # adapter: postgres, rabbitmq (build tag integration)
make cover                                       # ambang logika 80% (COVERAGE_MIN)
```
Setiap SRS-F-xx di atas dipetakan ke TC-xx di `docs/test/06-TP-RBT-001.md` (lihat SDD §15). Verifikasi dianggap lulus bila `make ci` hijau dan TC kunci (TC-PAY-01/05/06/08/09/11, TC-SEC-01/02/03, TC-MOB-01/02/03) terpenuhi.

---

## Traceability Matrix

| Req | SRS | TC | Status |
|-----|-----|----|--------|
| R1 Payment Page | SRS-F-14-01..07 | TC-PAY-01, TC-PAY-10 | TARGET |
| R2 Page Token/WebView | SRS-F-18-01..04 | TC-MOB-01, TC-SEC-01 | TARGET |
| R3 Create Attempt | SRS-F-14-04 | TC-PAY-01, TC-PAY-08 | TARGET (DB impl) |
| R4 Webhook + Signature | SRS-F-15, SRS-C-21 | TC-PAY-05, TC-PAY-11 | TARGET (DB impl) |
| R5 Settlement/Publish | SRS-F-16-01..03 | TC-PAY-03, TC-PAY-06 | TARGET (DB impl) |
| R6 Status | SRS-F-14 | TC-PAY-01 | TARGET |
| R7 Expiry/Resolve | SRS-F-17-01..05 | TC-PAY-04, TC-PAY-07, TC-PAY-08 | TARGET (DB impl) |
| R8 Refund Cases | SRS-F-17 | TC-PAY-07 | TARGET (DB impl) |
| R9 Circuit Breaker | SRS-F-19-01..03 | TC-RES-01, TC-RES-02, TC-PAY-08 | TARGET |
| R10 Cancel | SRS-F-13-01..03 | TC-PAY-09 | TARGET |
| R11 Security/Isolasi | SRS-Q-SEC-* | TC-SEC-01/02/03 | TARGET (DB impl) |
| R12 Health/Config | D-23 | — | IMPL (subset) |
