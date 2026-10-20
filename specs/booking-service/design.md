# Design — Booking Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-BOOKING-001.md`, `docs/adr/05-ADR-RBT.md` (ADR-019 event sourcing, ADR-020 login opsional) |
| **Service** | `services/booking` (port 8082, schema `booking`, role `svc_booking`) |
| **Arsitektur** | Hexagonal (ports & adapters) + event sourcing untuk agregat Booking |
| **Kontrak API** | `docs/contracts/openapi/booking.yaml` (publik), `internal-booking.yaml` (internal) |
| **Kontrak event** | `docs/contracts/events/*.schema.json` + `envelope.schema.json` |
| **Skema otoritatif** | `deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` |

## 1. Ringkasan & Status

Booking Service adalah **otoritas tunggal stok tiket dan uang**. Desain menggabungkan **hexagonal
architecture** (port inbound/outbound + adapter — per `[[architecture-hexagonal-ports-adapters]]`)
dengan **event sourcing** (ADR-019): siklus booking adalah log event append-only; read model `bookings`
diproyeksikan **sinkron di transaksi yang sama**; stok tetap relasional dan diubah dalam transaksi yang
sama, sehingga invarian "tidak oversell" ditegakkan oleh CHECK DB, **bukan** oleh replay.

**Status implementasi (audit 2026-10-20):**
- ✅ **Terimplementasi:** skema DB penuh + trigger invarian (`0001_init.up.sql`, `0002_event_store.up.sql`),
  CLI (`api`/`worker`/`migrate`/`healthcheck`), config fail-fast, `GET /health`, composition root
  (`internal/delivery/{server,worker,serve,clients}.go`).
- ❌ **Boilerplate / belum dibangun:** seluruh domain bisnis (katalog, detail, sesi, admission, hold,
  edit, settlement, tiket, SSE, riwayat), usecase, adapter PostgreSQL/RabbitMQ/HTTP domain, worker nyata
  (`internal/delivery/worker.go` → `jobs(*Clients) []Job { return nil }`), konsumen event, publisher outbox.

Desain ini adalah **target**; bagian yang belum ada ditandai *(belum)*. Nama-nama field mengikuti **kode
migrasi** (lebih kaya dari prosa SDD §6) — delta ditandai **Δ** (lihat requirements.md).

## 2. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` dan SDD §4. Lapisan ditegakkan `depguard`; antar-service dicegah
Go `internal/`.

```
                         ┌───────────────────── inbound adapters ─────────────────────┐
  HTTP (net/http)   ──►  │  infrastructure/http    (handler publik + internal, DTO)     │
  RabbitMQ consumer ──►  │  infrastructure/rabbitmq (payment.succeeded / payment.failed) │
  Worker ticker     ──►  │  infrastructure/worker   (sweeper, outbox relay, rekonsiliasi)│
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/inbound (I<Usecase>)
                         ┌───────────────────── application/usecase ──────────────────┐
                         │  satu usecase per file; keputusan domain murni + port       │
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/outbound (I<Repo/Gateway/UoW>)
                         ┌───────────────────── outbound adapters ────────────────────┐
  PostgreSQL (pgx)  ◄──  │  infrastructure/postgres  (event store, repos, IUnitOfWork)  │
  RabbitMQ (amqp)   ◄──  │  infrastructure/rabbitmq  (publisher via outbox relay)       │
  Payment (HTTP)    ◄──  │  infrastructure/paymentclient (resolve)                      │
  Valkey            ◄──  │  infrastructure/valkey    (cache ketersediaan SSE)           │
                         └─────────────────────────────────────────────────────────────┘
```

**Aturan (`code-principles.md`):** Accept interfaces, return structs (konstruktor → tipe konkret,
usecase menerima port); interface berawalan `I`, hanya di `port/`/`pkg/`; satu usecase per file; handler
selalu balas DTO (mapper), tidak pernah entity; logika Lua hanya di Queue.

### 2.1 Struktur paket (target)
```
internal/booking/
  port/inbound/     IEventCatalogUsecase, IEventDetailUsecase, ISessionUsecase, IAttendeeCheckUsecase,
                    IHoldUsecase, IEditUsecase, ICancelUsecase, ISettlementUsecase, IExpiryUsecase,
                    ITicketUsecase, IBookingHistoryUsecase, IAllocationUsecase, ISupplyUsecase
  port/outbound/    IEventStore, IBookingRepo, IStockRepo, IAdmissionRepo, IQuotaRepo, IOutboxWriter,
                    IIdempotencyStore, IPaymentClient, IAvailabilityCache, IUnitOfWork
  domain/entity/    Event, Category, Inventory, Supply, Admission, Booking, BookingItem, Ticket + state rules
  domain/valueobject NIK (hash/mask/enc), Money (minor IDR), TTL, Version, AdmissionStatus, BookingStatus
  domain/dto/       request/response publik + internal (cocok kontrak OpenAPI)
  domain/mapper/    entity ↔ DTO
  application/usecase satu file per usecase (decide() murni + orkestrasi UoW)
  infrastructure/http      handler publik & internal + Register(mux)
  infrastructure/postgres  event store + repo + IUnitOfWork (pgx tx)
  infrastructure/rabbitmq  consumer (payment.*) + publisher (relay outbox)
  infrastructure/valkey    cache ketersediaan untuk SSE
  infrastructure/healthcheck  ✅ sudah ada
```

---

## 3. API Endpoints

Semua respons memakai amplop standar `{ meta, data }` (`Meta{ code, message?, requestId?, errorCode?,
details? }`); error memakai `ErrorEnvelope{ meta (errorCode terisi), data: null }`. Error **tidak pernah**
memuat SQL, stack trace, secret, atau payload provider.

**Security schemes:** `visitorCookie` (`rb_visitor`, web HttpOnly), `visitorHeader` (`X-Visitor-Token`,
mobile), `userBearer` (JWT EdDSA User Service), `ticketLink` (query `t` dari email), `serviceBearer`
(JWT EdDSA antar service, ADR-011), `adminBearer` (role=admin).

### 3.1 Publik — Katalog

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| GET | `/api/v1/events` | — (publik) | Daftar event + filter, cache 2s |
| GET | `/api/v1/events/{eventId}` | — (publik) | Detail event + kategori, cache 2s |

