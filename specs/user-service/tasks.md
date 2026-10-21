# Tasks — User Service

| Field | Value |
|-------|-------|
| Service | User Service |
| Port | 8085 |
| Spec | `specs/user-service/{requirements,design}.md` |
| Status | **All business logic IMPLEMENTED.** Verified `GOWORK=off go build ./...` + `go test -race ./...` pass (2026-10-20). Remaining work is integration-test coverage + lint cleanup. |

## How to read this document

Each task is tagged `[DONE]` or `[TODO]`.

- **DONE** — code + unit tests exist and the package compiles/tests green. The task
  lists *verification steps only* (what to assert the code still does) and the real
  files/functions that satisfy each AC.
- **TODO** — not yet written (or only partially). The task lists exact file paths,
  function signatures, the `pkg/` helpers to use, and the pattern file to copy.

Error codes and HTTP status are the **code truth** per `port/inbound/errors.go` and
design §6 (note deltas D5/D12: `OTP_INVALID`/`OTP_EXPIRED`=401, `OTP_LOCKED`=429,
`ACCOUNT_DISABLED`=403, `DEPENDENCY_UNAVAILABLE`=503).

## Shared building blocks (already wired — do not recreate)

| Concern | Where | Notes |
|---------|-------|-------|
| Composition root | `services/user/internal/delivery/user_module.go` → `newUserModule` | Wires all adapters into `usecase.NewAuth` / `usecase.NewProfile` / `userhttp.NewAuthHandler`. Returns `noRoutes{}` (health only) when `clients.PG == nil || clients.VK == nil`. |
| HTTP envelope | `pkg/httpx` — `DecodeJSON`, `WriteData`, `WriteError`, `ClientIP`, `BearerToken` | Error mapping flows through `pkg/apperror`. |
| App error wrap | `pkg/apperror` — `apperror.Internal(err)`, `inbound.Validation(field, err)` | Domain errors live in `port/inbound/errors.go`. |
| Crypto | `pkg/crypto` — `ParseKey`, `NewHasher`, `NewSealer`; `pkg/token` — EdDSA signer/verifier | Used only inside `infrastructure/security`. |
| Broker | `pkg/rabbitmq` — `Dial`, `NewPublisher`, `NewEnvelope`, `Publisher.Publish` | OTP publish adapter wraps this. |
| Clock | `pkg/clock` — `clock.System`, `clock.IClock` | Injected into `Auth` for testability. |

## Pre-requisites (run before verifying)

```bash
cd services/user
GOWORK=off go build ./...          # must compile (isolation build)
make infra keys                    # Postgres + Valkey + RabbitMQ + token/PII keys
cp .env.example .env               # if not present
go run ./cmd migrate up            # apply 0001_init (schema users)
```

---

## Task 1: Request OTP (R1) [DONE]

