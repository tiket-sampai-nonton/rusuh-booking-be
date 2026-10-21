# Tasks — Payment Service

| Field | Value |
|-------|-------|
| Service | Payment Service |
| Port | 8083 · Schema/Role `payment`/`svc_payment` |
| Module | `github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment` |
| Spec | `specs/payment-service/{requirements,design}.md` · LLD `docs/sdd/SDD-PAYMENT-001.md` |
| Arch | Hexagonal + Event Sourcing (ADR-019) |
| Status | Boilerplate (health slice) + DB schema (0001, 0002) **IMPL**; all business logic **TARGET** |

## Workflow

```
Read Spec → Implement (TDD) → Test → (loop if error) → Verify ✓
```

Conventions (from `.kiro/steering/{structure,code-principles}.md`, enforced by `.golangci.yml` depguard):
- One usecase per file in `application/usecase/`; **accept interfaces, return structs**.
- Driving ports `port/inbound/I*.go`, driven ports `port/outbound/I*.go`; interfaces prefixed `I`, only in `port/`.
- Each port file carries a `//go:generate go tool mockgen -source=… -destination=../../../../_mock/payment/port/{inbound,outbound}/…_mock.go -package=mock_{inbound,outbound}` line.
- Handlers return DTOs via `domain/mapper` — **never** return entities.
- All success responses wrap `{meta,data}`; errors use `apperror.Envelope` (`{meta:{code,message,requestId,errorCode,details},data:null}`).
- Transactions go through `IUnitOfWork`; event append + read-model projection + outbox write happen in **one** tx.
- Coverage threshold 80% on logic (`cmd/`, `internal/delivery/`, `internal/config/`, `_mock/` excluded).

---

## What ALREADY exists (do not rebuild)

| Area | File(s) | Note |
|------|---------|------|
| Cobra verbs | `services/payment/cmd/{main,root,api,worker,migrate,healthcheck}.go` | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` |
| Composition root | `internal/delivery/{server.go,worker.go,clients.go,serve.go}` | `RunServer`, `RunWorker`, `NewHandler(cfg,clients,logger)`, `NewClients(ctx,cfg,Need{Postgres,RabbitMQ})`, `jobs(*Clients) []Job`, `loop(...)` |
| Config (subset) | `internal/config/{config.go,load.go}` | `APP_*`,`LOG_*`,`HTTP_*`,`HEALTH_TIMEOUT`,`WORKER_INTERVAL`,`POSTGRES_*`,`RABBITMQ_*` + `Config.Validate()` fail-fast |
| Health slice | `internal/payment/{domain/entity/health.go,domain/dto/health.go,domain/mapper/health_mapper.go,domain/valueobject/dependency_name.go,application/usecase/check_health.go,infrastructure/http/health_handler.go,infrastructure/healthcheck/checker.go,port/inbound/health_usecase.go,port/outbound/dependency_checker.go}` | Mirror this slice's structure for business slices |
| Migration 0001 | `deployments/migrations/0001_init.up.sql` | `payment_attempts`, `payment_callbacks`, `payment_page_tokens`, `refund_cases`, `idempotency_records`, `outbox_events` + all indexes/constraints |
| Migration 0002 | `deployments/migrations/0002_event_store.up.sql` | `payment_events` + append-only triggers + `payment_attempts.stream_version` |
| Circuit breaker | `pkg/resilience/breaker.go` | `Breaker`, `Bulkhead` — **TC-RES-01/02 PASS** |
| Event store kit | `pkg/eventstore/eventstore.go` | `Append`/`Load`/`Replay`, optimistic concurrency, forbidden-field guard |
| Error envelope | `pkg/apperror/apperror.go` | `apperror.New(code,httpStatus,msg)`, `.ToEnvelope(requestID)` |
| MQ publisher/consumer | `pkg/rabbitmq/{publisher,consumer,envelope,retry}.go` | `Publisher.Publish`, `Connection.Consume`, `NewEnvelope`, `Permanent` |

> **Migrations are applied and MUST NOT be edited** (`structure.md`). Build adapters to fit the schema exactly.

---

## Shared library APIs (use exactly as written)

### `pkg/resilience.Breaker`
```go
// Construct ONCE per gateway (in delivery composition root), reuse across calls.
cfg := resilience.BreakerConfig{
    Name:             "payment.midtrans",
    FailureThreshold: cfg.Midtrans.CBFailureThreshold, // default 3
    Cooldown:         cfg.Midtrans.CBRecoveryTimeout,   // default 60s
    HalfOpenProbes:   cfg.Midtrans.CBHalfOpenProbes,    // default 1
    Timeout:          cfg.Midtrans.Timeout,             // per-call ctx timeout; 0 = none
    OnStateChange: func(name string, from, to resilience.State) { /* metric rbt_payment_circuit_state */ },
    // IsFailure optional; default = any err except context.Canceled
}
br, err := resilience.NewBreaker(cfg, nil) // now=nil → time.Now

