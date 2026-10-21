# Implementation Plan: User Service

Owner: **DEV-2**. Penomoran Task 1–9 DEV-2 dipertahankan (semua `[DONE]` di `.progress.json`).
Sisa pekerjaan dari spec kita ditambahkan sebagai sub-task di task yang relevan dan Task 10.

Status 2026-10-21: semua fitur ada. OTP sudah dikirim lewat event `otp.requested`
(`infrastructure/rabbitmq/otp_publisher.go`); adapter SMTP sudah dihapus, jadi OQ-9 (breaker
User→SMTP) tidak relevan lagi.

## Tasks

- [ ] 1. Request OTP
  - Ada: rate limit, cooldown, anti-enumerasi, audit, publish `otp.requested` terenkripsi
  - [ ] 1.R1 Envelope OTP: `aggregateId` dikirim kosong (envelope mewajibkannya) dan `occurredAt` diisi `expiresAt`. Isi `aggregateId` = `email_hash` hex dan `occurredAt` = waktu sekarang
    - _Requirements: 1.7_
  - [ ] 1.R2 Tambah schema kontrak `otp.requested.v1` + `user-service` ke enum `producer` (platform 5.2, owner: kamu)
    - _Depends on: platform 5.2_
    - **Property 5: PII never in plaintext**
    - _Requirements: 1.8_
  - **Property 1: Anti-enumeration**
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_

- [x] 2. Verify OTP & login
  - **Property 2: OTP single use & bounded attempts**
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8_

- [x] 3. Refresh token (rotasi + deteksi replay)
  - **Property 3: Refresh rotation & replay revocation**
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

- [x] 4. Logout
  - _Requirements: 4.1, 4.2, 4.3_

- [x] 5. Get profile
  - _Requirements: 5.1, 5.2, 5.3, 5.4_

- [ ] 6. Update profile
  - [ ] 6.R1 Validasi NIK pakai `pkg/nik` di usecase (VO sekarang lebih longgar). Respons tetap `400 VALIDATION_ERROR` sesuai `user.yaml`
    - _Depends on: platform 1_
    - _Requirements: 6.3_
  - **Property 6: One NIK per account**
  - _Requirements: 6.1, 6.2, 6.4, 6.5, 6.6_

- [ ] 7. Klaim token untuk validasi service lain
  - Ada: `email`, `nik_hash`, `has_nik` di access token
  - [ ] 7.1 Integration test `GetNIKHashByID` (NULL → `nil`) dan `GetEmailEncByID`
    - **Property 4: Identity claims consistency**
    - _Requirements: 7.1, 7.2_
  - [ ] 7.2 Distribusi kunci publik ke Queue & Booking (compose/SSM) + prosedur rotasi `kid`
    - _Depends on: platform 6.1_
    - _Requirements: 7.3_
  - _Requirements: 7.4, 7.5_

- [x] 8. Health endpoint
  - _Requirements: 8.1, 8.2, 8.3_

- [x] 9. Build & verify
  - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 9.7, 9.8_

- [ ] 10. Observability (tambahan dari spec)
  - [ ] 10.1 Metrik `rbt_user_*` + alert brute force / `REFRESH_REUSED`, setelah OQ-5
    - _Depends on: platform 4.2_
    - _Requirements: 1.4, 3.3_

## Notes
- Token lama tanpa klaim identitas tetap valid dan dianggap `has_nik = false` sampai di-refresh; tidak perlu langkah rollout khusus.
- Riwayat pesanan (`/me/bookings`) milik Booking.