**Traces to:** Requirement 1, SRS-F-27-02, TC-USR-02, TC-USR-04, TC-USR-11
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/auth.go` — `(*Auth).RequestOTP(ctx, dto.RequestOTPRequest, inbound.ClientInfo) (dto.RequestOTPResponse, error)` implements the full §3.1 flow.
- `internal/user/domain/valueobject/values.go` — `NewEmail(string) (Email, error)` validation.
- `internal/user/infrastructure/valkey/otp_store.go` — `(*OTPStore).Start` enforces resend cooldown atomically via `SET cdKey NX PX`.
- `internal/user/infrastructure/valkey/otp_store.go` — `(*RateLimiter).Allow` fixed-window per-email + per-IP.
- `internal/user/infrastructure/rabbitmq/otp_publisher.go` — `(*OTPPublisher).PublishOTP(ctx, emailEnc, codeEnc []byte, expiresAt time.Time) error`; encrypts both before publishing (`otp.requested`, exchange `rusuh.events`).
- `internal/user/infrastructure/security/security.go` — `(*PII).SealEmail`, `(*PII).SealOTP`, `(*PII).HashOTP(emailHash, code)` (bound to email), `Secrets.OTPCode()`.
- `internal/user/infrastructure/http/auth_handler.go` — `(*AuthHandler).requestOTP` → `202` + `Cache-Control: no-store`.

### Acceptance Criteria (verify)
- [ ] AC1.1: valid email → `202` with `{expiresInSeconds, resendAfterSeconds}` (from `AuthSettings.OTPTTL`/`ResendCooldown`).
- [ ] AC1.2: anti-enumeration — identical `202` whether or not email is registered (RequestOTP never touches the account repo).
- [ ] AC1.3: cooldown active (`OTP.Start` returns `started=false`) → `429 OTP_RESEND_TOO_SOON` with `retryAfterSeconds` detail.
- [ ] AC1.4: per-email limit exceeded → `429 RATE_LIMITED` + audit `OTP_RATE_LIMITED`.
- [ ] AC1.5: per-IP limit exceeded → `429 RATE_LIMITED`.
- [ ] AC1.6: invalid email → `400 VALIDATION_ERROR` field `email`.
- [ ] AC1.7: `Publisher.PublishOTP` error → `OTP.Clear(key)` then `503 DEPENDENCY_UNAVAILABLE` (`inbound.ErrPublishFailed`).
- [ ] AC1.8: success → audit `OTP_SENT`; email + code only published encrypted (verify no plaintext in payload).

### Tests (exist)
**File:** `internal/user/application/usecase/auth_test.go`
```go
func TestRequestOTP_Success(t *testing.T)   // asserts 202 payload + OTP_SENT audit + PublishOTP called with encrypted args
func TestRequestOTP_Failures(t *testing.T)  // table: rate-limited→RATE_LIMITED; cooldown→OTP_RESEND_TOO_SOON; publish fail→DEPENDENCY_UNAVAILABLE + Clear
```
**File:** `internal/user/domain/valueobject/values_test.go`
```go
func TestNewEmail(t *testing.T)  // valid/invalid format + length
```
**File:** `internal/user/infrastructure/rabbitmq/otp_publisher_test.go`
```go
func TestOTPPublisher_PublishOTP_EnvelopeShape(t *testing.T) // envelope fields + base64 payload
func TestOTPPublisher_RoutingKey(t *testing.T)               // otp.requested
func TestOTPPublisher_ProducerName(t *testing.T)             // "user"
func TestOTPPublisher_PayloadHasNoPlaintext(t *testing.T)    // asserts email/code never appear plaintext
func TestOTPPublisher_PublishError_Propagated(t *testing.T)  // publisher error bubbles up
```
**File (integration, tag `integration`):** `internal/user/infrastructure/valkey/otp_store_integration_test.go`
```go
func TestOTPStore_Lifecycle(t *testing.T) // Start/Attempt/Clear + cooldown blocks resend
func TestRateLimiter(t *testing.T)        // fixed-window Allow boundary
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/application/usecase/... ./internal/user/infrastructure/rabbitmq/... ./internal/user/domain/valueobject/...
```

---

## Task 2: Verify OTP & Login (R2) [DONE]

**Traces to:** Requirement 2, SRS-F-27-03..05, TC-USR-03, TC-USR-05
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/auth.go` — `(*Auth).VerifyOTP(ctx, dto.VerifyOTPRequest, inbound.ClientInfo) (dto.TokenResponse, error)` implements §3.2.
- Constant-time compare: `subtle.ConstantTimeCompare(ch.CodeHash, PII.HashOTP(emailHash, code))`.
- Find-or-create: `Accounts.FindByEmailHash` → `Accounts.Create` (encrypted email + hash + masked).
- `internal/user/infrastructure/postgres/repositories.go` — `(*Accounts).FindByEmailHash`, `(*Accounts).Create` (idempotent `ON CONFLICT (email_hash)`), `(*Accounts).TouchLogin`.
- `internal/user/infrastructure/postgres/repositories.go` — `(*Audit).Record` writes one `login_audit` row.
- Session opened via `(*Auth).openSession(ctx, acc, email, client, now, rotateFrom="")`.

### Acceptance Criteria (verify)
- [ ] AC2.1: correct code → `OTP.Clear`, find-or-create, open session, `200` `dto.TokenResponse` (`tokenType`,`accessToken`,`accessTokenExpiresAt`,`refreshToken`,`refreshTokenExpiresAt`,`account`).
- [ ] AC2.2: new account → create with `EmailEnc`/`EmailHash`/`EmailMasked`; audit `LOGIN_OK`.
- [ ] AC2.3: OTP not found (`Attempt` → `found=false`) → `401 OTP_EXPIRED` + audit `OTP_EXPIRED`.
- [ ] AC2.4: `ch.Attempts > MaxAttempts` → `OTP.Clear`, `429 OTP_LOCKED` + audit `OTP_LOCKED`.
- [ ] AC2.5: wrong code → `401 OTP_INVALID` with `attemptsLeft` detail + audit `OTP_INVALID`.
- [ ] AC2.6: `!acc.Active()` → `403 ACCOUNT_DISABLED`.
- [ ] AC2.7: success → `Accounts.TouchLogin(now)`.
- [ ] AC2.8: OTP hash bound to `email_hash` via `HashOTP(emailHash, code)` — code for one email fails for another.

