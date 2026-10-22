# Specs — Rusuh Booking Tiket (backend)

Spesifikasi implementasi bergaya **Kiro Specs** untuk monorepo `rusuh-booking-be`. Setiap spec
terdiri dari tiga file yang saling menelusur:

| File | Isi | Dibaca oleh |
|------|-----|-------------|
| `requirements.md` | Introduction, Glossary, user story + acceptance criteria (EARS), invarian, trace ke SRS/TC | BA, reviewer, QA |
| `design.md` | Arsitektur, komponen/port, model data, API, event, state machine, concurrency, error, konfigurasi, **Correctness Properties** | Engineer, reviewer |
| `tasks.md` | Implementation plan: task bernomor + subtask, `_Requirements_`, `_Depends on_`, checkpoint | Developer, AI coding agent |

Steering yang berlaku untuk semua spec ada di `.kiro/steering/` (product, tech, structure,
code-principles, invariants, security-privacy, testing-review) dan **tidak diulang** di sini.

## Indeks spec

| Spec | Cakupan | Port | Owner | Status kode (2026-10-21, setelah merge kode DEV-2) |
|------|---------|------|-------|-----------------------------------------------------|
| [platform](platform/) | `pkg/`, kontrak, CI, compose/edge, `tests/`, `tools/` | — | **kamu** | `pkg/` inti + CORS lokal; `nik`, `outbox`, telemetry, kontrak OTP, edge belum |
| [notification-service](notification-service/) | Email + push | 8084 | **kamu** (kode awal DEV-2) | Fitur lengkap; perbaiki klaim retry, breaker SMTP, adapter push log |
| [payment-service](payment-service/) | Midtrans/simulator, attempt, webhook, resolve | 8083 | **kamu** | Belum ada kode bisnis |
| [user-service](user-service/) | Akun opsional, OTP, token, profil | 8085 | DEV-2 | Lengkap (OTP lewat event); perbaikan kecil envelope |
| [queue-service](queue-service/) | Waiting room, FIFO, admission | 8081 | DEV-2 | Fitur ada; ada defect direct admission & antrean tersumbat |
| [booking-service](booking-service/) | Stok & uang, sesi, hold, settlement | 8082 | DEV-2 | Alur publik ada; ada defect stok, sweeper, payload event; API untuk Payment belum |
| [e2e](e2e/) | Journey lintas service (dari DEV-2) | — | bersama | Menunggu rework di atas |

`tasks.md` tiap service = gabungan task DEV-2 (nomor dipertahankan, cocok dengan `.progress.json` dan
skill `implement-service`) + keputusan spec. Sub-task **R** = perbaikan kode yang sudah ada.

**Rework yang memutus alur end-to-end (perlu dikerjakan DEV-2 lebih dulu):** booking 6.R1 (hold
tidak mengurangi `allocated_units`), 10.R1 (alokasi sesi habis tidak dikembalikan, tanpa
`journey.ended`), 12.R1 (payload `journey.ended` tidak bisa dibaca Queue), 4.R1 (`supply` dan
`:release` belum ada), Task 13 (API untuk Payment); queue 8.R1 (direct admission tidak pernah
berhasil), 11.R1 (entry kuota habis menyumbat antrean).

**Keputusan gaya (2026-10-21):** error response hanya `code` + `message`, mis.
`{"meta":{"code":409,"message":"BOOKING_LOCKED: Pesanan sudah terkunci"},"data":null}`; `requestId` di
header; detail + payload request (diredaksi) hanya di log (platform R2.8, task 2.6). Respons sukses hanya
memuat field kontrak.

Urutan implementasi yang disarankan (dependensi antar-spec):

```
platform 2.6 (format error), 5.2 (schema OTP), 12 (routing key)   ← kamu
notification 0a.R1, 8.R1, 0b.R1                                  ← kamu
payment 0 → 12 (simulator) → 3 → 4 → 5 → 7 → 8 → 9              ← kamu, butuh booking Task 13 (DEV-2)
booking 6.R1, 10.R1, 12.R1, 4.R1, 13 → queue 8.R1, 11.R1         ← DEV-2
e2e journey                                                      ← bersama
```

## Prioritas sumber (dipakai saat dokumen saling bertentangan)

