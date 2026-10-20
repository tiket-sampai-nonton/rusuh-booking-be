# Requirements — Booking Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-BOOKING-001.md` |
| **Service** | `services/booking` (port 8082, schema `booking`, role `svc_booking`) |
| **Owner** | BE-2 |
| **Status implementasi** | Boilerplate + skema DB lengkap. Lihat §Status Implementasi. |

## Pendahuluan

Booking Service adalah jantung sistem Rusuh Booking Tiket: mengelola katalog event, otoritas stok,
sesi pemilihan (admission), hold atomik, ubah pesanan, settlement, dan penerbitan tiket. Semua
perubahan stok dan uang terjadi dalam **satu transaksi PostgreSQL**, dan siklus booking direkam
sebagai **event sourcing** (ADR-019). Service ini menegakkan tiga invarian produk:

1. **Tidak oversell** — stok hanya berubah di Booking Service dalam satu transaksi.
2. **Jangan PHP** — pembeli yang sudah admitted dijamin mendapat tiket sampai TTL habis.
3. **Kuota 3 tiket per NIK pembeli per event**, lintas sesi/perangkat, serta "satu tiket satu NIK pemegang".

Dokumen ini menulis user story + kriteria penerimaan bergaya **EARS** (Easy Approach to Requirements
Syntax) yang tertelusur ke SRS (`SRS-F-xx`) dan Test Plan (`TC-xx`, `docs/test/06-TP-RBT-001.md`).

## Status Implementasi (per audit kode 2026-10-19)

| Area | Status | Bukti di kode |
|------|--------|---------------|
| Skema DB (events, kategori, inventory, supply, admissions, bookings, items, tickets, event store, outbox, idempotency) | ✅ Terimplementasi | `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` |
| Trigger invarian DB (append-only event store, guard rules terkunci, audit config, supply=Σkategori, CHECK anti-oversell) | ✅ Terimplementasi | `0001_init.up.sql`, `0002_event_store.up.sql` |
| CLI verb `api`/`worker`/`migrate`/`healthcheck` | ✅ Terimplementasi | `cmd/*.go` |
| Config tervalidasi (fail fast) + `GET /health` | ✅ Terimplementasi | `internal/config/*`, `internal/booking/infrastructure/http/health_handler.go` |
| Domain/usecase/adapter untuk katalog, sesi, hold, settlement, tiket, SSE | ❌ Boilerplate | hanya domain `health`/`dependency_checker` ada; `delivery/worker.go:jobs()` mengembalikan `nil` |
| Worker nyata (session sweeper, expiry sweeper, outbox relay) | ❌ Belum | `jobs(*Clients) []Job { return nil }` |

> **Catatan traceability skema:** penamaan kolom di kode migrasi lebih kaya daripada prosa SDD
> (mis. `price_minor`, `held_quantity`/`sold_quantity`, status booking `PENDING_PAYMENT`/`VERIFYING`,
> `selection_ttl_seconds`/`claim_window_seconds`). Requirement di bawah mengacu pada **kode migrasi**
> sebagai kebenaran saat berbeda dari prosa SDD, dan menandai bagian yang masih harus dibangun.

Konvensi EARS yang dipakai: **WHEN** (event), **WHILE** (state), **IF/THEN** (kondisi),
**WHERE** (fitur), dan **SHALL** untuk kewajiban sistem.

---

## Requirement 1 — Katalog Event

**User story:** Sebagai calon pembeli, saya ingin melihat daftar event dengan filter dan paginasi,
agar saya dapat menemukan konser yang ingin saya beli tiketnya.

