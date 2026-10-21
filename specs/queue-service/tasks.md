# Tasks — Queue Service

| Field | Value |
|-------|-------|
| Service | Queue Service |
| Port | 8081 |
| Module path | `github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue` |
| Spec | `specs/queue-service/{requirements,design}.md` |
| Status | Boilerplate (health, config, clients, migrations) implemented; business logic is TARGET |

Grounding: **kode & kontrak menang**. Every signature, pkg helper, and pattern below is taken
from the actual tree (`services/queue`, `services/user`, `pkg/`) as of 2026-10-20. Follow the
hexagonal layering from `.kiro/steering/structure.md` (depguard-enforced): interfaces `I*` only in
`port/`, constructors accept interfaces & return concrete structs, domain imports stdlib only, Lua
only for atomicity (`.kiro/steering/code-principles.md`).

---

## Conventions (copy from existing code — do not reinvent)

- **Package layout per domain** (mirror `services/user/internal/user/`):
  - `domain/{entity,valueobject,dto,mapper}` — stdlib only.
  - `port/inbound` — usecase interfaces + `errors.go` (apperror codes). `port/outbound` — driven ports.
  - `application/usecase` — one usecase per file; `New*` accepts a `Deps`/`Settings` struct.
  - `infrastructure/{http,valkey,postgres,rabbitmq,bookingclient,security}` — adapters.
- **HTTP handler pattern** (see `services/user/.../http/auth_handler.go`):
  - `type XHandler struct { uc inbound.IXxx; ... }`, `NewXHandler(...)`, `Register(mux *stdhttp.ServeMux)`.
  - Decode: `httpx.DecodeJSON(w, r, &req, 0)`. Success: `httpx.WriteData(w, r, status, dto)`.
    Error: `httpx.WriteError(w, r, err)`. No-cache: `w.Header().Set("Cache-Control","no-store")`.
  - Path param: `r.PathValue("eventId")`. Client IP: `httpx.ClientIP(r, trustProxy)`.
- **Errors**: usecases return `*apperror.Error`; define them in `port/inbound/errors.go` exactly like
  `services/user/.../port/inbound/errors.go`. `httpx.WriteError` → `apperror.From` → envelope
  `{meta:{code,message,requestId,errorCode,details},data:null}`. Reuse shared codes from
  `pkg/apperror` (`CodeValidationFailed`, `CodeNotFound`, `CodeRateLimited`, `CodeDependencyUnavailable`).
- **Valkey adapter** (see `services/user/.../valkey/otp_store.go`): take `vk valkey.Client` +
  `Keyer` (`clients.VK.VK()`, `clients.VK`); hash tag `{...}` for co-slotting; `vk.IsValkeyNil(err)`
  for misses; `s.c.Do(ctx, s.c.B()....Build())` / `DoMulti`.
- **Lua**: embed scripts from `services/queue/assets/lua/*.lua` with `//go:embed`; load once via
  `valkey.NewLuaScript(src)` and run `script.Exec(ctx, client, keys, args)` (valkey-go `NewLuaScript`
  does EVALSHA with EVAL fallback). Map the returned `{STATUS, ...}` array to domain errors in Go.
- **RabbitMQ publish** (see `services/user/.../rabbitmq/otp_publisher.go`): wrap a
  `*pkgrabbitmq.Publisher`; build `pkgrabbitmq.NewEnvelope(eventType, "queue", aggregateID, corrID, payload, at)`
  then `pub.Publish(ctx, routingKey, env)`.
- **RabbitMQ consume** (`pkg/rabbitmq/consumer.go`): `conn.Consume(ctx, Subscription{Queue, RoutingKeys, Handler}, logger)`.
  Handler is `func(ctx, Delivery) error`; return `rabbitmq.Permanent(err)` for poison messages.
- **Tokens** (`pkg/token` + `services/user/.../security/security.go`): `token.NewSigner(priv, kid)` /
  `token.NewVerifier(map[kid]pub, issuer, audience, leeway)`; verify user token with `Verify` then read
  `Claims.HasNIK` / `Claims.NIKHash`. Admission/visitor/queue tokens are signed with the same `Signer`.
- **PII** (`pkg/crypto` + `security.PII`): `HashNIK`, `SealEmail`/`UnsealEmail`, keys via `crypto.ParseKey`.
- **Wiring**: add a `newQueueModule(cfg, clients, logger) (registrar, error)` in
  `internal/delivery/queue_module.go` (mirror `services/user/.../delivery/user_module.go`), register it
  in `NewHandler` (`server.go`) next to the health handler, and register worker jobs in `jobs(clients)`
  (`worker.go`).

---

## Pre-requisites (verify boilerplate once)

```bash
cd services/queue
go build ./...                                   # boilerplate compiles
make infra keys                                  # from repo root: Valkey/PG/RabbitMQ + EdDSA keys
cp .env.example .env
go run ./cmd migrate up                          # applies 0001_init, 0002_account_ref
go run ./cmd api & curl -fsS localhost:8081/health   # 200 UP
```

---

## Task 0: Config, keys & module scaffold [TODO]

**Traces to:** R2.13, R6.7, R10.2, design §10, §12

### What exists
- `internal/config/{config.go,load.go}` — `App/HTTP/Worker/Postgres/Valkey/RabbitMQ` loaded & validated (`errors.Join`, fail-fast).
- `internal/delivery/{server.go,worker.go,clients.go,serve.go}` — composition root; `NewHandler` mounts only health; `jobs()` returns `nil`.
- `internal/delivery/clients.go` — `Clients{PG,VK,MQ}`, `NewClients`, `Pingers()`, `Close()`.

### What to implement
**File:** `internal/config/queue.go` (new — mirror `services/user/internal/config/user.go`)
```go
type Token struct { Issuer, Audience, KeyID, PrivateKeyFile, PublicKeyFile string; AdmissionTTL, VisitorTTL, QueueTTL time.Duration }
type PII   struct { EncKeyHex, HashKeyHex string } // PII_ENC_KEY (email AES-GCM), PII_HMAC_KEY (nik/email HMAC — MUST match User)
type Booking struct { BaseURL string; Timeout time.Duration }
type RateLimit struct { JoinPerEmailWindow, JoinPerIPWindow, MePerVisitorWindow time.Duration; JoinPerEmail, JoinPerIP, MePerVisitor int }
type WorkerTuning struct { PromotorInterval, SweeperInterval, StatusInterval time.Duration; PromoteBatchMax int }
func (Token) Validate() error; func (PII) Validate() error; func (Booking) Validate() error
```
Add these to `Config` and call their `Validate()` inside `Config.Validate()`.
**Env added** (`.env.example` + `load.go`): `BOOKING_SERVICE_URL`, `BOOKING_TIMEOUT`,
`TOKEN_ISSUER`, `TOKEN_AUDIENCE`, `TOKEN_KEY_ID`, `TOKEN_PRIVATE_KEY_FILE`, `TOKEN_PUBLIC_KEY_FILE`,
`TOKEN_ADMISSION_TTL=60s`, `TOKEN_VISITOR_TTL`, `PII_ENC_KEY`, `PII_HMAC_KEY`,
`QUEUE_PROMOTOR_INTERVAL_MS=100`, `QUEUE_SWEEPER_INTERVAL_MS=10000`, `QUEUE_PROMOTE_BATCH_MAX=200`,
`RATE_LIMIT_JOIN_PER_EMAIL`, `RATE_LIMIT_JOIN_PER_IP`, `RATE_LIMIT_ME_PER_VISITOR`.

