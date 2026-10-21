# Tasks — Booking Service

| Field | Value |
|-------|-------|
| Service | Booking Service (otoritas tunggal stok & uang) |
| Port | 8082 |
| Schema / Role | `booking` / `svc_booking` |
| Spec | `specs/booking-service/{requirements,design}.md` |
| LLD | `docs/sdd/SDD-BOOKING-001.md`, ADR-019 (event sourcing), ADR-020 (login opsional) |
| Architecture | Hexagonal (ports & adapters) + event sourcing (agregat Booking) |
| Status | DB schema + triggers + CLI + health + composition root DONE; **all business logic is TARGET** |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

Setiap command stok/uang = **satu transaksi PostgreSQL** (`pkg/postgres.WithTx`): append event
(`pkg/eventstore`) + proyeksi read model + update counter stok + (bila perlu) tulis `outbox_events`.
Invarian "tidak oversell" ditegakkan **CHECK DB**, bukan replay.

---

## What ALREADY exists (do not re-create)

| Area | Evidence |
|------|----------|
| **DB schema penuh + trigger invarian** | `services/booking/deployments/migrations/0001_init.up.sql`, `0002_event_store.up.sql` (embed via `embed.go`) |
| Anti-oversell CHECK | `category_inventories CHECK (held_quantity + sold_quantity <= capacity)`; `event_supply CHECK (allocated_units + held_units + sold_units <= total_capacity)` |
| Anti-calo index | `uq_admission_active_visitor`, `uq_admission_active_buyer`, `uq_attendee_nik_per_event`, `uq_booking_item_nik_active` |
| Event store append-only | `booking_events` PK `(stream_id, version)`, triggers `trg_booking_events_append_only` / `trg_booking_events_no_truncate` (ERRCODE `restrict_violation`) |
| Config lock + audit | triggers `guard_event_rules` / `lock_rules_on_insert` (`EVENT_RULES_LOCKED`), `audit_event_config` → `config_audit` |
| Supply = Σ kategori | constraint trigger deferred `check_supply_matches_categories` (`SUPPLY_CATEGORY_MISMATCH`) |
| Outbox table | `outbox_events(routing_key, aggregate_id, schema_version, payload, available_at, published_at, publish_attempts, last_error)` + partial index `idx_booking_outbox_pending` |
| Idempotency table | `idempotency_records` PK `(scope, owner_id, idempotency_key)` + `request_hash`, `state`, `response_status`, `response_body` |
| CLI `api`/`worker`/`migrate`/`healthcheck` | `services/booking/cmd/*.go` (Cobra) |
| Config fail-fast | `internal/config/{config.go,load.go}` (`Config.Validate()`) |
| `GET /health` + checker | `internal/booking/infrastructure/http/health_handler.go`, `.../healthcheck/checker.go`, `internal/delivery/clients.go` |
| Composition root | `internal/delivery/{server.go,worker.go,serve.go,clients.go}` (`NewHandler`, `NewClients`, `jobs(*Clients) []Job { return nil }`) |
| Shared pkg | `pkg/eventstore` (`Store.Append/Load/Replay`, `ErrConcurrency`, `ErrForbiddenField`), `pkg/postgres` (`WithTx`, `IsCode`, `ConstraintName`, SQLSTATE consts), `pkg/rabbitmq` (`Publisher.Publish`, `Envelope`, consumer, `Permanent`), `pkg/httpx` (`DecodeJSON`, `BearerToken`, `Chain`), `pkg/apperror`, `pkg/clock`, `pkg/token`, `pkg/crypto` |

## What is MISSING (this spec builds it)

All domain `entity`/`valueobject`/`dto`/`mapper` beyond `health`; every `port/inbound` & `port/outbound`
interface beyond `IHealthUsecase` / `dependency_checker`; every `application/usecase` beyond `check_health`;
all `infrastructure/{postgres,rabbitmq,valkey,paymentclient,http}` adapters beyond health; the three real
worker jobs (session sweeper, expiry sweeper, outbox relay) wired into `jobs(*Clients)`.

---

## Pre-requisites

- [ ] Boilerplate compiles: `cd services/booking && go build ./...`
- [ ] Infra up: `make infra keys`
- [ ] Env: `cp .env.example .env`
- [ ] Migrations: `go run ./cmd migrate up` (embed, advisory lock)
- [ ] Health: `go run ./cmd api &` then `curl localhost:8082/health` → `200 UP`

### pkg/eventstore quick reference (used by Tasks 6, 8, 9, 11)

```go
st, _ := eventstore.New("booking_events")               // table must end with _events
// inside a pgx.Tx (tx satisfies eventstore.DB):
newVer, err := st.Append(ctx, tx, streamID /* = bookings.id */, expectedVersion, eventstore.NewEvent{
    Type:          "BookingHeld",                        // ^[A-Z][A-Za-z]+$
    SchemaVersion: 1,
    Payload:       heldPayload,                           // NO nik/email/token → ErrForbiddenField
    Metadata:      map[string]string{"correlationId": cid, "actor": "booking-service"},
})
// 23505 on (stream_id, version) → eventstore.ErrConcurrency → re-read + retry (max 3)
```

### pkg/postgres quick reference (transaction + error mapping)

```go
err := pg.WithTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error { ... })
postgres.IsCode(err, postgres.CodeCheckViolation)   // "23514" → CATEGORY_UNAVAILABLE / rules locked
postgres.IsCode(err, postgres.CodeUniqueViolation)  // "23505" → NIK registered / idem reuse / ErrConcurrency
postgres.ConstraintName(err)                        // disambiguate which unique index fired
```

---

## Task 1: Domain Core — Value Objects, Entities, Error Catalog (R11, cross-cutting)

**Traces to:** Requirement 11, SDD §6, §11 (Error Catalog) · foundation for all tasks

### Acceptance Criteria
- [ ] AC1.1: `BookingStatus` ∈ {PENDING_PAYMENT, VERIFYING, PAID, EXPIRED, CANCELLED, REVIEW}; `AdmissionStatus` ∈ {RESERVED, ACTIVE, EDITABLE, CONVERTED, EXPIRED, RELEASED}; `ItemStatus` ∈ {HELD, PAID, RELEASED, REPLACED} — names match migration CHECKs exactly
- [ ] AC1.2: `NIK` value object computes `Hash` (HMAC → `bytea`), `Masked` (`3174********0001`), `Enc` (AES-256-GCM via `pkg/crypto`); **never** exposes plaintext in String()/JSON
- [ ] AC1.3: `NIK.Validate()` enforces `^[0-9]{16}$`, province `[0:2]` ∈ 01..99, birth-day `[6:8]` ≤31 or 41..71 (Lampiran A.1) → `ErrNIKInvalid`
- [ ] AC1.4: `Money` is minor-unit IDR (`int64 ≥ 0`); `Version` is monotonic int
- [ ] AC1.5: Every domain error in design §11 constructed via `pkg/apperror` with exact `errorCode` + HTTP status (table below)

### Error Catalog (domain error → errorCode → HTTP) — authoritative