1. **Migrasi terterapkan** (`services/*/deployments/migrations/*.up.sql`) — tidak boleh diubah (`structure.md`).
2. **Kontrak normatif**: `docs/contracts/openapi/*.yaml`, `docs/contracts/events/*.schema.json`.
3. **Kode yang sudah ada** (`pkg/`, `services/user/`) untuk perilaku yang sudah terimplementasi.
4. **SRS-RBT-001** (`docs/analisa/`) untuk *apa* yang wajib (requirement, invarian backend, error code).
5. **SDD-RBT-001** (`docs/analisa/04-SDD-RBT-001.md`) §4.4–§4.10 untuk *bagaimana* (lock order, SQL, sweeper, routing).
6. **SDD per service** (`docs/analisa/SDD-*-001.md`) — ringkasan; bila berbeda dari (5), (5) yang dipakai.
7. HLD, ADR, PRD, integration guide — konteks dan alasan.

Bila dua sumber bertingkat sama berbeda (mis. SRS vs kontrak), spec **tidak memilih**; perbedaan
dicatat sebagai open question di bawah dan di spec terkait.

## Notasi traceability

- `R6.4` = Requirement 6, acceptance criterion 4 di `requirements.md` spec yang sama.
  Rujukan lintas spec ditulis `booking R6.4`.
- `Property N` = Correctness Property N di `design.md` spec yang sama.
- Di `tasks.md`: `_Requirements: 6.4, 6.5_`, `_Design: §9.1_`, `_Depends on: platform 2.1_`.
- `- [x]` = sudah ada di kode (diverifikasi saat audit); `- [ ]` = belum; `- [ ]*` = test opsional
  untuk MVP (konvensi Kiro) — **test invarian tidak pernah opsional**.
- SRS ID (`SRS-F-12-04`) dan TC ID (`TC-BKG-07`) merujuk `docs/analisa/02-SRS-RBT-001.md` dan
  `docs/analisa/06-TP-RBT-001.md`.
- Label fakta: **[kode]** hasil inspeksi codebase · **[dok]** dari dokumentasi · **[asumsi]** belum
  terkonfirmasi · **[OQ-x]** butuh keputusan.

## Register konflik yang sudah diselesaikan di spec

| ID | Spec | Konflik | Keputusan (sumber) |
|----|------|---------|--------------------|
| K1 | booking | `payment-lock` disebut memindahkan booking ke `VERIFYING` | Booking tetap `PENDING_PAYMENT` + `payment_started_at`; admission `EDITABLE → CONVERTED(PAYMENT_STARTED)`, sisa unit dilepas (SDD-RBT §4.7.1 C.9, SRS-F-14-04) |
| K2 | booking | Session sweeper meng-expire admission `EDITABLE` dan melepas hold | `EDITABLE` lewat `expires_at + grace` → `CONVERTED(SELECTION_EXPIRED)`, sisa unit dilepas, booking tetap bisa dibayar (SDD C.10, SRS-F-08-02) |
| K3 | booking | Expiry tanpa `VERIFYING`; "resolve menemukan bayar → REVIEW" | Lewat deadline → `VERIFYING` (`verification_until`) → resolve `CHECK_AND_CANCEL`: `SUCCEEDED` tepat waktu → `PAID`, `NOT_PAID`/`NO_ATTEMPT` → `EXPIRED`, `UNKNOWN` → retry sampai `verification_until` lalu `EXPIRED` (SRS-F-17-01..03, `internal-payment.yaml`, SDD C.4) |
| K4 | booking | Settlement hanya guard `status != PAID` | `settlement_time ≤ payment_expires_at` → `PAID`; terlambat atau booking sudah `EXPIRED/CANCELLED` → `REVIEW` tanpa tiket (SRS-F-16-02/03, SRS-F-17-05, SDD C.4) |
| K5 | booking | Urutan lock `supply → category → quota → booking` | `idempotency → admissions → bookings → event_supply → buyer_quotas → category_inventories (ASC)` (SDD-RBT §4.7) |
| K6 | queue | Outcome alokasi `RESERVED/QUOTA_EXHAUSTED`, status entry `REJECTED` | `GRANTED/NO_SUPPLY_NOW/SOLD_OUT/BUYER_QUOTA_EXHAUSTED/ALREADY_ACTIVE` (`internal-booking.yaml`) |
| K7 | queue | Claim sweeper mem-publish `booking.journey.ended` | Queue hanya menandai `MISSED`; Booking session sweeper yang menerbitkan `journey.ended(UNCLAIMED)` (SDD C.5/C.10; producer event `booking.*` = booking-service) |
| K8 | semua | Routing key tanpa versi (`booking.paid`) dan nama queue consumer berbeda-beda | Routing key berversi `<event>.v1`, queue `<service>.<purpose>` (SDD-RBT §4.6.2, komentar `pkg/rabbitmq.Subscription`) |
| K9 | payment | Default breaker `FailureThreshold=3, Cooldown=60s` | `pkg/resilience` tidak punya default [kode]; nilai PROPOSED SDD-RBT §4.6.2 dipakai sebagai konfigurasi awal |
| K10 | user | R1.7 publish `otp.requested`, R8.2 "tidak publish event", kode kirim SMTP langsung | Target = event `otp.requested` (ADR-020, SDD-USER v1.2); kode saat ini = SMTP `IMailer` → task migrasi (user tasks 3) |
| K11 | booking | Hold hanya menaikkan `held_units` | Hold memindahkan `allocated_units → held_units` (SDD C.3); edit menyesuaikan keduanya (C.8) |
| K12 | notification | Trace push giliran ke TC-QUE-04 | Push giliran = TC-QUE-13 (TP §6) |
| K13 | notification | SRS-IF-22: email lewat SMTP, SES sandbox untuk demo | Demo memakai **Resend** (HTTP API, akun gratis 100 email/hari dan 3.000/bulan); lokal/CI tetap Mailpit, dipilih lewat `EMAIL_PROVIDER` (notification R13). SRS-IF-22, HLD, handbook, dan SDD Notification sudah diperbarui 2026-10-22 |

