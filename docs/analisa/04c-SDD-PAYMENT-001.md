# Software Design Description — Payment Service

| Field | Value |
|-------|-------|
| **Service** | Payment Service |
| **Document ID** | SDD-PAYMENT-001 |
| **Version** | 1.0 |
| **Date** | 2026-10-19 |
| **Status** | Draft |
| **Owner** | BE-2 |
| **Port** | 8083 |
| **Schema** | `payment` (role `svc_payment`) |
| **Region** | ap-southeast-3 (Jakarta) |

---

## 1. Overview

Payment Service mengintegrasikan dengan Midtrans payment gateway. Service ini menangani payment page, charge request, webhook callback, dan refund cases. Siklus attempt direkam dengan event sourcing.

**Prinsip utama:**
- Idempotent webhook handling — callback dapat diterima berkali-kali
- Signature verification — semua webhook diverifikasi dengan server key
- At-most-once charge — tidak ada double charge
- Event sourcing — audit trail lengkap untuk compliance

---

## 2. Responsibilities

| Capability | SRS Reference | Description |
|------------|---------------|-------------|
| Payment Page | §3.2.14 | View model halaman pembayaran |
| Page Token | §3.2.18 | Token untuk mobile WebView |
| Payment Methods | §3.2.14 | Daftar metode tersedia |
| Payment Attempt | §3.2.14 | Create charge ke Midtrans |
| Webhook | §3.2.15 | Handle callback dari Midtrans |
| Settlement | §3.2.16 | Publish payment.succeeded |
| Expiry | §3.2.17 | Handle attempt expired |
| Circuit Breaker | §3.2.19 | Degradation protection |
| Refund Cases | §3.2.17 | Track late payment untuk refund |

---

## 3. API Endpoints (Public)

### 3.1 Get Payment Page

```
GET /api/v1/payments/{bookingId}/page
```

**Headers:** `X-Visitor-Token` or cookie `rb_visitor` or `rb_pay`

**Response:**
```json
{
  "bookingId": "uuid",
  "amount": 3000000,
  "currency": "IDR",
  "expiresAt": "2026-10-19T10:15:00Z",
  "serverTime": "2026-10-19T10:05:00Z",
  "status": "AWAITING_PAYMENT",
  "items": [
    {
      "categoryName": "VIP",
      "attendeeNikMasked": "320101****0001",
      "price": 1500000
    }
  ],
  "availableMethods": [
    { "code": "gopay", "name": "GoPay", "type": "ewallet" },
    { "code": "qris", "name": "QRIS", "type": "qr" },
    { "code": "bank_transfer_bca", "name": "BCA VA", "type": "va" }
  ],
  "currentAttempt": null
}
```

### 3.2 Create Page Token (Mobile WebView)

```
POST /api/v1/payments/{bookingId}/page-tokens
```

**Response:**
```json
{
  "token": "one-time-token",
  "expiresAt": "2026-10-19T10:06:00Z",
  "pageUrl": "https://api.rusuh.example/pay/{bookingId}?token=..."
}
```

### 3.3 Exchange Page Token

```
POST /api/v1/payment-page-sessions
```

**Request:**
```json
{
  "token": "one-time-token"
}
```

**Response:** Sets `rb_pay` cookie

### 3.4 Create Payment Attempt

```
POST /api/v1/payments/{bookingId}/attempts
```

**Headers:** `Idempotency-Key: <uuid>`

**Request:**
```json
{
  "method": "gopay"
}
```

**Response:**
```json
{
  "attemptId": "uuid",
  "status": "PENDING",
  "method": "gopay",
  "expiresAt": "2026-10-19T10:20:00Z",
  "instructions": {
    "type": "redirect",
    "redirectUrl": "https://simulator.sandbox.midtrans.com/...",
    "qrCodeUrl": null,
    "vaNumber": null
  }
}
```

### 3.5 Get Payment Status

```
GET /api/v1/payments/{bookingId}/status
```

**Response:**
```json
{
  "bookingId": "uuid",
  "status": "PAID",
  "paidAt": "2026-10-19T10:08:00Z",
  "method": "gopay",
  "ticketUrl": "/tickets/uuid"
}
```

### 3.6 Midtrans Webhook

```
POST /api/v1/webhooks/midtrans
```

