# Tasks — Queue Service

| Field | Value |
|-------|-------|
| Service | Queue Service |
| Port | 8081 |
| Spec | `specs/queue-service/{requirements,design}.md` |
| Status | Boilerplate implemented; business logic is TARGET |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

---

## Pre-requisites

- [ ] Verify boilerplate compiles: `cd services/queue && go build ./...`
- [ ] Verify health endpoint: `go run ./cmd api &` then `curl localhost:8081/health`
- [ ] Ensure infra running: `make infra keys`
- [ ] Copy env: `cp .env.example .env`
- [ ] Run migrations: `go run ./cmd migrate up`

---

## Task 1: Visitor Identity & Auth Resolution (R1)

**Traces to:** Requirement 1, SRS-F-01-01..09, TC-GST-01, TC-GST-03, TC-GST-04, TC-USR-09, TC-USR-09a

### Acceptance Criteria
- [ ] AC1.1: Guest join (web) → issue visitor token, set `rb_visitor` cookie (HttpOnly, Secure, SameSite=Lax)
- [ ] AC1.2: Mobile join → return `visitorToken` in body for secure storage
- [ ] AC1.3: Login with NIK (`token.has_nik == true`) → use `token.nik_hash`, ignore `body.buyerNik`
- [ ] AC1.4: Login without NIK (`token.has_nik == false`) → require `body.buyerNik`
- [ ] AC1.5: Visitor already has active entry + `one_active_per_visitor == true` → 409 `VISITOR_ALREADY_IN_QUEUE`
- [ ] AC1.6: Previous entry terminal → allow new join

### Implementation Steps
1. Create `domain/valueobject/visitor_id.go`
2. Create `port/outbound/token_verifier.go` for User Service token verification
3. Create `infrastructure/security/token_verifier.go` using `pkg/token`
4. Create auth resolution logic in join handler
5. Implement visitor token issuance

### Tests
- [ ] Unit: Auth resolution - guest, login+NIK, login-no-NIK cases
- [ ] Unit: Visitor token generation

### Verify
```bash
go test -race ./...
```

---

## Task 2: Queue Join & Direct Admission (R2)

**Traces to:** Requirement 2, SRS-F-04-01..10, TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08

### Acceptance Criteria
- [ ] AC2.1: Empty email or `email != emailConfirmation` → 422 `EMAIL_MISMATCH`
- [ ] AC2.2: `consentNik != true` → 400 `VALIDATION_ERROR`
- [ ] AC2.3: NIK required but empty → 422 `NIK_REQUIRED`
- [ ] AC2.4: NIK not `^[0-9]{16}$` → 422 `NIK_INVALID`
- [ ] AC2.5: Valid join + no queue + slot available → 201 `ADMITTED` with `claimDeadline`, `queueToken`
- [ ] AC2.6: Valid join + must wait → 201 `WAITING` with monotonic `seq`
- [ ] AC2.7: All constraints checked atomically via `join.lua`
- [ ] AC2.8: NIK already active → 409 `NIK_ALREADY_IN_QUEUE`
- [ ] AC2.9: Email already active + `one_active_per_email` → 409 `EMAIL_ALREADY_IN_QUEUE`
- [ ] AC2.10: NIK in `quota_exhausted` set → 409 `BUYER_QUOTA_EXHAUSTED`
- [ ] AC2.11: Sold out → 410 `EVENT_SOLD_OUT`
- [ ] AC2.12: Outside sale window → 409 `EVENT_NOT_ON_SALE`
- [ ] AC2.13: Valkey unreachable → 503 `DEPENDENCY_UNAVAILABLE` (fail closed)
- [ ] AC2.14: Same `Idempotency-Key` with active entry → return existing entry

### Implementation Steps
1. Create `domain/dto/join_request.go` with validation
2. Create `domain/entity/queue_entry.go` with status enum
3. Create `domain/valueobject/seq.go`, `buyer_nik_hash.go`
4. Create `port/outbound/queue_store.go` interface
5. Create `assets/lua/join.lua` script (see design.md §4.1)
6. Create `infrastructure/valkey/queue_store.go` with Lua execution
7. Create `port/outbound/event_settings_repo.go`
8. Create `infrastructure/postgres/event_settings_repo.go`
9. Create `application/usecase/join_queue.go`
10. Create `infrastructure/http/join_handler.go`
11. Wire routes in `server.go`

### Tests
- [ ] Unit: `usecase/join_queue_test.go` - all validation and constraint cases
- [ ] Integration: `valkey/queue_store_integration_test.go` - Lua atomicity
- [ ] Integration: 10,000 parallel joins - no seq gaps (TC-QUE-08)

### Verify
```bash
go test -race ./...
go test -tags integration -race ./...
curl -X POST localhost:8081/api/v1/events/{eventId}/queue-entries \
  -d '{"email":"test@example.com","emailConfirmation":"test@example.com","buyerNik":"1234567890123456","consentNik":true}'
```

### Demo
- Join returns 201 with `seq` and `status`
- Direct admission when slots available

---

## Task 3: Rate Limiting (R3)

**Traces to:** Requirement 3, SRS-F-04-09

