# Requirements — Notification Service

| Field | Nilai |
|-------|-------|
| Service | Notification Service |
| Spec dari | `docs/sdd/SDD-NOTIFICATION-001.md` (LLD, source of truth) |
| Port | 8084 |
| Schema | `notification` (role `svc_notification`) |
| Verb | `api`, `worker`, `migrate`, `healthcheck` |
| Dependency | PostgreSQL, RabbitMQ, SMTP (SES/Mailpit), FCM |
| Owner | BE-1 |

## Status implementasi (ground-truth vs SDD)

Spec ini membedakan **apa yang sudah ada di kode** dari **target SDD** yang belum
dibangun. Hasil telusur `services/notification/`:

- **Sudah ada (boilerplate):** struktur hexagonal lengkap, Cobra verb (`api`,
  `worker`, `migrate up|down|status`, `healthcheck`), config tervalidasi
  (`internal/config`), client PostgreSQL + RabbitMQ (`internal/delivery/clients.go`),
  `GET /health` dengan ping dependency (`infrastructure/http/health_handler.go`),
  dan migrasi `0001_init` (tabel `deliveries`, `push_subscriptions`).
- **Belum ada (target SDD, mulai Sprint 1):** consumer RabbitMQ — `jobs()` di
  `internal/delivery/worker.go` masih mengembalikan `nil` dan tidak ada
  `Subscription` yang didaftarkan; adapter SMTP, FCM, template email; usecase
  pengiriman; repository `deliveries`/`push_subscriptions`; endpoint
  `POST/DELETE /api/v1/push-subscriptions`.
- **Divergensi SDD vs kode** (kode menang; SDD prosa dikoreksi di bawah):
  - Exchange = `rusuh.events` (bukan `rbt.events` seperti prosa SDD §5.2).
  - Dedup delivery = `UNIQUE (source_message_id, notification_type)` di migrasi
    v4.6 (bukan `reference_type/reference_id/type/channel` seperti SDD §5.4).
  - Retry berjenjang dari `RABBITMQ_RETRY_DELAYS=5s,30s,2m` + `RABBITMQ_MAX_RETRIES=3`
    (bukan "fixed 5s, 3 attempts" seperti SDD §5.3); konsisten dengan
    `pkg/rabbitmq`.
  - Status delivery = `PENDING|SENDING|SENT|RETRY|DEAD` (migrasi v4.6), bukan
    `PENDING|SENT|DELIVERED|FAILED` (SDD §4.1).

Setiap requirement diberi tag **[implemented]** atau **[target]**.

## Ringkasan

Notification Service meng-*consume* domain event dari Booking dan Queue, lalu
mengirim email (e-tiket, expired, review) dan push notification (giliran) ke
pembeli. Prinsipnya: **at-least-once delivery**, **idempotent consumer tanpa
tabel inbox**, dan **kegagalan notifikasi tidak pernah membatalkan transaksi**
(Invarian §6). Service ini tidak otoritatif atas stok, uang, atau akun — ia hanya
sink event dan penyedia endpoint push-subscription.

## Trace matrix

| Req | Kapabilitas | SRS | Test Case (TP) | Status |
|-----|-------------|-----|----------------|--------|
| R1 | E-ticket email | SRS-F-20-01..03 | TC-TKT-01, TC-TKT-02 | target |
| R2 | Expiry & review email | SRS-F-21-01..03 | TC-MSG-01 | target |
| R3 | Turn soon / arrived push | SRS-F-25-01..13 | TC-QUE-04, E2E | target |
| R4 | Delivery tracking | SRS-F-21 | TC-MSG-01 | target |
| R5 | Push subscription API | SRS-F-25 | — | target |
| R6 | Idempotent consumer | SRS-Q-REL-01 | TC-MSG-03 | target |
| R7 | Retry berjenjang + DLQ | SRS-Q-REL-01 | TC-MSG-01, TC-MSG-02, TC-MSG-04 | target |
| R8 | Notifikasi tidak membatalkan transaksi | Invarian §6 | TC-MSG-01 | target |
| R9 | Health endpoint | D-23 | server_test.go | implemented |
| R10 | Konfigurasi fail-fast | D-24 | load_test.go | implemented |
| R11 | Privasi payload (tanpa PII plaintext) | SRS keamanan | — | target |

---

## R1 — E-ticket email [target]

**User story:** Sebagai pembeli yang baru menyelesaikan pembayaran, saya ingin
menerima e-tiket lewat email agar punya bukti dan kode masuk venue.

Sumber: SRS-F-20-01..03 · TC-TKT-01, TC-TKT-02 · SDD §2, §6.2, Appendix A.1.