// Wrap EVERY synchronous gateway call:
err := br.Do(ctx, func(ctx context.Context) error {
    resp, cerrr := client.Charge(ctx, req) // ctx already carries Timeout
    out = resp
    return cerr
})
// Fast-reject while OPEN / half-open probe busy → errors.Is(err, resilience.ErrOpen) == true
// br.State() → resilience.Closed|Open|HalfOpen (OPEN auto-advances to HalfOpen after Cooldown)
```
Mapping to HTTP: `errors.Is(err, resilience.ErrOpen)` on **charge/cancel** → 503 `PAYMENT_PROVIDER_UNAVAILABLE`; on **resolve** → `outcome=UNKNOWN`.
Optional bulkhead: `bh, _ := resilience.NewBulkhead(max, wait); bh.Do(ctx, fn)` → `resilience.ErrBulkheadFull` when full.

### `pkg/eventstore`
```go
store, _ := eventstore.New("payment_events")                 // table must end in _events
newVer, err := store.Append(ctx, tx, streamID, expectedVer,  // tx satisfies eventstore.DB (pgx.Tx)
    eventstore.NewEvent{Type: "AttemptSettled", SchemaVersion: 1, Payload: payloadStruct, Metadata: map[string]string{"correlationId": cid}})
// 23505 on (stream_id,version) → errors.Is(err, eventstore.ErrConcurrency) → reload stream & retry
// Type MUST be PascalCase ^[A-Z][A-Za-z]+$
// Payload MUST NOT contain forbidden keys (nik/email/token/serverkey/cardnumber/password/otp/...) → eventstore.ErrForbiddenField
events, _ := store.Load(ctx, db, streamID)                   // replay for current version
```

### `pkg/rabbitmq` (publisher confirm + consumer)
```go
// Publish (outbox relay):
pub, _ := clients.MQ.NewPublisher(); defer pub.Close()
env, _ := rabbitmq.NewEnvelope("payment.succeeded", config.ServiceName, aggregateID, correlationID, payload, occurredAt)
err := pub.Publish(ctx, "payment.succeeded", env)            // waits for broker confirm; err → leave unpublished, retry

// Consume (booking.review):
sub := rabbitmq.Subscription{
    Queue:       "payment.booking-review",
    RoutingKeys: []string{"booking.review.v1"},
    Handler: func(ctx context.Context, d rabbitmq.Delivery) error {
        // idempotent; return rabbitmq.Permanent(err) for unparseable payload → DLQ
    },
}
go clients.MQ.Consume(ctx, sub, logger)                       // cfg.Workers goroutines, auto retry/DLQ
```

### `pkg/apperror`
```go
var ErrBookingNotPayable = apperror.New("BOOKING_NOT_PAYABLE", http.StatusConflict, "booking tidak dapat dibayar")
// in handler: ae := apperror.From(err); httpx.WriteJSON(w, ae.HTTPStatus, ae.ToEnvelope(requestID))
```

---

## Error code catalog (map in `infrastructure/http`)

| errorCode | HTTP | Endpoint | Trigger | AC |
|-----------|------|----------|---------|----|
| `BOOKING_NOT_PAYABLE` | 409 | page/attempt | booking not HELD/LOCKED | AC1.2, AC3.x |
| `BOOKING_EXPIRED` | 410 | page/attempt | booking TTL elapsed | AC1.3 |
| `VISITOR_MISMATCH` | 403 | page/attempt/status | visitor token ≠ booking | AC1.4, AC11.x |
| `PAYMENT_PAGE_TOKEN_INVALID` | 403 | page-sessions | token wrong/expired/used | AC2.3, AC11.2 |
| `IDEMPOTENCY_KEY_REUSED` | 409 | attempt | same key, different request_hash | AC3.3 |
| `ATTEMPT_IN_PROGRESS` | 409 | attempt | open attempt exists (`uq_attempt_open_per_booking`) | AC3.2 |
| `PAYMENT_METHOD_UNAVAILABLE` | 422 | attempt | method not active/supported | AC3.4 |
| `PAYMENT_PROVIDER_UNAVAILABLE` | 503 | attempt/cancel | gateway error/timeout OR breaker OPEN (`resilience.ErrOpen`) | AC3.5, AC3.6 |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | webhook | signature wrong/empty | AC4.1 |
| `NOT_FOUND` | 404 | simulator | simulator disabled in this env | AC12.3 |

---

## Pre-requisites

- [ ] `cd services/payment && go build ./...` compiles
- [ ] `make infra keys` (Postgres + RabbitMQ + EdDSA keypair)
- [ ] `cp .env.example .env` and set `PAYMENT_PROVIDER=simulator`
- [ ] `go run ./cmd migrate up` → `curl -fsS localhost:8083/health`
- [ ] `go test ./pkg/resilience/... ./pkg/eventstore/...` green (shared libs)

---

## Task 0: Config + Shared Plumbing (R12, prerequisite for all)

**Traces to:** R12.2, Design §10. **Status:** config subset IMPL; Midtrans vars TARGET.

### Acceptance Criteria
- [ ] AC0.1: `internal/config/config.go` adds `Midtrans`, `Booking`, `PageToken` structs; `load.go` reads env; `.env.example` documents them.
- [ ] AC0.2: `Config.Validate()` fails fast when `PAYMENT_PROVIDER=midtrans` and `MIDTRANS_SERVER_KEY`/`MIDTRANS_BASE_URL` empty; CB thresholds > 0.
- [ ] AC0.3: `PAYMENT_PROVIDER ∈ {simulator, midtrans}`; unknown → validation error.

### Files & signatures
- `internal/config/config.go` — extend `type Config`:
  ```go
  type Midtrans struct {
      Provider           string // simulator | midtrans
      ServerKey          string // secret — never logged (Redacted())
      ClientKey          string
      Environment        string // sandbox | production
      BaseURL            string
      WebhookPath        string // /api/v1/webhooks/midtrans
      Timeout            time.Duration
      CBFailureThreshold int    // 3
      CBRecoveryTimeout  time.Duration // 60s
      CBHalfOpenProbes   int    // 1
  }
  type Booking struct { ServiceURL string; ServiceToken string; TokenPublicKeyPath string }
  type PageToken struct { TTL time.Duration } // PAYMENT_PAGE_TOKEN_TTL default 60s
  ```
- `internal/config/load.go` — read `PAYMENT_PROVIDER`, `MIDTRANS_*`, `BOOKING_SERVICE_URL`, `BOOKING_SERVICE_TOKEN`, `TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL`, `MIDTRANS_CB_*`, `MIDTRANS_TIMEOUT`.
- `internal/delivery/clients.go` — add `Breaker *resilience.Breaker`, `EventStore *eventstore.Store`, built in `NewClients`; extend `Need` if needed (reuse `Postgres`/`RabbitMQ`).

### Verify
```bash
go test ./internal/config/... && go build ./...
```

---

## Task 1: Payment Page (R1)

**Traces to:** R1, SRS-F-14, TC-PAY-01, TC-PAY-02, TC-PAY-10

### Acceptance Criteria
- [ ] AC1.1: `GET /api/v1/payments/{bookingId}/page` + valid visitor (`rb_visitor` cookie | `X-Visitor-Token` header | `rb_pay` cookie) → 200 `PaymentPage` `{bookingId, methods[], amountMinor, paymentExpiresAt, serverTime, canEdit, editableUntil, currentAttempt}`.
- [ ] AC1.2: booking not HELD/LOCKED → 409 `BOOKING_NOT_PAYABLE`.
- [ ] AC1.3: booking TTL elapsed → 410 `BOOKING_EXPIRED`.
- [ ] AC1.4: visitor ≠ booking → 403 `VISITOR_MISMATCH`.
- [ ] AC1.5: no NIK plaintext in response (I-7).

### Files & signatures (**TARGET**)
- `domain/dto/payment_page.go` — `type PaymentPageResponse struct {...}` (mirror OpenAPI `PaymentPage`).
- `domain/valueobject/method.go` — `type Method string`; consts `VABCA,VABNI,VABRI,VAPermata,QRIS,GoPay`; `Methods()` + `ParseMethod(string)`.
- `port/outbound/booking_client.go` — `type IBookingClient interface { GetPayable(ctx, bookingID, visitorID string) (entity.PayableBooking, error); PaymentLock(ctx, bookingID, visitorID string) (bookingVersion int, err error); Cancel/ReleaseStock(...)}` (+ `//go:generate mockgen`).
- `infrastructure/bookingclient/client.go` — `type Client struct{ base string; token string; http *http.Client }`; `func New(cfg config.Booking) *Client`; implements `IBookingClient` against Booking `/internal/v1/bookings/{id}/payment-lock` using `serviceBearer`.
- `port/inbound/get_payment_page_usecase.go` — `type IGetPaymentPage interface { GetPaymentPage(ctx, bookingID, visitorID string) (dto.PaymentPageResponse, error) }`.
- `application/usecase/get_payment_page.go` — `type GetPaymentPage struct{...}`; `func NewGetPaymentPage(booking outbound.IBookingClient, attempts outbound.IAttemptReadModel, clk clock.IClock) *GetPaymentPage`.
- `infrastructure/http/payment_handler.go` — `type PaymentHandler struct{...}`; `func (h *PaymentHandler) Register(mux *http.ServeMux)` → `GET /api/v1/payments/{bookingId}/page`; extract visitor via helper `visitorFrom(r)`.
- Register handler in `delivery/server.go` `NewHandler`.