| Error var | errorCode | HTTP |
|-----------|-----------|------|
| `ErrValidation` | `VALIDATION_ERROR` | 400 |
| `ErrNotFound` | `NOT_FOUND` | 404 |
| `ErrUnauthorized` | `UNAUTHORIZED` | 401 |
| `ErrAdmissionToken` | `ADMISSION_TOKEN_INVALID` | 403 |
| `ErrVisitorMismatch` | `VISITOR_MISMATCH` | 403 |
| `ErrNIKInvalid` | `NIK_INVALID` | 422 |
| `ErrQuantityExceeds` | `QUANTITY_EXCEEDS_LIMIT` | 422 |
| `ErrMixedNotAllowed` | `MIXED_CATEGORIES_NOT_ALLOWED` | 422 |
| `ErrCategoryUnavailable` | `CATEGORY_UNAVAILABLE` | 409 |
| `ErrBuyerQuota` | `BUYER_QUOTA_EXHAUSTED` | 409 |
| `ErrNikRegistered` | `ATTENDEE_NIK_ALREADY_REGISTERED` | 409 |
| `ErrNikDuplicate` | `ATTENDEE_NIK_DUPLICATE_IN_ORDER` | 409 |
| `ErrIdemReused` | `IDEMPOTENCY_KEY_REUSED` | 409 |
| `ErrBookingLocked` | `BOOKING_LOCKED` | 409 |
| `ErrVersionConflict` | `BOOKING_VERSION_CONFLICT` | 409 |
| `ErrEditLimit` | `EDIT_LIMIT_REACHED` | 409 |
| `ErrNotPayable` | `BOOKING_NOT_PAYABLE` | 409 |
| `ErrSessionExpired` | `SESSION_EXPIRED` | 410 |
| `ErrTurnMissed` | `TURN_MISSED` | 410 |
| `ErrRateLimited` | `RATE_LIMITED` | 429 |
| `ErrDependency` | `DEPENDENCY_UNAVAILABLE` | 503 |

### Files to create
- `internal/booking/domain/valueobject/booking_status.go`, `admission_status.go`, `item_status.go`
- `internal/booking/domain/valueobject/nik.go` (`type NIK struct`; `func ParseNIK(raw string, hmacKey, aesKey []byte) (NIK, error)`; `Hash() []byte`, `Masked() string`, `Enc() []byte`)
- `internal/booking/domain/valueobject/money.go`, `version.go`
- `internal/booking/domain/entity/{event.go,category.go,inventory.go,supply.go,admission.go,booking.go,booking_item.go,ticket.go}` (+ pure state-rule methods, e.g. `Admission.IsActive()`, `Booking.CanEdit()`)
- `internal/booking/domain/booking_errors.go` (all error vars above, built with `apperror.New`)

### Tests
- [ ] `valueobject/nik_test.go` — `TestNIK_Validate`, `TestNIK_MaskNeverLeaksPlaintext`, `TestNIK_HashDeterministic`
- [ ] `valueobject/booking_status_test.go` — `TestBookingStatus_Transitions`
- [ ] `entity/admission_test.go` — `TestAdmission_IsActive`, `TestAdmission_LockReasonInvariant`

### Verify
```bash
cd services/booking && go test -race ./internal/booking/domain/...
```

---

## Task 2: Event Catalog — `GET /api/v1/events` (R1)

**Traces to:** Requirement 1, SRS-F-02/F-28 · TC-EVT-01..03, TC-CAT-01..06

### Acceptance Criteria
- [ ] AC2.1: Returns `{ meta, data:{ events[], serverTime } }`; `EventSummary{ eventId, slug, name, venue, startsAt, salesStartsAt, salesEndsAt, status, minPriceMinor }`
- [ ] AC2.2: `timeframe` ∈ {ALL,THIS_WEEK,THIS_MONTH,UPCOMING} filters `starts_at` range
- [ ] AC2.3: `city` filters case-insensitive on venue/city
- [ ] AC2.4: `status` ∈ {ALL,NOT_STARTED,AVAILABLE,SOLD_OUT,CLOSED}
- [ ] AC2.5: `sort` ∈ {DATE_ASC(default),PRICE_ASC,PRICE_DESC}
- [ ] AC2.6: Response `Cache-Control: public, max-age=2`; endpoint public (`security: []`)

### Files
- `domain/dto/event_catalog.go` (`EventListQuery`, `EventSummary`, `EventList`)
- `port/inbound/event_catalog_usecase.go` — `IEventCatalogUsecase interface { ListEvents(ctx, EventListQuery) (dto.EventList, error) }`
- `port/outbound/event_repo.go` — `IEventRepo interface { ListEvents(ctx, EventListQuery) ([]entity.Event, error); GetDetail(ctx, eventID string) (entity.EventDetail, error) }`
- `infrastructure/postgres/event_repo.go` — read-only query (joins `events` + `ticket_categories` for `minPriceMinor`)
- `application/usecase/list_events.go`
- `infrastructure/http/catalog_handler.go` + `Register(mux)`; wire in `internal/delivery/server.go` `NewHandler`

### Key SQL
```sql
SELECT e.id, e.slug, e.name, e.venue, e.starts_at, e.sales_starts_at, e.sales_ends_at, e.status,
       COALESCE(MIN(c.price_minor), 0) AS min_price_minor
  FROM booking.events e
  LEFT JOIN booking.ticket_categories c ON c.event_id = e.id
 WHERE ($1 = 'ALL' OR e.status = $1)
   AND ($2 = '' OR e.venue ILIKE '%'||$2||'%')
 GROUP BY e.id
 ORDER BY /* DATE_ASC: e.starts_at | PRICE_*: min_price_minor */;
```

### Tests
- [ ] Unit: `usecase/list_events_test.go` — `TestListEvents_FilterTimeframe`, `_FilterCity`, `_SortPriceAsc` (table-driven)
- [ ] Integration: `postgres/event_repo_integration_test.go` (tag `integration`)

### Verify
```bash
go test -race ./... && curl 'localhost:8082/api/v1/events?sort=PRICE_ASC'
```

---

## Task 3: Event Detail — `GET /api/v1/events/{eventId}` (R2)

**Traces to:** Requirement 2, SRS-F-03 · TC-EVT-04, TC-EVT-05

### Acceptance Criteria
- [ ] AC3.1: Returns `EventDetail{ event, maxTicketsPerOrder, maxTicketsPerBuyerNik, allowMixedCategories, categories[]{ categoryId, code, name, priceMinor, available } }`
- [ ] AC3.2: `available = capacity − held_quantity − sold_quantity` from PostgreSQL (authoritative; **not** Valkey)
- [ ] AC3.3: `eventId` not UUID → 400 `VALIDATION_ERROR`
- [ ] AC3.4: Event missing → 404 `NOT_FOUND`
- [ ] AC3.5: `Cache-Control: public, max-age=2`

### Files
- `domain/dto/event_detail.go`; `port/inbound/event_detail_usecase.go`; `application/usecase/get_event_detail.go`; handler method `getEventDetail` in `catalog_handler.go`

