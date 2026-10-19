# Software Design Description — User Service

| Field | Value |
|-------|-------|
| **Service** | User Service |
| **Document ID** | SDD-USER-001 |
| **Version** | 1.1 |
| **Date** | 2026-10-19 |
| **Status** | Draft. Desain baseline = PRD v5.4, SRS v1.3, HLD v1.6 |
| **Owner** | BE-1 |
| **Port** | 8085 |
| **Schema** | `users` (role `svc_user`) |
| **Region** | ap-southeast-3 (Jakarta) |

## Change History

| Version | Date | Author | Summary |
|---------|------|--------|---------|
| 1.1 | 2026-10-19 | BE-1 | Align baseline metadata with PRD v5.4, SRS v1.3, HLD v1.6 for logged-in NIK handling |
| 1.0 | 2026-10-19 | BE-1 | Initial version |

---

## 1. Overview

User Service mengelola akun opsional dengan login OTP email (tanpa password). Login tidak pernah menjadi syarat membeli tiket — pembeli dapat tetap sebagai guest.

**Prinsip utama:**
- Login opsional — tidak memblokir pembelian
- OTP email — tidak ada password yang harus diingat
- Privacy by design — email dan NIK dienkripsi, token di-hash

---

## 2. Responsibilities

| Capability | SRS Reference | Description |
|------------|---------------|-------------|
| OTP Request | §3.2.27 | Kirim kode OTP ke email |
| OTP Verify | §3.2.27 | Verifikasi kode, buat akun jika baru |
| Access Token | §3.2.27 | JWT 15 menit untuk autentikasi |
| Refresh Token | §3.2.27 | Token 30 hari untuk perpanjang session |
| Profile NIK | §3.2.27 | Link NIK ke akun |
| Logout | §3.2.27 | Revoke refresh token |
| Session Management | §3.2.27 | Track active sessions |
| Login Audit | §3.2.27 | Audit trail login |

---

## 3. API Endpoints (Public)

### 3.1 Request OTP

```
POST /api/v1/auth/otp-request
```

**Request:**
```json
{
  "email": "user@example.com"
}
```

**Response (202):**
```json
{
  "message": "OTP sent to email",
  "expiresInSeconds": 300
}
```

**Rate limits:**
- 60 detik cooldown antar request
- Max 5 per jam per email
- Max 20 per jam per IP

**Errors:**
| Status | Code | Description |
|--------|------|-------------|
| 429 | `OTP_RESEND_TOO_SOON` | Cooldown belum selesai |
| 429 | `RATE_LIMITED` | Limit per jam tercapai |

### 3.2 Verify OTP

```
POST /api/v1/auth/otp-verify
```

**Request:**
```json
{
  "email": "user@example.com",
  "code": "123456"
}
```

**Response (200):**
```json
{
  "accountId": "uuid",
  "isNewAccount": true,
  "accessToken": "jwt-15min",
  "expiresAt": "2026-10-19T10:15:00Z"
}
```

**Set cookie:** `rb_refresh` (HttpOnly, 30 days)

**Errors:**
| Status | Code | Description |
|--------|------|-------------|
| 400 | `OTP_INVALID` | Kode salah |
| 400 | `OTP_EXPIRED` | Kode kedaluwarsa |
| 403 | `OTP_LOCKED` | Terlalu banyak percobaan salah |

### 3.3 Refresh Token

```
POST /api/v1/auth/refresh
```

**Cookie:** `rb_refresh`

**Response (200):**
```json
{
  "accessToken": "jwt-15min-new",
  "expiresAt": "2026-10-19T10:30:00Z"
}
```

**Set new cookie:** `rb_refresh` (rotated)

### 3.4 Logout

```
POST /api/v1/auth/logout
```

**Cookie:** `rb_refresh`

**Response:** 204 No Content

**Clear cookie:** `rb_refresh`

### 3.5 Get Profile

```
GET /api/v1/users/me
```

**Headers:** `Authorization: Bearer <accessToken>`

**Response:**
```json
{
  "accountId": "uuid",
  "email": "user@example.com",
  "nikLinked": true,
  "nikMasked": "320101****0001",
  "createdAt": "2026-10-19T09:00:00Z"
}
```

