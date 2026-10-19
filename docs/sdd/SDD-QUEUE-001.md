# Software Design Description — Queue Service

| Field | Value |
|-------|-------|
| **Service** | Queue Service |
| **Document ID** | SDD-QUEUE-001 |
| **Version** | 1.1 |
| **Date** | 2026-10-19 |
| **Status** | Draft. Desain baseline = PRD v5.4, SRS v1.3, HLD v1.6 |
| **Owner** | BE-1 |
| **Port** | 8081 |
| **Schema** | `queue` (role `svc_queue`) |
| **Region** | ap-southeast-3 (Jakarta) |

## Change History

| Version | Date | Author | Summary |
|---------|------|--------|---------|
| 1.1 | 2026-10-19 | BE-1 | Fix Appendix A.1 JoinRequest struct: `buyerNik` now `omitempty` (conditional validation per auth state as documented in §3.1). Align with PRD v5.4, SRS v1.3, HLD v1.6 logged-in NIK handling |
| 1.0 | 2026-10-19 | BE-1 | Initial version |

---

## 1. Overview

Queue Service mengelola waiting room untuk war tiket. Service ini menangani lonjakan join (hingga 20.000/detik) dengan Valkey sebagai state store, sementara database transaksi (Booking) terlindungi di belakang admission.

**Prinsip utama:**
- FIFO murni — nomor antrean tidak pernah loncat
- Fail closed — join/claim ditolak saat Valkey down, bukan gagal diam
- CDN-first — Status Document di-cache 1 detik, polling jutaan client tidak menyentuh origin

---

## 2. Responsibilities

| Capability | SRS Reference | Description |
|------------|---------------|-------------|
| Guest Session | §3.2.1 | Visitor token untuk identitas perangkat |
| Queue Join | §3.2.4 | Masuk antrean dengan NIK + email |
| Direct Admission | §3.2.4 | Langsung masuk jika slot tersedia |
| Queue Status | §3.2.5 | Status Document (CDN cached) |
| Position Calculation | §3.2.5 | Posisi = mySeq - servingSeq |
| Admission & Claim | §3.2.6 | Promotor batch, claim window 3 menit |
| Sold-Out Closure | §3.2.10 | Tutup antrean saat sold out |
| Turn Notification | §3.2.25 | Trigger push notification |

---

## 3. API Endpoints (Public)

### 3.1 Join Queue