**Tertelusur ke:** SRS-F-02-01..04, SRS-F-28-01..06 · TC-EVT-01..03, TC-CAT-01..06

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events`, sistem SHALL mengembalikan daftar event terpaginasi dengan `meta`, `data.items`, dan `data.pagination`.
2. WHERE parameter `timeframe` ∈ {`THIS_WEEK`,`THIS_MONTH`,`UPCOMING`} diberikan, sistem SHALL hanya mengembalikan event yang jatuh dalam rentang itu (TC-CAT-01).
3. WHERE parameter `city` diberikan, sistem SHALL memfilter case-insensitive berdasarkan kota.
4. WHERE parameter `status` ∈ {`NOT_STARTED`,`AVAILABLE`,`SOLD_OUT`,`CLOSED`} diberikan, sistem SHALL hanya mengembalikan event dengan status tersebut.
5. WHERE `sort` diberikan, sistem SHALL mengurutkan berdasarkan `DATE_ASC` (default), `PRICE_ASC`, atau `PRICE_DESC`.
6. IF `size > 100` atau `page < 1`, THEN sistem SHALL mengembalikan `400 VALIDATION_ERROR`.
7. Respons SHALL dapat di-cache 2 detik oleh CloudFront dengan kunci `path + page + size + timeframe + city + status`.

---

## Requirement 2 — Detail Event

**User story:** Sebagai calon pembeli, saya ingin melihat detail event beserta kategori tiket,
harga, ketersediaan, dan konfigurasi (TTL, batas), agar saya tahu apa yang bisa saya beli.

**Tertelusur ke:** SRS-F-03-01..05 · TC-EVT-04, TC-EVT-05

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events/{eventId}` dengan UUID valid, sistem SHALL mengembalikan detail event termasuk `categories[]` (harga, available, capacity) dan `config`.
2. IF `eventId` bukan UUID valid, THEN sistem SHALL mengembalikan `400 VALIDATION_ERROR`.
3. IF event tidak ada, THEN sistem SHALL mengembalikan `404 NOT_FOUND`.
4. `config` yang dikembalikan SHALL mencakup `maxTicketsPerOrder`, `maxTicketsPerBuyerNik`, `bookingTtlSeconds` (= `selection_ttl_seconds`), dan `paymentTtlSeconds`.
5. Angka `available` per kategori SHALL dihitung `capacity - held_quantity - sold_quantity` (otoritatif dari PostgreSQL, bukan Valkey).

---

## Requirement 3 — Admission & Reservasi (Jangan PHP)

**User story:** Sebagai pembeli yang gilirannya tiba di antrean, saya ingin unit tiket direservasi
untuk saya saat saya di-admit, agar saya dijamin mendapat tiket (tidak PHP).

**Tertelusur ke:** SRS-F-04, SRS-F-09-01..08 · TC-ADM-01..05

### Kriteria Penerimaan (EARS)

1. WHEN Queue memanggil `POST /internal/v1/allocations:batch`, sistem SHALL mereservasi `units` di `event_supply.allocated_units` dan membuat baris `admissions` berstatus `RESERVED` dalam satu transaksi (TC-ADM-01).
2. Operasi alokasi SHALL idempoten per `queueEntryId` (kolom unik `admissions.queue_entry_id`); panggilan ulang SHALL mengembalikan hasil yang sama tanpa reservasi ganda.
3. WHILE admission berstatus `RESERVED`/`ACTIVE`/`EDITABLE`, sistem SHALL menjamin satu admission aktif per `(event_id, visitor_id)` dan per `(event_id, buyer_nik_hash)` (indeks unik parsial, anti-calo).
4. WHILE admission aktif dan TTL belum habis, IF pembeli melakukan hold, THEN sistem SHALL mengizinkan hold berhasil (TC-ADM-02, jaminan Jangan PHP).
5. WHEN klien `POST /api/v1/booking-sessions` dengan admission token (one-time, TTL 60s), sistem SHALL mengubah admission `RESERVED → ACTIVE` dan mengembalikan sesi dengan `expiresAt` dan `serverTime`.
6. IF admission token tidak valid/expired, THEN sistem SHALL mengembalikan `403 ADMISSION_TOKEN_INVALID`/`ADMISSION_TOKEN_EXPIRED`.
7. IF `visitor_id` pada token tidak cocok dengan admission, THEN sistem SHALL mengembalikan `403 VISITOR_MISMATCH`.

---

## Requirement 4 — Snapshot Konfigurasi per Sesi

**User story:** Sebagai pembeli yang sedang memilih, saya ingin aturan event (TTL, batas) terkunci
untuk sesi saya, agar perubahan konfigurasi admin tidak mengganggu sesi yang sedang berjalan.

**Tertelusur ke:** SRS-F-26-01..08 · TC-CFG-01..04

### Kriteria Penerimaan (EARS)

