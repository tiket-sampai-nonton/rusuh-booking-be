# Tasks — Notification Service

| Field | Value |
|-------|-------|
| Service | Notification Service (consumer-only; **produces events: none**) |
| Port | 8084 |
| Module | `github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification` |
| Spec | `specs/notification-service/{requirements,design}.md` |
| Verbs | `api` (push-subscription API + `/health`), `worker` (consumers + `/health`) |
| Status | Boilerplate + health + config + migrations **implemented**; all delivery logic **TARGET** |

This file is grounded in the real code as of 2026-10-20. It states, per task,
the **exact file paths**, **function signatures**, the **verbatim `pkg/rabbitmq`
API**, the **`embed.FS` pattern**, the **`net/smtp` adapter**, the **FCM
interface**, the **`ON CONFLICT DO NOTHING`** idempotency pattern, and the
**exact error codes** per acceptance criterion.

---

## 0. Ground truth — what EXISTS vs what is MISSING

### Already implemented (do NOT rebuild)

| Area | File(s) | Notes |
|------|---------|-------|
| Cobra verbs `api`/`worker`/`migrate`/`healthcheck` | `cmd/*.go` | `cmd/worker.go` calls `delivery.RunWorker(ctx, cfg, logger, Need{Postgres:true, RabbitMQ:true})` |
| Composition root | `internal/delivery/{server.go,worker.go,clients.go,serve.go}` | `NewHandler`, `NewClients`, `RunServer`, `RunWorker`, `loop`, `jobs` |
| Config fail-fast (D-24) | `internal/config/{config.go,load.go}` + `load_test.go` | `App/HTTP/Worker/Postgres/RabbitMQ` loaded & validated together |
| Clients (PG + MQ) | `internal/delivery/clients.go` | `Clients{PG *postgres.Client; MQ *rabbitmq.Connection}`, `Pingers()`, `Close()` |
| Deep `GET /health` | `infrastructure/http/health_handler.go`, `infrastructure/healthcheck/checker.go`, `application/usecase/check_health.go` | `server_test.go` green |
| Migration `0001_init` | `deployments/migrations/0001_init.up.sql` + `embed.go` | `deliveries`, `push_subscriptions` — **applied, do NOT edit; new schema → `0002_*`** |
| Health port + mock | `port/{inbound/health_usecase.go,outbound/dependency_checker.go}`, `_mock/notification/port/...` | pattern to copy for new ports |
| Shared `pkg/rabbitmq` consumer/retry/envelope | `pkg/rabbitmq/{consumer.go,retry.go,envelope.go,config.go,publisher.go}` | reused as-is; see §A |
| `pkg/crypto` AES-GCM + HMAC | `pkg/crypto/crypto.go` | `Sealer.Open`, `ParseKey`, `Hasher.Hash` — see §C |
| `pkg/apperror` envelope | `pkg/apperror/apperror.go` | `Code`, `Error`, `ToEnvelope`; see §D |
| `pkg/postgres` pool + SQLSTATE | `pkg/postgres/client.go` | `Client.Pool() *pgxpool.Pool`, `IsCode`, `CodeUniqueViolation="23505"` |

### Missing (TARGET — this file drives it)

| Area | Where it will live |
|------|--------------------|
| Consumer wiring (replace empty `jobs()`/`nil`) | `internal/delivery/worker.go` — register `rabbitmq.Subscription` goroutines in `RunWorker` |
| Dispatch handlers (`booking.*`, `queue.turn.*`, `otp.requested`) | `infrastructure/rabbitmq/*_handler.go` |
| Delivery entity + repo | `domain/entity/delivery.go`, `port/outbound/delivery_repository.go`, `infrastructure/postgres/delivery_repository.go` |
| Push-subscription entity + repo | `domain/entity/push_subscription.go`, `port/outbound/subscription_repository.go`, `infrastructure/postgres/subscription_repository.go` |
| Email sender (SMTP) | `port/outbound/email_sender.go`, `infrastructure/smtp/email_sender.go` |
| Push sender (FCM) | `port/outbound/push_sender.go`, `infrastructure/fcm/push_sender.go` |
| Template renderer (`embed.FS`) | `port/outbound/template_renderer.go`, `infrastructure/template/renderer.go`, `assets/templates/*.tmpl` |
| PII protector (OTP decrypt) | `port/outbound/pii_protector.go`, `infrastructure/security/pii_protector.go` |
| Recipient hasher (HMAC) | folded into `infrastructure/security/pii_protector.go` |
| Notify usecases (one per event) | `application/usecase/notify_*.go` |
| Push-subscription usecases + HTTP | `application/usecase/{subscribe_push,unsubscribe_push}.go`, `infrastructure/http/subscription_handler.go` |
| New config fields (`SMTP_*`, `FCM_*`, `PII_*`, `TEMPLATE_*`) | `internal/config/{config.go,load.go}` + `.env.example` |

---

## A. `pkg/rabbitmq` consumer registration — EXACT API (verbatim)

The service does **not** write its own AMQP loop. It builds `Handler`s and
registers `Subscription`s; `Connection.Consume` owns topology, QoS, workers,
retry, and DLQ.

```go
// pkg/rabbitmq/consumer.go
type Delivery struct {
    Envelope   Envelope // parsed + Validate()'d before Handler is called
    RoutingKey string
    Attempt    int      // 1 on first delivery
}

type Handler func(ctx context.Context, d Delivery) error

type Subscription struct {
    Queue       string   // e.g. "notification.booking.events"
    RoutingKeys []string // e.g. []string{"booking.paid","booking.expired","booking.review"}
    Handler     Handler
}

func (c *Connection) Consume(ctx context.Context, sub Subscription, logger *slog.Logger) error
```

Guarantees already handled by `Consume`/`handle`:
- Declares quorum main queue, one `<queue>.retry.<delay>` per unique delay, and `<queue>.dlq` (`DeclareTopology`).
- Sets QoS prefetch; runs `cfg.Workers` goroutines; manual ack.
- Unmarshals the envelope and calls `env.Validate()` **before** the handler. Bad envelope → `Permanent` → DLQ (handler never sees it).
- Wraps the handler in `safeCall` (panic → `Permanent`).
- Calls `Decide(queue, attempt, err, MaxRetries, RetryDelays)`:
  - `err == nil` → `ActionAck`.
  - `IsPermanent(err)` **or** `attempt > MaxRetries` **or** no delays → `ActionDeadLetter` → `<queue>.dlq`.
  - else → `ActionRetry` → `<queue>.retry.<delays[attempt-1]>`, bumping header `x-rbt-attempt`.