**`GET /api/v1/events`** — query: `timeframe` ∈ {ALL,THIS_WEEK,THIS_MONTH,UPCOMING} (default ALL),
`city` (string), `status` ∈ {ALL,NOT_STARTED,AVAILABLE,SOLD_OUT,CLOSED} (default ALL), `sort` ∈
{DATE_ASC,PRICE_ASC,PRICE_DESC} (default DATE_ASC).
→ `200 EventList{ events: EventSummary[], serverTime }`.
`EventSummary{ eventId:uuid, slug, name, venue, startsAt, salesStartsAt, salesEndsAt, status∈{NOT_STARTED,AVAILABLE,SOLD_OUT,CLOSED}, minPriceMinor:int64≥0 }`.

**`GET /api/v1/events/{eventId}`** → `200 EventDetail{ event:EventSummary, maxTicketsPerOrder:int,
maxTicketsPerBuyerNik:int, allowMixedCategories:bool, categories:[{ categoryId:uuid, code, name,
priceMinor:int64≥0, available:int }] }`. Error: `404 NOT_FOUND`.

### 3.2 Publik — Sesi (Booking Session)

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| POST | `/api/v1/booking-sessions` | visitor | Buat/pulihkan sesi dari admissionToken |
| GET | `/api/v1/booking-sessions/current?eventId=` | visitor | Pulihkan sesi/pesanan aktif (refresh/back) |
| GET | `/api/v1/booking-sessions/{sessionId}` | visitor | Sisa waktu + ketersediaan live (fallback SSE) |
| GET | `/api/v1/booking-sessions/{sessionId}/stream` | visitor | SSE availability + timer |
| POST | `/api/v1/booking-sessions/{sessionId}/attendee-checks` | visitor | Cek NIK pemegang saat diketik |
| POST | `/api/v1/booking-sessions/{sessionId}/cancel` | visitor | Keluar tanpa membeli (204) |

**`POST /api/v1/booking-sessions`** — body `CreateSessionRequest{ admissionToken:string }`.
→ `201 BookingSession`. Error: `403 ADMISSION_TOKEN_INVALID`, `410 TURN_MISSED`/`SESSION_EXPIRED`.

`BookingSession{ sessionId:uuid, eventId:uuid, phase∈{SELECTING,EDITABLE,LOCKED,ENDED}, reason?,
reservedUnits:int, maxSelectable:int, buyerNikMasked?, allowMixedCategories?, expiresAt, serverTime,
submitGraceSeconds?, selectionWarningSeconds?, editEnabled?, maxEdits?, categories:[{categoryId, code,
name, priceMinor, available}], booking: Booking|null }`.

**`GET /api/v1/booking-sessions/current?eventId={uuid}`** → `200 BookingSession`. Error: `404 NOT_FOUND`
(tidak ada sesi aktif), `429 RATE_LIMITED` (header `Retry-After`).

**`GET /api/v1/booking-sessions/{sessionId}`** → `200 BookingSession`. Error: `410 SESSION_EXPIRED`.

**`GET /api/v1/booking-sessions/{sessionId}/stream`** (`Accept: text/event-stream`) → `200`
stream event `availability` dan `timer`; heartbeat tiap `SSE_HEARTBEAT_SECONDS`. Error: `503
DEPENDENCY_UNAVAILABLE` (batas koneksi SSE instance → klien beralih polling). **(Δ8: event `timer` bukan
`session`.)**
```
event: availability
data: {"categories":[{"categoryId":"...","available":49}],"serverTime":"..."}

event: timer
data: {"expiresAt":"...","serverTime":"...","phase":"SELECTING"}
```

**`POST /api/v1/booking-sessions/{sessionId}/attendee-checks`** — body `AttendeeCheckRequest{ nik:
^[0-9]{16}$ }`. → `200 AttendeeCheck{ result∈{AVAILABLE,ALREADY_REGISTERED,CHECK_LIMIT_REACHED},
checksRemaining:int }`. Error: `422 NIK_INVALID` (jatah tidak berkurang), `429 RATE_LIMITED`.

**`POST /api/v1/booking-sessions/{sessionId}/cancel`** → `204` (alokasi dilepas ke pengantre berikutnya).

### 3.3 Publik — Booking & Tiket

| Method | Path | Auth | Ringkas |
|--------|------|------|---------|
| POST | `/api/v1/bookings` | visitor | Hold (Idempotency-Key wajib) |
| GET | `/api/v1/bookings/{bookingId}` | visitor / ticketLink | Status booking |
| PUT | `/api/v1/bookings/{bookingId}/items` | visitor | Ubah pesanan sebelum bayar (Δ2) |
| POST | `/api/v1/bookings/{bookingId}/cancel` | visitor | Batal sebelum bayar (SHOULD) |
| GET | `/api/v1/bookings/{bookingId}/tickets` | visitor / ticketLink / userBearer | E-tiket (setelah PAID, Δ3) |
| GET | `/api/v1/me/bookings` | userBearer | Riwayat pesanan pembeli login (ADR-020) |

**`POST /api/v1/bookings`** — header `Idempotency-Key` (16..64), body `HoldRequest{ sessionId:uuid,
items[1..3]{ categoryId:uuid, attendee{ nik:^[0-9]{16}$, fullName:1..100 } } }`. → `201 Booking`.
Error: `400 VALIDATION_ERROR`; `409 ATTENDEE_NIK_ALREADY_REGISTERED | ATTENDEE_NIK_DUPLICATE_IN_ORDER |
CATEGORY_UNAVAILABLE | BUYER_QUOTA_EXHAUSTED | IDEMPOTENCY_KEY_REUSED`; `410 SESSION_EXPIRED`; `422
QUANTITY_EXCEEDS_LIMIT | MIXED_CATEGORIES_NOT_ALLOWED | NIK_INVALID`; `503 DEPENDENCY_UNAVAILABLE`.

`Booking{ bookingId:uuid, publicRef (mis. RB-00912), status∈{PENDING_PAYMENT,VERIFYING,PAID,EXPIRED,
CANCELLED,REVIEW}, amountMinor:int64≥0, currency:"IDR", paymentExpiresAt, serverTime, version:int,
canEdit:bool, editableUntil?, editsRemaining?, lockReason?∈{PAYMENT_STARTED,SELECTION_EXPIRED,
BOOKING_ENDED,EDIT_DISABLED,null}, paymentPageUrl?, items:[{ itemId:uuid, categoryId:uuid,
attendeeNikMasked, attendeeName }] }`.

