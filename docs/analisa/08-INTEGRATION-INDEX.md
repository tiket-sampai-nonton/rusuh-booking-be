# Integration Documentation

Dokumentasi integrasi untuk Rusuh Booking Tiket.

## Documents

| # | Document | Audience | Description |
|---|----------|----------|-------------|
| 01 | [Frontend Integration](08a-frontend-integration.md) | WEB, MOB | Queue flow, polling, SSE, token handling, error codes |
| 02 | [Payment Gateway Integration](08b-payment-gateway-integration.md) | WEB, BE-2, MOB | Midtrans integration, payment flow, WebView, webhook |
| 03 | [Inter-Service Communication](08c-inter-service-communication.md) | BE-1, BE-2 | HTTP internal APIs, domain events, saga, circuit breaker |

## Quick Links

### For Frontend (WEB-1, WEB-2, MOB-1)

- **Queue Flow:** [01-frontend-integration.md §2](08a-frontend-integration.md#2-queue-flow-waiting-room)
- **SSE Integration:** [01-frontend-integration.md §3](08a-frontend-integration.md#3-sse-server-sent-events-untuk-ketersediaan)
- **Payment Page:** [02-payment-gateway-integration.md §3](08b-payment-gateway-integration.md#3-frontend-integration)
- **Mobile WebView:** [02-payment-gateway-integration.md §4](08b-payment-gateway-integration.md#4-mobile-webview-integration)

### For Backend (BE-1, BE-2)

- **Internal APIs:** [03-inter-service-communication.md §2](08c-inter-service-communication.md#2-synchronous-communication-http-internal)
- **Domain Events:** [03-inter-service-communication.md §3](08c-inter-service-communication.md#3-asynchronous-communication-domain-events)
- **Saga Patterns:** [03-inter-service-communication.md §4](08c-inter-service-communication.md#4-saga-booking-to-payment)

## Related Documentation

- **OpenAPI Contracts:** `docs/contracts/openapi/`
- **Event Schemas:** `docs/contracts/events/`
- **HLD Runtime View:** `docs/analisa/03-HLD-AD-RBT-001.md §4.4`
- **SDD Event Processing:** `docs/analisa/04-SDD-RBT-001.md §4.5`
- **ADR-003:** Waiting room via polling Status Document
- **ADR-018:** Saga choreography
- **ADR-019:** Event sourcing

## Region

All services deployed in **AWS ap-southeast-3 (Jakarta)**.