### Tests
- [ ] Unit: all 4 error branches + success mapping; assert no NIK in DTO.

### Verify
```bash
curl -H "X-Visitor-Token: $TOKEN" localhost:8083/api/v1/payments/$BID/page
```

---

## Task 2: Page Token for Mobile WebView (R2)

**Traces to:** R2, SRS-F-18, TC-MOB-01, TC-SEC-01. **Status:** table IMPL; usecase TARGET.

### Acceptance Criteria
- [ ] AC2.1: `POST /api/v1/payments/{bookingId}/page-tokens` + valid visitor → 201 `{pageToken, pageUrl, expiresAt}`; store **only** `token_hash` (BYTEA) + `visitor_id`, TTL `PAYMENT_PAGE_TOKEN_TTL` (60s).
- [ ] AC2.2: `POST /api/v1/payment-page-sessions` + valid/unused/unexpired token → 204, set `rb_pay` cookie (HttpOnly, Secure, SameSite=Lax), mark `used_at`.
- [ ] AC2.3: used/expired/unknown token → 403 `PAYMENT_PAGE_TOKEN_INVALID`.

### Files & signatures (**TARGET**)
- `domain/entity/page_token.go` — `type PageToken struct { Hash []byte; BookingID, VisitorID string; ExpiresAt, UsedAt time.Time }`.
- `port/outbound/page_token_store.go` — `IPageTokenStore interface { Save(ctx, PageToken) error; ConsumeByHash(ctx, hash []byte, now time.Time) (PageToken, error) }` (`ConsumeByHash` = `UPDATE ... SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING ...` — one-shot, atomic).
- `infrastructure/postgres/page_token_store.go` — pgx v5 impl against `payment_page_tokens`; hash via `pkg/crypto` (SHA-256 of random token).
- `port/inbound/{create_page_token,exchange_page_token}_usecase.go`.
- `application/usecase/create_page_token.go` — `func NewCreatePageToken(store IPageTokenStore, booking IBookingClient, ttl time.Duration, clk clock.IClock) *CreatePageToken`; generate random token, store hash, return plaintext once.
- `application/usecase/exchange_page_token.go` — validate + consume; return visitor session.
- Handlers in `infrastructure/http/payment_handler.go` (`POST .../page-tokens`) and `session_handler.go` (`POST /api/v1/payment-page-sessions`).

