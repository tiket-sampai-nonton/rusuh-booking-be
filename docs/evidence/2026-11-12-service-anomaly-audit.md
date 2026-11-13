# Service Anomaly Audit (2026-11-12)

Read-only audit of the five services (`queue`, `booking`, `payment`, `notification`, `user`), one subagent per service,
covering normal flows and edge cases against the invariants, specs, and contracts.

- **Method:** code tracing only. `go test` / `go vet` could not run (local Go 1.18.1, `go.mod` requires 1.26.11 + `tool`
  directive). No finding has been proven by a failing test yet.
- **Status:** C = confirmed (code path traced), S = suspected (depends on runtime/deployment/other service behavior).
- **Owner:** per `CODEOWNERS` — nuzirwan: queue, booking, user; arnu12rahman: notification, payment, `pkg/`.
- Paths are relative to `services/<service>/internal/<domain>/` unless written otherwise.

## Re-check after commits `547e575`, `14286cd`, `d04cf0d` (2026-11-12)

The commits touch Booking hold/security/HTTP and User profile/security only; findings in other files are unchanged.

| Change | Effect on this report |
|---|---|
| `547e575` revert `useLoginNik` (Booking NIK client, User `/internal/v1/accounts/{id}/attendee-nik-enc`) | **P1-18 resolved** (feature gone). P2-20 reduced (no `/internal` route left on User). P1-17 reduced (Booking no longer decrypts User ciphertext). |
| `14286cd` Booking `HashNIK` now hashes `"nik:"+nik`, same format as User | **Fixes an unlisted P0**: Queue passes the token `nik_hash` (User format) to Booking, which reserved `buyer_quotas` under it at admission (`admission_repo.go:148-162`) while hold charged quota under Booking's own hash (`unit_of_work.go:90-97`) → two quota rows per logged-in buyer, 3-per-NIK limit bypassable. Introduces **new P1-19** (no migration for existing hashes). |
| `d04cf0d` `GET /me` returns plaintext `email` and `nik` | **New P2-23** (privacy decision not reflected in SRS/steering), **new P3-22** (NULL `nik_enc` → 500); worsens P2-21; perf impact recorded in the performance report (PF-P3-6). |
| Line shifts in `hold.go` | P2-6, P2-9, P2-10 locations updated. P2-7 still valid (records kept IN_PROGRESS on 5xx/cancel; `abortOnRefusal` only covers typed 4xx). |

## Re-check after fixes (2026-11-13)

Commits on branch `feature/audit-fixes`, per finding.

| Finding | Status | Commit |
|---|---|---|
| P0-4, P0-5, P3-5 | Resolved | `6eb951b` |
| P1-3 | Resolved | `95dc52e`, `fbc315f` |
| P1-4 | Resolved | `573d3fd` |
| P1-5 | Resolved | `88b7357` |
| P1-6 | Resolved | `5996824` |
| P1-7 | Resolved | `7b2f531`, `e15380a` |
| P1-1 (Booking part) | Resolved; Notification dedup: koordinasi | `eb1d7e3` |
| P2-8 | Resolved | `8743869` |
| P2-6 | Resolved | `210f282` |
| P2-7 | Resolved | `da7914d` |
| P2-9, P2-10 | Resolved | `f63597d` |
| P3-4, P3-6 | Resolved | `1f169df` |
| P1-19 | Decided: data lama dipertahankan (D-B12); test | `aaf0a0b` |
| P1-14, P1-15 | Resolved | `9aa88ba` |
| P2-22 | Resolved | `5bf9618` |
| P2-21, P3-22 | Resolved | `5112350` |
| P1-16, P2-19 | Resolved | `372ef6a` |
| P3-16 | Resolved | `3b5aaaa` |
| P3-17, P3-18 | Resolved | `fb0e371` |
| P2-23 | Decided: plaintext di `GET /me` dipertahankan (ADR-033) | `7465b12` |
| P2-20 | Open, di luar plan: `/internal/*` sudah hilang (`547e575`); tersisa `GET /metrics` | `547e575` |
| P3-19 | Won't fix | - |
| P3-20 | Informational | - |

## Priority definitions

| Priority | Meaning |
|---|---|
| **P0 — Critical** | Breaks an invariant: lost money/tickets, lost notifications, stock leak. Fix first. |
| **P1 — High** | Wrong behavior in a realistic flow or a security weakness; user-visible failure. |
| **P2 — Medium** | Edge case, operational risk, or contract mismatch with limited blast radius. |
| **P3 — Low** | Cosmetic, audit-trail, validation, or hygiene issue. |

