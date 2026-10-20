# Tasks — Notification Service

| Field | Value |
|-------|-------|
| Service | Notification Service |
| Port | 8084 |
| Spec | `specs/notification-service/{requirements,design}.md` |
| Status | Boilerplate implemented; business logic is TARGET |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

---

## Pre-requisites

- [ ] Verify boilerplate compiles: `cd services/notification && go build ./...`
- [ ] Verify health endpoint: `go run ./cmd api &` then `curl localhost:8084/health`
- [ ] Ensure infra running: `make infra keys`
- [ ] Copy env: `cp .env.example .env`
- [ ] Run migrations: `go run ./cmd migrate up`
- [ ] Verify Mailpit running: `curl localhost:8025` (email UI)

---

## Task 1: E-ticket Email (R1)

**Traces to:** Requirement 1, SRS-F-20-01..03, TC-TKT-01, TC-TKT-02

### Acceptance Criteria
- [ ] AC1.1: Consume `booking.paid` → render `eticket` template, send email to `buyerEmail`
- [ ] AC1.2: Success → `deliveries` status `SENT` with `provider_message_id`
- [ ] AC1.3: Corrupt payload → `rabbitmq.Permanent` to DLQ, mark `DEAD`
- [ ] AC1.4: Email contains ticket codes, attendee names, masked NIK

### Implementation Steps
1. Create `domain/entity/delivery.go` with status enum
2. Create `domain/dto/booking_paid_payload.go`
3. Create `port/outbound/delivery_repository.go`
4. Create `port/outbound/email_sender.go`
5. Create `port/outbound/template_renderer.go`
6. Create `infrastructure/postgres/delivery_repository.go`
7. Create `infrastructure/smtp/email_sender.go`
8. Create `infrastructure/template/renderer.go` with `embed.FS`
9. Create `assets/templates/eticket.tmpl`
10. Create `application/usecase/notify_booking_paid.go`
11. Create consumer handler
12. Register subscription in `RunWorker`

### Tests
- [ ] Unit: Template rendering with all fields
- [ ] Integration: SMTP sending (Mailpit)
- [ ] Integration: Idempotent delivery (TC-MSG-03)

### Verify
```bash
go test -tags integration -race ./...
# Check Mailpit for email: http://localhost:8025
```

### Demo
- E-ticket email appears in Mailpit
- Contains ticket codes and masked NIK

---

## Task 2: Expiry Email (R2)

**Traces to:** Requirement 2, SRS-F-21-01..03, TC-MSG-01

### Acceptance Criteria
- [ ] AC2.1: Consume `booking.expired` → render `expired` template, send email
- [ ] AC2.2: SMTP down → retry via retry queue, send after recovery

### Implementation Steps
1. Create `assets/templates/expired.tmpl`
2. Create `application/usecase/notify_booking_expired.go`
3. Add to consumer handler dispatch

### Tests
- [ ] Integration: SMTP failure → retry → success (TC-MSG-01)

### Verify
```bash
# Check Mailpit for expiry email
```

---

## Task 3: Review Email (R3)

**Traces to:** Requirement 3, SRS-F-21-01..03, TC-MSG-01

### Acceptance Criteria
- [ ] AC3.1: Consume `booking.review` → render `review` template, send email

### Implementation Steps
1. Create `assets/templates/review.tmpl`
2. Create `application/usecase/notify_booking_review.go`
3. Add to consumer handler dispatch

### Verify
```bash
# Check Mailpit for review email
```

---

## Task 4: Turn Soon Push (R4)

**Traces to:** Requirement 4, SRS-F-25-01..13, TC-QUE-04

### Acceptance Criteria
- [ ] AC4.1: Consume `queue.turn.soon` → lookup subscription, send FCM push
- [ ] AC4.2: No subscription → skip (ack without error)
- [ ] AC4.3: Invalid FCM token → delete subscription, ack