**Headers:** Signature from Midtrans

**Processing:**
1. Verify signature: `SHA512(order_id + status_code + gross_amount + server_key)`
2. Check callback fingerprint for idempotency
3. Update attempt status
4. Publish event

---

## 4. Internal API (Provided)

### 4.1 Resolve Payment (from Booking)

```
POST /internal/v1/payments/{bookingId}/resolve
```

**Purpose:** Called by Booking during expiry sweep

**Request:**
```json
{
  "action": "CHECK_AND_CANCEL",
  "reason": "BOOKING_EXPIRED"
}
```

**Response:**
```json
{
  "bookingId": "uuid",
  "resolution": "NO_PAYMENT",
  "cancelledAtGateway": true
}
```

**Resolution values:**
| Resolution | Description |
|------------|-------------|
| `NO_PAYMENT` | No payment at gateway |
| `PAYMENT_FOUND` | Payment exists (race condition) |
| `REFUND_REQUIRED` | Payment after cancel → refund case |

---

## 5. Internal API (Called)

### 5.1 Get Payable Info (to Booking)

```
GET /internal/v1/bookings/{id}/payable → Booking Service
```

### 5.2 Payment Lock (to Booking)

```
POST /internal/v1/bookings/{id}/payment-lock → Booking Service
```

**Purpose:** Lock booking when user clicks "Bayar"

### 5.3 Deadline Extension (to Booking)

```
POST /internal/v1/bookings/{id}/deadline-extension → Booking Service
```

**Purpose:** Extend TTL when gateway is degraded (SHOULD)

---

## 6. Data Model (PostgreSQL)

### 6.1 Payment Tables

```sql
CREATE TABLE payment.payment_attempts (
  id UUID PRIMARY KEY,
  booking_id UUID NOT NULL,
  method TEXT NOT NULL,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL DEFAULT 'IDR',
  status TEXT NOT NULL DEFAULT 'CREATED',
  midtrans_order_id TEXT UNIQUE,
  midtrans_transaction_id TEXT,
  redirect_url TEXT,
  qr_code_url TEXT,
  va_number TEXT,
  bank_code TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  settled_at TIMESTAMPTZ,
  failed_at TIMESTAMPTZ,
  failure_reason TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE payment.payment_callbacks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  attempt_id UUID NOT NULL REFERENCES payment.payment_attempts(id),
  fingerprint TEXT NOT NULL UNIQUE,
  raw_payload JSONB NOT NULL,
  transaction_status TEXT NOT NULL,
  processing_result TEXT NOT NULL,
  processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE payment.payment_page_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id UUID NOT NULL,
  token_hash BYTEA NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE payment.refund_cases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id UUID NOT NULL,
  attempt_id UUID NOT NULL REFERENCES payment.payment_attempts(id),
  amount BIGINT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'OPEN',
  refunded_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 6.2 Event Store

```sql
CREATE TABLE payment.payment_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  aggregate_id UUID NOT NULL,
  aggregate_type TEXT NOT NULL DEFAULT 'PaymentAttempt',
  event_type TEXT NOT NULL,
  version INT NOT NULL,
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (aggregate_id, version)
);

-- Append-only trigger
CREATE TRIGGER payment_events_append_only
  BEFORE UPDATE OR DELETE ON payment.payment_events
  FOR EACH ROW EXECUTE FUNCTION raise_append_only_violation();
```

### 6.3 Outbox and Idempotency

```sql
CREATE TABLE payment.outbox_events (
  id UUID PRIMARY KEY,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  published_at TIMESTAMPTZ
);

CREATE TABLE payment.idempotency_records (
  scope TEXT NOT NULL,
  owner TEXT NOT NULL,
  key TEXT NOT NULL,
  response JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (scope, owner, key)
);
```

---

## 7. State Machine

### 7.1 Payment Attempt State

```
┌─────────┐   charge    ┌─────────┐   webhook settled   ┌─────────┐
│ CREATED │────────────▶│ PENDING │───────────────────▶ │ SETTLED │
└─────────┘             └─────────┘                     └─────────┘
                             │
                             │ webhook failed/expired
                             ▼
                        ┌─────────┐
                        │ FAILED  │
                        └─────────┘
                             │
                             │ TTL expired (no webhook)
                             ▼
                        ┌─────────┐
                        │ EXPIRED │
                        └─────────┘