- If republish fails → `Nack(requeue=true)` (no loss).

Envelope (`pkg/rabbitmq/envelope.go`): `MessageID, EventType, SchemaVersion(int), OccurredAt, Producer, AggregateID, CorrelationID, Payload json.RawMessage`.
`source_message_id = d.Envelope.MessageID`. Payload decoded by each handler.

### A.1 Wiring task — replace the empty `jobs()` path in `RunWorker`

**File:** `internal/delivery/worker.go` (edit). Current state: `jobs()` returns
`nil`; `worker_test.go` asserts `assert.Empty(t, jobs(&Clients{}))`.

> The periodic `loop`/`jobs()` is a *separate* mechanism (reserved for a future
> reconciliation sweeper). The event consumers are **not** jobs — register them
> as `Subscription` goroutines. Keep `jobs()` returning `nil`, keep `loop`
> running (it still serves nothing until a sweeper is added), and add the
> consumer goroutines before `loop`.

Target shape (names must match the handlers built in later tasks):

```go
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger, need Need) error {
    clients, err := NewClients(ctx, cfg, need)
    if err != nil { return err }
    defer clients.Close()

    deps, err := newWorkerDeps(cfg, clients, logger) // builds repos, senders, renderer, pii, handlers
    if err != nil { return err }

    httpHandler, err := NewHandler(cfg, clients, logger)
    if err != nil { return err }
    errCh := make(chan error, 1)
    go func() { errCh <- serve(ctx, cfg, httpHandler, logger) }()

    subs := []rabbitmq.Subscription{
        {Queue: "notification.booking.events",
         RoutingKeys: []string{"booking.paid", "booking.expired", "booking.review"},
         Handler: deps.booking.Handle},
        {Queue: "notification.queue.events",
         RoutingKeys: []string{"queue.turn.soon", "queue.turn.arrived"},
         Handler: deps.turn.Handle},
        {Queue: "notification.otp",
         RoutingKeys: []string{"otp.requested"},
         Handler: deps.otp.Handle},
    }
    var wg sync.WaitGroup
    for _, s := range subs {
        s := s
        wg.Add(1)
        go func() {
            defer wg.Done()
            if cerr := clients.MQ.Consume(ctx, s, logger); cerr != nil {
                logger.Error("consume berhenti", "queue", s.Queue, "error", cerr)
            }
        }()
    }

    err = loop(ctx, cfg.Worker.Interval, jobs(clients), logger) // unchanged; returns on ctx.Done
    wg.Wait()
    if serr := <-errCh; serr != nil && !errors.Is(serr, context.Canceled) {
        return serr
    }
    return err
}
```

Add `newWorkerDeps(cfg, clients, logger)` in a new file
`internal/delivery/worker_deps.go` (composition root only — wires concrete
adapters into handlers). `worker` verb already requests `Need{Postgres:true,
RabbitMQ:true}` (`cmd/worker.go`), so no cmd change.

---

## B. Email templates — `embed.FS` pattern (EXACT)

**Files:**
- `assets/templates/eticket.tmpl`
- `assets/templates/expired.tmpl`
- `assets/templates/review.tmpl`
- `assets/templates/otp_login.tmpl`
- `infrastructure/template/renderer.go`
- `infrastructure/template/embed.go` (the `embed.FS` — must sit next to the files it embeds)

`embed.go` (mirrors `deployments/migrations/embed.go`):

```go
package template

import "embed"

//go:embed all:templates
var templatesFS embed.FS
```

> `embed` only reaches files in or below the embedding package's directory.
> Put the `.tmpl` files under `infrastructure/template/templates/` and embed
> `all:templates`, OR embed from a package rooted at `assets/`. Pick one;
> `infrastructure/template/templates/*.tmpl` keeps the adapter self-contained.

`renderer.go`:

```go
package template

import (
    "bytes"
    "fmt"
    "text/template"
)

// Renderer implements outbound.ITemplateRenderer. Templates parsed once at construction.
type Renderer struct{ t *template.Template }

// New parses every *.tmpl from the embedded FS with the IDR/time helper funcs.
func New() (*Renderer, error) {
    t, err := template.New("").Funcs(template.FuncMap{
        "formatIDR":  formatIDR,   // int64 minor → "3.000.000"
        "formatTime": formatTime,  // time.Time → RFC1123/local
    }).ParseFS(templatesFS, "templates/*.tmpl")
    if err != nil {
        return nil, fmt.Errorf("template: parse: %w", err)
    }
    return &Renderer{t: t}, nil
}

// Render executes the named template ("eticket","expired","review","otp_login").
// A render error is a POISON condition: callers wrap it in rabbitmq.Permanent.
func (r *Renderer) Render(name string, data any) (string, error) {
    var buf bytes.Buffer
    if err := r.t.ExecuteTemplate(&buf, name+".tmpl", data); err != nil {
        return "", fmt.Errorf("template: render %s: %w", name, err)
    }
    return buf.String(), nil
}
```

Template bodies per design.md §5.1–§5.4 (subject line is the template's first
line; split on first `\n` in the SMTP adapter, or keep subject in code — see
Task 1 decision). `text/template` (not `html/template`): bodies are plain-text
emails.

---

## C. Ports & adapters — EXACT signatures

All interfaces are `I`-prefixed and live in `port/outbound/` with a
`//go:generate mockgen` directive mirroring `dependency_checker.go`. Run
`make mock` after adding/changing a port.

### C.1 `port/outbound/delivery_repository.go`