### Tests (exist)
**File:** `internal/user/application/usecase/auth_test.go`
```go
func TestVerifyOTP_ExistingAccount(t *testing.T)          // 200 + tokens + LOGIN_OK + TouchLogin
func TestVerifyOTP_FirstLoginCreatesAccount(t *testing.T) // Create called; account in response
func TestVerifyOTP_Failures(t *testing.T)                 // table: expired→OTP_EXPIRED; locked→OTP_LOCKED+Clear; wrong→OTP_INVALID+attemptsLeft; disabled→ACCOUNT_DISABLED
```
**File (integration):** `internal/user/infrastructure/postgres/repositories_integration_test.go`
```go
func TestAccounts(t *testing.T) // Create/FindByEmailHash/GetByID/TouchLogin + encrypted columns present
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/application/usecase/...
```

---

## Task 3: Refresh Token with Rotation & Replay Detection (R3) [DONE]

**Traces to:** Requirement 3, SRS-F-27-06, TC-USR-06
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/auth.go` — `(*Auth).Refresh(ctx, dto.RefreshRequest, inbound.ClientInfo) (dto.TokenResponse, error)` implements §3.4 refresh flow.
- Replay detection: `sess.Revoked()` → `Sessions.RevokeAllForAccount(accountID, now)` + audit `REFRESH_REUSED` → `401 SESSION_INVALID`.
- Atomic rotation: `internal/user/infrastructure/postgres/repositories.go` — `(*Sessions).Rotate(ctx, oldID, next, at)` inserts new + revokes old (`revoked_at` + `replaced_by`) in one `pkgpg.WithTx`; single-winner → `entity.ErrRevoked` → `SESSION_INVALID`.
- `(*Sessions).FindByTokenHash`, `(*Sessions).RevokeAllForAccount`.
- Email for token claims fetched via `Accounts.GetEmailEncByID` + `PII.UnsealEmail` (Refresh has no email in context).

### Acceptance Criteria (verify)
- [ ] AC3.1: valid token → atomic `Rotate`, new token pair, `200`.
- [ ] AC3.2: empty or not-found token → `401 SESSION_INVALID`.
- [ ] AC3.3: revoked token reused → `RevokeAllForAccount` + audit `REFRESH_REUSED` + `401 SESSION_INVALID`.
- [ ] AC3.4: expired session (`sess.Expired(now)`) → `401 SESSION_INVALID`.
- [ ] AC3.5: account not found → `SESSION_INVALID`; `DISABLED` → `ACCOUNT_DISABLED`.
- [ ] AC3.6: success → audit `REFRESH_OK`.

### Tests (exist)
**File:** `internal/user/application/usecase/auth_test.go` — Refresh cases covered within the auth test suite (rotation success, replay → RevokeAllForAccount + REFRESH_REUSED, expired, disabled).
**File (integration):** `internal/user/infrastructure/postgres/repositories_integration_test.go`
```go
func TestSessions_RotateIsSingleWinner(t *testing.T) // two concurrent rotates on same session: one wins, other ErrRevoked
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/application/usecase/...
make test-integration   # exercises TestSessions_RotateIsSingleWinner
```

---

## Task 4: Logout (R4) [DONE]

**Traces to:** Requirement 4, SRS-F-27-07, TC-USR-07
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/auth.go` — `(*Auth).Logout(ctx, dto.RefreshRequest, inbound.ClientInfo) error` implements §3.5 (idempotent).
- `internal/user/infrastructure/postgres/repositories.go` — `(*Sessions).Revoke(ctx, id, at)` (idempotent `WHERE revoked_at IS NULL`).
- `internal/user/infrastructure/http/auth_handler.go` — `(*AuthHandler).logout` → `204 No Content`.

