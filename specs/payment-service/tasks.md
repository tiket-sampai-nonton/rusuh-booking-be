# Tasks — Payment Service

| Field | Value |
|-------|-------|
| Service | Payment Service |
| Port | 8083 |
| Spec | `specs/payment-service/{requirements,design}.md` |
| Status | Boilerplate + DB schema implemented; business logic is TARGET |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

---

## Pre-requisites

- [ ] Verify boilerplate compiles: `cd services/payment && go build ./...`
- [ ] Verify health endpoint: `go run ./cmd api &` then `curl localhost:8083/health`
- [ ] Ensure infra running: `make infra keys`
- [ ] Copy env: `cp .env.example .env` (set `PAYMENT_PROVIDER=simulator`)
- [ ] Run migrations: `go run ./cmd migrate up`
- [ ] Verify `pkg/resilience` breaker tests pass: `go test ./pkg/resilience/...`

---

## Task 1: Payment Page (R1)

**Traces to:** Requirement 1, SRS-F-14, TC-PAY-01, TC-PAY-02, TC-PAY-10

### Acceptance Criteria
- [ ] AC1.1: `GET /payments/{bookingId}/page` with visitor token → 200 `PaymentPage`
- [ ] AC1.2: Booking not payable → 409 `BOOKING_NOT_PAYABLE`
- [ ] AC1.3: TTL expired → 410 `BOOKING_EXPIRED`
- [ ] AC1.4: Visitor mismatch → 403 `VISITOR_MISMATCH`
- [ ] AC1.5: No NIK plaintext in response

### Implementation Steps
1. Create `domain/dto/payment_page.go`
2. Create `port/outbound/booking_client.go` for `/internal/v1/bookings/{id}/payable`
3. Create `infrastructure/bookingclient/client.go`
4. Create `application/usecase/get_payment_page.go`
5. Create `infrastructure/http/payment_handler.go`

### Tests
- [ ] Unit: All error branches
- [ ] Integration: Booking client communication

### Verify
```bash
curl -H "X-Visitor-Token: $TOKEN" localhost:8083/api/v1/payments/{bookingId}/page
```

---

## Task 2: Page Token for Mobile WebView (R2)

**Traces to:** Requirement 2, SRS-F-18, TC-MOB-01, TC-SEC-01

### Acceptance Criteria
- [ ] AC2.1: `POST /payments/{bookingId}/page-tokens` → 201 with `pageToken`, `pageUrl`, `expiresAt`
- [ ] AC2.2: `POST /payment-page-sessions` with valid token → 204, set `rb_pay` cookie
- [ ] AC2.3: Used/expired/unknown token → 403 `PAYMENT_PAGE_TOKEN_INVALID`

### Implementation Steps
1. Create `domain/entity/page_token.go`
2. Create `port/outbound/page_token_store.go`
3. Create `infrastructure/postgres/page_token_store.go`
4. Create `application/usecase/create_page_token.go`
5. Create `application/usecase/exchange_page_token.go`
6. Add handlers

### Tests
- [ ] Unit: Token generation and validation
- [ ] Integration: One-time use enforcement (TC-SEC-01)

### Verify
```bash
curl -X POST localhost:8083/api/v1/payments/{bookingId}/page-tokens
curl -X POST localhost:8083/api/v1/payment-page-sessions -d '{"pageToken":"..."}'
```

---

## Task 3: Create Payment Attempt (R3)

**Traces to:** Requirement 3, SRS-F-14, TC-PAY-01, TC-PAY-08, TC-PAY-10

### Acceptance Criteria
- [ ] AC3.1: `POST /payments/{bookingId}/attempts` with `Idempotency-Key` → call payment-lock, charge, return 201 `Attempt`
- [ ] AC3.2: At-most-once: one open attempt per booking (`uq_attempt_open_per_booking`)
- [ ] AC3.3: Same key + same hash → return stored response; different hash → 409 `IDEMPOTENCY_KEY_REUSED`
- [ ] AC3.4: Unsupported method → 422 `PAYMENT_METHOD_UNAVAILABLE`
- [ ] AC3.5: Breaker OPEN → 503 `PAYMENT_PROVIDER_UNAVAILABLE` without calling gateway
- [ ] AC3.6: Gateway error/timeout → 503, increment breaker, attempt `UNKNOWN`
- [ ] AC3.7: Method change → cancel old attempt, create new (no TTL reset)