### Acceptance Criteria
- [ ] AC3.1: Join rate exceeded per email/IP → 429 `RATE_LIMITED` with `Retry-After`
- [ ] AC3.2: `GET /queue-entries/me` rate exceeded → 429
- [ ] AC3.3: Rate limit calculated in Valkey (consistent across instances)
- [ ] AC3.4: `GET /queue-status/{eventId}` NOT rate limited at origin (CDN absorbs)

### Implementation Steps
1. Create `infrastructure/valkey/rate_limiter.go` (fixed-window)
2. Add rate limit middleware to handlers
3. Configure per-endpoint limits from `event_settings`

### Tests
- [ ] Unit: Rate limiter logic
- [ ] Integration: Valkey rate limit persistence

### Verify
```bash
go test -race ./...
# Rapid requests trigger 429
```

---

## Task 4: Queue Status Document (R4)

**Traces to:** Requirement 4, SRS-F-05-01..11, TC-QUE-03, TC-QUE-10, TC-QUE-11, TC-QUE-12, TC-PERF-01

### Acceptance Criteria
- [ ] AC4.1: `GET /queue-status/{eventId}` → `StatusDocument` with required fields
- [ ] AC4.2: Response has `Cache-Control: public, max-age=1, stale-while-revalidate=2`
- [ ] AC4.3: Status Publisher worker updates `q:{e}:status` every `status_publish_interval_ms`
- [ ] AC4.4: Polling intervals controlled by `poll` object (`farMs`, `midMs`, `nearMs`)
- [ ] AC4.5: Zone calculation: `guaranteedUntilSeq`, `possibleUntilSeq` from stock
- [ ] AC4.6: Category states: `AVAILABLE`, `LIMITED`, `SOLD_OUT`
- [ ] AC4.7: No personal data in status document
- [ ] AC4.8: Event not found → 404

### Implementation Steps
1. Create `domain/dto/status_document.go`
2. Create `application/usecase/get_status.go`
3. Create `infrastructure/http/status_handler.go`
4. Create Status Publisher worker job
5. Add zone calculation logic

### Tests
- [ ] Unit: Zone calculation (TC-QUE-10, TC-QUE-11)
- [ ] Unit: Category state thresholds (TC-QUE-12)
- [ ] Integration: Status Document caching

### Verify
```bash
go test -race ./...
curl localhost:8081/api/v1/queue-status/{eventId}
```

### Demo
- Status document with zones and polling intervals
- Cache headers present

---

## Task 5: Get My Entry (R5)

**Traces to:** Requirement 5, SRS-F-05-01..11, TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-14

### Acceptance Criteria
- [ ] AC5.1: Valid visitor → `EntryStatus` with `entryId`, `seq`, `status`, `exactPosition`, `zone`
- [ ] AC5.2: `exactPosition = mySeq - servingSeq - removedAhead`
- [ ] AC5.3: `ADMITTED` status includes `claimDeadline` and `redirect`
- [ ] AC5.4: Entry not found → 404
- [ ] AC5.5: Terminal status includes `reason`

### Implementation Steps
1. Create `domain/dto/entry_status.go`
2. Create `application/usecase/get_my_entry.go`
3. Create `infrastructure/http/my_entry_handler.go`
4. Implement position calculation

### Tests
- [ ] Unit: Position calculation with removed entries

### Verify
```bash
curl -H "X-Visitor-Token: $TOKEN" "localhost:8081/api/v1/queue-entries/me?eventId=$EVENT_ID"
```

---

## Task 6: Admission, Promotion & Claim (R6)

**Traces to:** Requirement 6, SRS-F-06-01..08, TC-QUE-05, TC-QUE-08, TC-QUE-09, TC-QUE-13, TC-QUE-14

### Acceptance Criteria
- [ ] AC6.1: Promoter worker takes N entries from `waiting` by seq order, calls Booking `allocations:batch`
- [ ] AC6.2: `RESERVED` → move to `active`, status `ADMITTED`, publish `queue.turn.arrived`
- [ ] AC6.3: `NO_SUPPLY_NOW` → keep in `waiting` at same seq
- [ ] AC6.4: `QUOTA_EXHAUSTED` → add to `quota_exhausted` set, entry `REJECTED`
- [ ] AC6.5: `SOLD_OUT` → close queue, entry `SOLD_OUT`
- [ ] AC6.6: FIFO maintained: no higher seq admitted before lower seq
- [ ] AC6.7: `POST .../claim` with valid visitor + `ADMITTED` + `now < claimDeadline` → issue `admissionToken`, status `CLAIMED`
- [ ] AC6.8: `now >= claimDeadline` or `MISSED` → 410 `TURN_MISSED`
- [ ] AC6.9: Sold out at claim → 410 `EVENT_SOLD_OUT`
- [ ] AC6.10: Deadline passed without claim → Sweeper sets `MISSED`, releases slot, publishes `booking.journey.ended`
- [ ] AC6.11: Position reaches `preTurnNotifyPosition` → publish `queue.turn.soon`

