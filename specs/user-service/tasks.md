# Implementation Plan: User Service

## Overview

User Service sudah terimplementasi (evidence `docs/evidence/2026-10-19-boilerplate-v1.5.md`, coverage
logika 94,4%). Rencana ini mencatat pekerjaan yang sudah selesai (untuk traceability) dan sisa
pekerjaan: penyelarasan validasi NIK, migrasi pengiriman OTP ke event `otp.requested` (ADR-020),
breaker SMTP selama transisi, dan observability.

Prasyarat lintas spec: platform task 1 (`pkg/nik`), 5.2 (schema `otp.requested.v1` + `producer`
`user-service` — OQ-4), 6 (distribusi kunci publik).

## Tasks

- [x] 0. Fitur inti (terverifikasi audit 2026-10-21)
  - OTP request/verify, rate limit + cooldown, audit login, akun otomatis, rotasi refresh + deteksi replay, logout idempoten, profil GET/PATCH, enkripsi/hash PII, migrasi `0001_init`, config fail-fast, `/health`
  - Klaim identitas token (dulu "P0 / D1"): `IssueAccess(accountID, email, nikHash, now)`, `GetNIKHashByID`, `GetEmailEncByID`, `UnsealEmail`, mock diregenerasi
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 4.1, 4.2, 4.3, 5.1, 5.2, 5.3, 5.4, 6.1, 6.2, 6.4, 6.5, 6.6, 7.1, 7.2, 7.4, 7.5, 8.1, 8.3, 9.1, 9.2, 9.3, 9.4, 9.5, 9.8_

- [ ] 1. Lengkapi verifikasi yang tertunda
  - [ ] 1.1 Property test anti-enumerasi, OTP sekali pakai, rotasi/replay, satu NIK per akun (sebagian sudah ada sebagai unit test — pastikan eksplisit)
    - **Property 1: Anti-enumeration**, **Property 2: OTP single use & bounded attempts**, **Property 3: Refresh rotation & replay revocation**, **Property 6: One NIK per account**
    - _Requirements: 1.2, 2.4, 3.3, 6.5_
  - [ ] 1.2 Integration test `GetNIKHashByID` (NULL → `nil, nil`) dan `GetEmailEncByID` terhadap PostgreSQL nyata
    - **Property 4: Identity claims consistency**
    - _Requirements: 7.1, 7.2, 9.6_
  - [ ] 1.3 Jalankan `make lint`; perbaiki temuan errcheck/misspell pre-existing yang dicatat spec lama (bila masih ada)
    - _Requirements: 9.7_

- [ ] 2. Selaraskan validasi NIK dengan `pkg/nik`
  - [ ] 2.1 Usecase `Profile.UpdateProfile` memanggil `nik.Validate` sebelum `valueobject.NewNIK`; pertahankan `400 VALIDATION_ERROR` field `nik` (kontrak `user.yaml`)
    - _Requirements: 6.3_
    - _Design: platform design §1_
    - _Depends on: platform 1_
  - [ ] 2.2 Test paritas: korpus SDD §4.9 ditolak/diterima sama dengan Queue/Booking
    - _Requirements: 6.3_

- [ ] 3. Migrasi pengiriman OTP ke event (ADR-020) — **blocked: OQ-4**
  - [ ] 3.1 Port `IOTPPublisher` + adapter `rabbitmq` (`pkg/rabbitmq.Publisher`, publisher confirm, routing key `otp.requested.v1`, envelope `producer = user-service`, `aggregateId` sesuai keputusan OQ-4)
    - _Requirements: 1.7, 8.2_
    - _Design: §5c_
    - _Depends on: platform 5.2_
  - [ ] 3.2 Config `RABBITMQ_*` (fail-fast) + health check RabbitMQ; `PII.SealOTP` untuk `codeEnc`
    - _Requirements: 1.7, 8.1_
  - [ ] 3.3 Ganti `IMailer` dengan `IOTPPublisher` di `Auth.RequestOTP`; gagal publish → `OTP.Clear` + `503 DEPENDENCY_UNAVAILABLE`; hapus adapter `smtp` dan `SMTP_*` setelah Notification task 8 siap
    - _Requirements: 1.7_
    - _Depends on: notification 8.2_
  - [ ] 3.4 Test: payload terenkripsi (tidak ada email/kode plaintext di payload/log), publish gagal menghapus OTP (TC-USR-11, TC-MSG-05 end-to-end)
    - **Property 5: PII never in plaintext**
    - _Requirements: 1.8_

- [ ] 4. Breaker SMTP selama transisi (OQ-9)
  - [ ] 4.1 Bungkus `smtp.Mailer.SendOTP` dengan `pkg/resilience` breaker + bulkhead (timeout 3 s, ambang 5, cooldown 30 s, bulkhead 10 — PROPOSED SDD §4.6.2); OPEN → `503 DEPENDENCY_UNAVAILABLE` cepat
    - _Requirements: 1.7_
  - Catatan: dihapus bersama task 3.3; kerjakan hanya bila task 3 tertunda melewati demo

- [ ] 5. Distribusi kunci publik ke Queue & Booking
  - [ ] 5.1 Ekspor kunci publik User (per `kid`) lewat `make keys`/compose dan SSM; dokumentasikan rotasi
    - _Requirements: 7.3_
    - _Depends on: platform 6.1_

- [ ] 6. Observability
  - [ ] 6.1 Metrik `rbt_user_*` (request OTP, verify per outcome, refresh, replay) via `pkg/telemetry`; alert brute-force (`OTP_INVALID`/`OTP_LOCKED` tinggi) dan `REFRESH_REUSED`
    - _Requirements: 1.4, 3.3_
    - _Depends on: platform 4.2_

- [ ] 7. Final checkpoint
  - `make ci` hijau; TC-USR-01..12 lulus; TC-MSG-05/06 lulus setelah task 3
  - Tanyakan ke user untuk OQ-4 dan OQ-9

## Notes

- Spec lama menyatakan "rollout: token lama tanpa klaim akan gagal validasi" — tidak benar: token lama
  tetap lolos verifikasi (klaim identitas `omitempty`) dan diperlakukan `has_nik = false` sampai
  di-refresh (≤ `TOKEN_ACCESS_TTL` 15 menit). Tidak perlu langkah rollout khusus.
- Riwayat pesanan (`/me/bookings`) milik Booking (OQ-10), bukan task User.