## Summary

| Service | P0 | P1 | P2 | P3 | Owner |
|---|---|---|---|---|---|
| Notification | 3 | 0 | 5 | 3 | arnu12rahman |
| Booking | 2 | 7 | 5 | 3 | nuzirwan |
| Payment | 2 | 3 | 3 | 4 | arnu12rahman |
| Queue | 1 | 4 | 5 | 5 | nuzirwan |
| User | 0 | 4 | 5 | 6 | nuzirwan |
| Cross / toolchain | 0 | 0 | 0 | 1 | — |
| **Total (active)** | **8** | **18** | **23** | **22** | |

Cross-service P1 items are counted under their primary service (P1-1 Booking, P1-2 Queue). Re-check: P1-18 resolved; new P1-19, P2-23, P3-22.

---

## P0 — Critical

| ID | Service | St | Location | Anomaly | Trigger scenario | Suggested fix |
|---|---|---|---|---|---|---|
| P0-1 | Notification | C | `infrastructure/rabbitmq/consumer.go:57,104,144` + `pkg/rabbitmq/consumer.go:72-74,128` | Handler is chosen by `d.RoutingKey`; after a retry the message is dead-lettered back with routing key = queue name (e.g. `notification.booking.events`), hits `default`, is logged and **acked**. | SMTP down when `booking.paid` arrives → retry 5s → acked as "unknown routing key". E-ticket never sent, never reaches DLQ. Tests always use `RoutingKey: "booking.paid"`. | Dispatch on `d.Envelope.EventType`, or preserve the original key in a header in `pkg/rabbitmq`. |
| P0-2 | Notification | C | `infrastructure/postgres/delivery_repository.go:25-51`, `application/usecase/send_failure.go:30`, `notify_turn_*.go` | `ClaimDelivery` returns false while a `SENDING` row's 60s lease is alive → usecase acks as duplicate. Failure status is written with the same cancelled `ctx`, error ignored → row stays `SENDING`. | Worker crash/shutdown between claim and send, or send fails on ctx cancel; redelivery (immediate or 5s retry) is acked as duplicate → notification lost. | Write failure status with `context.WithoutCancel(ctx)`; return retryable (not ack) when row is `SENDING` with a live lease. |
| P0-3 | Notification | C | `internal/delivery/worker.go:24-50,81-82` | Consumer goroutines not awaited; deferred `Close()` runs while handlers are in flight; handler ctx derives from root ctx so SIGTERM cancels every send. | Every deploy/scale-in cancels in-flight sends; acks after close fail; redelivery then hits P0-2. | errgroup/WaitGroup for consumers, detached handler ctx with grace period, close connections after drain. |
| P0-4 | Booking | C | `resolution.go:20-27`, `infrastructure/postgres/unit_of_work.go:480-484` | Missing SettlementTime is replaced with `now`; sweeper only resolves after `payment_expires_at`, so the payment is always "late". Consumer: `parseRFC3339` returns zero time on bad format → same. | Buyer pays at T-1 min, webhook lost, sweeper resolves SUCCEEDED without time → REVIEW, stock released, no ticket. Violates "Jangan PHP". | When time is missing, do not apply the late rule; use the attempt's time or return a retryable error. |
| P0-5 | Booking | C | `resolution.go:16-18` → `unit_of_work.go:748-772` vs `unit_of_work.go:486-530` | Late success via sweeper/cancel goes through `Expire{Review:true}`: REVIEW but held stock + buyer quota never released and no `booking.journey.ended`. Consumer path does release. | Late payment resolved by sweeper → stock, NIK quota and Queue active slot leak permanently. | Route `settleFound`'s late case through the same release path as the consumer (or at least enqueue journey.ended + release). |
| P0-6 | Payment | C | `domain/valueobject/attempt_status.go:24-25`, `usecase/create_attempt.go:253-274`, `0001_init.up.sql` | CANCELLED/EXPIRED → SUCCEEDED allowed and still publishes `payment.succeeded`; no unique "one SUCCEEDED per booking"; `refund_cases` never written. | Buyer switches method, attempt 1 cancelled, attempt 2 paid; late settlement of attempt 1 → two `payment.succeeded` for one booking (double charge, no refund case). | Detect other SUCCEEDED attempt / settle on non-open attempt → open `refund_cases` (DUPLICATE_PAYMENT / SETTLED_AFTER_CANCEL) instead of plain event. |
| P0-7 | Payment | C | `internal/delivery/worker.go:65-78` | UNKNOWN reconciler and expiry sweeper (design §7.2/§7.3) not implemented; only outbox relay runs. | Charge times out → UNKNOWN; payer actually pays, webhook lost → never re-checked unless Booking calls resolve (which only checks the latest attempt, see P1-8). | Add reconciler job (`FOR UPDATE SKIP LOCKED`, `gateway.Status`) for UNKNOWN and expired PENDING. |
| P0-8 | Queue | C | `assets/lua/join.lua:62-67,110-118`, `infrastructure/security/tokens.go:66-72` | Visitor token is global (jti=`gen.device`, no event) but `vtok` generation is stored per event. The 2026-11-11 fix only covers `serverGen==0`. | Join A (gen 1) → join B (adopts gen 1) → B entry ends → rejoin B rotates to gen 2 → A's heartbeat/me/claim/leave return 401, A entry goes STALE/ENDED; rejoin A creates a new visitor at the back. REATTACH via account does the same. No cross-event test. | Store gen per event in the token claims, or use one global `vtok` per visitor (no event prefix). |

