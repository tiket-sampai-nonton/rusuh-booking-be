# Tasks — Booking Service

| Field | Value |
|-------|-------|
| Service | Booking Service |
| Port | 8082 |
| Spec | `specs/booking-service/{requirements,design}.md` |
| Status | Boilerplate + full DB schema implemented; business logic is TARGET |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

---

## Pre-requisites

- [ ] Verify boilerplate compiles: `cd services/booking && go build ./...`
- [ ] Verify health endpoint: `go run ./cmd api &` then `curl localhost:8082/health`
- [ ] Ensure infra running: `make infra keys`
- [ ] Copy env: `cp .env.example .env`
- [ ] Run migrations: `go run ./cmd migrate up`
- [ ] Verify DB schema exists (events, categories, inventories, admissions, bookings, etc.)

---

## Task 1: Event Catalog (R1)

**Traces to:** Requirement 1, SRS-F-02-01..04, SRS-F-28-01..06, TC-EVT-01..03, TC-CAT-01..06

### Acceptance Criteria
- [ ] AC1.1: `GET /api/v1/events` → `EventList` with `EventSummary[]` and `serverTime`
- [ ] AC1.2: Filter by `timeframe` (ALL, THIS_WEEK, THIS_MONTH, UPCOMING)
- [ ] AC1.3: Filter by `city` (case-insensitive)
- [ ] AC1.4: Filter by `status` (ALL, NOT_STARTED, AVAILABLE, SOLD_OUT, CLOSED)
- [ ] AC1.5: Sort by `sort` (DATE_ASC, PRICE_ASC, PRICE_DESC)
- [ ] AC1.6: Response cacheable 2s by CloudFront
- [ ] AC1.7: Public endpoint (no auth)

### Implementation Steps
1. Create `domain/entity/event.go` with status enum
2. Create `domain/dto/event_list.go`, `event_summary.go`
3. Create `port/outbound/event_repository.go`
4. Create `infrastructure/postgres/event_repository.go` with filters
5. Create `application/usecase/list_events.go`
6. Create `infrastructure/http/catalog_handler.go`

### Tests
- [ ] Unit: Filter and sort logic
- [ ] Integration: Query with various filters

### Verify
```bash
curl "localhost:8082/api/v1/events?timeframe=UPCOMING&city=Jakarta&sort=PRICE_ASC"
```

---

## Task 2: Event Detail (R2)

**Traces to:** Requirement 2, SRS-F-03-01..05, TC-EVT-04, TC-EVT-05

### Acceptance Criteria
- [ ] AC2.1: `GET /api/v1/events/{eventId}` → `EventDetail` with categories
- [ ] AC2.2: `available` = `capacity - held_quantity - sold_quantity` from PostgreSQL
- [ ] AC2.3: Invalid UUID → 400 `VALIDATION_ERROR`
- [ ] AC2.4: Event not found → 404 `NOT_FOUND`
- [ ] AC2.5: Cacheable 2s

### Implementation Steps
1. Create `domain/dto/event_detail.go`
2. Add `GetByID` to event repository
3. Create `application/usecase/get_event_detail.go`
4. Add handler method

### Tests
- [ ] Unit: Available calculation
- [ ] Integration: Category availability

### Verify
```bash
curl localhost:8082/api/v1/events/{eventId}
```

---

## Task 3: Admission & Reservation (R3) - "Jangan PHP"

**Traces to:** Requirement 3, SRS-F-04, SRS-F-06-02, SRS-F-09-01..08, TC-ADM-01..05