1. WHEN admission dibuat, sistem SHALL menyimpan snapshot konfigurasi (`cfg_selection_ttl_seconds`, `cfg_payment_ttl_seconds`, `cfg_submit_grace_seconds`, `cfg_edit_enabled`, `cfg_max_edits`, `cfg_allow_mixed_categories`) ke baris admission.
2. WHILE sesi berjalan, sistem SHALL memakai nilai snapshot admission, bukan nilai event terkini (TC-CFG-01).
3. WHEN event beralih ke status `AVAILABLE`, sistem SHALL mengunci aturan pembentuk jaminan (`rules_locked_at` terisi otomatis via trigger).
4. IF ada upaya mengubah `max_tickets_per_order`/`max_tickets_per_buyer_nik`/`allow_mixed_categories` setelah terkunci, THEN DB SHALL menolak dengan `EVENT_RULES_LOCKED`.
5. WHEN konfigurasi event diubah, sistem SHALL mencatat perubahan (field, old→new, actor) ke `config_audit` via trigger.

---

## Requirement 5 — Validasi NIK Pemegang & Kuota Pembeli

**User story:** Sebagai sistem, saya ingin memvalidasi NIK pemegang dan menegakkan kuota pembeli,
agar batas 3 tiket per NIK pembeli dan "satu tiket satu NIK pemegang" tidak dilanggar.

**Tertelusur ke:** SRS-F-11-01..10 · TC-BKG-01, TC-BKG-02, TC-BKG-03, TC-BKG-08..12

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/booking-sessions/{sessionId}/attendee-checks`, sistem SHALL memvalidasi setiap NIK (16 digit, kode provinsi 01-99, tanggal lahir valid; Lampiran A.1).
2. IF format NIK salah, THEN sistem SHALL mengembalikan `422 NIK_INVALID`.
3. WHEN NIK yang sama dicek ulang dalam satu sesi, sistem SHALL tidak menghitungnya ulang ke batas (dedupe via PK `(admission_id, attendee_nik_hash)`).
4. IF jumlah NIK unik yang dicek melebihi `max_attendee_checks_per_session`, THEN sistem SHALL mengembalikan `429 ATTENDEE_CHECK_LIMIT`.
5. IF `held_quantity + paid_quantity + jumlah_item ≥ maxTicketsPerBuyerNik` untuk `buyer_nik_hash`, THEN sistem SHALL menolak hold dengan `409 BUYER_QUOTA_EXCEEDED`, termasuk booking aktif lain dan lintas sesi/perangkat (TC-BKG-01, invarian 3).
6. IF NIK pemegang sudah punya tiket aktif (HELD/PAID) di event yang sama, THEN sistem SHALL menolak dengan `409 NIK_ALREADY_HAS_TICKET` (indeks `uq_attendee_nik_per_event`, TC-BKG-03).
7. Kuota pembeli SHALL terpisah dari identitas pemegang: satu pembeli dapat membeli untuk NIK pemegang lain selama kuota belum habis (TC-BKG-11).

---

## Requirement 6 — Hold Atomik

**User story:** Sebagai pembeli, saya ingin menahan (hold) tiket secara atomik, agar stok tidak
oversell meski banyak pembeli bersamaan.

**Tertelusur ke:** SRS-F-12-01..07, SRS-Q-FUN-01 · TC-BKG-04..07, TC-PERF-04

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/bookings` dengan `Idempotency-Key`, sistem SHALL membuat booking berstatus `PENDING_PAYMENT`, menaikkan `held_quantity` kategori, `held_units` supply, dan `buyer_quotas.held_quantity` **dalam satu transaksi** dengan urutan lock tetap (IUnitOfWork).
2. Operasi hold SHALL idempoten per `(scope, owner_id, idempotency_key)`; permintaan ulang identik SHALL mengembalikan respons tersimpan.
3. IF request dengan `Idempotency-Key` sama tetapi body berbeda, THEN sistem SHALL mengembalikan `409 IDEMPOTENCY_CONFLICT`.
4. IF `held_quantity + sold_quantity` akan melebihi `capacity`, THEN transaksi SHALL dibatalkan oleh CHECK DB dan sistem mengembalikan `409 CATEGORY_SOLD_OUT` (TC-BKG-07: 100 hold paralel pada 50 tiket terakhir → tepat 50 berhasil).
5. IF `items.length > maxTicketsPerOrder`, THEN sistem SHALL mengembalikan `400 MAX_TICKETS_EXCEEDED`.
6. IF stok event habis, THEN sistem SHALL mengembalikan `409 NO_SUPPLY`.
7. WHEN hold berhasil, sistem SHALL meng-append event `BookingHeld` ke `booking_events` dan memproyeksikan read model `bookings` dalam transaksi yang sama (lihat Requirement 11).