### Acceptance Criteria (verify)
- [ ] AC4.1: valid token → `Sessions.Revoke` + `204`.
- [ ] AC4.2: empty / unknown / already-revoked token → still succeeds (no error), `204`.
- [ ] AC4.3: a real revoke → audit `LOGOUT`.

### Tests (exist)
Covered by the handler flow test and the auth usecase suite.
**File:** `internal/user/infrastructure/http/auth_handler_test.go`
```go
func TestVerifyRefreshLogout(t *testing.T) // full login→refresh→logout round-trip incl. 204 + idempotency
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/infrastructure/http/... ./internal/user/application/usecase/...
```

---

## Task 5: Get Profile (R5) [DONE]

**Traces to:** Requirement 5, SRS-F-27-08, TC-USR-08, TC-USR-12
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/profile.go` — `(*Profile).GetProfile(ctx, accountID string) (dto.ProfileResponse, error)`.
- `internal/user/domain/mapper/account_mapper.go` — `ToProfileResponse(acc)` returns masked-only fields.
- `internal/user/domain/entity/account.go` — entity carries only `EmailMasked`/`NIKMasked` (plaintext never leaves usecase); `HasNIK()`.
- `internal/user/infrastructure/http/auth_handler.go` — `(*AuthHandler).getMe` + `requireAuth` middleware (Bearer → `verifier.VerifyAccess` → accountID in ctx; failure sets `WWW-Authenticate: Bearer`).
- Error mapping: `mapAccountErr` maps `entity.ErrNotFound` → `inbound.ErrUnauthorized` (401).

### Acceptance Criteria (verify)
- [ ] AC5.1: valid Bearer → `200` `{accountId, emailMasked, fullName, nikMasked, hasNik}`.
- [ ] AC5.2: email + NIK masked only, never plaintext (entity has no plaintext fields).
- [ ] AC5.3: missing/invalid/expired token → `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer`.
- [ ] AC5.4: account not found → `401 UNAUTHORIZED`.

### Tests (exist)
**File:** `internal/user/application/usecase/profile_test.go`
```go
func TestGetProfile(t *testing.T) // masked fields + ErrNotFound → Unauthorized
```
**File:** `internal/user/domain/mapper/account_mapper_test.go`
```go
func TestToTokenResponse(t *testing.T) // token response mapping (profile masking asserted alongside)
```
**File:** `internal/user/infrastructure/http/auth_handler_test.go`
```go
func TestMe(t *testing.T) // GET /me with Bearer → 200 masked; missing token → 401 + WWW-Authenticate
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/application/usecase/... ./internal/user/domain/mapper/... ./internal/user/infrastructure/http/...
```

---

## Task 6: Update Profile (R6) [DONE]

**Traces to:** Requirement 6, SRS-F-27-08..09, TC-USR-08
**Status:** DONE — verify only

### What exists
- `internal/user/application/usecase/profile.go` — `(*Profile).UpdateProfile(ctx, accountID string, dto.UpdateProfileRequest) (dto.ProfileResponse, error)`.
- `internal/user/domain/valueobject/values.go` — `NewNIK(string) (NIK, error)` (16 numeric digits).
- `internal/user/infrastructure/security/security.go` — `(*PII).SealNIK`, `(*PII).HashNIK` (shared HMAC key with Queue/Booking).
- `internal/user/infrastructure/postgres/repositories.go` — `(*Accounts).UpdateProfile` partial update; `uq_accounts_nik` unique violation → `entity.ErrNIKTaken` → `409 NIK_ALREADY_LINKED`.
- Name length 1–100 enforced in usecase (`utf8.RuneCountInString`); no fields → `inbound.ErrNothingToUpdate` (400).

### Acceptance Criteria (verify)
- [ ] AC6.1: PATCH with `fullName` and/or `nik` → partial update of sent fields only → `200`.
- [ ] AC6.2: `fullName` outside 1–100 → `400 VALIDATION_ERROR` field `fullName`.
- [ ] AC6.3: `nik` not 16 numeric → `400 VALIDATION_ERROR` field `nik`.
- [ ] AC6.4: valid NIK → stored as `nik_enc` + `nik_hash` + `nik_masked`.
- [ ] AC6.5: NIK linked to another account → `409 NIK_ALREADY_LINKED`.
- [ ] AC6.6: no fields → `400 VALIDATION_ERROR` ("tidak ada field yang diubah").

### Tests (exist)
**File:** `internal/user/application/usecase/profile_test.go`
```go
func TestUpdateProfile(t *testing.T) // table: name OOB→VALIDATION; bad nik→VALIDATION; no fields→ErrNothingToUpdate; ErrNIKTaken→NIK_ALREADY_LINKED; happy path seal+hash+mask
```
**File:** `internal/user/domain/valueobject/values_test.go`
```go
func TestNewNIK(t *testing.T) // 16-digit numeric validation
```
**File (integration):** `internal/user/infrastructure/postgres/repositories_integration_test.go` — `TestAccounts` covers NIK uniqueness constraint path.

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/application/usecase/... ./internal/user/domain/valueobject/...
```

