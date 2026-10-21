# Software Design Description — Booking Service

| Field | Value |
|-------|-------|
| **Service** | Booking Service |
| **Document ID** | SDD-BOOKING-001 |
| **Version** | 1.0 |
| **Date** | 2026-10-19 |
| **Status** | Draft |
| **Owner** | BE-2 |
| **Port** | 8082 |
| **Schema** | `booking` (role `svc_booking`) |
| **Region** | ap-southeast-3 (Jakarta) |

---

## 1. Overview

Booking Service adalah jantung sistem — mengelola event catalog, stok tiket, sesi pemilihan, hold, dan settlement. Semua perubahan stok terjadi dalam satu transaksi PostgreSQL, menjamin tidak ada oversell.

**Prinsip utama:**
- Single source of truth untuk stok — semua counter di PostgreSQL, bukan Valkey
- Event sourcing — siklus booking direkam sebagai event append-only
- Reservation guarantee — pembeli yang sudah admitted pasti bisa beli (tidak PHP)

---

## 2. Responsibilities

| Capability | SRS Reference | Description |
|------------|---------------|-------------|
| Event Catalog | §3.2.2, §3.2.28 | List events dengan filter (timeframe, city, status) |
| Event Detail | §3.2.3 | Detail event + kategori tiket |
| Allocation | §3.2.4 | Reserve unit untuk entry yang di-admit |
| Booking Session | §3.2.7 | Sesi pemilihan dengan TTL |
| Session Timeout | §3.2.8 | Expiry sesi yang tidak aktif |
| Reservation Guarantee | §3.2.9 | Jaminan "Jangan PHP" |
| NIK & Quota | §3.2.11 | Validasi NIK pemegang, kuota pembeli |
| Atomic Hold | §3.2.12 | Hold tiket atomik |
| Cancel | §3.2.13 | Pembatalan booking |
| Settlement | §3.2.16 | Terima pembayaran, terbitkan tiket |
| Expiry | §3.2.17 | Handle booking expired |
| Ticket | §3.2.20 | Generate e-tiket |
| Edit Order | §3.2.23 | Ubah pesanan sebelum bayar |
| Session Recovery | §3.2.24 | Pulihkan sesi setelah refresh/disconnect |
| SSE Availability | §3.2.25 | Stream ketersediaan kategori |
| Configuration | §3.2.26 | Konfigurasi event (TTL, batas, dll) |

---

## 3. API Endpoints (Public)

> **Full schemas:** See `docs/contracts/openapi/booking.yaml` for complete request/response definitions.

### 3.1 Event Catalog

```
GET /api/v1/events
```

**Query Parameters:**
| Param | Type | Required | Validation | Description |
|-------|------|----------|------------|-------------|
| `page` | int | No | min=1 | Page number (default 1) |
| `size` | int | No | min=1, max=100 | Page size (default 20) |
| `timeframe` | string | No | enum | `THIS_WEEK`, `THIS_MONTH`, `UPCOMING` |
| `city` | string | No | max=100 | Filter by city (case-insensitive) |
| `status` | string | No | enum | `NOT_STARTED`, `AVAILABLE`, `SOLD_OUT`, `CLOSED` |
| `sort` | string | No | enum | `DATE_ASC` (default), `PRICE_ASC`, `PRICE_DESC` |

**Cache:** 2s CloudFront, key = `path + page + size + timeframe + city + status`

**Response (200):**
```json
{
  "meta": { "requestId": "uuid", "serverTime": "ISO8601" },
  "data": {
    "items": [
      {
        "id": "uuid",
        "title": "string",
        "city": "string",
        "venueName": "string",
        "startsAt": "ISO8601",
        "endsAt": "ISO8601",
        "status": "AVAILABLE",
        "minPrice": 500000,
        "maxPrice": 2000000,
        "imageUrl": "string"
      }
    ],
    "pagination": {
      "page": 1,
      "size": 20,
      "totalItems": 100,
      "totalPages": 5
    }
  }
}
```

### 3.2 Event Detail

```
GET /api/v1/events/{eventId}
```

**Path Parameters:**
| Param | Type | Validation |
|-------|------|------------|
| `eventId` | uuid | required, valid UUID |