### Implementation Steps
1. Create `domain/entity/push_subscription.go`
2. Create `port/outbound/subscription_repository.go`
3. Create `port/outbound/push_sender.go`
4. Create `infrastructure/postgres/subscription_repository.go`
5. Create `infrastructure/fcm/push_sender.go`
6. Create `application/usecase/notify_turn_soon.go`
7. Register `notification.queue.events` subscription

### Tests
- [ ] Unit: No subscription handling
- [ ] Integration: FCM mock or sandbox

### Verify
```bash
# Trigger queue.turn.soon and verify push (or mock)
```

---

## Task 5: Turn Arrived Push (R5)

**Traces to:** Requirement 5, SRS-F-25-01..13, TC-QUE-04

### Acceptance Criteria
- [ ] AC5.1: Consume `queue.turn.arrived` → send FCM with `claimDeadline`
- [ ] AC5.2: `claimDeadline` passed → skip (ack, set `expires_at`)
- [ ] AC5.3: No subscription or invalid token → same as R4

### Implementation Steps
1. Create `application/usecase/notify_turn_arrived.go`
2. Add deadline check before sending

### Tests
- [ ] Unit: Deadline check logic

### Verify
```bash
# Verify push includes claimDeadline
```

---

## Task 6: Delivery Tracking (R6)

**Traces to:** Requirement 6, SRS-F-21

### Acceptance Criteria
- [ ] AC6.1: One `deliveries` row per `(source_message_id, notification_type)`
- [ ] AC6.2: Track `attempts`, `last_error`, `provider_message_id`, `next_attempt_at`, `recipient_hash`

### Implementation Steps
1. Implement `ClaimDelivery` with `ON CONFLICT DO NOTHING`
2. Implement `MarkSent`, `MarkDead`, `BumpAttempt`, `MarkExpired`

### Tests
- [ ] Unit: State transitions

### Verify
```bash
# Query deliveries table after sending
```

---

## Task 7: Push Subscription API (R7)

**Traces to:** Requirement 7, SRS-F-25

### Acceptance Criteria
- [ ] AC7.1: `POST /push-subscriptions` with visitor token → 204, store encrypted token
- [ ] AC7.2: Existing subscription → upsert (idempotent)
- [ ] AC7.3: `DELETE /push-subscriptions/{eventId}` → 204
- [ ] AC7.4: Invalid fields → 400 `VALIDATION_ERROR`

### Implementation Steps
1. Create `domain/dto/push_subscription_request.go`
2. Create `application/usecase/subscribe_push.go`
3. Create `application/usecase/unsubscribe_push.go`
4. Create `infrastructure/http/subscription_handler.go`
5. Wire routes in `server.go`

### Tests
- [ ] Unit: Validation cases
- [ ] Integration: Upsert behavior

### Verify
```bash
curl -X POST -H "X-Visitor-Token: $TOKEN" localhost:8084/api/v1/push-subscriptions \
  -d '{"eventId":"...","platform":"ANDROID","pushToken":"..."}'
curl -X DELETE -H "X-Visitor-Token: $TOKEN" localhost:8084/api/v1/push-subscriptions/{eventId}
```

---

## Task 8: Idempotent Consumer (R8)

**Traces to:** Requirement 8, SRS-Q-REL-01, TC-MSG-03

### Acceptance Criteria
- [ ] AC8.1: Duplicate event → send exactly once via `ON CONFLICT DO NOTHING`
- [ ] AC8.2: Conflict on insert → conclude already processed, ack

### Implementation Steps
1. Implement `ClaimDelivery` as state guard before side effects
2. Use `rowsAffected == 0` to detect duplicate

### Tests
- [ ] Integration: Send same event twice → one email (TC-MSG-03)

### Verify
```bash
go test -tags integration -race ./...
```

---

## Task 9: Retry & DLQ (R9)

**Traces to:** Requirement 9, SRS-Q-REL-01, TC-MSG-01, TC-MSG-02, TC-MSG-04