```go
//go:generate go tool mockgen -source=delivery_repository.go -destination=../../../../_mock/notification/port/outbound/delivery_repository_mock.go -package=mock_outbound

// Delivery is the claim/status record (mirrors deliveries table).
type Delivery struct {
    SourceMessageID string        // = Envelope.MessageID (uuid)
    Type            string        // BOOKING_PAID|BOOKING_EXPIRED|BOOKING_REVIEW|TURN_SOON|TURN_ARRIVED|OTP_LOGIN*
    Channel         string        // EMAIL|PUSH
    BookingID       *string
    QueueEntryID    *string
    RecipientHash   []byte        // HMAC, never plaintext
}

type IDeliveryRepository interface {
    // ClaimDelivery inserts ... ON CONFLICT DO NOTHING. Returns inserted=true when the
    // row was claimed (rowsAffected==1), false on conflict (duplicate → ack, R8).
    ClaimDelivery(ctx context.Context, d Delivery) (inserted bool, err error)
    MarkSent(ctx context.Context, srcMsgID, notifType, providerMsgID string) error
    BumpAttempt(ctx context.Context, srcMsgID, notifType, lastErr string) error
    MarkDead(ctx context.Context, srcMsgID, notifType, lastErr string) error
    MarkExpired(ctx context.Context, srcMsgID, notifType string) error // sets expires_at, keeps ack
}
```

> **Note on `OTP_LOGIN`:** the migration's CHECK constraint on
> `notification_type` lists only the 5 booking/turn types, and the channel CHECK
> forces `PUSH` iff type ∈ {TURN_SOON,TURN_ARRIVED}. OTP email therefore cannot
> be persisted in `deliveries` under the current schema. **Decision for Task 12:
> OTP is NOT tracked in `deliveries`** (fire-once, no retry, no status row) —
> keep it out of the repo path entirely. If OTP tracking is later required, add
> migration `0002_*` extending both CHECKs; do not edit `0001_init`.

### C.2 `port/outbound/subscription_repository.go`

```go
//go:generate go tool mockgen -source=subscription_repository.go -destination=../../../../_mock/notification/port/outbound/subscription_repository_mock.go -package=mock_outbound

// PushSubscription mirrors push_subscriptions; Token is the DECRYPTED FCM token.
type PushSubscription struct {
    VisitorID string
    EventID   string
    Platform  string // ANDROID|IOS
    Token     string // decrypted on read; sealed on write
}

type ISubscriptionRepository interface {
    // Upsert stores the subscription, encrypting Token → push_token_enc.
    // ON CONFLICT (visitor_id, event_id) DO UPDATE (idempotent re-subscribe, R7).
    Upsert(ctx context.Context, s PushSubscription, expiresAt time.Time) error
    // Get returns nil (no error) when no row exists for (visitorID,eventID).
    Get(ctx context.Context, visitorID, eventID string) (*PushSubscription, error)
    Delete(ctx context.Context, visitorID, eventID string) error
}
```

### C.3 `port/outbound/email_sender.go`

```go
//go:generate go tool mockgen -source=email_sender.go -destination=../../../../_mock/notification/port/outbound/email_sender_mock.go -package=mock_outbound

type IEmailSender interface {
    // Send delivers one email; returns a provider message id on success.
    // A non-nil error is TRANSIENT by default (SMTP down → retry), unless the
    // caller chooses to ack (OTP, R12).
    Send(ctx context.Context, to, subject, body string) (providerMsgID string, err error)
}
```

### C.4 `port/outbound/push_sender.go`

```go
//go:generate go tool mockgen -source=push_sender.go -destination=../../../../_mock/notification/port/outbound/push_sender_mock.go -package=mock_outbound

type PushNotification struct {
    Token string            // decrypted FCM device token
    Title string
    Body  string
    Data  map[string]string // eventId, queueEntryId, type, claimDeadline?
}

type IPushSender interface {
    Send(ctx context.Context, n PushNotification) (providerMsgID string, err error)
    // IsTokenInvalid reports whether err means the token is dead (→ delete subscription + ack, R4/R5).
    IsTokenInvalid(err error) bool
}
```

### C.5 `port/outbound/template_renderer.go`

```go
//go:generate go tool mockgen -source=template_renderer.go -destination=../../../../_mock/notification/port/outbound/template_renderer_mock.go -package=mock_outbound

type ITemplateRenderer interface {
    Render(name string, data any) (string, error) // error → rabbitmq.Permanent (poison)
}
```

### C.6 `port/outbound/pii_protector.go`

```go
//go:generate go tool mockgen -source=pii_protector.go -destination=../../../../_mock/notification/port/outbound/pii_protector_mock.go -package=mock_outbound

type IPIIProtector interface {
    UnsealEmail(enc []byte) (string, error) // decrypt emailEnc (otp.requested)
    UnsealOTP(enc []byte) (string, error)   // decrypt codeEnc (otp.requested)
    HashEmail(email string) []byte          // HMAC → deliveries.recipient_hash
    HashVisitor(visitorID string) []byte    // HMAC → deliveries.recipient_hash (push)
}
```

---

## C.7 PII adapter — reuse `pkg/crypto` (EXACT), mirror User Service

**File:** `infrastructure/security/pii_protector.go`. The OTP payload is sealed
by the User Service with `pkg/crypto.Sealer` using **AAD-bound** contexts; the
Notification Service must open with the **same key and the same AAD strings**.

From `services/user/.../security/security.go` (source of the seal):
- email sealed with AAD `[]byte("users.accounts.email")` → open with the same.
- code sealed with AAD `[]byte("otp.requested.code")` → open with the same.

```go
package security

import (
    "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
)

// PII implements outbound.IPIIProtector. encKey & hashKey are 32-byte keys from env.
type PII struct {
    sealer *crypto.Sealer
    hasher *crypto.Hasher
}

func NewPII(encKey, hashKey []byte) (*PII, error) {
    s, err := crypto.NewSealer(encKey) // crypto.NewSealer requires exactly 32 bytes
    if err != nil { return nil, err }
    h, err := crypto.NewHasher(hashKey)
    if err != nil { return nil, err }
    return &PII{sealer: s, hasher: h}, nil
}

// AAD must match the User Service sealer contexts byte-for-byte.
func (p *PII) UnsealEmail(enc []byte) (string, error) {
    b, err := p.sealer.Open(enc, []byte("users.accounts.email"))
    return string(b), err // crypto.ErrDecrypt on wrong key/corrupt → caller wraps Permanent
}
func (p *PII) UnsealOTP(enc []byte) (string, error) {
    b, err := p.sealer.Open(enc, []byte("otp.requested.code"))
    return string(b), err
}
func (p *PII) HashEmail(email string) []byte       { return p.hasher.Hash("email:" + email) }
func (p *PII) HashVisitor(visitorID string) []byte { return p.hasher.Hash("visitor:" + visitorID) }
```

