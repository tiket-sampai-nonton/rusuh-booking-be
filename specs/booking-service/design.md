# Design — Booking Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Sumber desain** | **SDD-RBT-001 §4.4 (DDL), §4.5.2–4.5.3 (outbox, event sourcing), §4.6.2 (routing), §4.7 (lock order & SQL C.1–C.10), §4.10 (konfigurasi)**; `docs/analisa/04b-SDD-BOOKING-001.md` (ringkasan, App. C admin); ADR-002, ADR-006, ADR-007, ADR-013, ADR-018, ADR-019, ADR-020 |
| **Service** | `services/booking` (port 8082, schema `booking`, role `svc_booking`) |
| **Arsitektur** | Hexagonal (ports & adapters) + event sourcing untuk agregat Booking |
| **Kontrak API** | `docs/contracts/openapi/booking.yaml` (publik), `internal-booking.yaml` (internal), `internal-payment.yaml` (dipanggil) |
| **Kontrak event** | `docs/contracts/events/*.schema.json` + `envelope.schema.json` |
| **Skema otoritatif** | `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` (+ `0003` baru untuk `city`/gambar) |

## 1. Overview & Status

Booking Service adalah **otoritas tunggal stok tiket dan uang**. Desain menggabungkan **hexagonal
architecture** dengan **event sourcing** (ADR-019): siklus booking adalah log event append-only; read
model `bookings`/`booking_items`/`tickets` diproyeksikan **sinkron di transaksi yang sama**; stok tetap
relasional dan diubah dalam transaksi yang sama, sehingga invarian "tidak oversell" ditegakkan oleh
CHECK DB, **bukan** oleh replay.

**Status implementasi (audit 2026-10-21) [kode]:**
- ✅ skema DB 14 tabel + trigger invarian, CLI (`api`/`worker`/`migrate`/`healthcheck`), config
  fail-fast (subset), `GET /health`, composition root (`internal/delivery/{server,worker,serve,clients}.go`).
- ❌ kolom `events.city`, `image_url`, `banner_url`, `thumbnail_url`; seluruh domain bisnis, usecase,
  adapter PostgreSQL/RabbitMQ/HTTP domain, worker nyata (`jobs(*Clients) []Job { return nil }`),
  consumer event, publisher outbox, internal API.

Desain ini adalah **target**. Konflik dengan spec lama yang diselesaikan: K1–K5, K8, K11 (lihat
`specs/README.md`).

## 2. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` dan SDD §4.1. Lapisan ditegakkan `depguard`
(`domain/**` std-lib only; `application/**` boleh `pkg/*` tetapi tidak `infrastructure`/`net/http`).

```
                         ┌───────────────────── inbound adapters ─────────────────────┐
  HTTP (net/http)   ──►  │  infrastructure/http    (handler publik + internal, DTO)     │
  RabbitMQ consumer ──►  │  infrastructure/rabbitmq (payment.succeeded / payment.failed) │
  Worker ticker     ──►  │  infrastructure/worker   (sweeper, expiry, outbox relay, SSE cache, rekonsiliasi) │
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/inbound (I<Usecase>)
                         ┌───────────────────── application/usecase ──────────────────┐
                         │  satu usecase per file; keputusan domain murni + port       │
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/outbound (I<Repo/Gateway/UoW>)
                         ┌───────────────────── outbound adapters ────────────────────┐
  PostgreSQL (pgx)  ◄──  │  infrastructure/postgres  (event store, repos, IUnitOfWork)  │
  RabbitMQ (amqp)   ◄──  │  pkg/outbox relay (publisher)                                │
  Payment (HTTP)    ◄──  │  infrastructure/paymentclient (resolve; pkg/resilience)      │
  Valkey            ◄──  │  infrastructure/valkey    (cache SSE, rate limit)            │
  Token (EdDSA)     ◄──  │  infrastructure/security  (admission verify, ticket link sign)│
                         └─────────────────────────────────────────────────────────────┘
```

**Aturan (`code-principles.md`):** Accept interfaces, return structs; interface berawalan `I`, hanya
di `port/`/`pkg/`; satu usecase per file; handler selalu balas DTO (mapper), tidak pernah entity;
error domain bertipe → `errorCode` di `infrastructure/http` via `pkg/apperror`.

### 2.1 Struktur paket (target)
```
internal/booking/
  port/inbound/     IEventCatalogUsecase, IEventDetailUsecase, ISessionUsecase, IAttendeeCheckUsecase,
                    IHoldUsecase, IEditUsecase, IPaymentLockUsecase, IPayableUsecase, IDeadlineExtensionUsecase,
                    ICancelUsecase, ISettlementUsecase, IExpiryUsecase, ISessionSweepUsecase,
                    ITicketUsecase, IBookingHistoryUsecase, IAllocationUsecase, ISupplyUsecase,
                    IAdminEventUsecase, IAvailabilityStreamUsecase, IReconcileUsecase; errors.go (error domain)
  port/outbound/    IEventStore, IBookingRepo, IStockRepo, IAdmissionRepo, IQuotaRepo, IOutboxWriter,
                    IIdempotencyStore, IPaymentClient, IAvailabilityCache, IRateLimiter, ITokenVerifier,
                    ITicketLinkSigner, IPIIProtector, IImageStore, IUnitOfWork
  domain/entity/    Event, Category, Inventory, Supply, Admission, Booking, BookingItem, Ticket + state rules (decide())
  domain/valueobject NIKHash/Masked, Money (minor IDR), TTL, Version, AdmissionStatus, BookingStatus, LockReason, EndReason
  domain/dto/       request/response publik + internal (cocok kontrak OpenAPI)
  domain/mapper/    entity ↔ DTO
  application/usecase satu file per usecase (decide() murni + orkestrasi UoW)
  infrastructure/http      handler publik & internal + Register(mux) (middleware auth dari pkg/httpx)
  infrastructure/postgres  event store (pkg/eventstore) + repo + IUnitOfWork (pgx tx, lock order §9.0)
  infrastructure/rabbitmq  consumer payment.*.v1
  infrastructure/valkey    cache ketersediaan SSE + rate limit
  infrastructure/paymentclient resolve (service token, breaker Booking→Payment)
  infrastructure/healthcheck  ✅ sudah ada
```

---

## 3. API Endpoints

Semua respons memakai amplop standar `{ meta, data }`; error memakai `ErrorEnvelope{ meta (errorCode
terisi), data: null }`. Error **tidak pernah** memuat SQL, stack trace, secret, atau payload provider.

**Security schemes:** `visitorCookie` (`rb_visitor`), `visitorHeader` (`X-Visitor-Token`), `userBearer`
(JWT EdDSA User Service), `ticketLink` (query `t`), `serviceBearer` (ADR-011), `adminBearer` (role=admin).
Middleware dari `pkg/httpx` (platform R2).

### 3.1 Publik — Katalog (R1, R2)

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| GET | `/api/v1/events` | — | Daftar event + filter, cache 2s |
| GET | `/api/v1/events/{eventId}` | — | Detail event + kategori, cache 2s |

**`GET /api/v1/events`** — query `timeframe` ∈ {ALL,THIS_WEEK,THIS_MONTH,UPCOMING}, `city`, `genres`
(berulang atau CSV; overlap, kosong = semua), `startDate`/`endDate` (`YYYY-MM-DD`, inklusif WIB, opsional,
independen & di-AND dengan `timeframe`), `status` ∈ {ALL,NOT_STARTED,AVAILABLE,SOLD_OUT,CLOSED}, `sort` ∈
{DATE_ASC,DATE_DESC,PRICE_ASC,PRICE_DESC}.
→ `200 EventList{ events: EventSummary[], serverTime }`; `EventSummary{ eventId, slug, name, venue, genres,
startsAt, salesStartsAt, salesEndsAt, status, minPriceMinor }` (`genres` selalu array, `[]` bila kosong).
`minPriceMinor = MIN(price_minor)` per event. Query memfilter `status <> 'DRAFT'`.
Validasi: enum *fail-soft* ke default; tanggal cacat/terbalik & cap genre (>20 token, token >64 char)
`400 VALIDATION_ERROR` sebelum query DB. Filter genre memakai `e.genre && $genres::text[]` (GIN
`idx_events_genre`); rentang tanggal memakai `starts_at >= $start AND starts_at < $end` (sargable, btree
`idx_events_starts_at`). Cache key CDN memakai parameter **terkanonikalisasi**.