**`PUT /api/v1/bookings/{bookingId}/items`** — header `Idempotency-Key`, body `ReplaceItemsRequest{
version:int, items[1..3]{ categoryId, attendee: ({nik,fullName} | {keepFromItemId}) } }`.
→ `200 Booking` (`paymentExpiresAt` **tidak berubah**). Error: `409 BOOKING_LOCKED |
BOOKING_VERSION_CONFLICT | EDIT_LIMIT_REACHED | CATEGORY_UNAVAILABLE | ATTENDEE_NIK_ALREADY_REGISTERED`;
`422 QUANTITY_EXCEEDS_LIMIT`.

**`GET /api/v1/bookings/{bookingId}`** → `200 Booking`. Error: `404 NOT_FOUND`.

**`POST /api/v1/bookings/{bookingId}/cancel`** → `200 Booking`. Error: `409 BOOKING_LOCKED`.

**`GET /api/v1/bookings/{bookingId}/tickets`** → `200 TicketList{ bookingId, tickets:[{ ticketCode,
categoryCode, attendeeName, attendeeNikMasked, qrPayload }] }`. Error: `409 BOOKING_NOT_PAYABLE`.

**`GET /api/v1/me/bookings`** — query `cursor?`, `limit` (1..50, default 20). → `200 MyBookings{
bookings:[{ bookingId, publicRef, eventName, status, amountMinor, createdAt }], nextCursor:string|null }`.
Strict ownership `account_id = token.sub`. Error: `401 UNAUTHORIZED`.

### 3.4 Internal (dipanggil Queue & Payment; `serviceBearer`, `X-Request-ID`)

| Method | Path | Pemanggil | Ringkas |
|--------|------|-----------|---------|
| POST | `/internal/v1/allocations:batch` | Queue | Reservasi alokasi batch admission |
| POST | `/internal/v1/allocations/{allocationId}:release` | Queue | Lepas alokasi yatim (saga, ADR-018) |
| GET | `/internal/v1/events/{eventId}/supply` | Queue | Ringkasan stok (Status Document) |
| GET | `/internal/v1/bookings/{bookingId}/payable` | Payment | Data tagihan |
| POST | `/internal/v1/bookings/{bookingId}/payment-lock` | Payment | Kunci pesanan sebelum attempt (Δ7) |
| POST | `/internal/v1/bookings/{bookingId}/deadline-extension` | Payment | Perpanjang sekali saat gateway gangguan |

**`POST /internal/v1/allocations:batch`** — body `AllocationBatchRequest{ eventId:uuid, entries:[{
queueEntryId:uuid, seq:int, visitorId:uuid, buyerNikHash (hex HMAC), buyerNikMasked, buyerEmail,
accountId:uuid|null }] }`. → `200 AllocationBatchResponse{ results:[{ queueEntryId, outcome∈{GRANTED,
NO_SUPPLY_NOW,SOLD_OUT,BUYER_QUOTA_EXHAUSTED,ALREADY_ACTIVE}, allocationId?, units?, claimDeadline? }] }`.
Idempoten per `queueEntryId`.

**`POST /internal/v1/allocations/{allocationId}:release`** — body `ReleaseRequest{ reason:"QUEUE_ENTRY_GONE" }`.
→ `200 ReleaseResponse{ released:bool }` (kompensasi saga — per `[[saga-pattern]]`).

**`GET /internal/v1/events/{eventId}/supply`** → `200 Supply{ free, allocated, held, sold, total,
maxPerOrder, saleState∈{NOT_STARTED,OPEN,CLOSED}, soldOut:bool, version:int, categories:[{ code, name,
capacity, available }] }`. **(Lebih kaya dari prosa SDD §4.2.)**

**`GET /internal/v1/bookings/{bookingId}/payable`** → `200 Payable{ bookingId, status, amountMinor:int64,
paymentExpiresAt, visitorId?, buyerEmail?, version:int, canEdit?:bool }`.

**`POST /internal/v1/bookings/{bookingId}/payment-lock`** — body `PaymentLockRequest{ attemptKey:string }`
(idempoten per `attemptKey`). → `200 PaymentLockResponse{ outcome∈{OK,BOOKING_NOT_PAYABLE,PAYMENT_EXPIRED},
version?, amountMinor?, paymentExpiresAt? }`. Mengisi `bookings.payment_started_at`, mengunci pesanan
(status menuju `VERIFYING` saat attempt berjalan), menolak edit berikutnya (`BOOKING_LOCKED`).

**`POST /internal/v1/bookings/{bookingId}/deadline-extension`** — body `DeadlineExtensionRequest{
reason:"PROVIDER_UNAVAILABLE", seconds:1..600 }`. → `200 DeadlineExtensionResponse{ paymentExpiresAt,
extended:bool }`. Memakai `bookings.deadline_extended_at` (sekali).

### 3.5 Internal — Called (Booking → Payment)
- `POST /internal/v1/payments/{bookingId}/resolve` → Payment: cek/batalkan di gateway saat booking expired
  (lihat expiry sweeper §7.2). Kontrak di `internal-payment.yaml`.

### 3.6 Internal — Admin (seed/testing; SDD App. C; **bukan** back-office, tidak lewat ALB publik)
- `POST /internal/v1/admin/events` (`adminBearer`) — `AdminEventCreateRequest{ name, venue, city,
  eventDate, saleStart, saleEnd, imageUrl?, bannerUrl?, categories[]{code,name,price,capacity},
  config:EventConfig }` → `201 { eventId }`.
- `PATCH /internal/v1/admin/events/{eventId}` — ubah config **sebelum** `rulesLockedAt`; `409` bila terkunci.
- `POST /internal/v1/admin/events/{eventId}/images` (`multipart/form-data`: `image` ≤5MB JPEG/PNG/WebP,
  `type`∈{poster,banner,thumbnail}) → `201 ImageUploadResponse{ imageUrl, type }`.
- `EventConfig{ maxTicketsPerOrder=3, maxTicketsPerBuyerNik=3, selectionTtlSeconds=300,
  paymentTtlSeconds=600, claimWindowSeconds=180, allowMixedCategories=true }`.

### 3.7 Ops
- `GET /health` → `200 Health{status:UP,...}` / `503 Health{status:DOWN,...}` (ALB + container, D-23). ✅

