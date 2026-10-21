# Design — Queue Service

| Field | Value |
|-------|-------|
| Spec | queue-service |
| Sumber | Kode `services/queue/`, kontrak `docs/contracts/openapi/queue.yaml` + `docs/contracts/events/*`, `docs/analisa/04a-SDD-QUEUE-001.md` (v1.1), HLD §4.4.1–4.4.2, ADR-003/016/019/020 |
| Prinsip | **Kode/kontrak menang**; SDD referensi konseptual. Delta ditandai di `requirements.md` §2.1. |
| State store | Valkey (antrean, atomik Lua, rate limit), PostgreSQL (audit + settings), RabbitMQ (event) |

Dokumen ini cukup untuk mengimplementasikan Queue Service tanpa membaca SDD. Bagian yang **sudah ada** di kode ditandai *(implemented)*.

---

## 1. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` dan ADR-016. Lapisan ditegakkan `depguard`. Interface berawalan `I`, hanya didefinisikan di `port/`. Konstruktor mengembalikan struct konkret (*accept interfaces, return structs*).

```
services/queue/
  cmd/ (Cobra: api | worker | migrate | healthcheck)                       [implemented]
    └─ internal/delivery/ (composition root: server.go, worker.go, clients.go, serve.go)  [implemented]
         └─ internal/queue/
              ├─ port/inbound     IJoinQueue, IGetStatus, IGetMyEntry, IClaimTurn,
              │                   ILeaveQueue, IMoveEntry, IHealthUsecase*
              ├─ port/outbound    IQueueStore (Valkey/Lua), IEntryAuditRepo (PG),
              │                   IEventSettingsRepo (PG), IBookingClient (HTTP),
              │                   IEventPublisher (RabbitMQ), IRateLimiter (Valkey),
              │                   ITokenIssuer (EdDSA), IDependencyChecker*
              ├─ domain/          entity (Entry, EventSettings, StatusDocument),
              │                   valueobject (Seq, Status, EntryId, BuyerNikHash, Zone),
              │                   dto, mapper — hanya standard library
              ├─ application/usecase  satu usecase per file (JoinQueue, GetStatus, GetMyEntry,
              │                       ClaimTurn, LeaveQueue, MoveEntry, Promote, PublishStatus,
              │                       SweepClaims, ConsumeBookingEvent, CheckHealth*)
              └─ infrastructure/  http (handler + Register(mux)), valkey (Lua runner),
                                  postgres (repo), rabbitmq (publisher + consumer),
                                  bookingclient (HTTP), healthcheck*
```
`*` = ada di boilerplate (jalur health).

**Composition root saat ini** (`server.go`, *implemented*): `NewHandler` memasang hanya `NewHealthHandler(health).Register(mux)` lalu membungkus `httpx.Chain(mux, RequestID(), Recover(logger))`. Handler domain baru (`JoinHandler`, `StatusHandler`, `MyEntryHandler`, `ClaimHandler`, `LeaveHandler`) ditambahkan dengan pola `Register(mux)` yang sama.

**Worker** (`worker.go`, *implemented*): `RunWorker` menjalankan `loop(ctx, cfg.Worker.Interval, jobs(clients), logger)`; `jobs()` kini `nil`. Promotor/StatusPublisher/Sweeper didaftarkan sebagai `[]Job{{Name, Run}}` di `jobs(clients)`. Catatan: `loop` memakai satu `WORKER_INTERVAL` global; worker dengan interval berbeda (Promotor ~100ms vs Sweeper ~10s) di-implement dengan ticker internal per-job atau beberapa goroutine, bukan satu ticker (lihat §7).

---

## 2. API Endpoints (OpenAPI-style, normatif dari `queue.yaml`)

Semua respons sukses dibungkus `{ meta, data }`; semua error `{ meta, data: null }` dengan `meta.errorCode` (envelope SRS §3.1.4). `meta` = `{ code, message, requestId, errorCode?, details? }`.

### 2.1 `POST /api/v1/events/{eventId}/queue-entries` — Join Queue

| Aspek | Detail |
|-------|--------|
| Auth | Opsional `userBearer` (JWT EdDSA User Service); tanpa token = guest |
| Path param | `eventId` — uuid, required |
| Headers | `X-Request-ID` (opsional, dibuat bila kosong), `X-Visitor-ID`/cookie (web), `Authorization: Bearer` (opsional), `Idempotency-Key` (opsional, untuk retry aman) |
| Rate limit | Per email + per IP (Valkey); lampaui → 429 `RATE_LIMITED` + `Retry-After` |

**Request body (`JoinRequest`):**
```json
{
  "buyerNik": "3201234567890123",
  "email": "pembeli@contoh.id",
  "emailConfirmation": "pembeli@contoh.id",
  "consentNik": true
}
```
| Field | Type | Required | Constraint |
|-------|------|----------|-----------|
| `buyerNik` | string | Conditional | `^[0-9]{16}$`. Guest / login-tanpa-NIK: required. Login-dengan-NIK: optional & **diabaikan** (server pakai `token.nik_hash`). |
| `email` | string(email) | Yes | max 255 |
| `emailConfirmation` | string(email) | Yes | harus == `email` |
| `consentNik` | boolean | Yes | harus `true` |

**Responses:**

| Status | Body | Kondisi |
|--------|------|---------|
| 201 | `{ meta, data: QueueEntry }` | Masuk antrean (`WAITING`) atau langsung `ADMITTED` |
| 400 | `ErrorEnvelope` `VALIDATION_ERROR` | Field hilang/malformed, `consentNik != true` |
| 409 | `ErrorEnvelope` `NIK_ALREADY_IN_QUEUE` / `VISITOR_ALREADY_IN_QUEUE` / `EMAIL_ALREADY_IN_QUEUE` / `BUYER_QUOTA_EXHAUSTED` / `EVENT_NOT_ON_SALE` | Konflik constraint / sale window |
| 410 | `ErrorEnvelope` `EVENT_SOLD_OUT` | Tiket habis |
| 422 | `ErrorEnvelope` `NIK_INVALID` / `EMAIL_MISMATCH` / `NIK_REQUIRED` | Input tak valid (`email != emailConfirmation` → `EMAIL_MISMATCH`) |
| 429 | `ErrorEnvelope` `RATE_LIMITED` (+ `Retry-After`) | Rate limit |
| 503 | `ErrorEnvelope` `DEPENDENCY_UNAVAILABLE` | Valkey/Booking down — fail closed; retry dengan `Idempotency-Key` sama |

**`QueueEntry` (data 201):**
```json
{
  "entryId": "uuid",
  "seq": 12345,
  "status": "WAITING",
  "queueToken": "jwt-eddsa",
  "visitorToken": "jwt-eddsa (mobile only)",
  "statusUrl": "/api/v1/queue-status/{eventId}",
  "claimDeadline": "2026-10-20T10:03:00Z (ADMITTED only)",
  "joinedAt": "2026-10-20T10:00:00Z",
  "serverTime": "2026-10-20T10:00:00Z"
}
```
`status ∈ {WAITING,ADMITTED,CLAIMED,MISSED,LEFT,SOLD_OUT,CLOSED,COMPLETED,ENDED}`. Required: `entryId, seq, status, joinedAt, serverTime`.

