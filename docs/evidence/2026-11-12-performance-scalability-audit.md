# Performance & Scalability Audit (2026-11-12)

Read-only audit for the target of **millions of visitors** in one ticket war. Six subagents, one per service
(`queue`, `booking`, `payment`, `notification`, `user`) plus one for `pkg/` and infrastructure
(`deployments/`, Terraform, Dockerfiles, `.env.example`). Companion to `2026-11-12-service-anomaly-audit.md`
(correctness bugs).

- **Focus:** leaks (goroutine, connection, memory), heap/CPU on hot paths, efficiency, scalability, capacity.
- **Assumed load:** ~1M visitors in the waiting room, heartbeat every 30s (~33k/s), `me`/status polling, peak
  ~50k req/s at the edge, tens of thousands of holds/payments within minutes.
- **Method:** code reading only (local Go 1.18.1 cannot build modules that require 1.26.11). Throughput numbers are
  estimates (DB RTT ~0.4 ms, commit 1–3 ms, Midtrans/FCM 150 ms) and **must be validated with k6** (`tests/k6/`).
- **St:** C = confirmed in code, S = suspected (needs measurement/runtime check).
- **Owner:** nuzirwan = queue, booking, user; arnu12rahman = notification, payment, `pkg/`, `deployments/`.

## Re-check after commits `547e575`, `14286cd`, `d04cf0d` (2026-11-12)

Only Booking hold/security/HTTP and User profile/security changed; all other findings stand as written.

| Change | Effect on this report |
|---|---|
| `547e575` revert `useLoginNik` | Booking `user/nik_client.go` (no timeout, default transport) is gone → PF-P1-8 narrowed to the remaining clients. One fewer cross-service call on the hold path. |
| `14286cd` NIK hash prefix | No performance effect (data-migration risk tracked in the anomaly report, P1-19). |
| `d04cf0d` `GET /me` plaintext email + NIK | `GetProfile` now does **3 sequential SELECTs on the same row** (`GetByID`, `GetEmailEncByID`, `GetNIKEncByID`) + 2 AES-GCM opens per call → PF-P3-6 raised in importance (listed below). |
| `hold.go` line shifts | PF-P2-9 location updated (`GetDetail` now at `hold.go:275`). |

## Priority definitions

| Priority | Meaning |
|---|---|
| **P0 — Critical** | Will fail or collapse at the target load (hard ceiling far below need, cascade, or single point of failure). |
| **P1 — High** | Severe degradation at peak: big latency, user-visible errors, leaks that grow with traffic. |
| **P2 — Medium** | Noticeable waste or bottleneck under load; fix before the real event. |
| **P3 — Low** | Small allocations, hygiene, minor round trips. |

## Summary

| Area | P0 | P1 | P2 | P3 |
|---|---|---|---|---|
| Infrastructure / capacity | 3 | 1 | 1 | 1 |
| Shared `pkg/` (cross-service) | 2 | 3 | 0 | 2 |
| Queue | 1 | 5 | 5 | 2 |
| Booking | 2 | 3 | 4 | 1 |
| Payment | 1 | 2 | 2 | 1 |
| Notification | 0 | 4 | 4 | 1 |
| User | 0 | 2 | 2 | 1 |
| **Total** | **9** | **20** | **18** | **9** |

## Shared root causes (fix once, helps everywhere)

These showed up independently in several services; one change in `pkg/` or config fixes all of them.

| Root cause | Seen in | Item |
|---|---|---|
| RabbitMQ publisher: one channel, synchronous confirm under a mutex, confirm buffer of 1 | user, payment, booking, queue, notification | PF-P0-7 |
| Outbox relay capped at ~20 events/s (100 rows / 5s, serial confirms) | booking, payment, `pkg/outbox` | PF-P0-5 |
| Client IP = CDN edge IP → everyone shares one rate-limit bucket | queue, booking, payment, user | PF-P0-6 |
| Postgres pool 10, `MinConns=0`, no `statement_timeout`/`lock_timeout`, total > `max_connections` | all | PF-P0-9 |
| `http.DefaultTransport` (`MaxIdleConnsPerHost=2`), error bodies not drained | queue, booking, payment | PF-P1-8 |
| No request deadline middleware | all | PF-P1-9 |
| Access log line per request, `awslogs` blocking mode | all | PF-P1-10 |
| Tables / Valkey keys never purged | queue, booking, payment, user, notification | PF-P1-4, PF-P1-16, PF-P2-17 |
| RabbitMQ connection never reconnects, not in `/health` | user, notification | PF-P1-13 |

---

## P0 — Critical

