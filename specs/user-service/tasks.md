# Tasks — User Service

| Field | Value |
|-------|-------|
| Service | User Service |
| Port | 8085 |
| Spec | `specs/user-service/{requirements,design}.md` |
| Status | Boilerplate implemented; business logic is TARGET |

## Workflow

```
Read Spec → Implement → Test → (loop if error) → Verify ✓
```

---

## Pre-requisites

- [ ] Verify boilerplate compiles: `cd services/user && go build ./...`
- [ ] Verify health endpoint: `go run ./cmd api &` then `curl localhost:8085/health`
- [ ] Ensure infra running: `make infra keys`
- [ ] Copy env: `cp .env.example .env`

---

## Task 1: Request OTP (R1)

**Traces to:** Requirement 1, SRS-F-27-02, TC-USR-02, TC-USR-04, TC-USR-11

### Acceptance Criteria
- [ ] AC1.1: `POST /api/v1/auth/otp/request` with valid email → 202 with `expiresInSeconds`, `resendAfterSeconds`
- [ ] AC1.2: Response identical for registered/unregistered email (anti-enumeration)
- [ ] AC1.3: Cooldown active → 429 `OTP_RESEND_TOO_SOON` with `retryAfterSeconds`
- [ ] AC1.4: Per-email limit exceeded → 429 `RATE_LIMITED`, audit `OTP_RATE_LIMITED`
- [ ] AC1.5: Per-IP limit exceeded → 429 `RATE_LIMITED`
- [ ] AC1.6: Invalid email format → 400 `VALIDATION_ERROR`
- [ ] AC1.7: Publish `otp.requested` fails → delete OTP, return 503 `DEPENDENCY_UNAVAILABLE`
- [ ] AC1.8: Success → audit `OTP_SENT`, OTP encrypted in event payload

### Implementation Steps
1. Create `domain/dto/otp_request.go` with `OTPRequestInput`, `OTPRequestOutput`
2. Create `domain/valueobject/email.go` with validation
3. Create `port/outbound/otp_store.go` interface `IOTPStore` with `Start`, `Attempt`, `Clear`
4. Create `port/outbound/rate_limiter.go` interface `IRateLimiter` with `Allow`
5. Create `port/outbound/otp_publisher.go` interface `IOTPPublisher` with `PublishOTP`
6. Create `infrastructure/valkey/otp_store.go` adapter
7. Create `infrastructure/valkey/rate_limiter.go` adapter
8. Create `infrastructure/rabbitmq/otp_publisher.go` adapter
9. Create `application/usecase/request_otp.go` implementing the flow
10. Create `infrastructure/http/auth_handler.go` method `requestOTP`
11. Wire in `internal/delivery/server.go`

### Tests
- [ ] Unit: `usecase/request_otp_test.go` - all AC branches (table-driven)
- [ ] Unit: `valueobject/email_test.go` - validation cases
- [ ] Integration: `valkey/otp_store_integration_test.go` (tag `integration`)
- [ ] Integration: `valkey/rate_limiter_integration_test.go`

### Verify
```bash
cd services/user && go test -race ./...
curl -X POST localhost:8085/api/v1/auth/otp/request -d '{"email":"test@example.com"}'
```

### Demo
- `POST /api/v1/auth/otp/request` returns 202 with TTL info
- OTP event published to RabbitMQ (check with `rabbitmqadmin`)

---

## Task 2: Verify OTP & Login (R2)

**Traces to:** Requirement 2, SRS-F-27-03..05, TC-USR-03, TC-USR-05

### Acceptance Criteria
- [ ] AC2.1: Correct code → delete OTP, find-or-create account, create session, return 200 with tokens
- [ ] AC2.2: New account → create with encrypted email + hash + masked, audit `LOGIN_OK`
- [ ] AC2.3: OTP not in Valkey → 401 `OTP_EXPIRED`, audit `OTP_EXPIRED`
- [ ] AC2.4: Attempts > max → delete OTP, 429 `OTP_LOCKED`, audit `OTP_LOCKED`
- [ ] AC2.5: Wrong code (constant-time compare) → 401 `OTP_INVALID` with `attemptsLeft`
- [ ] AC2.6: Account DISABLED → 403 `ACCOUNT_DISABLED`
- [ ] AC2.7: Success → update `last_login_at`
- [ ] AC2.8: OTP hash bound to email_hash (code for one email invalid for another)