### Tests
- [ ] Unit: hash-only storage (assert plaintext never persisted).
- [ ] Integration: one-time use — second exchange → 403 (TC-SEC-01 replay).

---

## Task 3: Create Payment Attempt / Charge + Event Sourcing (R3, R9 wiring)

**Traces to:** R3, R9, SRS-F-14/§3.2.24, TC-PAY-01/08/10, TC-RES-01. **Status:** constraints+tables IMPL; everything else TARGET.

### Acceptance Criteria
- [ ] AC3.1: `POST /api/v1/payments/{bookingId}/attempts` + header `Idempotency-Key` (16–64 char) + `method ∈ {VA_BCA,VA_BNI,VA_BRI,VA_PERMATA,QRIS,GOPAY}` → payment-lock Booking → charge via breaker → 201 `{attemptNo, method, status, bookingVersion, expiresAt, instruction}`.
- [ ] AC3.2: at-most-once — ≤1 open attempt (`status IN (CREATED,PENDING,UNKNOWN)`) per booking; 23505 on `uq_attempt_open_per_booking` → 409 `ATTEMPT_IN_PROGRESS`.
- [ ] AC3.3: same `Idempotency-Key` + matching `request_hash` → return stored response (no re-charge); same key + different hash → 409 `IDEMPOTENCY_KEY_REUSED`.
- [ ] AC3.4: method not active/supported → 422 `PAYMENT_METHOD_UNAVAILABLE`.
- [ ] AC3.5: breaker OPEN → 503 `PAYMENT_PROVIDER_UNAVAILABLE` **without** calling gateway (`errors.Is(err, resilience.ErrOpen)`).
- [ ] AC3.6: gateway error/timeout → 503 `PAYMENT_PROVIDER_UNAVAILABLE`, breaker failure++, attempt persisted `UNKNOWN`.
- [ ] AC3.7: method change → cancel old open attempt at gateway, create new attempt, **do not reset booking TTL** (TC-PAY-10).

### Files & signatures (**TARGET**)
- `domain/entity/payment_attempt.go` — event-sourced aggregate:
  ```go
  type Status string // CREATED,PENDING,UNKNOWN,SUCCEEDED,FAILED,CANCELLED,EXPIRED,REFUND_REQUIRED,REFUNDED
  type PaymentAttempt struct { ID, BookingID string; AttemptNo int; Method vo.Method; Provider vo.Provider;
      ProviderOrderID, ProviderTxID string; Status Status; AmountMinor int64; ExpiresAt, SettlementTime time.Time;
      Instruction vo.Instruction; Version int /* stream_version */ }
  func (a *PaymentAttempt) Apply(e eventstore.Event) error // satisfies eventstore.Applier
  func (a *PaymentAttempt) isFinal() bool // SUCCEEDED/FAILED/EXPIRED/REFUNDED — guards downgrade
  ```
- `domain/valueobject/{instruction.go,provider.go,attempt_status.go,money.go}` — `Instruction{Type: VA|QR|DEEPLINK, Bank, VANumber, QRString, DeeplinkURL}`.
- `port/outbound/payment_gateway.go`:
  ```go
  type ChargeRequest struct { OrderID string; Method vo.Method; AmountMinor int64; BuyerEmail, ItemName, ItemID string }
  type ChargeResult struct { ProviderTxID string; Instruction vo.Instruction; RawStatus string }
  type IPaymentGateway interface {
      Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
      Status(ctx context.Context, orderID string) (ChargeResult, error)
      Cancel(ctx context.Context, orderID string) error
      VerifySignature(n MidtransNotification) bool
  }
  ```
- `port/outbound/attempt_event_store.go` — `IAttemptEventStore interface { Load(ctx, db eventstore.DB, streamID string) (*entity.PaymentAttempt, int, error); AppendAndProject(ctx, tx pgx.Tx, a *entity.PaymentAttempt, expectedVer int, ev ...eventstore.NewEvent) (int, error) }` (wraps `pkg/eventstore` + read-model projection in same tx).
- `port/outbound/attempt_read_model.go` — `IAttemptReadModel interface { Latest(ctx, bookingID string) (*entity.PaymentAttempt, error); ByID(ctx, id string) (*entity.PaymentAttempt, error); OpenByBooking(ctx, bookingID string) (*entity.PaymentAttempt, error) }`.
- `port/outbound/idempotency_store.go` — `IIdempotencyStore interface { Begin(ctx, tx, scope, ownerID, key string, reqHash []byte) (stored *StoredResponse, conflict bool, err error); Complete(ctx, tx, scope, ownerID, key string, status int, body []byte) error }` (maps to `idempotency_records`; `IN_PROGRESS→COMPLETED`; hash mismatch → `ErrIdempotencyKeyReused`).
- `port/outbound/unit_of_work.go` — `IUnitOfWork interface { WithTx(ctx, func(tx pgx.Tx) error) error }`.
- `infrastructure/midtrans/gateway.go` — real adapter; `func New(cfg config.Midtrans, br *resilience.Breaker, http *http.Client) *Gateway`; **wraps every call in `br.Do`** (R9.1). `order_id = "RBT-"+bookingID+"-"+attemptNo`. Method → `payment_type`: `GOPAY→gopay`, `QRIS→qris`, `VA_*→bank_transfer{bca|bni|bri|permata}`. Posts to Midtrans Core API `/v2/charge`.
- `infrastructure/simulator/gateway.go` — local adapter (`PAYMENT_PROVIDER=simulator`); returns deterministic instruction, no network; still wrapped in breaker for parity.
- `infrastructure/postgres/{attempt_event_store.go,attempt_read_model.go,idempotency_store.go,unit_of_work.go}`.
- `port/inbound/create_attempt_usecase.go` — `ICreateAttempt interface { CreateAttempt(ctx, bookingID, visitorID, idemKey string, req dto.CreateAttemptRequest) (dto.AttemptResponse, error) }`.
- `application/usecase/create_attempt.go` — `func NewCreateAttempt(booking IBookingClient, gw IPaymentGateway, es IAttemptEventStore, rm IAttemptReadModel, idem IIdempotencyStore, uow IUnitOfWork, clk clock.IClock) *CreateAttempt`. Flow (Design §9.1): idempotency begin → payment-lock Booking → `br.Do(Charge)` → **tx**{append `AttemptCreated`+`AttemptPending`, project read model, set `stream_version`, idempotency complete}. On gateway error/timeout: persist `UNKNOWN`, return 503.
- Handler route `POST /api/v1/payments/{bookingId}/attempts`; read `Idempotency-Key` header (validate 16–64 chars) and `request_hash = SHA256(method+bookingId)`.

