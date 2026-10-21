# Implementation Plan: Payment Service

## Overview

Membangun Payment Service di atas boilerplate `services/payment`: agregat PaymentAttempt event-sourced,
adapter gateway (simulator dulu, lalu Midtrans Sandbox), payment page + WebView token, charge yang
selalu didahului `payment-lock`, webhook idempoten, resolve untuk Booking, refund case dari
`booking.review`, dan worker (relay, expiry, reconciler UNKNOWN).

Simulator dibangun **sebelum** Midtrans agar seluruh alur dapat diuji di CI tanpa jaringan.

Prasyarat lintas spec: platform task 2 (middleware + service token), 3 (`pkg/outbox`), 6.1 (kunci);
booking task 9 (`payable`, `payment-lock`, `deadline-extension`) untuk task 4 di sini.

## Tasks

- [x] 0. Fondasi yang sudah ada (audit 2026-10-21)
  - Migrasi `0001_init` + `0002_event_store`, Cobra verb, config subset, composition root, `GET /health`
  - `pkg/resilience`, `pkg/eventstore`, `pkg/apperror` tersedia
  - _Requirements: 11.1_

- [ ] 1. Fondasi domain, port, konfigurasi
  - [ ] 1.1 Agregat `PaymentAttempt` (`decide()` + apply event, design §4), VO `Method`, `AttemptStatus`, `Instruction`, `OrderID`, `Money`; error domain + pemetaan HTTP (design §2.10)
    - _Requirements: 4.5, 4.6_
    - _Design: §4, §2.10_
  - [ ] 1.2 Port inbound/outbound (design §1) + `make mock`
    - _Requirements: 10.1_
  - [ ] 1.3 Config: `PAYMENT_PROVIDER`, `MIDTRANS_*`, `PAYMENT_METHODS_ENABLED`, `PAYMENT_PAGE_TOKEN_TTL_SECONDS`, `BOOKING_SERVICE_URL`, kunci token, breaker per tabel design §6 dengan validasi fail-fast
    - _Requirements: 11.2, 9.1_
    - _Design: §6, §10_
    - _Depends on: platform 6.1_
  - Catatan: format `order_id` bergantung **OQ-21** (`publicRef` di `Payable`)

- [ ] 2. Adapter PostgreSQL
  - [ ] 2.1 Event store (`pkg/eventstore`, `payment_events`) + proyeksi `payment_attempts` + `stream_version` dalam `IUnitOfWork`
    - _Requirements: 5.1, 5.5_
    - _Design: §3, §4_
  - [ ] 2.2 `ICallbackStore` (fingerprint, `ON CONFLICT DO NOTHING`), `IPageTokenStore` (hash, sekali pakai atomik), `IIdempotencyStore`, `IRefundCaseStore`
    - _Requirements: 2.1, 2.2, 3.6, 4.4, 4.7, 8.1_
  - [ ] 2.3 Integration test: append-only ditolak, `uq_attempt_open_per_booking` di bawah paralel, `ErrConcurrency`
    - **Property 1: At most one open attempt**
    - _Requirements: 3.3_

- [ ] 3. Gateway adapter: simulator
  - [ ] 3.1 `IPaymentGateway` simulator: charge/status/cancel + webhook bertanda tangan (kunci lokal), label `provider = SIMULATOR`
    - _Requirements: 10.1, 10.4_
    - _Design: §5.4_
  - [ ] 3.2 Endpoint `POST /api/v1/local-simulator/payments/{attemptId}/scenario` (7 skenario), 404 di luar lingkungan yang diizinkan — lingkungan: **OQ-20**
    - _Requirements: 10.2, 10.3_

- [ ] 4. Klien Booking internal
  - [ ] 4.1 `IBookingClient`: `payable`, `payment-lock` (`attemptKey = Idempotency-Key`), `deadline-extension`; service token + breaker `booking_internal`
    - _Requirements: 3.1, 3.2, 3.9_
    - _Depends on: platform 2.3, booking 9_

- [ ] 5. Payment page & WebView token
  - [ ] 5.1 `GET /api/v1/payments/{bookingId}/page`: data dari `payable`, `methods` dari `PAYMENT_METHODS_ENABLED` + penanda breaker OPEN, `409 BOOKING_NOT_PAYABLE`
    - _Requirements: 1.1, 1.2, 1.3, 1.5_
    - _Design: §2.1_
  - [ ] 5.2 Kepemilikan (visitor token / `rb_pay`) — kode error bukan pemilik **OQ-19**
    - _Requirements: 1.4_
  - [ ] 5.3 `POST page-tokens` (hash saja, TTL) + `POST /api/v1/payment-page-sessions` (cookie `rb_pay`, sekali pakai, `403 PAYMENT_PAGE_TOKEN_INVALID`)
    - _Requirements: 2.1, 2.2, 2.3, 2.4_
  - [ ] 5.4 Test TC-MOB-01, TC-SEC-01 (replay page token)
    - _Requirements: 2.3_

- [ ] 6. Create attempt (charge)
  - [ ] 6.1 Usecase design §9.1: idempotency → `payment-lock` → (cancel attempt terbuka lama) → `AttemptCreated` → charge di luar tx → `InstructionIssued` / `AttemptUnknown`
    - _Requirements: 3.1, 3.2, 3.3, 3.5, 3.6, 3.10, 3.11_
    - _Design: §9.1_
    - _Depends on: 2.1, 3.1, 4.1_
  - [ ] 6.2 `custom_expiry` dari `paymentExpiresAt`; validasi metode (`422`)
    - **Property 5: Gateway expiry within booking deadline**
    - _Requirements: 3.4, 3.7_
  - [ ] 6.3 Breaker charge OPEN → tutup attempt `CREATED`, `deadline-extension` sekali bila belum ada instruksi, `503`
    - _Requirements: 3.9_
  - [ ] 6.4 Integration test: lock-before-charge, retry key sama tanpa charge ulang, `IDEMPOTENCY_KEY_REUSED`, ganti metode (TC-PAY-01, TC-PAY-10, TC-EDT-04)
    - **Property 2: No charge before lock**, **Property 1: At most one open attempt**
    - _Requirements: 3.1, 3.6, 3.10_

