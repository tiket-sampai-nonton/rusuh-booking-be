# Requirements — Notification Service

## 1. Metadata service

| Field | Nilai |
|-------|-------|
| Service | Notification Service |
| Document ID | SPEC-NOTIFICATION-REQ (dari `docs/sdd/SDD-NOTIFICATION-001.md`, LLD = source of truth) |
| Port | 8084 |
| Schema | `notification` (role `svc_notification`, hanya melihat schema sendiri) |
| Verb | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` (D-14) |
| Dependency | PostgreSQL 16, RabbitMQ (exchange `rusuh.events`), SMTP (SES/Mailpit), FCM |
| Produces events | **tidak ada** (Notification hanya *consume*; tidak otoritatif atas stok/uang/akun) |
| Owner | BE-1 |
| Region | ap-southeast-3 (Jakarta) |
| Sumber | SDD-NOTIFICATION-001 v1.1 §1–§12 + Appendix A–D; kontrak `docs/contracts/`; Test Plan `docs/test/06-TP-RBT-001.md` |

Notification Service meng-*consume* domain event dari Booking dan Queue lalu
mengirim **email** (e-tiket, expired, review, OTP login) dan **push notification** (giliran)
ke pembeli. Prinsip: **at-least-once delivery**, **idempotent consumer tanpa
tabel inbox**, **template-based email**, dan — paling penting — **kegagalan
notifikasi tidak pernah membatalkan transaksi** (Invarian §6).

---

## 2. Status delta (ground-truth kode vs target SDD)

Spec ini membedakan **apa yang sudah ada di `services/notification/`** dari
**target SDD** yang belum dibangun. Telusur per 2026-10-20:

| Area | Status | Bukti di kode |
|------|--------|---------------|
| Struktur hexagonal (port/domain/application/infrastructure) | **ada** | `internal/notification/...` |
| Cobra verb `api`/`worker`/`migrate`/`healthcheck` | **ada** | `cmd/*.go` |
| Config fail-fast (D-24) | **ada** | `internal/config/{config.go,load.go}`, `load_test.go` |
| Client Postgres + RabbitMQ | **ada** | `internal/delivery/clients.go` |
| `GET /health` deep (ping dependency) | **ada** | `infrastructure/http/health_handler.go`, `server_test.go` |
| Migrasi `0001_init` (`deliveries`, `push_subscriptions`) | **ada** | `deployments/migrations/0001_init.up.sql` |
| **Consumer RabbitMQ** (`booking.*`, `queue.turn.*`) | **TARGET** | `internal/delivery/worker.go` → **`jobs()` mengembalikan `nil`**, tidak ada `Subscription` didaftarkan |
| Adapter SMTP / FCM / template | **TARGET** | folder `infrastructure/{smtp,fcm,template}` belum ada |
| **Consumer `otp.requested`** (OTP login email) | **TARGET** | queue `notification.otp` belum didaftarkan |
| Usecase pengiriman + repository `deliveries`/`push_subscriptions` | **TARGET** | hanya usecase `check_health` yang ada |
| Endpoint `POST`/`DELETE /api/v1/push-subscriptions` | **TARGET** | hanya `/health` terpasang di `server.go` |

> **Catatan `jobs()`:** `internal/delivery/worker.go` memiliki `func jobs(*Clients) []Job { return nil }`
> dan `worker_test.go` menegaskan `assert.Empty(t, jobs(&Clients{}))`. Worker saat ini
> hanya menyajikan `/health` dan menjalankan loop job kosong tiap `WORKER_INTERVAL`.
> Target bukan menambah periodic *job* ke `jobs()`, melainkan mendaftarkan
> `rabbitmq.Subscription` (consumer event-driven) di `RunWorker` — lihat design.md §4.

### Divergensi SDD prosa vs kode/kontrak (kode & kontrak menang)

| Topik | SDD prosa | Ground-truth (menang) | Sumber |
|-------|-----------|------------------------|--------|
| Exchange | `rbt.events` (§5.2) | **`rusuh.events`** | `config/load.go`, `.env.example` |
| Dedup key | `reference_type/reference_id/type/channel` (§5.4) | **`UNIQUE (source_message_id, notification_type)`** | migrasi `0001_init` |
| Status delivery | `PENDING\|SENT\|DELIVERED\|FAILED` (§4.1) | **`PENDING\|SENDING\|SENT\|RETRY\|DEAD`** | migrasi `0001_init` |
| Retry | "fixed 5s, 3 attempts" (§5.3) | **`RABBITMQ_RETRY_DELAYS=5s,30s,2m` + `RABBITMQ_MAX_RETRIES=3`** | `config/load.go`, `pkg/rabbitmq` |
| Platform push | `ANDROID\|IOS\|WEB` (§4.2) | **`ANDROID\|IOS`** saja | migrasi + `notification.yaml` |
| Envelope | field `eventTitle/venueName/totalAmount` inline | **`messageId, eventType, schemaVersion(int), occurredAt, producer(enum), aggregateId, correlationId, payload`** | `pkg/rabbitmq/envelope.go`, `envelope.schema.json` |
| Payload `booking.paid` | email terenkripsi base64 + `eventTitle/eventDate/venueName/totalAmount` | **`bookingId, publicRef, buyerEmail(email), event{eventId,name,startsAt,venue}, tickets[{ticketCode,attendeeName,attendeeNikMasked,categoryCode}], ticketLinkToken`** | `booking.paid.v1.schema.json` |
| Payload `booking.expired` | `eventTitle, amount, expiredAt` | **`bookingId, publicRef, buyerEmail, reason(PAYMENT_TIMEOUT\|SESSION_EXPIRED\|CANCELLED)`** | `booking.expired.v1.schema.json` |
| Payload `booking.review` | `eventTitle, amount` | **`bookingId, attemptHint?, buyerEmail, amountMinor(int64)`** | `booking.review.v1.schema.json` |
| Payload `queue.turn.*` | `pushToken/platform/position` inline | **`eventId, queueEntryId, visitorId` (+`claimDeadline` untuk arrived)** — token dilookup dari `push_subscriptions` | `queue.turn.*.v1.schema.json` |

Setiap requirement di bawah ditandai **[implemented]** atau **[target]**.

---

## 3. Trace matrix

| Req | Kapabilitas | SRS | Test Case (TP) | Status |
|-----|-------------|-----|----------------|--------|
| R1 | E-ticket email | SRS-F-20-01..03 | TC-TKT-01, TC-TKT-02 | target |
| R2 | Expiry email | SRS-F-21-01..03 | TC-MSG-01 | target |
| R3 | Review email | SRS-F-21-01..03 | TC-MSG-01 | target |
| R4 | Turn soon push | SRS-F-25-01..13 | TC-QUE-04, E2E | target |
| R5 | Turn arrived push (cek deadline) | SRS-F-25-01..13 | TC-QUE-04, E2E | target |
| R6 | Delivery tracking (state machine) | SRS-F-21 | TC-MSG-01 | target |
| R7 | Push subscription API | SRS-F-25 | — | target |
| R8 | Idempotent consumer tanpa inbox | SRS-Q-REL-01 | TC-MSG-03 | target |
| R9 | Retry berjenjang + DLQ | SRS-Q-REL-01 | TC-MSG-01, TC-MSG-02, TC-MSG-04 | target |
| R10 | Health endpoint | D-23 | server_test.go | **implemented** |
| R11 | Konfigurasi fail-fast + privasi payload | D-24, SRS keamanan | load_test.go | implemented (config) / target (privasi kirim) |
| R12 | OTP login email | SRS-F-27-02, SRS-F-27-12 | TC-MSG-05, TC-MSG-06 | target |

---

## R1 — E-ticket email [target]

**User story:** Sebagai pembeli yang baru menyelesaikan pembayaran, saya ingin
menerima e-tiket lewat email agar punya bukti dan kode masuk venue.

Sumber: SRS-F-20-01..03 · TC-TKT-01, TC-TKT-02, TC-TKT-03 · SDD §2, §6.2 · `booking.paid.v1.schema.json`.

- **WHEN** event `booking.paid` diterima dari exchange `rusuh.events` (queue
  `notification.booking.events`), **THE** Notification Service **SHALL** merender
  template `eticket` dan mengirim email ke `buyerEmail` berisi `event.name`,
  `event.startsAt`, `event.venue`, daftar `tickets` (`ticketCode`,
  `attendeeName`, `attendeeNikMasked`, `categoryCode`), `publicRef`, dan
  `ticketLinkToken` (tautan tiket).
- **WHEN** email e-tiket berhasil terkirim, **THE** service **SHALL** mengubah
  baris `deliveries` dengan `notification_type='BOOKING_PAID'`, `channel='EMAIL'`,
  `status='SENT'`, `sent_at=now()`, dan `provider_message_id`.
- **IF** template gagal dirender karena payload tidak lengkap/korup, **THEN THE**
  service **SHALL** memperlakukan event sebagai *poison* (`rabbitmq.Permanent`),
  menandai delivery `DEAD`, dan tidak me-retry (lihat R9).
- **WHERE** email terkirim, **THE** isi **SHALL** memuat kode tiket dan NIK
  tersamar agar bisa diverifikasi di pintu masuk (TC-TKT-01, TC-TKT-02).

## R2 — Expiry email [target]

**User story:** Sebagai pembeli yang bookingnya kedaluwarsa, saya ingin
pemberitahuan agar tahu statusnya.

Sumber: SRS-F-21-01..03 · TC-MSG-01 · SDD §6.3 · `booking.expired.v1.schema.json`.

- **WHEN** event `booking.expired` diterima, **THE** service **SHALL** mengirim
  email dari template `expired` berisi `publicRef` dan `reason`
  (`PAYMENT_TIMEOUT` | `SESSION_EXPIRED` | `CANCELLED`) dan mencatat delivery
  `notification_type='BOOKING_EXPIRED'`, `channel='EMAIL'`.
- **IF** SMTP down saat pengiriman, **THEN THE** service **SHALL** me-retry lewat
  retry queue dan mengirim setelah pemulihan (TC-MSG-01), bukan menandai gagal
  permanen.

## R3 — Review email [target]

**User story:** Sebagai pembeli yang pembayarannya terdeteksi terlambat, saya
ingin pemberitahuan beserta langkah berikutnya.

Sumber: SRS-F-21-01..03 · TC-MSG-01 · SDD §6.4 · `booking.review.v1.schema.json`.

- **WHEN** event `booking.review` diterima, **THE** service **SHALL** mengirim
  email dari template `review` berisi `bookingId`, `amountMinor` (diformat IDR),
  dan `attemptHint` bila ada, lalu mencatat delivery
  `notification_type='BOOKING_REVIEW'`, `channel='EMAIL'`.

## R4 — Turn soon push [target]

**User story:** Sebagai pembeli di waiting room, saya ingin notifikasi push saat
giliran saya hampir tiba agar bersiap.

Sumber: SRS-F-25-01..13 · TC-QUE-04, E2E · SDD §7.2 · `queue.turn.soon.v1.schema.json`.

- **WHEN** event `queue.turn.soon` diterima (queue `notification.queue.events`),
  **THE** service **SHALL** mencari subscription `(visitor_id, event_id)` dan
  mengirim push "Giliran Sebentar Lagi" ke token tersubscribe, lalu mencatat
  delivery `notification_type='TURN_SOON'`, `channel='PUSH'`.
- **IF** tidak ada subscription untuk `(visitorId, eventId)`, **THEN THE** service
  **SHALL** melewati pengiriman (ack) tanpa error.
- **IF** FCM melaporkan token tidak valid, **THEN THE** service **SHALL**
  menghapus subscription dan ack (bukan retry).

## R5 — Turn arrived push (cek deadline) [target]

**User story:** Sebagai pembeli, saya ingin notifikasi push saat giliran saya
tiba sehingga bisa segera claim slot.

Sumber: SRS-F-25-01..13 · TC-QUE-04, E2E · SDD §7.3–§7.4 · `queue.turn.arrived.v1.schema.json`.

- **WHEN** event `queue.turn.arrived` diterima, **THE** service **SHALL**
  mengirim push "Giliran Anda Tiba!" dengan data `eventId`, `queueEntryId`,
  `type='TURN_ARRIVED'`, dan `claimDeadline`.
- **IF** `claimDeadline` sudah lewat saat event diproses, **THEN THE** service
  **SHALL** melewati pengiriman (ack & buang; set `expires_at`) karena push yang
  telat tidak berguna — entry sudah `UNCLAIMED`.
- **IF** tidak ada subscription atau token invalid, **THEN THE** service **SHALL**
  berperilaku sama dengan R4 (ack / hapus subscription).

## R6 — Delivery tracking (state machine) [target]

**User story:** Sebagai operator, saya ingin status setiap pengiriman tercatat
agar bisa mengukur keberhasilan dan menelusuri kegagalan.

Sumber: SRS-F-21 · SDD §4.1.

- **WHEN** sebuah event mulai diproses, **THE** service **SHALL** memegang tepat
  satu baris `deliveries` per `(source_message_id, notification_type)` sebagai
  catatan status yang bergerak `PENDING → SENDING → SENT`, atau `→ RETRY → DEAD`.
- **THE** service **SHALL** menyimpan `attempts`, `last_error`,
  `provider_message_id`, `next_attempt_at`, dan `recipient_hash` (HMAC, **bukan**
  recipient plaintext) di setiap baris.

## R7 — Push subscription API [target]

**User story:** Sebagai aplikasi mobile, saya ingin mendaftarkan/mencabut token
FCM perangkat agar bisa menerima push untuk sebuah event.

Sumber: SRS-F-25 · SDD §3.1–§3.2 · `docs/contracts/openapi/notification.yaml`.

- **WHEN** `POST /api/v1/push-subscriptions` dengan header `X-Visitor-Token` valid
  dan body `{eventId, platform(ANDROID|IOS), pushToken(≤4096)}`, **THE** service
  **SHALL** menyimpan subscription (`push_token_enc` dienkripsi) dengan PK
  `(visitor_id, event_id)` dan mengembalikan **204**.
- **IF** subscription untuk `(visitor_id, event_id)` sudah ada, **THEN THE**
  service **SHALL** memperlakukannya idempoten (upsert) tanpa menduplikasi baris.
- **WHEN** `DELETE /api/v1/push-subscriptions/{eventId}` dengan `X-Visitor-Token`,
  **THE** service **SHALL** menghapus subscription pembeli untuk event itu dan
  mengembalikan **204**.
- **IF** validasi field gagal, **THEN THE** service **SHALL** mengembalikan 400
  `VALIDATION_ERROR` lewat `pkg/apperror` dalam `ErrorEnvelope` (tanpa SQL/stack
  trace/secret).

## R8 — Idempotent consumer tanpa tabel inbox [target]

**User story:** Sebagai sistem, saya ingin event yang terkirim ganda (jaminan
at-least-once) tidak menghasilkan email/push ganda.

Sumber: SRS-Q-REL-01 · TC-MSG-03 · SDD §5.4, §9.8 · code-principles.

- **WHEN** event yang sama diterima lebih dari sekali, **THE** service **SHALL**
  mengirim notifikasi tepat satu kali, dijaga oleh `INSERT ... ON CONFLICT
  (source_message_id, notification_type) DO NOTHING` — **tanpa tabel inbox
  terpisah**.
- **IF** `INSERT` delivery mengembalikan 0 baris (konflik), **THEN THE** service
  **SHALL** menyimpulkan event sudah/sedang diproses dan ack tanpa mengirim ulang.

## R9 — Retry berjenjang + DLQ [target]

**User story:** Sebagai sistem, saya ingin kegagalan transien dicoba ulang dan
kegagalan permanen dipindah ke DLQ agar satu pesan rusak tidak memblok antrean.

Sumber: SRS-Q-REL-01 · TC-MSG-01, TC-MSG-02, TC-MSG-04 · SDD §5.3 · `pkg/rabbitmq`.

- **WHEN** handler gagal transien, **THE** service **SHALL** mem-publish ulang
  pesan ke retry queue `<queue>.retry.<delay>` dengan delay dari
  `RABBITMQ_RETRY_DELAYS` (default `5s,30s,2m`; tier = `delays[attempt-1]`) dan
  menaikkan header `x-rbt-attempt`.
- **WHEN** `attempt > RABBITMQ_MAX_RETRIES` (default 3), **THE** service **SHALL**
  memindahkan pesan ke `<queue>.dlq`.
- **IF** payload rusak atau versi schema tidak dikenal, **THEN THE** service
  **SHALL** mengembalikan `rabbitmq.Permanent(err)` sehingga pesan langsung ke DLQ
  tanpa membakar retry.
- **WHEN** retry pertama diproses ulang, **THE** delivery **SHALL** membawa header
  `x-rbt-attempt=2` lalu ack setelah sukses (TC-MSG-04).

## R10 — Health endpoint [implemented]

Sumber: D-23 · `internal/delivery/server.go`, `health_handler.go`, `server_test.go`.

- **THE** service **SHALL** mengekspos `GET /health` yang mengembalikan 200 UP
  bila semua dependency (Postgres, RabbitMQ) sehat dalam `HEALTH_TIMEOUT`, dan
  503 DOWN bila ada yang gagal. Dipakai ALB + container HEALTHCHECK. **(Sudah
  terimplementasi.)**

## R11 — Konfigurasi fail-fast + privasi payload

Sumber: D-24 · `internal/config/*` · security-privacy.md · `pkg/eventstore`.

- **WHEN** service start, **THE** service **SHALL** memuat dan memvalidasi semua
  env (`APP_*`, `LOG_*`, `POSTGRES_*`, `RABBITMQ_*`) sekaligus dan keluar non-zero
  bila ada yang invalid. **(Terimplementasi — `load_test.go`.)** `[implemented]`
- **THE** payload event yang diproses **SHALL NOT** memuat NIK/token plaintext;
  hanya `attendeeNikMasked` dan `ticketLinkToken` yang diizinkan. `recipient`
  disimpan sebagai `recipient_hash` (HMAC), bukan email plaintext. `[target]`
- **THE** service **SHALL NOT** menulis kode OTP, token, atau PII plaintext ke
  log; hanya identifier (`messageId`, `bookingId`, `queueEntryId`). `[target]`

---

## R12 — OTP Login email [target]

**User story:** Sebagai pembeli yang meminta OTP login, saya ingin menerima kode
OTP via email agar bisa login ke akun saya.

Sumber: SRS-F-27-02, SRS-F-27-12 · TC-MSG-05, TC-MSG-06 · SDD-NOTIFICATION-001 v1.1 §5.4 · `otp.requested.v1`.

- **WHEN** event `otp.requested` diterima dari exchange `rusuh.events` (queue
  `notification.otp`), **THE** Notification Service **SHALL** mendekripsi
  `emailEnc` dan `codeEnc` menggunakan kunci `PII_ENC_KEY` (shared dengan User
  Service), merender template `otp_login`, dan mengirim email berisi kode OTP.
- **IF** `expiresAt` sudah lewat saat event diproses, **THEN THE** service
  **SHALL** melewati pengiriman (ack & buang) karena OTP sudah tidak valid.
- **IF** dekripsi `emailEnc` atau `codeEnc` gagal (kunci salah atau payload
  korup), **THEN THE** service **SHALL** memperlakukan event sebagai *poison*
  (`rabbitmq.Permanent`) dan memindahkan ke DLQ tanpa retry. *(TC-MSG-06)*
- **WHEN** SMTP gagal, **THE** service **SHALL NOT** me-retry (time-sensitive);
  user dapat meminta OTP baru. Event di-ack setelah attempt pertama.
- **THE** service **SHALL NOT** menulis kode OTP plaintext ke log; hanya
  `messageId` dan status pengiriman yang di-log. *(TC-USR-11)*
- **THE** email **SHALL** dikirim dalam waktu 5 detik dari publish event untuk
  memastikan OTP masih valid saat diterima user. *(TC-MSG-05)*

---

## 4. Invarian #6 — notifikasi tidak pernah membatalkan transaksi

> **Invarian §6 (invariants.md):** Kegagalan notifikasi tidak membatalkan
> transaksi (retry + DLQ).

Konsekuensi yang mengikat desain:

- Notification Service **tidak** memegang `IUnitOfWork`, **tidak** menulis
  stok/uang/akun, dan **tidak** mempublikasikan event balik ke Booking/Payment.
  Ia murni *sink* event (produces events: tidak ada).
- **IF** semua retry habis dan pesan masuk DLQ, **THEN THE** transaksi sumber
  (booking/payment yang sudah final) **SHALL** tetap final; satu-satunya efek
  adalah alert `NotificationDLQNotEmpty`.
- Pengiriman notifikasi adalah *best-effort terhadap transaksi* namun *required
  terhadap dirinya sendiri* (retry sampai `SENT` atau `DEAD`).

---

## 5. Build & Verify

```bash
# dari services/notification
go build ./...                      # kompilasi
go test -race ./...                 # unit test (testify, table-driven)
golangci-lint run                   # lint + depguard (lapisan hexagonal)

# dari repo root
make build                          # build semua modul
make test                           # unit test -race semua modul
make lint                           # golangci-lint v2
make test-integration               # + integration (Postgres, Valkey, RabbitMQ dari `make infra`)
make cover                          # coverage logika, ambang 80% (COVERAGE_MIN)
make mock                           # regen mock ke _mock/ bila port berubah
make ci                             # semua pemeriksaan CI lokal (wajib lolos sebelum PR)
```

Gate: `make ci` wajib hijau sebelum PR; coverage logika ≥ 80% (`cmd/`,
`internal/delivery/`, `internal/config/`, `_mock/` dikecualikan sebagai
bootstrap/wiring). Setiap requirement SRS punya TC di Test Plan
`docs/test/06-TP-RBT-001.md`.