**Response (200):**
```json
{
  "meta": { ... },
  "data": {
    "id": "uuid",
    "title": "string",
    "description": "string",
    "city": "string",
    "venueName": "string",
    "startsAt": "ISO8601",
    "endsAt": "ISO8601",
    "saleOpensAt": "ISO8601",
    "saleClosesAt": "ISO8601",
    "status": "AVAILABLE",
    "categories": [
      {
        "id": "uuid",
        "name": "VIP",
        "description": "string",
        "price": 1500000,
        "available": 50,
        "capacity": 100
      }
    ],
    "config": {
      "maxTicketsPerOrder": 3,
      "maxTicketsPerBuyerNik": 3,
      "bookingTtlSeconds": 600,
      "paymentTtlSeconds": 900
    }
  }
}
```

**Errors:**
| Status | Code | Condition |
|--------|------|-----------|
| 404 | `NOT_FOUND` | Event tidak ada |

### 3.3 Create Booking Session

```
POST /api/v1/booking-sessions
```

**Headers:** `Authorization: Bearer <admissionToken>` (one-time use, 60s TTL)

**Response:**
```json
{
  "id": "session-uuid",
  "eventId": "event-uuid",
  "units": 3,
  "expiresAt": "2026-10-19T10:10:00Z",
  "serverTime": "2026-10-19T10:00:00Z",
  "categories": [...],
  "config": {
    "maxTicketsPerOrder": 3,
    "sseEnabled": true,
    "pollingIntervalMs": 3000
  }
}
```

### 3.4 Get Current Session

```
GET /api/v1/booking-sessions/current?eventId={eventId}
```

**Headers:** `X-Visitor-Token`

### 3.5 Get Session by ID

```
GET /api/v1/booking-sessions/{sessionId}
```

### 3.6 SSE Availability Stream

```
GET /api/v1/booking-sessions/{sessionId}/stream
```

**Headers:** `Accept: text/event-stream`

**Events:**
```
event: availability
data: {"categories":[{"id":"uuid","available":49}]}

event: session
data: {"expiresAt":"...","serverTime":"..."}
```

### 3.7 Validate Attendee NIK

```
POST /api/v1/booking-sessions/{sessionId}/attendee-checks
```

**Request:**
```json
{
  "attendeeNiks": ["3201010101010001", "3201010101010002"]
}
```

### 3.8 Cancel Session

```
POST /api/v1/booking-sessions/{sessionId}/cancel
```

### 3.9 Create Booking (Hold)

```
POST /api/v1/bookings
```

**Headers:** `Idempotency-Key: <uuid>`

**Request:**
```json
{
  "sessionId": "session-uuid",
  "items": [
    { "categoryId": "cat-uuid", "attendeeNik": "3201010101010001" }
  ]
}
```

### 3.10 Get Booking

```
GET /api/v1/bookings/{bookingId}
```

### 3.11 Edit Booking

```
PUT /api/v1/bookings/{bookingId}
```

**Constraint:** Only before payment started, max 3 edits

### 3.12 Get Ticket

```
GET /api/v1/tickets/{bookingId}
```

---

## 4. Internal API (Provided)

### 4.1 Reserve Allocation Batch (from Queue)

```
POST /internal/v1/allocations:batch
```

**Idempotency:** Per `queueEntryId`

### 4.2 Get Supply Snapshot (from Queue)

```
GET /internal/v1/events/{id}/supply
```

**Response:**
```json
{
  "totalCapacity": 1000,
  "allocatedUnits": 200,
  "heldUnits": 50,
  "soldUnits": 150,
  "freeUnits": 600,
  "saleStatus": "OPEN"
}
```

### 4.3 Get Payable Info (from Payment)

```
GET /internal/v1/bookings/{id}/payable
```

### 4.4 Payment Lock (from Payment)

```
POST /internal/v1/bookings/{id}/payment-lock
```

**Purpose:** Lock booking saat user tekan "Bayar"

### 4.5 Deadline Extension (from Payment)

```
POST /internal/v1/bookings/{id}/deadline-extension
```

**Purpose:** Perpanjang TTL saat gateway degraded (SHOULD)

---

## 5. Internal API (Called)

### 5.1 Resolve Payment (to Payment)

```
POST /internal/v1/payments/{bookingId}/resolve → Payment Service
```

**Purpose:** Check/cancel di gateway saat booking expired

---

## 6. Data Model (PostgreSQL)

### 6.1 Core Tables