## P1 — High

| ID | Service | St | Location | Anomaly | Trigger scenario | Suggested fix |
|---|---|---|---|---|---|---|
| P1-1 | Cross (Booking → Notification) | C | booking `infrastructure/postgres/outbox_writer.go:22-26`, booking `infrastructure/rabbitmq/consumer.go:117` | Outbox relay has no claim/lock, and `NewEnvelope` mints a new `messageId` on every publish; Notification dedups on `(source_message_id, type)`. | Multiple worker replicas (ASG) all publish the same rows; or confirm timeout → republish → buyer receives two e-ticket emails. Also out-of-order `booking.paid` vs `journey.ended`. | `FOR UPDATE SKIP LOCKED` (or lease column) in relay; use outbox row id as `messageId`. |
| P1-2 | Cross (Queue, User) | C/S | queue `infrastructure/http/join_handler.go:69` → `pkg/httpx/request.go:61`; user `pkg/httpx/request.go:81-98`, `config/load.go:24` | Queue uses the rightmost XFF entry = CloudFront edge IP. User's `APP_TRUSTED_PROXY_HOPS=1` is only correct for one specific topology. | Thousands of buyers behind one PoP share the 200/min join bucket (and user OTP/verify limits) → mass 429 during the war. If ALB is directly reachable with hops=2, XFF is spoofable. | Queue: use `httpx.ClientIPHops`; enforce topology in Terraform (ALB SG only CDN prefix list); smoke test before changing hops. |
| P1-3 | Booking (↔ Queue) | C | `application/usecase/session.go:47` | Re-claim fallback calls `FindByJTI(claims.JTI)` (column `admission_jti` = random UUID from `allocate_batch.go:61`) but Queue sets `jti = allocationID` (admission PK). | Buyer re-claims an ACTIVE session → always 403 `ADMISSION_TOKEN`. | Use `GetByID(claims.JTI)` or store the admission id in `admission_jti`. |
| P1-4 | Booking | C | `cancel_booking.go:53-67`, `unit_of_work.go:716+` | `IsLocked()` checked on a read outside the tx; `expire()` does not re-check `payment_started_at` under the row lock. | PaymentLock commits between Resolve(CHECK_AND_CANCEL) and `uow.Expire(CANCELLED)` → booking cancelled, stock released, buyer still charged → REVIEW. | In `expire()`, reject `Reason==CANCELLED` when `payment_started_at IS NOT NULL` (ErrBookingLocked). |
| P1-5 | Booking | C | `unit_of_work.go:852-860,931` | ExtendDeadline allowed in VERIFYING; PaymentLock returns OK without stamping when VERIFYING; sweeper Phase B ignores `payment_expires_at`; extension appends no event. | VERIFYING (no attempt) → extend → PaymentLock OK w/o stamp → sweeper sees `PaymentStartedAt==nil`, skips Resolve, expires mid-payment. Event-store rebuild loses the extension. | Allow extend only in PENDING_PAYMENT, always stamp `payment_started_at`, append an event. |
| P1-6 | Booking | C | `infrastructure/postgres/admission_repo.go:178-185` | `UPDATE events SET status='SOLD_OUT'` and `booking.sold_out` outbox insert run before `ROLLBACK TO SAVEPOINT entry` → both undone. | Sold-out during reservation is never announced; Queue keeps admitting. | Roll back to savepoint first, then write status + outbox. |
| P1-7 | Booking | C | `booking_repo.go:116-119` | Sweeper takes 100 oldest due rows; stuck VERIFYING (Resolve UNKNOWN, Payment down, in grace) fill the batch every tick. | Newer bookings never expire; their stock stays held during the war. | Skip rows still in grace (`verification_until > now`), add retry backoff. |
| P1-8 | Payment | C | `usecase/resolve_payment.go:38-50` | Resolve only looks at the latest attempt. | Attempt N-1 cancelled locally but paid at Midtrans (webhook lost), attempt N FAILED → `NOT_PAID` → Booking releases stock, buyer's money held. | Also check earlier CANCELLED/UNKNOWN attempts not final at the gateway. |
| P1-9 | Payment | C | `usecase/handle_webhook.go:79-86`, `attempt_status.go:22` | CREATED → SUCCEEDED/EXPIRED not in the state machine; `Transition` false → logged `DUPLICATE`, HTTP 200 → Midtrans stops retrying. | Transition to PENDING after charge fails (DB error at `create_attempt.go:178`) → attempt stays CREATED → legitimate settlement silently lost. | Allow CREATED → SUCCEEDED/EXPIRED; reply 5xx / record `ERROR` when rejected from a non-final state. |
| P1-10 | Payment | S | `infrastructure/midtrans/gateway.go:332`, `create_attempt.go:256-257`, `resolve_payment.go:54-56` | Status 404 → `ErrOrderNotFound` → attempt marked CANCELLED without calling Cancel. Snap returns 404 until the payer picks a channel. | Payer opens Snap, goes back, picks QRIS → old Snap tab can still pay → double payment (feeds P0-6). | On 404 still call `gateway.Cancel` (ignore 404), or treat young SNAP 404 as uncertain. |
| P1-11 | Queue | C | `assets/lua/promote.lua:54-55`, `domain/valueobject/release.go:9` | `end_sold_out.lua` runs without the promoter lease; `ShouldRelease` does not include `SOLD_OUT`. | Booking GRANTED for a WAITING entry, entry flips to SOLD_OUT before Promote → allocation stays RESERVED until Booking sweeper; buyer told "sold out" while a unit is held. | Add `SOLD_OUT` (and other terminal statuses) to `ShouldRelease`. |
| P1-12 | Queue | C | `infrastructure/worker/promoter.go:241-242`, `infrastructure/rabbitmq/consumer.go:278` | `SetSale(CLOSED,true)` is never reverted to OPEN. | Stock returns (allocation expired, payment failed) but join still returns 410 `EVENT_SOLD_OUT` and status doc keeps `soldOut=true`. | Status publisher re-derives sale state from `Supply()` when `RemainingTickets > 0`. |
| P1-13 | Queue | C | `infrastructure/valkey/queue_store.go:121-128`, `join.lua:53-57` | Guest idempotency keys share the global `n:` scope; replay issues the original visitor's token. | Two tokenless guests send the same `Idempotency-Key` → second guest receives the victim's entry and visitor token (session takeover). | Scope guest keys by IP/device hash, or never issue a token on a credential-less replay. |
| P1-14 | User | C | `application/usecase/auth.go:271-276`, `postgres/repositories.go:142` | Any session with `revoked_at` triggers `handleReuse`, including normal logout (`replaced_by` not loaded). | Logout in tab A while tab B refreshes with the same token → all sessions on all devices revoked. | Treat as reuse only when `replaced_by IS NOT NULL`; return `SESSION_INVALID` for logged-out tokens. |
| P1-15 | User | C | `auth.go:401-408`, `repositories.go:176-197` | No grace window for concurrent refresh; loser's retry with the rotated token = reuse. | Two tabs refresh at once; loser retries → revoke-all, including the winner's fresh session. | 10–30s grace on the same `replaced_by` chain, or document single-flight refresh for clients. |
| P1-16 | User | C | `profile.go:56-80`, `repositories.go:83`, `auth_handler.go:185` | `PATCH /me` has no rate limit; 409 `NIK_ALREADY_LINKED` reveals registered NIKs; no ownership proof. | Logged-in attacker enumerates NIKs, or links a victim's NIK first so the owner can never link it. | Per-account/IP limiter + change cooldown; less specific conflict response; audit. |
| P1-17 | User | C | `security/security.go:350,368,378` | One `PII_ENC_KEY` seals account email/NIK and the broker OTP; Notification must hold it to open OTPs (re-check: after the `useLoginNik` revert Booking no longer decrypts User ciphertext; whether Booking's `PII_ENC_KEY` value equals User's is a deployment question). AAD values are public constants. No key id → no rotation. | Compromised Notification can decrypt every NIK in the users DB. | Per-purpose keys (HKDF), key-id byte in ciphertext. |
| ~~P1-18~~ **RESOLVED** (`547e575` removed `useLoginNik`) | User | S | `deployments/terraform/variables.tf:64`, booking `booking_module.go:101` | Internal ALB only has a Booking target group; `USER_SERVICE_URL` is never set in Terraform. | `useLoginNik` cannot reach `/internal/v1/accounts/{id}/attendee-nik-enc` in AWS → always DEPENDENCY_UNAVAILABLE. | Add internal target/service discovery for user and set `USER_SERVICE_URL`. |
| P1-19 | Booking | C | `infrastructure/security/security.go:43` (`14286cd`); tables `buyer_quotas`, `bookings`, `admissions`, attendee items; event-store payloads | NIK hash format changed (`hash(nik)` → `hash("nik:"+nik)`) with no data migration or dual-read, so every stored hash stops matching. The alignment only works if Booking's `PII_HASH_KEY` value equals User's. | Deploy onto existing data: a NIK that already holds/bought 3 tickets gets a fresh `buyer_quotas` row (quota bypass, invariant 3); `uq_attendee_nik_per_event` misses holders used before the change (1-ticket-per-holder bypass); attendee checks miss. | **Decided 2026-11-13 (owner, D-B12): old data kept unchanged** — no reset, no re-hash, no dual-hash. Residual risk accepted: rows hashed before `14286cd` do not match the new format, so a NIK that bought before the change can buy up to 3 more on the same event, and an attendee NIK used before the change can be used once more. New data is consistent. Parity test: `TestHashNIK_MatchesUserFormat` (booking security). |