**`GET /api/v1/events/{eventId}`** → `200 EventDetail{ event, maxTicketsPerOrder, maxTicketsPerBuyerNik,
allowMixedCategories, categories:[{ categoryId, code, name, priceMinor, available }] }`; `available =
capacity − held_quantity − sold_quantity`. Error: `400 VALIDATION_ERROR`, `404 NOT_FOUND`.

### 3.2 Publik — Sesi (R3, R5, R7, R8, R15, R16)

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| POST | `/api/v1/booking-sessions` | visitor | Buat/pulihkan sesi dari admissionToken |
| GET | `/api/v1/booking-sessions/current?eventId=` | visitor | Pulihkan sesi/pesanan aktif |
| GET | `/api/v1/booking-sessions/{sessionId}` | visitor (pemilik) | Sisa waktu + ketersediaan (fallback SSE) |
| GET | `/api/v1/booking-sessions/{sessionId}/stream` | visitor (pemilik) | SSE availability + timer |
| POST | `/api/v1/booking-sessions/{sessionId}/attendee-checks` | visitor (pemilik) | Cek NIK pemegang |
| POST | `/api/v1/booking-sessions/{sessionId}/cancel` | visitor (pemilik) | Keluar tanpa membeli (204) |

`sessionId = admissions.id`. `BookingSession{ sessionId, eventId, phase∈{SELECTING,EDITABLE,LOCKED,ENDED},
reason?, reservedUnits, maxSelectable, buyerNikMasked?, allowMixedCategories?, expiresAt, serverTime,
submitGraceSeconds?, selectionWarningSeconds?, editEnabled?, maxEdits?, categories[], booking|null }`.

**Pemetaan `phase`** (dari admission + booking):

| Admission | Booking | `phase` | `reason` |
|-----------|---------|---------|----------|
| `ACTIVE` | — | `SELECTING` | — |
| `EDITABLE` | `PENDING_PAYMENT` | `EDITABLE` | — |
| `CONVERTED` | `PENDING_PAYMENT`/`VERIFYING` | `LOCKED` | `lock_reason` |
| `EXPIRED`/`RELEASED` atau booking final | apa pun / `PAID`/`EXPIRED`/`CANCELLED`/`REVIEW` | `ENDED` | `end_reason` / status booking |

**Admission token** (body `admissionToken`): diverifikasi `ITokenVerifier` dengan kunci publik Queue
(klaim `jti, eventId, entryId, allocationId, visitorId`, TTL 60 s); `visitorId` harus sama dengan
visitor token; `jti` disimpan ke `admissions.admission_jti` (UNIQUE) saat `RESERVED → ACTIVE`.

**SSE** — lihat §7.5. Event: `availability`, `timer` (OQ-2), heartbeat komentar `: ping`.

**Attendee check** — `200 AttendeeCheck{ result, checksRemaining }`; `422 NIK_INVALID`; batas → OQ-1.

### 3.3 Publik — Booking & Tiket (R6, R8, R10, R15)

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| POST | `/api/v1/bookings` | visitor | Hold (`Idempotency-Key` wajib) |
| GET | `/api/v1/bookings/{bookingId}` | visitor / ticketLink | Status booking |
| PUT | `/api/v1/bookings/{bookingId}/items` | visitor | Ubah pesanan sebelum bayar (`Idempotency-Key` + `version`) |
| POST | `/api/v1/bookings/{bookingId}/cancel` | visitor | Batal sebelum bayar (SHOULD, `CANCEL_ENABLED`) |
| GET | `/api/v1/bookings/{bookingId}/tickets` | visitor / ticketLink / userBearer | E-tiket setelah PAID |
| GET | `/api/v1/me/bookings` | userBearer | Riwayat pesanan pembeli login |

`Booking{ bookingId, publicRef, status∈{PENDING_PAYMENT,VERIFYING,PAID,EXPIRED,CANCELLED,REVIEW},
amountMinor, currency:"IDR", paymentExpiresAt, serverTime, version, canEdit, editableUntil?,
editsRemaining?, lockReason?∈{PAYMENT_STARTED,SELECTION_EXPIRED,BOOKING_ENDED,EDIT_DISABLED,null},
paymentPageUrl?, items:[{ itemId, categoryId, attendeeNikMasked, attendeeName }] }`.
`canEdit = admission EDITABLE ∧ now ≤ expires_at + grace ∧ payment_started_at IS NULL ∧ edit_count < cfg_max_edits`;
`editableUntil = admissions.expires_at`.

`account_id` booking diisi dari `admissions.account_id` (dibawa Queue dari user token, SRS-F-27-10);
`GET /me/bookings` memfilter `account_id = token.sub` memakai `idx_bookings_account`.

### 3.4 Internal (dipanggil Queue & Payment; `serviceBearer`, `X-Request-ID`) (R3, R14, R17)

| Method | Path | Pemanggil | Ringkas |
|--------|------|-----------|---------|
| POST | `/internal/v1/allocations:batch` | Queue | Reservasi alokasi batch (§9.1) |
| POST | `/internal/v1/allocations/{allocationId}:release` | Queue | Lepas alokasi yatim (saga, ADR-018) |
| GET | `/internal/v1/events/{eventId}/supply` | Queue | Ringkasan stok (Status Document) |
| GET | `/internal/v1/bookings/{bookingId}/payable` | Payment | Data tagihan |
| POST | `/internal/v1/bookings/{bookingId}/payment-lock` | Payment | Kunci pesanan sebelum attempt (§9.4) |
| POST | `/internal/v1/bookings/{bookingId}/deadline-extension` | Payment | Perpanjang sekali saat gateway gangguan |

**`POST /internal/v1/allocations:batch`** — `AllocationBatchRequest{ eventId, entries:[{ queueEntryId,
seq, visitorId, buyerNikHash, buyerNikMasked, buyerEmail, accountId|null }] }` →
`200 AllocationBatchResponse{ results:[{ queueEntryId, outcome∈{GRANTED,NO_SUPPLY_NOW,SOLD_OUT,
BUYER_QUOTA_EXHAUSTED,ALREADY_ACTIVE}, allocationId?, units?, claimDeadline? }] }`. `allocationId =
admissions.id`. Diproses per entry urut `seq` (transaksi pendek terpisah, §9.1).

**`POST /internal/v1/allocations/{allocationId}:release`** — `ReleaseRequest{ reason:"QUEUE_ENTRY_GONE" }`
→ `200 ReleaseResponse{ released }`. Hanya admission `RESERVED` → `RELEASED` + `allocated_units −= units`.

**`GET /internal/v1/events/{eventId}/supply`** → `200 Supply{ free, allocated, held, sold, total,
maxPerOrder, saleState∈{NOT_STARTED,OPEN,CLOSED}, soldOut, version, categories:[{ code, name, capacity,
available }] }`. `saleState` dari `events.status`/jendela penjualan; `version = event_supply.version`.

**`GET /internal/v1/bookings/{bookingId}/payable`** → `200 Payable{ bookingId, status, amountMinor,
paymentExpiresAt, visitorId?, buyerEmail?, version, canEdit? }`.