```
POST /api/v1/events/{eventId}/queue-entries
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `buyerNik` | string | Conditional | NIK pembeli (16 digit). Required for guest and logged-in without NIK; omit for logged-in with stored NIK |
| `email` | string | Yes | Email untuk notifikasi |

**Headers:**
- `X-Visitor-ID`: UUID perangkat (required)
- `Authorization: Bearer <userToken>` (optional, untuk login user)
- `Idempotency-Key`: UUID (untuk retry)

**Behavior by Auth State (SRS-F-27-10, v5.4):**

| Auth State | Body `buyerNik` | NIK Source | Display |
|------------|-----------------|------------|---------|
| Guest (no token) | Required | `HMAC(body.buyerNik)` | Editable form |
| Logged-in, NIK stored | Omit (ignored if sent) | `token.nikHash` | Masked, read-only |
| Logged-in, no NIK | Required | `HMAC(body.buyerNik)` | Editable form |

**Implementation Logic:**
```go
func (h *Handler) JoinQueue(ctx context.Context, req JoinRequest) (*Entry, error) {
    // Email always required
    if req.Email == "" {
        return nil, ErrEmailRequired
    }
    
    token := auth.GetAccessToken(ctx)
    var buyerNikHash string
    var accountId *uuid.UUID
    
    if token != nil && token.HasNik {
        // Logged-in with NIK: use token.nikHash (body.buyerNik ignored)
        buyerNikHash = token.NikHash
        accountId = &token.AccountId
    } else if token != nil && !token.HasNik {
        // Logged-in without NIK: require body (same as guest)
        if req.BuyerNik == "" {
            return nil, ErrNikRequired
        }
        buyerNikHash = hmacNik(req.BuyerNik)
        accountId = &token.AccountId
    } else {
        // Guest: require body
        if req.BuyerNik == "" {
            return nil, ErrNikRequired
        }
        buyerNikHash = hmacNik(req.BuyerNik)
        accountId = nil
    }
    
    // Anti-scalper checks use buyerNikHash (same for all paths)
    // ...
}
```

**Responses:**
| Status | Code | Description |
|--------|------|-------------|
| 201 | `WAITING` | Masuk antrean, harus menunggu |
| 201 | `ADMITTED` | Langsung masuk (slot tersedia) |
| 409 | `NIK_ALREADY_IN_QUEUE` | NIK sudah punya entry aktif |
| 409 | `VISITOR_ALREADY_IN_QUEUE` | Perangkat sudah punya entry |
| 409 | `EMAIL_ALREADY_IN_QUEUE` | Email sudah punya entry |
| 409 | `BUYER_QUOTA_EXHAUSTED` | NIK sudah beli maksimal |
| 410 | `EVENT_SOLD_OUT` | Tiket habis |
| 422 | `NIK_REQUIRED` | Guest/logged-in tanpa NIK tapi body.buyerNik kosong |
| 429 | `RATE_LIMITED` | Rate limit exceeded |

### 3.2 Get Queue Status (CDN Cached)

```
GET /api/v1/queue-status/{eventId}
```

**Cache:** 1 detik di CloudFront

**Response:**
```json
{
  "eventId": "uuid",
  "servingSeq": 12300,
  "waitingCount": 500,
  "activeSlots": 45,
  "maxActiveSlots": 50,
  "saleStatus": "OPEN",
  "updatedAt": "2026-10-19T10:00:00Z",
  "config": {
    "pollingIntervalMs": 3000
  }
}
```

### 3.3 Get My Entry (Exact Position)

```
GET /api/v1/queue-entries/me?eventId={eventId}
```

**Headers:** `X-Visitor-Token` or cookie `rb_visitor`

### 3.4 Claim Turn

```
POST /api/v1/queue-entries/{entryId}/claim
```

**Headers:** `Authorization: Bearer <queueToken>`

**Response (200):**
```json
{
  "admissionToken": "jwt-one-time-use",
  "expiresAt": "2026-10-19T10:01:00Z"
}
```

### 3.5 Leave Queue

```
DELETE /api/v1/queue-entries/{entryId}
```

---

## 4. Internal API (Called)

Queue Service memanggil Booking Service:

### 4.1 Reserve Allocation Batch

```
POST /internal/v1/allocations:batch → Booking Service
```

**Request:**
```json
{
  "eventId": "uuid",
  "entries": [
    {
      "queueEntryId": "uuid",
      "visitorId": "uuid",
      "buyerNikHash": "sha256",
      "units": 3
    }
  ]
}
```

**Response:**
```json
{
  "allocations": [
    {
      "queueEntryId": "uuid",
      "allocationId": "uuid",
      "status": "RESERVED",
      "units": 3,
      "claimDeadline": "2026-10-19T10:03:00Z"
    }
  ]
}
```

**Status values:** `RESERVED`, `NO_SUPPLY_NOW`, `SOLD_OUT`, `QUOTA_EXHAUSTED`

### 4.2 Get Supply Snapshot

```
GET /internal/v1/events/{id}/supply → Booking Service
```

**Cache:** 1 detik di Valkey

---

## 5. Data Model

### 5.1 PostgreSQL (`queue` schema)

```sql
-- Entry audit untuk pemulihan
CREATE TABLE queue.entry_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id UUID NOT NULL,
  entry_id UUID NOT NULL,
  seq BIGINT NOT NULL,
  visitor_id UUID NOT NULL,
  buyer_nik_hash BYTEA NOT NULL,
  email_enc BYTEA NOT NULL,
  status TEXT NOT NULL,
  joined_at TIMESTAMPTZ NOT NULL,
  admitted_at TIMESTAMPTZ,
  claimed_at TIMESTAMPTZ,
  ended_at TIMESTAMPTZ,
  outcome TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Event settings
