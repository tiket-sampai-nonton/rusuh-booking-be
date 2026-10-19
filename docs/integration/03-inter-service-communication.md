# Inter-Service Communication Guide — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | INT-RBT-003 |
| **Version** | 1.0 |
| **Date** | 2026-10-19 |
| **Status** | Draft |
| **Author(s)** | Tim PT. Tiket Sampai Nonton Sentosa |
| **Audience** | BE-1, BE-2 |
| **Source** | HLD v1.4, SDD v1.3, ADR-018, ADR-019 |

> Dokumen ini menjelaskan komunikasi antar service: synchronous (HTTP internal) dan asynchronous (RabbitMQ domain events).

---

## 1. Service Topology

```
                                    ┌─────────────────────┐
                                    │   RabbitMQ (MQ)     │
                                    │   Domain Events     │
                                    └──────────┬──────────┘
                                               │
        ┌──────────────────────────────────────┼──────────────────────────────────────┐
        │                                      │                                      │
        ▼                                      ▼                                      ▼
┌───────────────┐                    ┌─────────────────┐                    ┌─────────────────┐
│ Queue Service │◀──── HTTP ────────▶│ Booking Service │◀──── HTTP ────────▶│ Payment Service │
│    :8081      │                    │     :8082       │                    │     :8083       │
└───────┬───────┘                    └────────┬────────┘                    └────────┬────────┘
        │                                     │                                      │
        │                                     ▼                                      │
        │                            ┌─────────────────┐                             │
        └───────────────────────────▶│  Notification   │◀────────────────────────────┘
                                     │    Service      │
                                     │     :8084       │
                                     └─────────────────┘
```

**Region:** `ap-southeast-3` (Jakarta)

---

## 2. Synchronous Communication (HTTP Internal)

### 2.1 Overview

Internal APIs (`/internal/v1/*`) hanya dapat diakses di jaringan privat. Tidak dirutekan oleh ALB publik.

**Authentication:** Service token (JWT) di header `Authorization: Bearer <serviceToken>`

**Timeout:** 5 detik (default), 10 detik untuk batch operations

### 2.2 Queue → Booking

| Endpoint | Method | Purpose | Idempotency |
|----------|--------|---------|-------------|
| `/internal/v1/allocations:batch` | POST | Reservasi alokasi untuk N entry terdepan | Per `queueEntryId` |
| `/internal/v1/events/{id}/supply` | GET | Snapshot stok (cached 1s) | N/A (read) |

#### POST /internal/v1/allocations:batch

**Request:**
```json
{
  "eventId": "event-uuid",
  "entries": [
    {
      "queueEntryId": "entry-uuid-1",
      "visitorId": "visitor-uuid-1",
      "buyerNikHash": "sha256-hash",
      "units": 3
    },
    {
      "queueEntryId": "entry-uuid-2",
      "visitorId": "visitor-uuid-2",
      "buyerNikHash": "sha256-hash",
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
      "queueEntryId": "entry-uuid-1",
      "allocationId": "alloc-uuid-1",
      "status": "RESERVED",
      "units": 3,
      "claimDeadline": "2026-10-19T10:03:00Z"
    },
    {
      "queueEntryId": "entry-uuid-2",
      "allocationId": null,
      "status": "NO_SUPPLY_NOW",
      "units": 0,
      "claimDeadline": null
    }
  ]
}
```

**Status values:**
| Status | Meaning | Queue Action |
|--------|---------|--------------|
| `RESERVED` | Alokasi berhasil | Entry → ADMITTED |
| `NO_SUPPLY_NOW` | Stok sementara habis | Entry tetap WAITING |
| `SOLD_OUT` | Stok habis permanen | Entry → REJECTED, tutup antrean |
| `QUOTA_EXHAUSTED` | Kuota NIK habis | Entry → REJECTED |

#### GET /internal/v1/events/{id}/supply

**Response:**
```json
{
  "eventId": "event-uuid",
  "totalCapacity": 1000,
  "allocatedUnits": 200,
  "heldUnits": 50,
  "soldUnits": 150,
  "freeUnits": 600,
  "saleStatus": "OPEN"
}
```

### 2.3 Booking → Payment

| Endpoint | Method | Purpose | Idempotency |
|----------|--------|---------|-------------|
| `/internal/v1/payments/{bookingId}/resolve` | POST | CHECK atau CHECK_AND_CANCEL | Yes |

#### POST /internal/v1/payments/{bookingId}/resolve