```sql
-- Events and categories
CREATE TABLE booking.events (
  id UUID PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT,
  city TEXT NOT NULL,
  venue_name TEXT NOT NULL,
  starts_at TIMESTAMPTZ NOT NULL,
  ends_at TIMESTAMPTZ NOT NULL,
  sale_opens_at TIMESTAMPTZ NOT NULL,
  sale_closes_at TIMESTAMPTZ NOT NULL,
  max_tickets_per_order INT NOT NULL DEFAULT 3,
  max_tickets_per_buyer_nik INT NOT NULL DEFAULT 3,
  booking_ttl_seconds INT NOT NULL DEFAULT 600,
  payment_ttl_seconds INT NOT NULL DEFAULT 900,
  rules_locked_at TIMESTAMPTZ,
  status TEXT NOT NULL DEFAULT 'DRAFT',
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE booking.ticket_categories (
  id UUID PRIMARY KEY,
  event_id UUID NOT NULL REFERENCES booking.events(id),
  name TEXT NOT NULL,
  description TEXT,
  price BIGINT NOT NULL,
  capacity INT NOT NULL,
  sort_order INT NOT NULL DEFAULT 0
);

CREATE TABLE booking.category_inventories (
  category_id UUID PRIMARY KEY REFERENCES booking.ticket_categories(id),
  capacity INT NOT NULL,
  held INT NOT NULL DEFAULT 0,
  sold INT NOT NULL DEFAULT 0,
  CONSTRAINT inv_held_sold CHECK (held >= 0 AND sold >= 0 AND held + sold <= capacity)
);

CREATE TABLE booking.event_supply (
  event_id UUID PRIMARY KEY REFERENCES booking.events(id),
  total_capacity INT NOT NULL,
  allocated_units INT NOT NULL DEFAULT 0,
  held_units INT NOT NULL DEFAULT 0,
  sold_units INT NOT NULL DEFAULT 0,
  CONSTRAINT supply_check CHECK (
    allocated_units >= 0 AND held_units >= 0 AND sold_units >= 0 AND
    allocated_units + held_units + sold_units <= total_capacity
  )
);
```

### 6.2 Admission and Session

```sql
CREATE TABLE booking.admissions (
  id UUID PRIMARY KEY,
  event_id UUID NOT NULL,
  visitor_id UUID NOT NULL,
  buyer_nik_hash BYTEA NOT NULL,
  queue_entry_id UUID,
  account_id UUID,
  status TEXT NOT NULL DEFAULT 'RESERVED',
  units INT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  cfg_booking_ttl_seconds INT NOT NULL,
  cfg_max_tickets_per_order INT NOT NULL,
  edit_count INT NOT NULL DEFAULT 0,
  cfg_max_edits INT NOT NULL DEFAULT 3,
  lock_reason TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  CONSTRAINT one_active_per_visitor UNIQUE (event_id, visitor_id) WHERE status IN ('RESERVED','ACTIVE','EDITABLE')
);

CREATE TABLE booking.attendee_checks (
  id UUID PRIMARY KEY,
  admission_id UUID NOT NULL REFERENCES booking.admissions(id),
  attendee_nik_hash BYTEA NOT NULL,
  valid BOOLEAN NOT NULL,
  checked_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE booking.buyer_quotas (
  event_id UUID NOT NULL,
  buyer_nik_hash BYTEA NOT NULL,
  held INT NOT NULL DEFAULT 0,
  paid INT NOT NULL DEFAULT 0,
  PRIMARY KEY (event_id, buyer_nik_hash),
  CONSTRAINT quota_check CHECK (held >= 0 AND paid >= 0)
);
```

### 6.3 Booking and Tickets

```sql
CREATE TABLE booking.bookings (
  id UUID PRIMARY KEY,
  admission_id UUID NOT NULL REFERENCES booking.admissions(id),
  event_id UUID NOT NULL,
  visitor_id UUID NOT NULL,
  buyer_nik_hash BYTEA NOT NULL,
  account_id UUID,
  status TEXT NOT NULL DEFAULT 'HELD',
  amount BIGINT NOT NULL,
  item_quantity INT NOT NULL,
  version INT NOT NULL DEFAULT 1,
  expires_at TIMESTAMPTZ NOT NULL,
  payment_started_at TIMESTAMPTZ,
  paid_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE booking.booking_items (
  id UUID PRIMARY KEY,
  booking_id UUID NOT NULL REFERENCES booking.bookings(id),
  category_id UUID NOT NULL,
  attendee_nik_enc BYTEA NOT NULL,
  attendee_nik_hash BYTEA NOT NULL,
  price BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'HELD',
  replaced_by UUID,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE booking.tickets (
  id UUID PRIMARY KEY,
  booking_item_id UUID NOT NULL UNIQUE REFERENCES booking.booking_items(id),
  public_code TEXT NOT NULL UNIQUE,
  qr_payload TEXT NOT NULL,
  issued_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 6.4 Event Store

```sql
CREATE TABLE booking.booking_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  aggregate_id UUID NOT NULL,
  aggregate_type TEXT NOT NULL DEFAULT 'Booking',
  event_type TEXT NOT NULL,
  version INT NOT NULL,
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (aggregate_id, version)
);