### Acceptance Criteria
- [ ] AC3.1: `POST /internal/v1/allocations:batch` → allocate units, insert `admissions(RESERVED)` in one tx
- [ ] AC3.2: Idempotent per `queueEntryId` (UNIQUE constraint)
- [ ] AC3.3: One active admission per `(event_id, visitor_id)` and per `(event_id, buyer_nik_hash)`
- [ ] AC3.4: Admitted buyer guaranteed ticket until TTL (no PHP)
- [ ] AC3.5: Partial category sold out → other categories still bookable
- [ ] AC3.6: `POST /api/v1/booking-sessions` with `admissionToken` → `RESERVED → ACTIVE`, return `BookingSession`
- [ ] AC3.7: Invalid/reused token → 403; deadline passed → 410

### Implementation Steps
1. Create `domain/entity/admission.go` with status enum and snapshot fields
2. Create `port/outbound/admission_repository.go`
3. Create `port/outbound/supply_repository.go`
4. Create `infrastructure/postgres/admission_repository.go`
5. Create `infrastructure/postgres/supply_repository.go`
6. Create `application/usecase/allocate_batch.go` (internal)
7. Create `application/usecase/create_session.go`
8. Create `infrastructure/http/internal_handler.go`
9. Create `infrastructure/http/session_handler.go`

### Tests
- [ ] Integration: Atomic allocation (TC-ADM-01)
- [ ] Integration: Anti-calo unique constraints (TC-ADM-02)
- [ ] Integration: Partial sold out (TC-ADM-05)

### Verify
```bash
# Queue calls this internal API
curl -X POST localhost:8082/internal/v1/allocations:batch -d '...'
```

---

## Task 4: Config Snapshot & Rule Locking (R4)

**Traces to:** Requirement 4, SRS-F-26-01..08, TC-CFG-01..04

### Acceptance Criteria
- [ ] AC4.1: Admission stores config snapshot (`cfg_*` fields)
- [ ] AC4.2: Session uses snapshot values, not current event config
- [ ] AC4.3: `rules_locked_at` set when event becomes AVAILABLE
- [ ] AC4.4: Modifying locked rules → DB exception `EVENT_RULES_LOCKED`
- [ ] AC4.5: Config changes → `config_audit` entries
- [ ] AC4.6: TTL values can change (affect new sessions only)

### Implementation Steps
1. Verify triggers exist in migration (`guard_event_rules`, `audit_event_config`)
2. Add config snapshot to admission entity
3. Use snapshot in session/hold logic

### Tests
- [ ] Integration: Snapshot isolation (TC-CFG-01)
- [ ] Integration: Rule lock enforcement

### Verify
```bash
# Try to update locked rules - should fail
```

---

## Task 5: NIK Validation & Buyer Quota (R5) - Invariant #3

**Traces to:** Requirement 5, SRS-F-11-01..10, TC-BKG-01..03, TC-BKG-08..12

### Acceptance Criteria
- [ ] AC5.1: `POST .../attendee-checks` with NIK → validate structure, return `result`
- [ ] AC5.2: Invalid NIK structure → 422 `NIK_INVALID` (no quota deduction)
- [ ] AC5.3: Same NIK check in session → dedupe (no additional count)
- [ ] AC5.4: Check limit exceeded → `CHECK_LIMIT_REACHED`, then 429
- [ ] AC5.5: `held + paid + new > max_quantity` → 409 `BUYER_QUOTA_EXHAUSTED`
- [ ] AC5.6: NIK already has HELD/PAID item in event → 409 `ATTENDEE_NIK_ALREADY_REGISTERED`
- [ ] AC5.7: Duplicate NIK in same order → 409 `ATTENDEE_NIK_DUPLICATE_IN_ORDER`
- [ ] AC5.8: Buyer quota separate from attendee identity

### Implementation Steps
1. Create `domain/valueobject/nik.go` with structure validation (Appendix A.1)
2. Create `domain/entity/attendee_check.go`
3. Create `port/outbound/quota_repository.go`
4. Create `infrastructure/postgres/quota_repository.go`
5. Create `application/usecase/check_attendee.go`
6. Create `application/usecase/validate_quota.go`
7. Add attendee-checks handler