### Tests
- [ ] Unit: breaker OPEN → 503 without gateway call (TC-RES-01); gateway timeout → UNKNOWN + 503.
- [ ] Unit: idempotency replay (same hash) vs `IDEMPOTENCY_KEY_REUSED`.
- [ ] Unit: event projection `AttemptCreated→CREATED`, `AttemptPending→PENDING`.
- [ ] Integration: `uq_attempt_open_per_booking` (second open attempt → `ATTEMPT_IN_PROGRESS`); optimistic concurrency `ErrConcurrency` retry.

### Demo
- Charge returns DEEPLINK/VA/QR instruction; 3 consecutive gateway failures trip breaker → subsequent charge fast-fails 503.

---

## Task 4: Webhook + Signature Verification (R4)

**Traces to:** R4, SRS-F-15, SRS-C-21, TC-PAY-05, TC-PAY-11. **Status:** `payment_callbacks` IMPL; usecase TARGET.

### Acceptance Criteria
- [ ] AC4.1: `POST /api/v1/webhooks/midtrans` → verify signature **before** processing; invalid/empty → 401 `WEBHOOK_SIGNATURE_INVALID` + store callback `{signature_valid:false, processing_result:REJECTED}`.
- [ ] AC4.2: signature verification is the **only** proof of payment (I-1) — no redirect/WebView/bridge path sets SUCCEEDED.
- [ ] AC4.3: duplicate `(provider, fingerprint)` → `INSERT ... ON CONFLICT DO NOTHING`, no double effect, 200, `processing_result=DUPLICATE`.
- [ ] AC4.4: status map (`settlement|capture→SUCCEEDED`, `pending→PENDING`, `deny|cancel|failure→FAILED`, `expire→EXPIRED`) → append event + project read model in **one tx**; write outbox on money change.
- [ ] AC4.5: out-of-order callbacks never downgrade a final status (`isFinal()` guard).
- [ ] AC4.6: callback persisted to `payment_callbacks` durably **before** returning 2xx.

### Midtrans SHA512 signature formula (Design §5.2)
```
expected = SHA512(order_id + status_code + gross_amount + MIDTRANS_SERVER_KEY)
valid    = (signature_key != "") && hmacEqual(hexLower(expected), signature_key)
```
- `gross_amount` taken verbatim from the body string (e.g. `"1500000.00"`).
- Compare constant-time (`crypto/subtle.ConstantTimeCompare` / `pkg/crypto`).
- `fingerprint = SHA256(order_id + "|" + transaction_id + "|" + transaction_status)` → dedup key (`UNIQUE(provider,fingerprint)`).

### Files & signatures (**TARGET**)
- `domain/entity/callback.go` — `type Callback struct { Provider, OrderID, NormalizedStatus string; Fingerprint, PayloadDigest []byte; SignatureValid bool; AmountMinor int64; Result string }`.
- `domain/dto/midtrans_notification.go` — `type MidtransNotification struct { OrderID, TransactionStatus, StatusCode, GrossAmount, SignatureKey, TransactionID, SettlementTime, FraudStatus, PaymentType string }` (JSON, `additionalProperties:true`).
- `infrastructure/midtrans/signature.go` — `func VerifySignature(n dto.MidtransNotification, serverKey string) bool`; `func Fingerprint(n dto.MidtransNotification) []byte`.
- `port/outbound/callback_store.go` — `ICallbackStore interface { Insert(ctx, tx, Callback) (inserted bool, err error) }` (`ON CONFLICT (provider,fingerprint) DO NOTHING RETURNING id`).
- `infrastructure/postgres/callback_store.go`.
- `port/inbound/handle_webhook_usecase.go` — `IHandleWebhook interface { HandleWebhook(ctx, n dto.MidtransNotification) error }`.
- `application/usecase/handle_webhook.go` — flow (Design §9.2): verify → if invalid store REJECTED + return `WEBHOOK_SIGNATURE_INVALID`; else **tx**{insert callback (dedup), load attempt, guard downgrade, map status → append `AttemptSettled|Failed|Expired`, project, outbox on money change}.
- Handler `POST /api/v1/webhooks/midtrans` in `infrastructure/http/webhook_handler.go` → always 200 after durable store (except 401 on bad signature).