Key material: `PII_ENC_KEY` (shared with User Service, 32-byte hex via
`crypto.ParseKey`) and a notification-local `PII_HASH_KEY` (32-byte hex) for
`recipient_hash`. `emailEnc`/`codeEnc` arrive base64 in JSON → decode to
`[]byte` before `Open`.

> **CONFIRM with User Service team:** the AAD strings above are copied from
> `user/.../security.go` (`SealEmail` uses `"users.accounts.email"`, `SealOTP`
> uses `"otp.requested.code"`). If the producer ever changes the seal context,
> decryption fails as `crypto.ErrDecrypt` and every OTP goes to DLQ — treat the
> AAD contract as a shared constant.

---

## D. HTTP error envelope — EXACT (`pkg/apperror`)

Push-subscription handlers map domain errors to `apperror.Error`, then
`ToEnvelope(requestID)`. Shared codes available: `CodeValidationFailed =
"VALIDATION_ERROR"`, `CodeNotFound = "NOT_FOUND"`, `CodeInternal =
"INTERNAL_ERROR"`. Service-specific codes (`PUSH_TOKEN_INVALID`,
`SUBSCRIPTION_EXISTS`, `SUBSCRIPTION_NOT_FOUND`) are declared next to the domain
error and mapped in `infrastructure/http`. Envelope: `{"meta":{"code",
"message","requestId","errorCode","details"},"data":null}` — never SQL, stack,
or secrets.

---

## Pre-requisites

```bash
cd services/notification
go build ./...                      # boilerplate must compile
make infra keys                     # Postgres, RabbitMQ, Mailpit + local keys (from repo root)
cp .env.example .env
go run ./cmd migrate up
curl localhost:8084/health          # after: go run ./cmd api &
curl localhost:8025                 # Mailpit UI
```

Add new env to `internal/config/{config.go,load.go}` + `.env.example` **first**
(Task 0b) so the service still starts fail-fast.

---

## Task 0a: Delivery entity, repository, idempotency (R6, R8) — do FIRST

**Traces to:** Requirement 6, Requirement 8, SRS-Q-REL-01, TC-MSG-03

Every email/push handler depends on this. Build it before the handlers.

### Files
- `domain/entity/delivery.go` — status constants `PENDING|SENDING|SENT|RETRY|DEAD`, type/channel constants.
- `port/outbound/delivery_repository.go` — interface §C.1.
- `infrastructure/postgres/delivery_repository.go` — `pgxpool`-backed impl.
- `_mock/notification/port/outbound/delivery_repository_mock.go` — via `make mock`.

### `ClaimDelivery` — EXACT `ON CONFLICT DO NOTHING` (idempotency key)

```go
func (r *DeliveryRepository) ClaimDelivery(ctx context.Context, d outbound.Delivery) (bool, error) {
    const q = `
INSERT INTO deliveries
    (source_message_id, notification_type, channel, booking_id, queue_entry_id, recipient_hash, status, attempts)
VALUES ($1, $2, $3, $4, $5, $6, 'SENDING', 0)
ON CONFLICT (source_message_id, notification_type) DO NOTHING;`
    tag, err := r.pool.Exec(ctx, q, d.SourceMessageID, d.Type, d.Channel, d.BookingID, d.QueueEntryID, d.RecipientHash)
    if err != nil {
        return false, fmt.Errorf("postgres: claim delivery: %w", err) // transient → retry
    }
    return tag.RowsAffected() == 1, nil // 1 = claimed (send); 0 = duplicate (ack, R8)
}
```

Status-mutating queries (all parameterized, scoped by the unique key):

```sql
-- MarkSent
UPDATE deliveries SET status='SENT', sent_at=now(), provider_message_id=$3
 WHERE source_message_id=$1 AND notification_type=$2;
-- BumpAttempt (transient; next_attempt_at advanced by retry-queue TTL, kept here for the sweeper)
UPDATE deliveries SET status='RETRY', attempts=attempts+1, last_error=$3, next_attempt_at=now()
 WHERE source_message_id=$1 AND notification_type=$2;
-- MarkDead
UPDATE deliveries SET status='DEAD', last_error=$3
 WHERE source_message_id=$1 AND notification_type=$2;
-- MarkExpired (push past claimDeadline; ack & drop, R5)
UPDATE deliveries SET expires_at=now()
 WHERE source_message_id=$1 AND notification_type=$2;
```

`r.pool` comes from `clients.PG.Pool()` (`pkg/postgres`). Claim **before** the
non-idempotent side effect (send). Row CHECKs enforce: EMAIL types carry
`booking_id`; PUSH types (TURN_*) carry `queue_entry_id`.

### Acceptance Criteria
- [ ] AC6.1: Exactly one `deliveries` row per `(source_message_id, notification_type)`.
- [ ] AC6.2: Row carries `attempts`, `last_error`, `provider_message_id`, `next_attempt_at`, `recipient_hash`, moving `PENDING→SENDING→SENT|RETRY→DEAD`.
- [ ] AC8.1: Duplicate event → `ClaimDelivery` returns `inserted=false` → no second send.
- [ ] AC8.2: On conflict, handler returns `nil` (ack) without sending.

### Tests
- [ ] Unit (table-driven, testify): `inserted=true`→proceed, `inserted=false`→ack; DB error→transient.
- [ ] Integration (`//go:build integration`, real Postgres): insert same key twice → second `RowsAffected()==0` (TC-MSG-03).

---

## Task 0b: New config + env (SMTP, FCM, PII, TEMPLATE)

**Traces to:** Requirement 11, D-24

### Files (edit)
- `internal/config/config.go` — add structs `SMTP`, `FCM`, `PII` (+ fields on `Config`); add `Validate()` rules (all fail-fast, errors joined).
- `internal/config/load.go` — read via `dotenv.Reader` with defaults; decode `PII_ENC_KEY`/`PII_HASH_KEY` with `crypto.ParseKey` (32-byte hex) and append parse errors to the joined set.
- `.env.example` — document new keys.
- `internal/config/load_test.go` — extend table.

