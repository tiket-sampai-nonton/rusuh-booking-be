# Requirements Document — Booking Service

## Metadata Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Service** | `services/booking` |
| **Port** | 8082 |
| **Schema / Role** | `booking` / `svc_booking` (hanya melihat schema sendiri) |
| **Owner** | BE-2 |
| **Region** | ap-southeast-3 (Jakarta) |
| **Dependency** | PostgreSQL 16 (otoritas stok/uang), Valkey 8.1 (cache SSE, rate limit), RabbitMQ (event + outbox), Payment Service (resolve) |
| **Verb CLI (Cobra)** | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` |
| **Arsitektur** | Hexagonal (ports & adapters) + event sourcing untuk agregat Booking (ADR-019) |
| **Sumber (urut prioritas)** | migrasi `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` → kontrak `docs/contracts/openapi/{booking,internal-booking,internal-payment}.yaml`, `docs/contracts/events/*.schema.json` → SRS-RBT-001 §3.1.4, §3.2.2–3.2.26 → **SDD-RBT-001 §4.7 (lock order, SQL, sweeper, race table C.1–C.10)** → `docs/sdd/SDD-BOOKING-001.md` (ringkasan) |

## Introduction

Booking Service adalah **jantung sistem** Rusuh Booking Tiket dan **otoritas tunggal stok dan uang**:
mengelola katalog event, inventori tiket, alokasi/admission, sesi pemilihan dengan TTL, hold atomik,
ubah pesanan, penguncian pesanan saat "Bayar", settlement, penerbitan e-tiket, dan ekspiri. Semua
perubahan stok dan uang terjadi dalam **satu transaksi PostgreSQL** dengan urutan lock tetap; siklus
booking direkam sebagai **event sourcing** append-only (ADR-019) dengan read model yang diproyeksikan
sinkron dalam transaksi yang sama.

Kriteria ditulis bergaya **EARS** (WHEN event, WHILE state, IF/THEN kondisi, WHERE fitur, SHALL
kewajiban), tertelusur ke **SRS** (`SRS-F-xx-yy`) dan **Test Plan** (`TC-xx`, `docs/test/06-TP-RBT-001.md`).
Skema mengikuti **kode migrasi** yang lebih kaya dari prosa SDD-BOOKING-001 §6 — perbedaan ditandai **Δ**.

### Delta prosa SDD-BOOKING-001 → sumber yang dipakai (ditandai Δ)

| # | Prosa SDD-BOOKING-001 | Yang dipakai spec ini |
|---|-----------------------|------------------------|
| Δ1 | Status booking `HELD → LOCKED → PAID` | `PENDING_PAYMENT → (VERIFYING) → PAID/EXPIRED/CANCELLED/REVIEW` (`bookings.status` CHECK). Pesanan "terkunci" = admission `CONVERTED` + `bookings.payment_started_at`, **bukan** status booking |
| Δ2 | Edit via `PUT /api/v1/bookings/{id}` | `PUT /api/v1/bookings/{bookingId}/items` (penggantian atomik, kirim `version`) |
| Δ3 | Tiket via `GET /api/v1/tickets/{bookingId}` | `GET /api/v1/bookings/{bookingId}/tickets` |
| Δ4 | Kolom `price`, `held`, `sold`, `amount` | `price_minor`, `held_quantity`, `sold_quantity`, `amount_minor` (minor unit IDR) |
| Δ5 | Error `CATEGORY_SOLD_OUT`, `BUYER_QUOTA_EXCEEDED`, `NIK_ALREADY_HAS_TICKET`, `MAX_TICKETS_EXCEEDED`, `MAX_EDITS_EXCEEDED` | `CATEGORY_UNAVAILABLE`, `BUYER_QUOTA_EXHAUSTED`, `ATTENDEE_NIK_ALREADY_REGISTERED`, `QUANTITY_EXCEEDS_LIMIT`, `EDIT_LIMIT_REACHED` |
| Δ6 | Admission state `ENDED` | `CONVERTED`, `EXPIRED`, `RELEASED` (akhir via `end_reason`) |
| Δ7 | `payment-lock` generik | Idempoten per `attemptKey`, `outcome ∈ {OK, BOOKING_NOT_PAYABLE, PAYMENT_EXPIRED}` |
| Δ8 | SSE event `availability` + `session` | Kontrak: `availability` + `timer` (lihat OQ-2) |
| Δ9 | Sweeper: "ACTIVE → ENDED"; expiry "HELD/LOCKED → resolve → REVIEW bila ada bayar" | SDD-RBT §4.7.1 C.4/C.10: `EDITABLE` dikunci (`SELECTION_EXPIRED`) bukan diakhiri; booking lewat deadline → `VERIFYING` → resolve; `SUCCEEDED` tepat waktu → `PAID` (K2, K3) |
| Δ10 | Urutan lock "supply → category → quota → booking" (spec lama) | `idempotency → admissions → bookings → event_supply → buyer_quotas → category_inventories ASC` (SDD-RBT §4.7, K5) |
| Δ11 | Routing key `booking.paid` | Berversi `booking.paid.v1` (SDD-RBT §4.6.2, K8) |

### Status Implementasi (audit kode 2026-10-21)

| Area | Status | Bukti di `services/booking/` |
|------|--------|------------------------------|
| Skema DB (14 tabel) + trigger invarian (append-only, `guard_event_rules`, `audit_event_config`, `check_supply_matches_categories` deferred, CHECK anti-oversell, indeks unik anti-calo) | ✅ | `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` |
| Kolom `events.city` (SRS-F-28-02) dan `image_url`/`banner_url`/`thumbnail_url` (SDD-BOOKING App. C.5) | ❌ **tidak ada di migrasi** | butuh migrasi baru `0003_*` (file terterapkan tidak boleh diubah) |
| CLI verb, config fail-fast, `GET /health`, composition root server + worker loop | ✅ (loop kosong) | `cmd/*.go`, `internal/config/*`, `internal/delivery/*` |
| Domain/usecase/adapter bisnis, worker nyata, consumer/publisher, internal API | ❌ | hanya slice `health`; `worker.go` → `jobs(*Clients) []Job { return nil }` |

## Glossary

- **Admission** — baris `admissions`: alokasi `units` tingkat event untuk satu entry antrean + sesi pemilihan; status `RESERVED → ACTIVE → EDITABLE → CONVERTED` atau berakhir `EXPIRED`/`RELEASED`.
- **Units (N)** — jumlah unit yang direservasi saat admission = `min(max_tickets_per_order, sisa kuota pembeli, free stock)`.
- **Free stock** — `total_capacity − allocated_units − held_units − sold_units` di `event_supply`.
- **Hold** — booking `PENDING_PAYMENT` dengan item `HELD`; memindahkan unit dari `allocated` ke `held`.
- **Pesanan terkunci** — admission `CONVERTED` dengan `lock_reason ∈ {PAYMENT_STARTED, SELECTION_EXPIRED, EDIT_DISABLED, BOOKING_ENDED}`; sisa unit (N − n) dilepas.
- **TTL Booking** — `admissions.expires_at` (snapshot `cfg_selection_ttl_seconds`); batas pilih dan ubah pesanan.
- **TTL Pembayaran** — `bookings.payment_expires_at` (snapshot `cfg_payment_ttl_seconds`), dimulai saat hold pertama, tidak pernah di-reset.
- **Grace** — `cfg_submit_grace_seconds` (default 5): request yang tiba dalam masa ini setelah TTL Booking diproses lalu langsung dikunci.
- **Verify grace** — `bookings.verification_until`: batas retry resolve saat booking `VERIFYING`.
- **Pembeli / pemegang** — NIK pembeli (kuota ≤ 3/event) berbeda dari NIK pemegang tiket (≤ 1 item `HELD`/`PAID`/event).
- **Resolve** — `POST /internal/v1/payments/{bookingId}/resolve` ke Payment, `outcome ∈ {SUCCEEDED, NOT_PAID, NO_ATTEMPT, UNKNOWN}`.

---

## Requirement 1 — Katalog Event

**User story:** Sebagai calon pembeli, saya ingin melihat daftar event dengan filter (waktu, kota,
status) dan urutan, agar saya dapat menemukan konser yang ingin saya beli tiketnya.

**Tertelusur ke:** SRS-F-02-01..04, SRS-F-28-01..06 · TC-EVT-01..03, TC-CAT-01..06

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events`, sistem SHALL mengembalikan `{ meta, data:{ events[], serverTime } }`; setiap `EventSummary` memuat `eventId`, `slug`, `name`, `venue`, `startsAt`, `salesStartsAt`, `salesEndsAt`, `status`, `minPriceMinor` (TC-EVT-01).
2. WHERE `timeframe` ∈ {`ALL`(default),`THIS_WEEK`(Senin–Minggu),`THIS_MONTH`,`UPCOMING`(hari ini ke depan)}, sistem SHALL memfilter `startsAt` relatif tanggal server (TC-CAT-01, TC-CAT-03, SRS-F-28-01/03).
3. WHERE `city` diberikan, sistem SHALL memfilter `events.city` case-insensitive (TC-CAT-02, SRS-F-28-02). *(Kolom `city` belum ada di migrasi — ditambah lewat migrasi `0003`.)*
4. WHERE `status` ∈ {`ALL`(default),`NOT_STARTED`,`AVAILABLE`,`SOLD_OUT`,`CLOSED`}, sistem SHALL hanya mengembalikan event dengan status itu; event `DRAFT` tidak pernah tampil (SRS-F-28-01).
5. WHERE beberapa filter diberikan, sistem SHALL mengembalikan irisan (AND) (TC-CAT-04, SRS-F-28-04).
6. WHERE `sort` ∈ {`DATE_ASC`(default),`PRICE_ASC`,`PRICE_DESC`}, sistem SHALL mengurutkan hasil sesuai (TC-CAT-06, SRS-F-28-06).
7. Respons katalog SHALL dapat di-cache ≤ **2 detik** di CDN dengan cache key memuat parameter filter (TC-CAT-05, SRS-F-02-04, SRS-F-28-05).
8. Endpoint katalog SHALL publik (tanpa visitor token) — `security: []` pada kontrak.

**Error codes:** — . Kontrak hanya mendefinisikan `200`; perilaku untuk nilai filter tak dikenal (abaikan → default, atau `400 VALIDATION_ERROR`) belum ditetapkan — OQ-14.

---

## Requirement 2 — Detail Event

**User story:** Sebagai calon pembeli, saya ingin melihat detail event beserta kategori tiket, harga,
ketersediaan, dan aturan (batas order/NIK, mixed categories), agar saya tahu apa yang bisa saya beli.

**Tertelusur ke:** SRS-F-03-01..05 · TC-EVT-04, TC-EVT-05

### Kriteria Penerimaan (EARS)

1. WHEN klien `GET /api/v1/events/{eventId}` dengan UUID valid, sistem SHALL mengembalikan `EventDetail`: `event`, `maxTicketsPerOrder`, `maxTicketsPerBuyerNik`, `allowMixedCategories`, dan `categories[]` (`categoryId`, `code`, `name`, `priceMinor`, `available`).
2. Nilai `available` per kategori SHALL dihitung `capacity − held_quantity − sold_quantity` dari PostgreSQL (otoritatif; **bukan** Valkey); reservasi tingkat event (`allocated_units`) SHALL tidak mengurangi angka per kategori (SRS-F-03-03).
3. Kategori dengan `available = 0` SHALL tetap dikembalikan (klien menampilkan "Habis", SRS-F-03-05).
4. IF `eventId` bukan UUID valid, THEN sistem SHALL mengembalikan `400 VALIDATION_ERROR`.
5. IF event tidak ada atau `DRAFT`, THEN sistem SHALL mengembalikan `404 NOT_FOUND`.
6. Respons detail SHALL dapat di-cache ≤ **2 detik** oleh CDN.

**Error codes:** `VALIDATION_ERROR` (400), `NOT_FOUND` (404).

---

## Requirement 3 — Admission & Reservasi (Invarian #2 "Jangan PHP")

**User story:** Sebagai pembeli yang gilirannya tiba di antrean, saya ingin unit tiket direservasi
untuk saya saat di-admit, agar saya dijamin mendapat tiket sampai TTL habis (tidak PHP).

**Tertelusur ke:** SRS-F-06-02, SRS-F-07-01, SRS-F-09-01..08, SRS-F-10-01 · TC-ADM-01..05, TC-PERF-05

### Kriteria Penerimaan (EARS)

1. WHEN Queue memanggil `POST /internal/v1/allocations:batch`, sistem SHALL memproses setiap entry dalam **transaksi pendek terpisah**: kunci `event_supply` dan `buyer_quotas`, hitung `units = min(events.max_tickets_per_order, max_quantity − (held+paid), free)`, sisipkan `admissions(RESERVED, expires_at = now + claim_window_seconds, snapshot cfg_*)`, dan naikkan `event_supply.allocated_units += units` (TC-ADM-01, SDD C.2).
2. Sistem SHALL membalas per entry `outcome` ∈ {`GRANTED` (+`allocationId`, `units`, `claimDeadline`), `NO_SUPPLY_NOW`, `SOLD_OUT`, `BUYER_QUOTA_EXHAUSTED`, `ALREADY_ACTIVE`} dengan aturan: sisa kuota = 0 → `BUYER_QUOTA_EXHAUSTED`; `free = 0` dan `allocated + held > 0` → `NO_SUPPLY_NOW`; `free + allocated + held = 0` → `SOLD_OUT`; admission aktif lain untuk NIK pembeli/visitor yang sama → `ALREADY_ACTIVE`.
3. Operasi alokasi SHALL idempoten per `queueEntryId` (`admissions.queue_entry_id UNIQUE`); panggilan ulang SHALL mengembalikan hasil sama tanpa reservasi ganda.
4. WHILE admission ∈ {`RESERVED`,`ACTIVE`,`EDITABLE`}, sistem SHALL menjamin **satu** admission aktif per `(event_id, visitor_id)` (`uq_admission_active_visitor`) dan per `(event_id, buyer_nik_hash)` (`uq_admission_active_buyer`).
5. WHEN `free + allocated + held = 0`, sistem SHALL menandai event `SOLD_OUT` dan menulis outbox `booking.sold_out.v1` dalam transaksi yang sama (SRS-F-09-05, SRS-F-10-01, TC-PERF-05).
6. WHILE admission aktif dan TTL belum habis, IF pembeli melakukan hold ≤ `units` dari kategori yang tersedia, THEN hold SHALL berhasil — jaminan **Jangan PHP** (TC-ADM-02, SRS-Q-FUN-02).
7. WHERE sebagian kategori habis, WHEN pembeli memilih kategori yang masih ada, THEN hold SHALL tetap berhasil dan reservasi tetap utuh (TC-ADM-05, SRS-F-09-07).
8. WHEN klien `POST /api/v1/booking-sessions` dengan `admissionToken` valid (EdDSA terbitan Queue, `jti` sekali pakai = `admissions.admission_jti UNIQUE`, terikat visitor), sistem SHALL mengubah admission `RESERVED → ACTIVE`, mengisi `claimed_at`, `expires_at = now + cfg_selection_ttl_seconds`, dan mengembalikan `201 BookingSession` (`expiresAt`, `serverTime`, `phase=SELECTING`, `reservedUnits`, `maxSelectable`, `categories`) (SRS-F-07-01, TC-SES-01).
9. WHEN `POST /api/v1/booking-sessions` diulang untuk admission yang sudah `ACTIVE`/`EDITABLE` dengan visitor yang sama, sistem SHALL mengembalikan sesi yang sama tanpa memperpanjang TTL (SRS-F-07-02).
10. IF `admissionToken` tidak valid/dipakai visitor lain, THEN `403 ADMISSION_TOKEN_INVALID`; IF claim deadline terlampaui, THEN `410 TURN_MISSED`; IF sesi sudah berakhir, THEN `410 SESSION_EXPIRED`.

**Error codes:** `ADMISSION_TOKEN_INVALID` (403), `VISITOR_MISMATCH` (403), `TURN_MISSED` (410), `SESSION_EXPIRED` (410).

---

## Requirement 4 — Snapshot Konfigurasi per Sesi & Penguncian Aturan

**User story:** Sebagai pembeli yang sedang memilih, saya ingin aturan event terkunci untuk sesi saya,
agar perubahan konfigurasi admin tidak mengganggu sesi yang sedang berjalan.

**Tertelusur ke:** SRS-F-26-01..08 · TC-CFG-01..04

### Kriteria Penerimaan (EARS)

1. WHEN admission dibuat, sistem SHALL menyimpan snapshot config ke baris admission: `cfg_selection_ttl_seconds`, `cfg_payment_ttl_seconds`, `cfg_submit_grace_seconds`, `cfg_edit_enabled`, `cfg_max_edits`, `cfg_allow_mixed_categories`.
2. WHILE sesi berjalan, sistem SHALL memakai nilai **snapshot admission**, bukan nilai event terkini; perubahan hanya berlaku untuk admission baru dan tidak pernah memperpendek sesi berjalan (TC-CFG-01).
3. WHEN event beralih ke status `AVAILABLE`, trigger `guard_event_rules`/`lock_rules_on_insert` SHALL mengisi `rules_locked_at` otomatis.
4. IF ada upaya mengubah `max_tickets_per_order`, `max_tickets_per_buyer_nik`, atau `allow_mixed_categories` setelah `rules_locked_at` terisi, THEN DB SHALL menolak dengan exception `EVENT_RULES_LOCKED` (ERRCODE `check_violation`) → `409 EVENT_RULES_LOCKED` (TC-CFG-02).
5. WHEN konfigurasi event diubah, trigger `audit_event_config` SHALL mencatat (`event_id`, `changed_by` dari `app.actor`, `field`, `old_value → new_value`) ke `config_audit` (TC-CFG-04).
6. TTL dan jendela (`selection_ttl_seconds`, `payment_ttl_seconds`, `claim_window_seconds`, warning/grace) BOLEH berubah setelah lock; kombinasi tidak valid SHALL ditolak oleh CHECK DB (TC-CFG-03).
7. Sistem SHALL menyediakan cara melihat konfigurasi efektif per event untuk internal (SRS-F-26-07). *(Kontrak `internal-booking.yaml` belum punya endpoint baca — OQ-12.)*

**Error codes:** `EVENT_RULES_LOCKED` (409, admin).

---

## Requirement 5 — Validasi NIK Pemegang & Kuota Pembeli (Invarian #3)

**User story:** Sebagai sistem, saya ingin memvalidasi NIK pemegang dan menegakkan kuota pembeli, agar
batas 3 tiket per NIK pembeli dan "satu tiket satu NIK pemegang" tidak dilanggar.

**Tertelusur ke:** SRS-F-11-01..11 · TC-BKG-01..03, TC-BKG-08..12, TC-PAY-04, TC-PAY-09

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/booking-sessions/{sessionId}/attendee-checks` dengan `{ nik }` untuk sesi milik visitor yang `ACTIVE`/`EDITABLE` dan belum lewat `expires_at`, sistem SHALL memvalidasi struktur NIK (`pkg/nik`, platform R1) dan membalas `AttendeeCheck{ result ∈ {AVAILABLE, ALREADY_REGISTERED, CHECK_LIMIT_REACHED}, checksRemaining }` (TC-BKG-09).
2. IF struktur NIK salah, THEN sistem SHALL mengembalikan `422 NIK_INVALID` **tanpa** mengurangi jatah cek (TC-BKG-08).
3. WHEN NIK sama dicek ulang dalam satu sesi, sistem SHALL menjawab tanpa menghitungnya lagi (PK `attendee_checks(admission_id, attendee_nik_hash)`).
4. IF jumlah NIK unik dicek mencapai `max_attendee_checks_per_session` (default 10, rentang 3..50), THEN NIK baru SHALL dijawab `CHECK_LIMIT_REACHED`. *(Status HTTP/kode error: OQ-1 — kontrak `RATE_LIMITED` 429 vs SRS `ATTENDEE_CHECK_LIMIT_REACHED` 429.)*
5. WHERE NIK pemegang ada di pesanan milik pembeli sendiri (mode ubah), cek SHALL menjawab `AVAILABLE` (SRS-F-11-06, SDD C.6).
6. Cek langsung SHALL **tidak** mengunci NIK; keputusan final atomik saat hold, dan kegagalan hold SHALL membiarkan sesi dan reservasi utuh (SRS-F-11-07).
7. IF `held_quantity + paid_quantity + n > max_quantity` pada `buyer_quotas` (≤ 3, lintas sesi/perangkat), THEN sistem SHALL menolak hold dengan `409 BUYER_QUOTA_EXHAUSTED` (TC-BKG-01, invarian #3).
8. IF NIK pemegang sudah punya item `HELD`/`PAID` di event sama, THEN sistem SHALL menolak dengan `409 ATTENDEE_NIK_ALREADY_REGISTERED` tanpa mengungkap pemiliknya (`uq_attendee_nik_per_event`, TC-BKG-03, SRS-F-11-09).
9. IF satu request memuat NIK pemegang duplikat, THEN sistem SHALL menolak dengan `409 ATTENDEE_NIK_DUPLICATE_IN_ORDER` (`uq_booking_item_nik_active`, TC-BKG-02).
10. Kuota pembeli SHALL terpisah dari status pemegang dan per event: menjadi pemegang tidak mengurangi kuota pembeli orang itu (TC-BKG-10, TC-BKG-11).
11. WHEN booking menjadi `EXPIRED` atau `CANCELLED`, sistem SHALL mengembalikan kuota (`held_quantity −= n`) dan NIK pemegang yang dilepas SHALL langsung bisa dipakai lagi (SRS-F-11-05).
12. NIK SHALL tidak pernah disimpan atau dilog plaintext: lookup `*_nik_hash` (HMAC-SHA256, kunci bersama Queue/User), tampilan `*_nik_masked`, NIK pemegang `attendee_nik_enc` (AES-256-GCM) (SRS-F-11-10).

**Error codes:** `NIK_INVALID` (422), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409), `BUYER_QUOTA_EXHAUSTED` (409), `SESSION_EXPIRED` (410), `RATE_LIMITED`/`ATTENDEE_CHECK_LIMIT_REACHED` (429, OQ-1).

---

## Requirement 6 — Hold Atomik (Invarian #1 "Tidak Oversell")

**User story:** Sebagai pembeli, saya ingin menahan (hold) tiket secara atomik, agar stok tidak
oversell meski banyak pembeli bersamaan.

**Tertelusur ke:** SRS-F-07-04, SRS-F-12-01..07, SRS-Q-FUN-01 · TC-BKG-04..07, TC-PERF-04, TC-EDT-01, TC-PAY-02

### Kriteria Penerimaan (EARS)

1. WHEN klien `POST /api/v1/bookings` dengan header `Idempotency-Key` (16..64 char) dan `HoldRequest{ sessionId, items[1..3]{ categoryId, attendee{ nik, fullName } } }` untuk admission `ACTIVE` dan `now ≤ expires_at + cfg_submit_grace_seconds`, sistem SHALL dalam **satu transaksi** dengan urutan lock tetap (**idempotency → admissions → event_supply → buyer_quotas → category_inventories ASC**): menyisipkan `bookings(PENDING_PAYMENT, payment_expires_at = now + cfg_payment_ttl_seconds, version=1)` dan `booking_items(HELD)`, menaikkan `category_inventories.held_quantity += n_c`, memindahkan `event_supply.allocated_units −= n` / `held_units += n`, menaikkan `buyer_quotas.held_quantity += n`, dan mengubah admission ke `EDITABLE` (TC-BKG-04, SDD C.3).
2. Semua item lintas kategori SHALL berhasil bersama atau tidak sama sekali (SRS-F-12-01); order boleh campur kategori hingga N bila `cfg_allow_mixed_categories` (SRS-F-12-02).
3. IF `n > admissions.units`, THEN sistem SHALL menolak `422 QUANTITY_EXCEEDS_LIMIT` (hold hanya boleh mengambil unit reservasi sesi itu).
4. IF `cfg_allow_mixed_categories = false` dan item memuat > 1 kategori, THEN `422 MIXED_CATEGORIES_NOT_ALLOWED`.
5. IF `held_quantity + sold_quantity + n_c > capacity` pada kategori mana pun, THEN transaksi SHALL rollback dan sistem mengembalikan `409 CATEGORY_UNAVAILABLE` beserta ketersediaan terbaru; sesi dan alokasi tetap aktif (SRS-F-12-03, TC-BKG-05). **TC-BKG-07: 100 hold paralel pada 50 tiket terakhir → tepat 50 berhasil.**
6. Hold SHALL idempoten per `(scope='create_booking', owner_id=visitor, idempotency_key)`; request ulang identik SHALL mengembalikan `response_status`/`response_body` tersimpan; key sama dengan `request_hash` berbeda → `409 IDEMPOTENCY_KEY_REUSED` (TC-BKG-06, SRS-F-12-04).
7. Satu sesi SHALL membuat paling banyak satu booking (`bookings.admission_id UNIQUE`); hold kedua untuk sesi yang sama SHALL mengembalikan booking yang ada (SRS-F-12-05).
8. WHERE `cfg_edit_enabled = false`, atau hold diterima di masa grace, sistem SHALL langsung mengunci pesanan (`CONVERTED`, `lock_reason = EDIT_DISABLED` / `SELECTION_EXPIRED`) dan melepas sisa unit `units − n` ke free stock di transaksi yang sama (SRS-F-07-05, SRS-F-09-04, SDD C.3 langkah 11).
9. WHEN hold berhasil, sistem SHALL meng-append `BookingHeld` (version 1, payload tanpa NIK plaintext) dan memproyeksikan read model dalam transaksi yang sama (R11).
10. Transaksi hold SHALL memakai `lock_timeout = 2s` dan `statement_timeout = 5s`; IF batas terlampaui atau DB/broker sementara gagal, THEN `503 DEPENDENCY_UNAVAILABLE` (klien mengulang dengan key sama) tanpa hold setengah jadi (SRS-Q-REL-02, TC-CAP-01).

**Error codes:** `VALIDATION_ERROR` (400), `CATEGORY_UNAVAILABLE` (409), `BUYER_QUOTA_EXHAUSTED` (409), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409), `IDEMPOTENCY_KEY_REUSED` (409), `SESSION_EXPIRED` (410), `QUANTITY_EXCEEDS_LIMIT` (422), `MIXED_CATEGORIES_NOT_ALLOWED` (422), `NIK_INVALID` (422), `DEPENDENCY_UNAVAILABLE` (503).

---

## Requirement 7 — Sesi & Timeout (Invarian #4 "Timer Server-Authoritative")

**User story:** Sebagai sistem, saya ingin TTL sesi dan pembayaran dihitung server-authoritative, agar
klien tidak dapat memperpanjang sendiri dan slot kembali bebas saat kedaluwarsa.

**Tertelusur ke:** SRS-F-07-01..06, SRS-F-08-01..03, SRS-F-23-10 · TC-SES-01..08, TC-EDT-05, TC-PAY-02

### Kriteria Penerimaan (EARS)

1. Sistem SHALL menghitung `admissions.expires_at` dan `bookings.payment_expires_at` dari waktu server + snapshot config; respons SHALL selalu memuat `expiresAt`/`paymentExpiresAt` dan `serverTime`; refresh tidak memperpanjang TTL (invarian #4, SRS-F-07-02).
2. WHEN klien `GET /api/v1/booking-sessions/{sessionId}`, sistem SHALL mengembalikan `BookingSession` terkini (`phase`, `expiresAt`, `serverTime`, ketersediaan kategori) untuk pemilik; sesi berakhir → `410 SESSION_EXPIRED`.
3. Worker **session sweeper** (interval `WORKER_INTERVAL`, `FOR UPDATE SKIP LOCKED LIMIT 200`, urutan lock R6.1) SHALL menerapkan (SDD C.10):
   - `RESERVED` dan `now > expires_at` → `EXPIRED`, `end_reason = UNCLAIMED`, `allocated_units −= units`, outbox `booking.journey.ended.v1 (UNCLAIMED)`;
   - `ACTIVE` dan `now > expires_at + cfg_submit_grace_seconds` → `EXPIRED`, `end_reason = SESSION_EXPIRED`, `allocated_units −= units`, outbox `booking.journey.ended.v1` (SRS-F-08-01, TC-SES-04). *(SDD C.5 menulis `outcome = SESSION_EXPIRED`, tetapi enum schema v1 hanya `PAID|EXPIRED|CANCELLED|LEFT_WITHOUT_BUYING|UNCLAIMED` dan `additionalProperties:false` — OQ-15; sampai diputuskan, kirim `EXPIRED`.)*
   - `EDITABLE` dan `now > expires_at + cfg_submit_grace_seconds` → `CONVERTED`, `lock_reason = SELECTION_EXPIRED`, sisa unit `units − item_quantity` dilepas; booking **tetap `PENDING_PAYMENT`** sampai TTL Pembayaran (SRS-F-08-02, TC-EDT-05).
4. IF request (selain baca) tiba setelah `expires_at + cfg_submit_grace_seconds`, THEN sistem SHALL mengembalikan `410 SESSION_EXPIRED`; request dalam masa grace diproses lalu pesanan langsung dikunci (SRS-F-07-05, TC-SES-08).
5. `bookings.payment_expires_at` SHALL dihitung sejak hold pertama dan **tidak di-reset** oleh ubah pesanan atau ganti metode (SRS-F-07-04, SRS-F-14-07, TC-PAY-02).
6. Setelah sesi berakhir tanpa membeli, pembeli SHALL dapat antre ulang dari belakang bila kuota masih ada (ditegakkan Queue; Booking hanya mengakhiri admission) (SRS-F-08-03).

**Error codes:** `SESSION_EXPIRED` (410).

---

## Requirement 8 — Ubah Pesanan (Edit) & Pemulihan Sesi

**User story:** Sebagai pembeli, saya ingin mengubah pesanan sebelum menekan "Bayar" dan memulihkan
sesi setelah refresh/disconnect, agar saya tidak kehilangan slot karena gangguan jaringan.

**Tertelusur ke:** SRS-F-12-06, SRS-F-23-01..11, SRS-F-24-01, SRS-F-24-12 · TC-EDT-01..06, TC-NAV-01..03, TC-SES-05..07

### Kriteria Penerimaan (EARS)

1. WHILE admission `EDITABLE`, `now ≤ expires_at + cfg_submit_grace_seconds`, `edit_count < cfg_max_edits`, booking `PENDING_PAYMENT` dan `payment_started_at IS NULL`, WHEN klien `PUT /api/v1/bookings/{bookingId}/items` dengan `Idempotency-Key` dan `ReplaceItemsRequest{ version, items[1..3] }` (**Δ2**), sistem SHALL dalam satu transaksi (urutan lock R6.1, `SAVEPOINT replace_items`) menandai item lama `REPLACED`, menyisipkan item baru `HELD`, menyesuaikan `held_quantity` per kategori, `event_supply` (`allocated += n_lama − n_baru`, `held += n_baru − n_lama`), `buyer_quotas.held += n_baru − n_lama`, menghitung ulang `amount_minor` dari harga kategori, menaikkan `bookings.version` dan `admissions.edit_count` (TC-EDT-01, SDD C.8).
2. Item yang dapat diubah: jumlah (1..N), kategori, NIK dan nama pemegang; `items[].attendee` MAY berupa `{ nik, fullName }` baru ATAU `{ keepFromItemId }` untuk mempertahankan pemegang lama tanpa mengirim ulang NIK (SRS-F-23-02, SRS-F-23-09).
3. Sisa unit reservasi SHALL tetap dipegang selama pesanan dapat diubah, sehingga menaikkan kembali hingga N selalu berhasil dari kategori yang tersedia (SRS-F-23-03).
4. IF edit gagal (kategori habis, konflik NIK), THEN sistem SHALL `ROLLBACK TO SAVEPOINT` sehingga pesanan lama utuh, kategori milik pembeli tidak hilang, dan percobaan gagal **tidak** dihitung ke `edit_count` (SRS-F-23-04, SRS-F-23-07, TC-EDT-02).
5. IF `version` ≠ versi terkini booking, THEN `409 BOOKING_VERSION_CONFLICT` (TC-EDT-06).
6. IF pesanan sudah terkunci (admission `CONVERTED` atau `payment_started_at` terisi), THEN `409 BOOKING_LOCKED` apa pun state UI (SRS-F-24-12, TC-EDT-04).
7. IF `edit_count ≥ cfg_max_edits` (default 3), THEN `409 EDIT_LIMIT_REACHED` (TC-EDT-03).
8. Ubah pesanan SHALL meng-append `BookingItemsReplaced` dan **tidak** me-reset `payment_expires_at`; NIK yang dilepas langsung bebas (SRS-F-23-05, SRS-F-23-09).
9. WHEN klien `GET /api/v1/booking-sessions/current?eventId=` dengan visitor token, sistem SHALL mengembalikan sesi/booking aktif milik visitor dengan `phase ∈ {SELECTING, EDITABLE, LOCKED, ENDED}` (+`reason`) dan pesanan dari server dengan NIK tersamar (TC-NAV-01, TC-SES-05, SRS-F-24-01); IF tidak ada → `404 NOT_FOUND`; rate limit → `429 RATE_LIMITED` + `Retry-After`.
10. Respons `Booking` SHALL memuat `canEdit`, `editableUntil`, `editsRemaining`, `lockReason` agar klien dapat merender mode ubah tanpa menebak (SRS IF Pembagian Tanggung Jawab).

**Error codes:** `BOOKING_LOCKED` (409), `BOOKING_VERSION_CONFLICT` (409), `EDIT_LIMIT_REACHED` (409), `CATEGORY_UNAVAILABLE` (409), `ATTENDEE_NIK_ALREADY_REGISTERED` (409), `IDEMPOTENCY_KEY_REUSED` (409), `QUANTITY_EXCEEDS_LIMIT` (422), `NOT_FOUND` (404), `RATE_LIMITED` (429).

---

## Requirement 9 — Settlement, Expiry & Rekonsiliasi Pembayaran (Invarian #5)

**User story:** Sebagai sistem, saya ingin menerapkan pembayaran sukses secara idempoten dan
menangani booking lewat batas waktu dengan rekonsiliasi lebih dulu, agar pembayaran sah tidak
dibatalkan dan stok tidak dilepas sebelum status pembayaran pasti.

**Tertelusur ke:** SRS-F-16-01..03, SRS-F-17-01..05, SRS-Q-REL-05 · TC-PAY-03, TC-PAY-04, TC-PAY-06..08 · SDD-RBT §4.7.1 C.4

### Kriteria Penerimaan (EARS)

1. WHEN `payment.succeeded.v1` (`{ bookingId, attemptId, settlementTime, amountMinor, method }`) diterima dan booking `PENDING_PAYMENT`/`VERIFYING` dengan `settlementTime ≤ payment_expires_at`, sistem SHALL dalam **satu transaksi** (urutan lock R6.1): `held → sold` (`category_inventories`, `event_supply`, `buyer_quotas.held → paid`), item `HELD → PAID`, menerbitkan `tickets` (satu per item), append `BookingPaid`, status `PAID` + `paid_at`/`settlement_time`, outbox `booking.paid.v1` + `booking.journey.ended.v1 (PAID)` (SRS-F-16-02, TC-PAY-03).
2. Ketepatan waktu SHALL dinilai dari `settlementTime` gateway, bukan waktu notifikasi tiba: webhook telat untuk pembayaran tepat waktu tetap `PAID` (SRS-F-16-03, TC-PAY-06).
3. IF `payment.succeeded` tiba dengan `settlementTime > payment_expires_at` saat booking `PENDING_PAYMENT`/`VERIFYING`, THEN sistem SHALL dalam satu transaksi melepas hold dan kuota, status `REVIEW` tanpa tiket, append `BookingReview`, outbox `booking.journey.ended.v1 (EXPIRED)` + `booking.review.v1`.
4. IF `payment.succeeded` tiba untuk booking `EXPIRED`/`CANCELLED`, THEN status SHALL menjadi `REVIEW`, tidak ada tiket, tidak ada stok orang lain yang diambil, outbox `booking.review.v1` (Payment membuka refund case; Notification mengirim email) (SRS-F-17-05, TC-PAY-07).
5. Consumer SHALL idempoten via **guard state** (booking dikunci; `PAID`/`REVIEW` → no-op ack; `tickets ... ON CONFLICT (booking_item_id) DO NOTHING`) **tanpa tabel inbox**; kirim ganda SHALL tidak menggandakan tiket (invarian #5; test kirim ganda wajib).
6. WHEN `payment.failed.v1` diterima, sistem SHALL tidak mengubah state; booking menunggu sampai TTL (pembeli dapat ganti metode).
7. WHILE booking `PENDING_PAYMENT` dan `now ≥ payment_expires_at`, worker **expiry sweeper** SHALL mengubahnya ke `VERIFYING` (`verification_until = now + verify grace`, stok tetap ditahan) lalu memanggil resolve `mode = CHECK_AND_CANCEL` di luar transaksi (SRS-F-17-01, TC-PAY-04).
8. WHEN resolve mengembalikan `SUCCEEDED` dengan `settlementTime ≤ payment_expires_at`, sistem SHALL menerapkan settlement seperti AC 1; `SUCCEEDED` terlambat → AC 3.
9. WHEN resolve mengembalikan `NOT_PAID` atau `NO_ATTEMPT`, sistem SHALL meng-expire booking (`EXPIRED`), `held → free`, kembalikan kuota, item `RELEASED`, append `BookingExpired`, admission (bila masih `EDITABLE`) dikunci `BOOKING_ENDED`, outbox `booking.expired.v1 (PAYMENT_TIMEOUT)` + `booking.journey.ended.v1 (EXPIRED)` (SRS-F-17-02).
10. WHEN resolve mengembalikan `UNKNOWN` atau Payment tidak terjangkau (breaker OPEN), sistem SHALL membiarkan booking `VERIFYING` dan menjadwal ulang resolve sampai `verification_until`; setelah lewat, booking SHALL `EXPIRED` seperti AC 9 (SRS-F-17-03, SRS-Q-REL-05, TC-PAY-08).
11. Klien SHALL melihat status `VERIFYING` di `GET /api/v1/bookings/{id}` agar dapat menampilkan "Kami sedang memastikan pembayaran Anda" (SRS-F-17-04).
12. Status booking SHALL monoton: tidak ada transisi keluar dari `PAID`, `EXPIRED` hanya ke `REVIEW`, `CANCELLED` hanya ke `REVIEW` (invarian backend #8).

**Error codes:** (consumer; tidak ada error HTTP publik) — payload rusak → `rabbitmq.Permanent(err)` → DLQ.
**Catatan:** verify grace = env `VERIFY_GRACE_SECONDS` (default 180, rentang 60–600; SDD-RBT §4.10), belum ada di `internal/config` [kode] → design §12.

---

## Requirement 10 — Tiket & Riwayat Booking

**User story:** Sebagai pembeli, saya ingin melihat e-tiket saya, dan jika login, melihat riwayat
booking saya, agar saya punya bukti dan jejak pembelian.

**Tertelusur ke:** SRS-F-20-01..03, SRS-F-27-11 · TC-TKT-01..03, TC-USR-10

### Kriteria Penerimaan (EARS)

1. WHEN settlement sukses, sistem SHALL menerbitkan satu baris `tickets` per `booking_item` (`public_code UNIQUE`, `booking_item_id UNIQUE`); tiket hanya untuk booking `PAID` (TC-TKT-01, SRS-F-20-01).
2. WHEN klien `GET /api/v1/bookings/{bookingId}/tickets` (**Δ3**) sebagai pemilik (visitor), pemegang ticket link token (`t`), atau user login pemilik, dan booking `PAID`, sistem SHALL mengembalikan `TicketList{ bookingId, tickets[]{ ticketCode, categoryCode, attendeeName, attendeeNikMasked, qrPayload } }` (SRS-F-20-03).
3. IF booking belum `PAID`, THEN sistem SHALL mengembalikan `409 BOOKING_NOT_PAYABLE`.
4. WHEN `GET /api/v1/bookings/{bookingId}` oleh pemilik atau ticket link token, sistem SHALL mengembalikan `Booking` (status, `paymentExpiresAt`, `serverTime`, `version`, `canEdit`, `editableUntil`, `lockReason`, item NIK tersamar); bukan pemilik → `404 NOT_FOUND`.
5. Ticket link token SHALL JWT EdDSA terbitan Booking berisi `bookingId`, berlaku hingga tanggal event + 7 hari (HLD tabel Token), dan disertakan sebagai `ticketLinkToken` di `booking.paid.v1`.
6. WHERE pembeli login, WHEN `GET /api/v1/me/bookings` dengan `userBearer`, sistem SHALL mengembalikan **hanya** booking dengan `account_id = token.sub` (TC-USR-10; tanpa kebocoran lintas akun), paginasi cursor (`limit` 1..50, default 20).
7. IF access token tidak valid, THEN sistem SHALL mengembalikan `401 UNAUTHORIZED`.

**Error codes:** `BOOKING_NOT_PAYABLE` (409), `NOT_FOUND` (404), `UNAUTHORIZED` (401).

---

## Requirement 11 — Event Sourcing & Integritas Stok

**User story:** Sebagai sistem, saya ingin siklus booking direkam append-only dan stok berubah dalam
transaksi yang sama, agar ada jejak audit yang dapat di-replay tanpa risiko oversell.

**Tertelusur ke:** SRS-C-09, SRS-D-10, SRS-Q-FUN-01, SRS-Q-REL-09, SRS-F-90-01 · TC-ES-01..05, TC-BKG-07, TC-BKG-12, TC-OPS-01

### Kriteria Penerimaan (EARS)

1. WHEN state booking berubah, sistem SHALL meng-append event ke `booking_events` (`PRIMARY KEY (stream_id, version)`), memproyeksikan read model, mengubah stok, dan menulis outbox — semuanya dalam **satu** transaksi (ADR-019).
2. IF append menemui konflik versi (SQLSTATE `23505` → `pkg/eventstore.ErrConcurrency`), THEN sistem SHALL membaca ulang state lalu mengulangi (maks 3), **bukan** menimpa; dua penulis bersamaan tidak pernah sama-sama berhasil (SRS-Q-REL-09, TC-ES-02).
3. Payload `booking_events` SHALL **tidak** memuat NIK plaintext, email, atau token — hanya hash/masked (ditolak `pkg/eventstore`).
4. DB SHALL menolak `UPDATE`/`DELETE`/`TRUNCATE` pada `booking_events` (trigger append-only, ERRCODE `restrict_violation`) (TC-ES-01).
5. Stok SHALL tidak pernah negatif: CHECK `held_quantity + sold_quantity ≤ capacity` (kategori) dan `allocated_units + held_units + sold_units ≤ total_capacity` (supply) (TC-BKG-07).
6. `event_supply.total_capacity` SHALL selalu = Σ `category_inventories.capacity` (constraint trigger deferred `check_supply_matches_categories`, `SUPPLY_CATEGORY_MISMATCH` saat COMMIT) (TC-BKG-12).
7. WHEN job rekonsiliasi dijalankan, sistem SHALL memeriksa: persamaan supply (termasuk sisa unit admission `EDITABLE`), kuota vs item, tiket vs item `PAID`, outbox pending, dan read model vs event store; temuan SHALL memicu alert (SRS-F-90-01, TC-OPS-01). Pemeriksaan lintas service (PAID tanpa attempt SUCCEEDED, slot aktif Queue) dijalankan oleh `tools/reconcile` (platform R9).

**Error codes:** `ErrConcurrency` (internal, retry), `SUPPLY_CATEGORY_MISMATCH` (internal, seed).

---

## Requirement 12 — Event Antar-Service (Transactional Outbox)

**User story:** Sebagai sistem, saya ingin menerbitkan event domain secara andal via outbox, agar Queue
dan Notification tetap konsisten meski broker sempat gagal.

**Tertelusur ke:** SRS-IF-30, SRS-Q-REL-01, SRS-Q-REL-04, SRS-F-05-06, SRS-F-06-08 · TC-API-02, TC-CAP-03, TC-MSG-01

### Kriteria Penerimaan (EARS)

1. WHEN perubahan stok/uang di-commit, sistem SHALL menulis baris `outbox_events` (`routing_key`, `aggregate_id`, `schema_version`, `payload`, `available_at`) dalam transaksi yang sama (outbox **hanya** untuk perubahan stok atau uang).
2. Worker **outbox relay** SHALL menerbitkan baris pending ke exchange `rusuh.events` (topic) dalam envelope standar dengan publisher confirm (`pkg/outbox`, platform R3).
3. Sistem SHALL menerbitkan (routing key → consumer): `booking.journey.ended.v1` → Queue (`outcome ∈ {PAID, EXPIRED, CANCELLED, LEFT_WITHOUT_BUYING, UNCLAIMED}` sesuai schema; lihat OQ-15); `booking.quota.exhausted.v1` → Queue; `booking.sold_out.v1` → Queue; `booking.paid.v1` → Notification; `booking.expired.v1` → Notification; `booking.review.v1` → Notification + Payment.
4. `booking.journey.ended.v1` SHALL diterbitkan tepat sekali per admission saat journey berakhir (PAID, EXPIRED, CANCELLED, sesi habis, keluar tanpa membeli, tidak diklaim) agar Queue membebaskan slot (SRS-F-06-08).
5. `booking.quota.exhausted.v1` SHALL diterbitkan ketika kuota NIK pembeli habis. *(Titik pemicu — saat `held+paid = max` atau hanya saat `paid = max` — belum didefinisikan di dokumen mana pun: OQ-13.)*
6. IF penerbitan gagal, THEN relay SHALL menaikkan `publish_attempts`, mencatat `last_error`, dan mencoba lagi (tanpa kehilangan event).
7. Payload setiap event SHALL valid terhadap `docs/contracts/events/*.schema.json` dan `envelope.schema.json`, **tanpa** PII plaintext selain `buyerEmail` dan `ticketLinkToken` yang memang ada di schema `booking.paid/expired/review`.

**Error codes:** (relay internal; tanpa error HTTP).

---

## Requirement 13 — Kesehatan, Konfigurasi & Observability

**User story:** Sebagai operator, saya ingin endpoint kesehatan, konfigurasi tervalidasi saat start,
dan metrik, agar ALB dan tim ops dapat memantau service.

**Tertelusur ke:** SRS-Q-MNT-02, SRS-Q-MNT-03, SRS-Q-SEC-03 · TC-OBS-01, TC-OBS-02, TC-SEC-03

### Kriteria Penerimaan (EARS)

1. WHEN `GET /health` dipanggil, sistem SHALL mengembalikan `200 UP` bila semua dependency sehat atau `503 DOWN` bila ada yang gagal (dalam `HEALTH_TIMEOUT`), dengan `Health{ status, service, checkedAt, dependencies[] }`. ✅ *Terimplementasi.*
2. WHEN service start, sistem SHALL memvalidasi seluruh konfigurasi (`Config.Validate()`) dan gagal cepat bila tidak valid. ✅ *Terimplementasi (subset; variabel target di design §12).*
3. Sistem SHALL mengekspos metrik: `rbt_booking_sessions_total{event_id,status}`, `rbt_booking_holds_total{event_id,status}`, `rbt_booking_settlements_total{event_id}`, `rbt_booking_expirations_total{event_id,reason}`, `rbt_booking_stock_gauge{event_id,type}`. *(Spec lama memuat `rbt_booking_event_store_version{aggregate_id}` — label `aggregate_id` = booking ID melanggar SRS-Q-MNT-03; dihapus.)*
4. IF counter stok < 0, atau expiry_rate > 20% selama 10m, atau outbox lag > 30s selama 5m, atau p99 DB > 100ms selama 5m, THEN sistem SHALL memicu alert sesuai severity (SDD-BOOKING §11.2).
5. Log SHALL terstruktur (slog JSON) dan **tidak pernah** memuat NIK/email/token.

**Error codes:** (ops; tanpa error domain).

---

## Requirement 14 — Data Tagihan, Kunci Pesanan & Perpanjangan Deadline (dipanggil Payment)

**User story:** Sebagai Payment Service, saya ingin mengambil tagihan dan mengunci pesanan sebelum
membuat transaksi di gateway, agar nominal yang ditagih selalu nominal versi terkunci.

**Tertelusur ke:** SRS-F-14-03, SRS-F-14-04, SRS-F-19-01, SRS-F-23-10, SRS-IF-34 · TC-EDT-04, TC-PAY-01, TC-PAY-08 · invarian backend #11

### Kriteria Penerimaan (EARS)

1. WHEN Payment memanggil `GET /internal/v1/bookings/{bookingId}/payable` dengan service token, sistem SHALL mengembalikan `Payable{ bookingId, status, amountMinor, paymentExpiresAt, visitorId?, buyerEmail?, version, canEdit? }` dari read model; nominal selalu dari snapshot harga Booking, tidak pernah dari klien (SRS-F-14-03).
2. WHEN Payment memanggil `POST /internal/v1/bookings/{bookingId}/payment-lock` dengan `attemptKey`, dan booking `PENDING_PAYMENT` serta `now < payment_expires_at`, sistem SHALL dalam satu transaksi (urutan lock admission → booking): mengisi `payment_started_at = COALESCE(payment_started_at, now)`; bila admission `EDITABLE`, mengubahnya ke `CONVERTED` (`lock_reason = PAYMENT_STARTED`, `end_reason = CONVERTED`) dan melepas sisa unit `units − item_quantity` ke free stock; lalu membalas `outcome = OK` + `version`, `amountMinor`, `paymentExpiresAt` (SDD C.9, TC-EDT-04). Status booking **tetap `PENDING_PAYMENT`** (K1).
3. Payment-lock SHALL idempoten per `attemptKey`: panggilan ulang mengembalikan versi dan nominal yang sama; lock yang kalah balapan dengan edit mengunci versi terbaru, edit yang kalah balapan dengan lock mendapat `409 BOOKING_LOCKED`.
4. IF booking bukan `PENDING_PAYMENT`, THEN `outcome = BOOKING_NOT_PAYABLE`; IF `now ≥ payment_expires_at`, THEN `outcome = PAYMENT_EXPIRED`.
5. WHEN Payment memanggil `POST /internal/v1/bookings/{bookingId}/deadline-extension` dengan `{ reason: PROVIDER_UNAVAILABLE, seconds: 1..600 }` dan booking belum pernah menerima instruksi pembayaran serta `deadline_extended_at IS NULL`, sistem SHALL memperpanjang `payment_expires_at` sekali sebesar `min(seconds, OUTAGE_EXTENSION_SECONDS)` (default 300) dan mengisi `deadline_extended_at` (SRS-F-19-01); panggilan berikutnya → `extended = false`.
6. Endpoint internal SHALL menolak request tanpa service token valid (`401`) dan tidak dapat diakses lewat ALB publik (SRS-IF-34, TC-SEC-02).

**Error codes:** `UNAUTHORIZED` (401), `NOT_FOUND` (404); hasil bisnis dikembalikan sebagai `outcome`.

---

## Requirement 15 — Keluar Tanpa Membeli & Batal Pesanan (SHOULD)

**User story:** Sebagai pembeli, saya ingin keluar dari halaman pilih atau membatalkan pesanan yang
belum dibayar, agar slot segera diberikan ke pengantre berikutnya.

**Tertelusur ke:** SRS-F-13-01, SRS-F-13-02, SRS-F-24-13 · TC-PAY-09 · prioritas **SHOULD** (SRS §2.2, Sprint 2 bila waktu cukup)

### Kriteria Penerimaan (EARS)

1. WHEN pemilik `POST /api/v1/booking-sessions/{sessionId}/cancel` untuk admission `ACTIVE` (belum hold), sistem SHALL mengakhiri admission (`EXPIRED`, `end_reason = LEFT_WITHOUT_BUYING`), mengembalikan `allocated_units`, menulis outbox `booking.journey.ended.v1 (LEFT_WITHOUT_BUYING)`, dan membalas `204` (SRS-F-13-01).
2. WHEN pemilik `POST /api/v1/bookings/{bookingId}/cancel` untuk booking `PENDING_PAYMENT` yang masih dalam TTL Pembayaran, sistem SHALL memanggil resolve `CHECK_AND_CANCEL` lebih dulu (di luar transaksi); bila `SUCCEEDED` tepat waktu → terapkan settlement (`PAID`) dan kembalikan booking `PAID`; bila `NOT_PAID`/`NO_ATTEMPT` → `CANCELLED`, `held → free`, kuota kembali, append `BookingCancelled`, outbox `booking.expired.v1 (CANCELLED)` + `booking.journey.ended.v1 (CANCELLED)` (SRS-F-13-02, TC-PAY-09).
3. IF resolve `UNKNOWN`/Payment tidak terjangkau, THEN sistem SHALL menolak pembatalan dengan `503 DEPENDENCY_UNAVAILABLE` tanpa mengubah state (stok tidak dilepas sebelum status pembayaran pasti — invarian backend #7).
4. IF waktu pembayaran sudah habis atau booking `VERIFYING`/`PAID`, THEN pembatalan SHALL ditolak (`409 BOOKING_LOCKED`) (SDD C.4).
5. WHERE env `CANCEL_ENABLED = false` (default `true`, SDD-RBT §4.10), endpoint batal pesanan SHALL menolak request dan respons `Booking`/`BookingSession` SHALL tidak menawarkan pembatalan (fitur SHOULD). *(Respons saat dimatikan tidak didefinisikan kontrak; spec memakai `404 NOT_FOUND` [asumsi].)*

**Error codes:** `BOOKING_LOCKED` (409), `NOT_FOUND` (404), `DEPENDENCY_UNAVAILABLE` (503).

---

## Requirement 16 — Ketersediaan Near-Real-Time (SSE)

**User story:** Sebagai pembeli di halaman pilih, saya ingin ketersediaan kategori dan sisa waktu
diperbarui otomatis, agar saya tahu kategori mana yang masih bisa dipilih.

**Tertelusur ke:** SRS-F-03-02, SRS-F-03-04, SRS-F-25-01, SRS-F-25-03..07 · TC-SSE-01..03, TC-EVT-05 · ADR-003

### Kriteria Penerimaan (EARS)

1. WHEN pemilik sesi `GET /api/v1/booking-sessions/{sessionId}/stream` dengan `Accept: text/event-stream`, sistem SHALL mengirim event `availability` (`categories[]{categoryId, available}`, `serverTime`) hanya saat berubah dan paling sering sekali per interval SSE (default 1000 ms), event sisa waktu (`expiresAt`, `serverTime`, `phase`), dan heartbeat tiap `SSE_HEARTBEAT_SECONDS` (TC-SSE-01). *(Nama event sisa waktu `timer` (kontrak) vs `session` (SRS): OQ-2.)*
2. Ketersediaan SSE SHALL diambil dari snapshot Valkey `booking:avail:{eventId}` yang direfresh berkala dari PostgreSQL; angka SSE bersifat informatif dan hold selalu divalidasi server (SRS-F-25-05, TC-SSE-03).
3. IF koneksi SSE per instance mencapai `SSE_MAX_CONNECTIONS_PER_INSTANCE`, THEN sistem SHALL menolak dengan `503 DEPENDENCY_UNAVAILABLE` sehingga klien beralih ke polling `GET /booking-sessions/{id}` (SRS-F-25-04/06, TC-SSE-02).
4. Satu sesi SHALL memiliki paling banyak satu koneksi stream; koneksi baru SHALL menutup yang lama (SRS-F-25-06).
5. WHEN sesi berakhir atau dibatalkan, stream SHALL ditutup dengan event penutup berisi alasan (SRS-F-25-07). *(Event `closed` belum ada di kontrak — OQ-2.)*
6. Endpoint SSE SHALL tidak di-cache CDN.

**Error codes:** `SESSION_EXPIRED` (410), `DEPENDENCY_UNAVAILABLE` (503).

---

## Requirement 17 — Supply Snapshot & Pelepasan Alokasi Yatim (dipanggil Queue)

**User story:** Sebagai Queue Service, saya ingin membaca ringkasan stok dan melepas alokasi yang
entry-nya sudah hilang, agar Status Document akurat dan tidak ada alokasi menggantung.

**Tertelusur ke:** SRS-F-05-09, SRS-F-05-11, SRS-F-10-03 · ADR-018 (saga + kompensasi) · TC-QUE-10..12

### Kriteria Penerimaan (EARS)

1. WHEN Queue memanggil `GET /internal/v1/events/{eventId}/supply`, sistem SHALL mengembalikan `Supply{ free, allocated, held, sold, total, maxPerOrder, saleState ∈ {NOT_STARTED, OPEN, CLOSED}, soldOut, version, categories[]{code, name, capacity, available} }` dari PostgreSQL.
2. WHEN Queue memanggil `POST /internal/v1/allocations/{allocationId}:release` dengan `{ reason: QUEUE_ENTRY_GONE }` untuk admission `RESERVED`, sistem SHALL mengakhiri admission (`RELEASED`), mengembalikan `allocated_units`, dan membalas `{ released: true }`; untuk admission yang sudah tidak `RESERVED` → `{ released: false }` tanpa perubahan (idempoten, kompensasi saga).
3. Kedua endpoint SHALL mensyaratkan service token (`401` tanpa token).

**Error codes:** `UNAUTHORIZED` (401), `NOT_FOUND` (404).

---

## Requirement 18 — Administrasi Event (internal/seed)

**User story:** Sebagai tim penyelenggara (tanpa back-office), saya ingin membuat dan mengubah event
lewat endpoint internal atau seed, agar event dapat disiapkan untuk penjualan.

**Tertelusur ke:** SRS-F-26-01, SRS-F-26-03, SRS-F-26-04, SRS-F-26-08 · TC-CFG-02..04 · SDD-BOOKING App. C

### Kriteria Penerimaan (EARS)

1. WHEN `POST /internal/v1/admin/events` dengan `adminBearer` dan `AdminEventCreateRequest{ name, venue, city, eventDate, saleStart, saleEnd, imageUrl?, bannerUrl?, categories[]{code,name,price,capacity}, config }`, sistem SHALL membuat `events`, `ticket_categories`, `category_inventories`, dan `event_supply (total_capacity = Σ capacity)` dalam satu transaksi dan membalas `201 { eventId }`.
2. WHEN `PATCH /internal/v1/admin/events/{eventId}`, sistem SHALL mengubah konfigurasi; aturan pembentuk jaminan setelah `rules_locked_at` → `409 EVENT_RULES_LOCKED` (R4.4); setiap perubahan tercatat di `config_audit` dengan `app.actor` = admin (R4.5).
3. WHEN `POST /internal/v1/admin/events/{eventId}/images` (`multipart/form-data`, `image` ≤ 5 MB JPEG/PNG/WebP, `type ∈ {poster, banner, thumbnail}`), sistem SHALL menyimpan gambar dan membalas `201 { imageUrl, type }`; file lain → `400 VALIDATION_ERROR`. *(Penyimpanan cloud = S3 + CloudFront (SDD-BOOKING C.4); penyimpanan lokal/compose belum dirancang — OQ-16.)*
4. Endpoint admin SHALL tidak lewat ALB publik dan mensyaratkan token `role = admin`.
5. Default `EventConfig` SHALL: `maxTicketsPerOrder = 3`, `maxTicketsPerBuyerNik = 3`, `selectionTtlSeconds = 300`, `paymentTtlSeconds = 600`, `claimWindowSeconds = 180`, `allowMixedCategories = true` (PRD §8.2, kontrak).

**Error codes:** `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401), `EVENT_RULES_LOCKED` (409).

---

## Invarian yang Ditegakkan

| # | Invarian | Ditegakkan oleh | Requirement | TC utama |
|---|----------|------------------|-------------|----------|
| #1 | **Tidak oversell** — stok hanya berubah di Booking dalam satu transaksi | CHECK DB kategori & supply; urutan lock tetap | R6, R8, R11 | TC-BKG-07, TC-PERF-04 |
| #2 | **Jangan PHP** — admitted dijamin dapat tiket sampai TTL | reservasi `allocated_units` saat admission; hold ≤ `units`; sisa unit dipegang selama editable | R3, R6.3, R8.3 | TC-ADM-02, TC-ADM-05 |
| #3 | **Kuota 3/NIK pembeli/event** lintas sesi & "1 tiket 1 NIK pemegang" | `buyer_quotas` CHECK; `uq_attendee_nik_per_event`; `uq_booking_item_nik_active` | R5 | TC-BKG-01..03, TC-BKG-11 |
| #4 | **Timer server-authoritative** | `expires_at`/`payment_expires_at` dihitung server; sweeper; TTL Pembayaran tidak di-reset | R7, R8.8 | TC-SES-05..08, TC-PAY-02 |
| #5 | **Settlement idempoten; status akhir hanya dari webhook/rekonsiliasi** | guard state + `settlement_time ≤ payment_expires_at`; resolve sebelum release; append-only | R9 | TC-PAY-03..08 |
| #6 | Kegagalan notifikasi tidak membatalkan transaksi | outbox + retry + DLQ (Notification async) | R12 | TC-MSG-01, TC-CAP-03 |
| B-11 | Payment tidak membuat transaksi gateway sebelum `payment-lock`; nominal = versi terkunci | R14.2–14.3 | R14 | TC-EDT-04 |

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
make build && make test && make test-integration && make cover && make lint && make isolation && make ci
```
Coverage dihitung atas kode logika; `cmd/`, `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan.

---

## Matriks Keterlacakan Ringkas

| Req | SRS | Test Cases | Invarian |
|-----|-----|-----------|----------|
| 1 Katalog | F-02, F-28 | TC-EVT-01..03, TC-CAT-01..06 | — |
| 2 Detail | F-03 | TC-EVT-04..05 | — |
| 3 Admission | F-06-02, F-07-01, F-09, F-10-01 | TC-ADM-01..05, TC-SES-01, TC-PERF-05 | #2 |
| 4 Config snapshot | F-26 | TC-CFG-01..04 | #4 |
| 5 NIK & Kuota | F-11 | TC-BKG-01..03, 08..12 | #3 |
| 6 Hold atomik | F-07-04, F-12, Q-FUN-01 | TC-BKG-04..07, TC-PERF-04 | #1 |
| 7 Sesi & timeout | F-07, F-08 | TC-SES-01..08, TC-EDT-05, TC-PAY-02 | #4 |
| 8 Edit & recovery | F-23, F-24-01/12 | TC-EDT-01..06, TC-NAV-01..03 | #1, #4 |
| 9 Settlement & expiry | F-16, F-17, Q-REL-05 | TC-PAY-03..08 | #5 |
| 10 Tiket & history | F-20, F-27-11 | TC-TKT-01..03, TC-USR-10 | — |
| 11 Event sourcing & stok | C-09, D-10, Q-FUN-01, Q-REL-09, F-90-01 | TC-ES-01..05, TC-BKG-07/12, TC-OPS-01 | #1, #5 |
| 12 Outbox/event | IF-30, Q-REL-01/04 | TC-API-02, TC-CAP-03, TC-MSG-01 | #6 |
| 13 Health/obs | Q-MNT-02/03 | TC-OBS-01..02 | — |
| 14 Payable/lock/extension | F-14-03/04, F-19-01 | TC-EDT-04, TC-PAY-01, TC-PAY-08 | B-11 |
| 15 Cancel (SHOULD) | F-13 | TC-PAY-09 | #5 |
| 16 SSE | F-03-02, F-25 | TC-SSE-01..03 | — |
| 17 Supply & release | F-05-09/11 | TC-QUE-10..12 | #2 |
| 18 Admin event | F-26-08 | TC-CFG-02..04 | — |

---

## Lampiran A — Validasi & Aturan Bisnis

### A.1 Validasi NIK (16 digit)
- Struktur divalidasi `pkg/nik` (platform R1, SDD-RBT §4.9): 16 digit numerik; kode provinsi dari daftar Kemendagri (11–19, 21, 31–36, 51–53, 61–65, 71–76, 81–82, 91–97); kab/kota dan kecamatan ≠ `00`; tanggal lahir kalender valid (DD+40 perempuan) dan tidak di masa depan; nomor urut ≠ `0000`. *(Spec lama menyebut "provinsi 01..99" — tidak berdasar, diganti.)*
- NIK **tidak pernah** disimpan plaintext — hanya `*_nik_hash` (HMAC) + `*_nik_masked` + `attendee_nik_enc` (AES-256-GCM).

### A.2 Batas field
| Field | Aturan |
|-------|--------|
| `Idempotency-Key` | 16..64 char, wajib pada hold & replace |
| `items` | 1..3 item per request, ≤ `admissions.units` |
| `attendee.fullName` | kontrak 1..100 char; DB `attendee_name` 2..100 → nama 1 karakter ditolak DB *(selaraskan validasi DTO ke 2..100)* |
| `nik` | `^[0-9]{16}$` + `pkg/nik` |
| `limit` (me/bookings) | 1..50, default 20 |

### A.3 Aturan bisnis → error
| Aturan | Error code |
|--------|-----------|
| item > unit reservasi / maxTicketsPerOrder | `QUANTITY_EXCEEDS_LIMIT` (422) |
| mixed categories dilarang | `MIXED_CATEGORIES_NOT_ALLOWED` (422) |
| kuota pembeli habis | `BUYER_QUOTA_EXHAUSTED` (409) |
| NIK pemegang sudah terdaftar | `ATTENDEE_NIK_ALREADY_REGISTERED` (409) |
| NIK duplikat dalam order | `ATTENDEE_NIK_DUPLICATE_IN_ORDER` (409) |
| edit/batal setelah pesanan terkunci | `BOOKING_LOCKED` (409) |
| version tidak cocok | `BOOKING_VERSION_CONFLICT` (409) |
| edit melebihi batas | `EDIT_LIMIT_REACHED` (409) |
| tiket sebelum PAID | `BOOKING_NOT_PAYABLE` (409) |
| sesi TTL + grace habis | `SESSION_EXPIRED` (410) |
| giliran lewat | `TURN_MISSED` (410) |