| ID | Area | Cat | St | Location | Problem | Impact at scale | Fix |
|---|---|---|---|---|---|---|---|
| PF-P0-1 | Infra | capacity | C | `deployments/terraform/modules/data_host/user_data.sh.tftpl:31-35`, `variables.tf:68-81` | PostgreSQL, Valkey, RabbitMQ and Notification api+worker all run as containers on **one `t4g.small`** (2 burstable vCPU, 2 GiB). Postgres defaults (`max_connections=100`, `shared_buffers=128MB`), Valkey `--maxmemory 400mb noeviction`, single-node RabbitMQ. | One to two orders of magnitude short for millions of visitors; single point of failure; 1M queue entries × ~300 B ≈ 300 MB fills Valkey → `noeviction` makes joins fail. | RDS/Aurora (+RDS Proxy), ElastiCache Valkey (cluster mode), Amazon MQ or 3-node RabbitMQ, non-burstable instances. |
| PF-P0-2 | Infra | capacity | C | `deployments/terraform/modules/compute/main.tf:230-238`, `variables.tf` | App ASG is `t4g.small` (burstable), max 3 queue / 2 core instances, CPU-60% target tracking with 180s warmup. | The war spike arrives in seconds; reactive scaling takes minutes and caps at 5 small instances; burst credits run out under sustained load. | Scheduled/predictive scale-out before the sale, `ALBRequestCountPerTarget` metric, non-burstable (c7g) with realistic max, pre-warm. |
| PF-P0-3 | Queue | scalability | C | `internal/queue/infrastructure/valkey/queue_store.go:78` | Every per-event key uses the same hash tag `q:{e:<id>}` → one concert's whole hot path (join, heartbeat, me, claim, status doc, `hb:0..15` "shards") runs on **one Valkey shard / one thread**. Heartbeat = 3 cmds, me = 4–6 cmds. | Heartbeats ~100k ops/s + `me` polling 400k–1M ops/s vs ~150–250k ops/s per Valkey thread (less for EVAL). Cluster mode gives no relief because all keys share the slot. | Per-visitor reads (vtok, vlast, entry hash) under visitor-level hash tags; answer `me` from the cached status doc + seq; in-process status doc cache. |
| PF-P0-4 | Booking | scalability | C | `infrastructure/postgres/unit_of_work.go:84` (hold), `:474` (settle), `:735` (expire), `:313` (edit) | All stock writes for an event serialize on **one `event_supply` row**. Hold locks it at step 2 then runs 11–14 more statements (~8–12 ms); settle ~25 statements (~15–20 ms). Waiting tx hold pool connections. | Ceiling ~80–120 holds/s or ~50–70 mixed tx/s **per event**; waiters exhaust the 10-conn pool shared with catalog/SSE. | Do lock-free reads before the supply lock; single `UPDATE … RETURNING` at the end; multi-row inserts; long term shard supply into N rows per event or rely on `category_inventories` CHECKs. |
| PF-P0-5 | Booking + `pkg/outbox` + Payment | scalability | C | booking `infrastructure/rabbitmq/consumer.go:111-135`, `postgres/outbox_writer.go:21-26`; `pkg/outbox/relay.go:17-18,63-95`; payment `config/load.go:104` | Relay runs once per `WORKER_INTERVAL` (5s) with batch 100 → **max ~20 events/s**, serial publish+confirm, one UPDATE per row. Booking uses its **own** relay with a plain SELECT (no `SKIP LOCKED`) → every ASG instance publishes the same rows. `pkg/outbox` (payment) has SKIP LOCKED but holds the tx open during broker publishes. | Settle writes 2–4 outbox rows; at 200 payments/s backlog grows ~600/s → `journey.ended` minutes late → Queue doesn't free slots → admission stalls; duplicate e-ticket emails; long tx block vacuum. | Booking switches to `pkg/outbox.Relay`; loop immediately while `published == batch`; pipelined confirms; batched `UPDATE … WHERE id = ANY($1)`; claim+commit before publish (lease). |
| PF-P0-6 | Cross (`pkg/httpx`) | scalability | C | `pkg/httpx/request.go:61-75`; queue `join_handler.go:69`; booking `delivery/server.go:58-59`; payment `delivery/server.go:65-66` | `ClientIP` takes the rightmost XFF entry; prod runs behind Cloudflare/CloudFront, so that entry is the **edge IP**. User service uses hops but `APP_TRUSTED_PROXY_HOPS=1` only fits one topology. | All buyers behind one PoP share one bucket (queue join 200/min, booking 300/min) → mass 429 exactly at the war; with wrong hops XFF becomes spoofable. | `ClientIPHops` / `CF-Connecting-IP` trusted only from CDN ranges, same config in every service; ALB SG restricted to the CDN prefix list. |
| PF-P0-7 | `pkg/rabbitmq` | scalability/leak | C | `pkg/rabbitmq/publisher.go:68,81,93-127` | One channel per process, mutex held across publish **and** confirm wait; confirm channel buffer = 1; no `DeliveryTag` matching. A late confirm after timeout is read by the next publish; a second unread confirm blocks the amqp091 reader goroutine (heartbeats and consumer deliveries on that connection stall). | Throughput ≈ 1/RTT ≈ 300–1000 msg/s per process: OTP spike (~3.3k/s) queues on the mutex then 503s; outbox, promoter `turn.arrived`, retry republish all bottleneck; mis-attributed acks can mark unpublished outbox rows as published. | `PublishWithDeferredConfirmWithContext` + `WaitContext`, pool of N channels, larger confirm buffer / tag matching. |
| PF-P0-8 | Payment | scalability | C | `infrastructure/bookingclient/client.go:27-32,68`, `usecase/get_payment_status.go:29`, `get_payment_page.go:31`, `common.go` | Every `GET /status` (FE polls) and `GET /page` calls Booking `GetPayable` through a **hard-coded 20-slot bulkhead, wait=0**, shared with `Lock` on CreateAttempt. | ~400–1000 calls/s per instance; tens of thousands of buyers polling every 3s ≈ 10k rps → polling fills slots and **charges fail with 503** (money path). | Separate bulkheads per operation (Lock/charge priority), env-configurable, 50–100 ms wait; serve status from the local attempt + 1–2s payable cache or session claims. |
| PF-P0-9 | Cross (Postgres) | capacity | C | `.env.example` (`POSTGRES_MAX_CONNS=10`), `pkg/postgres/client.go:28-31`, `pkg/postgres/config.go:57-70`, `compute/user_data.sh.tftpl:30-32` | api+worker per service, pool 10 each: queue 3×20 + core 2×(booking 20 + payment 20 + user 10–20) + notification 20 ≈ **180–200 vs `max_connections=100`**. No PgBouncer, `MinConns=0`, no `statement_timeout`/`lock_timeout`, no acquire timeout. | `too many clients` on scale-out; cold-pool connection storm at sale open; lock waiters pile up forever; `/health` (same pool) times out → ALB pulls instances → cascade. | PgBouncer/RDS Proxy (transaction mode), per-service pool budget, `MinConns>0`, `statement_timeout` + `lock_timeout` (~2s) in DSN, acquire timeout, health on a dedicated conn. |