### Tests
- [ ] Unit: NIK structure validation
- [ ] Integration: Quota enforcement (TC-BKG-01)
- [ ] Integration: NIK uniqueness (TC-BKG-02, TC-BKG-03)

### Verify
```bash
curl -X POST localhost:8082/api/v1/booking-sessions/{id}/attendee-checks -d '{"nik":"1234567890123456"}'
```

---

## Task 6: Atomic Hold (R6) - Invariant #1 "No Oversell"

**Traces to:** Requirement 6, SRS-F-12-01..07, SRS-Q-FUN-01, TC-BKG-04..07, TC-PERF-04

### Acceptance Criteria
- [ ] AC6.1: `POST /api/v1/bookings` with `Idempotency-Key` → create booking `PENDING_PAYMENT`, update counters atomically
- [ ] AC6.2: Idempotent per `(scope, owner_id, idempotency_key)`
- [ ] AC6.3: Same key + different hash → 409 `IDEMPOTENCY_KEY_REUSED`
- [ ] AC6.4: `held + sold > capacity` → rollback, 409 `CATEGORY_UNAVAILABLE` (TC-BKG-07: 100 parallel holds on 50 tickets → exactly 50 succeed)
- [ ] AC6.5: Items > `maxTicketsPerOrder` → 422 `QUANTITY_EXCEEDS_LIMIT`
- [ ] AC6.6: Mixed categories when disabled → 422 `MIXED_CATEGORIES_NOT_ALLOWED`
- [ ] AC6.7: Event sold out → 409 `CATEGORY_UNAVAILABLE`
- [ ] AC6.8: Append `BookingHeld` event, project read model in same tx
- [ ] AC6.9: Dependency failure → 503 `DEPENDENCY_UNAVAILABLE`

### Implementation Steps
1. Create `domain/entity/booking.go` with status enum
2. Create `domain/entity/booking_item.go`
3. Create `port/outbound/booking_repository.go`
4. Create `port/outbound/inventory_repository.go`
5. Create `port/outbound/idempotency_store.go`
6. Create `port/outbound/event_store.go` (for booking_events)
7. Create `port/outbound/unit_of_work.go` interface
8. Create `infrastructure/postgres/unit_of_work.go` with lock ordering
9. Create `application/usecase/hold_booking.go`
10. Create `infrastructure/http/booking_handler.go`

### Tests
- [ ] Unit: All validation branches
- [ ] Integration: **TC-BKG-07** - 100 concurrent holds on 50 tickets
- [ ] Integration: Idempotency key handling
- [ ] Integration: Lock ordering (no deadlock)

### Verify
```bash
go test -tags integration -race ./...
curl -X POST -H "Idempotency-Key: test-123" localhost:8082/api/v1/bookings -d '...'
```

### Demo
- Exactly 50 of 100 concurrent requests succeed
- No oversell in any scenario

---

## Task 7: Session & Timeout (R7) - Invariant #4

**Traces to:** Requirement 7, SRS-F-07-01..06, SRS-F-08-01..03, TC-SES-01..08, TC-PAY-02

### Acceptance Criteria
- [ ] AC7.1: `expires_at` and `payment_expires_at` calculated server-side
- [ ] AC7.2: `GET /booking-sessions/current` or `GET /booking-sessions/{id}` → `expiresAt`, `serverTime`, `phase`
- [ ] AC7.3: Session sweeper: expired admission → `EXPIRED`/`RELEASED`, release allocation/hold
- [ ] AC7.4: Request after `expires_at` → 410 `SESSION_EXPIRED`
- [ ] AC7.5: `payment_expires_at` not reset by edit
- [ ] AC7.6: No payment by `payment_expires_at` → booking expires (R9)

### Implementation Steps
1. Create session sweeper worker job
2. Implement phase calculation logic
3. Add `expiresAt` to all session responses

### Tests
- [ ] Integration: Session expiry flow (TC-SES-05..08)

