# Design — Queue Service

| Field | Value |
|-------|-------|
| Spec | queue-service |
| Sumber | `docs/sdd/SDD-QUEUE-001.md` (v1.1), HLD §4.4.1–4.4.2, ADR-003 |
| Service | `services/queue`, port 8081, schema `queue`, role `svc_queue` |
| State store | Valkey (antrean, atomik), PostgreSQL (audit + settings), RabbitMQ (event) |

Dokumen ini mendeskripsikan arsitektur target Queue Service. Bagian yang **sudah ada** di kode ditandai *(implemented)*; selebihnya adalah rancangan yang dibangun mulai Sprint 1. Lihat `requirements.md` §Status Implementasi untuk peta delta.

---

## 1. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` dan ADR-016. Lapisan ditegakkan `depguard`.

```
cmd/ (Cobra: api | worker | migrate | healthcheck)          [implemented]
  └─ internal/delivery/ (composition root: server.go, worker.go, clients.go)   [implemented]
       └─ internal/queue/
            ├─ port/inbound     IJoinQueue, IGetStatus, IGetMyEntry, IClaimTurn, ILeaveQueue, IHealthUsecase*
            ├─ port/outbound    IQueueStore(Valkey/Lua), IEntryAuditRepo(PG), IEventSettingsRepo(PG),
            │                   IBookingClient(HTTP), IEventPublisher(RabbitMQ), IDependencyChecker*
            ├─ domain/          entity (Entry, EventSettings, StatusDocument), valueobject (Seq, Status,
            │                   EntryId, BuyerNikHash), dto, mapper — hanya standard library
            ├─ application/usecase  satu usecase per file (JoinQueue, Promote, ClaimTurn, PublishStatus,
            │                       SweepClaims, CheckHealth*)
            └─ infrastructure/  http (handler + Register(mux)), valkey (Lua runner), postgres (repo),
                                rabbitmq (publisher/consumer), bookingclient, healthcheck*
```
`*` = sudah ada di boilerplate (jalur health). Interface berawalan `I`, hanya di `port/`. Konstruktor mengembalikan struct konkret (*accept interfaces, return structs*).

Composition root (`internal/delivery/server.go`, *implemented*) saat ini hanya memasang `NewHealthHandler(...).Register(mux)`. Handler domain baru (JoinHandler, StatusHandler, ClaimHandler) akan ditambahkan dengan pola `Register(mux)` yang sama. Worker (`worker.go`, *implemented*) menjalankan `loop()` atas `jobs()` yang kini `nil`; Promotor/StatusPublisher/Sweeper didaftarkan di `jobs(clients)` saat dibangun.

---

## 2. Data Model

### 2.1 PostgreSQL (schema `queue`) — *migrasi implemented*

Sumber: `deployments/migrations/0001_init.up.sql` + `0002_account_ref.up.sql`. **Ini yang aktual** (lebih kaya dari SDD §5.1).

`event_settings` (PK `event_id`):
`active_slot_capacity` (1..100000, default 500), `near_front_multiplier`, `status_publish_interval_ms` (250..5000), `poll_far_ms`/`poll_mid_ms`/`poll_near_ms` (server-authoritative polling, dengan CHECK `near <= mid <= far` dan `status_publish_interval_ms <= poll_near_ms`), `one_active_per_visitor`, `one_active_per_email`, `category_limited_threshold_pct`, `pre_turn_notify_enabled`, `updated_at`.

`entry_audit` (append-only, ditulis asinkron/batch untuk audit FIFO + pemulihan):
`id bigserial` PK, `event_id`, `entry_id`, `seq`, `buyer_nik_hash bytea`, `action` ∈ `{JOINED, ADMITTED, CLAIMED, MISSED, LEFT, COMPLETED, ENDED, SOLD_OUT, CLOSED, REJECTED}`, `occurred_at`, `metadata jsonb`, `account_id` (NULL untuk guest). Index: `(event_id, seq)`, `(entry_id, occurred_at)`.

> PostgreSQL adalah **audit & recovery**, bukan jalur panas. State antrean hidup di Valkey. Tabel `account_id` mencatat pembeli login opsional (ADR-020); guest tetap NULL.

### 2.2 Valkey Keys — *target*