## Open questions (butuh keputusan; spec tidak mengarang jawaban)

| ID | Pertanyaan | Sumber konflik | Dampak | Rekomendasi |
|----|-----------|----------------|--------|-------------|
| OQ-1 | Batas cek NIK pemegang: `429 ATTENDEE_CHECK_LIMIT_REACHED` atau `200 {result: CHECK_LIMIT_REACHED}` lalu `429 RATE_LIMITED`? | SRS-F-11-06 & SRS §3.1.4 vs `booking.yaml` | booking R5.4, TC-BKG-09 | Ikuti kontrak (normatif untuk klien) dan perbarui SRS, atau tambah `ATTENDEE_CHECK_LIMIT_REACHED` ke kontrak secara additive |
| OQ-2 | Nama event SSE sisa waktu: `session` (SRS-F-25-03) atau `timer` (`booking.yaml`)? Event `closed` (SRS-F-25-07) belum ada di kontrak | SRS vs kontrak | booking R16, klien web/mobile | Pakai `timer` (kontrak) + tambah `closed` ke kontrak (additive) |
| OQ-3 | Pindah entry antrean ke perangkat lain via email (SRS-F-01-10, SHOULD) tidak punya endpoint di `queue.yaml` | SRS vs kontrak | queue R7.3–7.4, TC-QUE-16 | Tunda (SHOULD) sampai kontrak ditambah; task ditandai blocked |
| OQ-4 | Schema `otp.requested.v1` belum ada di `docs/contracts/events/`, dan `envelope.schema.json` `producer` tidak memuat `user-service`; contoh di SDD memakai `schemaVersion: "v1"` (string) padahal envelope integer | ADR-020/SDD-USER vs kontrak | user R1, notification R12, TC-MSG-05/06 | Tambah schema + enum `user-service` (platform task 5.2) sebelum user/notification task OTP |
| OQ-5 | Library metrik (Prometheus client / OpenTelemetry) belum dipilih dan belum ada di kode; tech radar mensyaratkan ADOPT | `tech.md`, SDD §4.1.3 `pkg/telemetry` | Semua R "observability" | Putuskan lewat ADR sebelum platform task 4 |
| OQ-6 | Target availability bulanan belum ditetapkan | SRS-OI-01 | SRS-Q-REL-08 | Ditetapkan bersama klien; tidak ada task |
| OQ-7 | `booking.journey.ended` payload di SDD-BOOKING-001 memuat `bookingId, visitorId`; schema v1 hanya `eventId, queueEntryId, outcome` | SDD per service vs kontrak | queue R8 | Ikuti schema v1 (kontrak) |
| OQ-8 | Mobile mengirim visitor token sebagai `Authorization: Bearer <visitorToken>` (SRS-F-22-02) atau `X-Visitor-Token` (security scheme `visitorHeader` di semua kontrak)? | SRS vs kontrak | platform R2, semua endpoint visitor | Ikuti kontrak (`X-Visitor-Token`) karena `Authorization: Bearer` sudah dipakai `userBearer`; perbarui SRS |
| OQ-9 | SRS-F-19-03 menyebut breaker `User→SMTP`; setelah ADR-020 User tidak memanggil SMTP | SRS vs ADR-020 | user R1 | Perbarui SRS: breaker `Notification→SMTP` saja; selama SMTP langsung masih dipakai, bungkus dengan breaker |
| OQ-10 | HLD menyebut riwayat pesanan "proxy ke Booking" di User Service; kontrak menaruh `GET /api/v1/me/bookings` di Booking | HLD §4 vs `booking.yaml`, SDD-USER §3.7 | booking R10 | Ikuti kontrak (Booking) |
| OQ-12 | SRS-F-26-07 meminta endpoint internal untuk melihat konfigurasi efektif per event; `internal-booking.yaml` hanya punya POST/PATCH admin | SRS vs kontrak | booking R4.7 | Tambah `GET /internal/v1/admin/events/{eventId}` (additive) |
| OQ-13 | Kapan `booking.quota.exhausted` diterbitkan? Bila saat `held+paid = max`, Queue memblokir join meski hold bisa expired dan kuota kembali (SRS-F-11-05); Queue `SADD` tidak pernah dihapus | Tidak didefinisikan di SRS/SDD/HLD | booking R12.5, queue R8.3 | Terbitkan hanya saat `paid_quantity = max_quantity` (pasti permanen); konfirmasi BA |
| OQ-14 | Nilai filter katalog tak dikenal: abaikan (default) atau `400`? Kontrak hanya punya `200` | Spec lama vs kontrak | booking R1 | Tambah `400 VALIDATION_ERROR` ke kontrak (additive) |
| OQ-15 | SDD C.5 menerbitkan `journey.ended` `outcome = SESSION_EXPIRED`; schema v1 tidak memuat nilai itu (`additionalProperties:false`) | SDD vs kontrak | booking R7.3, queue R8.1 | Tambah `SESSION_EXPIRED` ke enum (additive) atau petakan ke `EXPIRED`; spec sementara memakai `EXPIRED` |
| OQ-16 | Penyimpanan gambar event di lingkungan lokal (compose) tidak dirancang; cloud = S3 + CloudFront + Lambda | SDD-BOOKING C.4 | booking R18.3 | Simpan ke volume lokal + URL edge di `local`; S3 di staging |
| OQ-17 | Bila `payment.succeeded.amountMinor` ≠ `bookings.amount_minor` (seharusnya mustahil karena Payment menagih versi terkunci), apa yang dilakukan Booking? | Tidak didefinisikan | booking R9 | `REVIEW` + alert Ops (konsisten dengan jalur pembayaran bermasalah); konfirmasi BA |
| OQ-18 | Bagaimana Queue menangani outcome alokasi `ALREADY_ACTIVE` (NIK/visitor sudah punya admission aktif di Booking padahal Queue menegakkan satu entry aktif)? | Tidak didefinisikan | queue R6.13 | Anggap anomali: entry `ENDED` (`reason = ALREADY_ACTIVE`) + alert; konfirmasi BA |
| OQ-19 | SDD-PAYMENT App.B mendefinisikan `BOOKING_EXPIRED` (410), `VISITOR_MISMATCH` (403), `ATTEMPT_IN_PROGRESS` (409); `payment.yaml` dan SRS §3.1.4 tidak. `ATTEMPT_IN_PROGRESS` juga bertentangan dengan SRS-F-14-06 (ganti metode membatalkan attempt lama) | SDD per service vs kontrak/SRS | payment R1.4, R3 | Pakai `409 BOOKING_NOT_PAYABLE` untuk expired, `404 NOT_FOUND` untuk bukan pemilik (konsisten Booking); tambah ke kontrak bila perlu |
| OQ-20 | Simulator aktif di `APP_ENV=local|ci` (`payment.yaml`) atau `ci|local-offline` (SRS-IF-24)? `local-offline` tidak ada di daftar `APP_ENV` SDD §6.1 | Kontrak vs SRS vs SDD | payment R10.3, platform R7.4 | Tambah `local-offline` ke validasi `APP_ENV` dan selaraskan kontrak + SRS |
| OQ-21 | `order_id = RB-{publicRef}-{attemptNo}` (SDD-RBT §4.6.3), tetapi `Payable` (`internal-booking.yaml`) tidak memuat `publicRef` | SDD vs kontrak | payment R3.3, booking R14.1 | Tambah `publicRef` ke `Payable` (additive) |
| OQ-11 | Retensi data pribadi (ciphertext NIK event + 30 hari via `PII_RETENTION_DAYS_AFTER_EVENT`, push token dihapus saat penjualan berakhir) tidak punya job yang dirancang | SRS §3.4 (PROPOSED), SDD §4.10 | booking, queue, notification | Rancang job purge setelah retensi dikonfirmasi Ops |
| OQ-22 | Respons `GET /payments/{id}/status` dipolling tiap 3 detik dan mengembalikan seluruh `attempt` termasuk instruksi; cukupkah `{bookingStatus, attemptStatus, serverTime}`? | Over-fetching (masukan tim 2026-10-21) | payment R6.3, klien web/mobile | Usulkan ke FE; ubah kontrak bila setuju |