### 2.2 `GET /api/v1/queue-status/{eventId}` — Status Document (CDN)

| Aspek | Detail |
|-------|--------|
| Auth | None (public) |
| Path param | `eventId` — uuid |
| Cache | `Cache-Control: public, max-age=1, stale-while-revalidate=2` (CloudFront 1s) |
| Rate limit | **Tidak** di origin (diserap CDN) |

**Response 200 (`StatusDocument`, tanpa data pribadi):**
```json
{
  "eventId": "uuid",
  "saleState": "OPEN",
  "servingSeq": 12300,
  "guaranteedUntilSeq": 12450,
  "possibleUntilSeq": 12600,
  "soldOut": false,
  "remainingTickets": 320,
  "admissionRatePerMin": 180,
  "removedBuckets": { "size": 100, "fromSeq": 12000, "counts": [3, 5, 2] },
  "nextPollAfterMs": 3000,
  "poll": { "farMs": 30000, "midMs": 10000, "nearMs": 3000, "farAbove": 500, "midAbove": 100 },
  "preTurnNotifyPosition": 20,
  "categories": [ { "code": "VIP", "name": "VIP", "state": "LIMITED" } ],
  "updatedAt": "2026-10-20T10:00:00Z"
}
```
| Field | Type | Notes |
|-------|------|-------|
| `saleState` | enum `NOT_STARTED\|OPEN\|CLOSED` | required |
| `servingSeq` | int | seq yang sedang dilayani; required |
| `guaranteedUntilSeq` | int | batas atas zona **Dijamin** |
| `possibleUntilSeq` | int | batas atas zona **Peluang** |
| `soldOut` | bool | required |
| `removedBuckets` | obj | kompensasi posisi untuk entry yang keluar di depan |
| `poll.farAbove/midAbove` | int | ambang posisi untuk memilih tier polling |
| `preTurnNotifyPosition` | int | posisi memicu banner/push `turn.soon` |
| `categories[].state` | enum `AVAILABLE\|LIMITED\|SOLD_OUT` | ambang `category_limited_threshold_pct` |

Required: `eventId, saleState, servingSeq, soldOut, updatedAt`.

**404** `NOT_FOUND` — event tidak ada.

### 2.3 `GET /api/v1/queue-entries/me?eventId={eventId}` — My Entry

| Aspek | Detail |
|-------|--------|
| Auth | `visitorCookie` (`rb_visitor`) **atau** `visitorHeader` (`X-Visitor-Token`) |
| Query param | `eventId` — uuid, required |
| Rate limit | Per visitor; lampaui → 429 |

**Response 200 (`EntryStatus`):**
```json
{
  "entryId": "uuid",
  "seq": 12345,
  "status": "WAITING",
  "exactPosition": 45,
  "zone": "POSSIBLE",
  "claimDeadline": "2026-10-20T10:03:00Z",
  "reason": "optional pesan",
  "redirect": "/pay/... atau booking url (ADMITTED)"
}
```
`zone ∈ {GUARANTEED,POSSIBLE,NOT_ENOUGH}`. `exactPosition = seq - servingSeq - removedAhead`. Required: `entryId, seq, status`.

**404** `NOT_FOUND` — tak ada entry untuk visitor. **429** `RATE_LIMITED`.

### 2.4 `POST /api/v1/queue-entries/{entryId}/claim` — Claim Turn

| Aspek | Detail |
|-------|--------|
| Auth | `visitorCookie` atau `visitorHeader` |
| Path param | `entryId` — uuid |
| Rate limit | Per visitor; lampaui → 429 |

**Response 200 (`ClaimResponse`):**
```json
{ "admissionToken": "jwt-eddsa one-time (TTL 60s)", "bookingUrl": "https://.../book/{entryId}" }
```
Required: `admissionToken, bookingUrl`.

| Status | Code | Kondisi |
|--------|------|---------|
| 200 | — | Entry `ADMITTED` & `now < claimDeadline` → token terbit, status `CLAIMED` |
| 410 | `TURN_MISSED` | `now >= claimDeadline` atau status `MISSED` |
| 410 | `EVENT_SOLD_OUT` | Event sold out saat claim |
| 404 | `NOT_FOUND` | Entry tak ada untuk visitor |
| 429 | `RATE_LIMITED` | Rate limit |

> **Delta vs SDD:** SDD §3.4 menggunakan `queueToken` Bearer + `403 CLAIM_DEADLINE_PASSED`/`403 ENTRY_NOT_ADMITTED`. Kontrak aktual memakai visitor auth + `410 TURN_MISSED`. Spec mengikuti kontrak.

### 2.5 `DELETE /api/v1/queue-entries/{entryId}` — Leave Queue

| Aspek | Detail |
|-------|--------|
| Auth | `visitorCookie` atau `visitorHeader` |
| Path param | `entryId` — uuid |

| Status | Code | Kondisi |
|--------|------|---------|
| 204 | — | Keluar; status `LEFT`, constraint keys dihapus, slot dirilis bila `ADMITTED` |
| 404 | `NOT_FOUND` | Entry tak ada |

### 2.6 `GET /health` — *(implemented)*

200 `UP` / 503 `DOWN`; header `Cache-Control: no-store`; body `Health { status, service, checkedAt, dependencies[] }`. Ping Valkey + PostgreSQL + RabbitMQ via `healthcheck.New(clients.Pingers(), cfg.HTTP.HealthTimeout)`.

### 2.7 Internal API yang DIPANGGIL (outbound ke Booking)

Semua panggilan memakai `Authorization: Bearer <service token>` + `X-Request-ID` (platform R2.3) dan
breaker Queue→Booking (timeout 500 ms, ambang 5, cooldown 5 s, bulkhead 50 — PROPOSED SDD-RBT §4.6.2).
Kontrak normatif: `internal-booking.yaml` (K6).

**`POST /internal/v1/allocations:batch`** (Booking) — reserve alokasi untuk batch entry terdepan.
Request:
```json
{ "eventId":"uuid", "entries":[ {"queueEntryId":"uuid","seq":182341,"visitorId":"uuid","buyerNikHash":"hex",
  "buyerNikMasked":"3174********0001","buyerEmail":"…","accountId":"uuid|null"} ] }
```
Response:
```json
{ "results":[ {"queueEntryId":"uuid","outcome":"GRANTED","allocationId":"uuid","units":3,"claimDeadline":"2026-10-20T10:03:00Z"} ] }
```
`outcome ∈ {GRANTED, NO_SUPPLY_NOW, SOLD_OUT, BUYER_QUOTA_EXHAUSTED, ALREADY_ACTIVE}`. Booking
menghitung `units`; Queue tidak mengirim `units`. `buyerEmail` didekripsi dari `emailEnc` hanya untuk
panggilan ini (tidak dilog). Idempoten per `queueEntryId` → aman diulang setelah timeout.