### New env (defaults for local)
```
SMTP_HOST=localhost
SMTP_PORT=1025                 # Mailpit
SMTP_USER=
SMTP_PASSWORD=
SMTP_FROM=no-reply@rusuh.local
SMTP_TIMEOUT=10s
FCM_PROJECT_ID=
FCM_CREDENTIALS_PATH=
PII_ENC_KEY=<64 hex chars>     # shared with User Service
PII_HASH_KEY=<64 hex chars>    # notification-local recipient_hash key
TEMPLATE_* (none — templates embedded)
```

### Acceptance Criteria
- [ ] AC11.1: Service exits non-zero if any new var is invalid (e.g. `PII_ENC_KEY` not 32-byte hex, `SMTP_PORT` out of range, `SMTP_TIMEOUT<=0`), reporting all at once.

### Tests
- [ ] Unit: missing/short `PII_ENC_KEY` → error; valid set → `Config` populated.

---

## Task 1: E-ticket Email (R1)

**Traces to:** Requirement 1, SRS-F-20-01..03, TC-TKT-01, TC-TKT-02

### Files
- `domain/dto/booking_paid_payload.go` — `BookingPaidPayload` + `Validate()` (matches `booking.paid.v1`: `BookingID, PublicRef, BuyerEmail, Event{EventID,Name,StartsAt,Venue}, Tickets[]{TicketCode,AttendeeName,AttendeeNikMasked,CategoryCode}, TicketLinkToken`).
- `assets` template per §B (`eticket`).
- `application/usecase/notify_booking_paid.go` — `NotifyBookingPaid` usecase.
- `infrastructure/rabbitmq/booking_handler.go` — `BookingHandler.Handle` dispatch by `d.RoutingKey`.
- SMTP adapter (Task created here, reused by R2/R3/R12): `infrastructure/smtp/email_sender.go`.

### SMTP adapter — `net/smtp` (EXACT)

```go
package smtp

import (
    "context"
    "fmt"
    "net/smtp"
    "strings"

    "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/config"
)

// Sender implements outbound.IEmailSender using net/smtp PlainAuth (SES / Mailpit).
type Sender struct {
    addr string // host:port
    auth smtp.Auth
    from string
}

func New(cfg config.SMTP) *Sender {
    var auth smtp.Auth
    if cfg.User != "" {
        auth = smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
    }
    return &Sender{addr: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), auth: auth, from: cfg.From}
}

func (s *Sender) Send(ctx context.Context, to, subject, body string) (string, error) {
    msg := buildRFC822(s.from, to, subject, body) // From/To/Subject/MIME headers + body
    // net/smtp.SendMail is blocking; enforce cfg.Timeout via a goroutine + ctx select,
    // or dial manually with a net.Dialer{Timeout} and smtp.NewClient for ctx cancellation.
    if err := smtp.SendMail(s.addr, s.auth, s.from, []string{to}, msg); err != nil {
        return "", fmt.Errorf("smtp: send: %w", err) // TRANSIENT by default
    }
    return "", nil // Mailpit/SES-SMTP returns no message id over SMTP; leave empty
}

func buildRFC822(from, to, subject, body string) []byte {
    var b strings.Builder
    b.WriteString("From: " + from + "\r\n")
    b.WriteString("To: " + to + "\r\n")
    b.WriteString("Subject: " + subject + "\r\n")
    b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
    b.WriteString(body)
    return []byte(b.String())
}
```

> `net/smtp.SendMail` ignores `ctx`. For the `SMTP_TIMEOUT` requirement, wrap the
> call: run `SendMail` in a goroutine and `select` on `ctx.Done()`, or dial via
> `net.Dialer{Timeout}` + `smtp.NewClient`. Subject is passed separately from the
> body — keep the subject in the usecase (per design.md §5), not inside the
> template body.

### Handler — EXACT flow (design.md §4.3)

```go
func (h *BookingHandler) Handle(ctx context.Context, d rabbitmq.Delivery) error {
    switch d.RoutingKey {
    case "booking.paid":    return h.handlePaid(ctx, d)
    case "booking.expired": return h.handleExpired(ctx, d)
    case "booking.review":  return h.handleReview(ctx, d)
    default:
        return rabbitmq.Permanent(fmt.Errorf("routing key tak dikenal: %s", d.RoutingKey))
    }
}

func (h *BookingHandler) handlePaid(ctx context.Context, d rabbitmq.Delivery) error {
    var p dto.BookingPaidPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decode booking.paid: %w", err))
    }
    if err := p.Validate(); err != nil {
        return rabbitmq.Permanent(err)
    }
    inserted, err := h.deliveries.ClaimDelivery(ctx, outbound.Delivery{
        SourceMessageID: d.Envelope.MessageID, Type: "BOOKING_PAID", Channel: "EMAIL",
        BookingID: &p.BookingID, RecipientHash: h.pii.HashEmail(p.BuyerEmail),
    })
    if err != nil { return err }          // transient → retry
    if !inserted { return nil }           // duplicate → ack (R8)

    body, rerr := h.tmpl.Render("eticket", p)
    if rerr != nil {
        _ = h.deliveries.MarkDead(ctx, d.Envelope.MessageID, "BOOKING_PAID", rerr.Error())
        return rabbitmq.Permanent(rerr)   // poison → DLQ (R1 AC1.3)
    }
    providerID, serr := h.email.Send(ctx, p.BuyerEmail, subjectEticket(p), body)
    if serr != nil {
        _ = h.deliveries.BumpAttempt(ctx, d.Envelope.MessageID, "BOOKING_PAID", serr.Error())
        return serr                       // SMTP transient → retry queue
    }
    return h.deliveries.MarkSent(ctx, d.Envelope.MessageID, "BOOKING_PAID", providerID)
}
```

### Acceptance Criteria → EXACT disposition
- [ ] AC1.1: `booking.paid` → render `eticket`, send to `buyerEmail`. **Error code:** n/a (consumer).
- [ ] AC1.2: success → `deliveries` `status='SENT'`, `sent_at`, `provider_message_id` set.
- [ ] AC1.3: decode/validate/render fail → `rabbitmq.Permanent` → **DLQ `notification.booking.events.dlq`**, row `DEAD`.
- [ ] AC1.4: email body contains `ticketCode`, `attendeeName`, `attendeeNikMasked`, `publicRef`, `ticketLinkToken`.