### Implementation Steps
1. Create `domain/entity/account.go` with status, encrypted fields
2. Create `domain/entity/session.go` with token hash, rotation support
3. Create `port/outbound/account_repository.go` interface
4. Create `port/outbound/session_repository.go` interface
5. Create `port/outbound/login_audit.go` interface
6. Create `infrastructure/postgres/account_repository.go`
7. Create `infrastructure/postgres/session_repository.go`
8. Create `infrastructure/postgres/login_audit.go`
9. Create `application/usecase/verify_otp.go` implementing flow
10. Create `infrastructure/http/auth_handler.go` method `verifyOTP`
11. Update `openSession` helper for token issuance

### Tests
- [ ] Unit: `usecase/verify_otp_test.go` - all branches including lockout
- [ ] Unit: constant-time compare test
- [ ] Integration: `postgres/account_repository_integration_test.go`

### Verify
```bash
go test -race ./...
# Request OTP first, then verify
curl -X POST localhost:8085/api/v1/auth/otp/verify -d '{"email":"test@example.com","code":"123456"}'
```

### Demo
- New user: account created, tokens returned
- Existing user: tokens returned, `last_login_at` updated

---

## Task 3: Refresh Token with Rotation & Replay Detection (R3)

**Traces to:** Requirement 3, SRS-F-27-06, TC-USR-06

### Acceptance Criteria
- [ ] AC3.1: Valid refresh token → rotate session atomically, return new tokens
- [ ] AC3.2: Empty/not found token → 401 `SESSION_INVALID`
- [ ] AC3.3: Revoked token reused → revoke ALL account sessions, audit `REFRESH_REUSED`, 401
- [ ] AC3.4: Expired session → 401 `SESSION_INVALID`
- [ ] AC3.5: Account not found → `SESSION_INVALID`; DISABLED → `ACCOUNT_DISABLED`
- [ ] AC3.6: Success → audit `REFRESH_OK`

### Implementation Steps
1. Add `Rotate` method to `ISessionRepository` (atomic: revoke old + create new)
2. Add `RevokeAllForAccount` method for replay detection
3. Implement `application/usecase/refresh.go`
4. Add `refresh` handler method
5. Implement replay detection logic

### Tests
- [ ] Unit: `usecase/refresh_test.go` - rotation, replay detection
- [ ] Integration: atomic rotation test with concurrent requests

### Verify
```bash
go test -race ./...
# Login first, then refresh
curl -X POST localhost:8085/api/v1/auth/refresh -d '{"refreshToken":"..."}'
```

### Demo
- Refresh returns new token pair
- Using revoked token triggers full account session revocation

---

## Task 4: Logout (R4)

**Traces to:** Requirement 4, SRS-F-27-07, TC-USR-07

### Acceptance Criteria
- [ ] AC4.1: Valid refresh token → revoke session, return 204
- [ ] AC4.2: Empty/unknown/revoked token → still return success (idempotent)
- [ ] AC4.3: Success → audit `LOGOUT`

### Implementation Steps
1. Add `Revoke` method to `ISessionRepository`
2. Implement `application/usecase/logout.go`
3. Add `logout` handler method

### Tests
- [ ] Unit: `usecase/logout_test.go` - idempotent behavior

### Verify
```bash
go test -race ./...
curl -X POST localhost:8085/api/v1/auth/logout -d '{"refreshToken":"..."}'
```

### Demo
- Logout returns 204
- Subsequent refresh with same token fails

---

## Task 5: Get Profile (R5)

**Traces to:** Requirement 5, SRS-F-27-08, TC-USR-08, TC-USR-12

### Acceptance Criteria
- [ ] AC5.1: Valid Bearer token → 200 with `accountId`, `emailMasked`, `fullName`, `nikMasked`, `hasNik`
- [ ] AC5.2: Email and NIK only in masked form, never plaintext
- [ ] AC5.3: No/invalid/expired token → 401 with `WWW-Authenticate: Bearer`
- [ ] AC5.4: Account not found → 401

### Implementation Steps
1. Create `domain/dto/profile_response.go`
2. Create `domain/mapper/account_mapper.go` with masking logic
3. Implement `application/usecase/get_profile.go`
4. Create `infrastructure/http/auth_handler.go` method `getMe`
5. Create `requireAuth` middleware