- **WHEN** event `booking.paid` diterima dari exchange `rusuh.events`, **THE**
  Notification Service **SHALL** merender template `eticket` dan mengirim email
  ke alamat pembeli (didekripsi dari `buyerEmail`) berisi event, tanggal, venue,
  daftar tiket (kode publik + NIK tersamar), dan total.
- **WHEN** email e-tiket berhasil terkirim, **THE** service **SHALL** mencatat
  delivery dengan `notification_type='BOOKING_PAID'`, `channel='EMAIL'`,
  `status='SENT'`, dan `sent_at`.
- **IF** template gagal dirender karena payload tidak lengkap, **THEN THE**
  service **SHALL** memperlakukan event sebagai *poison* (permanent) dan tidak
  me-retry (lihat R7).
- **WHERE** email terkirim, **THE** isi **SHALL** memuat kode tiket/QR agar bisa
  diverifikasi di pintu masuk (TC-TKT-01).

## R2 — Expiry & review email [target]

**User story:** Sebagai pembeli yang bookingnya kedaluwarsa atau pembayarannya
terlambat, saya ingin pemberitahuan agar tahu status dan langkah berikutnya.

Sumber: SRS-F-21-01..03 · TC-MSG-01 · SDD §2, §6.3–§6.4, Appendix A.2.

- **WHEN** event `booking.expired` diterima, **THE** service **SHALL** mengirim
  email dari template `expired` (booking id + total) dan mencatat delivery
  `notification_type='BOOKING_EXPIRED'`.
- **WHEN** event `booking.review` diterima, **THE** service **SHALL** mengirim
  email dari template `review` dan mencatat delivery
  `notification_type='BOOKING_REVIEW'`.
- **IF** SMTP down saat pengiriman, **THEN THE** service **SHALL** me-retry lewat
  retry queue dan mengirim setelah pemulihan (TC-MSG-01), bukan menandai gagal
  permanen.

## R3 — Turn soon / arrived push [target]

**User story:** Sebagai pembeli di waiting room, saya ingin notifikasi push saat
giliran saya hampir atau sudah tiba agar tidak kehilangan slot.

Sumber: SRS-F-25-01..13 · TC-QUE-04, E2E · SDD §7, Appendix A.3.

- **WHEN** event `queue.turn.soon` diterima (posisi ≤ 10), **THE** service
  **SHALL** mengirim push "Giliran Sebentar Lagi" ke token subscription pembeli.
- **WHEN** event `queue.turn.arrived` diterima, **THE** service **SHALL**
  mengirim push "Giliran Anda Tiba!" dengan data `entryId`, `eventId`, `type`,
  dan `claimDeadline`.
- **IF** `claimDeadline` sudah lewat saat event diproses, **THEN THE** service
  **SHALL** melewati pengiriman (ack, buang) karena push yang telat tidak berguna
  (kolom `expires_at` di `deliveries`).
- **IF** tidak ada subscription untuk pembeli, **THEN THE** service **SHALL**
  melewati pengiriman (ack) tanpa error.
- **IF** FCM melaporkan token tidak valid, **THEN THE** service **SHALL**
  menghapus subscription dan ack (bukan retry).

## R4 — Delivery tracking [target]

**User story:** Sebagai operator, saya ingin status setiap pengiriman tercatat
agar bisa mengukur keberhasilan dan menelusuri kegagalan.

Sumber: SRS-F-21 · SDD §4.1.

- **WHEN** sebuah event mulai diproses, **THE** service **SHALL** memegang satu
  baris `deliveries` per `(source_message_id, notification_type)` sebagai catatan
  status (PENDING → SENDING → SENT, atau → RETRY → DEAD).
- **THE** service **SHALL** menyimpan `attempts`, `last_error`, `provider_message_id`,
  dan `recipient_hash` (bukan recipient plaintext) di setiap baris.

## R5 — Push subscription API [target]

**User story:** Sebagai aplikasi mobile, saya ingin mendaftarkan/mencabut token
FCM perangkat agar bisa menerima push untuk sebuah event.

Sumber: SRS-F-25 · SDD §3.1–§3.2, Appendix B.

- **WHEN** `POST /api/v1/push-subscriptions` dengan header `X-Visitor-Token` valid
  dan body `{eventId, platform, token}`, **THE** service **SHALL** menyimpan
  subscription (token dienkripsi) dengan PK `(visitor_id, event_id)`.
- **IF** subscription untuk `(visitor_id, event_id)` sudah ada, **THEN THE**
  service **SHALL** memperlakukannya idempoten (upsert / 409 `SUBSCRIPTION_EXISTS`
  sesuai Appendix B) tanpa menduplikasi baris.