---

## Requirement 7 — Sesi & Timeout (Timer Server-Authoritative)

**User story:** Sebagai sistem, saya ingin TTL sesi dan pembayaran dihitung server-authoritative,
agar klien tidak dapat memperpanjang sendiri dan slot kembali bebas saat kedaluwarsa.

**Tertelusur ke:** SRS-F-07-01..06, SRS-F-08-01..03 · TC-SES-01..08

### Kriteria Penerimaan (EARS)

1. Sistem SHALL menghitung `admissions.expires_at` dan `bookings.payment_expires_at` dari waktu server dan nilai snapshot config; klien hanya menampilkan (invarian 4).
2. WHEN klien `GET /api/v1/booking-sessions/current?eventId=` atau `/{sessionId}`, sistem SHALL mengembalikan `expiresAt` dan `serverTime`.
3. WHILE admission `ACTIVE`/`EDITABLE` dan `expires_at < now`, worker sweeper (interval 10s) SHALL mengubah status ke `EXPIRED`/`RELEASED` dan melepas alokasi/hold (TC-SES-05..08).
4. IF permintaan tiba setelah `admissions.expires_at`, THEN sistem SHALL mengembalikan `410 SESSION_EXPIRED`.
5. IF pembayaran tidak selesai sebelum `bookings.payment_expires_at`, THEN booking SHALL kedaluwarsa (Requirement 9).
6. `payment_expires_at` SHALL dihitung sejak hold pertama dan **tidak di-reset** oleh ubah pesanan.

---

## Requirement 8 — Ubah Pesanan (Edit Order) & Pemulihan Sesi

**User story:** Sebagai pembeli, saya ingin mengubah pesanan sebelum menekan "Bayar" dan memulihkan
sesi setelah refresh/disconnect, agar saya tidak kehilangan slot karena gangguan jaringan.

**Tertelusur ke:** SRS-F-23-01..11, SRS-F-24-01..13 · TC-EDT-01..06, TC-NAV-01..03, TC-SES-05..08

### Kriteria Penerimaan (EARS)

1. WHILE `cfg_edit_enabled` true dan `booking.payment_started_at` null, WHEN klien `PUT /api/v1/bookings/{bookingId}`, sistem SHALL menandai item lama `REPLACED`, membuat item baru `HELD`, menaikkan `bookings.version`, dan menaikkan `admissions.edit_count` dalam satu transaksi (TC-EDT-01).
2. IF `payment_started_at` sudah terisi, THEN sistem SHALL menolak edit dengan `409 BOOKING_LOCKED`.
3. IF `edit_count ≥ cfg_max_edits`, THEN sistem SHALL menolak dengan `409 MAX_EDITS_EXCEEDED`.
4. Ubah pesanan SHALL meng-append event `BookingItemsReplaced` dan **tidak** me-reset `payment_expires_at`.
5. WHEN klien `GET /api/v1/booking-sessions/current?eventId=` dengan `X-Visitor-Token` setelah refresh, sistem SHALL memulihkan sesi/booking aktif yang sama (idempoten, TC-NAV-01, TC-SES-05).

---

## Requirement 9 — Settlement & Expiry Booking

**User story:** Sebagai sistem, saya ingin menerapkan pembayaran sukses secara idempoten dan
menangani booking kedaluwarsa dengan benar, agar status akhir hanya berasal dari webhook/rekonsiliasi.

**Tertelusur ke:** SRS-F-16-01..03, SRS-F-17-01..05 · TC-PAY-03, TC-PAY-04, TC-PAY-06..08

### Kriteria Penerimaan (EARS)

