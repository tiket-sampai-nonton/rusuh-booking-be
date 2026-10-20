# Requirements — Booking Service

## Metadata Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Document ID (LLD)** | `docs/sdd/SDD-BOOKING-001.md` (sumber kebenaran) |
| **Service** | `services/booking` |
| **Port** | 8082 |
| **Schema / Role** | `booking` / `svc_booking` (hanya melihat schema sendiri) |
| **Owner** | BE-2 |
| **Region** | ap-southeast-3 (Jakarta) |
| **Dependency** | PostgreSQL 16 (otoritas stok/uang), Valkey 8.1 (cache SSE, rate limit), RabbitMQ (event + outbox) |
| **Verb CLI (Cobra)** | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` |
| **Arsitektur** | Hexagonal (ports & adapters) + event sourcing untuk agregat Booking (ADR-019) |
| **Kontrak** | `docs/contracts/openapi/booking.yaml`, `internal-booking.yaml`, `docs/contracts/events/*.schema.json` |

## Pendahuluan

Booking Service adalah **jantung sistem** Rusuh Booking Tiket dan **otoritas tunggal stok dan uang**:
mengelola katalog event, inventori tiket, alokasi/admission, sesi pemilihan dengan TTL, hold atomik,
ubah pesanan, settlement, penerbitan e-tiket, dan ekspiri. Semua perubahan stok dan uang terjadi dalam
**satu transaksi PostgreSQL**; siklus booking direkam sebagai **event sourcing** append-only (ADR-019)
dengan read model yang diproyeksikan sinkron dalam transaksi yang sama.

Dokumen ini menulis user story + kriteria penerimaan bergaya **EARS** (WHEN event, WHILE state,
IF/THEN kondisi, WHERE fitur, SHALL kewajiban), tertelusur ke **SRS** (`SRS-F-xx`) dan **Test Plan**
(`TC-xx`, `docs/test/06-TP-RBT-001.md`). Sumber kebenaran skema adalah **kode migrasi**
(`deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql`) yang **lebih kaya** dari prosa
SDD §6 — perbedaan ditandai sebagai **Δ (delta)**.

### Delta penting prosa SDD → kode/kontrak (ditandai Δ di seluruh dokumen)

| # | Prosa SDD | Kebenaran (kode/kontrak) |
|---|-----------|--------------------------|
| Δ1 | Status booking `HELD → LOCKED → PAID` | Kode: `PENDING_PAYMENT → VERIFYING → PAID/EXPIRED/CANCELLED/REVIEW` (`bookings.status` CHECK) |
| Δ2 | Edit via `PUT /api/v1/bookings/{id}` | Kontrak: `PUT /api/v1/bookings/{bookingId}/items` (penggantian atomik, kirim `version`) |
| Δ3 | Tiket via `GET /api/v1/tickets/{bookingId}` | Kontrak: `GET /api/v1/bookings/{bookingId}/tickets` |
| Δ4 | Kolom `price`, `held`, `sold`, `amount` | Kode: `price_minor`, `held_quantity`, `sold_quantity`, `amount_minor` (minor unit IDR) |
| Δ5 | Error `CATEGORY_SOLD_OUT`, `BUYER_QUOTA_EXCEEDED`, `NIK_ALREADY_HAS_TICKET`, `MAX_TICKETS_EXCEEDED`, `MAX_EDITS_EXCEEDED` | Kontrak: `CATEGORY_UNAVAILABLE`, `BUYER_QUOTA_EXHAUSTED`, `ATTENDEE_NIK_ALREADY_REGISTERED`, `QUANTITY_EXCEEDS_LIMIT`, `EDIT_LIMIT_REACHED` |
| Δ6 | Admission state `CONVERTED` + `ENDED` | Kode: `CONVERTED`, `EXPIRED`, `RELEASED` (tidak ada status `ENDED`; akhir via `end_reason`) |
| Δ7 | Internal `POST /internal/v1/bookings/{id}/payment-lock` disebut "payment lock" generik | Kontrak: idempoten per `attemptKey`, balas `outcome` ∈ {OK, BOOKING_NOT_PAYABLE, PAYMENT_EXPIRED} |
| Δ8 | SSE event `availability` + `session` | Kontrak: event `availability` + `timer`, heartbeat `SSE_HEARTBEAT_SECONDS`, 503 `DEPENDENCY_UNAVAILABLE` saat batas koneksi |

---

## Status Implementasi (audit kode 2026-10-20)

| Area | Status | Bukti di `services/booking/` |
|------|--------|------------------------------|
| Skema DB penuh (events, ticket_categories, category_inventories, event_supply, admissions, attendee_checks, buyer_quotas, bookings, booking_items, tickets, booking_events, outbox_events, idempotency_records, config_audit) | ✅ Terimplementasi | `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` |
| Trigger invarian DB (append-only event store, `guard_event_rules`/`rules_locked_at`, `audit_event_config`, `check_supply_matches_categories` deferred, CHECK anti-oversell, indeks unik anti-calo) | ✅ Terimplementasi | `0001_init.up.sql`, `0002_event_store.up.sql` |
| CLI verb `api`/`worker`/`migrate`/`healthcheck` (Cobra) | ✅ Terimplementasi | `cmd/{api,worker,migrate,healthcheck,root,main}.go` |
| Config tervalidasi fail-fast (`APP_*`, `LOG_*`, `POSTGRES_*`, `VALKEY_*`, `RABBITMQ_*`, HTTP/Worker) | ✅ Terimplementasi | `internal/config/{config.go,load.go}` |
| `GET /health` (200 UP / 503 DOWN) + dependency checker | ✅ Terimplementasi | `internal/booking/infrastructure/http/health_handler.go`, `.../healthcheck/checker.go`, `internal/delivery/clients.go` |
| Composition root server + worker loop | ✅ Terimplementasi (loop kosong) | `internal/delivery/{server.go,worker.go,serve.go}` |
| Domain/usecase/adapter bisnis (katalog, detail, sesi, admission, hold, edit, settlement, tiket, SSE, riwayat) | ❌ Boilerplate | hanya domain `health`/`dependency_checker`; tidak ada usecase bisnis |
| Worker nyata (session sweeper, expiry sweeper, outbox relay) | ❌ Belum | `internal/delivery/worker.go` → `jobs(*Clients) []Job { return nil }` |
| Konsumen RabbitMQ (`payment.succeeded`/`payment.failed`) + publisher outbox | ❌ Belum | tidak ada adapter rabbitmq domain |
| Internal API (allocations:batch, supply, payable, payment-lock, deadline-extension, admin) | ❌ Belum | tidak ada handler internal |

> **Konvensi Δ:** di mana prosa SDD §6 berbeda dari kode migrasi, requirement mengacu pada **kode
> migrasi** dan menandai delta. Nama kolom aktual (minor unit, `*_quantity`, status) digunakan.

---

## Requirement 1 — Katalog Event

**User story:** Sebagai calon pembeli, saya ingin melihat daftar event dengan filter (waktu, kota,
status) dan urutan, agar saya dapat menemukan konser yang ingin saya beli tiketnya.

**Tertelusur ke:** SRS-F-02-01..04, SRS-F-28-01..06 · TC-EVT-01..03, TC-CAT-01..06

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events`, sistem SHALL mengembalikan `{ meta, data:{ events[], serverTime } }`; setiap `EventSummary` memuat `eventId`, `slug`, `name`, `venue`, `startsAt`, `salesStartsAt`, `salesEndsAt`, `status`, `minPriceMinor` (TC-EVT-01).
2. WHERE `timeframe` ∈ {`ALL`(default),`THIS_WEEK`,`THIS_MONTH`,`UPCOMING`}, sistem SHALL hanya mengembalikan event dalam rentang itu (TC-CAT-01, SRS-F-28-01).
3. WHERE `city` diberikan, sistem SHALL memfilter case-insensitive berdasarkan kota/venue (TC-CAT-02, SRS-F-28-02).
4. WHERE `status` ∈ {`ALL`(default),`NOT_STARTED`,`AVAILABLE`,`SOLD_OUT`,`CLOSED`}, sistem SHALL hanya mengembalikan event dengan status itu (TC-CAT-03, SRS-F-28-03).
5. WHERE `sort` ∈ {`DATE_ASC`(default),`PRICE_ASC`,`PRICE_DESC`}, sistem SHALL mengurutkan hasil sesuai (TC-CAT-04..06, SRS-F-28-06).
6. Respons katalog SHALL dapat di-cache **2 detik** oleh CloudFront (kunci: path + query filter).
7. Endpoint katalog SHALL publik (tanpa visitor token) — `security: []` pada kontrak.

**Error codes:** — (katalog tidak punya error khusus; filter tak dikenal diperlakukan default).

---

## Requirement 2 — Detail Event

**User story:** Sebagai calon pembeli, saya ingin melihat detail event beserta kategori tiket, harga,
ketersediaan, dan aturan (batas order/NIK, mixed categories), agar saya tahu apa yang bisa saya beli.

**Tertelusur ke:** SRS-F-03-01..05 · TC-EVT-04, TC-EVT-05

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events/{eventId}` dengan UUID valid, sistem SHALL mengembalikan `EventDetail`: `event`, `maxTicketsPerOrder`, `maxTicketsPerBuyerNik`, `allowMixedCategories`, dan `categories[]` (`categoryId`, `code`, `name`, `priceMinor`, `available`).
2. Nilai `available` per kategori SHALL dihitung `capacity − held_quantity − sold_quantity` dari PostgreSQL (otoritatif; **bukan** Valkey).
3. IF `eventId` bukan UUID valid, THEN sistem SHALL mengembalikan `400 VALIDATION_ERROR`.
4. IF event tidak ada, THEN sistem SHALL mengembalikan `404 NOT_FOUND`.
5. Respons detail SHALL dapat di-cache **2 detik** oleh CloudFront.

**Error codes:** `VALIDATION_ERROR` (400), `NOT_FOUND` (404).

---

## Requirement 3 — Admission & Reservasi (Invarian #2 "Jangan PHP")

**User story:** Sebagai pembeli yang gilirannya tiba di antrean, saya ingin unit tiket direservasi
untuk saya saat di-admit, agar saya dijamin mendapat tiket sampai TTL habis (tidak PHP).

**Tertelusur ke:** SRS-F-04, SRS-F-06-02, SRS-F-09-01..08 · TC-ADM-01..05

### Kriteria Penerimaan (EARS)

1. WHEN Queue memanggil `POST /internal/v1/allocations:batch`, sistem SHALL — untuk setiap entry yang berhasil — menaikkan `event_supply.allocated_units` dan menyisipkan baris `admissions` berstatus `RESERVED` dengan snapshot config, **dalam satu transaksi**, lalu membalas `outcome` ∈ {`GRANTED`,`NO_SUPPLY_NOW`,`SOLD_OUT`,`BUYER_QUOTA_EXHAUSTED`,`ALREADY_ACTIVE`} (TC-ADM-01).
2. Operasi alokasi SHALL idempoten per `queueEntryId` (`admissions.queue_entry_id UNIQUE`); panggilan ulang SHALL mengembalikan hasil sama tanpa reservasi ganda.
3. WHILE admission ∈ {`RESERVED`,`ACTIVE`,`EDITABLE`}, sistem SHALL menjamin **satu** admission aktif per `(event_id, visitor_id)` (anti-calo `uq_admission_active_visitor`) dan per `(event_id, buyer_nik_hash)` (`uq_admission_active_buyer`).
4. WHILE admission aktif dan TTL belum habis, IF pembeli melakukan hold yang valid, THEN sistem SHALL mengizinkan hold berhasil — jaminan **Jangan PHP** (TC-ADM-02, invarian #2).
5. WHERE sebagian kategori sold out, WHEN pembeli memilih kategori yang masih ada, THEN hold kategori tersebut SHALL tetap berhasil (TC-ADM-05, SRS-F-09-07).
6. WHEN klien `POST /api/v1/booking-sessions` dengan `admissionToken` (one-time, `admission_jti UNIQUE`), sistem SHALL mengubah admission `RESERVED → ACTIVE` dan mengembalikan `BookingSession` dengan `expiresAt`, `serverTime`, `phase`, `reservedUnits`, `maxSelectable`, `categories`.
7. IF `admissionToken` tidak valid/dipakai ulang, THEN sistem SHALL mengembalikan `403 ADMISSION_TOKEN_INVALID`; IF giliran lewat/claim deadline terlampaui, THEN `410 TURN_MISSED`/`SESSION_EXPIRED`.

**Error codes:** `ADMISSION_TOKEN_INVALID` (403), `VISITOR_MISMATCH` (403), `TURN_MISSED` (410), `SESSION_EXPIRED` (410).

---

## Requirement 4 — Snapshot Konfigurasi per Sesi & Penguncian Aturan

**User story:** Sebagai pembeli yang sedang memilih, saya ingin aturan event terkunci untuk sesi saya,
agar perubahan konfigurasi admin tidak mengganggu sesi yang sedang berjalan.

**Tertelusur ke:** SRS-F-26-01..08 · TC-CFG-01..04

### Kriteria Penerimaan (EARS)

1. WHEN admission dibuat, sistem SHALL menyimpan snapshot config ke baris admission: `cfg_selection_ttl_seconds`, `cfg_payment_ttl_seconds`, `cfg_submit_grace_seconds`, `cfg_edit_enabled`, `cfg_max_edits`, `cfg_allow_mixed_categories`.
2. WHILE sesi berjalan, sistem SHALL memakai nilai **snapshot admission**, bukan nilai event terkini (TC-CFG-01).
3. WHEN event beralih ke status `AVAILABLE`, trigger `guard_event_rules`/`lock_rules_on_insert` SHALL mengisi `rules_locked_at` otomatis.
4. IF ada upaya mengubah `max_tickets_per_order`, `max_tickets_per_buyer_nik`, atau `allow_mixed_categories` setelah `rules_locked_at` terisi, THEN DB SHALL menolak dengan exception `EVENT_RULES_LOCKED` (ERRCODE `check_violation`) → dipetakan ke error domain.
5. WHEN konfigurasi event diubah, trigger `audit_event_config` SHALL mencatat perubahan (`event_id`, `changed_by` dari `app.actor`, `field`, `old_value`→`new_value`) ke `config_audit` (TC-CFG-02..04).
6. TTL dan jendela (`selection_ttl_seconds`, `payment_ttl_seconds`, `claim_window_seconds`, warning/grace) BOLEH berubah setelah lock; nilai lama tetap berlaku untuk sesi berjalan via snapshot.

**Error codes:** `EVENT_RULES_LOCKED` (internal → 409, admin).

---

## Requirement 5 — Validasi NIK Pemegang & Kuota Pembeli (Invarian #3)

**User story:** Sebagai sistem, saya ingin memvalidasi NIK pemegang dan menegakkan kuota pembeli, agar
batas 3 tiket per NIK pembeli dan "satu tiket satu NIK pemegang" tidak dilanggar.

**Tertelusur ke:** SRS-F-11-01..10 · TC-BKG-01, TC-BKG-02, TC-BKG-03, TC-BKG-08..12

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/booking-sessions/{sessionId}/attendee-checks` dengan `{ nik }` (pattern `^[0-9]{16}$`), sistem SHALL memvalidasi struktur NIK (Lampiran A.1) dan membalas `AttendeeCheck{ result ∈ {AVAILABLE, ALREADY_REGISTERED, CHECK_LIMIT_REACHED}, checksRemaining }`.
2. IF struktur NIK salah, THEN sistem SHALL mengembalikan `422 NIK_INVALID` **tanpa** mengurangi jatah cek.
3. WHEN NIK sama dicek ulang dalam satu sesi, sistem SHALL tidak menghitungnya lagi ke batas (dedupe via PK `attendee_checks(admission_id, attendee_nik_hash)`).
4. IF jumlah NIK unik dicek melebihi `max_attendee_checks_per_session` (default 10, rentang 3..50), THEN hasil SHALL `CHECK_LIMIT_REACHED` dan request berikut `429 RATE_LIMITED`.
5. IF `held_quantity + paid_quantity + jumlah_item > max_quantity` pada `buyer_quotas` (≤3), THEN sistem SHALL menolak hold dengan `409 BUYER_QUOTA_EXHAUSTED`, termasuk booking aktif lain dan lintas sesi/perangkat (TC-BKG-01, invarian #3).
6. IF NIK pemegang sudah punya item aktif (`HELD`/`PAID`) di event sama, THEN sistem SHALL menolak dengan `409 ATTENDEE_NIK_ALREADY_REGISTERED` (indeks `uq_attendee_nik_per_event`, TC-BKG-03).
7. IF satu request memuat NIK pemegang duplikat, THEN sistem SHALL menolak dengan `409 ATTENDEE_NIK_DUPLICATE_IN_ORDER` (`uq_booking_item_nik_active`, TC-BKG-02).
8. Kuota pembeli SHALL terpisah dari identitas pemegang: satu pembeli dapat membeli untuk NIK pemegang lain selama kuota belum habis (TC-BKG-11).

**Error codes:** `NIK_INVALID` (422), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409), `BUYER_QUOTA_EXHAUSTED` (409), `RATE_LIMITED` (429).

---

## Requirement 6 — Hold Atomik (Invarian #1 "Tidak Oversell")

**User story:** Sebagai pembeli, saya ingin menahan (hold) tiket secara atomik, agar stok tidak
oversell meski banyak pembeli bersamaan.

**Tertelusur ke:** SRS-F-12-01..07, SRS-Q-FUN-01 · TC-BKG-04..07, TC-PERF-04

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/bookings` dengan header `Idempotency-Key` (16..64 char) dan `HoldRequest{ sessionId, items[1..3]{ categoryId, attendee{ nik, fullName } } }`, sistem SHALL membuat booking `PENDING_PAYMENT`, menaikkan `category_inventories.held_quantity`, `event_supply.held_units`, dan `buyer_quotas.held_quantity`, serta menyisipkan `booking_items` (`HELD`) — **dalam satu transaksi** dengan urutan lock tetap (IUnitOfWork): supply → category → quota → booking (TC-BKG-04).
2. Operasi hold SHALL idempoten per `(scope, owner_id, idempotency_key)` (`idempotency_records`); permintaan ulang identik SHALL mengembalikan `response_status`/`response_body` tersimpan.
3. IF `Idempotency-Key` sama tetapi `request_hash` berbeda, THEN sistem SHALL mengembalikan `409 IDEMPOTENCY_KEY_REUSED`.
4. IF `held_quantity + sold_quantity` akan melebihi `capacity`, THEN CHECK DB membatalkan transaksi dan sistem mengembalikan `409 CATEGORY_UNAVAILABLE`. **TC-BKG-07: 100 hold paralel pada 50 tiket terakhir → tepat 50 berhasil** (invarian #1, SRS-Q-FUN-01).
5. IF `items.length > maxTicketsPerOrder` (≤3), THEN sistem SHALL mengembalikan `422 QUANTITY_EXCEEDS_LIMIT`.
6. IF `cfg_allow_mixed_categories` false dan item memuat lebih dari satu kategori, THEN sistem SHALL mengembalikan `422 MIXED_CATEGORIES_NOT_ALLOWED`.
7. IF stok event habis (`event_supply` tak cukup), THEN sistem SHALL mengembalikan `409 CATEGORY_UNAVAILABLE`/sold out.
8. WHEN hold berhasil, sistem SHALL meng-append event `BookingHeld` (version 1) ke `booking_events` dan memproyeksikan `bookings`/`booking_items` dalam transaksi yang sama (Requirement 11).
9. IF dependency (DB/broker) sementara gagal, THEN sistem SHALL mengembalikan `503 DEPENDENCY_UNAVAILABLE`; klien mengulang dengan `Idempotency-Key` sama.

**Error codes:** `VALIDATION_ERROR` (400), `CATEGORY_UNAVAILABLE` (409), `BUYER_QUOTA_EXHAUSTED` (409), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409), `IDEMPOTENCY_KEY_REUSED` (409), `SESSION_EXPIRED` (410), `QUANTITY_EXCEEDS_LIMIT` (422), `MIXED_CATEGORIES_NOT_ALLOWED` (422), `NIK_INVALID` (422), `DEPENDENCY_UNAVAILABLE` (503).

---

## Requirement 7 — Sesi & Timeout (Invarian #4 "Timer Server-Authoritative")

**User story:** Sebagai sistem, saya ingin TTL sesi dan pembayaran dihitung server-authoritative, agar
klien tidak dapat memperpanjang sendiri dan slot kembali bebas saat kedaluwarsa.

**Tertelusur ke:** SRS-F-07-01..06, SRS-F-08-01..03 · TC-SES-01..08, TC-PAY-02

### Kriteria Penerimaan (EARS)

1. Sistem SHALL menghitung `admissions.expires_at` dan `bookings.payment_expires_at` dari waktu server + snapshot config; klien hanya menampilkan `expiresAt`/`serverTime` (invarian #4).
2. WHEN klien `GET /api/v1/booking-sessions/current?eventId=` atau `GET /api/v1/booking-sessions/{sessionId}`, sistem SHALL mengembalikan `expiresAt` dan `serverTime` (dan `phase`).
3. WHILE admission ∈ {`RESERVED`,`ACTIVE`,`EDITABLE`} dan `expires_at < now`, worker **session sweeper** (interval `WORKER_INTERVAL`) SHALL mengubah status ke `EXPIRED`/`RELEASED`, mengisi `ended_at`+`end_reason`, dan melepas alokasi/hold (TC-SES-05..08).
4. IF permintaan tiba setelah `admissions.expires_at`, THEN sistem SHALL mengembalikan `410 SESSION_EXPIRED`.
5. `bookings.payment_expires_at` SHALL dihitung sejak hold pertama dan **tidak di-reset** oleh ubah pesanan (kolom komentar migrasi).
6. IF pembayaran tidak selesai sebelum `payment_expires_at`, THEN booking SHALL kedaluwarsa (Requirement 9).

**Error codes:** `SESSION_EXPIRED` (410).

---

## Requirement 8 — Ubah Pesanan (Edit) & Pemulihan Sesi

**User story:** Sebagai pembeli, saya ingin mengubah pesanan sebelum menekan "Bayar" dan memulihkan
sesi setelah refresh/disconnect, agar saya tidak kehilangan slot karena gangguan jaringan.

**Tertelusur ke:** SRS-F-23-01..11, SRS-F-24-01..13, SRS-F-08-02, SRS-F-12-06 · TC-EDT-01..06, TC-NAV-01..03, TC-SES-05..08

### Kriteria Penerimaan (EARS)

1. WHILE `cfg_edit_enabled` true dan `bookings.payment_started_at` null, WHEN klien `PUT /api/v1/bookings/{bookingId}/items` dengan `ReplaceItemsRequest{ version, items[1..3] }` (**Δ2**), sistem SHALL menandai item lama `REPLACED`, membuat item baru `HELD`, menaikkan `bookings.version`, dan menaikkan `admissions.edit_count` — dalam satu transaksi; `paymentExpiresAt` **tidak berubah** (TC-EDT-01).
2. `ReplaceItemsRequest.items[].attendee` MAY berupa `{ nik, fullName }` baru ATAU `{ keepFromItemId }` untuk mempertahankan pemegang lama tanpa mengirim ulang NIK.
3. IF `version` yang dikirim ≠ versi terkini booking, THEN sistem SHALL menolak dengan `409 BOOKING_VERSION_CONFLICT` (pesanan lama tetap utuh).
4. IF `payment_started_at` sudah terisi, THEN sistem SHALL menolak dengan `409 BOOKING_LOCKED`.
5. IF `edit_count ≥ cfg_max_edits`, THEN sistem SHALL menolak dengan `409 EDIT_LIMIT_REACHED`.
6. Ubah pesanan SHALL meng-append event `BookingItemsReplaced` dan **tidak** me-reset `payment_expires_at`.
7. WHEN klien `GET /api/v1/booking-sessions/current?eventId=` dengan visitor token setelah refresh, sistem SHALL memulihkan sesi/booking aktif yang sama (idempoten, TC-NAV-01, TC-SES-05); IF tidak ada sesi aktif → `404 NOT_FOUND`.

**Error codes:** `BOOKING_LOCKED` (409), `BOOKING_VERSION_CONFLICT` (409), `EDIT_LIMIT_REACHED` (409), `CATEGORY_UNAVAILABLE` (409), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `QUANTITY_EXCEEDS_LIMIT` (422), `NOT_FOUND` (404).

---

## Requirement 9 — Settlement & Expiry Booking (Invarian #5 "Idempotent")

**User story:** Sebagai sistem, saya ingin menerapkan pembayaran sukses secara idempoten dan menangani
booking kedaluwarsa dengan benar, agar status akhir hanya berasal dari webhook/rekonsiliasi.

**Tertelusur ke:** SRS-F-16-01..03, SRS-F-17-01..05 · TC-PAY-03, TC-PAY-04, TC-PAY-06..08

### Kriteria Penerimaan (EARS)

1. WHEN event `payment.succeeded` (payload `{ bookingId, attemptId, settlementTime, amountMinor, method }`) diterima dari Payment, IF `booking.status != PAID`, THEN sistem SHALL menerapkan settlement **dalam satu transaksi**: `held → sold` (`category_inventories`, `event_supply`, `buyer_quotas`), item `HELD → PAID`, menerbitkan `tickets`, append `BookingPaid`, status `PAID` + isi `paid_at`/`settlement_time`, tulis outbox `booking.paid` (TC-PAY-03).
2. Consumer `payment.succeeded` SHALL idempoten via **guard state** (`status != PAID`, `ON CONFLICT DO NOTHING`, **tanpa tabel inbox**); kirim ganda SHALL tidak menggandakan tiket (invarian #5; uji kirim ganda wajib).
3. WHEN event `payment.failed` (`{ bookingId, attemptId, reason }`) diterima, sistem SHALL hanya mencatat; booking tetap menunggu hingga TTL.
4. WHILE booking ∈ {`PENDING_PAYMENT`,`VERIFYING`} dan `payment_expires_at < now`, worker **expiry sweeper** SHALL — untuk booking yang sudah memulai pembayaran — memanggil `POST /internal/v1/payments/{bookingId}/resolve` ke Payment.
5. IF resolve tidak menemukan pembayaran, THEN sistem SHALL meng-expire booking (status `EXPIRED`), melepas stok (`held → free`), append `BookingExpired`, tulis outbox `booking.expired` (`reason` ∈ {PAYMENT_TIMEOUT, SESSION_EXPIRED, CANCELLED}) (TC-PAY-04).
6. IF resolve menemukan pembayaran di gateway, THEN sistem SHALL membuat kasus `REVIEW` (status `REVIEW`), append `BookingReview`, tulis outbox `booking.review` — **bukan** oversell, **bukan** auto-cancel (TC-PAY-06..08, RISK-T-03).

**Error codes:** (consumer; tidak ada error HTTP publik) — payload rusak → `rabbitmq.Permanent(err)` → DLQ.

---

## Requirement 10 — Tiket & Riwayat Booking

**User story:** Sebagai pembeli, saya ingin melihat e-tiket saya, dan jika login, melihat riwayat
booking saya, agar saya punya bukti dan jejak pembelian.

**Tertelusur ke:** SRS-F-20-01..03, SRS-F-27-11 · TC-TKT-01..03, TC-USR-10

### Kriteria Penerimaan (EARS)

1. WHEN settlement sukses, sistem SHALL menerbitkan satu baris `tickets` per `booking_item` (`public_code UNIQUE`, 1:1 `booking_item_id UNIQUE`) (TC-TKT-01).
2. WHEN klien `GET /api/v1/bookings/{bookingId}/tickets` (**Δ3**) dan booking `PAID`, sistem SHALL mengembalikan `TicketList{ bookingId, tickets[]{ ticketCode, categoryCode, attendeeName, attendeeNikMasked, qrPayload } }`.
3. IF booking belum `PAID`, THEN sistem SHALL mengembalikan `409 BOOKING_NOT_PAYABLE`.
4. WHERE pembeli login, WHEN `GET /api/v1/me/bookings` dengan `userBearer` (JWT EdDSA User Service), sistem SHALL mengembalikan **hanya** booking dengan `account_id = token.sub` (TC-USR-10; tanpa kebocoran lintas akun), paginasi cursor (`limit` 1..50, default 20).
5. IF access token tidak valid, THEN sistem SHALL mengembalikan `401 UNAUTHORIZED`.

**Error codes:** `BOOKING_NOT_PAYABLE` (409), `NOT_FOUND` (404), `UNAUTHORIZED` (401).

---

## Requirement 11 — Event Sourcing & Integritas Stok

**User story:** Sebagai sistem, saya ingin siklus booking direkam append-only dan stok berubah dalam
transaksi yang sama, agar ada jejak audit yang dapat di-replay tanpa risiko oversell.

**Tertelusur ke:** SRS-C-09, SRS-Q-FUN-01, SRS-F-90-01 · TC-ES-01..05, TC-BKG-07, TC-OPS-01

### Kriteria Penerimaan (EARS)

1. WHEN state booking berubah, sistem SHALL meng-append event ke `booking_events` dengan `PRIMARY KEY (stream_id, version)`, memproyeksikan read model, mengubah stok, dan menulis outbox — semuanya dalam **satu** transaksi (ADR-019).
2. IF append menemui konflik versi (SQLSTATE `23505` pada `(stream_id, version)` → `ErrConcurrency`), THEN sistem SHALL membaca ulang state lalu mengulangi (re-read + retry, maks 3), **bukan** menimpa.
3. Payload `booking_events` SHALL **tidak** memuat NIK plaintext, email, atau token — hanya hash/masked (ditolak `pkg/eventstore`; invarian privasi, `security-privacy.md`).
4. DB SHALL menolak `UPDATE`/`DELETE`/`TRUNCATE` pada `booking_events` (trigger `trg_booking_events_append_only`, `trg_booking_events_no_truncate`, ERRCODE `restrict_violation`) (TC-ES-01).
5. Stok SHALL tidak pernah negatif: CHECK DB `held_quantity + sold_quantity <= capacity` (kategori) dan `allocated_units + held_units + sold_units <= total_capacity` (supply); counter negatif adalah pelanggaran kritis (TC-BKG-07).
6. `event_supply.total_capacity` SHALL selalu = Σ `category_inventories.capacity` (constraint trigger deferred `check_supply_matches_categories`, exception `SUPPLY_CATEGORY_MISMATCH` saat COMMIT).
7. WHEN job rekonsiliasi dijalankan, sistem SHALL mendeteksi inkonsistensi stok/booking dan mengirim alert (TC-OPS-01, SRS-F-90-01).

**Error codes:** `ErrConcurrency` (internal, retry), `SUPPLY_CATEGORY_MISMATCH` (internal, seed).

---

## Requirement 12 — Event Antar-Service (Transactional Outbox)

**User story:** Sebagai sistem, saya ingin menerbitkan event domain secara andal via outbox, agar Queue
dan Notification tetap konsisten meski broker sempat gagal.

**Tertelusur ke:** SRS-IF-30..31, SRS-Q-REL-* · TC-API-01, TC-API-02, TC-CAP-01..03

### Kriteria Penerimaan (EARS)

1. WHEN perubahan stok/uang di-commit, sistem SHALL menulis baris `outbox_events` (`routing_key`, `aggregate_id`, `schema_version`, `payload`, `available_at`) dalam transaksi yang sama (outbox **hanya** untuk perubahan stok atau uang).
2. Worker **outbox relay** (interval `WORKER_INTERVAL`) SHALL `SELECT ... WHERE published_at IS NULL ORDER BY available_at`, menerbitkan ke RabbitMQ exchange `rusuh.events` (topic) dalam **envelope** standar, lalu mengisi `published_at`.
3. Sistem SHALL menerbitkan event (routing key → consumer): `booking.journey.ended` → Queue; `booking.quota.exhausted` → Queue; `booking.sold_out` → Queue; `booking.paid` → Notification; `booking.expired` → Notification; `booking.review` → Notification/Payment.
4. IF penerbitan gagal, THEN relay SHALL menaikkan `publish_attempts`, mencatat `last_error`, dan mencoba lagi siklus berikutnya (tanpa kehilangan event).
5. Payload setiap event SHALL cocok dengan `docs/contracts/events/*.schema.json` dan envelope `envelope.schema.json`, **tanpa** PII plaintext (hanya `ticketLinkToken` diizinkan untuk email).

**Error codes:** (relay internal; tanpa error HTTP).

---

## Requirement 13 — Kesehatan, Konfigurasi & Observability

**User story:** Sebagai operator, saya ingin endpoint kesehatan, konfigurasi tervalidasi saat start,
dan metrik, agar ALB dan tim ops dapat memantau service.

**Tertelusur ke:** SRS-Q-MNT-02, SRS-Q-FLX-01 · TC-OBS-01, TC-OBS-02, TC-ASG-01

### Kriteria Penerimaan (EARS)

1. WHEN `GET /health` dipanggil, sistem SHALL mengembalikan `200 UP` bila semua dependency sehat atau `503 DOWN` bila ada yang gagal (dalam `HEALTH_TIMEOUT`), dengan `Health{ status, service, checkedAt, dependencies[] }`. ✅ *Terimplementasi.*
2. WHEN service start, sistem SHALL memvalidasi seluruh konfigurasi (`Config.Validate()` + validasi tiap sub-config) dan gagal cepat bila tidak valid. ✅ *Terimplementasi.*
3. Sistem SHALL mengekspos metrik: `rbt_booking_sessions_total{event_id,status}`, `rbt_booking_holds_total{event_id,status}`, `rbt_booking_settlements_total{event_id}`, `rbt_booking_expirations_total{event_id,reason}`, `rbt_booking_stock_gauge{event_id,type}`, `rbt_booking_event_store_version{aggregate_id}`.
4. IF counter stok < 0, atau expiry_rate > 20% selama 10m, atau outbox lag > 30s selama 5m, atau p99 DB > 100ms selama 5m, THEN sistem SHALL memicu alert sesuai severity (SDD §11.2).
5. Log SHALL terstruktur (slog JSON) dan **tidak pernah** memuat NIK/email/token (`security-privacy.md`).

**Error codes:** (ops; tanpa error domain).

---

## Invarian yang Ditegakkan

| # | Invarian | Ditegakkan oleh | Requirement | TC utama |
|---|----------|------------------|-------------|----------|
| #1 | **Tidak oversell** — stok hanya berubah di Booking dalam satu transaksi | CHECK DB `held+sold<=capacity`, `allocated+held+sold<=total_capacity`; IUnitOfWork urutan lock tetap | R6, R11 | TC-BKG-07, TC-PERF-04 |
| #2 | **Jangan PHP** — admitted dijamin dapat tiket sampai TTL | reservasi `allocated_units` saat admission; `check_supply_matches_categories` (supply=Σkategori) | R3 | TC-ADM-02, TC-ADM-05 |
| #3 | **Kuota 3/NIK pembeli/event** lintas sesi & "1 tiket 1 NIK pemegang" | `buyer_quotas` CHECK ≤3; `uq_attendee_nik_per_event`; `uq_booking_item_nik_active` | R5 | TC-BKG-01..03, TC-BKG-11 |
| #4 | **Timer server-authoritative** | `expires_at`/`payment_expires_at` dihitung server; sweeper; klien hanya tampil | R7 | TC-SES-05..08 |
| #5 | **Settlement idempoten; status akhir hanya dari webhook/rekonsiliasi** | guard state `status != PAID`, append-only event store, `ON CONFLICT DO NOTHING` | R9 | TC-PAY-03, TC-PAY-06..08 |
| (#6 global) | Kegagalan notifikasi tidak membatalkan transaksi | outbox + retry berjenjang + DLQ (Notification async) | R12 | TC-CAP-01..03 |

---

## Build & Verify

**Run (lokal):**
```bash
make infra keys                          # PostgreSQL, Valkey, RabbitMQ, kunci token
cd services/booking && cp .env.example .env
go run ./cmd migrate up                  # migrasi (embed, advisory lock)
go run ./cmd api &                       # HTTP :8082
go run ./cmd worker &                    # sweeper + outbox relay (saat dibangun)
curl localhost:8082/health               # 200 UP
```

**Verify:**
```bash
make build              # compile semua modul
make test               # unit test -race
make test-integration   # + PostgreSQL/Valkey/RabbitMQ (build tag integration)
make cover              # ambang coverage logika 80% (COVERAGE_MIN)
make lint               # golangci-lint v2 + depguard (penegak lapisan)
make isolation          # build GOWORK=off (tanpa go.work)
make ci                 # seluruh gerbang CI lokal
```

Gerbang CI (`.github/workflows/ci.yml`): `code-quality` → `verify-code-quality` → `case-coverage`
(unit + integration `-race`, coverage 80%) → `code-rank` (SonarCloud) → `build`/isolation →
`package`/`scan-package`. Coverage dihitung atas kode logika; `cmd/`, `internal/delivery/`,
`internal/config/`, `_mock/` dikecualikan. `make ci` wajib lolos sebelum PR (CODEOWNERS review).

---

## Matriks Keterlacakan Ringkas

| Req | SRS | Test Cases | Invarian |
|-----|-----|-----------|----------|
| 1 Katalog | F-02, F-28 | TC-EVT-01..03, TC-CAT-01..06 | — |
| 2 Detail | F-03 | TC-EVT-04..05 | — |
| 3 Admission | F-04, F-06, F-09 | TC-ADM-01..05 | #2 |
| 4 Config snapshot | F-26 | TC-CFG-01..04 | #4 |
| 5 NIK & Kuota | F-11 | TC-BKG-01..03, 08..12 | #3 |
| 6 Hold atomik | F-12, Q-FUN-01 | TC-BKG-04..07, TC-PERF-04 | #1 |
| 7 Sesi & timeout | F-07, F-08 | TC-SES-01..08, TC-PAY-02 | #4 |
| 8 Edit & recovery | F-23, F-24, F-08-02 | TC-EDT-01..06, TC-NAV-01..03 | — |
| 9 Settlement & expiry | F-16, F-17 | TC-PAY-03..08 | #5 |
| 10 Tiket & history | F-20, F-27-11 | TC-TKT-01..03, TC-USR-10 | — |
| 11 Event sourcing & stok | C-09, Q-FUN-01, F-90 | TC-ES-01..05, TC-BKG-07, TC-OPS-01 | #1, #5 |
| 12 Outbox/event | IF-30..31, Q-REL | TC-API-01..02, TC-CAP-01..03 | #6 |
| 13 Health/obs | Q-MNT-02, Q-FLX-01 | TC-OBS-01..02, TC-ASG-01 | — |

---

## Lampiran A — Validasi & Aturan Bisnis

### A.1 Validasi NIK (16 digit)
- Panjang tepat 16, numerik (pattern `^[0-9]{16}$`).
- Kode provinsi `[0:2]` ∈ 01..99.
- Tanggal lahir `[6:8]`: `dd` ≤ 31 (laki-laki) atau 41..71 (perempuan, +40).
- NIK **tidak pernah** disimpan plaintext — hanya `*_nik_hash` (HMAC) + `*_nik_masked` + `attendee_nik_enc` (AES-256-GCM).

### A.2 Batas field
| Field | Aturan |
|-------|--------|
| `Idempotency-Key` | 16..64 char, wajib pada hold & replace |
| `items` | 1..3 item per request |
| `attendee.fullName` | 1..100 char (DB: `attendee_name` 2..100) |
| `nik` | `^[0-9]{16}$` |
| `limit` (me/bookings) | 1..50, default 20 |

### A.3 Aturan bisnis → error
| Aturan | Error code |
|--------|-----------|
| item > maxTicketsPerOrder | `QUANTITY_EXCEEDS_LIMIT` (422) |
| mixed categories dilarang | `MIXED_CATEGORIES_NOT_ALLOWED` (422) |
| kuota pembeli habis | `BUYER_QUOTA_EXHAUSTED` (409) |
| NIK pemegang sudah terdaftar | `ATTENDEE_NIK_ALREADY_REGISTERED` (409) |
| NIK duplikat dalam order | `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409) |
| edit setelah payment start | `BOOKING_LOCKED` (409) |
| version tidak cocok | `BOOKING_VERSION_CONFLICT` (409) |
| edit melebihi batas | `EDIT_LIMIT_REACHED` (409) |
| sesi TTL habis | `SESSION_EXPIRED` (410) |
| giliran lewat | `TURN_MISSED` (410) |
