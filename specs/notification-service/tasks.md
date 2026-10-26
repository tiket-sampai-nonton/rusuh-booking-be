# Implementation Plan: Notification Service

Owner: **kamu** (inisialisasi kode oleh DEV-2). Penomoran task DEV-2 (0a, 0b, 1–12) dipertahankan;
isinya diselaraskan dengan `requirements.md`/`design.md` terbaru. Sub-task **R** = perbaikan kode yang
sudah ada.

Status 2026-10-21: semua consumer (email booking, push giliran, OTP), API push-subscription, repository
delivery, adapter SMTP/FCM/template sudah ada dan `go test` lulus.

**Rekonsiliasi audit 2026-10-23** (`docs/evidence/2026-10-23-audit-backend.md` §10):
- **0a.R1 terkonfirmasi masih ada** dan menjadi prioritas utama: email yang gagal tidak pernah dikirim
  ulang, sehingga retry NFR-10 tidak berjalan untuk FR-22/FR-23.
- Email e-tiket juga belum pernah terkirim karena producer Booking melanggar kontrak `booking.paid`
  (audit F5, milik DEV-2). Sisi Notification sudah sesuai kontrak; tidak perlu diubah.
- Push, OTP, redrive, dan retensi bukan requirement PDF (yang diminta hanya email sukses/expired);
  task terkait dipindah ke "Dikeluarkan dari rencana" dengan ID tetap.

## Tasks

- [x] 0a. Delivery entity, repository, idempotensi (selesai 2026-10-26)
  - Ada: `entity/delivery.go`, `postgres/delivery_repository.go` (`ClaimDelivery`, `MarkSent`, `MarkDead`, `BumpAttempt`)
  - [x] 0a.R1 **Selesai 2026-10-26** (klaim kondisional + lease 60 s; attempts hanya naik lewat `BumpAttempt`). **Bug (terkonfirmasi 2026-10-23, `delivery_repository.go:30`):** `ClaimDelivery` memakai `ON CONFLICT DO NOTHING`. Saat pesan datang lagi dari retry queue, barisnya sudah ada (status `RETRY`) sehingga dianggap duplikat dan email tidak pernah terkirim. Ganti dengan klaim kondisional: boleh lanjut bila status `PENDING`/`RETRY` atau `SENDING` yang sudah basi (design §4.3)
    - **Property 1: At most one send after success**, **Property 2: Retry eventually sends**
    - _Requirements: 8.1, 8.2, 2.2_
  - [x] 0a.2 Selesai 2026-10-26 (`delivery_repository_integration_test.go`, ditambah `MarkDead`/`MarkExpired` dan repository subscription; terbukti gagal di kode lama). Integration test repository (PostgreSQL, tag `integration`): baris baru → klaim; `RETRY` → klaim; `SENDING` basi → klaim; `SENT` → skip; dua klaim paralel → hanya satu berhasil
    - _Requirements: 6.1, 6.2_

- [x] 0c. Penyedia email dapat dipilih: Mailpit atau Resend (selesai 2026-10-22, branch `feature/notification-email-provider`)
  - [x] 0c.1 Config `EMAIL_PROVIDER` (default `mailpit`), `EMAIL_FROM` (cadangan `SMTP_FROM`; pengirim default hanya untuk mailpit), `RESEND_API_KEY`; validasi fail-fast; peringatan tepat sekali saat start dengan resend
    - _Requirements: 13.1, 13.3, 13.8, 13.11_
    - _Design: §13, §14_
  - [x] 0c.2 Adapter `infrastructure/resend` (net/http, tanpa SDK): `Idempotency-Key`, `id` sebagai `provider_message_id`, klasifikasi error, key/alamat/pesan Resend tidak pernah masuk error
    - **Property 7: Provider isolation**
    - _Requirements: 13.3, 13.4, 13.5, 13.6, 13.7_
  - [x] 0c.3 Pilih adapter di `notification_module.go` (`newEmailSender`); port `IEmailSender` menerima `idempotencyKey`; usecase paid/expired/review menandai delivery `DEAD` pada error permanen; test `httptest` tanpa Resend nyata
    - _Requirements: 13.2, 13.10_
  - Catatan: breaker + bulkhead (R13.9) dikerjakan di 8.R1 karena membungkus `IEmailSender` apa pun; retry email sementara belum mengirim ulang sampai 0a.R1 selesai (bug `ClaimDelivery`, bukan bagian 0c)
  - Uji nyata ke Resend belum dijalankan: lakukan manual dengan `EMAIL_PROVIDER=resend` ke alamat milikmu (kuota 100/hari), lalu kembalikan ke `mailpit`