**File:** `internal/delivery/queue_module.go` (new — mirror `user_module.go`)
```go
type registrar interface{ Register(*http.ServeMux) }
func newQueueModule(cfg config.Config, clients *Clients, logger *slog.Logger) (registrar, error)
```
Builds: `token.Signer`/`Verifier`, `security.Hasher`/`Sealer` (via `pkg/crypto`), `valkey.QueueStore`,
`valkey.RateLimiter`, `postgres.EventSettings`, `postgres.EntryAudit`, `rabbitmq.EventPublisher`,
`bookingclient.Client`, then the usecases and the HTTP handlers; returns a composite registrar that
calls each handler's `Register(mux)`.

**File:** `internal/delivery/server.go` (modify) — after `health`:
```go
mod, err := newQueueModule(cfg, clients, logger)
if err != nil { return nil, err }
mod.Register(mux)
```

**Uses:** `pkg/token`, `pkg/crypto`, `pkg/clock`, existing `config` loader.
**Pattern:** `services/user/internal/config/{user.go,load.go}`, `services/user/internal/delivery/{user_module.go,server.go}`.

### Acceptance Criteria
- [ ] AC10.2: missing/invalid `TOKEN_*`, `PII_*`, `BOOKING_*` → start fails reporting all errors at once (`errors.Join`).
- [ ] `go build ./...` green with new config fields wired.

### Tests
```go
func TestLoad_QueueExtras        // in internal/config/load_test.go: new env parsed & defaulted
func TestConfigValidate_TokenPII // missing keys aggregate into one error
```

### Verify
```bash
cd services/queue && go build ./... && go test ./internal/config/... -race
```

---

## Task 1: Domain model — Entry, value objects, status machine [TODO]

**Traces to:** R2, R6, design §3.2, §5

### What exists
- `domain/entity/health.go`, `domain/valueobject/dependency_name.go` — health-path only (reference style).
- `internal/shared/entity/validation_error.go` — shared `ValidationError`.

### What to implement
**File:** `domain/valueobject/seq.go`
```go
type Seq int64
func NewSeq(v int64) (Seq, error) // > 0
func (s Seq) Int64() int64
```
**File:** `domain/valueobject/status.go`
```go
type Status string
const ( StatusWaiting Status="WAITING"; StatusAdmitted="ADMITTED"; StatusClaimed="CLAIMED"
        StatusMissed="MISSED"; StatusLeft="LEFT"; StatusSoldOut="SOLD_OUT"; StatusClosed="CLOSED"
        StatusCompleted="COMPLETED"; StatusEnded="ENDED"; StatusRejected="REJECTED" )
func (Status) IsTerminal() bool            // MISSED,LEFT,SOLD_OUT,CLOSED,COMPLETED,ENDED,REJECTED
func (Status) CanTransitionTo(Status) bool // enforce §5 state machine
```
**File:** `domain/valueobject/zone.go`
```go
type Zone string
const ( ZoneGuaranteed Zone="GUARANTEED"; ZonePossible="POSSIBLE"; ZoneNotEnough="NOT_ENOUGH" )
func ZoneFor(seq, guaranteedUntil, possibleUntil Seq) Zone
```
**File:** `domain/valueobject/buyer_nik_hash.go`
```go
type BuyerNikHash []byte
func (BuyerNikHash) Hex() string
var nikPattern = regexp.MustCompile(`^[0-9]{16}$`)
func ValidateNIKFormat(nik string) error // → entity.ErrNikInvalid sentinel
```
**File:** `domain/valueobject/visitor_id.go`, `domain/valueobject/entry_id.go` — uuid wrappers + `New*`.
**File:** `domain/entity/entry.go`
```go
type Entry struct { ID, VisitorID, AccountID string; Seq valueobject.Seq; BuyerNikHash []byte
                    EmailEnc []byte; Status valueobject.Status; AllocationID string
                    JoinedAtMs, AdmittedAtMs, ClaimedAtMs, ClaimDeadlineMs int64 }
func (e Entry) ExactPosition(servingSeq int64, removedAhead int64) int64 // seq-serving-removedAhead
```
**File:** `domain/entity/event_settings.go` — struct mirroring the `event_settings` columns (§3.3).
**File:** `domain/entity/errors.go` — domain sentinels: `ErrNikInvalid, ErrNikRequired, ErrEmailMismatch` (used by valueobject/dto validation).

**Uses:** stdlib only (`regexp`, `errors`).
**Pattern:** `services/user/.../domain/valueobject/{nik.go,email.go}`, `.../domain/entity/account.go`.

### Acceptance Criteria
- [ ] Status machine rejects illegal transitions (e.g. `CLAIMED→WAITING` false; `ADMITTED→CLAIMED` true).
- [ ] `ValidateNIKFormat` rejects non-16-digit → maps to `NIK_INVALID` (422) later.
- [ ] `ExactPosition` = `seq - servingSeq - removedAhead`.

### Tests
```go
func TestStatus_CanTransitionTo   // table-driven over §5 matrix
func TestStatus_IsTerminal
func TestZoneFor                  // guaranteed/possible/not-enough boundaries (TC-QUE-10/11)
func TestValidateNIKFormat        // valid 16-digit, letters, wrong length
func TestEntry_ExactPosition
```

### Verify
```bash
go test ./internal/queue/domain/... -race
```

---

## Task 2: Outbound ports + mocks [TODO]

**Traces to:** R2, R4–R9, design §1

### What exists
- `port/outbound/dependency_checker.go`, `port/inbound/health_usecase.go` — health only, with `//go:generate mockgen` headers.
- `_mock/queue/port/...` — generated health mocks (confirms `make mock` pipeline works).

### What to implement (each file carries a `//go:generate go tool mockgen -source=... -destination=../../../../_mock/queue/port/outbound/<name>_mock.go -package=mock_outbound` header)

**File:** `port/outbound/queue_store.go`
```go
type IQueueStore interface {
  Join(ctx, p JoinParams) (JoinResult, error)                 // EVALSHA join.lua
  Promote(ctx, eventID string, cands []PromoteCandidate, capacity int, nowMs int64) ([]string, error) // promote.lua
  Claim(ctx, eventID, entryID, visitorID string, nowMs int64) (ClaimResult, error)                    // claim.lua
  Leave(ctx, eventID, entryID, visitorID string, nowMs int64) (wasActive bool, err error)             // leave.lua
  EndEntry(ctx, eventID, entryID, outcome string, nowMs int64) (final string, err error)              // end_entry.lua
  GetEntry(ctx, eventID, entryID string) (entity.Entry, bool, error)                                  // HGETALL
  FindByVisitor(ctx, eventID, visitorID string) (entity.Entry, bool, error)
  ActiveCount(ctx, eventID string) (int, error)                                                       // ZCARD active
  TopWaiting(ctx, eventID string, n int) ([]string, error)                                            // ZRANGEBYSCORE waiting 0 +inf LIMIT 0 n
  ExpiredActive(ctx, eventID string, nowMs int64) ([]string, error)                                   // ZRANGEBYSCORE active 0 now
  MarkMissed(ctx, eventID, entryID string, nowMs int64) (bool, error)
  AddQuotaExhausted(ctx, eventID, nikHash string) error                                               // SADD
  SetSale(ctx, eventID, saleState string, soldOut bool) error                                         // HSET sale
  GetSale(ctx, eventID string) (SaleState, error)
  SetStatusDoc(ctx, eventID string, doc []byte, ttl time.Duration) error                              // SET status EX 1
  GetStatusDoc(ctx, eventID string) ([]byte, bool, error)
}
```
**File:** `port/outbound/event_settings_repo.go`
```go
type IEventSettingsRepo interface {
  Get(ctx, eventID string) (entity.EventSettings, bool, error)
  ListEventIDs(ctx context.Context) ([]string, error) // worker iterates events
}
```
**File:** `port/outbound/entry_audit_repo.go`
```go
type IEntryAuditRepo interface { Append(ctx context.Context, e AuditRecord) error }
```
**File:** `port/outbound/booking_client.go`
```go
type IBookingClient interface {
  ReserveBatch(ctx, req AllocationBatchRequest) (AllocationBatchResponse, error) // POST /internal/v1/allocations:batch
  Supply(ctx, eventID string) (SupplySnapshot, error)                           // GET /internal/v1/events/{id}/supply
}
```
**File:** `port/outbound/event_publisher.go`
```go
type IEventPublisher interface {
  PublishTurnSoon(ctx, eventID, queueEntryID, visitorID string) error
  PublishTurnArrived(ctx, eventID, queueEntryID, visitorID string, claimDeadline time.Time) error
}
```
**File:** `port/outbound/rate_limiter.go`
```go
type IRateLimiter interface { Allow(ctx, key string, limit int, window time.Duration) (bool, error) }
```
**File:** `port/outbound/token_issuer.go`
```go
type ITokenIssuer interface {
  IssueAdmission(entryID, visitorID string, now time.Time) (string, time.Time, error) // one-time, TTL 60s
  IssueVisitor(visitorID string, now time.Time) (string, error)
  IssueQueue(entryID, visitorID string, now time.Time) (string, error)
}
type IUserTokenVerifier interface { Verify(bearer string) (UserIdentity, error) } // {AccountID, HasNIK, NIKHash, Email}
```
**File:** `port/outbound/pii.go`
```go
type IPIIProtector interface { HashNIK(nik string) []byte; HashEmail(email string) []byte
                               SealEmail(email string) ([]byte, error); UnsealEmail([]byte) (string, error) }
```