-- Append-only trigger
CREATE TRIGGER booking_events_append_only
  BEFORE UPDATE OR DELETE ON booking.booking_events
  FOR EACH ROW EXECUTE FUNCTION raise_append_only_violation();
```

### 6.5 Outbox and Idempotency

```sql
CREATE TABLE booking.outbox_events (
  id UUID PRIMARY KEY,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  published_at TIMESTAMPTZ
);

CREATE TABLE booking.idempotency_records (
  scope TEXT NOT NULL,
  owner TEXT NOT NULL,
  key TEXT NOT NULL,
  response JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (scope, owner, key)
);
```

---

## 7. State Machines

### 7.1 Admission State

```
┌──────────┐   claim session   ┌────────┐   hold    ┌──────────┐   lock    ┌───────────┐
│ RESERVED │─────────────────▶ │ ACTIVE │─────────▶ │ EDITABLE │────────▶ │ CONVERTED │
└──────────┘                   └────────┘           └──────────┘          └───────────┘
     │                              │                     │
     │ expired/unclaimed            │ session expired     │ booking expired
     ▼                              ▼                     ▼
┌──────────┐                  ┌──────────┐          ┌──────────┐
│  ENDED   │                  │  ENDED   │          │  ENDED   │
└──────────┘                  └──────────┘          └──────────┘
```

### 7.2 Booking State

```
┌────────┐   payment started   ┌────────┐   payment succeeded   ┌────────┐
│  HELD  │────────────────────▶│ LOCKED │──────────────────────▶│  PAID  │
└────────┘                     └────────┘                       └────────┘
     │                              │
     │ TTL expired                  │ TTL expired (with resolve)
     ▼                              ▼
┌─────────┐                   ┌─────────┐
│ EXPIRED │                   │ EXPIRED │ or REVIEW (if payment found)
└─────────┘                   └─────────┘
```

**Booking event types:**
`BookingHeld`, `BookingItemsReplaced`, `BookingLocked`, `PaymentVerifying`, `BookingPaid`, `BookingExpired`, `BookingCancelled`, `BookingReview`

---

## 8. Inter-Service Communication

### 8.1 Events Published

| Event | Routing Key | Consumer(s) | Payload |
|-------|-------------|-------------|---------|
| Journey Ended | `booking.journey.ended` | Queue | `{ bookingId, eventId, visitorId, queueEntryId, outcome }` |
| Quota Exhausted | `booking.quota.exhausted` | Queue | `{ eventId, buyerNikHash }` |
| Sold Out | `booking.sold_out` | Queue | `{ eventId }` |
| Paid | `booking.paid` | Notification | `{ bookingId, email, tickets[] }` |
| Expired | `booking.expired` | Notification | `{ bookingId, email }` |
| Review | `booking.review` | Notification, Payment | `{ bookingId, email, paymentId }` |

### 8.2 Events Consumed

| Event | Producer | Purpose | Idempotency |
|-------|----------|---------|-------------|
| `payment.succeeded` | Payment | Apply settlement, issue tickets | `booking.status != PAID` |
| `payment.failed` | Payment | Info only, booking waits for TTL | — |

---

## 9. Workers

### 9.1 Session Sweeper

**Interval:** 10s

**Logic:** Find ACTIVE admissions where `expires_at < now`, transition to ENDED, release allocation

### 9.2 Booking Expiry Sweeper

**Interval:** 10s

**Logic:**
1. Find HELD/LOCKED bookings where `expires_at < now`
2. Call `POST /internal/v1/payments/{id}/resolve` (for LOCKED)
3. If no payment found: expire booking, release stock
4. If payment found: create REVIEW case

### 9.3 Outbox Relay

**Interval:** 1s

**Logic:** Publish unpublished outbox_events to RabbitMQ, mark published

---

## 10. Configuration

```bash
# Application
APP_ENV=development
APP_NAME=booking-service
APP_PORT=8082
LOG_LEVEL=info

# PostgreSQL
DATABASE_URL=postgres://svc_booking:xxx@localhost:5432/rusuh?search_path=booking

# Valkey (for SSE cache)
VALKEY_URL=localhost:6379

# RabbitMQ
RABBITMQ_URL=amqp://guest:guest@localhost:5672/

# Payment Service (internal)
PAYMENT_SERVICE_URL=http://payment:8083

# Token
TOKEN_PUBLIC_KEY_PATH=/keys/queue.pub
BOOKING_TOKEN_PRIVATE_KEY_PATH=/keys/booking.key