---

## Task 7: Token Claims for Service Validation (R7) [DONE]

**Traces to:** Requirement 7, SRS-F-27-10, TC-USR-09, TC-USR-09a
**Status:** DONE — verify only (P0 enhancement complete 2026-10-20)

### What exists
- `pkg/token/token.go` — `Claims` struct has `Email`, `NIKHash` (hex, `omitempty`), `HasNIK`.
- `internal/user/port/outbound/services.go` — `ITokenIssuer.IssueAccess(accountID, email string, nikHash []byte, now time.Time) (token string, expiresAt time.Time, err error)`.
- `internal/user/infrastructure/security/security.go` — `(*Tokens).IssueAccess` populates `Email` (always), `HasNIK = len(nikHash) > 0`, `NIKHash = hex(nikHash)`; EdDSA sign, `scope="buyer"`, `jti`, leeway via verifier.
- `internal/user/port/outbound/repositories.go` — `IAccountRepository.GetNIKHashByID(ctx, id string) ([]byte, error)` (nil if no NIK), `GetEmailEncByID(ctx, id string) ([]byte, error)`.
- `internal/user/infrastructure/security/security.go` — `(*Tokens).VerifyAccess(tok) (string, error)` checks sig/issuer/audience/exp, returns `sub`.
- Wiring: `(*Auth).openSession` fetches `nikHash` via `GetNIKHashByID`; email from VerifyOTP input or (Refresh) via `GetEmailEncByID`+`UnsealEmail`.

### Acceptance Criteria (verify)
- [ ] AC7.1: access token JWT EdDSA carries `iss, sub, aud, jti, scope="buyer", iat, exp` + identity claims.
- [ ] AC7.2: public key shared via `kid` (`NewVerifier` map in `user_module.go`) → Queue/Booking verify offline.
- [ ] AC7.3: verify checks sig/iss/aud/exp (5s leeway) → returns `sub` as accountID.
- [ ] AC7.4 (D1): token with NIK → `has_nik=true`, `nik_hash` hex; without NIK → `has_nik=false`, empty `nik_hash`; email always present.

### Tests (exist)
**File:** `internal/user/infrastructure/security/security_test.go`
```go
func TestPII(t *testing.T)               // hash/seal/unseal roundtrips, HashOTP binding
func TestSecrets(t *testing.T)           // OTPCode 6-digit, RefreshToken entropy
func TestTokens_IssueAndVerify(t *testing.T) // issue→verify; claims with/without NIK; email present; sub == accountID
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/infrastructure/security/...
```

---

## Task 8: Health Endpoint (R8) [DONE]

**Traces to:** Requirement 8, D-23
**Status:** DONE — verify only

### What exists
- `internal/user/infrastructure/healthcheck/checker.go` — `CheckAll` probes Postgres + Valkey.
- `internal/user/infrastructure/http/health_handler.go` — `GET /health` → `200 UP` / `503 DOWN`.
- `internal/delivery/user_module.go` — returns `noRoutes{}` (health-only) when PG/VK not wired (unit-test mode).

### Acceptance Criteria (verify)
- [ ] AC8.1: PG + VK healthy → `200 UP`; a dependency down → `503 DOWN`.
- [ ] AC8.2: no broker publish/consume at module level beyond the OTP producer (User Service does not consume events).
- [ ] AC8.3: dependencies unwired → only `/health` route mounted.