### Tests
- [ ] Unit: `usecase/get_profile_test.go`
- [ ] Unit: `mapper/account_mapper_test.go` - masking verification

### Verify
```bash
go test -race ./...
curl -H "Authorization: Bearer $TOKEN" localhost:8085/api/v1/me
```

### Demo
- Profile returns masked PII only

---

## Task 6: Update Profile (R6)

**Traces to:** Requirement 6, SRS-F-27-08..09, TC-USR-08

### Acceptance Criteria
- [ ] AC6.1: PATCH with `fullName` and/or `nik` → update only sent fields, return 200
- [ ] AC6.2: `fullName` outside 1-100 chars → 400 `VALIDATION_ERROR`
- [ ] AC6.3: `nik` not 16 numeric digits → 400 `VALIDATION_ERROR`
- [ ] AC6.4: Valid NIK → store encrypted + hash + masked
- [ ] AC6.5: NIK already linked to another account → 409 `NIK_ALREADY_LINKED`
- [ ] AC6.6: No fields sent → 400 `VALIDATION_ERROR`

### Implementation Steps
1. Create `domain/dto/update_profile_request.go`
2. Create `domain/valueobject/nik.go` with validation
3. Add `UpdateProfile` to `IAccountRepository`
4. Implement `application/usecase/update_profile.go`
5. Add `patchMe` handler method

### Tests
- [ ] Unit: `usecase/update_profile_test.go` - all validation cases
- [ ] Unit: `valueobject/nik_test.go`
- [ ] Integration: NIK uniqueness constraint test

### Verify
```bash
go test -race ./...
curl -X PATCH -H "Authorization: Bearer $TOKEN" localhost:8085/api/v1/me -d '{"fullName":"Test User"}'
```

### Demo
- Profile updated with masked NIK returned

---

## Task 7: Token Claims for Service Validation (R7)

**Traces to:** Requirement 7, SRS-F-27-10, TC-USR-09, TC-USR-09a

**Status:** ✅ IMPLEMENTED (P0 Enhancement Complete)

### Verification Only
- [ ] Verify `pkg/token.Claims` has `Email`, `NIKHash`, `HasNIK` fields
- [ ] Verify `IssueAccess` populates identity claims
- [ ] Verify Queue/Booking can validate NIK offline from token

### Tests (Verify Existing)
```bash
cd services/user && go test -race ./internal/user/infrastructure/security/...
```

---

## Task 8: Health Endpoint (R8)

**Traces to:** Requirement 8, D-23

**Status:** ✅ IMPLEMENTED

### Verification Only
- [ ] `GET /health` returns 200 UP when Postgres & Valkey healthy
- [ ] Returns 503 DOWN when dependency fails
- [ ] No routes except `/health` when dependencies not wired (unit test mode)

### Tests (Verify Existing)
```bash
curl localhost:8085/health
go test -race ./internal/delivery/...
```

---

## Task 9: Build & Verify (R9)

**Traces to:** Requirement 9

### Acceptance Criteria
- [ ] AC9.1: `go run ./cmd migrate up` applies migrations
- [ ] AC9.2: `go run ./cmd api` starts on port 8085
- [ ] AC9.3: Invalid config → fail start with clear error
- [ ] AC9.4: `make build` compiles without error (including isolation)
- [ ] AC9.5: `make test` passes all unit tests with `-race`
- [ ] AC9.6: `make test-integration` passes against real infra
- [ ] AC9.7: `make lint` passes golangci-lint + depguard
- [ ] AC9.8: Coverage ≥ 80% (`make cover`)

### Final Verification
```bash
make build
make test
make test-integration
make lint
make cover
make ci
```

### Demo
- All CI checks pass
- Service starts and handles requests

---

## Test Case Alignment

| Task | Test Cases |
|------|------------|
| 1 | TC-USR-02, TC-USR-04, TC-USR-11 |
| 2 | TC-USR-03, TC-USR-05 |
| 3 | TC-USR-06 |
| 4 | TC-USR-07 |
| 5 | TC-USR-08, TC-USR-12 |
| 6 | TC-USR-08 |
| 7 | TC-USR-09, TC-USR-09a |
| 8 | (health integration) |
| 9 | (CI verification) |