**Uses:** `entity`, stdlib, `time`.
**Pattern:** `services/user/.../port/outbound/{services.go,repositories.go}`.

### Acceptance Criteria
- [ ] `make mock` regenerates `_mock/queue/port/outbound/*_mock.go` without error.
- [ ] `go build ./...` green.

### Verify
```bash
make mock && go build ./...
```

---

## Task 3: Lua scripts (atomicity) [TODO]

**Traces to:** R2.7, R6.1/6.7, R7.1, R8.1, design §4

### What exists
- `assets/README.md` — "Kosong di skeleton." No `assets/lua/` dir yet.

### What to implement
Create `assets/lua/{join,promote,claim,leave,end_entry}.lua` with the **exact** contents from design §4.1–4.5
(reproduced verbatim below), and `internal/queue/infrastructure/valkey/scripts.go`:
```go
//go:embed ... (actually embed from assets via a small embed.go in assets/lua or infrastructure/valkey)
var ( joinScript = valkey.NewLuaScript(joinSrc); promoteScript = valkey.NewLuaScript(promoteSrc); ... )
```
All KEYS of one script use hash tag `{e:<eventId>}`. Go builds KEYS/ARGV, runs `script.Exec`, maps result.

**`assets/lua/join.lua`**
```lua
-- KEYS[1]=seq KEYS[2]=waiting KEYS[3]=entry:<id> KEYS[4]=nik:<h> KEYS[5]=visitor:<v>
-- KEYS[6]=email:<eh> KEYS[7]=quota_exhausted
-- ARGV: 1=entryId 2=visitorId 3=nikHash 4=emailEnc 5=accountId 6=nowMs
--       7=oneActivePerVisitor("1"/"0") 8=oneActivePerEmail("1"/"0")
if redis.call('SISMEMBER', KEYS[7], ARGV[3]) == 1 then return {'ERR','BUYER_QUOTA_EXHAUSTED'} end
local nik = redis.call('GET', KEYS[4]); if nik then return {'EXISTING','NIK',nik} end
if ARGV[7] == '1' then local v = redis.call('GET', KEYS[5]); if v then return {'EXISTING','VISITOR',v} end end
if ARGV[8] == '1' then local em = redis.call('GET', KEYS[6]); if em then return {'EXISTING','EMAIL',em} end end
local seq = redis.call('INCR', KEYS[1])
redis.call('HSET', KEYS[3], 'seq',seq,'visitorId',ARGV[2],'buyerNikHash',ARGV[3],
  'emailEnc',ARGV[4],'accountId',ARGV[5],'status','WAITING','joinedAtMs',ARGV[6])
redis.call('ZADD', KEYS[2], seq, ARGV[1])
redis.call('SET', KEYS[4], ARGV[1])
if ARGV[7] == '1' then redis.call('SET', KEYS[5], ARGV[1]) end
if ARGV[8] == '1' then redis.call('SET', KEYS[6], ARGV[1]) end
return {'OK', seq, 'WAITING'}
```
**`assets/lua/promote.lua`**, **`claim.lua`**, **`leave.lua`**, **`end_entry.lua`** — verbatim from design §4.2–4.5.

**Go → domain mapping (document in `scripts.go`):**
| Script return | Domain |
|---|---|
| `OK,seq,WAITING` | 201 WAITING |
| `EXISTING,NIK,<id>` | `ErrNikInQueue` (409) or idempotent return when `Idempotency-Key` matches |
| `EXISTING,VISITOR,<id>` | `ErrVisitorInQueue` (409) |
| `EXISTING,EMAIL,<id>` | `ErrEmailInQueue` (409) |
| `ERR,BUYER_QUOTA_EXHAUSTED` | `ErrQuotaExhausted` (409) |
| `claim OK,CLAIMED`/`OK,ALREADY` | 200 (issue admission token) |
| `claim ERR,TURN_MISSED` | `ErrTurnMissed` (410) |
| `claim ERR,NOT_FOUND` | `ErrEntryNotFound` (404) |
| any Valkey transport error | `ErrDependencyUnavailable` (503) — fail closed |

**Uses:** `github.com/valkey-io/valkey-go` (`NewLuaScript`), `embed`.
**Pattern:** valkey-go script API; embed pattern like `deployments/migrations/embed.go`.

### Acceptance Criteria
- [ ] AC2.7: join constraint checks + `INCR seq` are a single atomic Lua call.
- [ ] Scripts load once and run via EVALSHA (valkey-go `NewLuaScript`).

### Tests (integration — real Valkey, build tag `integration`)
```go
func TestJoinLua_AtomicConstraints  // nik/visitor/email duplicate → EXISTING
func TestClaimLua_DeadlinePast      // now>=deadline → ERR,TURN_MISSED + status MISSED
func TestLeaveLua_ReleasesSlot      // ZREM active returns wasActive=1, constraint keys deleted
func TestEndEntryLua_Idempotent     // second call on terminal → IGNORED
```

### Verify
```bash
go test -tags integration -race ./internal/queue/infrastructure/valkey/...
```

---

## Task 4: QueueStore + RateLimiter (Valkey adapters) [TODO]

**Traces to:** R2.7, R2.13, R3, design §3.1, §4

### What exists
- `services/user/.../valkey/otp_store.go` — the exact adapter pattern (`Keyer`, hash tags, `Do`/`DoMulti`, `IsValkeyNil`).
- `services/user/.../valkey/RateLimiter` — fixed-window limiter to copy verbatim.

### What to implement
**File:** `internal/queue/infrastructure/valkey/queue_store.go`
```go
type Keyer interface{ Key(k string) string }
type QueueStore struct { c vk.Client; keyer Keyer; scripts *scripts; now func() time.Time }
func NewQueueStore(c vk.Client, keyer Keyer, now func() time.Time) *QueueStore
// implements outbound.IQueueStore; per-event key builder returns the `q:{e:<id>}:...` family (§3.1)
func (s *QueueStore) keys(eventID string) queueKeys
```
Transport errors wrapped as `apperror` `DEPENDENCY_UNAVAILABLE` (503) at the usecase boundary (fail closed, R2.13).
**File:** `internal/queue/infrastructure/valkey/rate_limiter.go` — copy `user`'s `RateLimiter` (`Allow`, fixed window).

**Uses:** `pkg/valkey` (`clients.VK.VK()`, `clients.VK` as Keyer), `github.com/valkey-io/valkey-go`.
**Pattern:** `services/user/.../valkey/otp_store.go` verbatim for structure.

