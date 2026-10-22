# Implementation Plan: Notification Service

Owner: **kamu** (inisialisasi kode oleh DEV-2). Penomoran task DEV-2 (0a, 0b, 1–10) dipertahankan;
isinya diselaraskan dengan `requirements.md`/`design.md` terbaru. Sub-task **R** = perbaikan kode yang
sudah ada.

Status 2026-10-21: semua consumer (email booking, push giliran, OTP), API push-subscription, repository
delivery, adapter SMTP/FCM/template sudah ada dan `go test` lulus. Yang perlu diperbaiki:
- **0a.R1** — email yang gagal tidak pernah dikirim ulang (bug klaim delivery).
- **8.R1** — SMTP belum dilindungi breaker.
- **0b.R1** — di lokal push dibuang diam-diam.

## Tasks

- [ ] 0a. Delivery entity, repository, idempotensi
  - Ada: `entity/delivery.go`, `postgres/delivery_repository.go` (`ClaimDelivery`, `MarkSent`, `MarkDead`, `BumpAttempt`)
  - [ ] 0a.R1 **Bug:** `ClaimDelivery` memakai `ON CONFLICT DO NOTHING`. Saat pesan datang lagi dari retry queue, barisnya sudah ada (status `RETRY`) sehingga dianggap duplikat dan email tidak pernah terkirim. Ganti dengan klaim kondisional: boleh lanjut bila status `PENDING`/`RETRY` atau `SENDING` yang sudah basi (design §4.3)
    - **Property 1: At most one send after success**, **Property 2: Retry eventually sends**
    - _Requirements: 8.1, 8.2, 2.2_
  - [ ] 0a.2 Test repository: baris baru → klaim; `RETRY` → klaim; `SENT` → skip
    - _Requirements: 6.1, 6.2_

- [ ] 0b. Config & env (SMTP, FCM, PII, template)
  - Ada: struct `SMTP`, `FCM`, `PII` di `internal/config`
  - [ ] 0b.R1 Push lokal sekarang `noopPush` (dibuang tanpa jejak). Ganti dengan adapter log (`PUSH_PROVIDER=log`) yang mencatat "push terkirim" tanpa token, supaya TC-QUE-13 bisa diamati di lokal/CI (SRS-IF-23)
    - _Requirements: 11.2_
  - _Requirements: 11.1_

- [ ] 0c. Penyedia email dapat dipilih: Mailpit atau Resend (keputusan 2026-10-21)
  - [ ] 0c.1 Config: `EMAIL_PROVIDER` (default `mailpit`), `EMAIL_FROM`, `RESEND_API_KEY`; validasi fail-fast (resend wajib key dan from); peringatan saat start bila resend; `.env.example` sudah memuat variabelnya
    - _Requirements: 13.1, 13.3, 13.8, 13.11_
    - _Design: §13, §14_
  - [ ] 0c.2 Adapter `infrastructure/resend` (net/http, tanpa SDK): kirim, `Idempotency-Key`, simpan `id` sebagai `provider_message_id`, klasifikasi error (429/5xx/jaringan sementara; 400/401/403/422 permanen), key tidak pernah dilog
    - **Property 7: Provider isolation**
    - _Requirements: 13.3, 13.4, 13.5, 13.6, 13.7_
  - [ ] 0c.3 Pilih adapter di `notification_module.go`; test dengan `httptest` (sukses, 429, 403, 422, timeout) dan test bahwa mode mailpit tidak memanggil HTTP
    - _Requirements: 13.2, 13.9, 13.10_
    - _Depends on: 8.R1 (breaker membungkus sender yang dipilih)_
  - Uji manual: `EMAIL_PROVIDER=resend` dengan alamat milikmu sendiri sebagai penerima (domain terverifikasi memungkinkan alamat lain); kembalikan ke `mailpit` setelahnya

- [x] 1. Email e-tiket (`booking.paid`)
  - [ ] 1.1 Test isi email: kode tiket, nama, NIK tersamar, tautan tiket (TC-TKT-02)
    - _Requirements: 1.4_
  - _Requirements: 1.1, 1.2, 1.3_