### Tests
- [ ] Unit: signature valid / wrong / empty (TC-PAY-11); fingerprint stability.
- [ ] Unit: status mapping table (A.4); downgrade guard (settlement after expire kept SUCCEEDED).
- [ ] Integration: duplicate `(provider,fingerprint)` → single effect, 200 DUPLICATE (TC-PAY-05).

---

## Task 5: Settlement & Event Publishing + Outbox Relay (R5)

**Traces to:** R5, SRS-F-16, TC-PAY-03, TC-PAY-06. **Status:** `outbox_events` table IMPL; worker TARGET.

### Acceptance Criteria
- [ ] AC5.1: attempt → SUCCEEDED ⇒ append `AttemptSettled` + outbox `payment.succeeded` `{bookingId, attemptId, settlementTime, amountMinor, method}` in same tx (I-6).
- [ ] AC5.2: attempt → FAILED ⇒ append `AttemptFailed` + outbox `payment.failed` `{bookingId, attemptId, reason}`.
- [ ] AC5.3: outbox relay worker publishes unpublished rows to exchange `rusuh.events` (topic) with **publisher confirm**, sets `published_at`, increments `publish_attempts`, records `last_error`; reschedules `available_at` per `RABBITMQ_RETRY_DELAYS` (`5s,30s,2m`), DLQ after `RABBITMQ_MAX_RETRIES`.
- [ ] AC5.4: settlement just before expiry → processed by `settlement_time`, publishes `payment.succeeded` (TC-PAY-06).

### Files & signatures (**TARGET**)
- `port/outbound/outbox_store.go` — `IOutboxStore interface { Enqueue(ctx, tx, routingKey, aggregateID string, payload any, occurredAt time.Time) error; FetchPending(ctx, limit int, now time.Time) ([]OutboxRow, error); MarkPublished(ctx, id string) error; MarkFailed(ctx, id string, availableAt time.Time, err error) error }`.
- `infrastructure/postgres/outbox_store.go` — query `WHERE published_at IS NULL AND available_at<=now() ORDER BY available_at` (`idx_payment_outbox_pending`).
- `application/usecase/relay_outbox.go` — `IRelayOutbox`; uses `clients.MQ.NewPublisher()` + `pub.Publish(ctx, row.RoutingKey, env)`; backoff schedule from `RABBITMQ_RETRY_DELAYS`.
- Register as a `delivery.Job{Name:"outbox-relay", Run: relay.Run}` in `delivery/worker.go` `jobs(*Clients)`.

### Tests
- [ ] Integration: enqueue in tx + relay → envelope on `rusuh.events`; confirm failure → `publish_attempts++`, retry.
- [ ] Integration: settlement race with expiry keeps SUCCEEDED (TC-PAY-06).

---

## Task 6: Payment Status (R6)

**Traces to:** R6, SRS-F-14, TC-PAY-01.

### Acceptance Criteria
- [ ] AC6.1: `GET /api/v1/payments/{bookingId}/status` → 200 `PaymentStatus` `{bookingId, bookingStatus, attempt, serverTime}` from read model; **read-only**, never derived from client action (I-4).

### Files & signatures (**TARGET**)
- `port/inbound/get_payment_status_usecase.go` — `IGetPaymentStatus`.
- `application/usecase/get_payment_status.go` — reads `IAttemptReadModel`.
- Handler route `GET /api/v1/payments/{bookingId}/status`.

---

## Task 7: Expiry Sweeper & Resolve (R7)

**Traces to:** R7, SRS-F-17, TC-PAY-04/07/08. **Status:** `refund_cases` table IMPL; usecase/worker TARGET.

### Acceptance Criteria
- [ ] AC7.1: `POST /internal/v1/payments/{bookingId}/resolve` (serviceBearer) + `mode ∈ {CHECK, CHECK_AND_CANCEL}` → 200 `{outcome ∈ {SUCCEEDED,NOT_PAID,NO_ATTEMPT,UNKNOWN}, settlementTime?, attemptId?}`.
- [ ] AC7.2: PENDING past `expires_at` with no webhook → sweeper appends `AttemptExpired` → EXPIRED; cancel at gateway if reachable.
- [ ] AC7.3: `CHECK_AND_CANCEL` finds settlement after booking cancel/expire → do **not** cancel; open `refund_cases(SETTLED_AFTER_CANCEL)`, mark attempt `REFUND_REQUIRED`, return `outcome=SUCCEEDED`.
- [ ] AC7.4: gateway degraded / breaker OPEN during resolve → `outcome=UNKNOWN` (do not block; Booking holds stock/VERIFYING) (TC-PAY-08).

### Files & signatures (**TARGET**)
- `domain/entity/refund_case.go` — `type RefundCase struct { AttemptID, BookingID, Reason, Status string; AmountMinor int64 }`.
- `domain/dto/resolve.go` — `ResolveRequest{Mode string}`, `ResolveResponse{Outcome string; SettlementTime *time.Time; AttemptID string}`.
- `port/outbound/refund_case_store.go` — `IRefundCaseStore interface { Open(ctx, tx, RefundCase) (opened bool, err error) }` (`ON CONFLICT (attempt_id) DO NOTHING` → I-8).
- `infrastructure/postgres/refund_case_store.go`.
- `port/inbound/{resolve_payment,expire_attempts}_usecase.go` — `IResolvePayment`, `IExpireAttempts`.
- `application/usecase/resolve_payment.go` — `func NewResolvePayment(gw IPaymentGateway, br *resilience.Breaker, es IAttemptEventStore, rm IAttemptReadModel, refunds IRefundCaseStore, uow IUnitOfWork, clk) *ResolvePayment`. `br.Do(Status)`; `ErrOpen` → `UNKNOWN`.
- `application/usecase/expire_attempts.go` — sweeper: `rm.PendingExpired(ctx, now)` → append `AttemptExpired`, cancel via breaker; settle-found → refund case.
- Register sweeper as `delivery.Job{Name:"expiry-sweeper", Run: ...}`.
- Internal handler `POST /internal/v1/payments/{bookingId}/resolve` behind `serviceBearer` middleware (Task 11).