### Tests (exist)
**File:** `internal/user/infrastructure/http/health_handler_test.go`
```go
func TestHealthHandler(t *testing.T)                 // 200 UP / 503 DOWN
func TestHealthHandler_MethodNotAllowed(t *testing.T)
```
**File:** `internal/user/infrastructure/healthcheck/checker_test.go`
```go
func TestCheckAll(t *testing.T) // aggregates dependency statuses
```
**File:** `internal/delivery/server_test.go`
```go
func TestNewHandler_HealthWithoutClients(t *testing.T) // noRoutes → only /health
func TestClients_EmptyIsSafe(t *testing.T)
```

### Verify
```bash
cd services/user && GOWORK=off go test -race ./internal/user/infrastructure/http/... ./internal/user/infrastructure/healthcheck/... ./internal/delivery/...
```

---

## Task 9: Build & Verify (R9) [DONE — one open item]

**Traces to:** Requirement 9
**Status:** DONE for build/unit. **TODO:** run integration suite against real infra + clear the 3 pre-existing lint findings.

### What exists / verified (2026-10-20)
- `GOWORK=off go build ./...` → compiles clean.
- `go test -race ./...` → all unit packages `ok` (usecase, security, http, mapper, config, delivery, cmd, migrations, rabbitmq).
- Cobra verbs `api` / `migrate` / `healthcheck` present (`cmd/cmd_test.go`).
- Config validation: `internal/config/load_test.go` — `TestLoad_Defaults`, `TestLoad_ReportsInvalidValues`.

### Acceptance Criteria
- [x] AC9.4: `make build` compiles (incl. `GOWORK=off` isolation) — verified.
- [x] AC9.5: `make test` passes `-race` — verified.
- [ ] AC9.1: `go run ./cmd migrate up` applies `0001_init` — run against local Postgres to confirm.
- [ ] AC9.2: `go run ./cmd api` starts on 8085, serves `/health` + `/api/v1/*` — smoke test.
- [ ] AC9.3: invalid config → clear validation failure on start — spot-check with a bad `TOKEN_*`/`PII_*`.
- [ ] AC9.6: `make test-integration` (tag `integration`) passes against real PostgreSQL + Valkey.
- [ ] AC9.7: `make lint` — **3 pre-existing findings** (errcheck, misspell) noted in design §11; clear them.
- [ ] AC9.8: `make cover` ≥ 80% (excl. `cmd/`, `internal/delivery/`, `internal/config/`, `_mock/`).

### TODO — close the open items
1. **Integration run** — requires infra:
   ```bash
   cd services/user && make infra keys
   make test-integration   # TestAccounts, TestSessions_RotateIsSingleWinner, TestOTPStore_Lifecycle, TestRateLimiter
   ```
2. **Lint cleanup** — fix the 3 `golangci-lint` findings (unchecked errors + spelling) so `make lint` is green. Follow existing error-handling style (wrap-and-return or explicit `_ =` with a `Warn` as in `(*Auth).audit`).
3. **Coverage gate** — `make cover`; if < 80%, add branch cases to the usecase table tests (the exclusions already favor logic packages).

### Verify
```bash
cd services/user
make build
make test
make test-integration
make lint
make cover
make ci
```

---

## Test Case Alignment

| Task | Status | Test Cases | Notes |
|------|--------|------------|-------|
| 1 | DONE | TC-USR-02, TC-USR-04, TC-USR-11 | anti-enumeration, cooldown, no-plaintext-in-broker |
| 2 | DONE | TC-USR-03, TC-USR-05 | lockout, constant-time compare; status 401/429 per D5 |
| 3 | DONE | TC-USR-06 | replay → revoke-all; atomic rotation (integration) |
| 4 | DONE | TC-USR-07 | idempotent logout → 204 |
| 5 | DONE | TC-USR-08, TC-USR-12 | masked-only PII |
| 6 | DONE | TC-USR-08 | NIK uniqueness → 409 |
| 7 | DONE | TC-USR-09, TC-USR-09a | token identity claims (D1 unblocked) |
| 8 | DONE | (health integration) | 200 UP / 503 DOWN |
| 9 | DONE (unit) / TODO (integration+lint) | (CI verification) | run `make ci` on infra; clear 3 lint findings |

## Status summary

All eight feature tasks (R1–R8) are implemented with unit + handler tests and compile/pass
green under `GOWORK=off`. The only outstanding work is operational verification (Task 9):
run the `integration`-tagged suite against real PostgreSQL/Valkey, clear the 3 known
`golangci-lint` findings, and confirm the `make cover` ≥ 80% gate.
