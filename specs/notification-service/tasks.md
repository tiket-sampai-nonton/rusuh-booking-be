# Implementation Plan: Notification Service

## Overview

Mengisi boilerplate `services/notification` dengan consumer event (email + push), API push-subscription,
pelacakan delivery idempoten, retry + DLQ, dan (setelah kontrak tersedia) email OTP. Consumer didaftarkan
sebagai `rabbitmq.Subscription` di `RunWorker`, bukan periodic job.

Prasyarat lintas spec: platform task 2.1 (VisitorAuth), 4.4 (redaksi logger), 5.2 (schema
`otp.requested.v1` — OQ-4); event produsen dari booking task 19 dan queue task 10.

## Tasks

- [x] 0. Fondasi yang sudah ada (audit 2026-10-21)
  - Cobra verb, config fail-fast subset, clients PG + RabbitMQ, `GET /health`; migrasi `deliveries` + `push_subscriptions`
  - _Requirements: 10.1, 11.1_

- [ ] 1. Konfigurasi & fondasi domain
  - [ ] 1.1 Config: `SMTP_*`, `PUSH_PROVIDER=fcm|log`, `FCM_*`, `PII_ENC_KEY`, kunci HMAC `recipient_hash`, kunci enkripsi push token, kunci publik Queue, breaker SMTP — fail-fast; update `.env.example`
    - _Requirements: 11.2_
    - _Design: §14_
  - [ ] 1.2 Domain `Delivery` (state machine design §8), port `IEmailSender`, `IPushSender`, `ITemplateRenderer`, `IDeliveryRepo`, `ISubscriptionRepo`, `IPIIProtector` + `make mock`
    - _Requirements: 6.1, 6.2_

- [ ] 2. Adapter outbound
  - [ ] 2.1 Repository `deliveries` dengan klaim kondisional (design §4.3), `BumpAttempt` → `RETRY`, `MarkDead`, `MarkSent`; repository `push_subscriptions` (upsert, delete, get)
    - _Requirements: 6.1, 6.2, 8.1, 8.2_
    - _Design: §4.3_
  - [ ] 2.2 SMTP adapter dibungkus breaker + bulkhead `smtp`; template `eticket`, `expired`, `review` di-embed
    - _Requirements: 9.5, 1.1, 2.1, 3.1_
    - _Design: §5, §13_
  - [ ] 2.3 Push adapter `fcm` (HTTP v1, deteksi token invalid) dan `log` (lokal/CI, tanpa token di log)
    - _Requirements: 4.3, 11.2_
  - [ ] 2.4 Integration test repository: insert baru, redelivery `RETRY`, `SENDING` basi, `SENT` → kosong
    - **Property 1: At most one send after success**
    - _Requirements: 8.1, 8.2_

- [ ] 3. Consumer email booking
  - [ ] 3.1 Subscription `notification.booking-paid` (`booking.paid.v1`): validasi payload, klaim, render `eticket` (NIK tersamar, tautan tiket), kirim, `SENT`
    - _Requirements: 1.1, 1.2, 1.3, 1.4_
    - _Design: §4.1, §4.3, §5.1_
  - [ ] 3.2 Subscription `notification.booking-expired` dan `notification.booking-review`
    - _Requirements: 2.1, 3.1_
    - _Design: §5.2, §5.3_
  - [ ] 3.3 Test TC-TKT-02, TC-MSG-01 (SMTP down → retry → terkirim), TC-MSG-03 (kirim ganda → satu email), TC-MSG-04 (retry ke-2 sukses)
    - **Property 2: Retry eventually sends**, **Property 5: Notification failure never affects source**
    - _Requirements: 2.2, 8.1, 9.4_
  - Catatan: konten "X hari kerja" di template review perlu konfirmasi Ops

- [ ] 4. Consumer push giliran
  - [ ] 4.1 Subscription `notification.turn-soon` (`queue.turn.soon.v1`): lookup subscription, kirim `TURN_SOON`, ack bila tidak ada subscription, hapus subscription bila token invalid
    - _Requirements: 4.1, 4.2, 4.3_
    - _Design: §4.4, §6_
  - [ ] 4.2 Subscription `notification.turn-arrived` (`queue.turn.arrived.v1`): buang bila `claimDeadline` lewat; kirim `TURN_ARRIVED`
    - **Property 4: Late push discarded**
    - _Requirements: 5.1, 5.2, 5.3_
  - [ ] 4.3 Test TC-QUE-13 dengan `PUSH_PROVIDER=log`
    - _Requirements: 4.1, 5.1_

- [ ] 5. API push subscription
  - [ ] 5.1 `POST /api/v1/push-subscriptions` (VisitorAuth, validasi, enkripsi token, upsert, 204) dan `DELETE /api/v1/push-subscriptions/{eventId}` (204)
    - _Requirements: 7.1, 7.2, 7.3, 7.4_
    - _Design: §2.1, §2.2_
    - _Depends on: platform 2.1_

- [ ] 6. Retry, DLQ, poison
  - [ ] 6.1 Pastikan semua handler: error transien → return err (retry berjenjang `pkg/rabbitmq`), percobaan habis → `DEAD` + DLQ, payload rusak / `schemaVersion` tak dikenal → `rabbitmq.Permanent`
    - **Property 3: Poison goes straight to DLQ**
    - _Requirements: 9.1, 9.2, 9.3_
    - _Design: §7_
  - [ ] 6.2 Test TC-MSG-02 (DLQ lalu redrive lewat `tools/redrive`)
    - _Requirements: 9.6_
    - _Depends on: platform 9.3_

- [ ] 7. Checkpoint — email & push
  - Test task 2–6 lulus; `make ci` hijau; tanyakan ke user bila OQ-4 belum diputuskan

- [ ] 8. Email OTP login — **blocked: OQ-4**
  - [ ] 8.1 Migrasi `0002_otp_delivery` (tambah `OTP_LOGIN`, longgarkan CHECK referensi) — `0001` tidak diubah
    - _Requirements: 12.7_
    - _Design: §4.2_
  - [ ] 8.2 Subscription `notification.otp` (`otp.requested.v1`): buang bila `expiresAt` lewat, dekripsi `emailEnc`/`codeEnc` (`PII_ENC_KEY`), render `otp_login`, kirim; gagal SMTP → ack tanpa retry; dekripsi gagal → `Permanent`
    - _Requirements: 12.1, 12.2, 12.3, 12.4_
    - _Design: §4.5, §11.3_
    - _Depends on: platform 5.2, user task 3.1_
  - [ ] 8.3 Test TC-MSG-05 (≤ 5 s), TC-MSG-06 (enkripsi invalid), TC-USR-11 (kode tidak di log/`last_error`)
    - **Property 6: No secret in logs**
    - _Requirements: 12.5, 12.6_

- [ ] 9. Observability & privasi
  - [ ] 9.1 Metrik design §15 + alert `NotificationDLQNotEmpty` via `pkg/telemetry`
    - _Requirements: 9.2_
    - _Depends on: platform 4.2_
  - [ ] 9.2 Log scan: tidak ada email/NIK/token/kode OTP plaintext; hanya PII yang ada di schema diproses
    - **Property 6: No secret in logs**
    - _Requirements: 11.3, 11.4_

- [ ] 10. Final checkpoint
  - Property 1–6 punya test lulus; contract test `notification.yaml`; `make ci` hijau

## Notes

- Notification tidak menerbitkan event dan tidak menulis stok/uang/akun (invarian #6).
- Penghapusan push token saat penjualan berakhir (SRS-D-07) belum punya task — menunggu OQ-11.