### Tests
- [ ] Unit (mocked repo/tmpl/email): happy path→MarkSent; duplicate→ack, no send; render err→Permanent+MarkDead; SMTP err→BumpAttempt+err returned.
- [ ] Integration: email lands in Mailpit with masked NIK + ticket code (TC-TKT-01, TC-TKT-02).
- [ ] Integration: duplicate delivery → one email (TC-MSG-03).

---

## Task 2: Expiry Email (R2)

**Traces to:** Requirement 2, SRS-F-21-01..03, TC-MSG-01

### Files
- `domain/dto/booking_expired_payload.go` — `BookingID, PublicRef, BuyerEmail, Reason(PAYMENT_TIMEOUT|SESSION_EXPIRED|CANCELLED)` + `Validate()`.
- `assets` template `expired`.
- `application/usecase/notify_booking_expired.go`; dispatch in `handleExpired`.

Flow identical to Task 1 with `Type="BOOKING_EXPIRED"`.

### Acceptance Criteria
- [ ] AC2.1: `booking.expired` → render `expired`, send; row `BOOKING_EXPIRED`/`EMAIL`.
- [ ] AC2.2: SMTP down → **transient** (return raw `err`) → retry queue `notification.booking.events.retry.5s|30s|2m`, delivered after recovery (TC-MSG-01). **Not** `Permanent`.

### Tests
- [ ] Integration: SMTP down then up → eventual SENT (TC-MSG-01).

---

## Task 3: Review Email (R3)

**Traces to:** Requirement 3, SRS-F-21-01..03, TC-MSG-01

### Files
- `domain/dto/booking_review_payload.go` — `BookingID, BuyerEmail, AmountMinor(int64), AttemptHint(optional)` + `Validate()`.
- `assets` template `review` (uses `formatIDR` func).
- `application/usecase/notify_booking_review.go`; dispatch in `handleReview`.

### Acceptance Criteria
- [ ] AC3.1: `booking.review` → render `review` (IDR-formatted `amountMinor`, optional `attemptHint`), send; row `BOOKING_REVIEW`/`EMAIL`.

---

## Task 4: Turn Soon Push (R4)

**Traces to:** Requirement 4, SRS-F-25-01..13, TC-QUE-04

### Files
- `domain/entity/push_subscription.go`.
- `domain/dto/turn_soon_payload.go` — `EventID, QueueEntryID, VisitorID` + `Validate()`.
- `port/outbound/subscription_repository.go` (§C.2), `port/outbound/push_sender.go` (§C.4).
- `infrastructure/postgres/subscription_repository.go`.
- `infrastructure/fcm/push_sender.go` — FCM HTTP v1 client, implements `IPushSender`.
- `application/usecase/notify_turn_soon.go`.
- `infrastructure/rabbitmq/turn_handler.go` — `TurnHandler.Handle` dispatch by routing key.

### FCM adapter interface (EXACT)

Adapter implements `outbound.IPushSender` (§C.4). FCM HTTP v1 to
`https://fcm.googleapis.com/v1/projects/{FCM_PROJECT_ID}/messages:send`, OAuth2
from `FCM_CREDENTIALS_PATH` service-account JSON (use
`golang.org/x/oauth2/google` token source; **pin exact version** when added).

```go
package fcm

type Sender struct {
    projectID string
    http      *http.Client // with oauth2 transport
}

func New(ctx context.Context, cfg config.FCM) (*Sender, error) { /* build oauth2 token source */ }

func (s *Sender) Send(ctx context.Context, n outbound.PushNotification) (string, error) {
    // POST messages:send {message:{token, notification:{title,body}, data}}
    // 200 → return response .name; non-2xx → classify.
}

// IsTokenInvalid returns true for UNREGISTERED / INVALID_ARGUMENT(token) FCM errors.
func (s *Sender) IsTokenInvalid(err error) bool { /* inspect typed fcmError.status */ }
```

### Handler flow (design.md §4.4 without deadline)
```go
sub, err := h.subs.Get(ctx, p.VisitorID, p.EventID) // transient err → retry
if err != nil { return err }
if sub == nil { return nil }                         // no subscription → ack (AC4.2)
inserted, err := h.deliveries.ClaimDelivery(ctx, outbound.Delivery{
    SourceMessageID: d.Envelope.MessageID, Type: "TURN_SOON", Channel: "PUSH",
    QueueEntryID: &p.QueueEntryID, RecipientHash: h.pii.HashVisitor(p.VisitorID)})
if err != nil { return err }
if !inserted { return nil }                          // duplicate → ack (R8)
id, serr := h.push.Send(ctx, outbound.PushNotification{Token: sub.Token,
    Title: "Giliran Sebentar Lagi", Body: "Bersiap, giliran Anda hampir tiba",
    Data: map[string]string{"eventId": p.EventID, "queueEntryId": p.QueueEntryID, "type": "TURN_SOON"}})
if serr != nil {
    if h.push.IsTokenInvalid(serr) {
        _ = h.subs.Delete(ctx, p.VisitorID, p.EventID)
        _ = h.deliveries.MarkDead(ctx, d.Envelope.MessageID, "TURN_SOON", serr.Error())
        return nil                                   // ack, not retry (AC4.3)
    }
    _ = h.deliveries.BumpAttempt(ctx, d.Envelope.MessageID, "TURN_SOON", serr.Error())
    return serr                                      // FCM transient → retry
}
return h.deliveries.MarkSent(ctx, d.Envelope.MessageID, "TURN_SOON", id)
```

### Acceptance Criteria → disposition
- [ ] AC4.1: `queue.turn.soon` → lookup `(visitorId,eventId)` → FCM; row `TURN_SOON`/`PUSH`.
- [ ] AC4.2: no subscription → `return nil` (ack), no error.
- [ ] AC4.3: `IsTokenInvalid` → delete subscription + `MarkDead` + ack (**no retry, no DLQ**).

### Tests
- [ ] Unit: nil subscription→ack; invalid token→Delete+MarkDead+ack; transient FCM→err returned.

---

## Task 5: Turn Arrived Push (R5)

**Traces to:** Requirement 5, SRS-F-25-01..13, TC-QUE-04

### Files
- `domain/dto/turn_arrived_payload.go` — `EventID, QueueEntryID, VisitorID, ClaimDeadline(time.Time)` + `Validate()`.
- `application/usecase/notify_turn_arrived.go`; dispatch in `TurnHandler.Handle`.