### Key SQL
```sql
SELECT c.id, c.code, c.name, c.price_minor, (ci.capacity - ci.held_quantity - ci.sold_quantity) AS available
  FROM booking.ticket_categories c
  JOIN booking.category_inventories ci ON ci.category_id = c.id
 WHERE c.event_id = $1 ORDER BY c.display_order;
```

### Tests
- [ ] Unit: `usecase/get_event_detail_test.go` — `TestGetEventDetail_AvailableMath`, `_NotFound`, `_InvalidUUID`

---

## Task 4: Admission & Allocations (Invarian #2 "Jangan PHP") (R3, R4)

**Traces to:** Requirement 3 & 4, SRS-F-04/F-06/F-09/F-26 · TC-ADM-01..05, TC-CFG-01..04

**Transactional boundary:** one tx per `allocations:batch` (all entries) — `pkg/postgres.WithTx`,
`pgx.TxOptions{IsoLevel: pgx.ReadCommitted}`.

### Acceptance Criteria
- [ ] AC4.1: `POST /internal/v1/allocations:batch` — per successful entry: `event_supply.allocated_units += units` **and** INSERT `admissions(RESERVED)` with config snapshot, one tx; reply `outcome` ∈ {GRANTED, NO_SUPPLY_NOW, SOLD_OUT, BUYER_QUOTA_EXHAUSTED, ALREADY_ACTIVE}
- [ ] AC4.2: Idempotent per `queueEntryId` (`admissions.queue_entry_id UNIQUE`); replay returns same result (23505 on that index → `ALREADY_ACTIVE` with existing row)
- [ ] AC4.3: One active admission per `(event_id, visitor_id)` and `(event_id, buyer_nik_hash)` — 23505 on `uq_admission_active_*` → `ALREADY_ACTIVE`
- [ ] AC4.4: Supply exhausted → `event_supply CHECK` fails (23514) → `SOLD_OUT`/`NO_SUPPLY_NOW`
- [ ] AC4.5: Config snapshot copied from `events` into admission cols `cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds, cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories`
- [ ] AC4.6: `POST /api/v1/booking-sessions` with one-time `admissionToken` (`admission_jti UNIQUE`) → RESERVED→ACTIVE, returns `BookingSession{ expiresAt, serverTime, phase, reservedUnits, maxSelectable, categories }`
- [ ] AC4.7: token invalid/reused → 403 `ADMISSION_TOKEN_INVALID`; turn missed → 410 `TURN_MISSED`/`SESSION_EXPIRED`
- [ ] AC4.8 (R4): while session runs, use **admission snapshot** values, not live event; changing locked rules → DB `EVENT_RULES_LOCKED` (check_violation) → 409 (admin)
- [ ] AC4.9 (`allocations/{id}:release`): saga compensation sets admission RELEASED + `allocated_units -= units`

### Files
- `domain/dto/allocation.go` (`AllocationBatchRequest/Response`, `ReleaseRequest/Response`), `session.go` (`CreateSessionRequest`, `BookingSession`)
- `port/inbound/{allocation_usecase.go,session_usecase.go}` — `IAllocationUsecase`, `ISessionUsecase`
- `port/outbound/admission_repo.go` — `IAdmissionRepo`; `event_repo.go` extension `LoadConfigForSnapshot`
- `infrastructure/postgres/admission_repo.go`
- `infrastructure/security/admission_token.go` — verify EdDSA `admissionToken` via `pkg/token`
- `application/usecase/{allocate_batch.go,release_allocation.go,create_session.go}`
- `infrastructure/http/{internal_allocation_handler.go,session_handler.go}` (internal uses `serviceBearer` + `X-Request-ID`)

### Key SQL (per entry, inside the batch tx)
```sql
-- 1) reserve supply (fails CHECK → SOLD_OUT)
UPDATE booking.event_supply
   SET allocated_units = allocated_units + $units, version = version + 1, updated_at = now()
 WHERE event_id = $eventId;                                       -- CHECK allocated+held+sold<=total_capacity
-- 2) reserve admission with config snapshot; UNIQUE conflicts → ALREADY_ACTIVE
INSERT INTO booking.admissions (
    event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email, buyer_nik_masked,
    units, status, attendee_checks, edit_count,
    cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
    cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
    reserved_at, expires_at, account_id)
SELECT $eventId, $queueEntryId, $seq, $visitorId, $nikHash, $email, $nikMasked,
       $units, 'RESERVED', 0, 0,
       e.selection_ttl_seconds, e.payment_ttl_seconds, e.selection_submit_grace_seconds,
       e.selection_edit_enabled, e.max_selection_edits, e.allow_mixed_categories,
       now(), now() + make_interval(secs => e.claim_window_seconds), $accountId
  FROM booking.events e WHERE e.id = $eventId;
```

### Tests
- [ ] Unit: `usecase/allocate_batch_test.go` — `TestAllocateBatch_OutcomeMapping` (GRANTED/SOLD_OUT/ALREADY_ACTIVE/QUOTA), `TestAllocateBatch_SnapshotCopied`
- [ ] Unit: `usecase/create_session_test.go` — `TestCreateSession_TokenReuseRejected`, `_TurnMissed`
- [ ] Integration: `postgres/admission_repo_integration_test.go` — `TestAdmission_IdempotentPerQueueEntry`, `TestAdmission_AntiCaloActiveVisitor` (TC-ADM-01/03)
- [ ] Integration: `TestAdmission_ReserveGuaranteesHold` (TC-ADM-02, Jangan PHP); `TestConfig_SnapshotIsolatesLiveChange` (TC-CFG-01)

---

## Task 5: NIK Validation & Buyer Quota — attendee-checks (Invarian #3) (R5)

**Traces to:** Requirement 5, SRS-F-11 · TC-BKG-01..03, TC-BKG-08..12

### Acceptance Criteria
- [ ] AC5.1: `POST /api/v1/booking-sessions/{sessionId}/attendee-checks` `{ nik }` → `AttendeeCheck{ result ∈ {AVAILABLE,ALREADY_REGISTERED,CHECK_LIMIT_REACHED}, checksRemaining }`
- [ ] AC5.2: Bad NIK structure → 422 `NIK_INVALID` **without** decrementing the check quota
- [ ] AC5.3: Re-checking same NIK in a session does not re-count (dedupe via PK `attendee_checks(admission_id, attendee_nik_hash)`)
- [ ] AC5.4: Unique checks > `max_attendee_checks_per_session` → `CHECK_LIMIT_REACHED`; next request → 429 `RATE_LIMITED`
- [ ] AC5.5: `held + paid + order_qty > max_quantity` (`buyer_quotas` ≤3) → 409 `BUYER_QUOTA_EXHAUSTED` (cross-session/device)
- [ ] AC5.6: Attendee NIK already active (HELD/PAID) in event → 409 `ATTENDEE_NIK_ALREADY_REGISTERED`
- [ ] AC5.7: Duplicate attendee NIK within one order → 409 `ATTENDEE_NIK_DUPLICATE_IN_ORDER`