Semua key per-event memakai hash tag `{e:<eventId>}` agar operasi Lua atomik berada di satu slot cluster.

| Key | Type | Deskripsi | TTL |
|-----|------|-----------|-----|
| `q:{e}:seq` | String | Counter sequence (INCR) | Persistent |
| `q:{e}:waiting` | Sorted Set | member=entryId, score=seq | Persistent |
| `q:{e}:entry:<entryId>` | Hash | Data entry (lihat §2.3) | Persistent |
| `q:{e}:nik:<buyerNikHash>` | String | entryId aktif (constraint NIK) | Persistent |
| `q:{e}:visitor:<visitorId>` | String | entryId aktif (constraint device) | Persistent |
| `q:{e}:email:<emailHash>` | String | entryId aktif (constraint email) | Persistent |
| `q:{e}:quota_exhausted` | Set | buyerNikHash kuota habis | Persistent |
| `q:{e}:active` | Sorted Set | entry ADMITTED/CLAIMED, score=claimDeadline | Persistent |
| `q:{e}:status` | String | Status Document JSON | 1s (cache) |

### 2.3 Entry Hash Fields

`seq, visitorId, buyerNikHash, emailEnc, accountId, status, joinedAtMs, admittedAtMs, claimedAtMs, claimDeadlineMs, allocationId`.

### 2.4 Aturan Lua-untuk-atomisitas (WAJIB)

Per `.kiro/steering/code-principles.md`: **Lua hanya untuk atomisitas antrean; aturan bisnis lain di Go.** Satu round-trip Lua menjamin tidak ada *race* saat memeriksa constraint + menambah sequence. Semua KEYS dalam satu skrip memakai hash tag `{e}` yang sama (satu slot). Skrip inti:

- `join.lua` — cek `quota_exhausted` → cek NIK/visitor/email → `INCR seq` → `HSET entry` → `ZADD waiting` → set constraint keys. Return `{OK, seq, WAITING}` / `{EXISTING, entryId}` / `{ERR, code}` (lihat SDD Appendix C.1).
- `promote.lua` — pindahkan N entry teratas `waiting` → `active` dengan `claimDeadline`, set `ADMITTED`, secara atomik terhadap slot.
- `claim.lua` — verifikasi `status==ADMITTED` dan `now < claimDeadline`, set `CLAIMED`.
- `end_entry.lua` — guard idempoten untuk consumer (`booking.journey.ended`): rilis slot hanya bila status transisi valid.

Go **tidak** menaruh keputusan bisnis (kuota, urutan FIFO logis) di luar skrip; Go membangun KEYS/ARGV, memanggil skrip, lalu memetakan hasil ke error domain.

---

## 3. Flows

### 3.1 Join & Direct Admission (HLD §4.4.1)

```
Klien ──POST queue-entries──▶ JoinHandler
  → JoinQueue usecase: validasi email (Go) + resolve buyerNikHash per auth state (Go)
  → IQueueStore.Join (EVAL join.lua, atomik)
     OK,seq,WAITING   → 201 WAITING
     EXISTING         → map ke 409 NIK/VISITOR/EMAIL_ALREADY_IN_QUEUE
     ERR,QUOTA        → 409 BUYER_QUOTA_EXHAUSTED
  → (opsional direct admission bila slot bebas) → 201 ADMITTED
  → IEntryAuditRepo.Append(JOINED)  (asinkron/batch)
Valkey down → fail closed → 503 / error (bukan diam)
```

### 3.2 Promotion & Claim (HLD §4.4.2)

```
Promotor (interval ~100ms):
  slots = capacity - len(active)
  if slots>0: take N from waiting (seq order)
    → IBookingClient.ReserveBatch(POST /internal/v1/allocations:batch)
       RESERVED → promote.lua (waiting→active, ADMITTED) → publish queue.turn.arrived
       NO_SUPPLY_NOW/SOLD_OUT/QUOTA_EXHAUSTED → tangani sesuai status
Klien ──POST claim──▶ ClaimHandler → claim.lua
  status!=ADMITTED → 403 ENTRY_NOT_ADMITTED
  now>=deadline    → 403 CLAIM_DEADLINE_PASSED
  OK → admissionToken sekali-pakai (EdDSA, pkg/token) + CLAIMED
Claim Sweeper (interval ~10s):
  ZRANGEBYSCORE active < now → UNCLAIMED, remove active, publish booking.journey.ended(UNCLAIMED)
```