```

**Event types:**
`AttemptCreated`, `InstructionIssued`, `SettlementReceived`, `AttemptFailed`, `AttemptExpired`, `RefundRequired`

---

## 8. Midtrans Integration

### 8.1 Supported Methods

| Method | Code | Type | TTL |
|--------|------|------|-----|
| GoPay | `gopay` | E-wallet | 15 min |
| ShopeePay | `shopeepay` | E-wallet | 15 min |
| QRIS | `qris` | QR | 15 min |
| BCA VA | `bank_transfer_bca` | VA | Booking TTL |
| BNI VA | `bank_transfer_bni` | VA | Booking TTL |
| Mandiri VA | `bank_transfer_mandiri` | VA | Booking TTL |
| Permata VA | `bank_transfer_permata` | VA | Booking TTL |

### 8.2 Environments

| Environment | Mode | Server Key |
|-------------|------|------------|
| Local (simulator) | `PAYMENT_PROVIDER=simulator` | N/A |
| Local (Midtrans) | `PAYMENT_PROVIDER=midtrans` | Sandbox |
| Staging | Midtrans Sandbox | `SB-Mid-server-xxx` |
| Production | Midtrans Production | `Mid-server-xxx` |

### 8.3 Webhook URL

```
POST https://api.rusuh.example/api/v1/webhooks/midtrans
```

Configure in Midtrans Dashboard → Settings → Configuration → Payment Notification URL

### 8.4 Signature Verification

```go
expected := sha512(orderId + statusCode + grossAmount + serverKey)
if received != expected {
    return ErrInvalidSignature
}
```

---

## 9. Inter-Service Communication

### 9.1 Events Published

| Event | Routing Key | Consumer | Payload |
|-------|-------------|----------|---------|
| Payment Succeeded | `payment.succeeded` | Booking | `{ bookingId, paymentId, amount, method, paidAt }` |
| Payment Failed | `payment.failed` | Booking | `{ bookingId, paymentId, reason }` |

### 9.2 Events Consumed

| Event | Producer | Purpose |
|-------|----------|---------|
| `booking.review` | Booking | Open refund case |

---

## 10. Workers

### 10.1 Outbox Relay

**Interval:** 1s

**Logic:** Publish unpublished outbox_events to RabbitMQ

### 10.2 Attempt Expiry Sweeper (optional)

**Interval:** 30s

**Logic:** Find PENDING attempts where `expires_at < now`, mark EXPIRED

---

## 11. Configuration

```bash
# Application
APP_ENV=development
APP_NAME=payment-service
APP_PORT=8083
LOG_LEVEL=info

# PostgreSQL
DATABASE_URL=postgres://svc_payment:xxx@localhost:5432/rusuh?search_path=payment

# RabbitMQ
RABBITMQ_URL=amqp://guest:guest@localhost:5672/

# Booking Service (internal)
BOOKING_SERVICE_URL=http://booking:8082

# Midtrans
PAYMENT_PROVIDER=midtrans
MIDTRANS_SERVER_KEY=SB-Mid-server-xxx
MIDTRANS_CLIENT_KEY=SB-Mid-client-xxx
MIDTRANS_ENVIRONMENT=sandbox
MIDTRANS_WEBHOOK_PATH=/api/v1/webhooks/midtrans

# Circuit breaker
MIDTRANS_CB_FAILURE_THRESHOLD=3
MIDTRANS_CB_RECOVERY_TIMEOUT=60s

# Token
TOKEN_PUBLIC_KEY_PATH=/keys/booking.pub
PAYMENT_PAGE_TOKEN_TTL=60s
```

---

## 12. Observability

### 12.1 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rbt_payment_attempts_total` | Counter | `method`, `status` | Attempts by method |
| `rbt_payment_webhooks_total` | Counter | `status`, `result` | Webhook processing |
| `rbt_payment_settlements_total` | Counter | `method` | Successful settlements |
| `rbt_payment_midtrans_latency` | Histogram | `operation` | Gateway latency |
| `rbt_payment_circuit_state` | Gauge | — | 0=closed, 1=open, 2=half-open |