Called by Booking during expiry to check if payment was made at gateway.

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
  "bookingId": "booking-uuid",
  "resolution": "NO_PAYMENT",
  "cancelledAtGateway": true
}
```

**Resolution values:**
| Resolution | Meaning | Booking Action |
|------------|---------|----------------|
| `NO_PAYMENT` | Tidak ada pembayaran | Expire booking |
| `PAYMENT_FOUND` | Ada pembayaran (race condition) | Tunggu webhook, jangan expire |
| `REFUND_REQUIRED` | Pembayaran ditemukan setelah cancel | Buat refund case |

### 2.4 Payment → Booking

| Endpoint | Method | Purpose | Idempotency |
|----------|--------|---------|-------------|
| `/internal/v1/bookings/{id}/payable` | GET | Amount, deadline, status | N/A (read) |
| `/internal/v1/bookings/{id}/payment-lock` | POST | Kunci pesanan saat "Bayar" | Yes |
| `/internal/v1/bookings/{id}/deadline-extension` | POST | Perpanjang saat gateway gangguan | Yes |

#### GET /internal/v1/bookings/{id}/payable

**Response:**
```json
{
  "bookingId": "booking-uuid",
  "amount": 3000000,
  "currency": "IDR",
  "expiresAt": "2026-10-19T10:15:00Z",
  "status": "HELD",
  "visitorId": "visitor-uuid",
  "version": 1
}
```

#### POST /internal/v1/bookings/{id}/payment-lock

**Request:**
```json
{
  "expectedVersion": 1
}
```

**Response:**
```json
{
  "bookingId": "booking-uuid",
  "version": 2,
  "amount": 3000000,
  "lockedAt": "2026-10-19T10:05:00Z"
}
```

#### POST /internal/v1/bookings/{id}/deadline-extension

**Request:**
```json
{
  "reason": "GATEWAY_DEGRADED",
  "extensionMinutes": 5
}
```

**Response:**
```json
{
  "bookingId": "booking-uuid",
  "newExpiresAt": "2026-10-19T10:20:00Z",
  "extended": true
}
```

---

## 3. Asynchronous Communication (Domain Events)

### 3.1 Overview

- **Broker:** RabbitMQ
- **Pattern:** Transactional outbox (event ditulis ke `outbox_events` dalam transaksi yang sama)
- **Delivery:** At-least-once
- **Consumer:** Idempoten (tanpa inbox table, cek efek di database)

### 3.2 Event Envelope

```json
{
  "messageId": "msg-uuid",
  "eventType": "booking.paid",
  "schemaVersion": "v1",
  "occurredAt": "2026-10-19T10:08:00Z",
  "correlationId": "corr-uuid",
  "producer": "booking-service",
  "payload": { ... }
}
```

### 3.3 Domain Events Catalog

#### Booking Service Events

| Event | Consumer(s) | Purpose | Payload |
|-------|-------------|---------|---------|
| `booking.journey.ended` | Queue | Bebaskan slot antrean | `{ bookingId, eventId, visitorId, outcome }` |
| `booking.quota.exhausted` | Queue | Blokir join ulang NIK | `{ eventId, buyerNikHash }` |
| `booking.sold_out` | Queue | Tutup waiting room | `{ eventId }` |
| `booking.paid` | Notification | Email e-tiket | `{ bookingId, email, tickets[] }` |
| `booking.expired` | Notification | Email pembayaran expired | `{ bookingId, email }` |
| `booking.review` | Notification, Payment | Email terlambat, buka refund case | `{ bookingId, email, paymentId }` |

#### Payment Service Events

| Event | Consumer(s) | Purpose | Payload |
|-------|-------------|---------|---------|
| `payment.succeeded` | Booking | Apply settlement | `{ bookingId, paymentId, amount, method, paidAt }` |
| `payment.failed` | Booking | Informasi (booking tetap menunggu) | `{ bookingId, paymentId, reason }` |

#### Queue Service Events

| Event | Consumer(s) | Purpose | Payload |
|-------|-------------|---------|---------|
| `queue.turn.soon` | Notification | Push "giliran sebentar lagi" | `{ entryId, eventId, pushToken, position }` |
| `queue.turn.arrived` | Notification | Push "giliran tiba" | `{ entryId, eventId, pushToken, claimDeadline }` |

### 3.4 Event Payloads

#### booking.journey.ended

```json
{
  "bookingId": "booking-uuid",
  "eventId": "event-uuid",
  "visitorId": "visitor-uuid",
  "queueEntryId": "entry-uuid",
  "outcome": "PAID",
  "occurredAt": "2026-10-19T10:08:00Z"
}
```

**Outcome values:**
| Outcome | Meaning |
|---------|---------|
| `PAID` | Pembayaran berhasil |
| `EXPIRED` | TTL habis tanpa bayar |
| `CANCELLED` | Dibatalkan user |
| `SESSION_EXPIRED` | TTL selection habis |
| `UNCLAIMED` | Giliran tidak di-claim |
| `LEFT_WITHOUT_BUYING` | Leave tanpa booking |

#### booking.paid

```json
{
  "bookingId": "booking-uuid",
  "eventId": "event-uuid",
  "buyerEmail": "encrypted-email",
  "tickets": [
    {
      "ticketId": "ticket-uuid",
      "publicCode": "RBT-ABC123",
      "categoryName": "VIP",
      "attendeeNikMasked": "320101****0001"
    }
  ],
  "totalAmount": 3000000,
  "paidAt": "2026-10-19T10:08:00Z"
}
```

#### payment.succeeded

```json
{
  "bookingId": "booking-uuid",
  "paymentId": "payment-uuid",
  "attemptId": "attempt-uuid",
  "amount": 3000000,
  "method": "gopay",
  "transactionId": "midtrans-txn-id",
  "paidAt": "2026-10-19T10:08:00Z"
}
```

### 3.5 Queue Topology

```
Exchange: rbt.events (topic)
  │
  ├── booking.* ──────────────────┬─▶ queue.booking.events (Queue Service)
  │                               └─▶ notification.booking.events (Notification)
  │
  ├── payment.* ──────────────────┬─▶ booking.payment.events (Booking Service)
  │                               └─▶ notification.payment.events (Notification)
  │
  └── queue.* ────────────────────┬─▶ notification.queue.events (Notification)