**`POST /internal/v1/bookings/{bookingId}/payment-lock`** — `PaymentLockRequest{ attemptKey }` →
`200 PaymentLockResponse{ outcome∈{OK,BOOKING_NOT_PAYABLE,PAYMENT_EXPIRED}, version?, amountMinor?,
paymentExpiresAt? }`. Mengisi `payment_started_at`, mengunci admission (`CONVERTED`, `PAYMENT_STARTED`),
melepas sisa unit. **Status booking tetap `PENDING_PAYMENT`** (K1). Idempotensi per `attemptKey` lewat
`idempotency_records(scope='payment_lock', owner_id=bookingId, key=attemptKey)`.

**`POST /internal/v1/bookings/{bookingId}/deadline-extension`** — `{ reason:"PROVIDER_UNAVAILABLE",
seconds:1..600 }` → `200 { paymentExpiresAt, extended }`. Sekali per booking (`deadline_extended_at`),
maks `OUTAGE_EXTENSION_SECONDS`; hanya bila belum ada instruksi pembayaran (Payment yang menegaskan
kondisi ini di request; Booking menegakkan "sekali").

### 3.5 Internal — Called (Booking → Payment)
- `POST /internal/v1/payments/{bookingId}/resolve` — `{ mode: CHECK | CHECK_AND_CANCEL }` →
  `{ outcome: SUCCEEDED | NOT_PAID | NO_ATTEMPT | UNKNOWN, settlementTime?, attemptId? }`
  (`internal-payment.yaml`). Dipanggil **di luar transaksi** oleh expiry sweeper (§7.2) dan cancel (§9.6),
  lewat `pkg/resilience` Breaker (timeout 3 s, ambang 5, cooldown 10 s, bulkhead 20 — PROPOSED SDD §4.6.2).
  Breaker OPEN / timeout ⇒ diperlakukan `UNKNOWN`.

### 3.6 Internal — Admin (R18; seed/testing; tidak lewat ALB publik)
- `POST /internal/v1/admin/events` (`adminBearer`) → `201 { eventId }`; membuat event + kategori +
  inventori + supply dalam satu transaksi (`SET LOCAL app.actor`). `status` awal `DRAFT`/`NOT_STARTED`.
- `PATCH /internal/v1/admin/events/{eventId}` — ubah config; `EVENT_RULES_LOCKED` → 409.
- `POST /internal/v1/admin/events/{eventId}/images` → `201 { imageUrl, type }` via `IImageStore` (OQ-16).
- `EventConfig{ maxTicketsPerOrder=3, maxTicketsPerBuyerNik=3, selectionTtlSeconds=300,
  paymentTtlSeconds=600, claimWindowSeconds=180, allowMixedCategories=true }`.
- Konfigurasi efektif per event (SRS-F-26-07): endpoint baca belum ada di kontrak (OQ-12).

### 3.7 Ops
- `GET /health` → `200 UP` / `503 DOWN` (ALB + container, D-23). ✅

---

## 4. Model Data (otoritatif: migrasi)

### 4.1 PostgreSQL (schema `booking`)

| Tabel | Peran | Kolom kunci | Invarian (CHECK/trigger/index) |
|-------|-------|-------------|--------------------------------|
| `events` | katalog + config | `slug UNIQUE`, `status ∈ {DRAFT,NOT_STARTED,AVAILABLE,SOLD_OUT,CLOSED}`, `timezone`, `max_tickets_per_order` (1..3), `max_tickets_per_buyer_nik` (1..3), `selection_ttl_seconds` (60..1800), `payment_ttl_seconds` (120..3600), `claim_window_seconds` (60..600), `max_attendee_checks_per_session` (3..50), `allow_mixed_categories`, `selection_edit_enabled`, `max_selection_edits` (0..10), `selection_submit_grace_seconds`, `selection_warning_seconds`, `rules_locked_at` | CHECK silang (`per_order ≤ per_buyer_nik`, grace/warning < ttl, `payment_ttl ≥ selection_ttl`, `sales_ends > sales_starts`); trigger `guard_event_rules`/`lock_rules_on_insert`; trigger `audit_event_config` → `config_audit` |
| `events` (**`0003` baru**) | filter & gambar | `city text NOT NULL DEFAULT ''` + index `lower(city)`; `image_url`, `banner_url`, `thumbnail_url text NULL` | migrasi additive; `0001`/`0002` tidak diubah |
| `ticket_categories` | kategori | `code`, `name`, `price_minor`≥0, `currency='IDR'`, `display_order` | `UNIQUE(event_id,code)`, `UNIQUE(event_id,id)` |
| `category_inventories` | stok per kategori | `capacity`, `held_quantity`, `sold_quantity` (≥0) | **CHECK `held_quantity + sold_quantity ≤ capacity`** |
| `event_supply` | stok tingkat event | `total_capacity`, `allocated_units`, `held_units`, `sold_units` (≥0), `version` | **CHECK `allocated+held+sold ≤ total_capacity`**; constraint trigger **deferred** `check_supply_matches_categories` (`SUPPLY_CATEGORY_MISMATCH`) |
| `admissions` | alokasi + sesi | `queue_entry_id UNIQUE`, `queue_seq`, `visitor_id`, `buyer_nik_hash`, `buyer_email`, `buyer_nik_masked`, `units` (1..3), `status`, `admission_jti UNIQUE`, `attendee_checks`, `edit_count`, snapshot `cfg_*`, `reserved_at`/`claimed_at`/`expires_at`/`locked_at`/`ended_at`, `lock_reason`, `end_reason`, `account_id` | `uq_admission_active_visitor`, `uq_admission_active_buyer` (parsial); `idx_admissions_due`; CHECK (`active ⇔ ended_at IS NULL`, `CONVERTED ⇔ lock_reason`, `edit_count ≤ cfg_max_edits`) |
| `attendee_checks` | dedupe cek NIK | PK `(admission_id, attendee_nik_hash)` | cek ulang tidak menambah hitungan |
| `buyer_quotas` | kuota pembeli | `held_quantity`, `paid_quantity`, `max_quantity` (1..3) | **CHECK `held + paid ≤ max_quantity`** |
| `bookings` | read model | `public_ref UNIQUE`, `admission_id UNIQUE`, `status`, `amount_minor`, `item_quantity` (1..3), `version`, `payment_expires_at`, `payment_started_at`, `deadline_extended_at`, `verification_until`, `settlement_time`, `paid_at`, `ended_at`, `account_id` | `idx_bookings_due`, `idx_bookings_visitor`, `idx_bookings_account`; CHECK (`PAID ⇒ paid_at & settlement_time`, `VERIFYING ⇒ verification_until`) |
| `booking_items` | 1 baris/tiket | `unit_price_minor`, `attendee_nik_hash`, `attendee_nik_enc`, `attendee_nik_masked`, `attendee_name` (2..100), `item_status ∈ {HELD,PAID,RELEASED,REPLACED}` | `uq_attendee_nik_per_event`, `uq_booking_item_nik_active` (HELD/PAID) |
| `tickets` | e-tiket | `public_code UNIQUE`, `booking_item_id UNIQUE` | 1:1 item |
| `booking_events` | **event store** | PK `(stream_id, version>0)`, `event_type ~ '^[A-Z][A-Za-z]+$'`, `schema_version`, `payload jsonb`, `metadata`, `occurred_at`, `global_seq` | append-only triggers; konflik = SQLSTATE 23505 |
| `outbox_events` | outbox | `routing_key`, `aggregate_id`, `schema_version`, `payload`, `available_at`, `published_at`, `publish_attempts`, `last_error` | `idx_booking_outbox_pending` |
| `idempotency_records` | dedupe request | PK `(scope, owner_id, idempotency_key)`, `request_hash`, `state`, `response_status`, `response_body`, `expires_at` | scope: `create_booking`, `replace_items`, `cancel_booking`, `payment_lock` |
| `config_audit` | audit config | `event_id`, `changed_by` (`app.actor`), `field`, `old_value`, `new_value` | diisi trigger |