## P2 — Medium

| ID | Service | St | Location | Anomaly / scenario | Suggested fix |
|---|---|---|---|---|---|
| P2-1 | Notification | C | `infrastructure/smtp/email_sender.go:57-68,141-186`, `emailguard/sender.go:217` | Breaker cancels at 3s but `deliver` goroutine keeps running with no conn deadline; slot released. Slow e-ticket (~4s) is delivered but marked RETRY → duplicate email; hung server leaks goroutines. | `conn.SetDeadline`, close conn on ctx done, align breaker timeout with `SMTP_TIMEOUT`. |
| P2-2 | Notification | C | `smtp/email_sender.go:169-186` | SMTP 5xx (e.g. 550 RCPT) treated as transient; 5 bad addresses open the breaker for 30s, blocking OTP emails (acked without retry). | Wrap `*textproto.Error` with `Code>=500` in `rabbitmq.Permanent`. |
| P2-3 | Notification | C | `worker.go:37-42,77-80` | Closed channel / dropped OTP connection ends the consume goroutine; no restart, `NotifyClose` unused; `/health` only pings `clients.MQ`. Failed OTP dial at start → no OTP consumer, health UP. | Return error from `RunWorker` for supervisor restart; add OTP conn to health pingers. |
| P2-4 | Notification | C | `notify_turn_arrived.go:45-50` | Past-deadline path ignores claim result and calls `MarkExpired` → redelivered SENT push becomes DEAD. | `MarkExpired` only when `claimed`. |
| P2-5 | Notification | C | `delivery_repository.go:36` | Non-UUID IDs pass `Validate` (non-empty only) → Postgres cast error retried as transient until DLQ. | Validate UUID in DTO, return `Permanent`. |
| P2-6 | Booking | C | `hold.go:272-274`, `check_attendee.go:57` | `plan()` rejects with SESSION_EXPIRED at `now >= expires_at` before the UoW, so `cfg_submit_grace_seconds` / `inGrace` is unreachable over HTTP. | Include `CfgSubmitGraceSeconds` in the `plan()` check. |
| P2-7 | Booking | S | `hold.go:105-126` | After 5xx/timeout the idempotency record stays IN_PROGRESS for 24h → same key gets 503; if hold committed but `Complete` failed, a new key gets ADMISSION_TOKEN and the client can't recover its booking id. | Short lease for IN_PROGRESS; on replay look up the committed booking. |
| P2-8 | Booking | C | `infrastructure/rabbitmq/consumer.go:62-70` | Non-UUID `bookingId` / unknown booking (Validation/NotFound) retried to MaxRetries instead of `Permanent`; `amountMinor` never compared with `bookings.amount_minor`. | Return `Permanent` for these; compare amount. |
| P2-9 | Booking | S | `hold.go:275-281` (`plan()`) | Event-cancelled check runs outside the tx → hold can slip through while the event is being cancelled. | Re-check event status inside the tx. |
| P2-10 | Booking | S | `hold.go:283,307`, `edit.go:139` | `AllowMixedCategories` / `MaxTicketsPerOrder` read from live event row, not the admission cfg snapshot (mitigated by rules-lock trigger once AVAILABLE). | Read from the admission snapshot. |
| P2-11 | Payment | S | `create_attempt.go:206-208` | `makeRoom` reuses an open PENDING attempt for the same method without comparing amount/`BookingVersion`. If edit changes the amount, payer pays the old amount → webhook rejected for amount mismatch → never settles. | Treat attempt as not live when amount/version differ. |
| P2-12 | Payment | S | `create_attempt.go` (`makeRoom` + `CreateNext`) | Two concurrent method switches both run `closeOpen`; one loses with 503; a third request can get instructions of an attempt being cancelled (no double charge thanks to `uq_attempt_open_per_booking`). | Per-booking advisory lock around `makeRoom` + `CreateNext`. |
| P2-13 | Payment | S | `gateway.go:240-243` | `capture`+fraud `challenge`, `authorize`, refunds → `ErrGatewayAmbiguous` → resolve UNKNOWN forever → Booking holds stock indefinitely. | Map challenge/authorize to PENDING; handle refund statuses. |
| P2-14 | Queue | C | `assets/lua/join.lua:129` (and :71) | With `oneActivePerVisitor=false` the `visitor:` key is never set → every join creates a new entry and rotates the token; orphan WAITING entries get promoted and burn slots/allocations. | Reject that setting or guard with `vlast`. |
| P2-15 | Queue | S | `claim.lua:21` | CLAIMED slot scored `inf`, freed only by `booking.journey.ended`; if it never arrives (see P0-5, P1-1), capacity shrinks permanently. | Upper bound (booking TTL + payment TTL + margin) and sweep. |
| P2-16 | Queue | S | `promoter.go:247-256`, `end_entry.lua:79` | Promote lease-lost/error or stale GRANTED replay: entry stays WAITING with RESERVED row; journey.ended(UNCLAIMED) ignored for WAITING; next tick ALREADY_ACTIVE ends a live buyer. | Handle UNCLAIMED for WAITING with a reserved allocation; reconcile instead of ending. |
| P2-17 | Queue | C | `heartbeat.lua:16`, `leave.lua:11`, `claim.lua:13` | SOLD_OUT not terminal: heartbeat 200 (contract: 404 `ENTRY_NOT_FOUND`), leave overwrites SOLD_OUT → LEFT, claim `NOT_FOUND` (contract: 410 `EVENT_SOLD_OUT`). | Treat SOLD_OUT as terminal in all three scripts. |
| P2-18 | Queue | C | `infrastructure/valkey/rate_limiter.go:39`, `internal/config/queue.go:168` | `RATE_LIMIT_JOIN_PER_IP` / heartbeat / me limits not validated; `JoinPerIP=0` blocks all joins. | Validate `> 0` at start. |
| P2-19 | User | C | `auth.go:389-394`, `profile.go:70+`; consumer queue `security/tokens.go:133` → booking `allocate_batch.go:40-56`, `admission_repo.go:148-162` | `nik_hash`/`has_nik` frozen in the JWT; `PATCH /me` issues no new token → Queue forwards the old `nik_hash` and Booking reserves buyer quota under the old NIK until the token expires. | Return a fresh token pair on NIK change (or deny-list + force refresh); document in `user.yaml`. |
| P2-20 | User | C | `delivery/server.go:55` | Re-check: `/internal/*` was removed by `547e575`; only `GET /metrics` remains on the public mux, protected only by ALB path rules (now effectively P3). | Serve `/metrics` on a separate listener or restrict at ALB. |
| P2-21 | User | C | `security.go:470-476`, `profile.go:38` | Disabled account keeps reading/patching its profile until the access token expires (`status` never checked); since `d04cf0d` that includes the plaintext email and NIK. | Check `Active()` in profile usecases. |
| P2-22 | User | C | `auth.go:399` | Every rotation sets `ExpiresAt = now + RefreshTTL` → sessions never have an absolute end. | Carry the original absolute expiry forward on rotate. |
| P2-23 | User | C | `application/usecase/profile.go:39-67`, `domain/dto/auth.go`, `docs/contracts/openapi/user.yaml` (`d04cf0d`) | `GET /me` now returns the plaintext `email` and 16-digit `nik`. Before, only masked values left the service (old DTO comment: "NIK hanya dikirim tersamar"); `security-privacy.md`/SRS not updated. A stolen access token, XSS, shared device or body-capturing proxy/APM now yields the full NIK. `Cache-Control: no-store` is set (good). | Make it an explicit owner decision (ADR + SRS + steering), or keep masked by default and return plaintext only where the FE truly needs it (e.g. prefill), ideally behind recent login; make sure no body logging on this route. |