### Acceptance Criteria
- [ ] AC2.6: `seq` strictly monotonic, no gaps (INCR in Lua).
- [ ] AC3.3: rate limit counters live in Valkey (shared across instances).

### Tests
```go
func TestQueueStore_Join_Integration   // (integration) OK/EXISTING paths
func TestQueueStore_Promote_Integration
func TestRateLimiter_Allow             // unit: window rollover
```

### Verify
```bash
go test -tags integration -race ./internal/queue/infrastructure/valkey/...
```

---

## Task 5: Postgres repos — event_settings & entry_audit [TODO]

**Traces to:** R2 (settings), R6.10/R8 (audit), design §3.3

### What exists
- Migrations `0001_init.up.sql` (`event_settings`, `entry_audit`) + `0002_account_ref.up.sql` (`account_id`) — **applied**.
- `services/user/.../postgres/repositories.go` — pgxpool repo pattern (`NewXxx(pool)`, `pkgpg.IsCode`, `WithTx`).

### What to implement
**File:** `internal/queue/infrastructure/postgres/event_settings_repo.go`
```go
type EventSettings struct{ pool *pgxpool.Pool }
func NewEventSettings(pool *pgxpool.Pool) *EventSettings
func (r *EventSettings) Get(ctx, eventID string) (entity.EventSettings, bool, error) // SELECT ... WHERE event_id=$1::uuid
func (r *EventSettings) ListEventIDs(ctx context.Context) ([]string, error)
```
**File:** `internal/queue/infrastructure/postgres/entry_audit_repo.go`
```go
type EntryAudit struct{ pool *pgxpool.Pool }
func NewEntryAudit(pool *pgxpool.Pool) *EntryAudit
// INSERT INTO entry_audit (event_id,entry_id,seq,buyer_nik_hash,action,occurred_at,metadata,account_id)
func (r *EntryAudit) Append(ctx context.Context, e outbound.AuditRecord) error
```
`action` must be one of the CHECK set `{JOINED,ADMITTED,CLAIMED,MISSED,LEFT,COMPLETED,ENDED,SOLD_OUT,CLOSED,REJECTED}`.

**Uses:** `pkg/postgres` (`clients.PG.Pool()`, `pkgpg.IsCode`), `jackc/pgx/v5`.
**Pattern:** `services/user/.../postgres/repositories.go`.

### Acceptance Criteria
- [ ] `event_settings.Get` returns `(_, false, nil)` on `pgx.ErrNoRows` / invalid uuid (`22P02`).
- [ ] `entry_audit.Append` is append-only; invalid `action` rejected by the DB CHECK.

### Tests
```go
func TestEventSettings_Get_Integration   // seed row → fields match
func TestEntryAudit_Append_Integration   // row persisted with metadata jsonb
```

### Verify
```bash
go test -tags integration -race ./internal/queue/infrastructure/postgres/...
```

---

## Task 6: Security adapters — tokens, user-token verify, PII [TODO]

**Traces to:** R1.3/1.4, R6.7, design §12

### What exists
- `pkg/token` — `Signer`/`Verifier`/`Claims` (reads `HasNIK`, `NIKHash`).
- `services/user/.../security/security.go` — `PII` (HMAC/AES via `pkg/crypto`) and `Tokens` issuer/verifier to mirror.

### What to implement
**File:** `internal/queue/infrastructure/security/tokens.go`
```go
type Tokens struct { signer *token.Signer; issuer, audience string; admissionTTL, visitorTTL, queueTTL time.Duration; now func() time.Time }
func NewTokens(signer *token.Signer, issuer, audience string, admTTL, visTTL, qTTL time.Duration, now func() time.Time) *Tokens
func (t *Tokens) IssueAdmission(entryID, visitorID string, now time.Time) (string, time.Time, error) // jti=unique → one-time; exp=now+60s
func (t *Tokens) IssueVisitor(visitorID string, now time.Time) (string, error)
func (t *Tokens) IssueQueue(entryID, visitorID string, now time.Time) (string, error)
```
**File:** `internal/queue/infrastructure/security/user_verifier.go`
```go
type UserVerifier struct { verifier *token.Verifier; now func() time.Time }
func (v *UserVerifier) Verify(bearer string) (outbound.UserIdentity, error) // token.Verify → {Subject, HasNIK, NIKHash, Email}
```
**File:** `internal/queue/infrastructure/security/pii.go` — wrap `crypto.Hasher`/`crypto.Sealer`:
`HashNIK("nik:"+nik)`, `HashEmail("email:"+email)`, `SealEmail`/`UnsealEmail` (AAD `queue.entry.email`).
Note: `HashNIK` **must** use the same `PII_HMAC_KEY` as User (`security.PII.HashNIK` prefixes `"nik:"`) so hashes match.

**Uses:** `pkg/token`, `pkg/crypto`.
**Pattern:** `services/user/.../security/security.go`.

### Acceptance Criteria
- [ ] AC6.7: admission token EdDSA, unique `jti` (one-time), `exp = now + 60s`.
- [ ] AC1.3: user bearer with `HasNIK==true` surfaces `NIKHash` so usecase ignores `body.buyerNik`.

### Tests
```go
func TestTokens_IssueAdmission_TTL60   // exp-iat == 60s, unique jti
func TestUserVerifier_Verify           // valid token → identity; expired/bad → error
func TestPII_HashNIK_MatchesUser       // same key+input as user.security produces same hash
```

### Verify
```bash
go test ./internal/queue/infrastructure/security/... -race
```

---

## Task 7: Booking client (outbound HTTP) [TODO]

**Traces to:** R6.1–6.5, design §2.7

### What exists
- `pkg/resilience/breaker.go` — circuit breaker for outbound calls.
- Contract `docs/contracts/openapi/internal-booking.yaml`.

### What to implement
**File:** `internal/queue/infrastructure/bookingclient/client.go`
```go
type Client struct { base string; http *stdhttp.Client; breaker *resilience.Breaker }
func NewClient(baseURL string, timeout time.Duration) *Client
func (c *Client) ReserveBatch(ctx, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) // POST /internal/v1/allocations:batch
func (c *Client) Supply(ctx, eventID string) (outbound.SupplySnapshot, error)                                    // GET /internal/v1/events/{id}/supply
```
`AllocationResult.Status ∈ {RESERVED,NO_SUPPLY_NOW,SOLD_OUT,QUOTA_EXHAUSTED}`. Non-2xx / timeout →
wrap as `DEPENDENCY_UNAVAILABLE` (fail closed). Supply snapshot cached 1s in Valkey by the caller.

**Uses:** `net/http`, `pkg/resilience`.
**Pattern:** standard JSON client; breaker per `[[circuit-breakers]]` (consult engineering-standards if tuning).

### Acceptance Criteria
- [ ] Each `queueEntryId` in the response maps to exactly one outcome handled by the Promotor.
- [ ] Transport failure → `DEPENDENCY_UNAVAILABLE`, not silent.

### Tests
```go
func TestBookingClient_ReserveBatch   // httptest server: RESERVED/NO_SUPPLY_NOW/SOLD_OUT/QUOTA_EXHAUSTED decoded
func TestBookingClient_Supply
func TestBookingClient_5xx_Unavailable
```

### Verify
```bash
go test ./internal/queue/infrastructure/bookingclient/... -race
```

---

## Task 8: Join Queue + Direct Admission (R2 + R1) [TODO]

**Traces to:** R1, R2, SRS-F-04-01..10, TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08, TC-GST-01..05, TC-USR-09/09a

### What exists
- Lua `join.lua`/`promote.lua` (Task 3), `QueueStore` (Task 4), tokens/PII (Task 6), booking client (Task 7).
- `services/user/.../http/auth_handler.go` — handler+decode+envelope pattern.