### 12.2 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| PaymentCircuitOpen | state == OPEN for 2m | Critical |
| PaymentWebhookErrorRate | error_rate > 5% for 5m | Warning |
| PaymentSettlementLag | settlement_lag > 5m | Warning |
| PaymentRefundCasesOpen | open_cases > 10 | Warning |

---

## 13. Running Locally

```bash
# From repo root
make infra keys

# Option 1: Simulator mode (no Midtrans)
cd services/payment
cp .env.example .env
# Set PAYMENT_PROVIDER=simulator

# Option 2: Midtrans Sandbox
# Set MIDTRANS_SERVER_KEY and MIDTRANS_CLIENT_KEY in .env
# Start tunnel for webhook:
cloudflared tunnel --url http://localhost:8083

go run ./cmd migrate up
go run ./cmd api &
go run ./cmd worker &

curl localhost:8083/health
```

---

## 14. References

- OpenAPI: `docs/contracts/openapi/payment.yaml`
- Internal API: `docs/contracts/openapi/internal-payment.yaml`
- HLD §4.4.4: Payment and Settlement sequence
- SRS §3.2.14-§3.2.19: Payment requirements
- ADR-019: Event sourcing
- Midtrans Docs: https://docs.midtrans.com/

---

## 15. Test Case Alignment

| Feature | SRS | Test Cases | Level |
|---------|-----|------------|-------|
| Payment page | SRS-F-14-01..07 | TC-PAY-01, TC-PAY-05, TC-PAY-10 | Integration + E2E |
| Payment methods | SRS-F-14-04 | TC-PAY-01 | Integration |
| Webhook | SRS-F-15-01..03 | TC-PAY-05 | Integration |
| Webhook signature | SRS-C-21 | TC-PAY-11 | Integration |
| Settlement | SRS-F-16-01..03 | TC-PAY-03, TC-PAY-06 | Integration |
| Expiry/Resolve | SRS-F-17-01..05 | TC-PAY-04, TC-PAY-07, TC-PAY-08 | Integration |
| WebView | SRS-F-18-01..04 | TC-MOB-01, TC-MOB-02, TC-MOB-03 | E2E |
| Circuit breaker | SRS-F-19-01..03 | TC-PAY-08, TC-RES-01, TC-RES-02 | Unit + Integration |
| Cancel | SRS-F-13-01..03 | TC-PAY-09 | Integration |
| Security | SRS-Q-SEC-* | TC-SEC-01, TC-SEC-02, TC-SEC-03 | Security |

### Key Test Scenarios

| TC ID | Scenario | Expected | Validates |
|-------|----------|----------|-----------|
| TC-PAY-01 | Create attempt with GoPay | 201 with redirect URL | Happy path |
| TC-PAY-05 | Webhook settlement | payment.succeeded published | Event sourcing |
| TC-PAY-06 | Payment just before expiry | Booking marked PAID | Race condition |
| TC-PAY-08 | Gateway down during resolve | Circuit opens, retry later | Resilience |
| TC-PAY-09 | Cancel before payment | Booking cancelled, stock released | Cancel flow |
| TC-PAY-11 | Webhook with invalid/empty signature | 401 WEBHOOK_SIGNATURE_INVALID | Security |
| TC-MOB-02 | WebView payment flow | Payment completes, status synced | Mobile |
| TC-MOB-03 | WebView back button | Dialog shown, order preserved | Navigation |
| TC-SEC-01 | Replay token attack | 401 rejected | Security |
| TC-SEC-02 | Internal API from public | 403 forbidden | Network isolation |
| TC-SEC-03 | Bypass origin check | 403 rejected | Security |

### Midtrans Sandbox Matrix

| Scenario | Test | Expected |
|----------|------|----------|
| Success | GoPay simulator → success | settlement webhook |
| Pending | VA transfer | pending → settlement |
| Expired | Wait TTL | expire webhook |
| Duplicate callback | Send webhook 2x | Idempotent, single effect |
| Invalid signature | Modify signature | 401 rejected (WEBHOOK_SIGNATURE_INVALID) |
| Out-of-order | settlement before pending | Handled correctly |

---

## Appendix A: Midtrans Integration Details

### A.1 Charge Request (GoPay)