**`POST /internal/v1/allocations/{allocationId}:release`** (Booking) — kompensasi saga saat entry
`ADMITTED` ditinggalkan (`reason = QUEUE_ENTRY_GONE`) → `{ released }`.

**`GET /internal/v1/events/{id}/supply`** (Booking) — snapshot stok untuk Status Document; di-cache 1s di
Valkey oleh Queue (`q:{e}:supply`).

---

## 3. Data Models

### 3.1 Valkey Keys (state panas, target)

Semua key per-event memakai **hash tag** `{e:<eventId>}` agar operasi Lua atomik berada di satu slot cluster.

| Key | Type | Isi | TTL | Operasi utama |
|-----|------|-----|-----|---------------|
| `q:{e:<eventId>}:seq` | String (int) | Counter sequence | Persistent | `INCR` (hanya di `join.lua`) |
| `q:{e:<eventId>}:waiting` | Sorted Set | member=entryId, score=seq | Persistent | `ZADD`, `ZRANGEBYSCORE`, `ZREM`, `ZCARD` |
| `q:{e:<eventId>}:active` | Sorted Set | member=entryId, score=claimDeadlineMs | Persistent | `ZADD`, `ZRANGEBYSCORE 0 now`, `ZCARD`, `ZREM` |
| `q:{e:<eventId>}:entry:<entryId>` | Hash | field entry (§3.2) | Persistent | `HSET`, `HGETALL`, `HGET` |
| `q:{e:<eventId>}:nik:<buyerNikHash>` | String | entryId aktif (constraint NIK) | Persistent | `GET`/`SET`/`DEL` (di Lua) |
| `q:{e:<eventId>}:visitor:<visitorId>` | String | entryId aktif (constraint device) | Persistent | idem |
| `q:{e:<eventId>}:email:<emailHash>` | String | entryId aktif (constraint email) | Persistent | idem |
| `q:{e:<eventId>}:quota_exhausted` | Set | buyerNikHash kuota habis | Persistent | `SADD`, `SISMEMBER` |
| `q:{e:<eventId>}:status` | String | Status Document JSON | **1s** | `SET ... EX 1` (Status Publisher) |
| `q:{e:<eventId>}:sale` | Hash | `saleState`, `soldOut`, `servingSeq` | Persistent | `HSET`/`HGETALL` |
| `rl:join:email:<emailHash>` | String (int) | rate-limit counter | window (mis. 60s) | `INCR`+`EXPIRE` |
| `rl:join:ip:<ip>` | String (int) | rate-limit counter | window | `INCR`+`EXPIRE` |

> `emailHash` = HMAC email (sejalan privasi); plaintext email hanya di `emailEnc` (terenkripsi) pada hash entry.

### 3.2 Entry Hash Fields (`q:{e}:entry:<entryId>`)

```
seq            (int)     nomor FIFO
visitorId      (uuid)
buyerNikHash   (hex)     HMAC-SHA256(NIK)
emailEnc       (bytes)   email terenkripsi (AES-256-GCM)
accountId      (uuid|"") NULL/kosong untuk guest (ADR-020)
status         (enum)    WAITING|ADMITTED|CLAIMED|MISSED|LEFT|SOLD_OUT|CLOSED|COMPLETED|ENDED
joinedAtMs     (int)
admittedAtMs   (int)
claimedAtMs    (int)
claimDeadlineMs(int)
allocationId   (uuid)    diisi saat ADMITTED (dari Booking)
```

### 3.3 PostgreSQL schema `queue` — *(migrasi implemented)*

Sumber: `deployments/migrations/0001_init.up.sql` + `0002_account_ref.up.sql`. **Ini yang aktual.** PostgreSQL = **audit & recovery**, bukan jalur panas.

**`event_settings`** (PK `event_id`):
| Kolom | Type | Default | Constraint |
|-------|------|---------|-----------|
| `event_id` | uuid | — | PRIMARY KEY |
| `active_slot_capacity` | integer | 500 | CHECK 1..100000 |
| `near_front_multiplier` | integer | 2 | CHECK 1..10 |
| `status_publish_interval_ms` | integer | 1000 | CHECK 250..5000 |
| `poll_far_ms` | integer | 30000 | CHECK 5000..60000 |
| `poll_mid_ms` | integer | 10000 | CHECK 2000..30000 |
| `poll_near_ms` | integer | 3000 | CHECK 1000..5000 |
| `one_active_per_visitor` | boolean | true | — |
| `one_active_per_email` | boolean | true | — |
| `category_limited_threshold_pct` | integer | 5 | CHECK 1..50 |
| `pre_turn_notify_enabled` | boolean | true | — |
| `updated_at` | timestamptz | now() | — |

Table CHECK: `poll_near_ms <= poll_mid_ms <= poll_far_ms` dan `status_publish_interval_ms <= poll_near_ms`.

> **Catatan sale window:** `sale_opens_at`/`sale_closes_at` **tidak** ada di migrasi aktual (SDD menyebutnya). Sampai migrasi ditambahkan, sale window disimpan di Valkey `q:{e}:sale` atau diturunkan dari supply Booking. `EVENT_NOT_ON_SALE` tetap kode error target.

**`entry_audit`** (append-only, ditulis asinkron/batch):
| Kolom | Type | Constraint |
|-------|------|-----------|
| `id` | bigserial | PRIMARY KEY |
| `event_id` | uuid | NOT NULL |
| `entry_id` | uuid | NOT NULL |
| `seq` | bigint | NOT NULL |
| `buyer_nik_hash` | bytea | NOT NULL |
| `action` | text | NOT NULL, CHECK ∈ `{JOINED,ADMITTED,CLAIMED,MISSED,LEFT,COMPLETED,ENDED,SOLD_OUT,CLOSED,REJECTED}` |
| `occurred_at` | timestamptz | NOT NULL |
| `metadata` | jsonb | NOT NULL DEFAULT `{}` |
| `account_id` | uuid | NULL (guest), diisi bila login (ADR-020) |

Index: `idx_entry_audit_event_seq (event_id, seq)`, `idx_entry_audit_entry (entry_id, occurred_at)`.

> **Tidak ada `consumer_inbox`** (v4.6): consumer idempoten lewat Lua `end_entry`, `SADD`, dan guard HSET — bukan tabel inbox.

---

## 4. Lua Scripts (atomisitas antrean — WAJIB)

Per `.kiro/steering/code-principles.md`: **Lua hanya untuk atomisitas; aturan bisnis lain di Go.** Go membangun KEYS/ARGV, memanggil `EVALSHA`, lalu memetakan hasil `{STATUS, ...}` ke error domain. Semua KEYS satu skrip memakai hash tag `{e}` sama. Skrip disimpan di `services/queue/assets/lua/`.