## Status open question (jawaban DEV-2, diverifikasi ke kode 2026-10-21)

Selesai:
- **OQ-1** — ikut kontrak: `200 {result: CHECK_LIMIT_REACHED}`. SRS perlu diperbarui.
- **OQ-3** — fitur pindah antrean ditunda.
- **OQ-9** — tidak relevan lagi; User sudah tidak memanggil SMTP.
- **OQ-18** — entry dengan `ALREADY_ACTIVE` tidak di-admit. Perlu queue 11.R1 supaya entry itu tidak menyumbat antrean.
- **OQ-19** — pakai kode error yang ada di kontrak; bukan pemilik → `404`.

Selesai:
- **OQ-13** — diputuskan: terbitkan `quota.exhausted` saat `paid = max`. Kode DEV-2 sudah begitu (booking 9.R4). Catatan: kode yang sama punya defect `total_units` (booking 9.R3).

Diputuskan, tetapi kodenya belum mengikuti:
- **OQ-17** — status `REVIEW` dan email review sudah ada, tetapi `Settle` belum menerima maupun membandingkan `amountMinor` (booking 9.R2).
- **OQ-4** — publisher, consumer, dan file schema `otp.requested.v1` sudah ada. Yang belum:
  - `user-service` di enum `producer` envelope (platform 5.2, owner: kamu);
  - `aggregateId` yang masih kosong (user 1.R1).