```

**Retry queues:**
- `<queue>.retry.5s` — retry setelah 5 detik
- `<queue>.dlq` — dead letter queue untuk investigasi

### 3.6 Retry and DLQ Policy

```
┌─────────┐     fail      ┌──────────────┐     fail      ┌─────────┐
│  Queue  │──────────────▶│ Retry Queue  │──────────────▶│   DLQ   │
│         │               │   (5s TTL)   │               │         │
└─────────┘               └──────────────┘               └─────────┘
     ▲                           │
     │         success           │
     └───────────────────────────┘
```

**Retry header:** `x-rbt-attempt` (1, 2, ...)

**DLQ conditions:**
- Attempt ke-3 gagal
- Error permanen (validation, not found)
- Poison message (parse error)

---

## 4. Saga: Booking to Payment

### 4.1 Happy Path

```
┌─────────┐   hold    ┌─────────┐   lock    ┌─────────┐  charge   ┌─────────┐
│ Booking │─────────▶ │ Booking │─────────▶ │ Payment │─────────▶ │ Midtrans│
│ Session │          │  HELD   │          │ Attempt │          │ Gateway │
└─────────┘          └─────────┘          └─────────┘          └─────────┘
                                                                     │
┌─────────┐  ticket   ┌─────────┐  settle   ┌─────────┐  webhook   │
│  Email  │◀───────── │ Booking │◀───────── │ Payment │◀───────────┘
│         │          │  PAID   │          │ SUCCESS │
└─────────┘          └─────────┘          └─────────┘
```

### 4.2 Compensation: Expiry

```
┌─────────┐   hold    ┌─────────┐   TTL     ┌─────────┐  resolve  ┌─────────┐
│ Booking │─────────▶ │ Booking │─────────▶ │ Booking │─────────▶ │ Payment │
│ Session │          │  HELD   │  expires  │ Expiry  │          │ CHECK   │
└─────────┘          └─────────┘          └─────────┘          └─────────┘
                                                │                     │
                                                ▼                     ▼
                                          ┌─────────┐          ┌─────────┐
                                          │ Booking │          │ Cancel  │
                                          │ EXPIRED │          │ Gateway │
                                          └─────────┘          └─────────┘
                                                │
                                                ▼
                                          ┌─────────┐
                                          │ Release │
                                          │  Slot   │
                                          └─────────┘
```

### 4.3 Compensation: Late Payment (Review)

```
Booking expired ──▶ Payment ditemukan di gateway ──▶ booking.review event
                                                            │
                    ┌───────────────────────────────────────┤
                    ▼                                       ▼
              ┌─────────┐                            ┌─────────┐
              │ Refund  │                            │  Email  │
              │  Case   │                            │ Review  │
              └─────────┘                            └─────────┘