### 3.6 Link NIK to Profile

```
POST /api/v1/users/me/nik
```

**Headers:** `Authorization: Bearer <accessToken>`

**Request:**
```json
{
  "nik": "3201010101010001"
}
```

**Errors:**
| Status | Code | Description |
|--------|------|-------------|
| 409 | `NIK_ALREADY_LINKED` | NIK sudah dipakai akun lain |

### 3.7 Order History

Booking history is accessed via **Booking Service** endpoint:

```
GET /api/v1/me/bookings
```

> **Note:** User Service does NOT have an order history endpoint. Clients call Booking Service directly with the access token. Booking Service validates the token using User Service public key (EdDSA, cached).

See **SDD-BOOKING-001 §15** for full specification.

**Test:** TC-USR-10
```

---

## 4. Data Model

### 4.1 PostgreSQL (`users` schema)

```sql
CREATE TABLE users.accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email_enc BYTEA NOT NULL,
  email_hash BYTEA NOT NULL UNIQUE,
  nik_enc BYTEA,
  nik_hash BYTEA UNIQUE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE users.sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES users.accounts(id),
  refresh_token_hash BYTEA NOT NULL UNIQUE,
  user_agent TEXT,
  ip_address INET,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE users.login_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID,
  email_hash BYTEA NOT NULL,
  action TEXT NOT NULL,
  success BOOLEAN NOT NULL,
  ip_address INET,
  user_agent TEXT,
  failure_reason TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_sessions_account ON users.sessions(account_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_login_audit_email ON users.login_audit(email_hash, created_at DESC);
```

### 4.2 Valkey (OTP Storage)

| Key | Type | Fields | TTL |
|-----|------|--------|-----|
| `otp:{emailHash}` | Hash | `codeHash`, `attempts`, `createdAt` | 5 min |
| `otp:cooldown:{emailHash}` | String | `1` | 60s |
| `otp:hourly:{emailHash}` | String | count | 1 hour |
| `otp:hourly:ip:{ipHash}` | String | count | 1 hour |

---

## 5. OTP Flow

### 5.1 Request OTP

```
1. Check cooldown key exists → 429 OTP_RESEND_TOO_SOON
2. Check hourly email limit → 429 RATE_LIMITED  
3. Check hourly IP limit → 429 RATE_LIMITED
4. Generate 6-digit code
5. Store hash in Valkey: otp:{emailHash} with 5 min TTL
6. Set cooldown key: otp:cooldown:{emailHash} with 60s TTL
7. Increment hourly counters
8. Send email via SMTP
9. Return 202
```

### 5.2 Verify OTP

```
1. Get otp:{emailHash} from Valkey → 400 OTP_EXPIRED if not found
2. Check attempts >= 5 → 403 OTP_LOCKED
3. Verify code hash
4. If wrong: increment attempts, return 400 OTP_INVALID with attemptsLeft
5. If correct:
   a. Delete OTP key
   b. Find or create account by email_hash
   c. Create session with refresh token
   d. Generate access token
   e. Audit: LOGIN_SUCCESS
   f. Return tokens
```

### 5.3 Security Notes

- OTP code never logged or stored in broker
- Only hash stored in Valkey
- Email encrypted at rest (accounts.email_enc)
- Refresh token hashed (sessions.refresh_token_hash)

---

## 6. Token Management

### 6.1 Access Token (JWT)

**Lifetime:** 15 minutes
**Algorithm:** EdDSA (Ed25519)
**Claims:**
```json
{
  "sub": "account-uuid",
  "iat": 1697706000,
  "exp": 1697706900,
  "iss": "user-service",
  "aud": ["queue-service", "booking-service"],
  "email": "user@example.com",
  "nik_hash": "hmac-sha256-of-nik-or-null",
  "has_nik": true
}
```

**Claim Details (v5.4, SRS-F-27-10):**

| Claim | Type | Description |
|-------|------|-------------|
| `sub` | string | Account UUID |
| `email` | string | User's email address |
| `nik_hash` | string | HMAC-SHA256 of NIK, or null if not stored |
| `has_nik` | boolean | true if account has NIK stored |

**Why include nikHash in token:**
- Queue Service can validate NIK match WITHOUT calling User Service
- Enables stateless validation: `HMAC(body.buyerNik) == token.nik_hash`
- Anti-scalper check (one queue per NIK) remains effective

### 6.2 Refresh Token

**Lifetime:** 30 days
**Storage:** HttpOnly cookie `rb_refresh`
**Rotation:** New token on each refresh
**Revocation:** Old token invalid after rotation (replay detection)

### 6.3 Token Validation by Other Services

Queue and Booking services validate access token:
```
1. Check signature with public key (cached)
2. Check exp not passed
3. Check aud includes service name
4. Extract claims:
   - accountId = sub
   - email = email
   - nikHash = nik_hash (may be null)
   - hasNik = has_nik
5. For Queue join (SRS-F-27-10):
   - If hasNik=true: use nikHash directly (ignore body.buyerNik)
   - If hasNik=false: require body.buyerNik, HMAC it
6. Include accountId in entry/booking
```

**Note:** No NIK_MISMATCH error — if `hasNik=true`, body `buyerNik` is simply ignored. This avoids PII exposure: logged-in users never send their NIK in requests.

---

## 7. Inter-Service Communication

### 7.1 Token Sharing

User Service issues tokens; other services validate:
- **Public key** shared via mounted secret or config
- **No HTTP calls** to User Service for validation

### 7.2 Account Linking

Queue and Booking include `accountId` in their records when user is logged in:
- `queue.entry_audit.account_id`
- `booking.admissions.account_id`
- `booking.bookings.account_id`

### 7.3 Events

**None published or consumed.** User Service is stateless from event perspective.

---

## 8. Configuration

```bash
# Application
APP_ENV=development
APP_NAME=user-service
APP_PORT=8085
LOG_LEVEL=info

# PostgreSQL
DATABASE_URL=postgres://svc_user:xxx@localhost:5432/rusuh?search_path=users

# Valkey
VALKEY_URL=localhost:6379

# SMTP (for OTP email)
SMTP_HOST=email-smtp.ap-southeast-3.amazonaws.com
SMTP_PORT=587
SMTP_USER=AKIAXXXXXXXX
SMTP_PASSWORD=xxxxxx
SMTP_FROM=noreply@rusuh.example

# Token
USER_TOKEN_PRIVATE_KEY_PATH=/keys/user.key
USER_TOKEN_PUBLIC_KEY_PATH=/keys/user.pub
ACCESS_TOKEN_TTL=15m
REFRESH_TOKEN_TTL=720h

# Encryption (for PII)
PII_ENCRYPTION_KEY_PATH=/keys/pii.key

# OTP
OTP_LENGTH=6
OTP_TTL=300
OTP_COOLDOWN=60
OTP_HOURLY_LIMIT_EMAIL=5
OTP_HOURLY_LIMIT_IP=20
OTP_MAX_ATTEMPTS=5
```

---

## 9. Observability

### 9.1 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rbt_user_otp_requests_total` | Counter | `status` | OTP request attempts |
| `rbt_user_otp_verifications_total` | Counter | `status` | OTP verify attempts |
| `rbt_user_logins_total` | Counter | `new_account` | Successful logins |
| `rbt_user_active_sessions` | Gauge | — | Active sessions count |
| `rbt_user_token_refresh_total` | Counter | `status` | Token refreshes |

### 9.2 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| UserOTPFailRateHigh | fail_rate > 30% for 10m | Warning |
| UserBruteForceDetected | locked_rate > 5% for 5m | Critical |
| UserSMTPFailure | smtp_error_rate > 10% for 5m | Critical |

---

## 10. Running Locally

```bash
# From repo root
make infra keys

# Start service
cd services/user
cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &

# Test
curl localhost:8085/health

# Request OTP (check Mailpit at localhost:8025)
curl -X POST localhost:8085/api/v1/auth/otp-request \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com"}'
```

---

## 11. References

- OpenAPI: `docs/contracts/openapi/user.yaml`
- HLD §4.2.1: Service responsibilities
- SRS §3.2.27: Optional Account and OTP Login
- ADR-020: User Service

---

## 12. Test Case Alignment

| Feature | SRS | Test Cases | Level |
|---------|-----|------------|-------|
| OTP request | SRS-F-27-02 | TC-USR-02 | Integration + E2E |
| OTP verify | SRS-F-27-03..05 | TC-USR-03, TC-USR-05 | Unit + Integration |
| Rate limiting | SRS-F-27-04 | TC-USR-04 | Unit + Integration |
| Token refresh | SRS-F-27-06 | TC-USR-06 | Integration |
| Logout | SRS-F-27-07 | TC-USR-07 | E2E |
| NIK linking | SRS-F-27-08..09 | TC-USR-08 | Integration |
| Logged-in join (NIK stored) | SRS-F-27-10 | TC-USR-09 | E2E |
| Logged-in join (no NIK) | SRS-F-27-10a | TC-USR-09a | E2E |
| Order history | SRS-F-27-11 | TC-USR-10 | E2E |
| Guest purchase | SRS-F-27-01 | TC-USR-01 | E2E |
| PII security | SRS-Q-SEC-10..11 | TC-USR-11, TC-USR-12 | Inspection |

### Key Test Scenarios

| TC ID | Scenario | Expected | Validates |
|-------|----------|----------|-----------|
| TC-USR-01 | Buy without login | Success, no User Service call | Optional login |
| TC-USR-02 | OTP for new/existing email | Same 202 response | Privacy |
| TC-USR-03 | Wrong OTP 5x then correct | OTP_LOCKED after 5th | Rate limit |
| TC-USR-04 | OTP 2x in 60s | 429 OTP_RESEND_TOO_SOON | Cooldown |
| TC-USR-06 | Use old refresh after rotation | SESSION_INVALID, all revoked | Replay detection |
| TC-USR-09 | Join with token (NIK stored) | No buyerNik in body; nikHash from token; join succeeds | Token NIK (v5.4) |
| TC-USR-09a | Join with token (no NIK) | Body buyerNik required; offer to save | Fallback (v5.4) |
| TC-USR-11 | Search OTP in logs | Not found | PII protection |
| TC-USR-12 | Check DB columns | Email/NIK encrypted, token hashed | Encryption |

---

## Appendix A: Validation Rules

### A.1 Request Validation

```go
type OTPRequestBody struct {
    Email string `json:"email" validate:"required,email,max=255"`
}

type OTPVerifyBody struct {
    Email string `json:"email" validate:"required,email,max=255"`
    Code  string `json:"code" validate:"required,len=6,numeric"`
}

type LinkNIKBody struct {
    NIK string `json:"nik" validate:"required,len=16,numeric"`
}
```

### A.2 Email Validation

```go
// RFC 5322 compliant email validation
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func ValidateEmail(email string) error {
    if len(email) > 255 { return ErrEmailTooLong }
    if !emailRegex.MatchString(email) { return ErrEmailInvalid }
    return nil
}
```

### A.3 OTP Generation

```go
func GenerateOTP(length int) string {
    // Cryptographically secure random
    max := big.NewInt(int64(math.Pow10(length)))
    n, _ := rand.Int(rand.Reader, max)
    return fmt.Sprintf("%0*d", length, n)
}

func HashOTP(code string, salt []byte) []byte {
    return argon2.IDKey([]byte(code), salt, 1, 64*1024, 4, 32)
}
```

### A.4 Rate Limit Rules

| Rule | Limit | Window | Key | Error Code |
|------|-------|--------|-----|------------|
| OTP cooldown | 1 | 60s | email | `OTP_RESEND_TOO_SOON` |
| OTP per email | 5 | 1 hour | email | `RATE_LIMITED` |
| OTP per IP | 20 | 1 hour | IP | `RATE_LIMITED` |
| Wrong attempts | 5 | per OTP | email | `OTP_LOCKED` |

---

## Appendix B: Error Codes

| Code | HTTP | Description | User Action |
|------|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Field tidak valid | Fix input |
| `OTP_RESEND_TOO_SOON` | 429 | Cooldown belum selesai | Tunggu `retryAfterSeconds` |
| `RATE_LIMITED` | 429 | Limit per jam tercapai | Tunggu 1 jam |
| `OTP_INVALID` | 400 | Kode salah | Coba lagi (`attemptsLeft`) |
| `OTP_EXPIRED` | 400 | Kode kedaluwarsa | Request OTP baru |
| `OTP_LOCKED` | 403 | Terlalu banyak salah | Request OTP baru |
| `SESSION_INVALID` | 401 | Refresh token tidak valid | Login ulang |
| `SESSION_EXPIRED` | 401 | Refresh token expired | Login ulang |
| `TOKEN_INVALID` | 401 | Access token tidak valid | Refresh token |
| `TOKEN_EXPIRED` | 401 | Access token expired | Refresh token |
| `NIK_ALREADY_LINKED` | 409 | NIK sudah dipakai akun lain | Gunakan NIK lain |
| `NIK_INVALID` | 422 | Format NIK salah | Perbaiki NIK |
| `ACCOUNT_NOT_FOUND` | 404 | Akun tidak ada | Login ulang |

---

## Appendix C: Token Claims

### C.1 Access Token (JWT) — v5.4

```json
{
  "header": {
    "alg": "EdDSA",
    "typ": "JWT"
  },
  "payload": {
    "sub": "account-uuid",
    "iat": 1697706000,
    "exp": 1697706900,
    "iss": "user-service",
    "aud": ["queue-service", "booking-service"],
    "email": "user@example.com",
    "nik_hash": "hmac-sha256-of-nik-or-null",
    "has_nik": true
  }
}
```

**Note:** `email` is included as plaintext for Queue/Booking to validate without calling User Service. For privacy, consider using `email_hash` if email lookup isn't needed. Current design prioritizes stateless validation over minimal claims.

**Claim usage:**
- `email`: For display and notification (already known to user)
- `nik_hash`: For Queue Service to validate `HMAC(body.buyerNik) == nik_hash`
- `has_nik`: To determine if NIK match validation is required
```

### C.2 Refresh Token Storage

```sql
-- Token is hashed before storage
INSERT INTO users.sessions (
    account_id,
    refresh_token_hash,  -- SHA256(token)
    user_agent,
    ip_address,
    expires_at
) VALUES ($1, $2, $3, $4, NOW() + INTERVAL '30 days');
```

### C.3 Token Rotation

```go
func RefreshToken(oldToken string) (*TokenPair, error) {
    // 1. Hash old token
    oldHash := sha256.Sum256([]byte(oldToken))
    
    // 2. Find session
    session, err := repo.FindSessionByTokenHash(oldHash[:])
    if err != nil { return nil, ErrSessionInvalid }
    
    // 3. Check not revoked
    if session.RevokedAt != nil { 
        // Replay attack - revoke all sessions for this account
        repo.RevokeAllSessions(session.AccountID)
        return nil, ErrSessionInvalid 
    }
    
    // 4. Revoke old token
    repo.RevokeSession(session.ID)
    
    // 5. Generate new tokens
    newRefresh := generateSecureToken()
    newAccess := generateJWT(session.AccountID)
    
    // 6. Store new session
    repo.CreateSession(session.AccountID, sha256.Sum256(newRefresh))
    
    return &TokenPair{Access: newAccess, Refresh: newRefresh}, nil
}
```

---

## Appendix D: Encryption

### D.1 PII Encryption (Email, NIK)

```go
// AES-256-GCM for encryption
func EncryptPII(plaintext string, key []byte) ([]byte, error) {
    block, _ := aes.NewCipher(key)
    gcm, _ := cipher.NewGCM(block)
    
    nonce := make([]byte, gcm.NonceSize())
    io.ReadFull(rand.Reader, nonce)
    
    ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
    return ciphertext, nil
}

func DecryptPII(ciphertext []byte, key []byte) (string, error) {
    block, _ := aes.NewCipher(key)
    gcm, _ := cipher.NewGCM(block)
    
    nonceSize := gcm.NonceSize()
    nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
    
    plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
    return string(plaintext), err
}
```

### D.2 Hash for Lookup (Email, NIK)

```go
// SHA256 for deterministic lookup
func HashForLookup(value string, salt []byte) []byte {
    h := sha256.New()
    h.Write(salt)
    h.Write([]byte(strings.ToLower(value)))
    return h.Sum(nil)
}
```