### Implementation Steps
1. Create `domain/entity/payment_attempt.go` with status enum
2. Create `domain/valueobject/method.go`, `instruction.go`
3. Create `port/outbound/payment_gateway.go` interface
4. Create `port/outbound/attempt_event_store.go`
5. Create `port/outbound/attempt_read_model.go`
6. Create `infrastructure/midtrans/gateway.go` (real adapter)
7. Create `infrastructure/simulator/gateway.go` (local adapter)
8. Create `infrastructure/postgres/attempt_event_store.go`
9. Create `infrastructure/postgres/attempt_read_model.go`
10. Wire circuit breaker from `pkg/resilience`
11. Create `application/usecase/create_attempt.go`
12. Add handler

### Tests
- [ ] Unit: Breaker integration (TC-RES-01)
- [ ] Unit: Idempotency handling
- [ ] Integration: At-most-once constraint

### Verify
```bash
curl -X POST -H "Idempotency-Key: pay-1" localhost:8083/api/v1/payments/{bookingId}/attempts \
  -d '{"method":"GOPAY"}'
```

### Demo
- Charge returns instruction (DEEPLINK/VA/QR)
- Breaker trips after failures

---

## Task 4: Webhook Handling (R4)

**Traces to:** Requirement 4, SRS-F-15, SRS-C-21, TC-PAY-05, TC-PAY-11

### Acceptance Criteria
- [ ] AC4.1: `POST /webhooks/midtrans` → verify SHA512 signature first; invalid → 401 `WEBHOOK_SIGNATURE_INVALID`
- [ ] AC4.2: Signature verification is ONLY proof of payment
- [ ] AC4.3: Duplicate `(provider, fingerprint)` → idempotent, return 200
- [ ] AC4.4: Status mapping: `settlement|capture→SUCCEEDED`, `pending→PENDING`, `deny|cancel|failure→FAILED`, `expire→EXPIRED`
- [ ] AC4.5: Out-of-order: final status not downgraded
- [ ] AC4.6: Callback stored durably before 2xx

### Implementation Steps
1. Create `domain/entity/callback.go`
2. Create `port/outbound/callback_store.go`
3. Create `infrastructure/postgres/callback_store.go`
4. Create `infrastructure/midtrans/signature.go` for verification
5. Create `application/usecase/handle_webhook.go`
6. Add webhook handler

### Tests
- [ ] Unit: Signature verification (valid, invalid, empty)
- [ ] Integration: Idempotent duplicate handling (TC-PAY-05)
- [ ] Integration: Out-of-order safety

### Verify
```bash
# Use simulator to trigger webhooks
curl -X POST localhost:8083/api/v1/local-simulator/payments/{attemptId}/scenario -d '{"scenario":"SETTLE"}'
```

---

## Task 5: Settlement & Event Publishing (R5)

**Traces to:** Requirement 5, SRS-F-16, TC-PAY-03, TC-PAY-06

### Acceptance Criteria
- [ ] AC5.1: SUCCEEDED → append `AttemptSettled`, outbox `payment.succeeded` in same tx
- [ ] AC5.2: FAILED → append `AttemptFailed`, outbox `payment.failed`
- [ ] AC5.3: Outbox relay publishes with confirms, handles retries
- [ ] AC5.4: Settlement just before expiry → still processed (TC-PAY-06)

### Implementation Steps
1. Create outbox relay worker job
2. Wire publishing to `rusuh.events` with confirms

### Tests
- [ ] Integration: Settlement race with expiry (TC-PAY-06)

### Verify
```bash
# Check RabbitMQ for events
rabbitmqadmin get queue=booking.payment.events
```

---

## Task 6: Payment Status (R6)

**Traces to:** Requirement 6, SRS-F-14, TC-PAY-01

### Acceptance Criteria
- [ ] AC6.1: `GET /payments/{bookingId}/status` → 200 `PaymentStatus` from read model

### Implementation Steps
1. Create `application/usecase/get_payment_status.go`
2. Add handler

### Verify
```bash
curl localhost:8083/api/v1/payments/{bookingId}/status
```

---

## Task 7: Expiry & Resolve (R7)

**Traces to:** Requirement 7, SRS-F-17, TC-PAY-04, TC-PAY-07, TC-PAY-08

### Acceptance Criteria
- [ ] AC7.1: `POST /internal/v1/payments/{bookingId}/resolve` → check gateway, return `outcome`
- [ ] AC7.2: Expiry sweeper marks PENDING past `expires_at` as EXPIRED
- [ ] AC7.3: Settlement found after cancel → open `refund_cases`, return `SUCCEEDED`
- [ ] AC7.4: Gateway degraded → return `UNKNOWN`, let breaker stay OPEN