- **WHEN** `DELETE /api/v1/push-subscriptions/{eventId}`, **THE** service
  **SHALL** menghapus subscription pembeli untuk event itu.
- **IF** validasi field gagal, **THEN THE** service **SHALL** mengembalikan 400
  `VALIDATION_ERROR` lewat `pkg/apperror`.

## R6 — Idempotent consumer tanpa tabel inbox [target]

**User story:** Sebagai sistem, saya ingin event yang terkirim ganda (jaminan
at-least-once) tidak menghasilkan email/push ganda.

Sumber: SRS-Q-REL-01 · TC-MSG-03 · SDD §5.4, §9.8 · code-principles.

- **WHEN** event yang sama diterima lebih dari sekali, **THE** service **SHALL**
  mengirim notifikasi tepat satu kali, dijaga oleh `INSERT ... ON CONFLICT
  (source_message_id, notification_type) DO NOTHING` (tanpa tabel inbox terpisah).
- **IF** `INSERT` delivery mengembalikan 0 baris (konflik), **THEN THE** service
  **SHALL** menyimpulkan event sudah/ sedang diproses dan ack tanpa mengirim ulang.

## R7 — Retry berjenjang + DLQ [target]

**User story:** Sebagai sistem, saya ingin kegagalan transien dicoba ulang dan
kegagalan permanen dipindah ke DLQ agar satu pesan rusak tidak memblok antrean.

Sumber: SRS-Q-REL-01 · TC-MSG-01, TC-MSG-02, TC-MSG-04 · SDD §5.3 · `pkg/rabbitmq`.

- **WHEN** handler gagal transien, **THE** service **SHALL** mem-publish ulang
  pesan ke retry queue `<queue>.retry.<delay>` dengan delay dari
  `RABBITMQ_RETRY_DELAYS` (default `5s,30s,2m`) dan menaikkan header
  `x-rbt-attempt`.
- **WHEN** jumlah percobaan melebihi `RABBITMQ_MAX_RETRIES` (default 3), **THE**
  service **SHALL** memindahkan pesan ke `<queue>.dlq`.
- **IF** payload rusak atau versi schema tidak dikenal, **THEN THE** service
  **SHALL** mengembalikan `rabbitmq.Permanent(err)` sehingga pesan langsung ke DLQ
  tanpa retry.
- **WHEN** retry pertama diproses ulang, **THE** service **SHALL** membawa header
  `x-rbt-attempt=2` lalu ack setelah sukses (TC-MSG-04).

## R8 — Kegagalan notifikasi tidak membatalkan transaksi [target]

**User story:** Sebagai sistem, saya ingin kegagalan kirim email/push tidak
pernah merusak booking/payment yang sudah final.

Sumber: Invarian §6 · invariants.md.

- **THE** Notification Service **SHALL** hanya meng-*consume* event; ia **SHALL
  NOT** mempublikasikan event yang mengubah stok/uang/akun atau memanggil balik
  Booking/Payment untuk membatalkan.
- **IF** semua retry habis dan pesan ke DLQ, **THEN THE** transaksi sumber
  **SHALL** tetap final; hanya alert DLQ yang dipicu (lihat observability).

## R9 — Health endpoint [implemented]

Sumber: D-23 · `internal/delivery/server.go`, `health_handler.go`, `server_test.go`.

- **THE** service **SHALL** mengekspos `GET /health` yang mengembalikan 200 UP
  bila semua dependency (Postgres, RabbitMQ) sehat dalam `HEALTH_TIMEOUT`, dan
  503 DOWN bila ada yang gagal. (Terimplementasi; dipakai ALB + container
  HEALTHCHECK.)

## R10 — Konfigurasi fail-fast [implemented]

Sumber: D-24 · `internal/config/config.go`, `load.go`, `load_test.go`.

- **WHEN** service start, **THE** service **SHALL** memuat dan memvalidasi semua
  env (`APP_*`, `LOG_*`, `POSTGRES_*`, `RABBITMQ_*`) sekaligus dan keluar non-zero
  bila ada yang invalid. (Terimplementasi.)

## R11 — Privasi payload [target]

Sumber: security-privacy.md · SRS keamanan · `pkg/eventstore`.

- **THE** payload event yang diproses **SHALL NOT** memuat NIK/email/token
  plaintext; email pembeli diterima terenkripsi dan `recipient` disimpan sebagai
  `recipient_hash`.
- **THE** service **SHALL NOT** menulis kode OTP, token, atau PII plaintext ke
  log; hanya identifier (messageId, bookingId, entryId).
