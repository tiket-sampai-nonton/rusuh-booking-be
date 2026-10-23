# Requirements Document — Notification Service

## Metadata service

| Field | Nilai |
|-------|-------|
| Spec | notification-service |
| Service | `services/notification` |
| Port | 8084 |
| Schema / Role | `notification` / `svc_notification` |
| Verb | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` (D-14) |
| Dependency | PostgreSQL 16, RabbitMQ (exchange `rusuh.events`), Mailpit (lokal, CI) / Resend (demo), FCM (cloud) / log adapter (lokal, CI) |
| Produces events | **tidak ada** — murni consumer; tidak otoritatif atas stok/uang/akun |
| Owner | BE-1 |
| Sumber (urut prioritas) | migrasi `0001_init` → kontrak `docs/contracts/openapi/notification.yaml`, `docs/contracts/events/*.schema.json` → SRS §3.1.3 (IF-22/23), §3.2.20–3.2.21, §3.2.25, §3.2.27 → SDD-RBT-001 §4.5.2, §4.6.2 → `docs/analisa/04d-SDD-NOTIFICATION-001.md` v1.1 |

## Introduction

Notification Service mengonsumsi event domain dari Booking, Queue, dan User lalu mengirim **email**
(e-tiket, expired, review, OTP login) dan **push** (giliran). Prinsip: **at-least-once delivery**,
**consumer idempoten tanpa tabel inbox**, **template-based email**, dan — paling penting —
**kegagalan notifikasi tidak pernah membatalkan transaksi** (invarian #6).

### Status Implementasi (2026-10-21, setelah kode DEV-2 masuk)

Semua consumer, API push-subscription, repository delivery, adapter SMTP/FCM/template, dan config sudah ada.
Perbaikan yang tersisa ada di `tasks.md` (0a.R1 klaim retry, 8.R1 breaker SMTP, 0b.R1 adapter push log).
OTP tidak dicatat di `deliveries` (R12.7).

### Divergensi SDD prosa vs kode/kontrak (kode & kontrak menang)

| Topik | SDD-NOTIFICATION prosa | Yang dipakai | Sumber |
|-------|------------------------|--------------|--------|
| Exchange | `rbt.events` | **`rusuh.events`** | `config/load.go` |
| Dedup key | `reference_type/reference_id/type/channel` | **`UNIQUE (source_message_id, notification_type)`** | migrasi |
| Status delivery | `PENDING\|SENT\|DELIVERED\|FAILED` | **`PENDING\|SENDING\|SENT\|RETRY\|DEAD`** | migrasi |
| Retry | "fixed 5s, 3 attempts" | **`RABBITMQ_RETRY_DELAYS=5s,30s,2m` + `RABBITMQ_MAX_RETRIES=3`** | `pkg/rabbitmq` |
| Platform push | `ANDROID\|IOS\|WEB` | **`ANDROID\|IOS`** | migrasi + `notification.yaml` |
| Payload event | field inline (`eventTitle`, `pushToken`, …) | schema v1 (`booking.*.v1`, `queue.turn.*.v1`) | `docs/contracts/events/` |
| Routing key / queue | `booking.paid`, queue gabungan `notification.booking.events` | **`booking.paid.v1`**, queue per tujuan `notification.booking-paid`, … (SDD-RBT §4.6.2, K8) | SDD-RBT §4.6.2 |

## Glossary

- **Delivery** — satu baris `deliveries` per `(source_message_id, notification_type)`; status `PENDING → SENDING → SENT` atau `→ RETRY → DEAD`.
- **source_message_id** — `messageId` envelope (= `outbox_events.id` produsen) — kunci dedup stabil.
- **Push subscription** — token FCM perangkat per `(visitor_id, event_id)`, disimpan terenkripsi.
- **Poison message** — payload rusak/tidak dikenal → `rabbitmq.Permanent(err)` → DLQ tanpa retry.

---

## Requirement 1 — Email E-tiket [target]

**User Story:** Sebagai pembeli yang baru menyelesaikan pembayaran, saya ingin menerima e-tiket lewat email, agar punya bukti dan kode masuk venue.

Trace: SRS-F-20-02, SRS-F-20-03, SRS-F-21-01, SRS-IF-22 · TC-TKT-02, TC-PAY-03 · `booking.paid.v1.schema.json`

### Acceptance Criteria (EARS)

1. WHEN `booking.paid.v1` diterima (queue `notification.booking-paid`), THE service SHALL merender template `eticket` dan mengirim email teks ke `buyerEmail` berisi `event.name`, `event.startsAt`, `event.venue`, `publicRef`, daftar `tickets` (`ticketCode`, `attendeeName`, `attendeeNikMasked`, `categoryCode`), dan tautan halaman tiket bertanda tangan (`ticketLinkToken`) **bila ada** — opsional sejak 2026-10-23 karena Booking belum menerbitkan token tersebut (payment Task 15.3).
2. WHEN email terkirim, THE service SHALL memperbarui delivery `notification_type='BOOKING_PAID'`, `channel='EMAIL'` menjadi `SENT` dengan `sent_at` dan `provider_message_id`.
3. IF payload tidak lengkap/korup sehingga template gagal dirender, THEN THE service SHALL memperlakukannya sebagai poison (`rabbitmq.Permanent`), menandai delivery `DEAD`, tanpa retry.
4. THE email SHALL menampilkan NIK hanya dalam bentuk tersamar (mis. `3174********0001`).

## Requirement 2 — Email Booking Expired [target]

**User Story:** Sebagai pembeli yang pesanannya kedaluwarsa atau dibatalkan, saya ingin pemberitahuan, agar tahu statusnya.

Trace: SRS-F-17-02, SRS-F-21-01, SRS-F-21-03 · TC-PAY-04, TC-MSG-01 · `booking.expired.v1.schema.json`

### Acceptance Criteria (EARS)

1. WHEN `booking.expired.v1` diterima (queue `notification.booking-expired`), THE service SHALL mengirim email template `expired` berisi `publicRef` dan alasan (`PAYMENT_TIMEOUT` | `SESSION_EXPIRED` | `CANCELLED`) dan mencatat delivery `BOOKING_EXPIRED`/`EMAIL`.
2. IF SMTP gagal sementara, THEN THE service SHALL me-retry lewat retry queue dan mengirim setelah pulih (TC-MSG-01); status booking tidak terpengaruh (SRS-F-21-03).

## Requirement 3 — Email Review (pembayaran terlambat) [target]

**User Story:** Sebagai pembeli yang pembayarannya diterima setelah batas waktu, saya ingin penjelasan dan langkah pengembalian dana, agar tidak bingung.

Trace: SRS-F-17-05, SRS-F-21-01 · TC-PAY-07 · `booking.review.v1.schema.json`

### Acceptance Criteria (EARS)

1. WHEN `booking.review.v1` diterima (queue `notification.booking-review`), THE service SHALL mengirim email template `review` berisi referensi booking, `amountMinor` diformat IDR, dan pesan "Pembayaran diterima setelah batas waktu; dana dikembalikan dalam X hari kerja", lalu mencatat delivery `BOOKING_REVIEW`/`EMAIL`. *(Nilai X tidak ditetapkan SRS — konten template dikonfirmasi Ops.)*

## Requirement 4 — Push "Giliran sebentar lagi" [target]

**User Story:** Sebagai pembeli di waiting room mobile, saya ingin notifikasi saat giliran hampir tiba, agar bersiap.

Trace: SRS-F-25-10, SRS-F-25-12, SRS-IF-23 · TC-QUE-13 · `queue.turn.soon.v1.schema.json`

### Acceptance Criteria (EARS)

1. WHEN `queue.turn.soon.v1` diterima (queue `notification.turn-soon`), THE service SHALL mencari subscription `(visitorId, eventId)` dan mengirim push `TURN_SOON` ("Giliran Anda sebentar lagi. Tetap buka halaman ini."), lalu mencatat delivery `TURN_SOON`/`PUSH`.
2. IF tidak ada subscription, THEN THE service SHALL ack tanpa error dan tanpa mengirim.
3. IF provider melaporkan token tidak valid, THEN THE service SHALL menghapus subscription dan ack (tanpa retry).

## Requirement 5 — Push "Giliran tiba" dengan cek deadline [target]

**User Story:** Sebagai pembeli, saya ingin notifikasi saat giliran tiba, agar segera claim dalam jendela klaim.

Trace: SRS-F-06-05, SRS-F-25-11, SRS-F-25-12 · TC-QUE-13 · `queue.turn.arrived.v1.schema.json`

### Acceptance Criteria (EARS)

1. WHEN `queue.turn.arrived.v1` diterima (queue `notification.turn-arrived`), THE service SHALL mengirim push `TURN_ARRIVED` ("Giliran Anda tiba, buka dalam 3 menit") dengan data `eventId`, `queueEntryId`, `claimDeadline`.
2. IF `claimDeadline` sudah lewat saat diproses, THEN THE service SHALL membuang pesan (ack, delivery dengan `expires_at` = `claimDeadline`) karena push terlambat tidak berguna (SRS-F-25-12).
3. IF tidak ada subscription atau token invalid, THEN perilaku SHALL sama dengan R4.2–4.3.

## Requirement 6 — Pelacakan Delivery [target]

**User Story:** Sebagai operator, saya ingin status setiap pengiriman tercatat, agar dapat mengukur keberhasilan dan menelusuri kegagalan.

Trace: SRS-F-21-02, SRS-D-07 · TC-MSG-02, TC-MSG-03

### Acceptance Criteria (EARS)

1. WHEN event mulai diproses, THE service SHALL memegang tepat satu baris `deliveries` per `(source_message_id, notification_type)` yang bergerak `PENDING → SENDING → SENT`, atau `→ RETRY → DEAD`.
2. THE baris SHALL menyimpan `attempts`, `last_error` (tanpa PII), `provider_message_id`, `next_attempt_at`, dan `recipient_hash` (HMAC), **bukan** alamat/token plaintext.

## Requirement 7 — API Push Subscription [target]

**User Story:** Sebagai aplikasi mobile, saya ingin mendaftarkan/mencabut token push perangkat untuk sebuah event, agar dapat menerima notifikasi giliran.

Trace: SRS-F-25-12, SRS-D-07 · TC-QUE-13 · `notification.yaml`

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/push-subscriptions` dengan visitor token valid dan body `{ eventId, platform ∈ {ANDROID, IOS}, pushToken (≤ 4096) }`, THE service SHALL menyimpan subscription dengan `push_token_enc` (AES-256-GCM) dan PK `(visitor_id, event_id)`, lalu membalas `204`.
2. IF subscription `(visitor_id, event_id)` sudah ada, THEN THE service SHALL meng-upsert tanpa duplikasi.
3. WHEN `DELETE /api/v1/push-subscriptions/{eventId}` dengan visitor token, THE service SHALL menghapus subscription dan membalas `204`.
4. IF validasi gagal, THEN `400 VALIDATION_ERROR` dalam `ErrorEnvelope`.

## Requirement 8 — Consumer Idempoten tanpa Inbox [target]

**User Story:** Sebagai sistem, saya ingin event terkirim ganda tidak menghasilkan email/push ganda setelah sukses tercatat.

Trace: SRS-F-21-02, SRS-Q-FUN-04, SRS-Q-REL-01 · TC-MSG-03

### Acceptance Criteria (EARS)

1. WHEN event yang sama diterima lebih dari sekali, THE service SHALL mengirim paling banyak satu kali setelah `SENT` tercatat, dijaga `INSERT … ON CONFLICT (source_message_id, notification_type) DO NOTHING` — tanpa tabel inbox.
2. IF baris delivery sudah ada, THEN THE service SHALL melanjutkan hanya bila status `PENDING`/`RETRY` (lanjutan percobaan); `SENT`/`DEAD`/`SENDING` yang masih dipegang → ack tanpa mengirim. At-least-once email diterima apa adanya bila crash terjadi antara kirim dan pencatatan `SENT` (SDD-RBT §4.5.2).

## Requirement 9 — Retry Berjenjang + DLQ [target]

**User Story:** Sebagai sistem, saya ingin kegagalan transien dicoba ulang dan kegagalan permanen dipindah ke DLQ, agar satu pesan rusak tidak memblok antrean.

Trace: SRS-F-21-02, SRS-F-90-02, SRS-F-19-03 · TC-MSG-01, TC-MSG-02, TC-MSG-04

### Acceptance Criteria (EARS)

1. WHEN handler gagal transien, THE service SHALL mem-publish ulang ke `<queue>.retry.<delay>` dengan delay dari `RABBITMQ_RETRY_DELAYS` (`5s,30s,2m`) dan menaikkan header percobaan (`pkg/rabbitmq`).
2. WHEN percobaan > `RABBITMQ_MAX_RETRIES` (3), THE service SHALL memindahkan pesan ke `<queue>.dlq` dan menandai delivery `DEAD`.
3. IF payload rusak atau `schemaVersion` tidak dikenal, THEN `rabbitmq.Permanent(err)` → DLQ langsung.
4. WHEN pesan retry diproses ulang dan berhasil, THE service SHALL ack dan menandai `SENT` (TC-MSG-04).
5. THE pengiriman SMTP SHALL melewati `pkg/resilience` breaker + bulkhead (PROPOSED SDD-RBT §4.6.2: timeout 3 s, ambang 5, cooldown 30 s, probe 1, bulkhead 10, retry maks 3); breaker OPEN diperlakukan sebagai kegagalan transien.
6. Redrive DLQ SHALL hanya lewat tool teraudit (`tools/redrive`, platform R9.3).

## Requirement 10 — Health Endpoint [implemented]

Trace: D-23 · `server_test.go`

### Acceptance Criteria (EARS)

1. THE service SHALL mengekspos `GET /health` → `200 UP` bila PostgreSQL & RabbitMQ sehat dalam `HEALTH_TIMEOUT`, `503 DOWN` bila tidak. ✅

## Requirement 11 — Konfigurasi Fail-fast & Privasi

Trace: D-24, SRS-Q-SEC-03, SRS-IF-23 · TC-SEC-03, TC-USR-11

### Acceptance Criteria (EARS)

1. WHEN service start, THE service SHALL memvalidasi semua env sekaligus dan keluar non-zero bila invalid. ✅ *(subset `APP_*`, `LOG_*`, `POSTGRES_*`, `RABBITMQ_*`)*
2. THE config SHALL diperluas untuk `SMTP_*`, `PUSH_PROVIDER ∈ {fcm, log}` (`log` di lokal/CI, SDD-RBT §4.10, SRS-IF-23), `FCM_*`, kunci enkripsi push token, kunci HMAC `recipient_hash`, `PII_ENC_KEY` (dekripsi OTP, sama dengan User) — semuanya divalidasi fail-fast.
3. THE service SHALL tidak menulis kode OTP, token, alamat email, atau NIK plaintext ke log; hanya identifier (`messageId`, `bookingId`, `queueEntryId`).
4. THE data yang diproses SHALL hanya PII yang memang ada di schema event (`buyerEmail`, `attendeeNikMasked`, `ticketLinkToken`); token push hanya disimpan terenkripsi.
5. WHEN `push_subscriptions.expires_at` lewat (penjualan berakhir), THE job worker SHALL menghapus subscription itu; baris `deliveries` hanya menyimpan `recipient_hash` dan dibersihkan setelah `PII_RETENTION_DAYS_AFTER_EVENT` (platform R13).

## Requirement 12 — Email OTP Login [implemented; kontrak OQ-4]

**User Story:** Sebagai pembeli yang meminta OTP login, saya ingin menerima kode lewat email dengan cepat, agar dapat login sebelum kode kedaluwarsa.

Trace: SRS-F-27-02, SRS-F-27-12 · TC-MSG-05, TC-MSG-06, TC-USR-11 · ADR-020 · SDD-NOTIFICATION v1.1 §5.4

### Acceptance Criteria (EARS)

1. WHEN `otp.requested.v1` diterima (queue `notification.otp`), THE service SHALL mendekripsi `emailEnc` dan `codeEnc` dengan `PII_ENC_KEY` (sama dengan User Service), merender template `otp_login`, dan mengirim email. *(Schema event belum ada di kontrak — OQ-4.)*
2. IF `expiresAt` sudah lewat, THEN THE service SHALL membuang pesan (ack).
3. IF dekripsi gagal, THEN poison → `rabbitmq.Permanent` → DLQ tanpa retry (TC-MSG-06).
4. WHEN SMTP gagal, THE service SHALL **tidak** me-retry (time-sensitive) dan meng-ack; pengguna dapat meminta OTP baru.
5. THE kode OTP SHALL tidak pernah muncul di log, `deliveries.last_error`, atau metrik (TC-USR-11).
6. THE email SHALL terkirim dalam 5 detik sejak publish dalam kondisi normal (TC-MSG-05).
7. THE OTP SHALL tidak dicatat di `deliveries` (skema `0001` tidak mendukung `OTP_LOGIN`); bila pelacakan OTP dibutuhkan, tambah migrasi `0002` lebih dulu.

---

## Requirement 13 — Penyedia email dapat dipilih (Mailpit / Resend)

**User Story:** Sebagai developer, saya ingin memilih penyedia email lewat satu env, agar lokal dan CI memakai Mailpit (tidak ada email keluar) dan demo memakai Resend untuk mengirim email sungguhan.

Trace: SRS-IF-22, SRS-F-21-03, SRS-Q-REL-01, SRS-Q-SEC-03 · TC-MSG-01, TC-TKT-02, TC-MSG-05 · keputusan tim 2026-10-21 (SRS-IF-22 diperbarui: SES diganti Resend)

### Acceptance Criteria (EARS)

1. THE service SHALL memilih penyedia dari `EMAIL_PROVIDER ∈ {mailpit, resend}` (default `mailpit`); nilai lain membuat start gagal (fail-fast).
2. WHERE `EMAIL_PROVIDER = mailpit`, THE service SHALL mengirim lewat SMTP (`SMTP_HOST`, `SMTP_PORT`) ke Mailpit dan tidak melakukan panggilan HTTP keluar ke penyedia email mana pun.
3. WHERE `EMAIL_PROVIDER = resend`, THE service SHALL mengirim lewat `POST https://api.resend.com/emails` dengan header `Authorization: Bearer <RESEND_API_KEY>`, body `{from, to, subject, text}`; `EMAIL_FROM` (domain terverifikasi di Resend) dan `RESEND_API_KEY` wajib terisi, bila tidak start gagal.
4. THE `id` dari respons Resend SHALL disimpan sebagai `deliveries.provider_message_id`.
5. THE request ke Resend SHALL membawa header `Idempotency-Key` = `source_message_id` + jenis notifikasi, sehingga pengiriman ulang dalam 24 jam tidak menghasilkan email ganda di sisi penyedia.
6. THE klasifikasi error Resend SHALL: `429`, `5xx`, timeout, dan gangguan jaringan = sementara (retry berjenjang); `400`, `401`, `403` (domain belum terverifikasi atau hanya boleh ke alamat sendiri), `422` = permanen (`rabbitmq.Permanent`, delivery `DEAD`, tanpa retry).
7. THE `RESEND_API_KEY` SHALL tidak pernah muncul di log, `last_error`, payload event, atau pesan error; alamat penerima tidak dilog.
8. WHEN service start dengan `EMAIL_PROVIDER = resend`, THE service SHALL mencatat satu peringatan bahwa email nyata akan dikirim (tanpa memuat key), agar tidak tertukar dengan mode Mailpit.
9. THE pengiriman lewat kedua penyedia SHALL melewati breaker dan bulkhead yang sama (R9.5); `429` tidak membuka breaker lebih cepat dari kegagalan lain.
10. THE CI dan test otomatis SHALL selalu memakai `mailpit` atau fake; test tidak pernah memanggil Resend nyata.
11. THE penggunaan Resend SHALL dibatasi untuk demo karena akun gratis hanya 100 email/hari dan 3.000/bulan; bila kuota habis, respons `429` diperlakukan sementara dan pesan masuk DLQ setelah retry berjenjang (redrive manual setelah kuota direset tengah malam UTC).

## Invarian #6 — notifikasi tidak pernah membatalkan transaksi

- Notification **tidak** memegang `IUnitOfWork` lintas service, **tidak** menulis stok/uang/akun, dan **tidak** mempublikasikan event balik.
- Bila semua retry habis dan pesan masuk DLQ, transaksi sumber tetap final; efeknya hanya alert `NotificationDLQNotEmpty`.

## Build & Verify

```bash
make infra                          # PostgreSQL, RabbitMQ, Mailpit
cd services/notification && cp .env.example .env
go run ./cmd migrate up && go run ./cmd api & go run ./cmd worker &
curl -fsS localhost:8084/health
make build && make test && make test-integration && make lint && make cover && make ci
```

## Trace matrix

| Req | SRS | Test Case | Status |
|-----|-----|-----------|--------|
| 1 E-tiket | F-20-02/03, F-21-01, IF-22 | TC-TKT-02, TC-PAY-03 | target |
| 2 Expired | F-17-02, F-21-01/03 | TC-PAY-04, TC-MSG-01 | target |
| 3 Review | F-17-05, F-21-01 | TC-PAY-07 | target |
| 4 Turn soon | F-25-10/12, IF-23 | TC-QUE-13 | target |
| 5 Turn arrived | F-06-05, F-25-11/12 | TC-QUE-13 | target |
| 6 Delivery | F-21-02, D-07 | TC-MSG-02/03 | target |
| 7 Push subscription | F-25-12, D-07 | TC-QUE-13 | target |
| 8 Idempoten | F-21-02, Q-FUN-04, Q-REL-01 | TC-MSG-03 | target |
| 9 Retry + DLQ | F-21-02, F-90-02, F-19-03 | TC-MSG-01/02/04 | target |
| 10 Health | D-23 | server_test.go | **implemented** |
| 11 Config & privasi | D-24, Q-SEC-03, IF-23 | TC-SEC-03, TC-USR-11 | partial |
| 12 OTP | F-27-02/12 | TC-MSG-05/06, TC-USR-11 | target, blocked OQ-4 |
| 13 Penyedia email | IF-22, F-21-03, Q-REL-01 | TC-MSG-01, TC-MSG-05 | target |