### 4.1 `join.lua` — atomic join dengan semua constraint

```lua
-- KEYS[1]=seq  KEYS[2]=waiting  KEYS[3]=entry:<id>
-- KEYS[4]=nik:<h>  KEYS[5]=visitor:<v>  KEYS[6]=email:<eh>  KEYS[7]=quota_exhausted
-- ARGV: 1=entryId 2=visitorId 3=nikHash 4=emailEnc 5=accountId 6=nowMs
--       7=oneActivePerVisitor("1"/"0") 8=oneActivePerEmail("1"/"0")

-- (1) kuota habis?
if redis.call('SISMEMBER', KEYS[7], ARGV[3]) == 1 then
  return {'ERR', 'BUYER_QUOTA_EXHAUSTED'}
end
-- (2) NIK sudah antre? (idempoten: kembalikan entry yang ada)
local nik = redis.call('GET', KEYS[4])
if nik then return {'EXISTING', 'NIK', nik} end
-- (3) visitor sudah antre?
if ARGV[7] == '1' then
  local v = redis.call('GET', KEYS[5])
  if v then return {'EXISTING', 'VISITOR', v} end
end
-- (4) email sudah antre?
if ARGV[8] == '1' then
  local em = redis.call('GET', KEYS[6])
  if em then return {'EXISTING', 'EMAIL', em} end
end
-- (5) sequence + entry + waiting + constraint keys (semua-atau-tidak)
local seq = redis.call('INCR', KEYS[1])
redis.call('HSET', KEYS[3],
  'seq', seq, 'visitorId', ARGV[2], 'buyerNikHash', ARGV[3],
  'emailEnc', ARGV[4], 'accountId', ARGV[5],
  'status', 'WAITING', 'joinedAtMs', ARGV[6])
redis.call('ZADD', KEYS[2], seq, ARGV[1])
redis.call('SET', KEYS[4], ARGV[1])
if ARGV[7] == '1' then redis.call('SET', KEYS[5], ARGV[1]) end
if ARGV[8] == '1' then redis.call('SET', KEYS[6], ARGV[1]) end
return {'OK', seq, 'WAITING'}
```
**Mapping Go:** `OK`→201 WAITING; `EXISTING,NIK`→409 `NIK_ALREADY_IN_QUEUE` (atau idempotent-return bila Idempotency-Key cocok); `EXISTING,VISITOR`→409 `VISITOR_ALREADY_IN_QUEUE`; `EXISTING,EMAIL`→409 `EMAIL_ALREADY_IN_QUEUE`; `ERR,BUYER_QUOTA_EXHAUSTED`→409. Direct admission adalah langkah terpisah di Go (cek slot + supply) setelah `OK`.

### 4.2 `promote.lua` — atomic batch promotion (waiting → active)

```lua
-- KEYS[1]=waiting  KEYS[2]=active  KEYS[3..]=entry:<id> untuk tiap kandidat
-- ARGV: 1=capacity 2=nowMs ; lalu triplet per entry: entryId, allocationId, claimDeadlineMs
-- Dipanggil Go HANYA untuk entry yang sudah RESERVED oleh Booking (invarian "Jangan PHP").
local active = redis.call('ZCARD', KEYS[2])
local free = tonumber(ARGV[1]) - active
if free <= 0 then return {'FULL', active} end
local promoted = {}
local i = 3                     -- index ARGV triplet pertama
local k = 3                     -- index KEYS entry pertama
while free > 0 and ARGV[i] do
  local entryId   = ARGV[i]
  local allocId   = ARGV[i+1]
  local deadline  = ARGV[i+2]
  -- hanya promosikan bila masih WAITING (guard FIFO/idempoten)
  if redis.call('HGET', KEYS[k], 'status') == 'WAITING' then
    redis.call('ZREM', KEYS[1], entryId)
    redis.call('ZADD', KEYS[2], deadline, entryId)
    redis.call('HSET', KEYS[k], 'status','ADMITTED',
      'admittedAtMs', ARGV[2], 'allocationId', allocId, 'claimDeadlineMs', deadline)
    table.insert(promoted, entryId)
    free = free - 1
  end
  i = i + 3
  k = k + 1
end
return {'OK', promoted}
```
**Catatan:** urutan kandidat ditentukan Go dari `ZRANGEBYSCORE waiting 0 +inf LIMIT 0 N` (seq naik) → FIFO. Reservasi Booking terjadi **sebelum** skrip, sehingga skrip hanya memindahkan yang sudah dijamin.

### 4.3 `claim.lua` — atomic claim dengan deadline check

```lua
-- KEYS[1]=entry:<id>  KEYS[2]=active
-- ARGV: 1=entryId 2=nowMs 3=visitorId
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[3] then return {'ERR','NOT_FOUND'} end
if st == 'CLAIMED' then
  return {'OK','ALREADY'}                       -- idempoten: klaim ulang aman
end
if st ~= 'ADMITTED' then return {'ERR','TURN_MISSED'} end
local dl = tonumber(redis.call('HGET', KEYS[1], 'claimDeadlineMs'))
if tonumber(ARGV[2]) >= dl then
  redis.call('HSET', KEYS[1], 'status', 'MISSED')
  return {'ERR','TURN_MISSED'}
end
redis.call('HSET', KEYS[1], 'status','CLAIMED', 'claimedAtMs', ARGV[2])
return {'OK','CLAIMED'}
```
**Mapping:** `OK,CLAIMED`/`OK,ALREADY`→200 (Go terbitkan `admissionToken` EdDSA TTL 60s + `bookingUrl`); `ERR,TURN_MISSED`→410; `ERR,NOT_FOUND`→404.

### 4.4 `leave.lua` — atomic leave dengan cleanup

```lua
-- KEYS[1]=entry:<id> KEYS[2]=waiting KEYS[3]=active
-- KEYS[4]=nik:<h> KEYS[5]=visitor:<v> KEYS[6]=email:<eh>
-- ARGV: 1=entryId 2=visitorId 3=nowMs
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'ERR','NOT_FOUND'} end
if redis.call('HGET', KEYS[1], 'visitorId') ~= ARGV[2] then return {'ERR','NOT_FOUND'} end
redis.call('ZREM', KEYS[2], ARGV[1])
local wasActive = redis.call('ZREM', KEYS[3], ARGV[1])   -- rilis slot bila ADMITTED
-- hapus constraint keys HANYA bila masih menunjuk entry ini
if redis.call('GET', KEYS[4]) == ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5]) == ARGV[1] then redis.call('DEL', KEYS[5]) end
if redis.call('GET', KEYS[6]) == ARGV[1] then redis.call('DEL', KEYS[6]) end
redis.call('HSET', KEYS[1], 'status', 'LEFT')
return {'OK', wasActive}                                 -- wasActive=1 → slot bebas, picu promosi
```

### 4.5 `end_entry.lua` — guard idempoten untuk consumer `booking.journey.ended`