```json
{
  "payment_type": "gopay",
  "transaction_details": {
    "order_id": "RBT-{bookingId}-{attemptNum}",
    "gross_amount": 1500000
  },
  "gopay": {
    "enable_callback": true,
    "callback_url": "https://app.rusuh.example/payment/callback"
  },
  "item_details": [
    {
      "id": "category-uuid",
      "name": "VIP - Konser ABC",
      "price": 1500000,
      "quantity": 1
    }
  ],
  "customer_details": {
    "email": "buyer@example.com"
  }
}
```

### A.2 Charge Request (Bank Transfer)

```json
{
  "payment_type": "bank_transfer",
  "transaction_details": {
    "order_id": "RBT-{bookingId}-{attemptNum}",
    "gross_amount": 1500000
  },
  "bank_transfer": {
    "bank": "bca"
  },
  "custom_expiry": {
    "expiry_duration": 900,
    "unit": "second"
  }
}
```

### A.3 Webhook Notification

```json
{
  "transaction_time": "2026-10-19 10:08:00",
  "transaction_status": "settlement",
  "transaction_id": "midtrans-uuid",
  "status_message": "Success, transaction is found",
  "status_code": "200",
  "signature_key": "sha512...",
  "settlement_time": "2026-10-19 10:08:05",
  "payment_type": "gopay",
  "order_id": "RBT-booking-uuid-1",
  "gross_amount": "1500000.00",
  "fraud_status": "accept",
  "currency": "IDR"
}
```

### A.4 Transaction Status Mapping

| Midtrans Status | Attempt Status | Action |
|-----------------|----------------|--------|
| `pending` | PENDING | Wait for webhook |
| `settlement` | SETTLED | Publish payment.succeeded |
| `capture` | SETTLED | (Credit card) Publish payment.succeeded |
| `deny` | FAILED | Publish payment.failed |
| `cancel` | FAILED | User cancelled |
| `expire` | EXPIRED | TTL passed |
| `failure` | FAILED | Gateway error |

---

## Appendix B: Error Codes

| Code | HTTP | Description | User Action |
|------|------|-------------|-------------|
| `BOOKING_NOT_PAYABLE` | 409 | Booking bukan HELD/LOCKED | Check booking status |
| `BOOKING_EXPIRED` | 410 | TTL booking habis | Kembali ke antrean |
| `BOOKING_NOT_FOUND` | 404 | Booking tidak ada | Check ID |
| `ATTEMPT_IN_PROGRESS` | 409 | Sudah ada attempt pending | Selesaikan atau tunggu |
| `PAYMENT_METHOD_UNAVAILABLE` | 400 | Metode tidak tersedia | Pilih metode lain |
| `GATEWAY_ERROR` | 502 | Midtrans error | Retry atau pilih metode lain |
| `GATEWAY_TIMEOUT` | 504 | Midtrans timeout | Retry |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | Webhook signature salah atau kosong | (Internal) Log alert |
| `DUPLICATE_CALLBACK` | 200 | Webhook sudah diproses | (Internal) Ignore |
| `PAYMENT_PAGE_TOKEN_INVALID` | 403 | Token WebView salah/expired | Request token baru |
| `VISITOR_MISMATCH` | 403 | Visitor tidak cocok | Gunakan device sama |

---

## Appendix C: Circuit Breaker Config

```go
type MidtransCircuitBreaker struct {
    FailureThreshold   int           // Default: 3
    RecoveryTimeout    time.Duration // Default: 60s
    HalfOpenMaxCalls   int           // Default: 1
    
    // Metrics
    ConsecutiveFailures int
    LastFailureTime     time.Time
    State               State // CLOSED, OPEN, HALF_OPEN
}

func (cb *MidtransCircuitBreaker) Call(fn func() error) error {
    if cb.State == OPEN {
        if time.Since(cb.LastFailureTime) > cb.RecoveryTimeout {
            cb.State = HALF_OPEN
        } else {
            return ErrCircuitOpen
        }
    }
    
    err := fn()
    
    if err != nil {
        cb.ConsecutiveFailures++
        cb.LastFailureTime = time.Now()
        if cb.ConsecutiveFailures >= cb.FailureThreshold {
            cb.State = OPEN
        }
        return err
    }
    
    cb.ConsecutiveFailures = 0
    cb.State = CLOSED
    return nil
}
```