### Files
- `domain/dto/attendee_check.go`; `port/inbound/attendee_check_usecase.go`; `port/outbound/{quota_repo.go,attendee_check_repo.go}`; `infrastructure/postgres/{quota_repo.go,attendee_check_repo.go}`; `application/usecase/check_attendee.go`; handler method in `session_handler.go`; `infrastructure/valkey/rate_limiter.go` (`booking:rl:check:{sessionId}`)

### Key SQL (dedupe insert — does not count if NIK already checked)
```sql
INSERT INTO booking.attendee_checks (admission_id, attendee_nik_hash)
VALUES ($admissionId, $nikHash)
ON CONFLICT (admission_id, attendee_nik_hash) DO NOTHING;   -- rows affected 0 ⇒ already counted
-- active-registration probe:
SELECT 1 FROM booking.booking_items
 WHERE event_id = $eventId AND attendee_nik_hash = $nikHash AND item_status IN ('HELD','PAID') LIMIT 1;
```

### Tests
- [ ] Unit: `usecase/check_attendee_test.go` — `TestCheckAttendee_InvalidNIKNoQuotaBurn`, `_DedupeSameNIK`, `_LimitReached`
- [ ] Integration: `TestBuyerQuota_CrossSessionEnforced` (TC-BKG-01), `TestAttendeeNIK_DuplicateInOrder` (TC-BKG-02), `TestAttendeeNIK_AlreadyRegistered` (TC-BKG-03)

---

## Task 6: Atomic Hold (Invarian #1 "Tidak Oversell") (R6) ⭐

**Traces to:** Requirement 6, SRS-F-12/Q-FUN-01 · TC-BKG-04..07, TC-PERF-04

**Transactional boundary:** entire hold = **one** `pkg/postgres.WithTx`. On `ErrConcurrency` (23505 on
`booking_events` PK) → re-read state + retry, **max 3** (`[[retry-and-backoff]]`).

**Lock ordering (deadlock prevention) — FIXED across ALL commands: supply → category → quota → booking.**
Every command that touches stock must `SELECT ... FOR UPDATE` in this exact order; never the reverse.