```lua
-- KEYS[1]=entry:<id> KEYS[2]=active KEYS[3..5]=nik/visitor/email keys
-- ARGV: 1=entryId 2=outcome 3=nowMs
local st = redis.call('HGET', KEYS[1], 'status')
if not st then return {'IGNORED','NO_ENTRY'} end
-- terminal sudah → idempoten no-op
if st=='COMPLETED' or st=='ENDED' or st=='LEFT' or st=='MISSED' then return {'IGNORED', st} end
redis.call('ZREM', KEYS[2], ARGV[1])                     -- rilis slot
local final = (ARGV[2]=='PAID') and 'COMPLETED' or 'ENDED'
redis.call('HSET', KEYS[1], 'status', final)
-- bersihkan constraint agar NIK/visitor/email bisa dipakai lagi bila boleh
if redis.call('GET', KEYS[3])==ARGV[1] then redis.call('DEL', KEYS[3]) end
if redis.call('GET', KEYS[4])==ARGV[1] then redis.call('DEL', KEYS[4]) end
if redis.call('GET', KEYS[5])==ARGV[1] then redis.call('DEL', KEYS[5]) end
return {'OK', final}
```

---

## 5. State Machine (lifecycle entry)

```
                      join (slot penuh)
          ┌──────────────────────────────────► WAITING
          │                                      │  │  │
   (new) ─┤ join (slot bebas, direct admission)  │  │  └── leave ────────────► LEFT
          │                                      │  │
          └──────────────────────────────────►  │  └── booking.sold_out ────► SOLD_OUT / CLOSED
                                                 │
                     promote / direct (GRANTED)  │   └── BUYER_QUOTA_EXHAUSTED ─► ENDED (reason)
                                                 ▼
                                             ADMITTED
                                             │   │   │
                     claim (now<deadline) ───┘   │   └── sweeper: now>=deadline ─► MISSED
                                                 │        (atau journey.ended UNCLAIMED dari Booking)
                                                 ▼
                                             CLAIMED
                                                 │
                          booking.journey.ended  │
                   outcome=PAID ─────────────────┼────────────► COMPLETED (terminal)
                   outcome≠PAID ─────────────────┴────────────► ENDED (terminal)
```

| Status | Masuk dari | Keluar ke | Pemicu |
|--------|-----------|-----------|--------|
| `WAITING` | (new) | ADMITTED, LEFT, SOLD_OUT/CLOSED, ENDED | join (penuh) |
| `ADMITTED` | (new) direct / WAITING | CLAIMED, MISSED, LEFT | promote / direct admission (setelah `GRANTED`) |
| `CLAIMED` | ADMITTED | COMPLETED, ENDED | claim sukses |
| `MISSED` | ADMITTED | (terminal) | Claim Sweeper (deadline lewat) atau `journey.ended(UNCLAIMED)` |
| `ENDED` (dari WAITING) | WAITING | (terminal) | outcome `BUYER_QUOTA_EXHAUSTED` (`reason`), audit `REJECTED` |
| `LEFT` | WAITING/ADMITTED | (terminal) | DELETE entry |
| `SOLD_OUT`/`CLOSED` | WAITING | (terminal) | `booking.sold_out.v1` |
| `COMPLETED` | CLAIMED | (terminal) | `journey.ended` outcome=PAID |
| `ENDED` | CLAIMED | (terminal) | `journey.ended` outcome≠PAID |

Status terminal: `MISSED, LEFT, SOLD_OUT, CLOSED, COMPLETED, ENDED` (tak ada transisi keluar; consumer idempoten mengabaikan event ulang).

---

## 6. Event Contracts

Semua event dibungkus **envelope** (`docs/contracts/events/envelope.schema.json`): `{ messageId(uuid), eventType, schemaVersion(int≥1), occurredAt, producer, aggregateId, correlationId?, payload }`. Exchange `rusuh.events` (topic). `additionalProperties:false` di payload → tak ada data pribadi bocor.

### 6.1 Published (producer = `queue-service`)

Routing key berversi (`<event>.v1`, K8). Queue mem-publish langsung (publisher confirm), tanpa outbox
(ADR-010); gagal publish → dicatat dan dilewati (push best effort).

**`queue.turn.soon` v1** (routing key `queue.turn.soon.v1`) → Notification:
```json
{ "eventId":"uuid", "queueEntryId":"uuid", "visitorId":"uuid" }
```
Required: `eventId, queueEntryId, visitorId`. Dipublikasikan saat posisi ≤ `preTurnNotifyPosition` (sekali per entry).

**`queue.turn.arrived` v1** (routing key `queue.turn.arrived.v1`) → Notification:
```json
{ "eventId":"uuid", "queueEntryId":"uuid", "visitorId":"uuid", "claimDeadline":"date-time" }
```
Required: `eventId, queueEntryId, visitorId, claimDeadline`. Dipublikasikan saat entry di-admit.

> **Delta vs SDD §7.2:** SDD menulis payload `{ entryId, eventId, pushToken, position }`. Kontrak aktual memakai `queueEntryId`/`visitorId` dan **tanpa** `pushToken`/`position` — Notification me-resolve kontak sendiri. Spec mengikuti kontrak.

### 6.2 Consumed (dari Booking)

Consumer queue (SDD-RBT §4.6.2): `queue.journey-ended` ← `booking.journey.ended.v1`,
`queue.quota-exhausted` ← `booking.quota.exhausted.v1`, `queue.sold-out` ← `booking.sold_out.v1`.

**`booking.journey.ended` v1** → rilis slot & finalisasi entry (payload schema v1, bukan versi SDD-BOOKING yang memuat `bookingId`/`visitorId` — OQ-7; `outcome` untuk sesi habis — OQ-15):
```json
{ "eventId":"uuid", "queueEntryId":"uuid",
  "outcome":"PAID|EXPIRED|CANCELLED|LEFT_WITHOUT_BUYING|UNCLAIMED" }
```
Idempoten via `end_entry.lua` (guard status terminal). Entry `CLAIMED`: `outcome=PAID → COMPLETED`,
lainnya → `ENDED`. Entry `ADMITTED` + `UNCLAIMED` → `MISSED`. Entry terminal → no-op.

**`booking.quota.exhausted` v1** → blok NIK (pemicu di Booking: OQ-13):
```json
{ "eventId":"uuid", "buyerNikHash":"string" }
```
`SADD q:{e}:quota_exhausted buyerNikHash` (idempoten).

**`booking.sold_out` v1** → tutup antrean:
```json
{ "eventId":"uuid" }
```
Set `q:{e}:sale` `saleState=CLOSED, soldOut=true`; waiting berikutnya → `SOLD_OUT`.

### 6.3 Consumer resilience