### What to implement
**File:** `domain/dto/join.go`
```go
type JoinRequest struct { BuyerNik, Email, EmailConfirmation string; ConsentNik bool } // json tags per queue.yaml
type QueueEntryResponse struct { EntryID string `json:"entryId"`; Seq int64 `json:"seq"`; Status string `json:"status"`
  QueueToken, VisitorToken, StatusURL, ClaimDeadline, JoinedAt, ServerTime string }   // omitempty on optionals
func (r JoinRequest) Validate(nikRequired bool) error // order: email→emailConfirmation→consent→nik
```
**File:** `port/inbound/join_usecase.go`
```go
type IJoinQueue interface { Join(ctx, eventID string, req dto.JoinRequest, auth AuthContext, client ClientInfo) (dto.QueueEntryResponse, error) }
type AuthContext struct { AccountID, NIKHash string; HasNIK bool; VisitorID string; IsMobile bool }
type ClientInfo struct { IP, IdempotencyKey string }
```
**File:** `port/inbound/errors.go` — define every queue error code + status exactly:
```go
ErrValidation=VALIDATION_ERROR(400); ErrNikInvalid=NIK_INVALID(422); ErrNikRequired=NIK_REQUIRED(422)
ErrEmailMismatch=EMAIL_MISMATCH(422); ErrNikInQueue=NIK_ALREADY_IN_QUEUE(409)
ErrVisitorInQueue=VISITOR_ALREADY_IN_QUEUE(409); ErrEmailInQueue=EMAIL_ALREADY_IN_QUEUE(409)
ErrQuotaExhausted=BUYER_QUOTA_EXHAUSTED(409); ErrNotOnSale=EVENT_NOT_ON_SALE(409)
ErrSoldOut=EVENT_SOLD_OUT(410); ErrTurnMissed=TURN_MISSED(410); ErrEntryNotFound=NOT_FOUND(404)
ErrEntryMoved=ENTRY_MOVED(409); ErrRateLimited=RATE_LIMITED(429); ErrDependencyUnavailable=DEPENDENCY_UNAVAILABLE(503)
```
**File:** `application/usecase/join_queue.go`
```go
type JoinQueue struct { store outbound.IQueueStore; settings outbound.IEventSettingsRepo; audit outbound.IEntryAuditRepo
  booking outbound.IBookingClient; tokens outbound.ITokenIssuer; pii outbound.IPIIProtector
  limiter outbound.IRateLimiter; clock clock.IClock; cfg JoinSettings }
func NewJoinQueue(JoinDeps, JoinSettings) *JoinQueue
func (u *JoinQueue) Join(...) (dto.QueueEntryResponse, error)
```
Flow (design §8.1): rate-limit (email+IP) → validate (`EMAIL_MISMATCH`/`VALIDATION_ERROR`/`NIK_REQUIRED`/`NIK_INVALID`)
→ resolve NIK per `AuthContext` (R1.3/1.4) → `store.Join` (`join.lua`) → on `OK`, if slot free & supply available,
`ReserveBatch` + `promote.lua` → `ADMITTED` + issue queueToken + claimDeadline, else `WAITING` → `audit.Append(JOINED)`
→ issue/attach visitor token (cookie or body) → map errors.
**File:** `infrastructure/http/join_handler.go`
```go
func (h *JoinHandler) Register(mux) { mux.HandleFunc("POST /api/v1/events/{eventId}/queue-entries", h.join) }
```
Resolve auth: optional `Authorization: Bearer` via `IUserTokenVerifier`; read/issue `rb_visitor` cookie
(HttpOnly, Secure, SameSite=Lax) via `http.SetCookie`; detect mobile (no cookie / `X-Client: mobile`) to
return `visitorToken` in body. `201` with `httpx.WriteData`.

**Uses:** `pkg/httpx`, `pkg/apperror`, `pkg/clock`, `pkg/token`, `pkg/crypto`.
**Pattern:** `services/user/.../http/auth_handler.go` + `.../usecase/auth.go`.

### Acceptance Criteria
- [ ] AC2.1: empty `email` or `email != emailConfirmation` → `422 EMAIL_MISMATCH` (missing field → `400 VALIDATION_ERROR`).
- [ ] AC2.2: `consentNik != true` → `400 VALIDATION_ERROR`.
- [ ] AC2.3: NIK required but empty → `422 NIK_REQUIRED`.
- [ ] AC2.4: NIK not `^[0-9]{16}$` → `422 NIK_INVALID`.
- [ ] AC2.5: valid + slot free + no earlier waiter → `201 ADMITTED` with `claimDeadline`, `queueToken`.
- [ ] AC2.6: valid + must wait → `201 WAITING` with monotonic `seq`.
- [ ] AC2.8: NIK active → `409 NIK_ALREADY_IN_QUEUE`.
- [ ] AC2.9: email active + `one_active_per_email` → `409 EMAIL_ALREADY_IN_QUEUE`.
- [ ] AC2.10: NIK in `quota_exhausted` → `409 BUYER_QUOTA_EXHAUSTED`.
- [ ] AC2.11: sold out → `410 EVENT_SOLD_OUT`.
- [ ] AC2.12: outside sale window → `409 EVENT_NOT_ON_SALE`.
- [ ] AC2.13: Valkey unreachable → `503 DEPENDENCY_UNAVAILABLE`.
- [ ] AC2.14: same `Idempotency-Key` + existing active entry → return same entry.
- [ ] AC1.1: guest web → set `rb_visitor` cookie (HttpOnly, Secure, SameSite=Lax).
- [ ] AC1.2: mobile → `visitorToken` in body.
- [ ] AC1.3: `HasNIK==true` → use `token.nik_hash`, ignore `body.buyerNik`.
- [ ] AC1.5: visitor active + `one_active_per_visitor` → `409 VISITOR_ALREADY_IN_QUEUE`.

### Tests
```go
func TestJoin_Validation            // unit: email/confirm/consent/nik table (TC-QUE-01)
func TestJoin_AuthResolution        // guest / login+NIK / login-no-NIK (TC-USR-09/09a)
func TestJoin_DirectAdmission       // slot free → ADMITTED+token (TC-QUE-02)
func TestJoin_Waiting               // must wait → WAITING+seq
func TestJoin_ConstraintConflicts   // NIK/visitor/email/quota → 409 codes
func TestJoin_ValkeyDown_503        // fail closed (TC uses store returning transport err)
func TestJoin_Idempotent            // same Idempotency-Key → same entry (AC2.14)
func TestJoinLua_10kParallel_NoGaps // integration (TC-QUE-08): 10,000 joins, seq 1..N no gap
```

### Verify
```bash
go test -race ./internal/queue/... && go test -tags integration -race ./internal/queue/infrastructure/valkey/...
curl -X POST localhost:8081/api/v1/events/$EVENT/queue-entries \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.id","emailConfirmation":"a@b.id","buyerNik":"3201234567890123","consentNik":true}'
```

---

## Task 9: Status Document + Status Publisher worker (R4) [TODO]

**Traces to:** R4, TC-QUE-03, TC-QUE-10, TC-QUE-11, TC-QUE-12, TC-PERF-01, TC-PERF-03

### What exists
- `QueueStore.SetStatusDoc/GetStatusDoc` (Task 2/4), `EventSettings` repo (Task 5), booking `Supply` (Task 7).
- `internal/delivery/worker.go` — `jobs()` registry + `loop`.