### Acceptance Criteria
- [ ] AC6.1: `POST /api/v1/bookings` with `Idempotency-Key` (16..64) + `HoldRequest{ sessionId, items[1..3]{ categoryId, attendee{ nik, fullName } } }` → create booking `PENDING_PAYMENT`, `category_inventories.held_quantity += n`, `event_supply.held_units += n`, `buyer_quotas.held_quantity += n`, INSERT `booking_items(HELD)` — one tx, lock order supply→category→quota→booking
- [ ] AC6.2: Idempotent per `(scope='hold', owner_id=visitorId, idempotency_key)`; identical replay returns stored `response_status`/`response_body`
- [ ] AC6.3: Same key, different `request_hash` → 409 `IDEMPOTENCY_KEY_REUSED`
- [ ] AC6.4: `held + sold` would exceed `capacity` → DB CHECK aborts tx → 409 `CATEGORY_UNAVAILABLE`. **TC-BKG-07: 100 parallel holds on last 50 tickets → exactly 50 succeed** (invarian #1)
- [ ] AC6.5: `items.length > maxTicketsPerOrder` (≤3) → 422 `QUANTITY_EXCEEDS_LIMIT`
- [ ] AC6.6: `cfg_allow_mixed_categories=false` & >1 category → 422 `MIXED_CATEGORIES_NOT_ALLOWED`
- [ ] AC6.7: Supply exhausted → 409 `CATEGORY_UNAVAILABLE`
- [ ] AC6.8: On success append `BookingHeld` (version 1) to `booking_events`, project `bookings`/`booking_items` in same tx
- [ ] AC6.9: Transient DB/broker failure → 503 `DEPENDENCY_UNAVAILABLE`; client retries with same key

### Files
- `domain/dto/hold.go` (`HoldRequest`, `Booking`), `domain/dto/booking_view.go`
- `port/inbound/hold_usecase.go` — `IHoldUsecase interface { Hold(ctx, HoldCommand) (dto.Booking, error) }`
- `port/outbound/unit_of_work.go` — `IUnitOfWork interface { Hold(ctx, HoldSpec) (HoldResult, error) }` (tx orchestrator; hides lock ordering)
- `port/outbound/{stock_repo.go,booking_repo.go,idempotency_store.go,event_store.go}` — `IStockRepo`, `IBookingRepo`, `IIdempotencyStore`, `IEventStore`
- `infrastructure/postgres/unit_of_work.go` — implements `IUnitOfWork` over `pkg/postgres.WithTx` + `pkg/eventstore.Store`
- `infrastructure/postgres/{stock_repo.go,booking_repo.go,idempotency_store.go}`
- `application/usecase/hold.go` — validation (`decide()`), idempotency guard, retry loop, UoW call
- `infrastructure/http/booking_handler.go` method `createBooking`

### SQL — lock ordering inside IUnitOfWork.Hold (critical)
```sql
BEGIN;  -- ReadCommitted

-- (1) SUPPLY first — single-row lock serializes concurrent holds per event
SELECT total_capacity, allocated_units, held_units, sold_units, version
  FROM booking.event_supply
 WHERE event_id = $eventId
   FOR UPDATE;

-- (2) CATEGORY rows — deterministic order by category_id to avoid cycles
SELECT category_id, capacity, held_quantity, sold_quantity
  FROM booking.category_inventories
 WHERE event_id = $eventId AND category_id = ANY($categoryIds::uuid[])
 ORDER BY category_id
   FOR UPDATE;

-- (3) QUOTA — buyer row
SELECT held_quantity, paid_quantity, max_quantity
  FROM booking.buyer_quotas
 WHERE event_id = $eventId AND buyer_nik_hash = $buyerNikHash
   FOR UPDATE;              -- upsert row first if absent (INSERT ... ON CONFLICT DO NOTHING)

-- (4) append event store (optimistic): PK (stream_id, version) → 23505 = ErrConcurrency
INSERT INTO booking.booking_events (stream_id, version, event_type, schema_version, payload, metadata)
VALUES ($bookingId, 1, 'BookingHeld', 1, $payload /* NO nik/email */, $meta);

-- (5) project read model (booking LAST in the order)
INSERT INTO booking.bookings (id, public_ref, event_id, admission_id, visitor_id, buyer_nik_hash,
       buyer_email, status, amount_minor, item_quantity, payment_expires_at, version, account_id)
VALUES ($bookingId, $publicRef, $eventId, $admissionId, $visitorId, $buyerNikHash, $email,
       'PENDING_PAYMENT', $amount, $n, now() + make_interval(secs => $cfgPaymentTtl), 1, $accountId);
INSERT INTO booking.booking_items (booking_id, event_id, category_id, unit_price_minor,
       attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
VALUES (...);              -- 23505 uq_attendee_nik_per_event → ATTENDEE_NIK_ALREADY_REGISTERED
                           -- 23505 uq_booking_item_nik_active → ATTENDEE_NIK_DUPLICATE_IN_ORDER

-- (6) bump counters — anti-oversell CHECKs validated here
UPDATE booking.category_inventories SET held_quantity = held_quantity + $cn WHERE category_id = $cid;  -- CHECK held+sold<=capacity
UPDATE booking.event_supply         SET held_units = held_units + $n       WHERE event_id = $eventId;  -- CHECK allocated+held+sold<=total_capacity
UPDATE booking.buyer_quotas         SET held_quantity = held_quantity + $n WHERE event_id=$eventId AND buyer_nik_hash=$buyerNikHash; -- CHECK held+paid<=max_quantity

COMMIT;  -- any CHECK (23514) → ROLLBACK → map to CATEGORY_UNAVAILABLE / BUYER_QUOTA_EXHAUSTED
```

### DB error → domain error mapping (in postgres adapter)
| Signal | Domain error |
|--------|--------------|
| `23514` on `category_inventories`/`event_supply` | `ErrCategoryUnavailable` (409) |
| `23514` on `buyer_quotas` | `ErrBuyerQuota` (409) |
| `23505` on `uq_attendee_nik_per_event` | `ErrNikRegistered` (409) |
| `23505` on `uq_booking_item_nik_active` | `ErrNikDuplicate` (409) |
| `23505` on `booking_events` PK | `eventstore.ErrConcurrency` → retry (≤3) |

### Idempotency protocol
```sql
INSERT INTO booking.idempotency_records (scope, owner_id, idempotency_key, request_hash, state, expires_at)
VALUES ('hold', $visitorId, $key, $hash, 'IN_PROGRESS', now() + interval '24h')
ON CONFLICT (scope, owner_id, idempotency_key) DO NOTHING;
-- 0 rows ⇒ existing: if request_hash<>$hash → IDEMPOTENCY_KEY_REUSED (409);
--                    if COMPLETED → replay stored response_status/response_body.
-- on success: UPDATE ... SET state='COMPLETED', response_status=201, response_body=$json.
```

### Tests
- [ ] Unit: `usecase/hold_test.go` — `TestHold_QuantityExceeds`, `_MixedNotAllowed`, `_IdempotentReplay`, `_IdempotencyKeyReused`, `_RetryOnConcurrency`
- [ ] **Integration (TC-BKG-07):** `infrastructure/postgres/hold_concurrency_integration_test.go` — `TestHold_NoOversell_100ParallelOn50` : seed capacity so only 50 remain, launch 100 goroutines (`sync.WaitGroup`, shared pool), assert exactly 50 succeed & 50 → `CATEGORY_UNAVAILABLE`, and final `held_quantity + sold_quantity == capacity` (never exceeds). Run with `-race`.
- [ ] Integration: `TestHold_DeadlockFree_MixedCategoryOrder` — concurrent holds across overlapping categories in random request order complete without `40P01` (proves fixed lock order)
- [ ] Integration: `TestHold_ProjectsEventAndReadModel` (TC-BKG-04)

### Verify
```bash
go test -race ./internal/booking/application/...
go test -tags integration -race -run TestHold_NoOversell ./internal/booking/infrastructure/postgres/...
```

---

## Task 7: Session, Timer & SSE (Invarian #4 "Server-Authoritative") (R7)

**Traces to:** Requirement 7, SRS-F-07/F-08 · TC-SES-01..08, TC-PAY-02

### Acceptance Criteria
- [ ] AC7.1: `admissions.expires_at` & `bookings.payment_expires_at` computed from server time + snapshot; client only displays `expiresAt`/`serverTime`
- [ ] AC7.2: `GET /booking-sessions/current?eventId=` and `GET /booking-sessions/{sessionId}` return `expiresAt`, `serverTime`, `phase`
- [ ] AC7.3: `payment_expires_at` computed from first hold; **not reset** by edit
- [ ] AC7.4: request after `expires_at` → 410 `SESSION_EXPIRED`
- [ ] AC7.5: SSE `GET /booking-sessions/{sessionId}/stream` emits `availability` + `timer` events, heartbeat every `SSE_HEARTBEAT_SECONDS`; connection cap reached → 503 `DEPENDENCY_UNAVAILABLE`

### Files
- `domain/dto/session_view.go`; `application/usecase/get_session.go`, `get_current_session.go`, `stream_session.go`; `infrastructure/http/{session_handler.go,sse_handler.go}`; `infrastructure/valkey/availability_cache.go` (`booking:avail:{eventId}`, `booking:sse:conn:{instance}`)
- (Sweeper that enforces expiry lives in Task 10.)

### Tests
- [ ] Unit: `usecase/get_session_test.go` — `TestSession_ReturnsServerTime`, `_ExpiredReturns410`
- [ ] Integration: `sse_integration_test.go` — `TestSSE_EmitsAvailabilityAndTimer`, `TestSSE_ConnCapReturns503`

---

## Task 8: Edit / Replace Items & Session Recovery (R8)

**Traces to:** Requirement 8, SRS-F-23/F-24/F-08-02 · TC-EDT-01..06, TC-NAV-01..03

**Transactional boundary:** one `WithTx`; lock order supply→category→quota→booking (same as hold).
`payment_expires_at` **unchanged**.

### Acceptance Criteria
- [ ] AC8.1: `PUT /api/v1/bookings/{bookingId}/items` `{ version, items[1..3] }` while `cfg_edit_enabled` & `payment_started_at IS NULL` → mark old items `REPLACED`, insert new `HELD`, `bookings.version++`, `admissions.edit_count++`, one tx; append `BookingItemsReplaced`; `paymentExpiresAt` unchanged
- [ ] AC8.2: `items[].attendee` MAY be new `{nik,fullName}` OR `{keepFromItemId}`
- [ ] AC8.3: `version` ≠ current → 409 `BOOKING_VERSION_CONFLICT`
- [ ] AC8.4: `payment_started_at` set → 409 `BOOKING_LOCKED`
- [ ] AC8.5: `edit_count ≥ cfg_max_edits` → 409 `EDIT_LIMIT_REACHED`
- [ ] AC8.6: `GET /booking-sessions/current?eventId=` after refresh recovers the same active session/booking (idempotent); none → 404 `NOT_FOUND`

### Files
- `domain/dto/replace_items.go`; `port/inbound/edit_usecase.go` (`IEditUsecase`); `application/usecase/replace_items.go`; extend `IUnitOfWork` with `ReplaceItems`; handler method `replaceItems` in `booking_handler.go`

### SQL (inside tx, after FOR UPDATE on booking)
```sql
SELECT version, payment_started_at FROM booking.bookings WHERE id = $bookingId FOR UPDATE;
-- guard: payment_started_at NOT NULL → BOOKING_LOCKED; version<>$req → BOOKING_VERSION_CONFLICT
UPDATE booking.booking_items SET item_status = 'REPLACED'
 WHERE booking_id = $bookingId AND item_status = 'HELD';
INSERT INTO booking.booking_items (...) VALUES (...);          -- new HELD rows
UPDATE booking.bookings SET version = version + 1, item_quantity = $n, updated_at = now()
 WHERE id = $bookingId;                                        -- payment_expires_at untouched
UPDATE booking.admissions SET edit_count = edit_count + 1 WHERE id = $admissionId;  -- CHECK edit_count<=cfg_max_edits
```

### Tests
- [ ] Unit: `usecase/replace_items_test.go` — `TestReplace_VersionConflict`, `_Locked`, `_EditLimit`, `_PaymentExpiryUnchanged`
- [ ] Integration: `TestReplace_AtomicSwapNoOversell` (TC-EDT-01), `TestSession_RecoveryIdempotent` (TC-NAV-01)

---

## Task 9: Settlement (payment.succeeded consumer) — Invarian #5 (R9, R10)

**Traces to:** Requirement 9 & 10, SRS-F-16/F-17/F-20 · TC-PAY-03, TC-TKT-01..03

**Transactional boundary:** one `WithTx` per delivered message. Idempotent via **guard state**
(`status != PAID`) + `ON CONFLICT DO NOTHING` — **no inbox table**.

### Acceptance Criteria
- [ ] AC9.1: `payment.succeeded{ bookingId, attemptId, settlementTime, amountMinor, method }` & `status != PAID` → one tx: `held→sold` (`category_inventories`, `event_supply`, `buyer_quotas`), items `HELD→PAID`, issue `tickets`, append `BookingPaid`, status `PAID` + set `paid_at`/`settlement_time`, write outbox `booking.paid` + `booking.journey.ended`
- [ ] AC9.1a: IF `paid_quantity >= max_quantity` after settlement, THEN publish `booking.quota.exhausted { eventId, buyerNikHash }` (OQ-13, TC-MSG-03)
- [ ] AC9.1b: IF `sold_units >= total_units` after settlement (SRS-F-10-01), THEN publish `booking.sold_out { eventId }` (OQ-13, TC-MSG-03)
- [ ] AC9.2: Idempotent — duplicate delivery does not double tickets (guard + `ON CONFLICT (booking_item_id) DO NOTHING`)
- [ ] AC9.3: `payment.failed{ bookingId, attemptId, reason }` → log only; booking waits for TTL
- [ ] AC9.4 (tickets): one `tickets` row per `booking_item` (`public_code UNIQUE`, 1:1 `booking_item_id`)
- [ ] AC9.5: `GET /api/v1/bookings/{bookingId}/tickets` & `PAID` → `TicketList`; not PAID → 409 `BOOKING_NOT_PAYABLE`
- [ ] AC9.6: `GET /api/v1/me/bookings` with `userBearer` → only `account_id = token.sub`, cursor pagination (`limit` 1..50, default 20); invalid token → 401 `UNAUTHORIZED`
- [ ] AC9.7: corrupt payload → `rabbitmq.Permanent(err)` → DLQ

### Files
- `port/inbound/{settlement_usecase.go,ticket_usecase.go,history_usecase.go}`; `port/outbound/{ticket_repo.go,outbox_writer.go}`; `infrastructure/postgres/{ticket_repo.go,outbox_writer.go}`; `infrastructure/rabbitmq/payment_consumer.go` (handles `payment.succeeded`/`payment.failed`); `application/usecase/{settle_payment.go,get_tickets.go,list_my_bookings.go}`; handler methods in `booking_handler.go`; register consumer in `RunWorker`

### SQL — settlement (held → sold, idempotent)
```sql
SELECT status, version FROM booking.bookings WHERE id = $bookingId FOR UPDATE;
-- IF status = 'PAID' → COMMIT, ack (no-op idempotent)
INSERT INTO booking.booking_events (stream_id, version, event_type, schema_version, payload, metadata)
VALUES ($bookingId, $version + 1, 'BookingPaid', 1, $payload, $meta);
UPDATE booking.bookings SET status='PAID', paid_at=$t, settlement_time=$t, version=$version+1 WHERE id=$bookingId;
UPDATE booking.booking_items SET item_status='PAID' WHERE booking_id=$bookingId AND item_status='HELD';
INSERT INTO booking.tickets (booking_id, booking_item_id, public_code)
SELECT booking_id, id, $code FROM booking.booking_items WHERE booking_id=$bookingId
ON CONFLICT (booking_item_id) DO NOTHING;                       -- idempotent ticket issue
UPDATE booking.category_inventories SET held_quantity=held_quantity-$cn, sold_quantity=sold_quantity+$cn WHERE category_id=$cid;
UPDATE booking.event_supply         SET held_units=held_units-$n, sold_units=sold_units+$n WHERE event_id=$eventId;
UPDATE booking.buyer_quotas         SET held_quantity=held_quantity-$n, paid_quantity=paid_quantity+$n WHERE event_id=$eventId AND buyer_nik_hash=$h;
INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
VALUES ('booking.paid', $bookingId, 1, $paidPayload), ('booking.journey.ended', $bookingId, 1, $endedPayload);
```

### Tests
- [ ] Unit: `usecase/settle_payment_test.go` — `TestSettle_HeldToSold`, `_PayloadNoPII`
- [ ] **Integration (idempotency):** `rabbitmq/payment_consumer_integration_test.go` — `TestSettle_DuplicateDeliveryNoDoubleTickets` (deliver `payment.succeeded` twice → exactly one ticket set) (TC-PAY-03)
- [ ] Integration: `TestTickets_RequirePaid` (TC-TKT-01), `TestMyBookings_StrictOwnership` (TC-USR-10)

---

## Task 10: Worker — Session Sweeper & Expiry Sweeper + Resolve (R7, R9)

**Traces to:** Requirement 7 & 9, SRS-F-07/F-17 · TC-SES-05..08, TC-PAY-04, TC-PAY-06..08

**Pattern:** register jobs in `internal/delivery/worker.go` `jobs(*Clients) []Job` (currently returns
`nil`); each job runs every `WORKER_INTERVAL`; each affected row handled in **its own** `WithTx`.

### Acceptance Criteria
- [ ] AC10.1: Session sweeper: `admissions WHERE status IN ('RESERVED','ACTIVE','EDITABLE') AND expires_at < now()` (uses `idx_admissions_due`) → RESERVED→RELEASED (`end_reason=UNCLAIMED`, `allocated_units -= units`); ACTIVE/EDITABLE→EXPIRED (`end_reason=SESSION_EXPIRED`/`LEFT_WITHOUT_BUYING`, release held, append `BookingExpired` if booking exists, outbox `booking.expired` + `booking.journey.ended`)
- [ ] AC10.2: Expiry sweeper: `bookings WHERE status IN ('PENDING_PAYMENT','VERIFYING') AND payment_expires_at < now()` (uses `idx_bookings_due`)
- [ ] AC10.3: if `payment_started_at IS NOT NULL` → `POST /internal/v1/payments/{bookingId}/resolve` (Payment)
- [ ] AC10.4: resolve = NO_PAYMENT → EXPIRED, `held→free`, append `BookingExpired`, outbox `booking.expired` (`reason` ∈ {PAYMENT_TIMEOUT,SESSION_EXPIRED,CANCELLED}) (TC-PAY-04)
- [ ] AC10.5: resolve = PAID_FOUND → REVIEW, append `BookingReview`, outbox `booking.review` — **not** oversell, **not** auto-cancel (TC-PAY-06..08, RISK-T-03)

### Files
- `port/inbound/{session_sweeper.go,expiry_sweeper.go}` (`ISessionSweeper`, `IExpirySweeper`); `port/outbound/payment_client.go` (`IPaymentClient.Resolve`); `infrastructure/paymentclient/client.go` (uses `pkg/httpx` + `serviceBearer`); `application/usecase/{sweep_sessions.go,sweep_expiries.go}`; register in `jobs(*Clients)` and set `delivery.Need{Postgres:true, RabbitMQ:true}` for worker verb

### SQL
```sql
-- session sweeper candidate scan
SELECT id, event_id, status, units, expires_at FROM booking.admissions
 WHERE status IN ('RESERVED','ACTIVE','EDITABLE') AND expires_at < now();   -- idx_admissions_due
-- expiry sweeper candidate scan
SELECT id, event_id, status, payment_started_at FROM booking.bookings
 WHERE status IN ('PENDING_PAYMENT','VERIFYING') AND payment_expires_at < now(); -- idx_bookings_due
```

### Tests
- [ ] Unit: `usecase/sweep_expiries_test.go` — `TestExpiry_ResolveNoPayment_Expires`, `TestExpiry_ResolvePaidFound_Review`
- [ ] Integration: `worker/session_sweeper_integration_test.go` — `TestSessionSweeper_ReleasesExpired` (TC-SES-05..08), `TestExpiry_ReviewNotOversell` (TC-PAY-06..08)

---

## Task 11: Event Sourcing Integrity (append-only, replay, concurrency) (R11)

**Traces to:** Requirement 11, SRS-C-09/Q-FUN-01/F-90 · TC-ES-01..05, TC-BKG-07, TC-OPS-01

### Acceptance Criteria
- [ ] AC11.1: Every state change appends to `booking_events` (PK `(stream_id, version)`), projects read model, mutates stock, writes outbox — all in one tx (uses `pkg/eventstore.Store.Append` on the same `pgx.Tx`)
- [ ] AC11.2: Version conflict (23505 on `(stream_id,version)` → `eventstore.ErrConcurrency`) → re-read + retry (max 3), **never** overwrite
- [ ] AC11.3: `booking_events` payload has **no** plaintext NIK/email/token — enforced by `pkg/eventstore` (`ErrForbiddenField`); only hash/masked allowed
- [ ] AC11.4: DB rejects UPDATE/DELETE/TRUNCATE on `booking_events` (triggers; ERRCODE `restrict_violation`) (TC-ES-01)
- [ ] AC11.5: Stock never negative: CHECKs on category/supply (TC-BKG-07)
- [ ] AC11.6: `event_supply.total_capacity = Σ category_inventories.capacity` (deferred trigger `SUPPLY_CATEGORY_MISMATCH` at COMMIT)
- [ ] AC11.7: Reconciliation job detects stock/booking drift → alert (TC-OPS-01, SRS-F-90-01)

### Files
- `infrastructure/postgres/event_store.go` — thin wrapper constructing `eventstore.New("booking_events")`, exposed as `IEventStore`
- `application/usecase/replay_booking.go` — rebuild read model via `eventstore.Load` + `eventstore.Replay` (Applier over `bookings`/`booking_items`; **stock not replayed**)
- `application/usecase/reconcile.go` + reconciliation worker job (compares Σ held/sold read model vs stock counters vs event store)

### pkg/eventstore usage
```go
st, err := eventstore.New("booking_events")                 // table suffix guard
events, _ := st.Load(ctx, tx, bookingID)                    // version-ordered
proj := &bookingProjector{}
lastVer, err := eventstore.Replay(events, proj)             // rebuild read model (not stock)
// append path: st.Append(ctx, tx, bookingID, expectedVersion, NewEvent{...})
//   → ErrConcurrency on 23505; ErrForbiddenField if payload has nik/email/token
```

### Tests
- [ ] Integration: `postgres/event_store_integration_test.go` — `TestEventStore_AppendOnlyRejectsUpdate` (TC-ES-01), `TestEventStore_VersionConflictRetry` (TC-ES-03), `TestEventStore_ReplayRebuildsReadModel` (TC-ES-04)
- [ ] Unit: `TestEventStore_ForbidsPIIPayload` (asserts `ErrForbiddenField`)
- [ ] Integration: `TestReconcile_DetectsDrift` (TC-OPS-01)

---

## Task 12: Transactional Outbox + Relay Worker (R12)

**Traces to:** Requirement 12, SRS-IF-30..31/Q-REL · TC-API-01, TC-API-02, TC-CAP-01..03

**Pattern:** `[[transactional-outbox]]` — outbox row written in the **same tx** as the stock/money change
(Tasks 6, 9, 10); relay worker publishes at-least-once; downstream consumers idempotent.

### Outbox table (already exists — `0001_init.up.sql`)
```sql
CREATE TABLE outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    routing_key text NOT NULL,
    aggregate_id uuid NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    available_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    publish_attempts integer NOT NULL DEFAULT 0,
    last_error text
);
CREATE INDEX idx_booking_outbox_pending ON outbox_events(available_at) WHERE published_at IS NULL;
```

### Acceptance Criteria
- [ ] AC12.1: On any committed stock/money change, a row is written to `outbox_events` in the same tx (outbox only for stock/money events)
- [ ] AC12.2: Relay worker `SELECT ... WHERE published_at IS NULL ORDER BY available_at` → publish to exchange `rusuh.events` (topic) in standard envelope → set `published_at`
- [ ] AC12.3: Routing keys → consumers: `booking.journey.ended`→Queue, `booking.quota.exhausted`→Queue, `booking.sold_out`→Queue, `booking.paid`→Notification, `booking.expired`→Notification, `booking.review`→Notification/Payment
- [ ] AC12.4: Publish failure → `publish_attempts++`, `last_error=...`, retry next cycle (no event lost; `available_at` backoff)
- [ ] AC12.5: Payloads match `docs/contracts/events/*.schema.json` + `envelope.schema.json`, **no** plaintext PII (only `ticketLinkToken` for email)

### Files
- `port/outbound/outbox_writer.go` (`IOutboxWriter.Enqueue(tx, routingKey, aggregateID, payload)`) — already referenced by Tasks 6/9/10
- `infrastructure/postgres/outbox_writer.go` — INSERT within caller's tx
- `infrastructure/rabbitmq/outbox_relay.go` — the relay job (uses `pkg/rabbitmq.Publisher.Publish` + `rabbitmq.NewEnvelope`)
- register relay in `jobs(*Clients)`

### Relay worker pattern
```go
// one cycle (own tx per batch scan; publish outside the scan tx to avoid long locks)
rows := SELECT id, routing_key, aggregate_id, schema_version, payload
          FROM booking.outbox_events WHERE published_at IS NULL ORDER BY available_at LIMIT $batch;
for each row {
    env, _ := rabbitmq.NewEnvelope(row.routing_key, config.ServiceName, row.aggregate_id, cid, row.payload, now)
    if err := pub.Publish(ctx, row.routing_key, env); err != nil {
        UPDATE booking.outbox_events
           SET publish_attempts = publish_attempts + 1, last_error = $err,
               available_at = now() + make_interval(secs => backoff(publish_attempts))
         WHERE id = row.id;                      // retry next cycle, no loss
        continue
    }
    UPDATE booking.outbox_events SET published_at = now() WHERE id = row.id;
}
```

### Tests
- [ ] Integration: `rabbitmq/outbox_relay_integration_test.go` — `TestOutboxRelay_PublishesPendingInOrder`, `TestOutboxRelay_RetriesOnPublishFailure` (TC-CAP-01..03)
- [ ] Unit: `TestOutboxEnvelope_NoPIIPayload`
- [ ] Contract: `TestOutboxPayload_MatchesSchema` against `events/*.schema.json` (TC-API-01/02)

---

## Task 13: Internal Payment APIs — payable, payment-lock, deadline-extension (R9, supports Payment)

**Traces to:** SDD §3.4 (internal-booking.yaml), Δ7 · TC-PAY-02

### Acceptance Criteria
- [ ] AC13.1: `GET /internal/v1/bookings/{bookingId}/payable` → `Payable{ bookingId, status, amountMinor, paymentExpiresAt, visitorId?, buyerEmail?, version, canEdit? }`
- [ ] AC13.2: `POST /internal/v1/bookings/{bookingId}/payment-lock` `{ attemptKey }` idempotent per `attemptKey` → `{ outcome ∈ {OK,BOOKING_NOT_PAYABLE,PAYMENT_EXPIRED}, version?, amountMinor?, paymentExpiresAt? }`; sets `payment_started_at`, moves toward `VERIFYING`, rejects later edits (`BOOKING_LOCKED`)
- [ ] AC13.3: `GET /internal/v1/events/{eventId}/supply` → `Supply{ free, allocated, held, sold, total, maxPerOrder, saleState, soldOut, version, categories[] }`
- [ ] AC13.4: `POST /internal/v1/bookings/{bookingId}/deadline-extension` `{ reason, seconds 1..600 }` once → extend `payment_expires_at`, set `deadline_extended_at`

### Files
- `domain/dto/internal_payment.go`; `port/inbound/{payable_usecase.go,payment_lock_usecase.go,supply_usecase.go,deadline_usecase.go}`; `application/usecase/{get_payable.go,payment_lock.go,get_supply.go,extend_deadline.go}`; `infrastructure/http/internal_payment_handler.go` (`serviceBearer`)

### SQL — payment-lock (one tx, FOR UPDATE)
```sql
SELECT status, version, amount_minor, payment_expires_at, payment_started_at
  FROM booking.bookings WHERE id = $bookingId FOR UPDATE;
-- payment_expires_at < now() → PAYMENT_EXPIRED; status not in (PENDING_PAYMENT,VERIFYING) → BOOKING_NOT_PAYABLE
UPDATE booking.bookings
   SET payment_started_at = COALESCE(payment_started_at, now()),
       status = 'VERIFYING', verification_until = now() + interval '...', version = version + 1
 WHERE id = $bookingId;                            -- idempotent on retry via stored attemptKey
```

### Tests
- [ ] Unit: `usecase/payment_lock_test.go` — `TestPaymentLock_Idempotent`, `_Expired`, `_NotPayable`
- [ ] Integration: `TestSupply_RicherThanProse`

---

## Task 14: Admin Seed APIs (R-seed) + Build & Verify (R13)

**Traces to:** Requirement 13, SDD App. C · TC-OBS-01, TC-ASG-01

### Acceptance Criteria
- [ ] AC14.1: `POST /internal/v1/admin/events` (`adminBearer`) creates event + categories + supply in one tx; `check_supply_matches_categories` enforces `total = Σ capacity` → `SUPPLY_CATEGORY_MISMATCH`
- [ ] AC14.2: `PATCH /internal/v1/admin/events/{eventId}` before `rules_locked_at`; locked → 409 `EVENT_RULES_LOCKED`
- [ ] AC14.3: `go run ./cmd migrate up` applies; `go run ./cmd api` serves :8082; invalid config → fail fast
- [ ] AC14.4: metrics exposed: `rbt_booking_sessions_total`, `rbt_booking_holds_total`, `rbt_booking_settlements_total`, `rbt_booking_expirations_total`, `rbt_booking_stock_gauge`, `rbt_booking_event_store_version`
- [ ] AC14.5: logs structured (slog JSON), never contain NIK/email/token
- [ ] AC14.6: `make build`/`test`/`test-integration`/`lint`/`cover`(≥80%)/`isolation`/`ci` pass

### Files
- `infrastructure/http/admin_handler.go`; `application/usecase/{create_event.go,update_event_config.go}`; `infrastructure/http/metrics.go`

### Final Verification
```bash
make build && make test && make test-integration && make cover && make lint && make isolation && make ci
```

---

## Test Case Alignment

| Task | Requirement | Test Cases | Invarian |
|------|-------------|-----------|----------|
| 1 Domain core | 11 | TC-ES-* (foundation) | — |
| 2 Catalog | 1 | TC-EVT-01..03, TC-CAT-01..06 | — |
| 3 Detail | 2 | TC-EVT-04..05 | — |
| 4 Admission/config | 3,4 | TC-ADM-01..05, TC-CFG-01..04 | #2, #4 |
| 5 NIK & quota | 5 | TC-BKG-01..03, 08..12 | #3 |
| 6 **Atomic hold** | 6 | **TC-BKG-04..07**, TC-PERF-04 | **#1** |
| 7 Session/timer/SSE | 7 | TC-SES-01..08, TC-PAY-02 | #4 |
| 8 Edit & recovery | 8 | TC-EDT-01..06, TC-NAV-01..03 | — |
| 9 Settlement/tickets | 9,10 | TC-PAY-03, TC-TKT-01..03, TC-USR-10 | #5 |
| 10 Sweepers + resolve | 7,9 | TC-SES-05..08, TC-PAY-04/06..08 | #4, #5 |
| 11 Event sourcing | 11 | TC-ES-01..05, TC-OPS-01 | #1, #5 |
| 12 Outbox relay | 12 | TC-API-01..02, TC-CAP-01..03 | #6 |
| 13 Internal payment | 9 | TC-PAY-02 | — |
| 14 Admin/build | 13 | TC-OBS-01..02, TC-ASG-01 | — |

## Key Concurrency Test — TC-BKG-07 (highlighted)

`TestHold_NoOversell_100ParallelOn50` in
`internal/booking/infrastructure/postgres/hold_concurrency_integration_test.go` (build tag `integration`,
`-race`): seed one category so 50 units remain → spawn 100 concurrent `Hold` calls over a shared pool →
assert **exactly 50** succeed, 50 fail with `CATEGORY_UNAVAILABLE`, and `held_quantity + sold_quantity ==
capacity` (never exceeds). Companion `TestHold_DeadlockFree_MixedCategoryOrder` proves the fixed
supply→category→quota→booking lock order yields zero `40P01` deadlocks.