1. WHEN event `payment.succeeded` diterima dari Payment, IF `booking.status != PAID`, THEN sistem SHALL menerapkan settlement: `held → sold` (kategori + supply + `buyer_quotas`), menerbitkan tiket, append `BookingPaid`, status `PAID` — dalam satu transaksi (TC-PAY-03).
2. Consumer `payment.succeeded` SHALL idempoten via guard state (`status != PAID`, tanpa tabel inbox); kirim ganda SHALL tidak menggandakan tiket (invarian 5, uji TC kirim ganda).
3. WHEN event `payment.failed` diterima, sistem SHALL hanya mencatat; booking tetap menunggu hingga TTL.
4. WHILE booking `PENDING_PAYMENT`/`VERIFYING` dan `payment_expires_at < now`, worker expiry sweeper (10s) SHALL: untuk booking terkunci, memanggil `POST /internal/v1/payments/{id}/resolve`.
5. IF resolve tidak menemukan pembayaran, THEN sistem SHALL meng-expire booking, melepas stok (`held → free`), dan append `BookingExpired`.
6. IF resolve menemukan pembayaran, THEN sistem SHALL membuat kasus `REVIEW` dan append `BookingReview` (bukan oversell, bukan auto-cancel).

---

## Requirement 10 — Tiket & Riwayat Booking

**User story:** Sebagai pembeli, saya ingin melihat e-tiket saya, dan jika login, melihat riwayat
booking saya, agar saya punya bukti dan jejak pembelian.

**Tertelusur ke:** SRS-F-20-01..03, SRS-F-27-11 · TC-TKT-01..03, TC-USR-10

### Kriteria Penerimaan (EARS)

1. WHEN settlement sukses, sistem SHALL menerbitkan satu baris `tickets` per `booking_item` dengan `public_code` unik (TC-TKT-01).
2. WHEN klien `GET /api/v1/tickets/{bookingId}`, sistem SHALL mengembalikan e-tiket hanya untuk booking berstatus `PAID`.
3. WHERE pembeli login, WHEN `GET /api/v1/me/bookings` dengan access token (JWT EdDSA User Service), sistem SHALL mengembalikan **hanya** booking dengan `account_id = token.sub` (TC-USR-10, tanpa kebocoran lintas akun).
4. IF token akses tidak valid, THEN sistem SHALL mengembalikan `401`.

---

## Requirement 11 — Event Sourcing & Integritas Stok

**User story:** Sebagai sistem, saya ingin siklus booking direkam append-only dan stok berubah
dalam transaksi yang sama, agar ada jejak audit yang dapat di-replay tanpa risiko oversell.

**Tertelusur ke:** SRS-C-09, SRS-Q-FUN-01, SRS-F-90-01 · TC-ES-01..05, TC-BKG-07, TC-OPS-01

### Kriteria Penerimaan (EARS)

1. WHEN perubahan state booking terjadi, sistem SHALL meng-append event ke `booking_events` dengan `(stream_id, version)` unik, memproyeksikan read model, mengubah stok, dan menulis outbox — semuanya dalam **satu** transaksi (ADR-019).
2. IF append menemui konflik versi (`23505` pada `(stream_id, version)` → `ErrConcurrency`), THEN sistem SHALL membaca ulang state lalu mengulangi (re-read + retry), bukan menimpa.
3. Payload `booking_events` SHALL **tidak** memuat NIK plaintext, email, atau token (ditolak `pkg/eventstore`; invarian privasi).
4. DB SHALL menolak `UPDATE`/`DELETE`/`TRUNCATE` pada `booking_events` (trigger append-only).
5. Sistem SHALL menjamin stok tidak pernah negatif (CHECK DB `held + sold <= capacity`, `allocated + held + sold <= total_capacity`); setiap counter negatif adalah pelanggaran kritis (TC-BKG-07).
6. `total_capacity` event SHALL selalu sama dengan Σ kapasitas kategori (constraint trigger deferred).
7. WHEN job rekonsiliasi dijalankan, sistem SHALL mendeteksi inkonsistensi stok/booking dan mengirim alert (TC-OPS-01).

---

## Requirement 12 — Event Antar-Service (Outbox)

**User story:** Sebagai sistem, saya ingin menerbitkan event domain secara andal via outbox, agar
Queue dan Notification tetap konsisten meski broker sempat gagal.