- Prefetch `RABBITMQ_PREFETCH` (20), `RABBITMQ_WORKERS` (4).
- Retry berjenjang `RABBITMQ_RETRY_DELAYS` (`5s,30s,2m`), DLQ setelah `RABBITMQ_MAX_RETRIES` (3).
- Payload rusak/tak-terparse → `rabbitmq.Permanent(err)` (langsung DLQ, bukan retry).
- Tanpa tabel inbox; idempotensi dari guard state + `SADD` + Lua.

---

## 7. Worker Jobs

Ketiga worker berjalan di proses `queue worker`. Karena `loop()` memakai satu `WORKER_INTERVAL`, tiap worker didaftarkan sebagai `Job` yang mengelola jadwalnya sendiri (goroutine + ticker internal) atau `jobs()` mengembalikan beberapa `Job` yang masing-masing idempoten per tick. Interval efektif di bawah bersumber dari `event_settings` (per-event) dengan fallback env.

### 7.1 Promotor

| Parameter | Nilai |
|-----------|-------|
| Interval | ~100ms (SDD §8.1; fallback env `QUEUE_PROMOTOR_INTERVAL_MS`) |
| Batch size N | `active_slot_capacity - len(active)` dibatasi batas aman (mis. ≤ 200/siklus) |
| Sumber urutan | `ZRANGEBYSCORE q:{e}:waiting 0 +inf LIMIT 0 N` (seq naik → FIFO) |

**Satu promotor per event (SRS-F-06-01):** sebelum tick, worker mengambil lease
`q:{e}:promoter = <instanceId>` (`SET NX PX <3×interval>`, diperpanjang tiap tick oleh pemilik). Instance
tanpa lease melewati event itu. Lease hilang (instance mati) → instance lain mengambil alih setelah PX.

**Algoritma per event (tiap tick, pemegang lease):**
1. `free = active_slot_capacity - ZCARD(active)`; bila `free <= 0` → skip.
2. Ambil N=`free` entry teratas `waiting` (seq naik).
3. Panggil `POST /internal/v1/allocations:batch` (Booking) untuk N entry (urut seq). Error/breaker OPEN → skip tick (retry tick berikutnya, idempoten per `queueEntryId`).
4. Untuk tiap hasil, urut seq:
   - `GRANTED` → kumpulkan (entryId, allocationId, claimDeadline).
   - `NO_SUPPLY_NOW` → berhenti memproses sisa batch pada tick ini (FIFO); entry tetap di waiting.
   - `BUYER_QUOTA_EXHAUSTED` → `SADD quota_exhausted`, entry → `ENDED` (`reason`), hapus constraint key, audit `REJECTED`.
   - `SOLD_OUT` → `HSET sale saleState=CLOSED soldOut=1`; entry `WAITING` → `SOLD_OUT` (bertahap oleh publisher).
   - `ALREADY_ACTIVE` → OQ-18 (sementara: tetap WAITING + alert).
5. `EVALSHA promote.lua` (atomik: waiting→active, ADMITTED) **hanya** untuk entry `GRANTED`.
6. Untuk tiap entry promoted → publish `queue.turn.arrived.v1`.
7. Append `entry_audit(action=ADMITTED)` (batch asinkron).