### Implementation Steps
1. Create `assets/lua/promote.lua` script
2. Create `assets/lua/claim.lua` script
3. Create `port/outbound/booking_client.go` interface
4. Create `infrastructure/bookingclient/client.go`
5. Create Promoter worker job
6. Create Claim Sweeper worker job
7. Create `application/usecase/claim_turn.go`
8. Create `infrastructure/http/claim_handler.go`
9. Implement admission token issuance (EdDSA, TTL 60s)

### Tests
- [ ] Unit: FIFO ordering verification
- [ ] Integration: `promote.lua` atomicity
- [ ] Integration: `claim.lua` deadline check
- [ ] Integration: Concurrent claims (TC-PERF-04)

### Verify
```bash
go test -race ./...
go test -tags integration -race ./...
curl -X POST localhost:8081/api/v1/queue-entries/{entryId}/claim
```

### Demo
- Promoter moves entries FIFO
- Claim returns admission token
- Sweeper handles missed claims

---

## Task 7: Leave Queue & Move Entry (R7)

**Traces to:** Requirement 7, SRS-F-13-03, TC-QUE-16, TC-QUE-17

### Acceptance Criteria
- [ ] AC7.1: `DELETE /queue-entries/{entryId}` → status `LEFT`, cleanup constraint keys, release slot, 204
- [ ] AC7.2: Left user rejoins → new `seq` (back of queue)
- [ ] AC7.3: Move entry via email link → same `seq`, new visitor ownership
- [ ] AC7.4: Old link after move → `ENTRY_MOVED`
- [ ] AC7.5: Entry not found → 404

### Implementation Steps
1. Create `assets/lua/leave.lua` script
2. Create `application/usecase/leave_queue.go`
3. Create `infrastructure/http/leave_handler.go`
4. Implement move entry logic (if needed)

### Tests
- [ ] Unit: Leave and rejoin flow
- [ ] Integration: Constraint cleanup

### Verify
```bash
curl -X DELETE localhost:8081/api/v1/queue-entries/{entryId}
```

---

## Task 8: Event Consumption (R8)

**Traces to:** Requirement 8, SRS-F-10-01..03, TC-QUE-05

### Acceptance Criteria
- [ ] AC8.1: `booking.journey.ended` with `CLAIMED` entry → release slot, set terminal status
- [ ] AC8.2: Already terminal → ignore (idempotent)
- [ ] AC8.3: `booking.quota.exhausted` → `SADD quota_exhausted` (idempotent)
- [ ] AC8.4: `booking.sold_out` → close queue
- [ ] AC8.5: Corrupt payload → `rabbitmq.Permanent` to DLQ
- [ ] AC8.6: Retry with delays, DLQ after max retries
- [ ] AC8.7: Idempotent proven by duplicate send test

### Implementation Steps
1. Create `assets/lua/end_entry.lua` script
2. Create consumer handlers for each event type
3. Register subscriptions in `RunWorker`
4. Wire `pkg/rabbitmq` consumer

### Tests
- [ ] Integration: Duplicate event delivery (TC-QUE-05)
- [ ] Integration: DLQ for corrupt payload

### Verify
```bash
go test -tags integration -race ./...
```

---

## Task 9: Event Publication (R9)

**Traces to:** Requirement 9, SRS-F-03-25, TC-QUE-13, TC-PERF-05

### Acceptance Criteria
- [ ] AC9.1: Pre-turn position → publish `queue.turn.soon`
- [ ] AC9.2: Admission → publish `queue.turn.arrived` with `claimDeadline`
- [ ] AC9.3: No NIK/email/secret/token in payload
- [ ] AC9.4: Wrapped in envelope, published with confirms

### Implementation Steps
1. Create `port/outbound/event_publisher.go`
2. Create `infrastructure/rabbitmq/event_publisher.go`
3. Wire publishing into Promoter worker

### Tests
- [ ] Unit: Payload validation (no PII)
- [ ] Integration: Publisher confirms

### Verify
```bash
# Check RabbitMQ for events
rabbitmqadmin get queue=notification.queue.events
```

---

## Task 10: Health & Operability (R10)

**Traces to:** Requirement 10, D-23, D-24

**Status:** Health ✅ IMPLEMENTED, Metrics ❌ TARGET

### Acceptance Criteria
- [ ] AC10.1: `GET /health` → 200 UP or 503 DOWN (implemented)
- [ ] AC10.2: Config validated at start (implemented)
- [ ] AC10.3: Metrics exposed (target)
- [ ] AC10.4: No NIK/email/token in logs

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
| 1 | TC-GST-01, TC-GST-03, TC-GST-04, TC-USR-09, TC-USR-09a |
| 2 | TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08 |
| 3 | (rate limit inspection) |
| 4 | TC-QUE-03, TC-QUE-10, TC-QUE-11, TC-QUE-12, TC-PERF-01 |
| 5 | TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-14 |
| 6 | TC-QUE-05, TC-QUE-08, TC-QUE-09, TC-QUE-13, TC-QUE-14, TC-PERF-04 |
| 7 | TC-QUE-16, TC-QUE-17 |
| 8 | TC-QUE-05, TC-PERF-05 |
| 9 | TC-QUE-13, TC-PERF-05 |
| 10 | (health integration) |