- [ ] 7. Webhook
  - [ ] 7.1 Handler `POST /api/v1/webhooks/midtrans`: signature constant-time, simpan callback durable, dedup fingerprint, validasi order/amount/environment/transisi, pemetaan status design §5.3, outbox untuk uang
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 5.1, 5.2_
    - _Design: §5.2, §5.3, §9.2_
  - [ ] 7.2 Integration test duplikat, out-of-order, signature salah/kosong (TC-PAY-05, TC-PAY-11), bridge/return URL tidak mengubah state (TC-MOB-03), settlement terlambat tetap menerbitkan `payment.succeeded.v1` (skenario `SETTLE_LATE`, TC-PAY-06/07)
    - **Property 3: Signature is the only proof**, **Property 4: Webhook idempotency & monotonic status**
    - _Requirements: 4.1, 4.3, 4.4, 4.6, 5.3_

- [ ] 8. Status, resolve, worker
  - [ ] 8.1 `GET /api/v1/payments/{bookingId}/status` read-only
    - _Requirements: 6.1, 6.2_
  - [ ] 8.2 `POST /internal/v1/payments/{bookingId}/resolve` design §9.3 (`CHECK` / `CHECK_AND_CANCEL`, tanpa refund case), service token wajib
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.7_
    - _Depends on: platform 2.3_
  - [ ] 8.3 Worker: outbox relay (`pkg/outbox`), attempt expiry sweeper (CHECK dulu), UNKNOWN reconciler
    - **Property 6: No duplicate order on ambiguous timeout**
    - _Requirements: 5.4, 7.6, 3.8_
    - _Design: §7_
    - _Depends on: platform 3_
  - [ ] 8.4 Integration test resolve: belum bayar → `NOT_PAID`; settle di sela cancel → `SUCCEEDED`; breaker OPEN → `UNKNOWN` cepat (TC-PAY-04, TC-PAY-08, TC-PAY-09)
    - **Property 8: Resolve never blocks**
    - _Requirements: 7.2, 7.4_

- [ ] 9. Refund case & rekonsiliasi
  - [ ] 9.1 Consumer `payment.booking-review` (`booking.review.v1`): refund case idempoten, `RefundRequired`, penurunan `reason` (design §8.2), payload rusak → `Permanent`
    - _Requirements: 8.1, 8.2, 8.3_
    - _Design: §8.2_
  - [ ] 9.2 Penutupan kasus oleh Ops (`REFUNDED`/`CLOSED`, `resolved_by`) lewat tool internal/SQL teraudit
    - _Requirements: 8.4_
  - [ ] 9.3 Pemeriksaan rekonsiliasi milik Payment untuk `tools/reconcile` (SUCCEEDED tanpa outbox terbit, attempt terbuka melewati expiry, refund case OPEN lama)
    - _Depends on: platform 9.1_
    - _Requirements: 8.4, 5.4_
  - [ ] 9.4 Test kirim ganda `booking.review` (TC-PAY-07)
    - **Property 7: Refund case idempotent**
    - _Requirements: 8.1_

- [ ] 10. Checkpoint — alur lengkap dengan simulator
  - TC-PAY-01..11 lulus di compose dengan `PAYMENT_PROVIDER=simulator`; `make ci` hijau; tanyakan ke user untuk OQ-19/20/21

- [ ] 11. Adapter Midtrans Sandbox
  - [ ] 11.1 Verifikasi detail Core API terhadap dokumentasi resmi Midtrans (OI-05): endpoint, `custom_expiry` minimal, payload per metode, zona waktu `settlement_time`
    - _Requirements: 3.4, 4.5_
  - [ ] 11.2 Adapter `midtrans`: charge/status/cancel lewat breaker `midtrans_charge`/`midtrans_status` + bulkhead; 4xx bisnis tidak dihitung kegagalan
    - _Requirements: 9.1, 9.2, 9.3_
    - _Design: §5, §6_
  - [ ] 11.3 Uji sandbox manual (SDD §4.6.3 "Skenario Uji Sandbox") + evidence
    - _Requirements: 3.5, 4.1_
  - [ ] 11.4 Test breaker TC-RES-01/02 di adapter (OPEN → gagal cepat, HALF_OPEN satu probe)
    - _Requirements: 9.2, 9.3, 9.4_

- [ ] 12. Observability & keamanan
  - [ ] 12.1 Metrik + alert design §11 via `pkg/telemetry`
    - _Requirements: 11.3, 9.4_
    - _Depends on: platform 4.2_
  - [ ] 12.2 Log scan: tidak ada server key, token, signature, payload provider (TC-SEC-03)
    - _Requirements: 11.4, 5.5_

- [ ] 13. Final checkpoint
  - Property 1–8 punya test lulus; contract test `payment.yaml` + `internal-payment.yaml`; evidence sandbox tersimpan
  - Tanyakan ke user untuk OQ yang masih terbuka

## Notes

- Payment **tidak** memutuskan PAID/REVIEW — hanya melaporkan `settlementTime`; Booking yang menilai.
- Tidak ada panggilan gateway/Booking di dalam transaksi DB.
- Tidak ada retry buta pada charge; timeout ambigu selalu direkonsiliasi ke order yang sama.