### Acceptance Criteria
- [ ] AC9.1: Transient failure → republish to retry queue with delay from `RABBITMQ_RETRY_DELAYS`
- [ ] AC9.2: `attempt > MAX_RETRIES` → move to DLQ
- [ ] AC9.3: Corrupt payload → `rabbitmq.Permanent` directly to DLQ
- [ ] AC9.4: First retry has `x-rbt-attempt=2` (TC-MSG-04)

### Implementation Steps
1. Wire `pkg/rabbitmq` consumer with retry topology
2. Return appropriate errors for classification

### Tests
- [ ] Integration: Retry flow (TC-MSG-04)
- [ ] Integration: DLQ for permanent errors (TC-MSG-02)

### Verify
```bash
# Check DLQ: rabbitmqadmin get queue=notification.booking.events.dlq
```

---

## Task 10: Health Endpoint (R10)

**Traces to:** Requirement 10, D-23

**Status:** ✅ IMPLEMENTED

### Verify
```bash
curl localhost:8084/health
```

---

## Task 11: Config & Privacy (R11)

**Traces to:** Requirement 11, D-24

**Status:** Config ✅ IMPLEMENTED, Privacy enforcement ❌ TARGET

### Acceptance Criteria
- [ ] AC11.1: All env validated at start (implemented)
- [ ] AC11.2: No NIK/token plaintext in event payloads
- [ ] AC11.3: Recipient stored as `recipient_hash` (HMAC)
- [ ] AC11.4: No OTP code or PII in logs

### Implementation Steps
1. Verify payload validation
2. Implement `recipient_hash` storage
3. Review log statements

### Verify
```bash
# Grep logs for PII - should find nothing
```

---

## Task 12: OTP Login Email (R12)

**Traces to:** Requirement 12, SRS-F-27-02, SRS-F-27-12, TC-MSG-05, TC-MSG-06

### Acceptance Criteria
- [ ] AC12.1: Consume `otp.requested` → decrypt `emailEnc`/`codeEnc`, render `otp_login`, send email
- [ ] AC12.2: `expiresAt` passed → skip (ack, expired OTP not useful)
- [ ] AC12.3: Decryption fails → `rabbitmq.Permanent` to DLQ (TC-MSG-06)
- [ ] AC12.4: SMTP fails → NO retry (time-sensitive), user requests new OTP
- [ ] AC12.5: No OTP plaintext in logs (TC-USR-11)
- [ ] AC12.6: Email sent within 5 seconds (TC-MSG-05)

### Implementation Steps
1. Create `domain/dto/otp_requested_payload.go`
2. Create `port/outbound/pii_protector.go` with `UnsealEmail`, `UnsealOTP`
3. Create `infrastructure/security/pii_protector.go` using shared `PII_ENC_KEY`
4. Create `assets/templates/otp_login.tmpl`
5. Create `application/usecase/notify_otp_requested.go`
6. Register `notification.otp` subscription (NO retry queue)

### Tests
- [ ] Unit: Decryption failure handling
- [ ] Unit: Expiry check
- [ ] Integration: End-to-end OTP email (TC-MSG-05)
- [ ] Integration: Invalid encryption → DLQ (TC-MSG-06)

### Verify
```bash
# Request OTP via User Service, check Mailpit for email
curl -X POST localhost:8085/api/v1/auth/otp/request -d '{"email":"test@example.com"}'
# Check Mailpit: http://localhost:8025
```

### Demo
- OTP email arrives within 5 seconds
- Code visible in email, not in logs

---

## Final Verification

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
| 1 | TC-TKT-01, TC-TKT-02, TC-MSG-03 |
| 2 | TC-MSG-01 |
| 3 | TC-MSG-01 |
| 4 | TC-QUE-04 |
| 5 | TC-QUE-04 |
| 6 | TC-MSG-01 |
| 7 | — |
| 8 | TC-MSG-03 |
| 9 | TC-MSG-01, TC-MSG-02, TC-MSG-04 |
| 10 | (health) |
| 11 | (privacy) |
| 12 | TC-MSG-05, TC-MSG-06, TC-USR-11 |