### What to implement
**File:** `domain/dto/status_document.go` — exact field set from design §2.2 (`eventId, saleState, servingSeq,
guaranteedUntilSeq, possibleUntilSeq, soldOut, remainingTickets, admissionRatePerMin, removedBuckets,
nextPollAfterMs, poll{farMs,midMs,nearMs,farAbove,midAbove}, preTurnNotifyPosition, categories[], updatedAt`).
No PII (R4.7).
**File:** `application/usecase/get_status.go`
```go
type GetStatus struct { store outbound.IQueueStore }
func (u *GetStatus) Get(ctx, eventID string) (dto.StatusDocument, bool, error) // reads q:{e}:status; false → 404
```
**File:** `application/usecase/publish_status.go`
```go
type PublishStatus struct { store IQueueStore; settings IEventSettingsRepo; booking IBookingClient; clock clock.IClock }
func (u *PublishStatus) RunOnce(ctx context.Context) error // per event: compute doc → SetStatusDoc EX 1
func computeZones(remaining, servingSeq int64, nearFrontMult int) (guaranteedUntil, possibleUntil int64) // ceil when remaining<3 (TC-QUE-11)
func categoryState(sold, total int, thresholdPct int) string // AVAILABLE|LIMITED|SOLD_OUT (TC-QUE-12)
```
**File:** `infrastructure/http/status_handler.go`
```go
func (h *StatusHandler) Register(mux) { mux.HandleFunc("GET /api/v1/queue-status/{eventId}", h.get) }
// on success: w.Header().Set("Cache-Control","public, max-age=1, stale-while-revalidate=2"); WriteData 200
// not found: WriteError(ErrEntryNotFound) → 404
```
**Worker job:** in `internal/delivery/queue_module.go` build `PublishStatus`; register in `jobs(clients)`
as a `Job{Name:"status-publisher", Run: ps.RunOnce}` driven by a ticker at `status_publish_interval_ms`
(per design §7: use an internal ticker, not the single `WORKER_INTERVAL`).

**Uses:** `pkg/httpx`, `pkg/clock`.
**Pattern:** health handler `Register`; `get_status` mirrors `check_health` usecase style.

### Acceptance Criteria
- [ ] AC4.1: `GET /queue-status/{eventId}` → required fields `eventId, saleState, servingSeq, soldOut, updatedAt`.
- [ ] AC4.2: `Cache-Control: public, max-age=1, stale-while-revalidate=2`.
- [ ] AC4.3: publisher writes `q:{e}:status` with TTL 1s every `status_publish_interval_ms`.
- [ ] AC4.5: zones from stock + `near_front_multiplier`; `ceil` when remaining < 3 (TC-QUE-11).
- [ ] AC4.6: category state thresholds (TC-QUE-12).
- [ ] AC4.7: no NIK/email/token in document.
- [ ] AC4.8: unknown event → `404 NOT_FOUND`.

### Tests
```go
func TestComputeZones            // boundaries + ceil<3 (TC-QUE-10/11)
func TestCategoryState           // AVAILABLE/LIMITED/SOLD_OUT (TC-QUE-12)
func TestStatusHandler_CacheHeader
func TestStatusHandler_NotFound  // 404
func TestPublishStatus_WritesTTL1 // integration
```

### Verify
```bash
go test -race ./internal/queue/... ; curl -i localhost:8081/api/v1/queue-status/$EVENT | grep Cache-Control
```

---

## Task 10: Get My Entry (R5) [TODO]

**Traces to:** R5, TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-14

### What exists
- `QueueStore.FindByVisitor` + status doc for `servingSeq`/`removedAhead`; `Entry.ExactPosition` (Task 1).

### What to implement
**File:** `domain/dto/entry_status.go`
```go
type EntryStatus struct { EntryID string `json:"entryId"`; Seq int64 `json:"seq"`; Status string `json:"status"`
  ExactPosition int64 `json:"exactPosition"`; Zone, ClaimDeadline, Reason, Redirect string `json:",omitempty"` }
```
**File:** `port/inbound/my_entry_usecase.go` — `IGetMyEntry interface { Get(ctx, eventID, visitorID string) (dto.EntryStatus, error) }`.
**File:** `application/usecase/get_my_entry.go` — find entry, read `servingSeq`+`removedAhead` from status/sale,
compute `exactPosition` + `zone`; `ADMITTED` adds `claimDeadline`+`redirect`; terminal adds `reason`; missing → `ErrEntryNotFound`.
**File:** `infrastructure/http/my_entry_handler.go`
```go
func (h *MyEntryHandler) Register(mux) { mux.HandleFunc("GET /api/v1/queue-entries/me", h.get) }
// visitor from cookie rb_visitor OR header X-Visitor-Token; eventId from r.URL.Query().Get("eventId"); rate-limited per visitor
```

### Acceptance Criteria
- [ ] AC5.1: valid visitor → `entryId, seq, status, exactPosition, zone`.
- [ ] AC5.2: `exactPosition = mySeq - servingSeq - removedAhead`.
- [ ] AC5.3: `ADMITTED` includes `claimDeadline` + `redirect`.
- [ ] AC5.4: no entry → `404 NOT_FOUND`.
- [ ] AC5.5: terminal status returns that status + `reason`.
- [ ] AC3.2: over rate limit → `429 RATE_LIMITED`.

### Tests
```go
func TestGetMyEntry_Position     // exactPosition with removedAhead (TC-QUE-06)
func TestGetMyEntry_NotFound     // 404
func TestGetMyEntry_Terminal     // reason present
```

### Verify
```bash
curl -H "X-Visitor-Token: $V" "localhost:8081/api/v1/queue-entries/me?eventId=$EVENT"
```

---

## Task 11: Promotor + Claim + Claim Sweeper (R6) [TODO]

**Traces to:** R6, SRS-F-06-01..08 + SRS-Q-FUN-03, TC-QUE-05, TC-QUE-08, TC-QUE-09, TC-QUE-13, TC-QUE-14, TC-PERF-04

### What exists
- `promote.lua`/`claim.lua` (Task 3), booking client (Task 7), tokens (Task 6), event publisher (Task 12), audit repo (Task 5).

### What to implement
**File:** `application/usecase/promote.go`
```go
type Promote struct { store IQueueStore; settings IEventSettingsRepo; booking IBookingClient
  publisher IEventPublisher; audit IEntryAuditRepo; clock clock.IClock; batchMax int }
func (u *Promote) RunOnce(ctx context.Context) error // per event: free=cap-ActiveCount; TopWaiting(n); ReserveBatch;
// RESERVED→promote.lua+turn.arrived+audit(ADMITTED); NO_SUPPLY_NOW→keep; QUOTA_EXHAUSTED→SADD+REJECTED; SOLD_OUT→close
```
**File:** `application/usecase/sweep_claims.go`
```go
type SweepClaims struct { store IQueueStore; publisher IEventPublisher; audit IEntryAuditRepo; clock clock.IClock }
func (u *SweepClaims) RunOnce(ctx context.Context) error // ExpiredActive→MarkMissed+release+publish journey.ended(UNCLAIMED)+audit(MISSED)
```
**File:** `port/inbound/claim_usecase.go` — `IClaimTurn interface { Claim(ctx, entryID, visitorID string) (dto.ClaimResponse, error) }`.
**File:** `domain/dto/claim.go` — `type ClaimResponse struct { AdmissionToken string `json:"admissionToken"`; BookingURL string `json:"bookingUrl"` }`.
**File:** `application/usecase/claim_turn.go`
```go
type ClaimTurn struct { store IQueueStore; tokens ITokenIssuer; limiter IRateLimiter; clock clock.IClock; bookingBaseURL string }
func (u *ClaimTurn) Claim(...) // store.Claim(claim.lua): OK→issue admission token(60s)+bookingUrl; TURN_MISSED→410; NOT_FOUND→404; sold out→410
```
**File:** `infrastructure/http/claim_handler.go`
```go
func (h *ClaimHandler) Register(mux) { mux.HandleFunc("POST /api/v1/queue-entries/{entryId}/claim", h.claim) }
// visitor from cookie/header; rate-limited per visitor; WriteData 200
```
**Worker jobs:** register `promotor` (ticker `QUEUE_PROMOTOR_INTERVAL_MS`≈100ms) and `claim-sweeper`
(ticker `QUEUE_SWEEPER_INTERVAL_MS`≈10s) in `jobs(clients)` with internal tickers (design §7).
**Pre-turn notify (AC6.11):** in Promote/PublishStatus, when position reaches `preTurnNotifyPosition` and
`pre_turn_notify_enabled`, publish `queue.turn.soon` once (guard with a `q:{e}:notified` SADD).