- **OQ-21** — `publicRef` kini wajib di `Payable` pada kontrak (DEV-2). Tersisa: Booking mengisinya saat `payable` dibuat (booking Task 13) dan Payment memakainya untuk `order_id` (payment Task 3).

Masih terbuka:
- **OQ-15** — jawaban DEV-2 ("`SESSION_EXPIRED` ada di `booking.expired`") menjawab event yang berbeda. Pertanyaannya tentang `outcome` di `booking.journey.ended`, yang memang belum memuat nilai itu. Saat ini kode tidak mengirim `journey.ended` sama sekali untuk sesi habis (booking 10.R1). Usul: pakai `EXPIRED`.
- **OQ-11** — diputuskan tim: data pribadi booking dibersihkan 30 hari setelah event, token push dihapus saat penjualan berakhir, outbox terkirim dihapus setelah 7 hari, log disimpan 30 hari, email pembeli di Booking dienkripsi (platform R13, booking R19). Konfirmasi akhir ke klien tetap perlu.
- **OQ-22** — usulan respons status Payment yang lebih ramping; menunggu FE.
- **OQ-5** — diputuskan: Prometheus client + Grafana/Loki/Alloy (ADR-023). Tinggal implementasi (platform 4.2, 4.5, 4.6).
- **OQ-6** (target uptime): menunggu keputusan.
- **OQ-2, 7, 8, 10, 12, 14, 16, 20**: rekomendasi di tabel dipakai sampai ada keberatan.