### Added guard (design.md §4.4)
```go
if time.Now().After(p.ClaimDeadline) {
    _ = h.deliveries.MarkExpired(ctx, d.Envelope.MessageID, "TURN_ARRIVED") // sets expires_at
    return nil // ack & drop (AC5.2)
}
```
then identical to Task 4 with `Type="TURN_ARRIVED"`, title `"Giliran Anda
Tiba!"`, and `Data` includes `"claimDeadline": p.ClaimDeadline.Format(time.RFC3339)`.

### Acceptance Criteria → disposition
- [ ] AC5.1: send FCM with `eventId,queueEntryId,type=TURN_ARRIVED,claimDeadline`.
- [ ] AC5.2: `now > claimDeadline` → `MarkExpired` + ack (drop), no send.
- [ ] AC5.3: no subscription / invalid token → same as R4 (ack / delete + ack).

### Tests
- [ ] Unit: deadline passed→MarkExpired+ack (no send); deadline future→send.

---

## Task 6: Push Subscription API (R7)

**Traces to:** Requirement 7, SRS-F-25, `docs/contracts/openapi/notification.yaml`

### Files
- `domain/dto/push_subscription_request.go` — `{EventID, Platform(ANDROID|IOS), PushToken(≤4096)}` + `Validate()`.
- `domain/errors.go` (or per-usecase) — declare service error codes mapped to `apperror`.
- `application/usecase/subscribe_push.go` — `SubscribePush` (derives `visitor_id` from token, encrypts, `Upsert`).
- `application/usecase/unsubscribe_push.go` — `UnsubscribePush` (`Delete`, no-op OK).
- `infrastructure/http/subscription_handler.go` — `Register(mux)` adds `POST /api/v1/push-subscriptions`, `DELETE /api/v1/push-subscriptions/{eventId}`; reads `X-Visitor-Token`; maps errors to `apperror.Envelope`.
- Wire in `internal/delivery/server.go` `NewHandler` (alongside existing health registrar): the `api` verb needs `Need{Postgres:true}` — update `cmd/api.go` if it currently opens nothing.

### Visitor token → `visitor_id`
Reuse the shared verifier pattern (`pkg/token`, same public key issued by User
Service — see `services/user/.../security.go` `Tokens.VerifyAccess`). Confirm
whether the push-subscription endpoints accept a **visitor** token vs an
**access** token; the OpenAPI `visitorHeader` = `X-Visitor-Token`. Add a thin
`IVisitorTokenVerifier` port if a verifier is not already wired into this service.

### Acceptance Criteria → EXACT error codes
- [ ] AC7.1: valid `X-Visitor-Token` + body → **204**, `push_token_enc` stored.
- [ ] AC7.2: existing `(visitor_id,event_id)` → upsert (`ON CONFLICT ... DO UPDATE`), **204**, no duplicate row.
- [ ] AC7.3: `DELETE .../{eventId}` → **204** (no-op when absent; idempotent).
- [ ] AC7.4: invalid field (`pushToken` empty / `platform` ∉ {ANDROID,IOS} / `pushToken` > 4096) → **400** `VALIDATION_ERROR` via `pkg/apperror` `ErrorEnvelope`.
- [ ] AC7.5: missing/invalid `X-Visitor-Token` → **401** (verifier error → `apperror` `NOT_FOUND`/unauthorized mapping per existing convention).
- [ ] (optional) `PUSH_TOKEN_INVALID` → **400** if the token is validated at register time.

### Tests
- [ ] Unit: validation table (empty token, bad platform, oversize token) → 400 `VALIDATION_ERROR`.
- [ ] Integration: subscribe twice → one row (upsert); delete → 204; delete absent → 204.

---

## Task 7: OTP Login Email (R12) — time-sensitive, NO retry

**Traces to:** Requirement 12, SRS-F-27-02, SRS-F-27-12, TC-MSG-05, TC-MSG-06, TC-USR-11

### Files
- `domain/dto/otp_requested_payload.go` — `{EmailEnc(base64→[]byte), CodeEnc(base64→[]byte), ExpiresAt(time.Time), Locale(default "id")}` + `Validate()`.
- `port/outbound/pii_protector.go` (§C.6) + `infrastructure/security/pii_protector.go` (§C.7).
- `assets` template `otp_login` (uses `formatTime`).
- `application/usecase/notify_otp_requested.go`.
- `infrastructure/rabbitmq/otp_handler.go` — `OTPHandler.Handle`.

### Handler flow (design.md §4.5) — EXACT dispositions
```go
func (h *OTPHandler) Handle(ctx context.Context, d rabbitmq.Delivery) error {
    var p dto.OTPRequestedPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decode otp.requested: %w", err)) // AC12.3 → DLQ
    }
    if time.Now().After(p.ExpiresAt) {
        h.log.Warn("OTP expired, skip", "messageId", d.Envelope.MessageID)
        return nil                                                              // AC12.2 → ack & drop
    }
    email, err := h.pii.UnsealEmail(p.EmailEnc)
    if err != nil { return rabbitmq.Permanent(fmt.Errorf("decrypt email: %w", err)) } // AC12.3 → DLQ
    code, err := h.pii.UnsealOTP(p.CodeEnc)
    if err != nil { return rabbitmq.Permanent(fmt.Errorf("decrypt code: %w", err)) }  // AC12.3 → DLQ
    body, err := h.tmpl.Render("otp_login", map[string]any{"Code": code, "ExpiresAt": p.ExpiresAt, "Locale": p.Locale})
    if err != nil { return rabbitmq.Permanent(err) }                            // poison → DLQ
    if _, serr := h.email.Send(ctx, email, "Kode Login Anda", body); serr != nil {
        h.log.Error("OTP email failed", "messageId", d.Envelope.MessageID, "error", serr) // NO code in log (AC12.5)
        return nil                                                              // AC12.4 → ack, NO retry
    }
    return nil
}
```

> OTP is **not** persisted in `deliveries` (schema CHECKs exclude it — see §C.1
> note). No `ClaimDelivery`/`MarkSent` on this path.