**Tertelusur ke:** SRS-IF-30..31, SRS-Q-REL-* · TC-API-01, TC-API-02, TC-CAP-01..03

### Kriteria Penerimaan (EARS)

1. WHEN perubahan stok/uang di-commit, sistem SHALL menulis baris `outbox_events` dalam transaksi yang sama (outbox hanya untuk perubahan stok atau uang).
2. Worker outbox relay (interval 1s) SHALL menerbitkan `outbox_events` yang belum terbit ke RabbitMQ lalu menandai `published_at`.
3. Sistem SHALL menerbitkan event: `booking.journey.ended`, `booking.quota.exhausted`, `booking.sold_out` (ke Queue); `booking.paid`, `booking.expired`, `booking.review` (ke Notification/Payment).
4. IF penerbitan gagal, THEN relay SHALL menaikkan `publish_attempts`, mencatat `last_error`, dan mencoba lagi (tanpa kehilangan event).
5. Payload event SHALL mengikuti kontrak `docs/contracts/` dan tanpa PII plaintext.

---

## Requirement 13 — Kesehatan, Konfigurasi & Observability

**User story:** Sebagai operator, saya ingin endpoint kesehatan, konfigurasi tervalidasi saat start,
dan metrik, agar ALB dan tim ops dapat memantau service.

**Tertelusur ke:** SRS-Q-MNT-02, SRS-Q-FLX-01 · TC-OBS-01, TC-OBS-02, TC-ASG-01

### Kriteria Penerimaan (EARS)

1. WHEN `GET /health` dipanggil, sistem SHALL mengembalikan `200 UP` bila semua dependency sehat atau `503 DOWN` bila ada yang gagal (dengan batas `HEALTH_TIMEOUT`). ✅ *Terimplementasi.*
2. WHEN service start, sistem SHALL memvalidasi seluruh konfigurasi (`APP_*`, `LOG_*`, `POSTGRES_*`, `VALKEY_*`, `RABBITMQ_*`) dan gagal cepat bila tidak valid. ✅ *Terimplementasi.*
3. Sistem SHALL mengekspos metrik `rbt_booking_sessions_total`, `rbt_booking_holds_total`, `rbt_booking_settlements_total`, `rbt_booking_expirations_total`, `rbt_booking_stock_gauge`, `rbt_booking_event_store_version`.
4. IF counter stok < 0, atau expiry_rate > 20% selama 10m, atau outbox lag > 30s selama 5m, atau p99 DB > 100ms selama 5m, THEN sistem SHALL memicu alert sesuai severity (§11.2 SDD).

---

## Matriks Keterlacakan Ringkas

| Req | SRS | Test Cases | Invarian |
|-----|-----|-----------|----------|
| 1 Katalog | F-02, F-28 | TC-EVT-01..03, TC-CAT-01..06 | — |
| 2 Detail | F-03 | TC-EVT-04..05 | — |
| 3 Admission | F-04, F-09 | TC-ADM-01..05 | #2 Jangan PHP |
| 4 Config snapshot | F-26 | TC-CFG-01..04 | #4 |
| 5 NIK & Kuota | F-11 | TC-BKG-01..03, 08..12 | #3 |
| 6 Hold atomik | F-12, Q-FUN-01 | TC-BKG-04..07, TC-PERF-04 | #1 No oversell |
| 7 Sesi & timeout | F-07, F-08 | TC-SES-01..08 | #4 Timer server-auth |
| 8 Edit & recovery | F-23, F-24 | TC-EDT-01..06, TC-NAV-01..03 | — |
| 9 Settlement & expiry | F-16, F-17 | TC-PAY-03..08 | #5 |
| 10 Tiket & history | F-20, F-27-11 | TC-TKT-01..03, TC-USR-10 | — |
| 11 Event sourcing & stok | C-09, Q-FUN-01, F-90 | TC-ES-01..05, TC-BKG-07, TC-OPS-01 | #1 |
| 12 Outbox/event | IF-30..31, Q-REL | TC-API-01..02, TC-CAP-01..03 | #6 |
| 13 Health/obs | Q-MNT-02, Q-FLX-01 | TC-OBS-01..02, TC-ASG-01 | — |