# Booking defaults
BOOKING_TTL_SECONDS=600
PAYMENT_TTL_SECONDS=900
MAX_TICKETS_PER_ORDER=3
MAX_EDITS=3

# SSE
SSE_ENABLED=true
SSE_CACHE_INTERVAL_MS=1000
```

---

## 11. Observability

### 11.1 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rbt_booking_sessions_total` | Counter | `event_id`, `status` | Sessions created |
| `rbt_booking_holds_total` | Counter | `event_id`, `status` | Bookings held |
| `rbt_booking_settlements_total` | Counter | `event_id` | Successful payments |
| `rbt_booking_expirations_total` | Counter | `event_id`, `reason` | Expired bookings |
| `rbt_booking_stock_gauge` | Gauge | `event_id`, `type` | allocated/held/sold/free |
| `rbt_booking_event_store_version` | Gauge | `aggregate_id` | Latest event version |

### 11.2 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| BookingStockNegative | Any counter < 0 | Critical |
| BookingExpiryRateHigh | expiry_rate > 20% for 10m | Warning |
| BookingOutboxLag | lag > 30s for 5m | Warning |
| BookingDBLatencyHigh | p99 > 100ms for 5m | Warning |

---

## 12. Running Locally

```bash
# From repo root
make infra keys

# Start service
cd services/booking
cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &
go run ./cmd worker &

# Test
curl localhost:8082/health
curl localhost:8082/api/v1/events
```

---

## 13. References

- OpenAPI: `docs/contracts/openapi/booking.yaml`
- Internal API: `docs/contracts/openapi/internal-booking.yaml`
- HLD §4.4.3: Selection Session sequence
- HLD §4.4.4: Hold and Settlement sequence
- SRS §3.2.7-§3.2.13: Booking requirements
- ADR-019: Event sourcing

---

## 14. Test Case Alignment

| Feature | SRS | Test Cases | Level |
|---------|-----|------------|-------|
| Event catalog | SRS-F-02-01..04 | TC-EVT-01, TC-EVT-02, TC-EVT-03 | Integration |
| Event catalog filter | SRS-F-28-01..06 | TC-CAT-01, TC-CAT-02, TC-CAT-03, TC-CAT-04, TC-CAT-05, TC-CAT-06 | Integration |
| Event detail | SRS-F-03-01..05 | TC-EVT-04, TC-EVT-05 | Integration |
| Admission | SRS-F-09-01..08 | TC-ADM-01, TC-ADM-02, TC-ADM-03, TC-ADM-04, TC-ADM-05 | Integration + E2E |
| Booking session | SRS-F-07-01..06 | TC-SES-01, TC-SES-02, TC-SES-03, TC-SES-04, TC-PAY-02 | Integration + E2E |
| Session timeout | SRS-F-08-01..03 | TC-SES-05, TC-SES-06, TC-SES-07, TC-SES-08 | Integration |
| NIK/Quota | SRS-F-11-01..10 | TC-BKG-01, TC-BKG-02, TC-BKG-03, TC-BKG-08, TC-BKG-09, TC-BKG-10, TC-BKG-11, TC-BKG-12 | Integration |
| Atomic hold | SRS-F-12-01..07 | TC-BKG-04, TC-BKG-05, TC-BKG-06, TC-BKG-07 | Integration |
| Edit order | SRS-F-23-01..11 | TC-EDT-01, TC-EDT-02, TC-EDT-03, TC-EDT-04, TC-EDT-05, TC-EDT-06 | Integration + E2E |
| Session recovery | SRS-F-24-01..13 | TC-NAV-01, TC-NAV-02, TC-NAV-03, TC-SES-05, TC-SES-06, TC-SES-07, TC-SES-08 | E2E |
| SSE availability | SRS-F-25-01..13 | TC-SSE-01, TC-SSE-02, TC-SSE-03 | Integration |
| Settlement | SRS-F-16-01..03 | TC-PAY-03, TC-PAY-06 | Integration |
| Expiry | SRS-F-17-01..05 | TC-PAY-04, TC-PAY-07, TC-PAY-08 | Integration |
| Ticket | SRS-F-20-01..03 | TC-TKT-01, TC-TKT-02, TC-TKT-03 | Integration + E2E |
| Config audit | SRS-F-26-01..08 | TC-CFG-01, TC-CFG-02, TC-CFG-03, TC-CFG-04 | Integration |
| Event sourcing | SRS-C-09 | TC-ES-01, TC-ES-02, TC-ES-03, TC-ES-04, TC-ES-05 | Integration |
| No oversell | SRS-Q-FUN-01 | TC-BKG-07, TC-PERF-04, Harness | Integration + Load |
| Reconciliation | SRS-F-90-01 | TC-OPS-01 | Integration + Demonstration |
| Migration | SRS-C-08 | TC-MIG-01 | Integration |
| API contract | SRS-IF-30..31 | TC-API-01, TC-API-02 | Contract |
| Autoscaling | SRS-Q-FLX-01 | TC-ASG-01 | Infrastructure |
| Failover | SRS-Q-REL-* | TC-CAP-01, TC-CAP-02, TC-CAP-03 | Chaos |
| Observability | SRS-Q-MNT-02 | TC-OBS-01, TC-OBS-02 | Demonstration |
| Booking history | SRS-F-27-11 | TC-USR-10 | E2E |