### 3.3 Status Publisher

```
Status Publisher (interval = event_settings.status_publish_interval_ms):
  compute servingSeq, waitingCount, activeSlots
  SET q:{e}:status = JSON, EX 1s
Klien polling GET queue-status → CloudFront cache 1s → origin hanya 1x/detik/event
```

---

## 4. Inter-Service Communication

| Arah | Target | Endpoint / Event | Tujuan |
|------|--------|------------------|--------|
| Outbound HTTP | Booking | `POST /internal/v1/allocations:batch` | Reserve alokasi batch untuk admitted |
| Outbound HTTP | Booking | `GET /internal/v1/events/{id}/supply` | Snapshot stok (cache 1s di Valkey) |
| Publish | Notification | `queue.turn.soon` | Trigger push "giliran sebentar lagi" |
| Publish | Notification | `queue.turn.arrived` | Trigger push giliran tiba + claimDeadline |
| Consume | ← Booking | `booking.journey.ended` | Rilis slot, entry → ENDED (guard `status==CLAIMED`) |
| Consume | ← Booking | `booking.quota.exhausted` | `SADD quota_exhausted` |
| Consume | ← Booking | `booking.sold_out` | `saleStatus = SOLD_OUT` |

Consumer idempoten tanpa tabel inbox (guard state + `SADD`/Lua `end_entry`). Retry berjenjang via `RABBITMQ_RETRY_DELAYS`, DLQ setelah `RABBITMQ_MAX_RETRIES`; payload rusak → `rabbitmq.Permanent(err)`.

---

## 5. Error Map

Error domain bertipe dipetakan ke `errorCode` di `infrastructure/http` memakai `pkg/apperror`. Handler selalu mengembalikan DTO lewat `domain/mapper`, tidak pernah entity.

| Error domain | HTTP | Code |
|---|---|---|
| ErrValidation | 400 | `VALIDATION_ERROR` |
| ErrNikInvalid | 422 | `NIK_INVALID` |
| ErrNikRequired | 422 | `NIK_REQUIRED` |
| ErrNikInQueue | 409 | `NIK_ALREADY_IN_QUEUE` |
| ErrVisitorInQueue | 409 | `VISITOR_ALREADY_IN_QUEUE` |
| ErrEmailInQueue | 409 | `EMAIL_ALREADY_IN_QUEUE` |
| ErrQuotaExhausted | 409 | `BUYER_QUOTA_EXHAUSTED` |
| ErrNotOnSale | 409 | `EVENT_NOT_ON_SALE` |
| ErrSoldOut | 410 | `EVENT_SOLD_OUT` |
| ErrClaimDeadline | 403 | `CLAIM_DEADLINE_PASSED` |
| ErrNotAdmitted | 403 | `ENTRY_NOT_ADMITTED` |
| ErrEntryNotFound | 404 | `ENTRY_NOT_FOUND` |
| ErrRateLimited | 429 | `RATE_LIMITED` (+ `Retry-After`) |

---

## 6. Configuration (D-24)

*Implemented* di `internal/config`: `APP_*`, `LOG_*`, `HTTP_*`, `WORKER_INTERVAL`, `POSTGRES_*` (+`POSTGRES_SCHEMA=queue`), `VALKEY_*` (`VALKEY_SERVER` JSON), `RABBITMQ_*` (exchange `rusuh.events`, retry delays, prefetch, max retries). Semua divalidasi saat start (`Config.Validate`), gagal cepat, seluruh error dilaporkan sekaligus.

*Target* (ditambahkan saat fitur dibangun): `BOOKING_SERVICE_URL`, `TOKEN_PRIVATE_KEY_PATH`/`TOKEN_PUBLIC_KEY_PATH` (EdDSA, `pkg/token`), dan parameter antrean. **Catatan:** SDD §9 menyebut `QUEUE_MAX_ACTIVE_SLOTS`, `QUEUE_CLAIM_WINDOW_SECONDS`, dll. sebagai env, tetapi migrasi aktual menaruh nilai setara di tabel `event_settings` per-event (`active_slot_capacity`, `status_publish_interval_ms`, `poll_*_ms`). Desain ini **mengutamakan `event_settings`** sebagai sumber (per-event, dapat diubah tanpa deploy); env hanya untuk default/fallback.

