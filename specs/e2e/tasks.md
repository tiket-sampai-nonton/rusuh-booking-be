# Tasks — End-to-End Integration

| Field | Value |
|-------|-------|
| Type | E2E Integration Testing |
| Prerequisite | All 5 services implemented and passing unit/integration tests |
| Test Fixtures | `tests/features/`, `tests/apidog/`, `tests/k6/` |

> Catatan penggabungan (2026-10-21): file ini dari DEV-2 dan menjadi rincian platform Requirement 10
> (`platform/tasks.md` task 10). Journey 2, 3, 4, dan 6 baru bisa lulus setelah rework yang memutus alur
> selesai: booking 6.R1, 10.R1, 12.R1, 4.R1, Task 13; queue 8.R1, 11.R1. Journey 4 juga butuh
> Payment Service (belum ada).

## Workflow

```
All Services Pass → E2E Tests → Verify Full Journey ✓
```

---

## Pre-requisites

- [ ] All 5 services pass `make ci`
- [ ] All services running: `make up`
- [ ] Database seeded with test event
- [ ] Mailpit running for email verification

---

## Task 1: User Login Journey (J-06)

**Traces to:** E2E-FLOW-DIAGRAMS Journey J-06

### Test Flow
1. Request OTP: `POST /api/v1/auth/otp/request`
2. Verify OTP arrives in Mailpit (< 5s)
3. Verify OTP: `POST /api/v1/auth/otp/verify`
4. Get profile: `GET /api/v1/me`
5. Update profile with NIK: `PATCH /api/v1/me`
6. Verify token has `has_nik=true`

### Acceptance Criteria
- [ ] AC1.1: OTP email delivered within 5 seconds
- [ ] AC1.2: Login returns valid access + refresh tokens
- [ ] AC1.3: Profile shows masked email, no plaintext
- [ ] AC1.4: NIK update reflects in token claims

### Verify
```bash
# Run BDD tests
cd tests/features && godog --tags=@user-login
```

---

## Task 2: Queue Join to Admission Journey

**Traces to:** E2E-FLOW-DIAGRAMS Journey J-01, J-02

### Test Flow
1. Guest joins queue: `POST /events/{id}/queue-entries`
2. Poll status: `GET /queue-status/{id}`
3. Get my entry: `GET /queue-entries/me`
4. Wait for promotion (Promoter worker)
5. Claim turn: `POST /queue-entries/{id}/claim`
6. Receive admission token

### Acceptance Criteria
- [ ] AC2.1: Join returns monotonic seq
- [ ] AC2.2: Status document shows correct zone
- [ ] AC2.3: Promotion maintains FIFO order
- [ ] AC2.4: Claim returns valid admission token

### Verify
```bash
cd tests/features && godog --tags=@queue-admission
```

---

## Task 3: Booking Session to Hold Journey

**Traces to:** E2E-FLOW-DIAGRAMS Journey J-03

### Test Flow
1. Create session with admission token: `POST /booking-sessions`
2. Check attendee NIK: `POST /booking-sessions/{id}/attendee-checks`
3. Hold booking: `POST /bookings`
4. Verify booking status `PENDING_PAYMENT`
5. Edit booking: `PUT /bookings/{id}/items`
6. Verify version incremented

### Acceptance Criteria
- [ ] AC3.1: Session created with correct TTL
- [ ] AC3.2: NIK validation returns pass/fail correctly
- [ ] AC3.3: Hold is atomic (no oversell)
- [ ] AC3.4: Edit increments version

### Verify
```bash
cd tests/features && godog --tags=@booking-hold
```

---

## Task 4: Payment Journey

**Traces to:** E2E-FLOW-DIAGRAMS Journey J-04

### Test Flow
1. Get payment page: `GET /payments/{id}/page`
2. Create attempt: `POST /payments/{id}/attempts`
3. Trigger webhook: bayar lewat halaman simulator Midtrans Sandbox, atau kirim notifikasi bertanda tangan seperti `tests/e2e` (tidak ada endpoint `local-simulator`)
4. Verify `payment.succeeded` published
5. Verify booking status `PAID`

### Acceptance Criteria
- [ ] AC4.1: Payment page shows correct amount
- [ ] AC4.2: Attempt returns payment instruction
- [ ] AC4.3: Webhook settles payment
- [ ] AC4.4: Booking transitions to PAID

### Verify
```bash
cd tests/features && godog --tags=@payment
```

---

## Task 5: E-ticket Email Journey

**Traces to:** E2E-FLOW-DIAGRAMS Journey J-05

### Test Flow
1. After payment success (Task 4)
2. Verify `booking.paid` consumed by Notification
3. Check Mailpit for e-ticket email
4. Verify ticket codes in email

### Acceptance Criteria
- [ ] AC5.1: Email delivered within 10 seconds of payment
- [ ] AC5.2: Email contains all ticket codes
- [ ] AC5.3: NIK shown as masked only
- [ ] AC5.4: Attendee names present

