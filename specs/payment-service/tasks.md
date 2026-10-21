# Implementation Plan: Payment Service

Owner: **kamu**. Penomoran Task 0–13 DEV-2 dipertahankan; isinya diselaraskan dengan
`requirements.md`/`design.md` terbaru. Kode bisnis Payment belum ada (baru boilerplate + migrasi).

Prinsip implementasi supaya kode mudah dijelaskan:
- satu usecase per file;
- respons hanya berisi field yang ada di kontrak `payment.yaml`;
- error memakai format ringkas platform 2.6, detail cukup di log;
- simulator dikerjakan sebelum Midtrans (Task 12 dulu, lalu Task 3), supaya semua alur bisa diuji tanpa jaringan.

Blocker dari pihak lain: booking Task 13 (`payable`, `payment-lock`, `deadline-extension`) milik DEV-2.
Sambil menunggu, pakai klien Booking palsu di test.

## Tasks

- [ ] 0. Config + plumbing bersama
  - [ ] 0.1 Config: `PAYMENT_PROVIDER` (simulator/midtrans), `MIDTRANS_*`, `PAYMENT_METHODS_ENABLED`, `PAYMENT_PAGE_TOKEN_TTL_SECONDS`, `BOOKING_SERVICE_URL`, kunci token, nilai breaker (design §6) — fail-fast
    - _Requirements: 11.2, 9.1_
  - [ ] 0.2 Domain attempt (status + transisi), VO metode/instruksi/order ID, error domain
    - _Requirements: 4.5, 4.6_
  - [ ] 0.3 Event store (`payment_events`) + proyeksi `payment_attempts`, idempotency store, callback store, refund store
    - **Property 1: At most one open attempt**
    - _Requirements: 3.3, 5.1, 5.5_
  - [ ] 0.4 Klien Booking internal (`payable`, `payment-lock`, `deadline-extension`) dengan service token + breaker
    - _Depends on: platform 2.3, booking 13_
    - _Requirements: 3.1, 3.2_
  - _Requirements: 11.1_

- [ ] 1. Payment page `GET /api/v1/payments/{bookingId}/page`
  - Data dari `payable`; metode dari `PAYMENT_METHODS_ENABLED`; metode yang breaker-nya OPEN ditandai gangguan
  - Bukan pemilik: `404 NOT_FOUND` (OQ-19, konsisten dengan Booking)
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5_

- [ ] 2. Page token untuk WebView
  - `POST page-tokens` (simpan hash saja) + `POST /api/v1/payment-page-sessions` (cookie `rb_pay`, sekali pakai)
  - _Requirements: 2.1, 2.2, 2.3, 2.4_

- [ ] 3. Buat attempt (charge) + event sourcing
  - Urutan: idempotency → `payment-lock` → tutup attempt lama bila ganti metode → simpan attempt `CREATED` → charge (di luar transaksi) → `PENDING` atau `UNKNOWN`
  - Expiry di gateway tidak boleh melewati `paymentExpiresAt`; order ID `RB-{publicRef}-{attemptNo}` (OQ-21)
  - _Depends on: 12, 0.4_
  - **Property 2: No charge before lock**, **Property 5: Gateway expiry within booking deadline**, **Property 6: No duplicate order on ambiguous timeout**
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.10, 3.11_

- [ ] 4. Webhook + verifikasi signature
  - Signature SHA-512 constant-time; cek order, nominal, lingkungan; simpan callback sebelum balas 200; duplikat → 200 tanpa efek; status final tidak mundur
  - **Property 3: Signature is the only proof**, **Property 4: Webhook idempotency & monotonic status**
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7_

- [ ] 5. Settlement & publish event + relay outbox
  - `SettlementReceived` → outbox `payment.succeeded`; gagal → `payment.failed`; settle terlambat tetap diterbitkan (Booking yang memutuskan REVIEW)
  - Relay memakai `pkg/outbox`
  - _Depends on: platform 3_
  - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5_

- [ ] 6. Status `GET /api/v1/payments/{bookingId}/status` (read-only)
  - Usulan respons ringkas ke FE dicatat di OQ-22; implementasi awal ikuti kontrak
  - _Requirements: 6.1, 6.2, 6.3_

- [ ] 7. Expiry sweeper & resolve
  - `POST /internal/v1/payments/{bookingId}/resolve` (`CHECK` / `CHECK_AND_CANCEL`), breaker OPEN → `UNKNOWN`; resolve tidak membuka refund case
  - Sweeper attempt: cek gateway dulu, baru expire/cancel; reconciler untuk attempt `UNKNOWN`
  - **Property 8: Resolve never blocks**
  - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7_

- [ ] 8. Refund case dari `booking.review`
  - Idempoten lewat `refund_cases.attempt_id UNIQUE`; alasan diturunkan dari data attempt; payload rusak → DLQ
  - **Property 7: Refund case idempotent**
  - _Requirements: 8.1, 8.2, 8.3, 8.4_

- [ ] 9. Wiring circuit breaker + adapter Midtrans
  - Breaker `midtrans_charge` dan `midtrans_status` + bulkhead; 4xx bisnis tidak dihitung gagal
  - Verifikasi detail API Midtrans Sandbox ke dokumentasi resmi sebelum menulis adapter
  - _Requirements: 9.1, 9.2, 9.3, 9.4_

- [ ] 10. Cancel payment
  - Tidak ada endpoint cancel publik di Payment: pembatalan dipicu Booking lewat resolve `CHECK_AND_CANCEL` (Task 7). Ganti metode (Task 3) membatalkan attempt lama
  - Task ini cukup berupa test alur batal: sudah bayar → PAID, belum → CANCELLED (TC-PAY-09)
  - _Requirements: 7.2, 3.10_

- [ ] 11. Keamanan & isolasi
  - `/internal/*` wajib service token; tidak ada data kartu, server key, token, atau payload provider di log/event
  - _Requirements: 7.7, 11.4, 5.5_

- [ ] 12. Simulator (dikerjakan sebelum Task 3)
  - Adapter `simulator` dengan perilaku sama seperti Midtrans + endpoint `/api/v1/local-simulator/payments/{attemptId}/scenario`; hanya aktif di `local`/`ci` (OQ-20), hasil berlabel `SIMULATOR`
  - _Requirements: 10.1, 10.2, 10.3, 10.4_

- [ ] 13. Verifikasi akhir
  - `make ci` hijau; TC-PAY-01..11, TC-RES-01/02 lulus dengan simulator; uji Sandbox manual + evidence
  - Metrik dan alert setelah OQ-5
  - _Depends on: platform 4.2_
  - _Requirements: 11.3_

- [ ] 14. Retensi data (keputusan 2026-10-21)
  - [ ] 14.1 Job purge outbox: hapus baris terkirim > 7 hari
    - _Depends on: platform 13.1_
    - _Requirements: 5.6_

## Notes
- Payment tidak memutuskan PAID/REVIEW; ia hanya melaporkan `settlementTime`.
- Tidak ada panggilan gateway/Booking di dalam transaksi DB, dan tidak ada retry buta pada charge.