- [x] 1. Email e-tiket (`booking.paid`)
  - [x] 1.1 Selesai 2026-10-26 (`renderer_test.go`; `ticketLinkToken` kini opsional di `Validate()` dan template menyembunyikan baris tautan bila kosong; schema kontrak diubah di payment Task 15.3). Test isi email: kode tiket, nama, NIK tersamar, tautan tiket (TC-TKT-02). Fixture payload diambil dari `booking.paid.v1.schema.json` (bukan payload Booking saat ini, yang belum sesuai kontrak — audit F5)
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

- [x] 10. Health endpoint
  - _Requirements: 10.1_

- [x] 8. Retry & DLQ (selesai 2026-10-26)
  - Ada: retry berjenjang + DLQ dari `pkg/rabbitmq`, `Permanent` untuk payload rusak (Property 3 sudah diuji di `pkg/rabbitmq/handle_test.go`)
  - [x] 8.R1 Selesai 2026-10-26 (`infrastructure/emailguard`, dipasang untuk Mailpit dan Resend; permanen dan bulkhead penuh tidak membuka breaker). Bungkus pengiriman email dengan `pkg/resilience` breaker + bulkhead (timeout 3 s, ambang 5, cooldown 30 s, bulkhead 10). Breaker OPEN = gagal sementara → retry. Prioritas rendah (P2): retry queue sudah memenuhi NFR-10; kerjakan setelah 0a.R1
    - **Property 5: Notification failure never affects source**
    - _Requirements: 9.5, 13.9_
  - _Requirements: 9.1, 9.2, 9.3, 9.4_

- [x] 9. Privasi (selesai 2026-10-26)
  - [x] 9.1 Selesai 2026-10-26 (alamat email di teks error SMTP diganti `[email]` di adapter, sehingga `last_error` dan log bersih; hanya alamat email yang di-scrub). `last_error` menyimpan `err.Error()` mentah; balasan SMTP saat `RCPT` gagal (`smtp: rcpt: %w`) bisa memuat alamat penerima. Simpan hanya kelas error (mis. `smtp_rcpt`, `smtp_dial`) + test. Prioritas rendah: SMTP hanya dipakai Mailpit lokal, adapter Resend sudah membersihkan error. Redaksi log sudah dicakup platform 4.4
    - **Property 6: No secret in logs**
    - _Requirements: 11.3, 11.4_

## Dikeluarkan dari rencana (audit 2026-10-23)

ID dipertahankan agar jejaknya jelas. Kode yang sudah ada tidak dihapus, hanya tidak dikembangkan.

- 0b.R1 Adapter push `log` — push bukan requirement PDF; `noopPush` cukup untuk lokal.
- 6. API push subscription (6.R1 format error) — push di luar requirement PDF.
- 7. Email OTP (7.1 test kontrak, 7.2 TC-MSG-05/06) — login OTP opsional menurut asumsi PDF; poison → DLQ sudah diuji di `pkg/rabbitmq`.
- 8.2 Redrive DLQ (`tools/redrive`) — bergantung platform 9.3 yang berprioritas rendah; redrive manual lewat RabbitMQ management cukup untuk demo.
- 11.1 Metrik notifikasi + alert — NFR-09 meminta metrik antrian, konversi, dan expired; jumlah DLQ terlihat di RabbitMQ management.
- 12.1 Retensi data pribadi — bukan requirement PDF; menunggu platform 13.1.

## Notes
- Nama queue sekarang gabungan (`notification.booking.events`, dst.) dan routing key tanpa `.v1`. Keduanya bekerja karena producer memakai nama yang sama; jangan diganti kecuali lewat platform 12.1.
- Notification tidak menerbitkan event dan tidak menulis stok/uang/akun (invarian #6).
- Urutan kerja: 0a.R1 → 0a.2 → 1.1 → 8.R1 → 9.1 (semua selesai 2026-10-26).