### Tests
- [ ] Unit: resolve outcome matrix incl. `UNKNOWN` on `ErrOpen` (TC-PAY-08).
- [ ] Integration: sweeper EXPIRED (TC-PAY-04); settled-after-cancel refund case (TC-PAY-07).

---

## Task 8: Refund Cases via `booking.review` Consumer (R8)

**Traces to:** R8, SRS-F-17, TC-PAY-07. **Status:** `refund_cases.attempt_id UNIQUE` IMPL; consumer TARGET.

### Acceptance Criteria
- [ ] AC8.1: consume `booking.review` `{bookingId, attemptHint?, buyerEmail, amountMinor}` → open refund case **idempotent without inbox** via `refund_cases.attempt_id UNIQUE` + `ON CONFLICT DO NOTHING` (I-8).
- [ ] AC8.2: unparseable payload → return `rabbitmq.Permanent(err)` → DLQ (not infinite retry).

### Files & signatures (**TARGET**)
- `infrastructure/rabbitmq/review_consumer.go` — builds `rabbitmq.Subscription{Queue:"payment.booking-review", RoutingKeys:["booking.review.v1"], Handler: handler}`; handler decodes `d.Envelope.Payload`, resolves attempt, calls `IOpenRefundCase`.
- `port/inbound/open_refund_case_usecase.go` — `IOpenRefundCase interface { Open(ctx, cmd dto.ReviewCommand) error }`.
- `application/usecase/open_refund_case.go` — idempotent open via `IRefundCaseStore.Open`.
- Register consumer goroutine in `delivery/worker.go` `RunWorker` (`go clients.MQ.Consume(ctx, sub, logger)`).

### Tests
- [ ] Integration: same `booking.review` delivered 2× → single refund case.
- [ ] Unit: corrupt payload → `rabbitmq.IsPermanent(err)` true.

---

## Task 9: Circuit Breaker Wiring (R9)

**Traces to:** R9, SRS-F-19, D-40, TC-RES-01, TC-RES-02. **Status:** `pkg/resilience` IMPL (tests PASS); wiring TARGET.

### Acceptance Criteria
- [ ] AC9.1: every synchronous Midtrans call (charge, status, cancel) wrapped in a single shared `resilience.Breaker` built in the composition root.
- [ ] AC9.2: CLOSED→OPEN at `FailureThreshold` consecutive failures; OPEN→HALF_OPEN after `Cooldown`; HALF_OPEN→CLOSED on probe success / →OPEN on probe failure (verified by `pkg/resilience` tests — do not reimplement).
- [ ] AC9.3 (SHOULD): optional `resilience.Bulkhead` limits concurrency → `ErrBulkheadFull` (TC-RES-02).

### Implementation
- Construct `*resilience.Breaker` in `delivery.NewClients` from `config.Midtrans.CB*`; inject into `midtrans.New` / `simulator.New` and `ResolvePayment`.
- `OnStateChange` emits metric `rbt_payment_circuit_state` (0/1/2).

### Verify
```bash
go test ./pkg/resilience/...   # TC-RES-01, TC-RES-02 already PASS
```

---

## Task 10: Cancel Payment (R10)

**Traces to:** R10, SRS-F-13, TC-PAY-09.

### Acceptance Criteria
- [ ] AC10.1: cancel before payment → cancel attempt at gateway (via breaker), append `AttemptFailed`→CANCELLED projection, trigger Booking release stock.
- [ ] AC10.2: already SUCCEEDED → keep PAID (no cancel).

### Files & signatures (**TARGET**)
- `port/inbound/cancel_payment_usecase.go` — `ICancelPayment`.
- `application/usecase/cancel_payment.go` — guard `isFinal()`; `br.Do(gw.Cancel)`.
- Wire into booking cancel flow / handler as applicable.

---

## Task 11: Security & Isolation (R11)

**Traces to:** R11, SRS-Q-SEC-*, SRS-C-21, TC-SEC-01..03.

### Acceptance Criteria
- [ ] AC11.1: `/internal/v1/*` accepts only `Authorization: Bearer <service token>` (EdDSA, ADR-011) via `pkg/token`; rejected from public network.
- [ ] AC11.2: page-token replay → 403 (TC-SEC-01); origin bypass → 403 (TC-SEC-03).
- [ ] AC11.3: `payment_events`, `outbox_events`, logs carry no card data / `MIDTRANS_SERVER_KEY` / plaintext token (enforced by `pkg/eventstore` forbidden-field guard + review in usecases).

### Files & signatures (**TARGET**)
- `infrastructure/http/middleware.go` — `func ServiceBearer(pub ed25519.PublicKey) func(http.Handler) http.Handler` using `pkg/token`; applied to internal mux only.
- Config loads `TOKEN_PUBLIC_KEY_PATH`.

### Tests
- [ ] Unit: internal endpoint without/with bad bearer → 401/403.
- [ ] Unit: event payload with forbidden field → `eventstore.ErrForbiddenField` (assert settlement payload has no email/NIK/serverkey).

---