### Acceptance Criteria
- [ ] AC6.1: Promotor takes N from `waiting` by seq order; calls `allocations:batch`.
- [ ] AC6.2: `RESERVED` → `active`, `ADMITTED`, store `allocationId`, publish `queue.turn.arrived`.
- [ ] AC6.3: `NO_SUPPLY_NOW` → stays `waiting` at same seq.
- [ ] AC6.4: `QUOTA_EXHAUSTED` → `SADD quota_exhausted`, entry `REJECTED`.
- [ ] AC6.5: `SOLD_OUT` → close queue, next entry `SOLD_OUT`.
- [ ] AC6.6: FIFO preserved across promotions (TC-QUE-08/14).
- [ ] AC6.7: valid visitor + `ADMITTED` + `now<claimDeadline` → issue `admissionToken`(EdDSA,60s)+`bookingUrl`, status `CLAIMED`.
- [ ] AC6.8: `now>=claimDeadline` or `MISSED` → `410 TURN_MISSED`.
- [ ] AC6.9: sold out at claim → `410 EVENT_SOLD_OUT`.
- [ ] AC6.10: Sweeper sets `MISSED`, releases slot, publishes `booking.journey.ended` outcome `UNCLAIMED`.
- [ ] AC6.11: position reaches `preTurnNotifyPosition` → publish `queue.turn.soon` once.

### Tests
```go
func TestPromote_FIFOOrder        // lower seq admitted first (TC-QUE-08/14)
func TestPromote_OutcomeRouting   // RESERVED/NO_SUPPLY_NOW/QUOTA_EXHAUSTED/SOLD_OUT branches
func TestClaim_Success            // 200 token + CLAIMED
func TestClaim_Deadline410        // TURN_MISSED (TC-QUE-09)
func TestClaim_NotFound404
func TestSweepClaims_MissedReleasesSlot
func TestPromote_Integration_NoOversell // integration, promote only after RESERVED (TC-PERF-04)
```

### Verify
```bash
go test -race ./internal/queue/... && go test -tags integration -race ./internal/queue/...
curl -X POST localhost:8081/api/v1/queue-entries/$ENTRY/claim -H "X-Visitor-Token: $V"
```

---

## Task 12: Event Publication — turn.soon / turn.arrived (R9) [TODO]

**Traces to:** R9, TC-QUE-13, TC-PERF-05, event schemas `docs/contracts/events/queue.turn.*`

### What exists
- `pkg/rabbitmq` publisher + envelope (`NewEnvelope`, `Publish`); `services/user/.../rabbitmq/otp_publisher.go` pattern.

### What to implement
**File:** `internal/queue/infrastructure/rabbitmq/event_publisher.go`
```go
const ( rkTurnSoon="queue.turn.soon"; rkTurnArrived="queue.turn.arrived"; producer="queue" )
type EventPublisher struct { pub *pkgrabbitmq.Publisher; now func() time.Time }
func NewEventPublisher(pub *pkgrabbitmq.Publisher, now func() time.Time) *EventPublisher
func (p *EventPublisher) PublishTurnSoon(ctx, eventID, queueEntryID, visitorID string) error             // payload {eventId,queueEntryId,visitorId}
func (p *EventPublisher) PublishTurnArrived(ctx, eventID, queueEntryID, visitorID string, dl time.Time) error // + claimDeadline
```
Build envelope with `aggregateID=eventID`; payload structs use `json` tags matching schemas; **no** NIK/email/token.

**Uses:** `pkg/rabbitmq`.
**Pattern:** `services/user/.../rabbitmq/otp_publisher.go` verbatim.

### Acceptance Criteria
- [ ] AC9.1/9.2: publish `queue.turn.soon` / `queue.turn.arrived` (+`claimDeadline`) with correct payloads.
- [ ] AC9.3: no PII/secret/token in payload.
- [ ] AC9.4: wrapped in envelope, published to `rusuh.events` topic with publisher confirm.

### Tests
```go
func TestPublishTurnSoon_Payload     // fake Publisher captures routingKey+payload, asserts no PII fields
func TestPublishTurnArrived_Deadline
```

### Verify
```bash
go test -race ./internal/queue/infrastructure/rabbitmq/...
```

---

## Task 13: Event Consumption — journey.ended / quota.exhausted / sold_out (R8) [TODO]

**Traces to:** R8, SRS-F-10-01..03, TC-QUE-05, TC-PERF-05, event schemas `docs/contracts/events/booking.*`

### What exists
- `pkg/rabbitmq/consumer.go` — `Consume(ctx, Subscription{Queue,RoutingKeys,Handler}, logger)`, `Permanent(err)`, retry+DLQ topology.
- `end_entry.lua` (Task 3), `QueueStore.AddQuotaExhausted`/`SetSale` (Task 2/4).

### What to implement
**File:** `internal/queue/infrastructure/rabbitmq/consumer.go`
```go
type Consumer struct { store outbound.IQueueStore; audit outbound.IEntryAuditRepo; clock clock.IClock; logger *slog.Logger }
func (c *Consumer) HandleJourneyEnded(ctx, d pkgrabbitmq.Delivery) error  // end_entry.lua → COMPLETED(PAID)|ENDED; idempotent
func (c *Consumer) HandleQuotaExhausted(ctx, d pkgrabbitmq.Delivery) error // SADD quota_exhausted
func (c *Consumer) HandleSoldOut(ctx, d pkgrabbitmq.Delivery) error        // SetSale(CLOSED,true)
func (c *Consumer) Subscriptions() []pkgrabbitmq.Subscription // one Subscription per queue/routing key
```
Decode `d.Envelope.Payload` into the booking payload struct; unparseable → `pkgrabbitmq.Permanent(err)` (DLQ).
**Worker wiring:** in `RunWorker`/`queue_module.go`, for each `Subscription` spawn a goroutine running
`clients.MQ.Consume(ctx, sub, logger)` (alongside the ticker jobs).

### Acceptance Criteria
- [ ] AC8.1: `booking.journey.ended` on `CLAIMED` → release slot + terminal (`PAID→COMPLETED`, else `ENDED`) via `end_entry.lua`.
- [ ] AC8.2: already terminal → ignored (idempotent).
- [ ] AC8.3: `booking.quota.exhausted` → `SADD quota_exhausted` (idempotent).
- [ ] AC8.4: `booking.sold_out` → `saleState=CLOSED, soldOut=true`.
- [ ] AC8.5: corrupt payload → `rabbitmq.Permanent` → DLQ.
- [ ] AC8.6: tiered retry + DLQ after max retries (handled by `pkg/rabbitmq`).
- [ ] AC8.7: duplicate delivery idempotent (TC-QUE-05).

### Tests
```go
func TestHandleJourneyEnded_Idempotent  // double delivery → one terminal transition (TC-QUE-05)
func TestHandleQuotaExhausted_SADD
func TestHandleSoldOut_ClosesSale
func TestConsumer_CorruptPayload_Permanent
func TestConsumer_DoubleDelivery_Integration // real RabbitMQ (TC-QUE-05)
```

### Verify
```bash
go test -race ./internal/queue/infrastructure/rabbitmq/... && go test -tags integration -race ./internal/queue/infrastructure/rabbitmq/...
```

---

## Task 14: Leave Queue & Move Entry (R7) [TODO]

**Traces to:** R7, SRS-F-13-03 + SRS-F-01-10, TC-QUE-16, TC-QUE-17