CREATE TABLE queue.event_settings (
  event_id UUID PRIMARY KEY,
  sale_opens_at TIMESTAMPTZ NOT NULL,
  sale_closes_at TIMESTAMPTZ NOT NULL,
  max_active_slots INT NOT NULL DEFAULT 50,
  claim_window_seconds INT NOT NULL DEFAULT 180,
  polling_interval_ms INT NOT NULL DEFAULT 3000,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 5.2 Valkey Keys

Semua key per event memakai hash tag `{e:<eventId>}` untuk operasi Lua atomik.

| Key | Type | Description | TTL |
|-----|------|-------------|-----|
| `q:{e}:seq` | String | Counter sequence | Persistent |
| `q:{e}:waiting` | Sorted Set | member=entryId, score=seq | Persistent |
| `q:{e}:entry:<entryId>` | Hash | Entry data | Persistent |
| `q:{e}:nik:<buyerNikHash>` | String | entryId aktif | Persistent |
| `q:{e}:visitor:<visitorId>` | String | entryId aktif | Persistent |
| `q:{e}:email:<emailHash>` | String | entryId aktif | Persistent |
| `q:{e}:quota_exhausted` | Set | buyerNikHash yang kuotanya habis | Persistent |
| `q:{e}:active` | Sorted Set | entry ADMITTED/CLAIMED, score=claimDeadline | Persistent |
| `q:{e}:status` | String | Status Document JSON | 1s (cache) |

### 5.3 Entry Hash Fields

```
seq, visitorId, buyerNikHash, emailEnc, accountId,
status, joinedAtMs, admittedAtMs, claimedAtMs,
claimDeadlineMs, allocationId
```

---

## 6. State Machine

```
┌─────────┐   join    ┌─────────┐   promote   ┌──────────┐   claim    ┌─────────┐
│  (new)  │─────────▶ │ WAITING │────────────▶│ ADMITTED │───────────▶│ CLAIMED │
└─────────┘           └─────────┘             └──────────┘            └─────────┘
     │                      │                       │                       │
     │ direct admission     │ leave/sold_out        │ deadline passed       │ journey.ended
     ▼                      ▼                       ▼                       ▼
┌──────────┐          ┌──────────┐            ┌──────────┐            ┌──────────┐
│ ADMITTED │          │ REJECTED │            │ UNCLAIMED│            │  ENDED   │
└──────────┘          └──────────┘            └──────────┘            └──────────┘
```

**Status values:**
| Status | Description |
|--------|-------------|
| `WAITING` | Dalam antrean, menunggu giliran |
| `ADMITTED` | Giliran tiba, belum claim |
| `CLAIMED` | Sudah claim, sedang di booking session |
| `REJECTED` | Ditolak (sold out, quota, leave) |
| `UNCLAIMED` | Giliran hangus (deadline passed) |
| `ENDED` | Journey selesai (paid/expired/cancelled) |

---

## 7. Inter-Service Communication

### 7.1 Outbound HTTP (Synchronous)

| Target | Endpoint | Purpose |
|--------|----------|---------|
| Booking | `POST /internal/v1/allocations:batch` | Reserve allocation for admitted entries |
| Booking | `GET /internal/v1/events/{id}/supply` | Stock snapshot (cached 1s) |

### 7.2 Events Published

| Event | Routing Key | Consumer | Payload |
|-------|-------------|----------|---------|
| Turn Soon | `queue.turn.soon` | Notification | `{ entryId, eventId, pushToken, position }` |
| Turn Arrived | `queue.turn.arrived` | Notification | `{ entryId, eventId, pushToken, claimDeadline }` |

### 7.3 Events Consumed

| Event | Producer | Purpose | Idempotency Check |
|-------|----------|---------|-------------------|
| `booking.journey.ended` | Booking | Release slot, update entry status | `entry.status == CLAIMED` |
| `booking.quota.exhausted` | Booking | Block NIK from rejoining | Add to quota_exhausted set |
| `booking.sold_out` | Booking | Close queue, reject waiting | Set saleStatus = SOLD_OUT |

---

## 8. Workers

### 8.1 Promotor

**Interval:** 100ms (configurable)

**Logic:**
1. Check available slots: `maxActiveSlots - len(active)`
2. If slots > 0, take N entries from `waiting` sorted set
3. Call `POST /internal/v1/allocations:batch`
4. Move successful entries to `active`, set ADMITTED
5. Publish `queue.turn.arrived` for each

### 8.2 Status Document Publisher

**Interval:** 500ms

**Logic:**
1. Compute: servingSeq, waitingCount, activeSlots
2. Build Status Document JSON
3. SET to `q:{e}:status` with 1s TTL
4. CloudFront caches this endpoint

### 8.3 Claim Deadline Sweeper

**Interval:** 10s

**Logic:**
1. ZRANGEBYSCORE `active` where score < now
2. For each expired: status → UNCLAIMED, remove from active
3. Publish `booking.journey.ended` with outcome UNCLAIMED

---

## 9. Configuration

```bash
# Application
APP_ENV=development
APP_NAME=queue-service
APP_PORT=8081
LOG_LEVEL=info
LOG_FORMAT=json

# PostgreSQL
DATABASE_URL=postgres://svc_queue:xxx@localhost:5432/rusuh?search_path=queue

# Valkey
VALKEY_URL=localhost:6379

# RabbitMQ
RABBITMQ_URL=amqp://guest:guest@localhost:5672/

# Booking Service (internal)
BOOKING_SERVICE_URL=http://booking:8082

# Token signing
TOKEN_PRIVATE_KEY_PATH=/keys/queue.key
TOKEN_PUBLIC_KEY_PATH=/keys/queue.pub

# Queue settings
QUEUE_MAX_ACTIVE_SLOTS=50
QUEUE_CLAIM_WINDOW_SECONDS=180
QUEUE_POLLING_INTERVAL_MS=3000
QUEUE_PROMOTOR_INTERVAL_MS=100
QUEUE_STATUS_PUBLISH_INTERVAL_MS=500
```

---

## 10. Observability

### 10.1 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rbt_queue_joins_total` | Counter | `event_id`, `status` | Join attempts |
| `rbt_queue_claims_total` | Counter | `event_id`, `status` | Claim attempts |
| `rbt_queue_waiting_count` | Gauge | `event_id` | Current waiting count |
| `rbt_queue_active_count` | Gauge | `event_id` | Current active slots |
| `rbt_queue_promotions_total` | Counter | `event_id` | Entries promoted |
| `rbt_queue_unclaimed_total` | Counter | `event_id` | Deadlines missed |

### 10.2 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| QueuePromotorStalled | No promotions for 1m during sale | Critical |
| QueueUnclaimedRateHigh | unclaimed_rate > 5% for 5m | Warning |
| QueueValkeyLatencyHigh | p99 > 50ms for 5m | Warning |

### 10.3 Health Check

```
GET /health
```

Checks: Valkey ping, PostgreSQL ping, RabbitMQ connection

---

## 11. Running Locally

```bash
# From repo root
make infra keys

# Start service
cd services/queue
cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &
go run ./cmd worker &

# Test
curl localhost:8081/health
```

---

## 12. References

- OpenAPI: `docs/contracts/openapi/queue.yaml`
- HLD §4.4.1: Join and Direct Admission sequence
- HLD §4.4.2: Waiting, Promotion, and Claim sequence
- SRS §3.2.4-§3.2.6: Queue requirements
- ADR-003: Waiting room via polling Status Document

---

## 13. Test Case Alignment

| Feature | SRS | Test Cases | Level |
|---------|-----|------------|-------|
| Guest session | SRS-F-01-01..09 | TC-GST-01, TC-GST-02, TC-GST-03, TC-GST-04, TC-GST-05 | Integration + E2E |
| Join queue | SRS-F-04-01..10 | TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08 | Integration |
| Logged-in join (NIK stored) | SRS-F-27-10 | TC-USR-09 | E2E |
| Logged-in join (no NIK) | SRS-F-27-10a | TC-USR-09a | E2E |
| Status Document | SRS-F-05-01..11 | TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-10, TC-QUE-12, TC-QUE-15 | Integration |
| Claim/Admission | SRS-F-06-01..08 | TC-QUE-09, TC-QUE-13, TC-QUE-14 | Integration + E2E |
| FIFO guarantee | SRS-Q-FUN-03 | TC-QUE-08, TC-QUE-14 | Integration |
| Rate limiting | SRS-F-04-09 | TC-QUE-11 | Integration |
| Sold out | SRS-F-10-01..03 | TC-QUE-05 | Integration |
| Move entry | SRS-F-01-10 | TC-QUE-16 | Integration + E2E |
| Leave queue | SRS-F-13-03 | TC-QUE-17 | Integration + E2E |
| Performance | SRS-Q-PERF-* | TC-PERF-01, TC-PERF-02, TC-PERF-03, TC-PERF-05 | k6 load test |

### Key Test Scenarios

| TC ID | Scenario | Expected | Validates |
|-------|----------|----------|-----------|
| TC-GST-01 | Join with valid NIK+email | Entry created, visitor token issued | Guest flow |
| TC-GST-02 | Join with invalid NIK format | 422 NIK_INVALID | Validation |
| TC-GST-03 | Join with NIK on another device | 409 NIK_ALREADY_IN_QUEUE | Constraint |
| TC-GST-04 | Join with same device different NIK | 409 VISITOR_ALREADY_IN_QUEUE | Constraint |
| TC-GST-05 | Join with same email different NIK | 409 EMAIL_ALREADY_IN_QUEUE | Constraint |
| TC-QUE-01 | Join with valid NIK+email | 201 WAITING or ADMITTED | Happy path |
| TC-QUE-07 | Join with NIK already in queue | 409 NIK_ALREADY_IN_QUEUE | Constraint |
| TC-QUE-08 | 1000 parallel joins, check seq order | No gaps, FIFO preserved | Concurrency |
| TC-QUE-09 | Claim after deadline | 403 CLAIM_DEADLINE_PASSED | State machine |
| TC-QUE-14 | Verify FIFO across promotions | seq order preserved | Core guarantee |
| TC-QUE-16 | Move entry to new device via email link | Entry moves, old link invalid | Move feature |
| TC-QUE-17 | Leave queue while waiting | Entry LEFT, can rejoin from back | Leave feature |

---

## Appendix A: Validation Rules

### A.1 Join Request Validation

```go
type JoinRequest struct {
    // BuyerNIK: Conditional - required for guest/logged-in-without-NIK, omit for logged-in-with-NIK
    // Validation in handler (§3.1) checks presence based on auth state, not struct tag
    BuyerNIK string `json:"buyerNik,omitempty" validate:"omitempty,len=16,numeric"`
    Email    string `json:"email" validate:"required,email,max=255"`
}
```

**Note:** The `buyerNik` field uses `omitempty` because validation is conditional on auth state:
- Guest / Logged-in without NIK → handler returns `NIK_REQUIRED` if empty
- Logged-in with NIK → field ignored, server uses `token.nikHash`

### A.2 NIK Hash Generation

```go
// HMAC-SHA256 with service secret for privacy
func HashNIK(nik string, secret []byte) []byte {
    h := hmac.New(sha256.New, secret)
    h.Write([]byte(nik))
    return h.Sum(nil)
}
```

### A.3 Business Rules

| Rule | Constraint | Error Code |
|------|------------|------------|
| One entry per NIK | No active entry with same NIK hash | `NIK_ALREADY_IN_QUEUE` |
| One entry per device | No active entry with same visitor_id | `VISITOR_ALREADY_IN_QUEUE` |
| One entry per email | No active entry with same email hash | `EMAIL_ALREADY_IN_QUEUE` |
| Quota not exhausted | NIK not in quota_exhausted set | `BUYER_QUOTA_EXHAUSTED` |
| Sale window open | `sale_opens_at <= now < sale_closes_at` | `EVENT_NOT_ON_SALE` |
| Event not sold out | `saleStatus != SOLD_OUT` | `EVENT_SOLD_OUT` |
| Claim within window | `now < entry.claimDeadline` | `CLAIM_DEADLINE_PASSED` |
| Entry status ADMITTED | `entry.status == ADMITTED` | `ENTRY_NOT_ADMITTED` |

---

## Appendix B: Error Codes

| Code | HTTP | Description | User Action |
|------|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Field tidak valid | Fix input |
| `NIK_INVALID` | 422 | Format NIK salah | Perbaiki NIK |
| `NIK_ALREADY_IN_QUEUE` | 409 | NIK sudah dalam antrean | Gunakan entry yang ada |
| `VISITOR_ALREADY_IN_QUEUE` | 409 | Device sudah dalam antrean | Gunakan entry yang ada |
| `EMAIL_ALREADY_IN_QUEUE` | 409 | Email sudah dalam antrean | Gunakan entry yang ada |
| `BUYER_QUOTA_EXHAUSTED` | 409 | Kuota NIK habis | Tidak bisa join lagi |
| `NIK_REQUIRED` | 422 | buyerNik kosong untuk guest/logged-in tanpa NIK | Isi NIK |
| `EVENT_NOT_ON_SALE` | 409 | Belum/sudah tutup | Tunggu jadwal |
| `EVENT_SOLD_OUT` | 410 | Tiket habis | Event sold out |
| `CLAIM_DEADLINE_PASSED` | 403 | Giliran hangus | Kembali ke antrean |
| `ENTRY_NOT_ADMITTED` | 403 | Belum giliran | Tunggu giliran |
| `ENTRY_NOT_FOUND` | 404 | Entry tidak ada | Check ID |
| `RATE_LIMITED` | 429 | Terlalu banyak request | Tunggu Retry-After |

---

## Appendix C: Lua Scripts

### C.1 Atomic Join

```lua
-- join.lua: Atomic join dengan pengecekan constraint
-- KEYS[1] = q:{e}:seq
-- KEYS[2] = q:{e}:waiting
-- KEYS[3] = q:{e}:entry:{entryId}
-- KEYS[4] = q:{e}:nik:{nikHash}
-- KEYS[5] = q:{e}:visitor:{visitorId}
-- KEYS[6] = q:{e}:email:{emailHash}
-- KEYS[7] = q:{e}:quota_exhausted
-- ARGV = entryId, visitorId, nikHash, emailEnc, accountId, now

-- Check quota exhausted
if redis.call('SISMEMBER', KEYS[7], ARGV[3]) == 1 then
    return {'ERR', 'BUYER_QUOTA_EXHAUSTED'}
end

-- Check NIK already in queue
local existingNik = redis.call('GET', KEYS[4])
if existingNik then
    return {'EXISTING', existingNik}
end

-- Check visitor already in queue
local existingVisitor = redis.call('GET', KEYS[5])
if existingVisitor then
    return {'EXISTING', existingVisitor}
end

-- Check email already in queue
local existingEmail = redis.call('GET', KEYS[6])
if existingEmail then
    return {'EXISTING', existingEmail}
end

-- Increment sequence
local seq = redis.call('INCR', KEYS[1])

-- Create entry
redis.call('HSET', KEYS[3],
    'seq', seq,
    'visitorId', ARGV[2],
    'buyerNikHash', ARGV[3],
    'emailEnc', ARGV[4],
    'accountId', ARGV[5],
    'status', 'WAITING',
    'joinedAtMs', ARGV[6])

-- Add to waiting set
redis.call('ZADD', KEYS[2], seq, ARGV[1])

-- Set constraint keys
redis.call('SET', KEYS[4], ARGV[1])
redis.call('SET', KEYS[5], ARGV[1])
redis.call('SET', KEYS[6], ARGV[1])

return {'OK', seq, 'WAITING'}
```