## P3 — Low

| ID | Service | St | Location | Anomaly | Suggested fix |
|---|---|---|---|---|---|
| P3-1 | Notification | C | `fcm/push_sender.go:139-150` | Any 400 with `INVALID_ARGUMENT` deletes the subscription (even payload errors); 403 `SENDER_ID_MISMATCH` retried; raw FCM body (unbounded `io.ReadAll`) in `last_error`, header, logs. | Match on `errorCode`, truncate body. |
| P3-2 | Notification | C | `send_failure.go`, `pkg/rabbitmq/consumer.go` | After retries are exhausted the message is in DLQ but the row stays `RETRY`, not `DEAD`. | Mark `DEAD` when `d.Attempt > MaxRetries`. |
| P3-3 | Notification | S | `smtp/email_sender.go:94-96`, `config/config.go:104-124`, `notify_turn_arrived.go:78` | Subject not RFC 2047 encoded / CRLF not stripped (header injection if event name has newline); SMTP host/port and `SUBSCRIPTION_TTL` not validated; changing `RABBITMQ_RETRY_DELAYS` → `PRECONDITION_FAILED`; `time.Until` instead of injected clock. | Encode subject + strip CRLF; validate config; use clock. |
| P3-4 | Booking | C | `unit_of_work.go:838` | Buyer cancel publishes `journey.ended` with `outcome:"EXPIRED"`; contract enum has `CANCELLED`. | Map `Reason==CANCELLED` → `CANCELLED`. |
| P3-5 | Booking | C | `unit_of_work.go:~544` | Happy path stores `settlement_time=spec.SettlementTime` (possibly 0001-01-01) instead of `effectiveSettlement`; late branch leaves an EDITABLE admission unconverted. | Store `effectiveSettlement`; convert admission. |
| P3-6 | Booking | S | `ListByAccount` | Keyset paging on `created_at` only skips bookings with equal timestamps. | Page on `(created_at, id)`. |
| P3-7 | Payment | C | `create_attempt.go:272-273` | Gateway FAILED in `closeOpen` uses `moveTo` → no `payment.failed` event. | Route through `applyFailure`. |
| P3-8 | Payment | C | `postgres/idempotency_store.go:29-50`, `create_attempt.go:122-123` | Crash between `Begin` and `Complete`/`Release` leaves key IN_PROGRESS for 24h → 503 on every retry. | Short lease (e.g. reclaim after 1 min). |
| P3-9 | Payment | C | `create_attempt.go:86-101` | Idempotent replay checked after `isPayable` → retry after the booking is paid returns 409 instead of the stored 201. | Look up idempotency before the payable check. |
| P3-10 | Payment | C | `handle_webhook.go:62-63` | Unparseable `gross_amount` recorded as `AmountMinor=0` (misleading audit). Also: `payment_events` table (migration 0002) unused; `editableUntil` missing from `dto.PaymentPage` (optional field). | Set only when parse succeeds; clean up. |
| P3-11 | Queue | C | contract vs code | Claim lists 429 but has no limiter; empty `eventId` on claim/leave/me returns 401 `VISITOR_TOKEN_INVALID` instead of 400. | Align contract or code. |
| P3-12 | Queue | C | `queue_store.go:583` | GET `vlast` error ignored + TOCTOU → wrong shard key; self-heals via liveness sweep but leaves stale hb members. | Handle the error; do it inside Lua. |
| P3-13 | Queue | C | Valkey keys | Entry hash, `visitor:`, `account:`, `seq`, per-event sets have no TTL → memory grows per event. | TTL/cleanup after event end. |
| P3-14 | Queue | C | `application/usecase/join.go:116,298` | `now` read once before rate limit and Booking call, reused for deadline and `admittedAtMs`; skew of seconds when Booking is slow. | Re-read the clock after the Booking call. |
| P3-15 | Queue | S | `infrastructure/worker/status_publisher.go:80-81` | If `ranks` fails, status doc isn't written; after TTL (4× interval) `GET /queue-status` returns 404 for a valid event. | Write a degraded doc or extend TTL on error. |
| P3-16 | User | C | `infrastructure/valkey/otp_store.go:454-456` | Lock deletes code + counter → next attempt returns `OTP_EXPIRED` instead of `OTP_LOCKED`; `OTP_EXPIRED` audit never written. (Attempt counting itself is atomic.) | Keep a short lock marker until code TTL ends. |
| P3-17 | User | C | `domain/valueobject/email.go:21` | Normalization only lowercases/trims; plus-addressing, Unicode lookalikes, non-ASCII spaces → distinct `email_hash` → per-email quota bypass. | ASCII-printable local part; optional plus-stripped rate-limit key. |
| P3-18 | User | C | `profile.go:50-54` | `fullName` accepts control and bidi characters (shown later in emails/tickets). | Reject `unicode.IsControl` and bidi format runes. |
| P3-19 | User | S | `security.go:458` | Access token carries plaintext `email` (and `nik_hash`), forwarded to Queue/Booking; header-capturing logs/APM would record it. | Won't fix: klaim `email` dipakai daftar admin di Queue (`admin_verifier.go:63`) dan Booking (`security.go:157`). |
| P3-20 | User | — | (OTP compare) | Lua compare not constant-time, but compares keyed HMAC hex → not exploitable. Informational. | None. |
| P3-21 | Cross | — | toolchain | Local Go 1.18.1 cannot build/test any module (`go 1.26.11` + `tool`). Findings remain unverified until tests run. | Install Go 1.26.11 locally, then write a red test per P0/P1 finding. |
| P3-22 | User | C | `profile.go:56-64`, `postgres/repositories.go` (`GetNIKEncByID`) | If `HasNIK()` is true but `nik_enc` is NULL (legacy or manually edited data), `UnsealNIK(nil)` fails → `GET /me` returns 500 for that account. | Skip when `nikEnc == nil` (return masked only). |

