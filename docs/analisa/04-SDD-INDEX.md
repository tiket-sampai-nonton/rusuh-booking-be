# Software Design Description — Index

Dokumentasi desain rinci per service untuk Rusuh Booking Tiket monorepo.

## Quick Links

📋 **[E2E-VERIFICATION.md](04f-E2E-VERIFICATION.md)** — Complete end-to-end flow verification matrix (PRD → SRS → HLD → SDDs → Test Cases)


📊 **[E2E-FLOW-DIAGRAMS-MERMAID.md](04g-E2E-FLOW-DIAGRAMS-MERMAID.md)** — Visual diagrams for all user journeys (Mermaid)

## Per-Service SDD Documents

| Service | Document | Owner | Port | Schema |
|---------|----------|-------|------|--------|
| Queue | [SDD-QUEUE-001](04a-SDD-QUEUE-001.md) | BE-1 | 8081 | `queue` |
| Booking | [SDD-BOOKING-001](04b-SDD-BOOKING-001.md) | BE-2 | 8082 | `booking` |
| Payment | [SDD-PAYMENT-001](04c-SDD-PAYMENT-001.md) | BE-2 | 8083 | `payment` |
| Notification | [SDD-NOTIFICATION-001](04d-SDD-NOTIFICATION-001.md) | BE-1 | 8084 | `notification` |
| User | [SDD-USER-001](04e-SDD-USER-001.md) | BE-1 | 8085 | `users` |

## Architecture Overview

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

                                     ┌─────────────────┐
                                     │  User Service   │ (standalone, token-based auth)
                                     │     :8085       │
                                     └─────────────────┘
```

## Inter-Service Communication Summary

### Synchronous (HTTP Internal)

| From | To | Endpoint | Purpose |
|------|----|---------|---------| 
| Queue | Booking | `POST /internal/v1/allocations:batch` | Reserve allocation |
| Queue | Booking | `GET /internal/v1/events/{id}/supply` | Stock snapshot |
| Booking | Payment | `POST /internal/v1/payments/{id}/resolve` | Check/cancel at gateway |
| Payment | Booking | `GET /internal/v1/bookings/{id}/payable` | Get amount, deadline |
| Payment | Booking | `POST /internal/v1/bookings/{id}/payment-lock` | Lock booking |
| Payment | Booking | `POST /internal/v1/bookings/{id}/deadline-extension` | Extend TTL |

### Asynchronous (Domain Events)

| Event | Producer | Consumer(s) |
|-------|----------|-------------|
| `booking.journey.ended` | Booking | Queue |
| `booking.quota.exhausted` | Booking | Queue |
| `booking.sold_out` | Booking | Queue |
| `booking.paid` | Booking | Notification |
| `booking.expired` | Booking | Notification |
| `booking.review` | Booking | Notification, Payment |
| `payment.succeeded` | Payment | Booking |
| `payment.failed` | Payment | Booking |
| `queue.turn.soon` | Queue | Notification |
| `queue.turn.arrived` | Queue | Notification |

## Shared Patterns

### Token Flow

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              TOKEN LIFECYCLE                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  1. VISITOR TOKEN (device identity)                                             │
│     Created: First visit                                                        │
│     Storage: Cookie rb_visitor or header X-Visitor-Token                        │
│     TTL: 24 hours                                                               │
│     Used by: All services                                                       │
│                                                                                 │
│  2. QUEUE TOKEN (queue entry ownership)                                         │
│     Created: POST /queue-entries (join)                                         │
│     Storage: Client state/memory                                                │
│     TTL: Until journey ends                                                     │
│     Used by: Queue Service (claim, leave)                                       │
│                                                                                 │
│  3. ADMISSION TOKEN (one-time booking session entry)                            │
│     Created: POST /queue-entries/{id}/claim                                     │
│     Storage: Client state (immediate use)                                       │
│     TTL: 60 seconds, single use                                                 │
│     Used by: Booking Service (create session)                                   │
│                                                                                 │
│  4. USER TOKENS (optional login)                                                │
│     Access Token: JWT, 15 min TTL, header Authorization                         │
│     Refresh Token: 30 days, HttpOnly cookie rb_refresh                          │
│     Created: POST /auth/otp-verify                                              │
│     Used by: Queue, Booking (link to account)                                   │
│                                                                                 │
│  5. PAYMENT PAGE TOKEN (mobile WebView)                                         │
│     Created: POST /payments/{id}/page-tokens                                    │
│     Storage: URL param, exchanged for rb_pay cookie                             │
│     TTL: 60 seconds, single use                                                 │
│     Used by: Payment Service                                                    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Data Storage

| Store | Purpose | Used By |
|-------|---------|---------|
| PostgreSQL | Source of truth, transactions | All services |
| Valkey | Queue state, cache, rate limit, OTP | Queue, Booking, User |
| RabbitMQ | Domain events, retry, DLQ | All except User |

### Event Sourcing

- **Booking Service:** `booking.booking_events` — siklus booking
- **Payment Service:** `payment.payment_events` — siklus payment attempt

### Outbox Pattern

- **Booking Service:** `booking.outbox_events`
- **Payment Service:** `payment.outbox_events`

### Idempotency

- **Booking Service:** `booking.idempotency_records`
- **Payment Service:** `payment.idempotency_records`

## Region

All services deployed in **AWS ap-southeast-3 (Jakarta)**.

## Related Documentation

- **Monolithic SDD:** [04-SDD-RBT-001.md](04-SDD-RBT-001.md) — legacy full SDD
- **HLD:** `docs/analisa/03-HLD-AD-RBT-001.md`
- **Integration Guide:** `docs/analisa/`
- **OpenAPI Contracts:** `docs/contracts/openapi/`
- **Event Schemas:** `docs/contracts/events/`

## Using as Kiro Specs

These SDD documents are designed to be used as Kiro specs for AI-assisted development:

```bash
# Reference a service SDD in Kiro
kiro chat --spec docs/analisa/04b-SDD-BOOKING-001.md "implement the hold endpoint"
```

Each SDD contains:
- API endpoints with request/response examples
- Data model with SQL DDL
- State machines
- Configuration environment variables
- Inter-service communication contracts