`public_ref` (mis. `RB-00912`) dibangkitkan saat hold; format persis tidak ditetapkan kontrak [asumsi:
prefix `RB-` + sekuens].

### 4.2 Valkey (cache SSE + rate limit)

Otoritas stok tetap PostgreSQL. Prefix `VALKEY_PREFIX_ENV`.

| Pola kunci | Tipe | TTL | Guna |
|------------|------|-----|------|
| `booking:avail:{eventId}` | string JSON | 2 × `SSE_AVAILABILITY_INTERVAL_MS` | snapshot ketersediaan kategori (ditulis job §7.5) |
| `booking:sse:conn:{instance}` | counter | proses | batas koneksi SSE per instance |
| `booking:sse:session:{sessionId}` | string (connId) | sesi | satu stream per sesi; koneksi lama ditutup bila berbeda |
| `booking:rl:session:{visitorId}:{eventId}` | counter | window | rate limit `GET current` → `429 RATE_LIMITED` |

Batas cek NIK ditegakkan di PostgreSQL (`admissions.attendee_checks`), bukan Valkey.

---

## 5. Event Sourcing (ADR-019)

### 5.1 Tipe event (stream = `bookings.id`) — mengikuti SDD-RBT §4.5.3

| event_type | Dipicu oleh | Proyeksi read model | Stok | Outbox |
|------------|-------------|---------------------|------|--------|
| `BookingHeld` | Hold (R6) | `bookings`(PENDING_PAYMENT) + `booking_items`(HELD) | `allocated→held` | — |
| `BookingItemsReplaced` | Edit (R8) | item lama `REPLACED`, baru `HELD`, `amount_minor`, `version++` | penyesuaian held/allocated | — |
| `BookingLocked` (`lockReason`) | payment-lock (R14), sweeper EDITABLE (R7.3), hold dengan edit nonaktif/grace (R6.8) | `payment_started_at` (bila PAYMENT_STARTED) | sisa unit `allocated→free` | — |
| `PaymentVerifying` | expiry sweeper (R9.7) | `status=VERIFYING`, `verification_until` | tetap ditahan | — |
| `BookingPaid` (`settlementTime`, `attemptId`) | settlement (R9.1/9.8) | `PAID` + `tickets` + item `PAID` | `held→sold`; kuota `held→paid` | `booking.paid.v1`, `booking.journey.ended.v1(PAID)` |
| `BookingExpired` (`reason`) | resolve NOT_PAID/NO_ATTEMPT atau verify grace habis (R9.9/9.10) | `EXPIRED`, item `RELEASED` | `held→free`; kuota kembali | `booking.expired.v1`, `booking.journey.ended.v1(EXPIRED)` |
| `BookingCancelled` | cancel (R15.2) | `CANCELLED`, item `RELEASED` | `held→free`; kuota kembali | `booking.expired.v1(CANCELLED)`, `booking.journey.ended.v1(CANCELLED)` |
| `BookingReview` | settlement terlambat / setelah EXPIRED/CANCELLED (R9.3/9.4) | `REVIEW` | terlambat: `held→free`; sudah final: tidak berubah | `booking.review.v1` (+ `journey.ended(EXPIRED)` bila dari PENDING/VERIFYING) |

`BookingLocked` dari payment-lock tidak mengubah status booking, tetapi tetap dicatat agar replay dapat
merekonstruksi `payment_started_at` dan audit kunci. Event admission tanpa booking (sesi habis tanpa
hold, keluar tanpa membeli) **tidak** masuk event store (bukan agregat Booking); jejaknya di kolom
`admissions` + outbox `journey.ended`.

Payload event: `BookingHeld.items[]{categoryId, attendeeNikHash, attendeeNikMasked, priceMinor}` — tanpa
NIK plaintext, email, token (ditolak `pkg/eventstore`).

### 5.2 Skema event store & versioning
`booking_events(stream_id, version, event_type, schema_version, payload, metadata{correlationId,
causationId, actor}, occurred_at, global_seq)`. `version` naik per stream; `expectedVersion =
bookings.version`. `schema_version` menaungi evolusi payload (tambah field opsional, jangan ubah makna).

### 5.3 Aturan replay & proyeksi
- **Proyeksi sinkron** dalam transaksi command yang sama.
- **Replay/rebuild** read model per `global_seq`; **stok tidak di-replay** (relasional).
- **Append-only** ditegakkan DB (TC-ES-01).
- `ErrConcurrency` → baca ulang, `decide()` ulang, coba lagi (maks 3) lalu `503 DEPENDENCY_UNAVAILABLE`.

---

## 6. State Machines

### 6.1 Admission (`admissions.status`)
```
                 create session                hold                    payment-lock / sweeper / hold(edit off|grace)
  RESERVED ─────────────────────► ACTIVE ───────────────► EDITABLE ────────────────────────────────► CONVERTED
     │ now > expires_at              │ now > expires_at+grace   │ booking berakhir saat editable          (lock_reason:
     │ (claim window)                │ / cancel session         │ (expire/cancel)                          PAYMENT_STARTED |
     ▼                               ▼                          ▼                                          SELECTION_EXPIRED |
  EXPIRED (UNCLAIMED)        EXPIRED (SESSION_EXPIRED /   CONVERTED (BOOKING_ENDED)                       EDIT_DISABLED)
  RELEASED (Queue release)           LEFT_WITHOUT_BUYING)
```
CHECK: `(status∈{RESERVED,ACTIVE,EDITABLE}) = (ended_at IS NULL)`; `(status=CONVERTED) = (lock_reason IS NOT NULL)`;
`edit_count ≤ cfg_max_edits`. `end_reason`∈{UNCLAIMED,SESSION_EXPIRED,LEFT_WITHOUT_BUYING,CONVERTED};
`CONVERTED` selalu `end_reason=CONVERTED`, `ended_at=locked_at`. `RELEASED` hanya dari
`allocations/{id}:release`. **(Δ6, Δ9.)**