## P1 — High

| ID | Area | Cat | St | Location | Problem | Impact at scale | Fix |
|---|---|---|---|---|---|---|---|
| PF-P1-1 | Booking | scalability | C | `infrastructure/postgres/admission_repo.go:91-259`, `internal_handler.go:112` | `ReserveBatch` holds the supply lock for the whole batch, 6–8 RTT per entry (SAVEPOINT, lookup, repeated `FOR UPDATE`, UPDATE, INSERT, RELEASE); batch size unbounded (1 MB body ≈ 5k entries). | 500 entries ≈ 3,500 RTT ≈ 1.5–3s of supply lock; every hold/settle/expire of that event waits. | Cap batch (~100), lock supply once, bulk idempotency check (`= ANY`), one aggregate UPDATE, `INSERT … SELECT unnest(...)`. |
| PF-P1-2 | Booking | scalability | C | `usecase/sweepers.go:14-69`, `admission_repo.go:363-371`, `booking_repo.go:115-119` | Sweepers: 100 rows / 5s (~20 rows/s), serial, may call Payment `Resolve` (5s timeout) per booking, share one ticker with the relay, no `SKIP LOCKED` → all instances process the same rows. | Abandoned admissions (thousands/s at peak) release `allocated_units` far too slowly → false "sold out"; duplicate Resolve calls. | `FOR UPDATE SKIP LOCKED` claim, parallel workers, separate loop per job, repeat while batch is full. |
| PF-P1-3 | Booking | leak | C | `pkg/httpx/server.go:53-56`, booking `http/sse_handler.go:44-50`, `usecase/stream_session.go:81-167` | `WriteTimeout` 30s kills every SSE stream; loop ignores write errors and never checks `ExpiresAt` → goroutine keeps polling Valkey + `json.Marshal` every tick and holds the conn slot until TCP close. | Every buyer reconnects every ~30s (each reconnect: Postgres `GetByID` + INCR + EXPIRE); zombie goroutines accumulate. | `http.NewResponseController(w).SetWriteDeadline(time.Time{})`; exit on write error or expiry; `context.WithTimeout` for `DecrConn`. |
| PF-P1-4 | Queue | leak (memory) | C | `assets/lua/join.lua:125-131` | Entry hashes `entry:<id>`, `visitor:`, `account:`, `waiting`/`stale`, `seq` never get a TTL. | ~250–400 MB per 1M-visitor event never reclaimed; with `noeviction` writes fail. | `PEXPIRE` entry hash (~24h) on end/leave/liveness/sold-out and at join; expire per-event keys after the sale. |
| PF-P1-5 | Queue | efficiency | C | `infrastructure/worker/promoter.go:341-361` (defer order vs `:113`) | `notifyPreTurn` runs every 100 ms tick **while holding the lease**: `TopWaiting(capacity×multiplier≈3000)` then serial EXISTS per id, plus HGETALL + publish for new ones. | ~30k serial RTT/s; starves liveness (LEASE_LOST) and direct admission on join. | "Notified-up-to-seq" watermark, only new seqs, run outside the lease. |
| PF-P1-6 | Queue | scalability | C | `infrastructure/rabbitmq/consumer.go:105-114` | Sold-out drain capped at 20×500 = 10k entries per delivery; with `RABBITMQ_MAX_RETRIES=3` at most ~40k ended before DLQ. | ~960k WAITING visitors never marked SOLD_OUT and keep heartbeating/polling for nothing. | Drain in a worker job with a seq cursor, or republish a "continue" message not counted as a failure. |
| PF-P1-7 | Queue | efficiency | C | `application/usecase/join.go:175,177`, `infrastructure/postgres/entry_audit_repo.go:33` | Synchronous Postgres `entry_audit` INSERT on every join (twice on direct admission). | 16k joins/s → 16k+ inserts/s; join latency and pool exhaustion tied to Postgres. | Buffered async batch writer (COPY / multi-row INSERT), drop when full. |
| PF-P1-8 | Cross (HTTP clients) | efficiency/leak | C | queue `bookingclient/client.go:246,321-330`; booking `paymentclient/client.go:39` (`user/nik_client.go` removed in `547e575`); payment `midtrans/gateway.go:78-82`, `bookingclient/client.go:57-58,150-158`, `delivery/payment_module.go:68,75` | All inter-service and Midtrans clients use `http.DefaultTransport` (`MaxIdleConnsPerHost=2`); non-200 bodies closed without draining. (Re-check: Booking's timeout-less `NIKClient` was removed with `useLoginNik`.) | Constant TCP/TLS churn (+50–150 ms to Midtrans, ~20–30% bulkhead capacity lost), TIME_WAIT / ephemeral port exhaustion (~28k). | `pkg/httpx.NewClient` with tuned Transport (`MaxIdleConnsPerHost` ≥ bulkhead/100, `IdleConnTimeout` 90s, TLS timeout, HTTP/2), mandatory timeout, drain with `io.Copy(io.Discard, io.LimitReader(body, 64<<10))`. |
| PF-P1-9 | Cross (`pkg/httpx`) | scalability | C/S | `pkg/httpx/server.go:49-58` | No per-request deadline: `WriteTimeout` does not cancel the request ctx; valkey-go used without command timeout; `MaxHeaderBytes` default 1 MB; no load shedding. | If Valkey/Postgres stalls, request goroutines pile up (hundreds of thousands at 50k rps) → OOM. | Timeout middleware (~2–3s ctx), Valkey command timeout, lower `MaxHeaderBytes`, concurrency limiter / load shedding. |
| PF-P1-10 | Cross (logging) | efficiency | C | `compute/user_data.sh.tftpl` (`--log-driver awslogs` without `mode=non-blocking`), `pkg/httpx/accesslog.go:57,90-99`, `json.go:29-39` | Blocking `awslogs`: CloudWatch throttling blocks stdout → all request goroutines stall. One INFO line per 2xx, two lines per 4xx/5xx (WriteError + access log). | 50k rps × ~300 B ≈ 15 MB/s ≈ 54 GB/h (~$27/h ingest); 429 storms double it. | `--log-opt mode=non-blocking --log-opt max-buffer-size=8m`, sample 2xx/429, log WriteError only for 5xx. |
| PF-P1-11 | `pkg/httpx` | CPU/lock | C | `pkg/httpx/ratelimit.go:47-56` | In-memory limiter: once 100k keys are live, every new IP triggers an O(100k) sweep under one global mutex (~1–3 ms), then fails open. Per instance → N instances = N × limit. | Serializes all requests in the instance (throughput drops to hundreds of rps) and the limiter stops limiting exactly during the spike. | Periodic ticker sweep, sharded map, or move to Valkey (queue already has one). |
| PF-P1-12 | Notification | scalability | C | `usecase/notify_turn_arrived.go:45-99`, `pkg/rabbitmq/consumer.go:98`, queue `promoter.go:197,358` | `turn.arrived`: 3 DB RTT + blocking FCM call per message, 4 workers per replica, shares `notification.queue.events` with `turn.soon`. Promoter publishes for every admitted entry, not only subscribers. | ~27 pushes/s per replica → 100k pushes ≈ 1h; pushes past the claim deadline are discarded → buyers miss their turn. | Dedicated (or priority) queue for `turn.arrived`, 32–64 workers there, scale on queue depth; skip non-subscribers before DB work. |
| PF-P1-13 | Notification + User | leak/availability | C | notification `delivery/worker.go:24-50,82-107`, `clients.go:51`; user `delivery/user_module.go:293-311`, `clients.go:64-72` | Consumers exit on channel close and are never restarted; publisher never reconnects; `NotifyClose` unused; OTP connection not in `/health`; shutdown doesn't drain in-flight handlers. | After any broker blip: OTP 503 / no consumption until restart, while ALB keeps routing (health UP). | Reconnect loop on `NotifyClose` (or exit so the supervisor restarts), add AMQP conns to health pingers, errgroup + drain on shutdown. |
| PF-P1-14 | Notification | leak | C | `smtp/email_sender.go:57-63,141-186`, `emailguard/sender.go:20,62,217`, `pkg/resilience/breaker.go:180` | SMTP `deliver` ignores ctx; conn has no `SetDeadline`; breaker cancels at 3s but `SMTP_TIMEOUT` is 10s; bulkhead slot released while the send continues. | Stalled server leaks one goroutine + fd + MIME buffer per send; bulkhead doesn't bound real concurrency; retry re-sends → duplicate e-tickets. | `conn.SetDeadline` from ctx, close conn on `ctx.Done()`, align timeouts. |
| PF-P1-15 | Notification | scalability | C | `delivery/notification_module.go:74,98`, `usecase/notify_otp_requested.go:69-72` | OTP shares breaker and bulkhead (10, wait 0) with e-ticket mail; OTP has `MaxRetries=0`. | 5 e-ticket failures open the breaker → every OTP dropped → login breaks during the e-ticket burst; raising workers >5/queue causes `ErrBulkheadFull` and e-tickets hit DLQ. | Separate guard for OTP, bulkhead ≥ total workers or short wait, don't count bulkhead-full as a retry. |
| PF-P1-16 | User | scalability | C | `deployments/migrations/0001_init.up.sql`, `cmd/root.go:52`, `usecase/auth.go:305` | `sessions` and `login_audit` never purged: every refresh inserts a session (3 index entries), leaves a dead tuple, and writes a `REFRESH_OK` audit row. | ~8.3k refresh/s ≈ 1M rows/min → index bloat, autovacuum pressure, WAL growth. | Batched purge job (or time partitioning); drop/sample `REFRESH_OK` audit. |
| PF-P1-17 | User | efficiency | C | `usecase/auth.go:263-305,390`, `postgres/repositories.go:176-197`, `config/load.go:42` | Refresh ≈ 9 Postgres RTT (FindByTokenHash, then 3 separate SELECTs on the same account row, Rotate BEGIN/INSERT/UPDATE/COMMIT, audit INSERT). | 500k refreshes/min ≈ 75k RTT/s; with pool 10 one instance does ~100–150 refresh/s. | One SELECT joining sessions+accounts; Rotate as one CTE statement; async/sampled audit. |
| PF-P1-18 | Payment | scalability | C | `config/load.go:102`, `delivery/server.go` (`limitHook`) | Webhook rate limit 600/min per IP; Midtrans sends from few IPs. | ~10 webhooks/s per Midtrans IP per instance → settlement burst gets 429 → Midtrans backoff → confirmations minutes late. | Raise drastically or allowlist Midtrans IPs (signature is the credential). |
| PF-P1-19 | Payment | scalability | C | `usecase/resolve_payment.go:52,74,78`, `create_attempt.go:254-271`, `config/payment.go:103` | Resolve/closeOpen make up to 3 Midtrans calls through a status bulkhead of 10; all war bookings expire in the same 10-minute window. | ~10–30 resolves/s per instance; full bulkhead → UNKNOWN → Booking keeps stock and retries → more load. | Bigger separate resolve bulkhead with short wait; answer from final webhook state without calling Midtrans. |
| PF-P1-20 | Queue | efficiency | C (conditional) | `application/usecase/join.go:200`, `infrastructure/worker/status_publisher.go:121` | Sale window cached only when start **and** end are non-zero; otherwise every join calls Booking `Supply`. | Thundering herd from Queue onto the slower Booking service. | Cache a "known" flag with TTL, wrap in singleflight. |

## P2 — Medium

| ID | Area | Cat | St | Location | Problem / impact | Fix |
|---|---|---|---|---|---|---|
| PF-P2-1 | Queue | efficiency | C | `assets/lua/liveness_tick.lua:33-69` | One call processes up to 16 shards × 1000 × 2 phases ≈ 32k entries ≈ 250k commands → blocks the event shard 50–200 ms every 5s after a mass disconnect. | Total budget across shards or rotate a subset per tick. |
| PF-P2-2 | Queue | efficiency | C | `worker/promoter.go:197,269`, `pkg/rabbitmq/publisher.go:112` | Up to 200 serial HGETALLs and one synchronous confirm per `turn.arrived` publish, under the lease, 100 ms interval → tick far longer than interval. | `DoMulti`/HMGET in Lua; batch publishes. |
| PF-P2-3 | Queue | heap/efficiency | C | `usecase/my_entry.go:128-138`, `usecase/status.go`, `queue_store.go:583` | Every me/heartbeat GETs + `json.Unmarshal`s the status doc (~50k/s on one hot key); status handler unmarshals then re-marshals; `GetMyEntry` makes 5–8 sequential RTT; heartbeat does a separate GET vlast before EVAL; me limit 120/min/visitor is too loose. | Process-local `atomic.Pointer` doc refreshed ~250 ms, serve raw bytes; one Lua script/pipeline for me; tighten me limit. |
| PF-P2-4 | Queue | efficiency | C | promoter / status publisher (`ListEventIDs`) | `SELECT event_id FROM event_settings` unfiltered every tick (promoter 10/s, status 1/s); status publisher calls Booking `Supply` for every event ever configured, every second. | Filter to events on sale; cache the list. |
| PF-P2-5 | Queue | efficiency | S | `security/tokens.go`, auth middleware | Ed25519 verify + device HMAC per authenticated request ≈ 50 µs ≈ 2–3 cores at 50k rps. | Measure; acceptable if budgeted. |
| PF-P2-6 | Booking | correctness/efficiency | C | `valkey/availability_cache.go:62-76` | SSE connection counter TTL 60s refreshed only on INCR → key expires on long streams → DECR goes negative → limit of 1000 no longer enforced. | Per-instance `atomic.Int64`. |
| PF-P2-7 | Booking | efficiency | C | `postgres/funnel_stats.go:20-28` | Metrics collector runs full-table `GROUP BY` on `admissions`/`bookings` (all events, all time) on every scrape per API instance; no `event_id`-leading index; `event_id` label grows. | Cache 30–60s in process or run in worker; only events on sale. |
| PF-P2-8 | Booking | efficiency | C | `admission_repo.go:367-371` | `DueSessions`: `expires_at + CASE… < $1` is non-sargable → `idx_admissions_due` unused → scan+sort all active admissions every tick. | `expires_at < $1 - interval '30s'`, re-check grace in Go. |
| PF-P2-9 | Booking | efficiency | C | `postgres/event_repo.go:205-256`, `http/catalog_handler.go:101`, `usecase/hold.go:275` | `GET /events` runs JOIN/GROUP BY per request, no pagination, no in-process cache (only `max-age=2`, effective only if CDN forwards query string); regex twice per facets request; hold calls `GetDetail` (2 queries) per request. | Singleflight + 1–2s cache per normalized query, cached event config, `LIMIT`/keyset. |
| PF-P2-10 | Payment | efficiency | C | `usecase/create_attempt.go:170` | Charge uses the request ctx: client disconnect cancels in-flight charge → UNKNOWN → later Status+Cancel (Midtrans load ×3); read failure counted as ambiguous in the breaker. | `context.WithoutCancel(ctx)` + own timeout. |
| PF-P2-11 | Notification | efficiency | C | `fcm/push_sender.go:68-69` | FCM has no breaker/bulkhead; at the 10s HTTP timeout a worker does 0.1 msg/s and every message walks the 5s/30s/2m retry tiers (useless for a turn deadline). | Breaker with ~2s timeout; for turn.arrived compute remaining deadline and fail permanently instead of retrying. |
| PF-P2-12 | Notification | efficiency | C | `smtp/email_sender.go:43,143` | New TCP+TLS+AUTH per email (~300–600 ms to SES) → ~8–13 emails/s per replica → 50k e-tickets ≈ 1–1.7h. | Small pool of authenticated SMTP conns (RSET between messages) or SES HTTP API. |
| PF-P2-13 | Notification | scalability | C | `0001_init.up.sql` (push_subscriptions), `subscription_repository.go:51-54`, `worker.go:104` | `push_subscriptions.expires_at` never enforced or purged, no index; expired subscriptions still pushed. | `AND expires_at > now()`, batched DELETE job, index on `expires_at`. |
| PF-P2-14 | Notification | efficiency | C | `0001_init.up.sql` (`idx_deliveries_retry`) | Index `(status, next_attempt_at)` read by no query but updated on every claim/mark/bump → blocks HOT updates; ~2 tuples per push, no retention. | Drop or make partial (`WHERE status IN ('RETRY','SENDING')`); purge/partition. |
| PF-P2-15 | User | efficiency | C | `usecase/auth.go:77-109,211-236` | VerifyOTP = 5 Postgres statements; RequestOTP = 4 sequential Valkey RTT. | `UPDATE … RETURNING` for find+touch; `DoMulti` for cooldown + both limiters. |
| PF-P2-16 | User | scalability | C | `usecase/auth.go:93,175,243` | Fixed-window per-IPv4 limits (OTP ≤1000/IP/h, refresh ≤6000/IP/min) vs Indonesian carrier CGNAT. | Lean on per-email/per-token limits, raise IP ceilings, enforce per-IP at WAF. |
| PF-P2-17 | Payment + Booking | scalability | C | payment `delivery/worker.go`, `postgres/idempotency_store.go:24`; booking idempotency/outbox tables | `idempotency_records` (24h TTL, deleted only on key reuse), `payment_page_tokens`, `payment_callbacks`, published `outbox_events` never purged in either service. | Batched purge job + index on `expires_at`/`published_at`; or idempotency in Valkey. |
| PF-P2-18 | Infra | heap | S | all `services/*/deployments/Dockerfile`, compose | `GOMEMLIMIT`/`GOGC` not set; prod containers have no memory limit, dev `mem_limit` 256–512m without `GOMEMLIMIT` → OOM-kill risk under spikes. | Set container memory limits + `GOMEMLIMIT` ≈ 80–90% of limit. |

## P3 — Low

| ID | Area | St | Location | Problem | Fix |
|---|---|---|---|---|---|
| PF-P3-1 | Queue | C | `internal/delivery/queue_module.go:106`, `worker.go:393,400` | `NewHandler` dials RabbitMQ + opens a publisher that is never closed; `RunWorker` builds a second module → extra AMQP connections per process. | Build once, close on shutdown. |
| PF-P3-2 | Queue | C | `usecase/join.go:370`, `valkey/event_settings_cache.go:387` | Deprecated `queueToken` still Ed25519-signed per join; settings cache has no singleflight on 30s expiry. | Stop signing; add singleflight. |
| PF-P3-3 | Booking | S | `unit_of_work.go:1024` | `retry` only retries `ErrConcurrency`, not SQLSTATE 40P01/40001. | Retry those too. |
| PF-P3-4 | Payment | C | `usecase/handle_webhook.go:49`, `idempotency_store.go:24-29`, `pkg/resilience/breaker.go:130-137` | Invalid-signature webhooks still write a DB row (unauthenticated write amplification); duplicate webhooks still open a tx; idempotency `Begin` = DELETE+INSERT; breaker half-open treats `ErrBulkheadFull` as success; consecutive-failure breaker may never open at ~50% timeouts (S). | Sample rejected callbacks; short-circuit final status; single `INSERT … ON CONFLICT DO UPDATE WHERE expires_at < now()`; ratio-based breaker. |
| PF-P3-5 | Notification | C | `notify_turn_soon.go:41`, `notify_turn_arrived.go:47-57`, `smtp/email_sender.go:92-137,190`, `fcm/push_sender.go:135-145` | INFO log per non-subscribed turn event (millions of lines); late turn.arrived still costs 2 writes; MIME body copied 4–5×; FCM `io.ReadAll` unbounded and raw body in errors/logs. | Debug level + counter; ack late without DB; streaming base64 encoder; `io.LimitReader(…, 64KB)`. |
| PF-P3-6 | User | C | `pkg/crypto/crypto.go:45-49`, `pkg/token/token.go:114,134`, `pkg/revocation/revocation.go:77`, `http/auth_handler.go:~133` | `hmac.New` per call; token split/concat copies; revocation uses `context.Background()` (cancelled requests still wait up to 500 ms); `GET /me` = Ed25519 + Valkey EXISTS + **3 sequential Postgres SELECTs on the same account row + 2 AES-GCM opens** per page load since `d04cf0d` (`profile.go:39-67`) — at millions of page loads this is ~3× the DB round trips it needs. | `sync.Pool` of HMACs; verify over `tok[:lastDot]`; pass request ctx; one SELECT returning `email_enc`/`nik_enc` with the account (extend `accountCols` or a dedicated query); short cache for name. |
| PF-P3-7 | `pkg/eventstore` | C | `pkg/eventstore/eventstore.go:96-108,163-169` | One INSERT per event (extra RTT while the stock lock is held); `findForbidden` marshals then unmarshals each payload. | `pgx.Batch`; scan the struct once. |
| PF-P3-8 | `pkg/httpx` | C | `pkg/httpx/server.go`, RequestID/trace | `MaxHeaderBytes` default 1 MB; 4–5 regex + 2× `crypto/rand` per request in RequestID/trace (cheap). | Lower header limit; otherwise ignore. |
| PF-P3-9 | Infra | S | `compute/user_data.sh.tftpl` | Containers on bridge network with `-p` (NAT/conntrack) — costly at very high connection counts; RabbitMQ `vm_memory_high_watermark` 0.4 computed from a host shared with Postgres/Valkey can trigger flow control; quorum queues on one node add fsync without replication benefit. | `--network host` for app containers; size RabbitMQ on its own host; classic or properly replicated quorum queues. |

## Already done well (keep)

- **No goroutine-leak patterns** in workers: tickers stopped with `defer`, breaker/bulkhead run inline (no spawned
  goroutines), timers stopped; `pkg/postgres.WithTx` rolls back on error and panic; `rows.Close` / `resp.Body.Close`
  consistent.
- **Valkey usage:** no `KEYS`/`SMEMBERS`/`ZRANGE 0 -1`; sorted-set ops bounded (`ZCOUNT`/index ranges O(log N),
  `ZRANGEBYSCORE … LIMIT`); Lua via EVALSHA; rate-limit keys use per-visitor/IP hash tags; valkey-go auto-pipelining.
- **Booking:** commands don't replay event streams (version from read model, O(1) append); User NIK call and
  crypto happen before the transaction; AES-GCM built once; fixed lock order; CHECK constraints guard stock/quota;
  partial indexes for outbox pending, booking due, admission due; `ListByAccount` uses keyset pagination; SSE
  availability served from a Valkey snapshot.
- **Payment:** Midtrans charge outside DB tx; short `Transition` tx; Midtrans responses read with 1 MB `LimitReader`;
  webhook body capped 64 KB; correct indexes; `pkg/outbox` uses `SKIP LOCKED` with jittered backoff.
- **Notification:** templates parsed once; QR only after successful claim; FCM OAuth `ReuseTokenSource`; shared
  HTTP client (HTTP/2); single-statement delivery claim with lease; handlers recover panics; malformed payloads →
  DLQ.
- **User:** atomic OTP Lua; per-IP verify limit before Postgres; 16 KB body limit; indexes cover all queries; no
  unbounded caches.
- **Platform:** metric labels use route templates (bounded cardinality); `/health` and `/metrics` skipped from logs;
  `ReadHeaderTimeout`/`ReadTimeout`/`IdleTimeout` set, `IdleTimeout` 120s > ALB 60s; PII-redacting logger;
  distroless static images; queue status endpoint CDN-cacheable (`max-age=1, stale-while-revalidate=2`).

## Recommended roadmap

1. **Capacity first (P0-1, P0-2, P0-9)** — no code tuning matters while all state lives on one `t4g.small` with
   100 Postgres connections. Managed Postgres + proxy, Valkey cluster, multi-node broker, scheduled scale-out.
2. **Shared `pkg/` fixes (P0-5, P0-6, P0-7, P1-8, P1-9, P1-10, P1-11)** — one PR each in `pkg/` (owner
   arnu12rahman) fixes every service: publisher confirms, outbox draining, client IP, HTTP client factory, request
   deadline, non-blocking logs + sampling, Valkey-backed limiter.
3. **Hot paths you own:** Queue hash-tag layout + status doc cache + TTLs (P0-3, P1-4..7, P2-1..4); Booking supply
   lock and ReserveBatch (P0-4, P1-1), sweepers (P1-2), SSE (P1-3).
4. **Money and notification throughput:** Payment bulkheads/webhook limit (P0-8, P1-18, P1-19); Notification
   turn.arrived queue, SMTP deadlines, OTP isolation, reconnect (P1-12..15).
5. **Retention jobs** for all ever-growing tables/keys (P1-16, P2-13, P2-14, P2-17).
6. **Prove it:** run `tests/k6` load/spike scenarios after each phase and store results in `docs/evidence/`;
   add `pprof` (heap, goroutine) captures under load to confirm the leak findings.