### Key Test Scenarios

| TC ID | Scenario | Expected | Validates |
|-------|----------|----------|-----------|
| TC-ADM-01 | Reservation at admission | Units reserved, admission ACTIVE | Core guarantee |
| TC-ADM-02 | Admitted always gets ticket | Hold succeeds within TTL | Jangan PHP |
| TC-ADM-05 | Category sold out, mixed succeeds | Other categories available | Mixed category |
| TC-BKG-01 | Buyer quota across sessions | held + paid ≤ 3 | Quota constraint |
| TC-BKG-02 | Same attendee NIK in parallel | One succeeds, one 409 | NIK uniqueness |
| TC-BKG-03 | NIK already has ticket in event | 409 ATTENDEE_NIK_ALREADY_REGISTERED | Constraint |
| TC-BKG-07 | 100 parallel holds on last 50 tickets | Exactly 50 succeed | No oversell |
| TC-BKG-11 | Buyer quota separate from attendee | Buyer can buy for others | Quota separation |
| TC-CAT-01 | Filter by timeframe | Only matching events | Catalog filter |
| TC-CFG-01 | Config snapshot per session | Changes don't affect running sessions | Config isolation |
| TC-EDT-01 | Edit before payment | Items replaced, version++ | Edit flow |
| TC-ES-01 | Event sourcing audit trail | All events recorded, replayable | Event store |
| TC-SES-05 | Refresh during selection | Session recovered | Idempotency |
| TC-OPS-01 | Run reconciliation job | Inconsistencies detected, alert sent | Operations |
| TC-USR-10 | Logged-in user views history | Only own bookings | Auth + ownership |

---

## 15. Booking History for Logged-in Users

### Overview

Logged-in users can view their booking history via `GET /me/bookings`. This endpoint lives in **Booking Service** but requires User Service access token validation.

**SRS Reference:** SRS-F-27-11, Test: TC-USR-10

### Endpoint

```
GET /api/v1/me/bookings?page=1&limit=10&status=PAID
```

**Auth:** Access token (JWT from User Service)

**Query Parameters:**
| Param | Type | Default | Description |
|-------|------|---------|-------------|
| page | int | 1 | Page number |
| limit | int | 10 | Items per page (max 50) |
| status | string | all | Filter: PENDING, PAID, EXPIRED, CANCELLED, REVIEW |

**Response:**
```json
{
  "meta": { "code": 200, "requestId": "..." },
  "data": {
    "items": [
      {
        "id": "uuid",
        "eventId": "uuid",
        "eventName": "Coldplay World Tour",
        "eventDate": "2027-01-15T19:00:00Z",
        "status": "PAID",
        "itemCount": 2,
        "totalAmount": 3000000,
        "createdAt": "2026-12-25T10:00:00Z",
        "tickets": [
          { "code": "TKT-ABC123", "category": "VIP", "attendeeName": "John Doe" }
        ]
      }
    ],
    "pagination": { "page": 1, "limit": 10, "total": 25, "totalPages": 3 }
  }
}
```

### Implementation

```go
// port/inbound/booking_history.go
type BookingHistoryUsecase interface {
    ListUserBookings(ctx context.Context, accountID uuid.UUID, filter BookingFilter) (*BookingListResponse, error)
}

// Query: SELECT b.*, e.name, e.event_date FROM bookings b 
//        JOIN events e ON b.event_id = e.id
//        WHERE b.account_id = $1 ORDER BY b.created_at DESC
```

### Data Flow

```
Client ──► Booking Service ──► Verify Access Token (JWT EdDSA)
                │                        │
                │                        └── Public key from User Service (cached)
                ▼
        Query bookings WHERE account_id = token.sub
```

### Security
- Token validation using User Service public key (EdDSA)
- Strict ownership: only returns bookings matching `account_id` claim
- No cross-account data leakage