**Direct admission (join):** setelah `join.lua` → `OK`, bila `ZCARD(waiting)` hanya berisi entry ini dan
ada slot bebas, usecase memanggil `allocations:batch` untuk satu entry; `GRANTED` → `promote.lua` →
respons `ADMITTED`; selain itu respons `WAITING` (promotor akan mencoba lagi). Tidak pernah promote
berdasarkan supply snapshot saja (invarian #2).

### 7.2 Status Publisher

| Parameter | Nilai |
|-----------|-------|
| Interval | `event_settings.status_publish_interval_ms` (default 1000ms; CHECK ≤ `poll_near_ms`) |
| TTL key | `SET q:{e}:status ... EX 1` (1 detik) |

**Algoritma per event (tiap tick):**
1. Hitung `servingSeq` (min seq active atau seq terakhir ditutup), `remainingTickets`/`admissionRatePerMin` dari supply (cache 1s), `guaranteedUntilSeq`/`possibleUntilSeq` dari stok + `near_front_multiplier`, `categories[].state` dari `category_limited_threshold_pct`, `poll{...}` dari `event_settings.poll_*`, `preTurnNotifyPosition`.
2. Serialize `StatusDocument` JSON; `SET q:{e}:status EX 1`.
3. Endpoint `GET /api/v1/queue-status/{eventId}` membaca key ini; CloudFront cache 1s → origin ≤ 1 hit/detik/event (TC-PERF-01).

### 7.3 Claim Sweeper

| Parameter | Nilai |
|-----------|-------|
| Interval | ~10s (SDD §8.3) |
| Scan | `ZRANGEBYSCORE q:{e}:active 0 <nowMs>` (deadline sudah lewat) |

**Algoritma per event (tiap tick):**
1. `expired = ZRANGEBYSCORE active 0 now`.
2. Untuk tiap entry `expired` yang masih `ADMITTED`:
   - set status `MISSED`, `ZREM active` (rilis slot) — atomik via `end_entry.lua` (guard sama dengan consumer).
   - **tidak** publish event: Booking session sweeper melepas alokasi `RESERVED` saat `expires_at` (= claimDeadline) lewat dan menerbitkan `booking.journey.ended.v1 (UNCLAIMED)` sendiri (K7).
   - append `entry_audit(action=MISSED)`.
3. Slot yang bebas akan dipakai Promotor pada siklus berikutnya (FIFO berikutnya).

### 7.4 Release Retrier (kompensasi saga)
Entry `LEFT` yang sebelumnya `ADMITTED` dicatat di `q:{e}:release_pending` (hash entryId→allocationId).
Tiap tick: `POST /internal/v1/allocations/{allocationId}:release`; `released` diterima (true/false) →
`HDEL`. Gagal → coba tick berikut; setelah claimDeadline lewat entri dihapus (Booking sweeper sudah
mengakhiri admission).

---

## 8. Sequence Diagrams

### 8.1 Join & Direct Admission (HLD §4.4.1)

```
Klien            JoinHandler        JoinQueue UC      Valkey(Lua)       Booking         AuditRepo
  │  POST queue-entries │               │                 │               │               │
  ├────────────────────►│  validate     │                 │               │               │
  │                     ├──────────────►│ email/confirm/   │               │               │
  │                     │               │ consent/NIK (Go) │               │               │
  │                     │               ├─ resolve nikHash per auth state   │               │
  │                     │               ├─EVALSHA join.lua─►│               │               │
  │                     │               │  OK,seq,WAITING ◄─┤               │               │
  │                     │               ├─ (paling depan & slot bebas?) allocations:batch ─►│  │
  │                     │               │  GRANTED → promote.lua (ADMITTED)  │  (else WAITING)│
  │                     │               ├─ append(JOINED) ──────────────────────────────────►│
  │  201 WAITING/ADMITTED◄──────────────┤                 │               │               │
  │                     │               │  (Valkey down → 503 DEPENDENCY_UNAVAILABLE)        │
```

### 8.2 Promotion & Claim (HLD §4.4.2)

```
Promotor(worker)      Valkey        Booking            RabbitMQ        Klien       ClaimHandler
  │ tick(~100ms)        │              │                  │             │              │
  ├ free=cap-ZCARD(active)            │                  │             │              │
  ├ ZRANGEBYSCORE waiting 0 +inf N ──►│                  │             │              │
  ├ allocations:batch ───────────────►│ GRANTED(+deadline)             │              │
  ├ EVALSHA promote.lua (ADMITTED) ──►│                  │             │              │
  ├ publish queue.turn.arrived ───────────────────────► │────────────►(Notification)  │
  │                     │              │                  │  POST claim │              │
  │                     │              │                  │             ├─────────────►│
  │                     │ EVALSHA claim.lua◄──────────────────────────────────────────┤
  │                     │  OK,CLAIMED  │                  │             │  issue token │
  │                     │              │                  │  200 {admissionToken,bookingUrl}◄─┤
Sweeper(~10s): ZRANGEBYSCORE active 0 now → MISSED (tanpa publish; Booking menerbitkan journey.ended UNCLAIMED)
```

### 8.3 Status polling (CDN-first)

```
Klien ──GET queue-status──► CloudFront ──(cache miss, max 1/s)──► ALB → Queue origin
   ▲                            │ (cache hit <1s, tidak ke origin)
   └── StatusDocument ──────────┘
Status Publisher (worker): tiap status_publish_interval_ms → SET q:{e}:status EX 1
```

---

## 9. Error Catalog (lengkap)

| errorCode | HTTP | Endpoint | Kondisi |
|-----------|------|----------|---------|
| `VALIDATION_ERROR` | 400 | join | field hilang/malformed, `consentNik != true` |
| `NIK_INVALID` | 422 | join | `buyerNik` tidak cocok `^[0-9]{16}$` |
| `NIK_REQUIRED` | 422 | join | guest/login-tanpa-NIK tapi `buyerNik` kosong |
| `EMAIL_MISMATCH` | 422 | join | `email != emailConfirmation` |
| `NIK_ALREADY_IN_QUEUE` | 409 | join | NIK punya entry aktif |
| `VISITOR_ALREADY_IN_QUEUE` | 409 | join | device punya entry aktif (`one_active_per_visitor`) |
| `EMAIL_ALREADY_IN_QUEUE` | 409 | join | email punya entry aktif (`one_active_per_email`) |
| `BUYER_QUOTA_EXHAUSTED` | 409 | join | NIK di `quota_exhausted` |
| `EVENT_NOT_ON_SALE` | 409 | join | di luar sale window |
| `EVENT_SOLD_OUT` | 410 | join, claim | tiket habis |
| `TURN_MISSED` | 410 | claim | `now >= claimDeadline` atau status `MISSED` |
| `NOT_FOUND` | 404 | status, me, claim, leave | event/entry tak ada |
| `ENTRY_MOVED` | 409/410 | me/claim (device lama) | entry dipindahkan ke device lain |
| `RATE_LIMITED` | 429 | join, me, claim | rate limit (+ `Retry-After`) |
| `DEPENDENCY_UNAVAILABLE` | 503 | join, claim | Valkey/Booking down — fail closed, retry idempotent |

Pemetaan error domain → HTTP di `infrastructure/http` memakai `pkg/apperror`. Handler selalu mengembalikan DTO lewat `domain/mapper`, tidak pernah entity. Envelope error tidak pernah memuat SQL, stack trace, secret, atau payload provider.

**Error domain ↔ code (konstruktor di `domain/`):**
`ErrValidation→VALIDATION_ERROR(400)`, `ErrNikInvalid→NIK_INVALID(422)`, `ErrNikRequired→NIK_REQUIRED(422)`, `ErrEmailMismatch→EMAIL_MISMATCH(422)`, `ErrNikInQueue→NIK_ALREADY_IN_QUEUE(409)`, `ErrVisitorInQueue→VISITOR_ALREADY_IN_QUEUE(409)`, `ErrEmailInQueue→EMAIL_ALREADY_IN_QUEUE(409)`, `ErrQuotaExhausted→BUYER_QUOTA_EXHAUSTED(409)`, `ErrNotOnSale→EVENT_NOT_ON_SALE(409)`, `ErrSoldOut→EVENT_SOLD_OUT(410)`, `ErrTurnMissed→TURN_MISSED(410)`, `ErrEntryNotFound→NOT_FOUND(404)`, `ErrEntryMoved→ENTRY_MOVED`, `ErrRateLimited→RATE_LIMITED(429)`, `ErrDependencyUnavailable→DEPENDENCY_UNAVAILABLE(503)`.

---

## 10. Configuration (D-24)

*Implemented* di `internal/config` (`config.go`, `load.go`): `APP_*`, `LOG_*`, `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL`, `POSTGRES_*` (+`POSTGRES_SCHEMA=queue`), `VALKEY_*` (`VALKEY_SERVER` JSON array), `RABBITMQ_*` (exchange `rusuh.events` topic, retry delays, prefetch, max retries, confirm timeout). Semua divalidasi `Config.Validate` saat start — gagal cepat, semua error dilaporkan sekaligus (`errors.Join`).

**Target** (ditambah saat fitur dibangun): `BOOKING_SERVICE_URL`; kunci privat penerbit visitor/queue/admission token + `kid` (pola `TOKEN_*_FILE` seperti User), kunci publik User (verifikasi `userBearer`), kunci privat service token; `PII_HASH_KEY` (**nama SDD-RBT §6.1**, sama dengan Booking/User — spec lama menulis `PII_HMAC_KEY`) dan kunci enkripsi email; `ADMISSION_TOKEN_TTL_SECONDS=60` (30–120, < claim window); `QUEUE_PROMOTOR_INTERVAL_MS`, `QUEUE_SWEEPER_INTERVAL_MS`, `RATE_LIMIT_*` *(tiga terakhir usulan spec, belum ada di SDD-RBT §4.10 [asumsi])*.

> **Sumber kebenaran parameter antrean = `event_settings` (per-event), bukan env.** SDD §9 menyebut `QUEUE_MAX_ACTIVE_SLOTS`/`QUEUE_CLAIM_WINDOW_SECONDS`/`QUEUE_POLLING_INTERVAL_MS` sebagai env, tetapi migrasi aktual menaruh nilai setara di `event_settings` (`active_slot_capacity`, `status_publish_interval_ms`, `poll_*_ms`). Desain ini mengutamakan `event_settings` (dapat diubah tanpa deploy); env hanya default/fallback untuk interval worker.

---

## 11. Observability

- **Metrics** (target): `rbt_queue_joins_total{event_id,status}`, `rbt_queue_claims_total{event_id,status}`, `rbt_queue_waiting_count{event_id}`, `rbt_queue_active_count{event_id}`, `rbt_queue_promotions_total{event_id}`, `rbt_queue_unclaimed_total{event_id}`.
- **Alerts**: `QueuePromotorStalled` (tak ada promosi 1m saat sale → Critical), `QueueUnclaimedRateHigh` (>5% 5m → Warning), `QueueValkeyLatencyHigh` (p99 > 50ms 5m → Warning).
- **Health** *(implemented)*: `GET /health` → 200 UP / 503 DOWN; ping Valkey + PostgreSQL + RabbitMQ (`healthcheck.New(clients.Pingers(), cfg.HTTP.HealthTimeout)`), untuk ALB & container `HEALTHCHECK` (D-23).
- **Log**: `slog` JSON + `RequestID()` middleware (*implemented* `httpx.Chain`). NIK/email/token plaintext tidak pernah masuk log (security-privacy steering).

---

## 12. Keamanan & Privasi

- NIK divalidasi `pkg/nik` lalu hanya disimpan sebagai `HMAC-SHA256(NIK, PII_HASH_KEY)`; plaintext tidak disimpan/dilog. Email disimpan terenkripsi AES-256-GCM (`emailEnc`), `emailHash` HMAC untuk constraint.
- `admissionToken`/`queueToken`/`visitorToken` EdDSA via `pkg/token`; `admissionToken` sekali-pakai TTL 60s, terbit **hanya** lewat jalur antrean (NFR-07).
- Fail closed: Valkey/Booking down → join/claim ditolak (`503 DEPENDENCY_UNAVAILABLE`).
- Rate limit per email + IP di Valkey; cookie `rb_visitor` HttpOnly/Secure/SameSite=Lax.
- Payload event tanpa data pribadi (kontrak `additionalProperties:false`).

---

## 13. Testing

Mengikuti `.kiro/steering/testing-review.md` (testify, table-driven, mock `_mock/` dari mockgen, integration build tag `integration`).

| Level | Cakupan | TC |
|-------|---------|----|
| Unit (usecase) | validasi join (email/confirm/consent/NIK), resolve NIK per auth state, mapping error, posisi = `seq-serving-removedAhead`, zona | TC-GST-02, TC-QUE-01, TC-QUE-03, TC-QUE-06, TC-QUE-10, TC-QUE-11 |
| Unit (domain) | state machine Entry (transisi valid/terminal), value object Seq/Status/Zone | TC-QUE-09 |
| Integration (Valkey+Lua) | atomisitas `join.lua`, constraint NIK/visitor/email, 10.000 join paralel tanpa celah seq, FIFO lintas promosi, `claim.lua` deadline, `leave.lua` cleanup | TC-GST-03..05, TC-QUE-07, TC-QUE-08, TC-QUE-14, TC-QUE-17 |
| Integration (consumer) | idempoten kirim-ganda `journey.ended`/`quota.exhausted`/`sold_out`; payload rusak → DLQ | TC-QUE-05 |
| Integration (PG) | append `entry_audit`, recovery dari audit | — |
| Integration (promotor) | promote hanya setelah `GRANTED` (Jangan PHP), satu promotor per event (dua instance), slot aktif tinggi tanpa oversell | TC-PERF-04 |
| E2E | guest flow, logged-in (NIK stored / no NIK), direct admission, leave & rejoin, move entry, missed turn | TC-GST-01, TC-QUE-02, TC-QUE-05, TC-USR-09/09a, TC-QUE-16, TC-QUE-17 |
| Load (k6) | 20.000 join/s, cache hit Status Document, p95<1s, sold-out notif ≤5s | TC-PERF-01..03, TC-PERF-05 |

Coverage logika ≥ 80% (CI `case-coverage`); `cmd/`, `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan.

### 13.1 Build & Verify

```bash
# Build
make infra keys
cd services/queue && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api &        # :8081
go run ./cmd worker &

# Verify
curl -fsS localhost:8081/health            # 200 UP (implemented)
go build ./... && go test ./... -race
make test-integration                       # Valkey/PG/RabbitMQ nyata (integration tag)
make lint                                   # golangci-lint v2 + depguard (lapisan hexagonal)
make cover                                  # ambang 80%
```

---

## 13a. Correctness Properties

### Property 1: Gapless monotonic sequence
Untuk setiap himpunan join paralel pada satu event, nilai `seq` yang diterbitkan unik, naik, dan tanpa celah.
**Validates: Requirements 2.6, 2.7**

### Property 2: FIFO admission
Untuk setiap dua entry eligible dengan `seq_a < seq_b`, entry b tidak pernah `ADMITTED` lebih dulu dari a; frekuensi polling tidak mengubah urutan.
**Validates: Requirements 6.3, 6.6**

### Property 3: No admission without reservation
Untuk setiap entry `ADMITTED`, ada `allocationId` hasil `GRANTED` dari Booking.
**Validates: Requirements 2.5, 6.2**

### Property 4: One active entry per NIK / visitor / email
Untuk setiap event, paling banyak satu entry aktif per `buyerNikHash`, per `visitorId` (bila `one_active_per_visitor`), dan per `emailHash` (bila `one_active_per_email`).
**Validates: Requirements 1.5, 2.8, 2.9**

### Property 5: Idempotent consumers
Untuk setiap pengiriman ulang `booking.journey.ended.v1` / `quota.exhausted.v1` / `sold_out.v1`, state Valkey akhir sama dengan pengiriman tunggal.
**Validates: Requirements 8.1, 8.2, 8.3, 8.7**

### Property 6: Fail closed
Untuk setiap join/claim saat Valkey tidak terjangkau, respons `503 DEPENDENCY_UNAVAILABLE` dan tidak ada entry/seq yang tercipta setengah jadi.
**Validates: Requirement 2.13**

### Property 7: Single promoter
Untuk dua instance worker yang berjalan bersamaan, paling banyak satu mem-promote event yang sama pada satu waktu.
**Validates: Requirement 6.12**

### Property 8: No PII in Status Document & events
Untuk setiap Status Document dan payload `queue.turn.*`, tidak ada NIK, email, atau token.
**Validates: Requirements 4.7, 9.3**

---

## 14. Referensi

- `docs/analisa/04a-SDD-QUEUE-001.md` v1.1 (LLD, Appendix C Lua), HLD §4.4.1–4.4.2, ADR-003 (polling Status Document), ADR-016/019/020.
- Kontrak: `docs/contracts/openapi/queue.yaml` (normatif), `docs/contracts/openapi/internal-booking.yaml`, `docs/contracts/events/{envelope,queue.turn.soon,queue.turn.arrived,booking.journey.ended,booking.quota.exhausted,booking.sold_out}.*.schema.json`.
- Migrasi: `services/queue/deployments/migrations/0001_init.up.sql`, `0002_account_ref.up.sql`.
- Test Plan: `docs/analisa/06-TP-RBT-001.md` (TC-QUE-*, TC-GST-*, TC-USR-09*, TC-PERF-*).
- Steering: `.kiro/steering/{structure,code-principles,tech,security-privacy,invariants,testing-review}.md`.