```

---

## 5. Circuit Breaker

### 5.1 Configuration

| Service Call | Failure Threshold | Recovery Timeout |
|--------------|-------------------|------------------|
| Queue → Booking | 5 failures / 10s | 30s |
| Booking → Payment | 5 failures / 10s | 30s |
| Payment → Midtrans | 3 failures / 10s | 60s |

### 5.2 States

```
┌────────┐   failures >= threshold   ┌────────┐
│ CLOSED │──────────────────────────▶│  OPEN  │
└────────┘                           └────────┘
     ▲                                    │
     │                              timeout
     │                                    │
     │         success                    ▼
     └────────────────────────────┌───────────┐
                                  │ HALF_OPEN │
                                  └───────────┘
```

### 5.3 Fallback Behavior

| Circuit | When Open | Fallback |
|---------|-----------|----------|
| Queue → Booking | Allocations fail | Entry stays WAITING, retry later |
| Booking → Payment | Resolve fail | Keep booking, manual reconciliation |
| Payment → Midtrans | Charge fail | Return error to user, allow retry |

---

## 6. Idempotency

### 6.1 Synchronous Calls

| Endpoint | Idempotency Key | Scope |
|----------|-----------------|-------|
| POST allocations:batch | `queueEntryId` | Per entry in batch |
| POST payment-lock | `bookingId + expectedVersion` | Per booking |
| POST resolve | `bookingId` | Per booking |
| POST deadline-extension | `bookingId` | Per booking (once) |

### 6.2 Event Processing

| Event | Idempotency Check |
|-------|-------------------|
| `payment.succeeded` | `booking.status != PAID` |
| `booking.journey.ended` | `queue_entry.status == ADMITTED` |
| `booking.paid` | `notification.deliveries` fingerprint |

---

## 7. Observability

### 7.1 Correlation ID

Setiap request chain memiliki `X-Correlation-ID` yang diteruskan:

```
Frontend → Queue → Booking → Payment → Midtrans
    └───────────────────────────────────────────▶ same correlation ID
```

### 7.2 Metrics

| Metric | Labels | Purpose |
|--------|--------|---------|
| `rbt_internal_request_duration_seconds` | `service`, `endpoint`, `status` | Latency monitoring |
| `rbt_event_published_total` | `event_type`, `producer` | Event throughput |
| `rbt_event_consumed_total` | `event_type`, `consumer`, `result` | Consumer health |
| `rbt_circuit_breaker_state` | `circuit` | Circuit breaker status |
| `rbt_outbox_lag_seconds` | `service` | Outbox publishing delay |

### 7.3 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| OutboxLagHigh | lag > 30s for 5m | Warning |
| CircuitBreakerOpen | state == OPEN for 2m | Critical |
| DLQNotEmpty | dlq_size > 0 | Warning |
| EventProcessingFailed | error_rate > 5% for 5m | Critical |

---

## 8. Local Development

### 8.1 Service Discovery

Di Docker Compose, gunakan service name:

```yaml
# booking service .env
QUEUE_SERVICE_URL=http://queue:8081
PAYMENT_SERVICE_URL=http://payment:8083

# queue service .env
BOOKING_SERVICE_URL=http://booking:8082
```

### 8.2 RabbitMQ Management

- URL: http://localhost:15672
- User: `guest` / `guest`
- Queues: lihat topology di §3.5

### 8.3 Testing Events

```bash
# Publish test event
rabbitmqadmin publish exchange=rbt.events routing_key=booking.paid \
  payload='{"messageId":"test","eventType":"booking.paid",...}'

# Check DLQ
rabbitmqadmin get queue=notification.booking.events.dlq count=10
```

---

## 9. Contract Files

| Service | OpenAPI | Events Schema |
|---------|---------|---------------|
| Queue | `docs/contracts/openapi/queue.yaml` | `docs/contracts/events/queue.*.json` |
| Booking | `docs/contracts/openapi/booking.yaml` | `docs/contracts/events/booking.*.json` |
| Booking (internal) | `docs/contracts/openapi/internal-booking.yaml` | — |
| Payment | `docs/contracts/openapi/payment.yaml` | `docs/contracts/events/payment.*.json` |
| Payment (internal) | `docs/contracts/openapi/internal-payment.yaml` | — |
| Notification | `docs/contracts/openapi/notification.yaml` | — |

---

## References

- HLD §4.2: Container View (service responsibilities)
- HLD §4.2.2: Internal API
- HLD §4.2.3: Domain Events
- SDD §4.5: Event processing and idempotency
- ADR-018: Saga choreography
- ADR-019: Event sourcing