---

## 7. Observability

- **Metrics** (target): `rbt_queue_joins_total{event_id,status}`, `rbt_queue_claims_total{event_id,status}`, `rbt_queue_waiting_count{event_id}`, `rbt_queue_active_count{event_id}`, `rbt_queue_promotions_total{event_id}`, `rbt_queue_unclaimed_total{event_id}`.
- **Alerts**: QueuePromotorStalled (tak ada promosi 1m saat sale → Critical), QueueUnclaimedRateHigh (>5% 5m → Warning), QueueValkeyLatencyHigh (p99 > 50ms 5m → Warning).
- **Health** *(implemented)*: `GET /health` → 200 UP / 503 DOWN; ping Valkey + PostgreSQL + RabbitMQ (`healthcheck.New(clients.Pingers(), cfg.HTTP.HealthTimeout)`), untuk ALB & container `HEALTHCHECK` (D-23).
- **Log**: `slog` JSON, dengan `RequestID()` middleware (*implemented* di `httpx.Chain`). OTP/NIK/token plaintext tidak pernah masuk log (security-privacy steering).

---

## 8. Keamanan & Privasi

- NIK hanya sebagai hash HMAC-SHA256 (`HashNIK(nik, secret)`, SDD App. A.2) — plaintext NIK tidak disimpan/dilog. Email disimpan terenkripsi (`emailEnc`).
- Admission/claim token terbit hanya lewat jalur antrean (NFR-07), EdDSA via `pkg/token`; `admissionToken` sekali-pakai.
- Fail closed: Valkey down → join/claim ditolak.
- Rate limit per email + IP di Valkey.

---

## 9. Testing

Mengikuti `.kiro/steering/testing-review.md` (testify, table-driven, mock dari `_mock/`, integration build tag `integration`).

| Level | Cakupan | TC |
|-------|---------|----|
| Unit (usecase) | validasi join, resolve NIK per auth state, mapping error, posisi = seq-serving | TC-GST-02, TC-QUE-01 |
| Unit (domain) | state machine Entry, value object Seq/Status | TC-QUE-09 |
| Integration (Valkey + Lua) | atomisitas `join.lua`, constraint NIK/visitor/email, 1000 join paralel tanpa celah seq, FIFO lintas promosi | TC-QUE-07, TC-QUE-08, TC-QUE-14 |
| Integration (consumer) | idempoten kirim ganda `journey.ended`/`quota.exhausted`/`sold_out` | TC-QUE-05 |
| Integration (PG) | append `entry_audit`, recovery dari audit | — |
| E2E | guest flow, logged-in (NIK stored / no NIK), leave & rejoin, move entry | TC-GST-01..05, TC-USR-09/09a, TC-QUE-16, TC-QUE-17 |
| Load (k6) | 20.000 join/s, cache hit Status Document | TC-PERF-01..03, TC-PERF-05 |

Coverage logika ≥ 80% (CI `case-coverage`); `cmd/`, `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan.

### 9.1 Run (build) stage

```bash
make infra keys
cd services/queue && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &        # port 8081
go run ./cmd worker &
```

### 9.2 Verify stage

```bash
curl -fsS localhost:8081/health            # 200 UP (implemented)
cd services/queue && go build ./... && go test ./... -race
make test-integration                       # Valkey/PG/RabbitMQ nyata (integration tag)
make lint                                   # golangci-lint v2 + depguard (lapisan hexagonal)
```

Target verifikasi fitur (saat dibangun): `join.lua` lulus test atomisitas & 1000-join-paralel tanpa celah seq (TC-QUE-08), FIFO lintas promosi (TC-QUE-14), consumer idempoten kirim-ganda (TC-QUE-05), k6 20k join/s (TC-PERF).

---

## 10. Referensi

- `docs/sdd/SDD-QUEUE-001.md` (LLD, Appendix C skrip Lua), HLD §4.4.1–4.4.2, ADR-003 (polling Status Document), ADR-016/019/020.
- Kontrak: `docs/contracts/openapi/queue.yaml`.
- Steering: `.kiro/steering/{structure,code-principles,tech,security-privacy,invariants,testing-review}.md`.