### Verify
```bash
# Watch session expire
curl localhost:8082/api/v1/booking-sessions/{id}
```

---

## Task 8: Edit Booking & Session Recovery (R8)

**Traces to:** Requirement 8, SRS-F-23-01..11, SRS-F-24-01..13, TC-EDT-01..06, TC-NAV-01..03

### Acceptance Criteria
- [ ] AC8.1: `PUT /bookings/{id}/items` with `version` → atomic replacement, version++
- [ ] AC8.2: `items[].attendee` can be `{nik, fullName}` or `{keepFromItemId}`
- [ ] AC8.3: Version mismatch → 409 `BOOKING_VERSION_CONFLICT`
- [ ] AC8.4: `payment_started_at` set → 409 `BOOKING_LOCKED`
- [ ] AC8.5: `edit_count >= max` → 409 `EDIT_LIMIT_REACHED`
- [ ] AC8.6: Append `BookingItemsReplaced` event, `payment_expires_at` unchanged
- [ ] AC8.7: `GET /booking-sessions/current` after refresh → recover active session

### Implementation Steps
1. Create `application/usecase/edit_booking.go`
2. Implement item replacement logic
3. Add version conflict detection
4. Add edit handler method
5. Implement session recovery in `get_current_session.go`

### Tests
- [ ] Unit: Version conflict detection
- [ ] Integration: Edit with `keepFromItemId`
- [ ] Integration: Session recovery (TC-NAV-01)

### Verify
```bash
curl -X PUT -H "Idempotency-Key: edit-1" localhost:8082/api/v1/bookings/{id}/items -d '...'
```

---

## Task 9: Settlement & Expiry (R9) - Invariant #5

**Traces to:** Requirement 9, SRS-F-16-01..03, SRS-F-17-01..05, TC-PAY-03..08

### Acceptance Criteria
- [ ] AC9.1: `payment.succeeded` → settlement in one tx: held→sold, items PAID, issue tickets, append `BookingPaid`, outbox `booking.paid`
- [ ] AC9.2: Consumer idempotent via state guard (no inbox table)
- [ ] AC9.3: `payment.failed` → log only, booking waits for TTL
- [ ] AC9.4: Expiry sweeper for PENDING/VERIFYING past TTL → call Payment `/resolve`
- [ ] AC9.5: Resolve no payment → expire booking, release stock, outbox `booking.expired`
- [ ] AC9.6: Resolve payment found → `REVIEW` status, outbox `booking.review`

### Implementation Steps
1. Create consumer for `payment.succeeded` and `payment.failed`
2. Create `application/usecase/settle_booking.go`
3. Create expiry sweeper worker job
4. Create `port/outbound/payment_client.go` for resolve
5. Create `application/usecase/expire_booking.go`
6. Register consumers in `RunWorker`

### Tests
- [ ] Integration: Settlement idempotency (duplicate event test)
- [ ] Integration: Expiry with resolve (TC-PAY-04, TC-PAY-06..08)

### Verify
```bash
go test -tags integration -race ./...
```

---

## Task 10: Tickets & History (R10)

**Traces to:** Requirement 10, SRS-F-20-01..03, SRS-F-27-11, TC-TKT-01..03, TC-USR-10

### Acceptance Criteria
- [ ] AC10.1: Settlement → one `tickets` row per `booking_item`
- [ ] AC10.2: `GET /bookings/{id}/tickets` when PAID → `TicketList`
- [ ] AC10.3: Not PAID → 409 `BOOKING_NOT_PAYABLE`
- [ ] AC10.4: `GET /api/v1/me/bookings` with userBearer → only own bookings
- [ ] AC10.5: Invalid access token → 401

### Implementation Steps
1. Create `domain/entity/ticket.go`
2. Create `port/outbound/ticket_repository.go`
3. Create `application/usecase/get_tickets.go`
4. Create `application/usecase/list_my_bookings.go`
5. Add handlers