### 6.2 Booking (`bookings.status`)
```
                     settlement tepat waktu (webhook / resolve SUCCEEDED)
  PENDING_PAYMENT ─────────────────────────────────────────────────────────► PAID
        │  now ≥ payment_expires_at (sweeper)              ▲
        ▼                                                  │ resolve SUCCEEDED tepat waktu / webhook tepat waktu
     VERIFYING ────────────────────────────────────────────┘
        │ resolve NOT_PAID/NO_ATTEMPT  atau  now > verification_until
        ▼
     EXPIRED ──(payment.succeeded datang belakangan)──► REVIEW
  PENDING_PAYMENT ──(cancel + resolve NOT_PAID)──► CANCELLED ──(payment.succeeded)──► REVIEW
  PENDING_PAYMENT/VERIFYING ──(payment.succeeded terlambat: settlement_time > deadline)──► REVIEW
```
CHECK: `PAID ⇒ paid_at & settlement_time`; `VERIFYING ⇒ verification_until`. Status monoton (invarian #8).
`payment-lock` **tidak** mengubah status (K1). **(Δ1, Δ9.)**

### 6.3 Booking Item (`booking_items.item_status`)
```
  HELD ──(edit)──► REPLACED     HELD ──(settlement)──► PAID     HELD ──(expire/cancel/review terlambat)──► RELEASED
```

---

## 7. Worker Jobs

Loop `internal/delivery/worker.go` menjalankan `jobs()` tiap `WORKER_INTERVAL`. Consumer RabbitMQ
didaftarkan sebagai `rabbitmq.Subscription` di `RunWorker` (bukan periodic job). Semua job aman
multi-instance (`FOR UPDATE SKIP LOCKED`).

### 7.1 Session Sweeper (SDD C.5 + C.10)
```
SELECT … FROM admissions
 WHERE status IN ('RESERVED','ACTIVE','EDITABLE')
   AND expires_at + CASE WHEN status='RESERVED' THEN 0 ELSE cfg_submit_grace_seconds END * interval '1s' < clock_timestamp()
 ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT 200;           -- idx_admissions_due
per baris (lock order §9.0):
  RESERVED → EXPIRED, end_reason=UNCLAIMED;       supply.allocated -= units; outbox journey.ended(UNCLAIMED)
  ACTIVE   → EXPIRED, end_reason=SESSION_EXPIRED; supply.allocated -= units; outbox journey.ended(EXPIRED*)   *OQ-15
  EDITABLE → CONVERTED, lock_reason=SELECTION_EXPIRED; supply.allocated -= (units - item_quantity);
             append BookingLocked(SELECTION_EXPIRED); booking tetap PENDING_PAYMENT
```

### 7.2 Expiry Sweeper (booking expiry + resolve, SDD C.4)
```
Fase A (tx): SELECT bookings WHERE status='PENDING_PAYMENT' AND payment_expires_at <= now()
             FOR UPDATE SKIP LOCKED LIMIT 200
             → status VERIFYING, verification_until = now() + VERIFY_GRACE_SECONDS, append PaymentVerifying
Fase B (di luar tx): SELECT bookings WHERE status='VERIFYING' (idx_bookings_due)
   outcome = paymentClient.Resolve(id, CHECK_AND_CANCEL)    -- breaker; error/OPEN ⇒ UNKNOWN
   tx (lock order §9.0, re-check status = VERIFYING):
     SUCCEEDED ∧ settlementTime ≤ payment_expires_at → settlement §9.2
     SUCCEEDED ∧ settlementTime >  payment_expires_at → REVIEW + held→free (§9.2 cabang terlambat)
     NOT_PAID | NO_ATTEMPT                           → BookingExpired(PAYMENT_TIMEOUT) + held→free + kuota kembali
                                                        + admission EDITABLE→CONVERTED(BOOKING_ENDED) + outbox expired, journey.ended(EXPIRED)
     UNKNOWN ∧ now <  verification_until             → tidak berubah; dicoba lagi tick berikut
     UNKNOWN ∧ now >= verification_until             → seperti NOT_PAID
```
Booking tanpa attempt tetap melewati resolve (`NO_ATTEMPT`) — biaya satu panggilan, menjaga satu jalur
dan mencegah race dengan attempt yang dibuat tepat sebelum deadline. Retry ownership: expiry worker
(SDD §4.6.2).

### 7.3 Outbox Relay
`pkg/outbox.Relay.RunOnce` (platform R3): pending → publish envelope ke `rusuh.events` → `published_at`;
gagal → `publish_attempts++`, `last_error`, backoff `available_at`.

### 7.4 Rekonsiliasi (R11.7, SRS-F-90-01)
Query read-only periodik; hasil ke metrik `rbt_reconcile_violations{check}` (platform R9):

| Check | Query (ringkas) |
|-------|-----------------|
| `supply_equation` | `allocated_units = Σ units admission RESERVED/ACTIVE + Σ (units − item_quantity) EDITABLE`; `held_units = Σ item HELD`; `sold_units = Σ item PAID` |
| `category_equation` | per kategori `held_quantity = Σ item HELD`, `sold_quantity = Σ item PAID` |
| `quota_vs_items` | per (event, buyer) `held_quantity`/`paid_quantity` = Σ item HELD/PAID booking pembeli itu |
| `tickets_vs_paid` | setiap item `PAID` punya tepat satu `tickets` |
| `outbox_pending_age` | baris pending tertua > 30 s |
| `readmodel_vs_events` | `bookings.version` = `MAX(booking_events.version)` per stream |

### 7.5 SSE availability cache
Job tiap `SSE_AVAILABILITY_INTERVAL_MS` (hanya bila `SSE_ENABLED`): untuk event `AVAILABLE`, baca
`category_inventories` → tulis `booking:avail:{eventId}`. Handler stream membaca cache, mengirim
`availability` hanya bila berubah, `timer` saat fase/expiresAt berubah, heartbeat tiap
`SSE_HEARTBEAT_SECONDS`; menutup stream saat sesi berakhir (R16.5).

---

## 8. Event Contracts

Envelope (`envelope.schema.json`): `{ messageId, eventType, schemaVersion≥1, occurredAt, producer =
"booking-service", aggregateId, correlationId?, payload }`. Routing key **berversi** (K8).

### 8.1 Dikonsumsi (producer: payment-service)

| Routing key | Queue | Payload | Aksi | Idempotensi |
|-------------|-------|---------|------|-------------|
| `payment.succeeded.v1` | `booking.payment-succeeded` | `{ bookingId, attemptId, settlementTime, amountMinor, method }` | §9.2 | lock booking + guard status (`PAID`/`REVIEW` → no-op); `tickets ON CONFLICT DO NOTHING` |
| `payment.failed.v1` | `booking.payment-failed` | `{ bookingId, attemptId, reason }` | catat saja | no-op |

Retry berjenjang `RABBITMQ_RETRY_DELAYS=5s,30s,2m`, DLQ setelah `RABBITMQ_MAX_RETRIES=3`; payload rusak
→ `rabbitmq.Permanent(err)`. Perilaku bila `amountMinor` ≠ `bookings.amount_minor` tidak didefinisikan
dokumen mana pun (Payment memverifikasi nominal webhook, SRS-F-15-01) — OQ-17; implementasi awal
mencatat error + alert dan **tidak** menerapkan settlement sampai diputuskan.

### 8.2 Dipublikasikan (producer: booking-service)

| Routing key | Consumer queue | Payload (schema v1) |
|-------------|----------------|---------------------|
| `booking.journey.ended.v1` | `queue.journey-ended` | `{ eventId, queueEntryId, outcome∈{PAID,EXPIRED,CANCELLED,LEFT_WITHOUT_BUYING,UNCLAIMED} }` |
| `booking.quota.exhausted.v1` | `queue.quota-exhausted` | `{ eventId, buyerNikHash }` (pemicu: OQ-13) |
| `booking.sold_out.v1` | `queue.sold-out` | `{ eventId }` |
| `booking.paid.v1` | `notification.booking-paid` | `{ bookingId, publicRef, buyerEmail, event?{eventId,name,startsAt,venue}, tickets[]{ticketCode,attendeeName,attendeeNikMasked,categoryCode}, ticketLinkToken }` |
| `booking.expired.v1` | `notification.booking-expired` | `{ bookingId, publicRef, buyerEmail, reason∈{PAYMENT_TIMEOUT,SESSION_EXPIRED,CANCELLED} }` |
| `booking.review.v1` | `notification.booking-review`, `payment.booking-review` | `{ bookingId, attemptHint?, buyerEmail, amountMinor }` |

---

## 9. Atomic Operations (IUnitOfWork)

### 9.0 Urutan lock global (SDD-RBT §4.7, K5)
1. klaim idempotency (`INSERT … ON CONFLICT DO NOTHING`, lalu baca & verifikasi) — hanya request HTTP;
2. `admissions` FOR UPDATE; 3. `bookings` FOR UPDATE (bila ada); 4. `event_supply` FOR UPDATE;
5. `buyer_quotas` FOR UPDATE (insert `ON CONFLICT DO NOTHING` bila belum ada);
6. `category_inventories` FOR UPDATE urut `category_id ASC`;
7. insert/update `booking_items`, `tickets`, `booking_events`, `outbox_events`; 8. commit; panggilan
eksternal hanya di luar transaksi.

Jalur yang dimulai dari booking (settlement, expiry, payment-lock) membaca `admission_id` tanpa lock,
lalu mengunci admission → booking dan memvalidasi ulang status. `IUnitOfWork.Do(ctx, fn)` menyetel
`SET LOCAL lock_timeout='2s'`, `statement_timeout='5s'`, `app.actor`.

### 9.1 Alokasi batch (SDD C.2)
```
per entry (tx pendek):
  supply FOR UPDATE → free = total - allocated - held - sold
  quota  FOR UPDATE (insert bila belum ada, max_quantity = events.max_tickets_per_buyer_nik)
  remaining = max_quantity - (held + paid)
  remaining = 0                 → BUYER_QUOTA_EXHAUSTED
  free = 0 ∧ allocated+held > 0 → NO_SUPPLY_NOW
  free + allocated + held = 0   → SOLD_OUT; events.status=SOLD_OUT; outbox booking.sold_out.v1 (sekali)
  units = min(max_tickets_per_order, remaining, free)
  INSERT admissions(RESERVED, units, expires_at = now + claim_window_seconds, cfg_* snapshot,
                    queue_entry_id, account_id) ON CONFLICT (queue_entry_id) → kembalikan hasil lama
     unique violation uq_admission_active_* → ALREADY_ACTIVE
  supply.allocated += units, version++
```

### 9.2 Hold (SDD C.3)
```
BEGIN; SET LOCAL lock_timeout='2s', statement_timeout='5s'
  1 idempotency(create_booking, visitor, key)
  2 admissions FOR UPDATE: id=$s, visitor=$v, status='ACTIVE', clock_timestamp() <= expires_at + grace
  3 supply → 4 quota → 5 categories ASC (FOR UPDATE)
  6 validasi decide(): n ≤ units; n ≤ sisa kuota; mixed; per kategori held+sold+n_c ≤ capacity; NIK unik dalam order
  7 append BookingHeld(v1); INSERT bookings(PENDING_PAYMENT, payment_expires_at = now + cfg_payment_ttl, version=1),
    booking_items(HELD)  -- 23505 uq_attendee_nik_per_event → rollback → 409
  8 categories.held += n_c; 9 supply.allocated -= n, held += n; 10 quota.held += n
  11 admissions → EDITABLE
     bila !cfg_edit_enabled atau diterima di masa grace → CONVERTED(EDIT_DISABLED|SELECTION_EXPIRED),
     supply.allocated -= (units - n), append BookingLocked
  12 simpan respons ke idempotency_records (COMPLETED)
COMMIT   -- CHECK oversell → rollback → 409 CATEGORY_UNAVAILABLE (+ ketersediaan terbaru)
```

### 9.3 Edit — penggantian atomik (SDD C.8)
```
BEGIN
  1 idempotency(replace_items, visitor, key)
  2 admissions FOR UPDATE: status='EDITABLE', now ≤ expires_at+grace, edit_count < cfg_max_edits
       CONVERTED → 409 BOOKING_LOCKED; edit_count ≥ max → 409 EDIT_LIMIT_REACHED
  3 bookings FOR UPDATE: PENDING_PAYMENT, payment_started_at IS NULL, version = $expected
       beda → 409 BOOKING_VERSION_CONFLICT
  4 supply, quota, categories ASC FOR UPDATE
  5 SAVEPOINT replace_items
       item lama → REPLACED, kembalikan held per kategori; resolve keepFromItemId (hash/enc/masked lama)
       INSERT item baru HELD (23505 → ROLLBACK TO SAVEPOINT → 409, pesanan lama utuh, edit_count tidak naik)
       tambah held per kategori (CHECK)
  6 supply: allocated += n_lama − n_baru, held += n_baru − n_lama
  7 quota.held += n_baru − n_lama
  8 bookings: item_quantity, amount_minor (harga kategori), version+1; payment_expires_at TIDAK berubah
  9 admissions.edit_count + 1; append BookingItemsReplaced
COMMIT
```

### 9.4 Payment-lock (SDD C.9, K1)
```
BEGIN
  idempotency(payment_lock, bookingId, attemptKey) → hasil lama bila COMPLETED
  admissions FOR UPDATE → bookings FOR UPDATE
  booking.status ≠ PENDING_PAYMENT → outcome BOOKING_NOT_PAYABLE
  clock_timestamp() ≥ payment_expires_at → outcome PAYMENT_EXPIRED
  UPDATE bookings SET payment_started_at = COALESCE(payment_started_at, now())
  bila admission EDITABLE:
     supply.allocated -= (units - item_quantity)
     admission → CONVERTED, lock_reason=PAYMENT_STARTED, locked_at=ended_at=now(), end_reason=CONVERTED
     append BookingLocked(PAYMENT_STARTED)
  return OK + version + amount_minor + payment_expires_at
COMMIT
```

### 9.5 Settlement — held → sold atomik (SDD C.4, K4)
```
BEGIN
  baca admission_id; admissions FOR UPDATE → bookings FOR UPDATE
  status ∈ {PAID, REVIEW}                         → COMMIT (no-op, ack)
  status ∈ {EXPIRED, CANCELLED}                   → append BookingReview; status REVIEW; outbox booking.review
  status ∈ {PENDING_PAYMENT, VERIFYING}:
     settlementTime > payment_expires_at          → append BookingReview; REVIEW; item RELEASED;
                                                     held→free (category/supply), quota.held -= n;
                                                     outbox booking.review + journey.ended(EXPIRED)
     else → supply, quota, categories FOR UPDATE
            append BookingPaid; status PAID, paid_at, settlement_time
            items HELD→PAID; INSERT tickets ON CONFLICT (booking_item_id) DO NOTHING
            category held→sold; supply held→sold; quota held→paid
            admission EDITABLE → CONVERTED(BOOKING_ENDED) bila belum terkunci (sisa unit dilepas)
            outbox booking.paid (+ticketLinkToken), journey.ended(PAID)
COMMIT
```

### 9.6 Cancel booking (R15, SDD C.4 kolom "Cancel customer")
```
di luar tx: outcome = Resolve(CHECK_AND_CANCEL)
  UNKNOWN → 503 DEPENDENCY_UNAVAILABLE (tidak ada perubahan)
tx (lock order): re-check status PENDING_PAYMENT ∧ now < payment_expires_at (else 409 BOOKING_LOCKED)
  SUCCEEDED tepat waktu → §9.5
  NOT_PAID | NO_ATTEMPT → append BookingCancelled; CANCELLED; items RELEASED; held→free; quota kembali;
                          admission → CONVERTED(BOOKING_ENDED) bila EDITABLE;
                          outbox booking.expired(CANCELLED), journey.ended(CANCELLED)
```

---

## 10. Sequence Diagrams

### 10.1 Admission (reservasi — Jangan PHP)
```
Queue → Booking: POST /internal/v1/allocations:batch { entries[] }   (service token)
loop per entry: Booking → PG: tx §9.1
Booking → Queue: 200 { results[]: GRANTED(allocationId, units, claimDeadline) | NO_SUPPLY_NOW | SOLD_OUT | BUYER_QUOTA_EXHAUSTED | ALREADY_ACTIVE }
Queue: terbitkan admissionToken (jti, allocationId) untuk GRANTED
```

### 10.2 Buka sesi & Hold
```
Client → Booking: POST /booking-sessions { admissionToken }   (visitor token)
Booking: verify admissionToken (kunci Queue), visitor cocok
Booking → PG: admission RESERVED→ACTIVE, admission_jti, expires_at = now + cfg_selection_ttl
Booking → Client: 201 BookingSession { phase=SELECTING, expiresAt, serverTime, reservedUnits, categories }
Client → Booking: POST /bookings (Idempotency-Key) { sessionId, items[] }
Booking → PG: tx §9.2  (CHECK oversell → rollback → 409 CATEGORY_UNAVAILABLE)
Booking → Client: 201 Booking { status=PENDING_PAYMENT, paymentExpiresAt, version=1, canEdit=true }
```

### 10.3 Edit lalu "Bayar"
```
Client → Booking: PUT /bookings/{id}/items (Idempotency-Key) { version, items[] } → tx §9.3 → 200 Booking (version+1)
Client → Payment: POST /payments/{id}/attempts (Idempotency-Key) { method }
Payment → Booking: POST /internal/v1/bookings/{id}/payment-lock { attemptKey } → tx §9.4 → OK(version, amountMinor)
Payment → Midtrans: charge(amountMinor versi terkunci, expiry = paymentExpiresAt)
Client → Booking: PUT /bookings/{id}/items → 409 BOOKING_LOCKED
```

### 10.4 Settlement (dari Payment)
```
Payment → RabbitMQ: payment.succeeded.v1 { bookingId, attemptId, settlementTime, amountMinor, method }
RabbitMQ → Booking(consumer booking.payment-succeeded): tx §9.5 → ack
OutboxRelay → RabbitMQ: booking.paid.v1 → Notification; booking.journey.ended.v1(PAID) → Queue
```

### 10.5 Expiry (sweeper + resolve)
```
Worker → PG: Fase A — PENDING_PAYMENT lewat deadline → VERIFYING (verification_until)
Worker → Payment: POST /internal/v1/payments/{id}/resolve { mode: CHECK_AND_CANCEL }   (di luar tx, breaker)
alt SUCCEEDED tepat waktu → tx §9.5 (PAID)
alt NOT_PAID | NO_ATTEMPT → tx: BookingExpired + held→free + outbox expired, journey.ended(EXPIRED)
alt UNKNOWN & now < verification_until → tidak berubah, ulang tick berikutnya
alt UNKNOWN & now ≥ verification_until → seperti NOT_PAID
```

---

## 11. Error Catalog (domain → HTTP via `pkg/apperror`)

| Error domain | errorCode | HTTP | Pemicu | Endpoint |
|--------------|-----------|------|--------|----------|
| ErrValidation | `VALIDATION_ERROR` | 400 | field tidak valid | semua |
| ErrUnauthorized | `UNAUTHORIZED` | 401 | token user/service tidak valid | me/bookings, internal |
| ErrAdmissionToken | `ADMISSION_TOKEN_INVALID` | 403 | token salah/dipakai ulang | create session |
| ErrVisitorMismatch | `VISITOR_MISMATCH` | 403 | visitor beda | sesi |
| ErrNotFound | `NOT_FOUND` | 404 | resource tidak ada / bukan pemilik | detail, sesi, booking |
| ErrCategoryUnavailable | `CATEGORY_UNAVAILABLE` | 409 | CHECK kategori / supply | hold, edit |
| ErrBuyerQuota | `BUYER_QUOTA_EXHAUSTED` | 409 | kuota pembeli habis | hold, edit |
| ErrNikRegistered | `ATTENDEE_NIK_ALREADY_REGISTERED` | 409 | `uq_attendee_nik_per_event` | hold, edit |
| ErrNikDuplicate | `ATTENDEE_NIK_DUPLICATE_IN_ORDER` | 409 | NIK duplikat dalam order | hold, edit |
| ErrIdemReused | `IDEMPOTENCY_KEY_REUSED` | 409 | key sama, `request_hash` beda | hold, edit, cancel |
| ErrBookingLocked | `BOOKING_LOCKED` | 409 | edit/cancel setelah kunci / deadline | edit, cancel |
| ErrVersionConflict | `BOOKING_VERSION_CONFLICT` | 409 | version tidak cocok | edit |
| ErrEditLimit | `EDIT_LIMIT_REACHED` | 409 | `edit_count ≥ cfg_max_edits` | edit |
| ErrNotPayable | `BOOKING_NOT_PAYABLE` | 409 | tiket sebelum PAID | tickets |
| ErrRulesLocked | `EVENT_RULES_LOCKED` | 409 | DB exception setelah lock | admin PATCH |
| ErrSessionExpired | `SESSION_EXPIRED` | 410 | > `expires_at + grace` / sesi berakhir | sesi, hold, edit, attendee-check |
| ErrTurnMissed | `TURN_MISSED` | 410 | claim deadline terlampaui | create session |
| ErrNIKInvalid | `NIK_INVALID` | 422 | struktur NIK salah | attendee-checks, hold, edit |
| ErrQuantityExceeds | `QUANTITY_EXCEEDS_LIMIT` | 422 | n > units / maxTicketsPerOrder | hold, edit |
| ErrMixedNotAllowed | `MIXED_CATEGORIES_NOT_ALLOWED` | 422 | mixed dilarang | hold, edit |
| ErrRateLimited | `RATE_LIMITED` | 429 | rate limit (Retry-After); batas cek NIK → OQ-1 | current, attendee-checks |
| ErrDependency | `DEPENDENCY_UNAVAILABLE` | 503 | DB/broker/Payment/SSE penuh; `ErrConcurrency` habis retry | hold, edit, cancel, stream |
| ErrConcurrency | *(internal, retry)* | — | konflik versi event store (23505) | semua command ES |
| *(DB)* | `SUPPLY_CATEGORY_MISMATCH` | — | supply ≠ Σ kategori saat COMMIT | admin/seed |

Pemetaan constraint → error di adapter postgres: `uq_attendee_nik_per_event` → ErrNikRegistered;
`uq_booking_item_nik_active` → ErrNikDuplicate; CHECK `category_inventories`/`event_supply` →
ErrCategoryUnavailable; CHECK `buyer_quotas` → ErrBuyerQuota; `lock_not_available`/`query_canceled`
→ ErrDependency.

---

## 12. Konfigurasi (`internal/config`, D-24, fail-fast)

| Kelompok | Variabel | Status |
|----------|----------|--------|
| App | `APP_ENV`, `APP_NAME=booking`, `APP_PORT=8082`, `LOG_LEVEL`, `LOG_FORMAT` | ✅ |
| HTTP/Worker | `HTTP_SHUTDOWN_TIMEOUT=15s`, `HEALTH_TIMEOUT=2s`, `WORKER_INTERVAL=5s` | ✅ |
| Postgres | `POSTGRES_HOST/PORT/USER=svc_booking/PASSWORD/DATABASE=rbt/SSLMODE/SCHEMA=booking/MAX_CONNS` | ✅ |
| Valkey | `VALKEY_SERVER` (JSON), `VALKEY_USERNAME/PASSWORD/TLS/DB/PREFIX_ENV` | ✅ |
| RabbitMQ | `RABBITMQ_URL/EXCHANGE=rusuh.events/EXCHANGE_TYPE=topic/WORKERS/PREFETCH/MAX_RETRIES=3/RETRY_DELAYS=5s,30s,2m/CONFIRM_TIMEOUT=5s` | ✅ |
| Payment client | `PAYMENT_SERVICE_URL` + breaker (`timeout 3s`, ambang 5, cooldown 10s, bulkhead 20) | ⏳ |
| Token | kunci publik Queue (admission/visitor), kunci publik User (`userBearer`), kunci privat ticket link + `kid`, kunci privat service token, `PII_HASH_KEY`, kunci enkripsi NIK pemegang | ⏳ (nama variabel mengikuti pola `TOKEN_*_FILE` User; ditetapkan saat task 1.4) |
| Waktu/fitur (SDD-RBT §4.10) | `VERIFY_GRACE_SECONDS=180` (60–600), `OUTAGE_EXTENSION_SECONDS=300` (0–600), `CANCEL_ENABLED=true`, `MAX_TICKETS_PER_ATTENDEE_NIK=1`, `PII_RETENTION_DAYS_AFTER_EVENT=30` | ⏳ |
| SSE | `SSE_ENABLED=true`, `SSE_AVAILABILITY_INTERVAL_MS=1000` (500–5000), `SSE_HEARTBEAT_SECONDS=20` (10–45), `SSE_MAX_CONNECTIONS_PER_INSTANCE=5000` | ⏳ |

TTL dan batas per event diambil dari DB (snapshot admission), **bukan** env (SDD-BOOKING §10 yang memuat
`BOOKING_TTL_SECONDS=600/PAYMENT_TTL_SECONDS=900` tidak dipakai — bertentangan dengan default PRD 300/600
dan prinsip snapshot). Secret lewat env/SSM.

---

## 13. Resilience, Observability & Testing

**Resilience:** consumer idempoten tanpa inbox; retry + DLQ; `Booking→Payment` lewat breaker +
bulkhead; DB primary down → `503` tanpa hold setengah jadi (TC-CAP-01); broker down → commit tetap
jalan, outbox menunda (TC-CAP-03); Payment down saat expiry → `VERIFYING` menahan stok (TC-PAY-08).

**Observability (SDD-BOOKING §11, SRS-Q-MNT-03):** `rbt_booking_sessions_total{event_id,status}`,
`rbt_booking_holds_total{event_id,status}`, `rbt_booking_settlements_total{event_id}`,
`rbt_booking_expirations_total{event_id,reason}`, `rbt_booking_stock_gauge{event_id,type}`,
`rbt_booking_edits_total{event_id,result}`, `rbt_booking_verifying_gauge{event_id}`,
`rbt_booking_review_total{event_id}`. *(`rbt_booking_event_store_version{aggregate_id}` dihapus —
label booking ID melanggar SRS-Q-MNT-03.)* Alert: `BookingStockNegative` (Critical),
`BookingExpiryRateHigh` (>20%/10m), `BookingOutboxLag` (>30s/5m), `BookingDBLatencyHigh` (p99>100ms/5m).

**Testing (`testing-review.md`):** lihat Correctness Properties dan tasks.md.

---

## 14. Correctness Properties

### Property 1: No oversell
Untuk setiap urutan operasi bersamaan (alokasi, hold, edit, lock, settlement, expiry, cancel), setelah setiap commit berlaku `held+sold ≤ capacity` per kategori dan `allocated+held+sold ≤ total_capacity` per event; 100 hold paralel pada 50 tiket terakhir menghasilkan tepat 50 hold.
**Validates: Requirements 6.5, 11.5, 11.6**

### Property 2: Jangan PHP
Untuk setiap admission `ACTIVE` dengan `units = N`, hold berisi n ≤ N item dari kategori yang `available ≥ n_c` dalam TTL selalu berhasil; setelah hold, kenaikan kembali sampai N saat `EDITABLE` selalu berhasil dari kategori yang tersedia.
**Validates: Requirements 3.6, 3.7, 8.3**

### Property 3: Buyer quota & attendee uniqueness
Untuk setiap (event, NIK pembeli), `held+paid ≤ max_quantity` lintas sesi; untuk setiap (event, NIK pemegang), paling banyak satu item `HELD`/`PAID`.
**Validates: Requirements 5.7, 5.8, 5.9, 5.10**

### Property 4: Edit atomicity
Untuk setiap edit yang gagal, booking, item, stok, kuota, dan `edit_count` identik dengan sebelum request; untuk setiap edit sukses, `version` naik tepat 1 dan `payment_expires_at` tidak berubah.
**Validates: Requirements 8.1, 8.4, 8.8**

### Property 5: Lock serialization
Untuk edit dan payment-lock bersamaan pada pesanan yang sama, tepat satu urutan terjadi: edit lalu lock (lock mengunci versi terbaru) atau lock lalu edit (`409 BOOKING_LOCKED`); nominal yang dikembalikan lock selalu `amount_minor` versi yang dikunci.
**Validates: Requirements 14.2, 14.3, 8.6**

### Property 6: Idempotent settlement
Untuk setiap jumlah pengiriman ulang `payment.succeeded` yang sama, jumlah tiket = jumlah item, `sold` naik tepat sekali, dan hanya satu `BookingPaid` di event store.
**Validates: Requirements 9.1, 9.5**

### Property 7: Timeliness by settlement time
Untuk setiap `payment.succeeded`, booking menjadi `PAID` jika dan hanya jika status `PENDING_PAYMENT`/`VERIFYING` dan `settlementTime ≤ payment_expires_at`; selain itu `REVIEW` tanpa tiket (kecuali duplikat `PAID`/`REVIEW`).
**Validates: Requirements 9.2, 9.3, 9.4, 9.8**

### Property 8: No release before payment certainty
Untuk setiap booking yang lewat deadline, stok hanya dilepas setelah resolve `NOT_PAID`/`NO_ATTEMPT` atau `now ≥ verification_until`; booking yang dibayar tepat waktu tidak pernah `EXPIRED`.
**Validates: Requirements 9.7, 9.9, 9.10, 15.3**

### Property 9: Server-authoritative timers
Untuk setiap request baca/refresh, `expiresAt` dan `paymentExpiresAt` tidak berubah; hanya `deadline-extension` (sekali) yang dapat memajukan `payment_expires_at`.
**Validates: Requirements 7.1, 7.5, 14.5**

### Property 10: Append-only & concurrency
Untuk dua command bersamaan pada stream yang sama, tepat satu berhasil pada versi v+1; event store tidak pernah berkurang dan replay read model menghasilkan `bookings` identik.
**Validates: Requirements 11.1, 11.2, 11.4**

### Property 11: Journey end exactly once
Untuk setiap admission, `booking.journey.ended.v1` ditulis ke outbox tepat sekali ketika journey berakhir.
**Validates: Requirements 12.4, 7.3**

### Property 12: Monotonic status
Untuk setiap booking, urutan status yang teramati adalah prefix dari salah satu jalur di §6.2; tidak ada transisi keluar dari `PAID`.
**Validates: Requirement 9.12**

## 15. Testing Strategy

| Level | Cakupan | TC |
|-------|---------|----|
| Unit (testify, table-driven, mock `_mock/`) | `decide()` setiap command, pemetaan phase, peta error, proyeksi dari event, kalkulasi units/zona | — |
| Integration (`-tags integration`, PG/Valkey/RabbitMQ nyata) | Property 1–12; T1–T22 SDD §4.7.1 sebagai test Go | TC-BKG-04..07/12, TC-ADM-01..05, TC-EDT-01..06, TC-SES-05..08, TC-CFG-01..04, TC-PAY-03/04/06..09, TC-ES-01..05 |
| Contract | `booking.yaml`, `internal-booking.yaml`, schema event | TC-API-01/02 |
| Load | 100 hold paralel / 50 tiket; 500 slot aktif | TC-BKG-07, TC-PERF-04 |
| Coverage | logika ≥ 80% (`cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan) | — |

## 16. Referensi

- SDD-RBT-001 §4.4–4.10 · SDD-BOOKING-001 · ADR-002/006/007/013/018/019/020 · HLD §4.4.3–4.4.4
- Kontrak: `docs/contracts/openapi/{booking,internal-booking,internal-payment}.yaml`, `docs/contracts/events/*.schema.json`
- Steering: `.kiro/steering/{structure,code-principles,tech,security-privacy,invariants,testing-review}.md`
- Util: `pkg/{eventstore,apperror,httpx,postgres,valkey,rabbitmq,migrate,token,crypto,resilience,nik,outbox,telemetry}`