---

## 4. Model Data (otoritatif: migrasi)

### 4.1 PostgreSQL (schema `booking`)

| Tabel | Peran | Kolom kunci | Invarian (CHECK/trigger/index) |
|-------|-------|-------------|--------------------------------|
| `events` | katalog + config | `slug UNIQUE`, `status`, `max_tickets_per_order` (1..3), `max_tickets_per_buyer_nik` (1..3), `selection_ttl_seconds` (60..1800), `payment_ttl_seconds` (120..3600), `claim_window_seconds` (60..600), `max_attendee_checks_per_session` (3..50), `allow_mixed_categories`, `selection_edit_enabled`, `max_selection_edits` (0..10), `selection_submit_grace_seconds`, `selection_warning_seconds`, `rules_locked_at` | CHECK silang (`per_order ≤ per_buyer_nik`, grace/warning < ttl, `payment_ttl ≥ selection_ttl`, `sales_ends > sales_starts`); trigger `guard_event_rules`/`lock_rules_on_insert` (lock saat AVAILABLE, tolak ubah aturan terkunci → `EVENT_RULES_LOCKED`); trigger `audit_event_config` → `config_audit` |
| `ticket_categories` | kategori | `code`, `name`, `price_minor`≥0, `currency='IDR'`, `display_order` | `UNIQUE(event_id,code)`, `UNIQUE(event_id,id)` |
| `category_inventories` | stok per kategori | `capacity`≥0, `held_quantity`≥0, `sold_quantity`≥0 | **CHECK `held_quantity + sold_quantity ≤ capacity`** (anti-oversell), `UNIQUE(event_id,category_id)` |
| `event_supply` | stok tingkat event | `total_capacity`≥0, `allocated_units`≥0, `held_units`≥0, `sold_units`≥0, `version` (optimistic) | **CHECK `allocated+held+sold ≤ total_capacity`**; constraint trigger **deferred** `check_supply_matches_categories` → `total_capacity = Σ category.capacity` (`SUPPLY_CATEGORY_MISMATCH`) |
| `admissions` | alokasi + sesi | `queue_entry_id UNIQUE`, `queue_seq`>0, `visitor_id`, `buyer_nik_hash`, `buyer_email`, `buyer_nik_masked`, `units` (1..3), `status`, `admission_jti UNIQUE`, `attendee_checks`, `edit_count`, snapshot `cfg_*`, `reserved_at`/`claimed_at`/`expires_at`/`locked_at`/`ended_at`, `lock_reason`, `end_reason`, `account_id` | `uq_admission_active_visitor` & `uq_admission_active_buyer` (parsial, anti-calo); `idx_admissions_due`; CHECK state machine (`active ⇔ ended_at IS NULL`, `CONVERTED ⇔ lock_reason`, `edit_count ≤ cfg_max_edits`) |
| `attendee_checks` | dedupe cek NIK | PK `(admission_id, attendee_nik_hash)` | cek ulang NIK sama tidak tambah batas |
| `buyer_quotas` | kuota pembeli | `held_quantity`, `paid_quantity`, `max_quantity` (1..3) | **CHECK `held + paid ≤ max_quantity`** (invarian #3) |
| `bookings` | read model booking | `public_ref UNIQUE`, `admission_id UNIQUE`, `status`, `amount_minor`≥0, `item_quantity` (1..3), `version`, `payment_expires_at`, `payment_started_at`, `deadline_extended_at`, `verification_until`, `settlement_time`, `paid_at`, `ended_at`, `account_id` | `idx_bookings_due` (PENDING/VERIFYING), `idx_bookings_visitor`, `idx_bookings_account`; CHECK (`PAID ⇒ paid_at & settlement_time`, `VERIFYING ⇒ verification_until`) |
| `booking_items` | 1 baris/tiket | `unit_price_minor`, `attendee_nik_hash`, `attendee_nik_enc` (AES-256-GCM), `attendee_nik_masked`, `attendee_name` (2..100), `item_status` | `uq_attendee_nik_per_event` (HELD/PAID — "1 tiket 1 NIK"), `uq_booking_item_nik_active` (per booking), FK `(event_id,category_id)` |
| `tickets` | e-tiket | `public_code UNIQUE`, `booking_item_id UNIQUE` | 1:1 item |
| `booking_events` | **event store** | PK `(stream_id, version>0)`, `event_type ~ '^[A-Z][A-Za-z]+$'`, `schema_version`, `payload jsonb` (tanpa PII), `metadata` (correlation/causation/actor), `occurred_at`, `global_seq` IDENTITY | **append-only** triggers (`forbid_event_mutation` tolak UPDATE/DELETE/TRUNCATE); `uq_...global_seq`; konflik `(stream_id,version)` = SQLSTATE 23505 |
| `outbox_events` | outbox | `routing_key`, `aggregate_id`, `schema_version`, `payload`, `available_at`, `published_at`, `publish_attempts`, `last_error` | `idx_booking_outbox_pending` (parsial `published_at IS NULL`) |
| `idempotency_records` | dedupe request | PK `(scope, owner_id, idempotency_key)`, `request_hash`, `state`∈{IN_PROGRESS,COMPLETED}, `response_status`, `response_body`, `expires_at` | `idx_booking_idem_expiry` |
| `config_audit` | audit config | `event_id`, `changed_by` (`app.actor`), `field`, `old_value`→`new_value` | diisi trigger `audit_event_config` |

**Status enum aktual (Δ):** `bookings.status` ∈ {PENDING_PAYMENT, VERIFYING, PAID, EXPIRED, CANCELLED,
REVIEW}; `admissions.status` ∈ {RESERVED, ACTIVE, EDITABLE, CONVERTED, EXPIRED, RELEASED};
`booking_items.item_status` ∈ {HELD, PAID, RELEASED, REPLACED}.

### 4.2 Valkey (cache SSE + rate limit)

Otoritas stok tetap PostgreSQL; Valkey hanya cache/limit. Prefix `VALKEY_PREFIX_ENV`.

| Pola kunci | Tipe | TTL | Guna |
|------------|------|-----|------|
| `booking:avail:{eventId}` | hash/json | `SSE_CACHE_INTERVAL_MS` | snapshot ketersediaan kategori untuk SSE (refresh berkala dari PG) |
| `booking:sse:conn:{instance}` | counter | sesi | batas koneksi SSE per instance (503 `DEPENDENCY_UNAVAILABLE` saat penuh) |
| `booking:rl:check:{sessionId}` | counter | window | rate limit attendee-check per sesi |
| `booking:rl:session:{visitorId}:{eventId}` | counter | window | rate limit `GET current` (→ 429 `RATE_LIMITED`) |

---

## 5. Event Sourcing (ADR-019)

### 5.1 Tipe event (stream = `bookings.id`)

| event_type | Dipicu oleh | Versi mulai | Proyeksi read model | Stok | Outbox |
|------------|-------------|-------------|---------------------|------|--------|
| `BookingHeld` | Hold (R6) | v1 | `bookings`(PENDING_PAYMENT) + `booking_items`(HELD) | category/supply/quota `held += n` | — (outbox hanya stok/uang signifikan; `booking.quota.exhausted`/`booking.sold_out` ditulis saat terdeteksi) |
| `BookingItemsReplaced` | Edit (R8) | v+1 | item lama `REPLACED`, baru `HELD`, `version++` | penyesuaian held antar kategori | — |
| `BookingPaid` | `payment.succeeded` (R9) | v+1 | `bookings`(PAID) + `tickets` + item `PAID` | `held → sold` (category/supply/quota) | `booking.paid`, `booking.journey.ended(PAID)` |
| `BookingExpired` | expiry sweeper (R9) | v+1 | `bookings`(EXPIRED) | `held → free` | `booking.expired`, `booking.journey.ended(EXPIRED)` |
| `BookingReview` | expiry + resolve menemukan bayar (R9) | v+1 | `bookings`(REVIEW) | tahan (tak dilepas) | `booking.review` |

> **Catatan Δ:** prosa SDD §7 juga menyebut `BookingLocked`, `PaymentVerifying`, `BookingCancelled`.
> DB hanya membatasi format `event_type` (`^[A-Z][A-Za-z]+$`), jadi tipe tambahan sah; task ini mencakup 5
> tipe inti di atas. `payment-lock` memproyeksikan `bookings.payment_started_at`/`VERIFYING` (boleh tanpa
> event terpisah atau dengan `BookingLocked` sesuai implementasi).

### 5.2 Skema event store & versioning
`booking_events(stream_id, version, event_type, schema_version, payload, metadata, occurred_at,
global_seq)`. `version` naik per stream (optimistic concurrency via PK unik). `schema_version` menaungi
evolusi payload (per `[[versioning-and-compatibility]]` — tambah field opsional, jangan ubah makna).
`global_seq` (IDENTITY) memberi urutan global untuk replay/proyeksi ulang.

### 5.3 Aturan replay & proyeksi
- **Proyeksi sinkron:** setiap command memproyeksikan read model di transaksi yang sama — read model tak
  pernah tertinggal dari event (bukan eventual).
- **Replay/rebuild:** read model dapat dibangun ulang dengan memutar event per `global_seq`; **stok tidak
  di-replay** (relasional, sumber kebenaran sendiri), sehingga rebuild read model aman tanpa risiko oversell.
- **Append-only:** `UPDATE`/`DELETE`/`TRUNCATE` ditolak DB — jejak audit immutable (TC-ES-01).
- **Privasi:** payload tanpa NIK plaintext/email/token (hanya hash/masked) — ditolak `pkg/eventstore`.

---

## 6. State Machines

### 6.1 Admission (`admissions.status`)
```
                claim session (POST /booking-sessions)        payment-lock
  RESERVED ───────────────────────────────────► ACTIVE ───(hold)──► EDITABLE ──────────► CONVERTED
     │ claim window habis                           │ TTL habis          │ TTL habis / disabled   (lock_reason terisi)
     ▼ (end_reason=UNCLAIMED)                        ▼                    ▼
  EXPIRED/RELEASED                            EXPIRED/RELEASED      EXPIRED/RELEASED
                                              (SESSION_EXPIRED)     (LEFT_WITHOUT_BUYING)
```
CHECK: `(status∈{RESERVED,ACTIVE,EDITABLE}) = (ended_at IS NULL)`; `(status=CONVERTED) = (lock_reason IS NOT NULL)`;
`edit_count ≤ cfg_max_edits`. `lock_reason`∈{PAYMENT_STARTED,SELECTION_EXPIRED,EDIT_DISABLED,BOOKING_ENDED};
`end_reason`∈{UNCLAIMED,SESSION_EXPIRED,LEFT_WITHOUT_BUYING,CONVERTED}. **(Δ6: tidak ada status `ENDED`.)**

### 6.2 Booking (`bookings.status`)
```
  PENDING_PAYMENT ──(payment-lock / attempt)──► VERIFYING ──(payment.succeeded)──► PAID
        │                                            │
        │ payment_expires_at < now                   │ payment_expires_at < now (resolve)
        ▼                                            ▼
     EXPIRED ◄────(resolve: tidak ada bayar)────────┤
     REVIEW  ◄────(resolve: ada bayar di gateway)────┘
     CANCELLED ◄──(cancel sebelum lock)
```
CHECK: `PAID ⇒ paid_at & settlement_time`; `VERIFYING ⇒ verification_until`. **(Δ1.)**

### 6.3 Booking Item (`booking_items.item_status`)
```
  HELD ──(edit)──► REPLACED            HELD ──(settlement)──► PAID            HELD ──(expire/cancel)──► RELEASED
```
`uq_attendee_nik_per_event` & `uq_booking_item_nik_active` hanya berlaku untuk {HELD, PAID}.

---

## 7. Worker Jobs

Loop `internal/delivery/worker.go` menjalankan `jobs()` tiap `WORKER_INTERVAL` *(saat ini `nil` —
boilerplate)*. Target job:

### 7.1 Session Sweeper (admission expiry)
```
SELECT * FROM admissions WHERE status IN ('RESERVED','ACTIVE','EDITABLE') AND expires_at < now()  -- idx_admissions_due
FOR EACH: tx →
  RESERVED kadaluarsa  → status RELEASED, end_reason=UNCLAIMED, allocated_units -= units
  ACTIVE/EDITABLE habis → status EXPIRED, end_reason=SESSION_EXPIRED/LEFT_WITHOUT_BUYING,
                          lepas held (category/supply/quota), append BookingExpired (bila ada booking),
                          outbox booking.expired + booking.journey.ended
```

### 7.2 Expiry Sweeper (booking expiry + resolve)
```
SELECT * FROM bookings WHERE status IN ('PENDING_PAYMENT','VERIFYING') AND payment_expires_at < now()  -- idx_bookings_due
FOR EACH:
  jika payment_started_at IS NOT NULL → POST /internal/v1/payments/{id}/resolve (ke Payment)
     resolve: tidak ada bayar → tx: append BookingExpired + held→free + outbox booking.expired(+journey.ended EXPIRED)
     resolve: ada bayar       → tx: append BookingReview  + status REVIEW + outbox booking.review  (bukan oversell)
  jika belum mulai bayar      → tx: append BookingExpired + held→free + outbox booking.expired
```

### 7.3 Outbox Relay
```
SELECT * FROM outbox_events WHERE published_at IS NULL ORDER BY available_at  -- idx_booking_outbox_pending
FOR EACH: publish envelope ke exchange rusuh.events (topic, routing_key) →
  sukses → SET published_at = now()
  gagal  → publish_attempts++, last_error=..., retry siklus berikut (available_at backoff)
```
Transactional Outbox per `[[transactional-outbox]]`: at-least-once ke broker; konsumen hilir idempoten.

### 7.4 Rekonsiliasi (TC-OPS-01, SRS-F-90-01)
Periodik membandingkan `Σ held/sold` read model vs counter stok vs event store; inkonsistensi → alert.

---

## 8. Event Contracts

Semua event dibungkus **envelope** (`envelope.schema.json`): `{ messageId:uuid, eventType, schemaVersion≥1,
occurredAt, producer∈{queue-service,booking-service,payment-service}, aggregateId, correlationId?, payload }`.
Payload tanpa PII plaintext (hanya `ticketLinkToken` diizinkan untuk email).

### 8.1 Dikonsumsi (producer: payment-service)

| Event | Payload | Aksi | Idempotensi |
|-------|---------|------|-------------|
| `payment.succeeded` | `{ bookingId:uuid, attemptId:uuid, settlementTime, amountMinor:int64, method }` | Settlement: held→sold, terbit tiket, `BookingPaid`, outbox `booking.paid` | guard `status != PAID` + `ON CONFLICT DO NOTHING` (tanpa inbox) |
| `payment.failed` | `{ bookingId:uuid, attemptId:uuid, reason }` | Catat saja; booking menunggu TTL | no-op idempoten |

Konsumen: retry berjenjang `RABBITMQ_RETRY_DELAYS=5s,30s,2m`, DLQ setelah `RABBITMQ_MAX_RETRIES=3`
(per `[[at-least-once-processing]]`, `[[decision-dead-letter-queue-vs-capped-retry-and-continue]]`);
payload rusak → `rabbitmq.Permanent(err)` langsung DLQ.

### 8.2 Dipublikasikan (producer: booking-service)

| Routing key | Consumer | Payload (contract) |
|-------------|----------|--------------------|
| `booking.journey.ended` | Queue | `{ eventId:uuid, queueEntryId:uuid, outcome∈{PAID,EXPIRED,CANCELLED,LEFT_WITHOUT_BUYING,UNCLAIMED} }` |
| `booking.quota.exhausted` | Queue | `{ eventId:uuid, buyerNikHash }` |
| `booking.sold_out` | Queue | `{ eventId:uuid }` |
| `booking.paid` | Notification | `{ bookingId:uuid, publicRef, buyerEmail, event?{eventId,name,startsAt,venue}, tickets[]{ticketCode,attendeeName,attendeeNikMasked,categoryCode}, ticketLinkToken }` |
| `booking.expired` | Notification | `{ bookingId:uuid, publicRef, buyerEmail, reason∈{PAYMENT_TIMEOUT,SESSION_EXPIRED,CANCELLED} }` |
| `booking.review` | Notification / Payment | `{ bookingId:uuid, attemptHint?, buyerEmail, amountMinor:int64 }` |

---

## 9. Atomic Operations (IUnitOfWork)

### 9.1 Hold — urutan lock tetap: supply → category → quota → booking
```
BEGIN
  1) SELECT ... FOR UPDATE event_supply WHERE event_id=$e          -- lock supply dulu (cegah deadlock)
  2) SELECT ... FOR UPDATE category_inventories WHERE category_id IN (...)  -- lalu kategori
  3) SELECT ... FOR UPDATE buyer_quotas WHERE (event,buyer_nik_hash)        -- lalu kuota
  4) append booking_events (BookingHeld, v1)                       -- optimistic: UNIQUE(stream_id,version)
     proyeksi bookings(PENDING_PAYMENT) + booking_items(HELD)      -- booking terakhir
     UPDATE category.held_quantity += n; supply.held_units += n; quota.held_quantity += n
     [outbox bila perlu: quota.exhausted / sold_out]
COMMIT   -- CHECK held+sold≤capacity & allocated+held+sold≤total_capacity menolak oversell → rollback → 409 CATEGORY_UNAVAILABLE
```
Urutan lock **selalu sama** di semua command → bebas deadlock. ErrConcurrency (23505) → re-read + retry
(maks 3) per `[[idempotency-and-dedup]]` / `[[retry-and-backoff]]`.

### 9.2 Settlement — held → sold atomik (idempoten)
```
BEGIN
  SELECT booking FOR UPDATE; IF status = PAID THEN COMMIT; RETURN  -- guard idempoten
  append booking_events (BookingPaid, v+1)
  UPDATE bookings SET status=PAID, paid_at=.., settlement_time=..
  UPDATE booking_items SET item_status=PAID WHERE item_status=HELD
  INSERT tickets (public_code, ...) ON CONFLICT (booking_item_id) DO NOTHING
  UPDATE category: held_quantity -= n, sold_quantity += n
  UPDATE supply:   held_units -= n,    sold_units += n
  UPDATE buyer_quotas: held_quantity -= n, paid_quantity += n
  INSERT outbox_events (booking.paid), (booking.journey.ended outcome=PAID)
COMMIT
```

### 9.3 Edit — penggantian atomik (tanpa reset TTL)
```
BEGIN
  SELECT booking FOR UPDATE; IF payment_started_at IS NOT NULL → 409 BOOKING_LOCKED
  IF req.version != booking.version → 409 BOOKING_VERSION_CONFLICT
  IF admission.edit_count >= cfg_max_edits → 409 EDIT_LIMIT_REACHED
  append booking_events (BookingItemsReplaced, v+1)
  UPDATE booking_items SET item_status=REPLACED WHERE id IN (lama)
  INSERT booking_items (baru, HELD)   -- cek uq_attendee_nik_per_event
  penyesuaian held antar kategori (supply/category/quota)   -- CHECK anti-oversell
  UPDATE bookings SET version=v+1 (payment_expires_at TIDAK berubah); admissions.edit_count++
COMMIT
```

---

## 10. Sequence Diagrams

### 10.1 Admission (reservasi — Jangan PHP)
```
Queue → Booking: POST /internal/v1/allocations:batch { entries[] }
Booking → PG: BEGIN
Booking → PG:   (idempoten per queue_entry_id) supply.allocated += units; INSERT admissions(RESERVED, cfg snapshot)
Booking → PG: COMMIT  (CHECK allocated+held+sold ≤ total_capacity)
Booking → Queue: 200 { results[]: GRANTED + allocationId + claimDeadline }  → Queue terbitkan admissionToken
```

### 10.2 Buka sesi & Hold
```
Client → Booking: POST /booking-sessions { admissionToken }
Booking → PG: admission RESERVED→ACTIVE (admission_jti one-time)
Booking → Client: 201 BookingSession { expiresAt, serverTime, categories }
Client → Booking: POST /bookings (Idempotency-Key) { sessionId, items[] }
Booking → Booking: validasi NIK + kuota + max/order + mixed
Booking → PG: BEGIN  lock supply→category→quota→booking
Booking → PG:   append BookingHeld + proyeksi PENDING_PAYMENT/HELD + held += n + [outbox]
Booking → PG: COMMIT  (CHECK oversell → rollback → 409 CATEGORY_UNAVAILABLE)
Booking → Client: 201 Booking { status=PENDING_PAYMENT, paymentExpiresAt, version=1 }
```

### 10.3 Edit (ubah pesanan sebelum bayar)
```
Client → Booking: PUT /bookings/{id}/items (Idempotency-Key) { version, items[] }
Booking → PG: SELECT booking FOR UPDATE
alt payment_started_at != null → 409 BOOKING_LOCKED
alt version != current         → 409 BOOKING_VERSION_CONFLICT
alt edit_count >= max          → 409 EDIT_LIMIT_REACHED
Booking → PG: append BookingItemsReplaced + item lama REPLACED + item baru HELD + version++ + edit_count++
Booking → Client: 200 Booking (paymentExpiresAt tidak berubah)
```

### 10.4 Settlement (dari Payment)
```
Payment → RabbitMQ: payment.succeeded { bookingId, attemptId, settlementTime, amountMinor, method }
RabbitMQ → Booking(consumer): deliver
Booking → PG: SELECT booking FOR UPDATE
alt status == PAID → ack (idempoten, no-op)
Booking → PG: append BookingPaid + status PAID + INSERT tickets + held→sold + INSERT outbox(booking.paid, journey.ended)
Booking → RabbitMQ: ack
OutboxRelay → RabbitMQ: publish booking.paid → Notification
```

### 10.5 Expiry (sweeper + resolve)
```
Worker(expiry) → PG: SELECT bookings WHERE status IN (PENDING_PAYMENT,VERIFYING) AND payment_expires_at < now
loop each booking:
  alt payment_started_at != null:
    Booking → Payment: POST /internal/v1/payments/{id}/resolve
    alt resolve = NO_PAYMENT → PG: append BookingExpired + held→free + outbox(booking.expired, journey.ended EXPIRED)
    alt resolve = PAID_FOUND → PG: append BookingReview + status REVIEW + outbox(booking.review)
  else:
    Booking → PG: append BookingExpired + held→free + outbox(booking.expired)
```

---

## 11. Error Catalog (domain → HTTP via `pkg/apperror`)

Error bertipe dipetakan di `infrastructure/http`; handler selalu balas `ErrorEnvelope` (DTO), tidak pernah
entity. Error **tidak pernah** memuat SQL/stack/secret/payload provider.

| Error domain | errorCode | HTTP | Pemicu | Endpoint |
|--------------|-----------|------|--------|----------|
| ErrValidation | `VALIDATION_ERROR` | 400 | field tidak valid | semua |
| ErrNotFound | `NOT_FOUND` | 404 | resource/sesi tidak ada | detail, get session/booking, me/bookings |
| ErrNIKInvalid | `NIK_INVALID` | 422 | struktur NIK salah (jatah cek tak berkurang) | attendee-checks, hold |
| ErrQuantityExceeds | `QUANTITY_EXCEEDS_LIMIT` | 422 | item > maxTicketsPerOrder | hold, edit |
| ErrMixedNotAllowed | `MIXED_CATEGORIES_NOT_ALLOWED` | 422 | mixed category dilarang | hold |
| ErrCategoryUnavailable | `CATEGORY_UNAVAILABLE` | 409 | CHECK held+sold>capacity / supply habis | hold, edit |
| ErrBuyerQuota | `BUYER_QUOTA_EXHAUSTED` | 409 | kuota pembeli habis | hold |
| ErrNikRegistered | `ATTENDEE_NIK_ALREADY_REGISTERED` | 409 | `uq_attendee_nik_per_event` | hold, edit |
| ErrNikDuplicate | `ATTENDEE_NIK_DUPLICATE_IN_ORDER` | 409 | NIK duplikat dalam order | hold |
| ErrIdemReused | `IDEMPOTENCY_KEY_REUSED` | 409 | key sama, request_hash beda | hold, edit |
| ErrBookingLocked | `BOOKING_LOCKED` | 409 | edit/cancel setelah payment_started_at | edit, cancel |
| ErrVersionConflict | `BOOKING_VERSION_CONFLICT` | 409 | version tidak cocok | edit |
| ErrEditLimit | `EDIT_LIMIT_REACHED` | 409 | edit_count ≥ cfg_max_edits | edit |
| ErrNotPayable | `BOOKING_NOT_PAYABLE` | 409 | tiket diminta sebelum PAID | get tickets |
| ErrSessionExpired | `SESSION_EXPIRED` | 410 | > admissions.expires_at | sesi, hold |
| ErrTurnMissed | `TURN_MISSED` | 410 | claim deadline terlampaui | create session |
| ErrRateLimited | `RATE_LIMITED` | 429 | rate limit (Retry-After) | get current, attendee-checks |
| ErrAdmissionToken | `ADMISSION_TOKEN_INVALID` | 403 | token salah/dipakai ulang | create session |
| ErrVisitorMismatch | `VISITOR_MISMATCH` | 403 | visitor beda | sesi |
| ErrUnauthorized | `UNAUTHORIZED` | 401 | access token tidak valid | me/bookings |
| ErrDependency | `DEPENDENCY_UNAVAILABLE` | 503 | DB/broker/SSE sementara gagal | hold, SSE stream |
| ErrConcurrency | *(internal, retry)* | — | konflik versi event store (23505) | semua command ES |
| *(DB)* | `EVENT_RULES_LOCKED` | 409 | ubah aturan setelah lock (admin) | admin update |
| *(DB)* | `SUPPLY_CATEGORY_MISMATCH` | — | supply ≠ Σ kategori saat COMMIT (seed) | seed/admin |

---

## 12. Konfigurasi (`internal/config`, D-24, fail-fast)

Divalidasi saat start (`Config.Validate()` ✅). Dari `.env.example`:

| Kelompok | Variabel |
|----------|----------|
| App | `APP_ENV` (local/dev/staging/prod), `APP_NAME=booking`, `APP_PORT=8082` |
| Log | `LOG_LEVEL`, `LOG_FORMAT=json` |
| HTTP/Worker | `HTTP_SHUTDOWN_TIMEOUT=15s`, `HEALTH_TIMEOUT=2s`, `WORKER_INTERVAL=5s` (semua > 0) |
| Postgres | `POSTGRES_HOST/PORT/USER=svc_booking/PASSWORD/DATABASE=rbt/SSLMODE/SCHEMA=booking/MAX_CONNS` |
| Valkey | `VALKEY_SERVER` (JSON array), `VALKEY_USERNAME/PASSWORD/TLS/DB/PREFIX_ENV` |
| RabbitMQ | `RABBITMQ_URL/EXCHANGE=rusuh.events/EXCHANGE_TYPE=topic/WORKERS/PREFETCH/MAX_RETRIES=3/RETRY_DELAYS=5s,30s,2m/CONFIRM_TIMEOUT=5s` |

*Target tambahan (SDD §10, belum di `config.go`):* `PAYMENT_SERVICE_URL`, `BOOKING_TOKEN_PRIVATE_KEY_PATH`,
`TOKEN_PUBLIC_KEY_PATH` (verifikasi admissionToken EdDSA), `SSE_ENABLED`, `SSE_CACHE_INTERVAL_MS`,
`SSE_HEARTBEAT_SECONDS`. TTL/batas per-event diambil dari DB (snapshot admission), bukan env. Secret lewat
env/SSM; `.env` tidak di-commit (`security-privacy.md`).

---

## 13. Resilience, Observability & Testing

**Resilience (`code-principles.md` + standar):** consumer `payment.succeeded` idempoten tanpa inbox
(guard state + `ON CONFLICT DO NOTHING`); retry berjenjang + DLQ; payload rusak → `rabbitmq.Permanent`;
kegagalan Notifikasi tidak membatalkan transaksi (invarian #6, via outbox async).

**Observability (SDD §11):** metrik `rbt_booking_sessions_total{event_id,status}`,
`rbt_booking_holds_total{event_id,status}`, `rbt_booking_settlements_total{event_id}`,
`rbt_booking_expirations_total{event_id,reason}`, `rbt_booking_stock_gauge{event_id,type}`,
`rbt_booking_event_store_version{aggregate_id}`. Alert: `BookingStockNegative` (Critical),
`BookingExpiryRateHigh` (>20%/10m), `BookingOutboxLag` (>30s/5m), `BookingDBLatencyHigh` (p99>100ms/5m).
Log slog JSON, tanpa NIK/email/token.

**Testing (`testing-review.md`, `[[testing-strategy]]`):**
- **Unit** testify table-driven, mock `_mock/` (mockgen): `decide()` murni, peta error, validasi NIK, proyeksi.
- **Integration** (tag `integration`, PG/Valkey/RabbitMQ nyata): **TC-BKG-07** 100 hold paralel pada 50
  tiket → tepat 50 (no oversell); TC-ES-01..05 append-only/replay/konflik versi; TC-ADM-01/02/05 reservasi
  + Jangan PHP; TC-BKG-01..03/11 kuota & NIK unik; TC-CFG-01 snapshot config; TC-PAY-03/04/06..08 settlement
  idempoten + expiry/resolve/REVIEW; consumer kirim ganda.
- **Contract** TC-API-01/02 terhadap `booking.yaml` + `internal-booking.yaml` + `events/*.schema.json`.
- **Coverage** logika ≥ 80% (`make cover`); `cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan.

---

## 14. Build (run) & Verify

```bash
# Run (lokal)
make infra keys
cd services/booking && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &            # HTTP :8082
go run ./cmd worker &         # sweeper + outbox relay (saat dibangun)
curl localhost:8082/health    # 200 UP

# Verify
make build && make test && make test-integration && make cover && make lint && make isolation && make ci
```

## 15. Referensi

- LLD: `docs/sdd/SDD-BOOKING-001.md` · ADR-019 event sourcing, ADR-020 login · `docs/adr/05-ADR-RBT.md`
- Kontrak: `docs/contracts/openapi/{booking,internal-booking}.yaml`, `docs/contracts/events/*.schema.json`, `envelope.schema.json`
- HLD §4.4.3 (Selection Session), §4.4.4 (Hold & Settlement)
- Steering: `.kiro/steering/{structure,code-principles,tech,security-privacy,invariants,testing-review}.md`
- Wiki: `[[architecture-hexagonal-ports-adapters]]`, `[[transactional-outbox]]`, `[[idempotency-and-dedup]]`,
  `[[at-least-once-processing]]`, `[[retry-and-backoff]]`, `[[saga-pattern]]`,
  `[[decision-dead-letter-queue-vs-capped-retry-and-continue]]`, `[[versioning-and-compatibility]]`,
  `[[event-driven-worker]]`, `[[data-and-migrations]]`
- Util lintas service: `pkg/{eventstore,apperror,httpx,postgres,valkey,rabbitmq,migrate,token,crypto}`