## Task 12: Simulator (local/ci only) (Design §2.7)

**Traces to:** Design §2.7, enables TC-PAY-03..07 locally without real Midtrans.

### Acceptance Criteria
- [ ] AC12.1: `POST /api/v1/local-simulator/payments/{attemptId}/scenario` + `{scenario}` → 202, drives a synthetic webhook into `IHandleWebhook` (same path as real Midtrans — signature computed with local server key so AC4.1 still holds).
- [ ] AC12.2: scenarios `SETTLE|SETTLE_LATE|EXPIRE|DENY|TIMEOUT|DUPLICATE_WEBHOOK|OUT_OF_ORDER`.
- [ ] AC12.3: route mounted only when `APP_ENV ∈ {local, ci}`; otherwise 404 `NOT_FOUND`.

### Scenario → behavior
| scenario | drives | effect |
|----------|--------|--------|
| `SETTLE` | webhook `transaction_status=settlement` now | attempt → SUCCEEDED, `payment.succeeded` |
| `SETTLE_LATE` | webhook `settlement` with `settlement_time` before `expires_at`, delivered after sweep | SUCCEEDED by `settlement_time` (TC-PAY-06) |
| `EXPIRE` | webhook `transaction_status=expire` | attempt → EXPIRED |
| `DENY` | webhook `transaction_status=deny` | attempt → FAILED, `payment.failed` |
| `TIMEOUT` | no webhook; simulator gateway `Charge`/`Status` returns ctx deadline | breaker failure++, attempt UNKNOWN (TC-PAY-08) |
| `DUPLICATE_WEBHOOK` | same `(provider,fingerprint)` webhook twice | second → DUPLICATE, single effect (TC-PAY-05) |
| `OUT_OF_ORDER` | `settlement` then late `pending` | final SUCCEEDED not downgraded (TC-PAY-05) |

### Files & signatures (**TARGET**)
- `infrastructure/http/simulator_handler.go` — `type SimulatorHandler struct { webhook inbound.IHandleWebhook; rm outbound.IAttemptReadModel; serverKey string }`; `func (h *SimulatorHandler) Register(mux)` guarded by env check in `delivery.NewHandler`.
- Build a valid `MidtransNotification` per scenario, compute `signature_key = SHA512(order_id+status_code+gross_amount+serverKey)`, feed to `IHandleWebhook`.

### Verify
```bash
curl -X POST localhost:8083/api/v1/local-simulator/payments/$ATTEMPT/scenario -d '{"scenario":"SETTLE"}'
```

---

## Task 13: Final Verification (R12)

**Traces to:** R12, D-23. **Status:** health IMPL; config extension in Task 0.

### Final gate
```bash
cd services/payment
go build ./... && make lint
go test ./... -race
make test-integration   # build tag integration: append-only triggers, uq_attempt_open_per_booking,
                        # callback dedup, outbox relay + confirm, refund idempotency, ErrConcurrency
make cover              # ≥ 80% logic (COVERAGE_MIN)
make ci                 # final gate before PR
```

---

## Test Case Alignment

| Task | Error codes | Test Cases |
|------|-------------|------------|
| 0 Config | — | TC-OPS-01 |
| 1 Page | `BOOKING_NOT_PAYABLE`,`BOOKING_EXPIRED`,`VISITOR_MISMATCH` | TC-PAY-01/02/10 |
| 2 Page Token | `PAYMENT_PAGE_TOKEN_INVALID` | TC-MOB-01, TC-SEC-01 |
| 3 Attempt + ES + CB | `ATTEMPT_IN_PROGRESS`,`IDEMPOTENCY_KEY_REUSED`,`PAYMENT_METHOD_UNAVAILABLE`,`PAYMENT_PROVIDER_UNAVAILABLE` | TC-PAY-01/08/10, TC-RES-01 |
| 4 Webhook | `WEBHOOK_SIGNATURE_INVALID` | TC-PAY-05, TC-PAY-11 |
| 5 Settlement/Outbox | — | TC-PAY-03, TC-PAY-06 |
| 6 Status | — | TC-PAY-01 |
| 7 Expiry/Resolve | — | TC-PAY-04/07/08 |
| 8 booking.review | — | TC-PAY-07 |
| 9 Breaker wiring | `PAYMENT_PROVIDER_UNAVAILABLE` | TC-RES-01, TC-RES-02 |
| 10 Cancel | — | TC-PAY-09 |
| 11 Security | `NOT_FOUND` (internal) | TC-SEC-01/02/03 |
| 12 Simulator | `NOT_FOUND` | drives TC-PAY-03..07 |
| 13 CI | — | TC-OPS-01 |

## Invariant → enforcement map

| Invariant | Enforced by (task) |
|-----------|--------------------|
| I-1 signature = only proof | Task 4 (`VerifySignature` before processing) |
| I-2 at-most-once charge | Task 3 (`uq_attempt_open_per_booking`, idempotency) |
| I-3 webhook idempotent/out-of-order | Task 4 (`UNIQUE(provider,fingerprint)`, `isFinal()` guard) |
| I-4 final status only from webhook/resolve | Task 4/6/7 (read-only status) |
| I-5 event store append-only | migration triggers + `pkg/eventstore` |
| I-6 money change ⇒ outbox in same tx | Task 4/5 (`AppendAndProject` + outbox in one tx) |
| I-7 no PII/secret in event/outbox/log | Task 11 (`eventstore.ErrForbiddenField`) |
| I-8 refund idempotent without inbox | Task 7/8 (`refund_cases.attempt_id UNIQUE`) |