### What exists
- `leave.lua` (Task 3), `QueueStore.Leave` (Task 2/4).

### What to implement
**File:** `port/inbound/leave_usecase.go` — `ILeaveQueue interface { Leave(ctx, entryID, visitorID string) error }` (+ optional `IMoveEntry`).
**File:** `application/usecase/leave_queue.go`
```go
type LeaveQueue struct { store IQueueStore; audit IEntryAuditRepo; clock clock.IClock }
func (u *LeaveQueue) Leave(ctx, entryID, visitorID string) error // leave.lua → LEFT + cleanup; wasActive triggers promote next tick; audit(LEFT); not found → ErrEntryNotFound
```
**File:** `infrastructure/http/leave_handler.go`
```go
func (h *LeaveHandler) Register(mux) { mux.HandleFunc("DELETE /api/v1/queue-entries/{entryId}", h.leave) }
// visitor from cookie/header; success → w.WriteHeader(204); not found → WriteError(ErrEntryNotFound) 404
```
Move-entry (TC-QUE-16) via email link is OPTIONAL for this slice: if implemented, reassign `visitorId` on the
entry hash + constraint keys keeping the same `seq`, invalidate the old link, old device → `ENTRY_MOVED`.

### Acceptance Criteria
- [ ] AC7.1: `DELETE` on own entry → `LEFT`, cleanup constraint keys, release slot if `ADMITTED`, `204 No Content`.
- [ ] AC7.2: left user rejoin → new `seq` (back of queue).
- [ ] AC7.3/7.4: move keeps `seq`, new visitor owns it, old link → `ENTRY_MOVED` (if implemented).
- [ ] AC7.5: entry not found → `404 NOT_FOUND`.

### Tests
```go
func TestLeave_Success204
func TestLeave_NotFound404
func TestLeave_ReleasesSlot_Integration   // constraint keys deleted (TC-QUE-17)
func TestLeaveThenRejoin_NewSeq
```

### Verify
```bash
curl -i -X DELETE localhost:8081/api/v1/queue-entries/$ENTRY -H "X-Visitor-Token: $V"   # 204
```

---

## Task 15: Rate limiting wiring (R3) [TODO]

**Traces to:** R3, SRS-F-04-09 (inspection — no dedicated TC)

### What exists
- `valkey.RateLimiter` (Task 4). Join/me/claim usecases call `limiter.Allow` (Tasks 8/10/11).

### What to implement
Apply `limiter.Allow` at the start of Join (per email + per IP), Get My Entry (per visitor), Claim (per visitor)
with limits/windows from `config.RateLimit`. On deny → `ErrRateLimited` (429) and set `Retry-After` header
in the handler. Do **not** rate-limit `GET /queue-status/{eventId}` (CDN absorbs — AC3.4).

### Acceptance Criteria
- [ ] AC3.1: join over limit (email/IP) → `429 RATE_LIMITED` + `Retry-After`.
- [ ] AC3.2: `GET /queue-entries/me` over limit → `429`.
- [ ] AC3.3: counters in Valkey (cross-instance).
- [ ] AC3.4: `GET /queue-status/{eventId}` NOT rate-limited at origin.

### Tests
```go
func TestJoin_RateLimited429     // unit with limiter returning false → 429 + Retry-After
func TestStatus_NotRateLimited
```

### Verify
```bash
go test -race ./internal/queue/...
```

---

## Task 16: Metrics & final verification (R10) [TODO]

**Traces to:** R10.3/10.4, design §11

**Status:** Health ✅ IMPLEMENTED; config validation ✅ IMPLEMENTED; metrics ❌ TARGET.

### What to implement
Expose Prometheus counters/gauges `rbt_queue_joins_total{event_id,status}`,
`rbt_queue_claims_total{event_id,status}`, `rbt_queue_waiting_count{event_id}`,
`rbt_queue_active_count{event_id}`, `rbt_queue_promotions_total{event_id}`,
`rbt_queue_unclaimed_total{event_id}`. Increment in the matching usecases/workers. Ensure NIK/email/token
never reach logs (security-privacy steering).

### Acceptance Criteria
- [ ] AC10.1: `GET /health` → 200 UP / 503 DOWN (implemented — verify only).
- [ ] AC10.2: config validated at start (implemented — verify only).
- [ ] AC10.3: metrics exposed.
- [ ] AC10.4: no NIK/email/token plaintext in logs.

### Final Verification
```bash
cd services/queue
go build ./...
go test ./... -race
make test-integration     # Valkey/PG/RabbitMQ real (integration tag)
make lint                 # golangci-lint v2 + depguard (hexagonal layers)
make cover                # logic coverage ≥ 80%
make mock                 # regen _mock/ if ports changed
```

---

## Implementation order & dependencies

```
Task 0 (config/module scaffold)
  └─ Task 1 (domain) ─ Task 2 (ports+mocks) ─ Task 3 (Lua)
        ├─ Task 4 (valkey)  ─ Task 5 (postgres) ─ Task 6 (security) ─ Task 7 (booking client)
        │     └─ Task 8 (Join+Direct Admission)   ← first end-to-end endpoint
        │     └─ Task 9 (Status + publisher)
        │     └─ Task 10 (My Entry)
        │     └─ Task 12 (publisher) → Task 11 (Promotor/Claim/Sweeper)
        │     └─ Task 13 (consumer)
        │     └─ Task 14 (Leave)
        └─ Task 15 (rate-limit wiring)  ─ Task 16 (metrics + final verify)
```

## Status summary (implemented vs TODO)

| Area | Status |
|------|--------|
| Cobra verbs, config+validation, clients, `/health`, worker loop skeleton, migrations, shared ValidationError | ✅ DONE (verify only) |
| Config extras (token/PII/booking/rate-limit/worker tuning) + `queue_module.go` wiring | ❌ TODO (Task 0) |
| Domain (Entry, Seq/Status/Zone/BuyerNikHash, state machine) | ❌ TODO (Task 1) |
| Ports + mocks | ❌ TODO (Task 2) |
| Lua scripts (join/promote/claim/leave/end_entry) | ❌ TODO (Task 3) |
| Valkey QueueStore + RateLimiter | ❌ TODO (Task 4) |
| Postgres event_settings + entry_audit repos | ❌ TODO (Task 5) |
| Security (tokens, user-token verify, PII) | ❌ TODO (Task 6) |
| Booking client | ❌ TODO (Task 7) |
| Join + Direct Admission | ❌ TODO (Task 8) |
| Status Document + publisher | ❌ TODO (Task 9) |
| Get My Entry | ❌ TODO (Task 10) |
| Promotor + Claim + Sweeper | ❌ TODO (Task 11) |
| Event publication | ❌ TODO (Task 12) |
| Event consumption | ❌ TODO (Task 13) |
| Leave + Move | ❌ TODO (Task 14) |
| Rate-limit wiring | ❌ TODO (Task 15) |
| Metrics + final verify | ❌ TODO (Task 16) |

## Test Case Alignment

| Task | Test Cases |
|------|------------|
| 1 | TC-QUE-09 (state machine), TC-QUE-10/11 (zones) |
| 3 | TC-GST-03..05, TC-QUE-07, TC-QUE-08, TC-QUE-17 |
| 8 | TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08, TC-GST-01/02/05, TC-USR-09/09a |
| 9 | TC-QUE-03, TC-QUE-10, TC-QUE-11, TC-QUE-12, TC-PERF-01, TC-PERF-03 |
| 10 | TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-14 |
| 11 | TC-QUE-05, TC-QUE-08, TC-QUE-09, TC-QUE-13, TC-QUE-14, TC-PERF-04 |
| 12 | TC-QUE-13, TC-PERF-05 |
| 13 | TC-QUE-05, TC-PERF-05 |
| 14 | TC-QUE-16, TC-QUE-17 |
| 15 | (rate-limit inspection) |
| 16 | (health integration) |