### Implementation Steps
1. Create `domain/entity/refund_case.go`
2. Create `port/outbound/refund_case_store.go`
3. Create `infrastructure/postgres/refund_case_store.go`
4. Create expiry sweeper worker job
5. Create `application/usecase/resolve_payment.go`
6. Add internal handler

### Tests
- [ ] Integration: Resolve outcomes (TC-PAY-04, TC-PAY-07)
- [ ] Integration: Breaker behavior during resolve (TC-PAY-08)

### Verify
```bash
curl -X POST localhost:8083/internal/v1/payments/{bookingId}/resolve -d '{"mode":"CHECK_AND_CANCEL"}'
```

---

## Task 8: Refund Cases via `booking.review` (R8)

**Traces to:** Requirement 8, SRS-F-17, TC-PAY-07

### Acceptance Criteria
- [ ] AC8.1: Consume `booking.review` → open refund case idempotent (UNIQUE `attempt_id`)
- [ ] AC8.2: Corrupt payload → `rabbitmq.Permanent` to DLQ

### Implementation Steps
1. Create consumer for `booking.review`
2. Register subscription in `RunWorker`

### Tests
- [ ] Integration: Idempotent refund case creation

### Verify
```bash
go test -tags integration -race ./...
```

---

## Task 9: Circuit Breaker Wiring (R9)

**Traces to:** Requirement 9, SRS-F-19, D-40, TC-RES-01, TC-RES-02

**Status:** `pkg/resilience` ✅ IMPLEMENTED, wiring ❌ TARGET

### Acceptance Criteria
- [ ] AC9.1: All gateway calls wrapped with `pkg/resilience.Breaker`
- [ ] AC9.2: State transitions: CLOSED→OPEN→HALF_OPEN→CLOSED
- [ ] AC9.3: Optional `Bulkhead` for concurrency limiting

### Implementation Steps
1. Wire breaker in gateway adapter
2. Configure thresholds from env

### Tests (Verify Existing)
```bash
go test ./pkg/resilience/... # TC-RES-01, TC-RES-02 already pass
```

---

## Task 10: Cancel Payment (R10)

**Traces to:** Requirement 10, SRS-F-13, TC-PAY-09

### Acceptance Criteria
- [ ] AC10.1: Cancel before payment → cancel at gateway, mark CANCELLED
- [ ] AC10.2: Already SUCCEEDED → keep PAID

### Implementation Steps
1. Create `application/usecase/cancel_payment.go`
2. Add cancel handler or integrate with booking cancel flow

### Verify
```bash
# Cancel booking triggers payment cancel
```

---

## Task 11: Security & Isolation (R11)

**Traces to:** Requirement 11, SRS-Q-SEC-*, TC-SEC-01..03

### Acceptance Criteria
- [ ] AC11.1: `/internal/v1/*` requires service bearer token
- [ ] AC11.2: Page token replay rejected (TC-SEC-01)
- [ ] AC11.3: No card data, server key, or tokens in events/logs

### Tests
- [ ] Integration: Internal API auth
- [ ] Unit: Payload sanitization

---

## Task 12: Simulator (Local Testing)

**Traces to:** Design §2.7

### Implementation Steps
1. Create `infrastructure/http/simulator_handler.go`
2. Implement scenarios: `SETTLE`, `SETTLE_LATE`, `EXPIRE`, `DENY`, `TIMEOUT`, `DUPLICATE_WEBHOOK`, `OUT_OF_ORDER`
3. Only enabled for `APP_ENV=local|ci`

### Verify
```bash
curl -X POST localhost:8083/api/v1/local-simulator/payments/{attemptId}/scenario -d '{"scenario":"SETTLE"}'
```

---

## Task 13: Health & Config (R12)

**Traces to:** Requirement 12, D-23

**Status:** Health ✅ IMPLEMENTED, Config subset ✅, Midtrans vars ❌ TARGET

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
| 1 | TC-PAY-01, TC-PAY-02, TC-PAY-10 |
| 2 | TC-MOB-01, TC-SEC-01 |
| 3 | TC-PAY-01, TC-PAY-08, TC-PAY-10, TC-RES-01 |
| 4 | TC-PAY-05, TC-PAY-11 |
| 5 | TC-PAY-03, TC-PAY-06 |
| 6 | TC-PAY-01 |
| 7 | TC-PAY-04, TC-PAY-07, TC-PAY-08 |
| 8 | TC-PAY-07 |
| 9 | TC-RES-01, TC-RES-02 |
| 10 | TC-PAY-09 |
| 11 | TC-SEC-01..03 |
| 12 | (local testing) |
| 13 | (CI verification) |