### Tests
- [ ] Integration: Ticket issuance
- [ ] Integration: Booking history ownership

### Verify
```bash
curl localhost:8082/api/v1/bookings/{id}/tickets
curl -H "Authorization: Bearer $TOKEN" localhost:8082/api/v1/me/bookings
```

---

## Task 11: Event Sourcing & Stock Integrity (R11)

**Traces to:** Requirement 11, SRS-C-09, SRS-Q-FUN-01, TC-ES-01..05, TC-BKG-07

### Acceptance Criteria
- [ ] AC11.1: State changes → append to `booking_events`, project read model, change stock in one tx
- [ ] AC11.2: Version conflict (23505) → re-read + retry (max 3)
- [ ] AC11.3: No NIK/email/token in event payload (rejected by `pkg/eventstore`)
- [ ] AC11.4: `UPDATE`/`DELETE`/`TRUNCATE` on `booking_events` → trigger rejects
- [ ] AC11.5: Stock never negative (CHECK constraints)
- [ ] AC11.6: `total_capacity = Σ category.capacity` (deferred trigger)
- [ ] AC11.7: Reconciliation job detects inconsistencies

### Implementation Steps
1. Verify triggers in migration
2. Implement event store adapter using `pkg/eventstore`
3. Implement optimistic concurrency retry loop
4. Add payload validation

### Tests
- [ ] Integration: Append-only enforcement (TC-ES-01)
- [ ] Integration: Concurrency retry
- [ ] Integration: Stock constraint checks

### Verify
```bash
# Try to UPDATE booking_events - should fail
```

---

## Task 12: Transactional Outbox (R12)

**Traces to:** Requirement 12, SRS-IF-30..31, TC-API-01..02, TC-CAP-01..03

### Acceptance Criteria
- [ ] AC12.1: Stock/money changes → write `outbox_events` in same tx
- [ ] AC12.2: Outbox relay worker publishes to `rusuh.events` with confirms
- [ ] AC12.3: Events published: `booking.journey.ended`, `booking.quota.exhausted`, `booking.sold_out`, `booking.paid`, `booking.expired`, `booking.review`
- [ ] AC12.4: Publish failure → increment attempts, retry next cycle
- [ ] AC12.5: Payload matches contract schemas, no PII

### Implementation Steps
1. Create `port/outbound/outbox_writer.go`
2. Create `infrastructure/postgres/outbox_writer.go`
3. Create outbox relay worker job
4. Wire publishing with confirms

### Tests
- [ ] Integration: Outbox relay with publisher confirms

### Verify
```bash
# Check RabbitMQ for events
rabbitmqadmin get queue=notification.booking.events
```

---

## Task 13: Health & Observability (R13)

**Traces to:** Requirement 13, SRS-Q-MNT-02, TC-OBS-01..02

**Status:** Health ✅ IMPLEMENTED, Metrics ❌ TARGET

### Final Verification
```bash
make build
make test
make test-integration
make lint
make cover
make ci
```

---

## Test Case Alignment

| Task | Test Cases |
|------|------------|
| 1 | TC-EVT-01..03, TC-CAT-01..06 |
| 2 | TC-EVT-04, TC-EVT-05 |
| 3 | TC-ADM-01..05 |
| 4 | TC-CFG-01..04 |
| 5 | TC-BKG-01..03, TC-BKG-08..12 |
| 6 | TC-BKG-04..07, TC-PERF-04 |
| 7 | TC-SES-01..08, TC-PAY-02 |
| 8 | TC-EDT-01..06, TC-NAV-01..03 |
| 9 | TC-PAY-03..08 |
| 10 | TC-TKT-01..03, TC-USR-10 |
| 11 | TC-ES-01..05, TC-BKG-07, TC-OPS-01 |
| 12 | TC-API-01..02, TC-CAP-01..03 |
| 13 | TC-OBS-01..02 |