---

## Appendix A: Validation Rules

### A.1 NIK Validation

```go
// NIK must be 16 digits, valid structure
func ValidateNIK(nik string) error {
    if len(nik) != 16 { return ErrNIKLength }
    if !isNumeric(nik) { return ErrNIKFormat }
    
    // Province code (01-99)
    province, _ := strconv.Atoi(nik[0:2])
    if province < 1 || province > 99 { return ErrNIKProvince }
    
    // Birth date (DDMMYY, female +40 to DD)
    dd, _ := strconv.Atoi(nik[6:8])
    if dd > 71 || (dd > 31 && dd < 41) { return ErrNIKBirthDate }
    
    return nil
}
```

### A.2 Field Length Limits

| Field | Min | Max | Notes |
|-------|-----|-----|-------|
| `buyerNik` | 16 | 16 | Exact |
| `attendeeNik` | 16 | 16 | Exact |
| `email` | 5 | 255 | RFC 5322 |
| `categoryId` | 36 | 36 | UUID |
| `sessionId` | 36 | 36 | UUID |

### A.3 Business Rules

| Rule | Constraint | Error Code |
|------|------------|------------|
| Max tickets per order | `items.length <= config.maxTicketsPerOrder` | `MAX_TICKETS_EXCEEDED` |
| Max tickets per NIK | `buyer quota held + paid < maxTicketsPerBuyerNik` | `BUYER_QUOTA_EXCEEDED` |
| NIK unique per event | One active ticket per NIK per event | `NIK_ALREADY_HAS_TICKET` |
| Edit before payment | `booking.paymentStartedAt == null` | `BOOKING_LOCKED` |
| Max edits | `admission.editCount < cfg_max_edits` | `MAX_EDITS_EXCEEDED` |
| Session TTL | Request within `admission.expiresAt` | `SESSION_EXPIRED` |
| Booking TTL | Payment within `booking.expiresAt` | `BOOKING_EXPIRED` |

---

## Appendix B: Error Codes

| Code | HTTP | Description | User Action |
|------|------|-------------|-------------|
| `NOT_FOUND` | 404 | Resource tidak ditemukan | Check ID |
| `VALIDATION_ERROR` | 400 | Field tidak valid | Fix input |
| `SESSION_EXPIRED` | 410 | TTL sesi habis | Kembali ke antrean |
| `BOOKING_EXPIRED` | 410 | TTL booking habis | Kembali ke antrean |
| `BOOKING_LOCKED` | 409 | Sudah mulai bayar | Selesaikan pembayaran |
| `BOOKING_NOT_FOUND` | 404 | Booking tidak ada | Check ID |
| `CATEGORY_SOLD_OUT` | 409 | Kategori habis | Pilih kategori lain |
| `NO_SUPPLY` | 409 | Stok event habis | Event sold out |
| `NIK_INVALID` | 422 | Format NIK salah | Perbaiki NIK |
| `NIK_ALREADY_HAS_TICKET` | 409 | NIK sudah punya tiket | Gunakan NIK lain |
| `BUYER_QUOTA_EXCEEDED` | 409 | Kuota pembelian habis | Batas tercapai |
| `MAX_TICKETS_EXCEEDED` | 400 | Melebihi batas per order | Kurangi jumlah |
| `MAX_EDITS_EXCEEDED` | 409 | Melebihi batas edit | Lanjut bayar |
| `ATTENDEE_CHECK_LIMIT` | 429 | Terlalu banyak cek NIK | Tunggu |
| `ADMISSION_TOKEN_INVALID` | 403 | Token admission salah | Claim ulang |
| `ADMISSION_TOKEN_EXPIRED` | 403 | Token admission expired | Claim ulang |
| `VISITOR_MISMATCH` | 403 | Visitor tidak cocok | Gunakan device sama |
| `IDEMPOTENCY_CONFLICT` | 409 | Request duplikat berbeda | Gunakan key baru |

---

## Appendix C: Event Administration (Internal/Seed)

### C.1 Overview

Per PRD §2.2 dan SRS §3.2.26, back-office event creation is **out of scope**. Event data is provided through:
1. **Migration/Seed** - Primary method for PoC
2. **Internal Admin API** - For automated testing and future back-office

### C.2 Internal Admin Endpoints

> ⚠️ These endpoints are for internal use only, not exposed via public ALB.

#### Create Event
```
POST /internal/v1/admin/events
```

**Auth:** Admin service token (JWT with `role: admin`)