### Verify
```bash
# Check Mailpit: http://localhost:8025
cd tests/features && godog --tags=@eticket
```

---

## Task 6: Expiry Journey

**Traces to:** TC-PAY-04, TC-SES-05..08

### Test Flow
1. Create booking, don't pay
2. Wait for TTL expiry
3. Verify session sweeper runs
4. Verify booking status `EXPIRED`
5. Verify stock released
6. Check expiry email in Mailpit

### Acceptance Criteria
- [ ] AC6.1: Session expires after TTL
- [ ] AC6.2: Stock released to inventory
- [ ] AC6.3: Expiry email sent
- [ ] AC6.4: User can rejoin queue

### Verify
```bash
cd tests/features && godog --tags=@expiry
```

---

## Task 7: Concurrent Load Test

**Traces to:** TC-PERF-01..05, TC-BKG-07

### Test Flow
1. Run k6 load test: 100 concurrent holds on 50 tickets
2. Verify exactly 50 succeed
3. Verify no oversell
4. Verify queue handles 10k+ joins

### Acceptance Criteria
- [ ] AC7.1: Exactly 50 of 100 concurrent holds succeed (TC-BKG-07)
- [ ] AC7.2: No seq gaps in queue (TC-QUE-08)
- [ ] AC7.3: Status document cache-friendly (TC-PERF-01)
- [ ] AC7.4: No deadlocks under load

### Verify
```bash
cd tests/k6 && k6 run booking_load.js
cd tests/k6 && k6 run queue_load.js
```

### Demo
- Terminal shows exactly 50 successful bookings
- Grafana/logs show no oversell errors

---

## Task 8: Full Journey Integration

### Test Flow (Complete User Journey)
1. User requests OTP, logs in
2. User joins queue with NIK
3. User gets promoted, claims turn
4. User creates booking session
5. User holds tickets
6. User pays via GOPAY
7. Webhook settles payment
8. User receives e-ticket email
9. User can view tickets

### Acceptance Criteria
- [ ] AC8.1: Full journey completes in < 60s (excluding queue wait)
- [ ] AC8.2: All events published correctly
- [ ] AC8.3: All emails delivered
- [ ] AC8.4: Tickets accessible after payment

### Verify
```bash
# Run full E2E suite
cd tests/apidog && newman run collection.json
```

---

## Task 9: Failure Scenarios

### Test Flow
1. Queue join with invalid NIK → 422
2. Hold with exhausted quota → 409
3. Claim after deadline → 410
4. Payment with breaker open → 503
5. Webhook with invalid signature → 401
6. Edit after payment started → 409

### Acceptance Criteria
- [ ] AC9.1: All error codes match spec
- [ ] AC9.2: Error responses include `code` and `message`
- [ ] AC9.3: No sensitive data in error responses
- [ ] AC9.4: System remains consistent after errors

### Verify
```bash
cd tests/features && godog --tags=@error-scenarios
```

---

## Task 10: Cross-Service Event Flow

### Test Flow
1. Verify `booking.journey.ended` → Queue releases slot
2. Verify `booking.quota.exhausted` → Queue blocks NIK
3. Verify `booking.sold_out` → Queue closes
4. Verify `payment.succeeded` → Booking settles
5. Verify all events idempotent (send twice)

### Acceptance Criteria
- [ ] AC10.1: Events delivered at-least-once
- [ ] AC10.2: Consumers handle duplicates correctly
- [ ] AC10.3: No data inconsistency after event replay
- [ ] AC10.4: DLQ contains only corrupt messages

### Verify
```bash
go test -tags integration -race ./tests/...
# Check DLQs are empty except for intentional corrupt messages
rabbitmqadmin list queues name messages | grep dlq
```

---

## Final Verification

```bash
# All services
cd rusuh-booking-be && make ci

# E2E
make up
cd tests/features && godog
cd tests/apidog && newman run collection.json
cd tests/k6 && k6 run smoke.js

# Coverage report
make cover
```

---

## Success Criteria

- [ ] All unit tests pass (`make test`)
- [ ] All integration tests pass (`make test-integration`)
- [ ] Coverage ≥ 80% per service
- [ ] All E2E journeys pass
- [ ] No oversell in load test (TC-BKG-07: exactly 50 of 100)
- [ ] Emails arrive in Mailpit
- [ ] All events idempotent
- [ ] No sensitive data in logs/responses

---

## Test Case Alignment Summary

| Journey | Test Cases |
|---------|------------|
| User Login | TC-USR-02..05, TC-MSG-05 |
| Queue Admission | TC-QUE-01..14, TC-GST-01..05 |
| Booking Hold | TC-BKG-01..12, TC-ADM-01..05 |
| Payment | TC-PAY-01..11, TC-SEC-01..03 |
| E-ticket | TC-TKT-01..03 |
| Expiry | TC-SES-05..08, TC-PAY-04 |
| Load | TC-PERF-01..05, TC-BKG-07 |
| Events | TC-API-01..02, TC-CAP-01..03 |