### Acceptance Criteria → EXACT disposition
- [ ] AC12.1: `otp.requested` → decrypt `emailEnc`/`codeEnc` (shared `PII_ENC_KEY`, AADs per §C.7), render `otp_login`, send.
- [ ] AC12.2: `now > expiresAt` → `return nil` (ack, drop).
- [ ] AC12.3: decode or `UnsealEmail`/`UnsealOTP` fail (`crypto.ErrDecrypt`) → `rabbitmq.Permanent` → **DLQ `notification.otp.dlq`** (TC-MSG-06). **No retry.**
- [ ] AC12.4: SMTP fail → log + `return nil` (ack). **No retry** (`notification.otp` registered without reliance on retry; even if retry queues are declared, the handler never returns a transient error).
- [ ] AC12.5: no OTP code / email plaintext in logs — only `messageId` (TC-USR-11).
- [ ] AC12.6: delivered within ~5s of publish (no backoff on this path) (TC-MSG-05).

### Tests
- [ ] Unit: expired→ack(no send); bad ciphertext→`IsPermanent`==true; SMTP err→ack (no error returned); assert log never contains the code.
- [ ] Integration: User Service publishes `otp.requested` → email in Mailpit ≤5s (TC-MSG-05); corrupt `codeEnc` → DLQ (TC-MSG-06).

---

## Task 8: Retry & DLQ behavior (R9) — verify, mostly free from `pkg/rabbitmq`

**Traces to:** Requirement 9, SRS-Q-REL-01, TC-MSG-01, TC-MSG-02, TC-MSG-04

No new transport code — `Consume`/`handle`/`Decide` do it. This task is about
**correct error classification** in the handlers and **integration proof**.

### Classification contract (design.md §12.2) — handlers MUST obey
| Condition | Return | Outcome |
|-----------|--------|---------|
| decode payload / `Validate()` fail / render fail / unknown routing key / decrypt fail | `rabbitmq.Permanent(err)` | DLQ, no retry |
| SMTP / FCM / DB unavailable | raw `err` (non-permanent) | retry `5s→30s→2m`, then DLQ at `attempt>3` |
| FCM token invalid | `nil` (after Delete+MarkDead) | ack |
| `claimDeadline` passed / `expiresAt` passed / duplicate claim / no subscription | `nil` | ack |
| OTP SMTP fail | `nil` | ack (no retry) |

### Acceptance Criteria
- [ ] AC9.1: transient → republish to `<queue>.retry.<delay>` with `delays[attempt-1]`.
- [ ] AC9.2: `attempt > RABBITMQ_MAX_RETRIES` (3) → `<queue>.dlq`.
- [ ] AC9.3: `Permanent` → straight to DLQ (no retry burn).
- [ ] AC9.4: first retry carries `x-rbt-attempt=2`, then ack on success (TC-MSG-04).

### Tests
- [ ] Unit: assert each handler returns the right class (`IsPermanent` true/false) per row above.
- [ ] Integration (real RabbitMQ): SMTP down → message in retry queue → up → SENT (TC-MSG-01); permanent → DLQ (TC-MSG-02); `x-rbt-attempt` increments (TC-MSG-04).

---

## Task 9: Privacy enforcement (R11) — cross-cutting

**Traces to:** Requirement 11, security-privacy.md

### Acceptance Criteria
- [ ] AC11.2: handlers reject payloads carrying NIK/token plaintext (schema only has `attendeeNikMasked`, `ticketLinkToken`); `Validate()` enforces required fields.
- [ ] AC11.3: `deliveries.recipient_hash` written via `pii.HashEmail`/`HashVisitor` (HMAC) — never plaintext email/visitor.
- [ ] AC11.4: `slog` statements log only `messageId`, `bookingId`, `queueEntryId`, `correlationId` — never OTP code, email, or token.

### Tests
- [ ] Unit: grep-style assertion that handler log fields exclude PII; `recipient_hash` is bytes, not the email.

---

## Task 10: Health Endpoint (R10) — ✅ IMPLEMENTED

`GET /health` deep-checks Postgres + RabbitMQ within `HEALTH_TIMEOUT` (200 UP /
503 DOWN). No work. Verify: `curl localhost:8084/health`.

---

## Final Verification (from repo root unless noted)

```bash
cd services/notification && go build ./... && go test -race ./... && golangci-lint run
cd ../.. 
make build
make test
make mock              # if any port added/changed
make lint              # depguard enforces hexagonal layering
make test-integration  # Postgres + RabbitMQ + Mailpit from `make infra`
make cover             # ≥ 80% (cmd/, internal/delivery/, internal/config/, _mock/ excluded)
make ci                # MUST be green before PR
```

Manual smoke:
```bash
go run ./cmd migrate up
go run ./cmd worker &   # consumers: booking.*, queue.turn.*, otp.requested
go run ./cmd api &      # push-subscription API + /health
# publish a test booking.paid / otp.requested → check Mailpit http://localhost:8025
# DLQ check: rabbitmqadmin get queue=notification.booking.events.dlq
```

---

## Build order (dependencies)

```
0a deliveries repo + idempotency ─┐
0b config/env (SMTP,FCM,PII)      ─┤→ 1 eticket+SMTP ─→ 2 expired ─→ 3 review
                                   │→ 4 turn.soon (+FCM) ─→ 5 turn.arrived
                                   │→ 7 OTP (+PII adapter)
                                   └→ 6 push-subscription API (independent, api verb)
8 retry/DLQ = integration proof over 1–7 ; 9 privacy = cross-cutting audit ; 10 done
```

---

## Test Case Alignment

| Task | Requirement | Test Cases | Error/DLQ target |
|------|-------------|------------|------------------|
| 0a | R6, R8 | TC-MSG-03 | — (ack on conflict) |
| 0b | R11 | — | fail-fast exit |
| 1 | R1 | TC-TKT-01, TC-TKT-02, TC-MSG-03 | `notification.booking.events.dlq` |
| 2 | R2 | TC-MSG-01 | retry queue → dlq |
| 3 | R3 | TC-MSG-01 | retry queue → dlq |
| 4 | R4 | TC-QUE-04 | ack (invalid token) |
| 5 | R5 | TC-QUE-04 | ack (deadline) |
| 6 | R7 | — | 400 `VALIDATION_ERROR` |
| 7 | R12 | TC-MSG-05, TC-MSG-06, TC-USR-11 | `notification.otp.dlq` |
| 8 | R9 | TC-MSG-01, TC-MSG-02, TC-MSG-04 | `<queue>.dlq` |
| 9 | R11 | — | — |
| 10 | R10 | server_test.go | — (implemented) |