**Request:**
```json
{
  "name": "Coldplay World Tour Jakarta",
  "venue": "Gelora Bung Karno",
  "city": "Jakarta",
  "eventDate": "2027-01-15T19:00:00+07:00",
  "saleStart": "2026-12-25T10:00:00+07:00",
  "saleEnd": "2027-01-15T18:00:00+07:00",
  "imageUrl": "https://cdn.example.com/events/coldplay-2027.jpg",
  "bannerUrl": "https://cdn.example.com/events/coldplay-2027-banner.jpg",
  "categories": [
    { "code": "VIP", "name": "VIP", "price": 5000000, "capacity": 1000 },
    { "code": "CAT1", "name": "Category 1", "price": 2500000, "capacity": 5000 },
    { "code": "FES", "name": "Festival", "price": 1000000, "capacity": 20000 }
  ],
  "config": {
    "maxTicketsPerOrder": 3,
    "maxTicketsPerBuyerNik": 3,
    "selectionTtlSeconds": 300,
    "paymentTtlSeconds": 600,
    "claimWindowSeconds": 180,
    "allowMixedCategories": true
  }
}
```

**Response:** `201 Created` with event ID

#### Upload Event Image
```
POST /internal/v1/admin/events/{eventId}/images
Content-Type: multipart/form-data
```

**Form Fields:**
| Field | Type | Max Size | Formats |
|-------|------|----------|---------|
| `image` | file | 5MB | JPEG, PNG, WebP |
| `type` | string | - | `poster`, `banner`, `thumbnail` |

**Processing:**
1. Validate file type and size
2. Generate unique filename: `{eventId}/{type}_{timestamp}.{ext}`
3. Upload to S3 bucket: `rbt-event-images-{env}`
4. Return CDN URL: `https://cdn.rbt.example.com/events/...`

**Response:**
```json
{
  "imageUrl": "https://cdn.rbt.example.com/events/uuid/poster_1730123456.webp",
  "type": "poster"
}
```

#### Update Event
```
PATCH /internal/v1/admin/events/{eventId}
```

**Rules:**
- Cannot change `config` after `rulesLockedAt` (sale started)
- Changes logged to `config_audit` table

### C.3 Seed Data Structure

For PoC, events are seeded via migration:

```sql
-- deployments/migrations/booking/0003_seed_events.sql
INSERT INTO events (id, name, venue, city, event_date, sale_start, sale_end, 
                    image_url, banner_url, status, config)
VALUES (
  'e1b2c3d4-...',
  'Coldplay World Tour Jakarta',
  'Gelora Bung Karno',
  'Jakarta',
  '2027-01-15 19:00:00+07',
  '2026-12-25 10:00:00+07',
  '2027-01-15 18:00:00+07',
  'https://cdn.rbt.example.com/events/coldplay-poster.jpg',
  'https://cdn.rbt.example.com/events/coldplay-banner.jpg',
  'NOT_STARTED',
  '{"maxTicketsPerOrder":3,"maxTicketsPerBuyerNik":3,...}'::jsonb
);

INSERT INTO ticket_categories (id, event_id, code, name, price, capacity)
VALUES 
  ('cat-vip-...', 'e1b2c3d4-...', 'VIP', 'VIP', 5000000, 1000),
  ('cat-c1-...', 'e1b2c3d4-...', 'CAT1', 'Category 1', 2500000, 5000),
  ('cat-fes-...', 'e1b2c3d4-...', 'FES', 'Festival', 1000000, 20000);
```

### C.4 Image Storage Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ Admin/Seed  │────▶│ S3 Bucket   │────▶│ CloudFront  │
│ Upload      │     │ (private)   │     │ CDN         │
└─────────────┘     └─────────────┘     └─────────────┘
                           │                    │
                           │                    ▼
                    ┌──────▼──────┐     ┌─────────────┐
                    │ Lambda      │     │ Client      │
                    │ Image       │     │ (public     │
                    │ Processor   │     │  read-only) │
                    └─────────────┘     └─────────────┘

Bucket: rbt-event-images-{env}
CDN: cdn.rbt.example.com/events/*
Cache: 1 day (immutable filenames)
```

### C.5 Events Table Schema Addition

```sql
-- Add image columns to events table
ALTER TABLE booking.events ADD COLUMN IF NOT EXISTS 
  image_url TEXT,
  banner_url TEXT,
  thumbnail_url TEXT;
```

### C.6 Future: Back-Office Integration

When back-office is implemented (post-PoC):
1. Back-office calls internal admin API
2. Uses same service token auth
3. Image upload via presigned S3 URL for large files
4. Audit trail maintained in `config_audit`