## Verified OK (no anomaly found)

- **Booking:** fixed lock order (`event_supply` first), no oversell (CHECK constraints), per-NIK quota race serialized by
  `buyer_quotas FOR UPDATE`, duplicate holder NIKs rejected, qty 0/over-limit rejected, event-store versioning +
  ErrConcurrency retry, duplicate `payment.succeeded` guarded, no plaintext NIK/email in event payloads.
- **Payment:** Midtrans signature SHA512 over raw fields with constant-time compare, amount checked before state change,
  guarded `UPDATE ... WHERE status = ANY(...)`, outbox in the same tx, atomic page-token consumption, breaker timeouts.
  Event sourcing for Payment was dropped by amendment (`specs/payment-service/design.md` §4) — not a finding.
- **Notification:** OTP payload is AES-GCM ciphertext only, DTOs match `docs/contracts/events`, emails scrubbed from
  errors, no PII in usecase logs, malformed payloads return `Permanent`, Resend 4xx/5xx classification correct.
- **User:** OTP via `crypto/rand` (uniform), stored as HMAC, cooldown `SET NX` atomic, used code deleted, concurrent first
  login via `ON CONFLICT`, JWT pins `alg=EdDSA` + kid/iss/aud/exp/nbf, logout deny-list TTL, revocation fails closed,
  `{"meta":...}` envelope, migrations match queries.
- **Queue:** no SSE/streaming handler exists in this service, so no streaming goroutine leak risk.

## Recommended order

1. **Notification P0-1..P0-3** — small, together restore the retry + DLQ invariant (owner: arnu12rahman).
2. **Booking P0-4, P0-5** and **Payment P0-6, P0-7** — money/ticket correctness.
3. **Queue P0-8** — regression of the 2026-11-11 multi-event token fix (owner: nuzirwan).
4. **P1 cross-service items** (P1-1 duplicate emails, P1-2 client IP behind CloudFront, P1-3 re-claim) — need coordination.
5. Remaining P1, then P2/P3 as backlog.

Each fix should start with a red test reproducing the scenario above (TDD via `/feature`).