- [x] 2. Email expired (`booking.expired`)
  - _Requirements: 2.1_

- [x] 3. Email review (`booking.review`)
  - Teks "X hari kerja" masih perlu dikonfirmasi Ops
  - _Requirements: 3.1_

- [x] 4. Push giliran sebentar lagi (`queue.turn.soon`)
  - Catatan: Queue belum pernah mempublikasikan event ini (queue 12.R1), jadi belum bisa diuji end-to-end
  - _Requirements: 4.1, 4.2, 4.3_

- [x] 5. Push giliran tiba (`queue.turn.arrived`) dengan cek deadline
  - **Property 4: Late push discarded**
  - _Requirements: 5.1, 5.2, 5.3_

- [ ] 6. API push subscription
  - Ada: `POST /api/v1/push-subscriptions`, `DELETE /api/v1/push-subscriptions/{eventId}`
  - [ ] 6.R1 Samakan format error dengan standar ringkas (platform 2.6)
    - _Depends on: platform 2.6_
    - _Requirements: 7.4_
  - _Requirements: 7.1, 7.2, 7.3_

- [ ] 7. Email OTP login (tanpa retry)
  - Ada: consumer OTP di koneksi terpisah dengan `MaxRetries=0`, AAD dekripsi cocok dengan User
  - OTP tidak dicatat di `deliveries` (skema tidak mendukung), sesuai R12.7, jadi migrasi `0002` tidak diperlukan
  - [ ] 7.1 Setelah schema `otp.requested.v1` masuk kontrak (platform 5.2), tambah test kontrak payload
    - _Depends on: platform 5.2_
    - _Requirements: 12.1_
  - [ ] 7.2 Test TC-MSG-05 (terkirim < 5 detik) dan TC-MSG-06 (dekripsi gagal → DLQ)
    - **Property 3: Poison goes straight to DLQ**
    - _Requirements: 12.3, 12.6_
  - _Requirements: 12.2, 12.4, 12.5, 12.7_

- [ ] 8. Retry & DLQ
  - Ada: retry berjenjang + DLQ dari `pkg/rabbitmq`, `Permanent` untuk payload rusak
  - [ ] 8.R1 Bungkus pengiriman SMTP dengan `pkg/resilience` breaker + bulkhead (timeout 3 s, ambang 5, cooldown 30 s, bulkhead 10). Breaker OPEN = gagal sementara → retry
    - **Property 5: Notification failure never affects source**
    - _Requirements: 9.5_
  - [ ] 8.2 Redrive DLQ lewat `tools/redrive` (TC-MSG-02)
    - _Depends on: platform 9.3_
    - _Requirements: 9.6_
  - _Requirements: 9.1, 9.2, 9.3, 9.4_

- [ ] 9. Privasi
  - [ ] 9.1 Test log scan: tidak ada email, NIK, token push, atau kode OTP di log dan `last_error`
    - **Property 6: No secret in logs**
    - _Requirements: 11.3, 11.4_

- [x] 10. Health endpoint
  - _Requirements: 10.1_

- [ ] 11. Metrik (tambahan dari spec)
  - [ ] 11.1 Jumlah terkirim/gagal per jenis + alert `NotificationDLQNotEmpty`, setelah OQ-5
    - _Depends on: platform 4.2_
    - _Requirements: 9.2_

- [ ] 12. Retensi data pribadi (keputusan 2026-10-21)
  - [ ] 12.1 Job worker: hapus `push_subscriptions` yang `expires_at` lewat; purge `deliveries` lama. Bertahap (`LIMIT`), idempoten, log hanya jumlah baris
    - _Depends on: platform 13.1_
    - _Requirements: 11.5_

## Notes
- Nama queue sekarang gabungan (`notification.booking.events`, dst.) dan routing key tanpa `.v1`. Keduanya bekerja karena producer memakai nama yang sama; penyelarasan dikerjakan sekali di platform task 12.
- Notification tidak menerbitkan event dan tidak menulis stok/uang/akun (invarian #6).
