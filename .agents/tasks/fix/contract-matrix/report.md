# Contract matrix pass (Batch 4) — report

Repo `rusuh-booking-be`, branch `feature/rework` @ `469f478`. Pass started **2026-11-09T05:59:53Z**; coverage is measured only from requests after that time. Verification only: nothing under `services/`, `pkg/`, `docs/contracts/` or migrations was changed.

## 1. Method

1. **Matrix.** `curl/gen_matrix.py` parses `docs/contracts/openapi/*.yaml` (7 files, current content) into one row per *method × path × status × errorCode*; errorCodes are the backticked UPPER_SNAKE tokens of the response description (single words such as `UNAUTHORIZED` included, so this is larger than the earlier 237-row matrix); a response with no code in its text is one row that is satisfied by the status alone. `/health` 200/503 of every contract file is included. **249 rows.**
2. **Driving.** `curl/matrix/` (Python `requests`, real HTTP to the running stack; `mlib.py` records every call with its contract row, observed status/errorCode, `X-Request-Id` and a masked curl). Parts: `p1` booking public, `p2` queue, `p3` payment (+ internal resolve), `p4` user + notification, `p5` internal booking (admin + service routes with service tokens signed with `deployments/compose/keys/*` exactly like `pkg/token`), `p7` real timers (session TTL, admission token, claim window, OTP TTL and per-email limit), `p9` temporary instances. `p1–p5,p7,p9` ran in parallel; then alone: `p6` outages, `p8` rate limits, `p0` health. Entry point `run_matrix.sh`; `build_matrix.py` joins rows and records; `analyze_logs.py` cross-checks the service request logs.
3. **Outages (p6).** One local process at a time, restored with the same binary/cwd/env as `start.sh` and `pids` updated: Payment API (booking cancel 503), fake Midtrans (attempt 503), Booking API (simulator 503, queue join with a cold sale-window cache 503, queue-settings PUT 503). No container was touched.
4. **Temporary instances (p9).** The shared stack must not be put in these states, so extra instances of the same binaries ran on spare ports and were killed afterwards: booking with `RATE_LIMIT_PER_IP=5` (429 on `current` and `attendee-checks`; the main booking runs with the limiter off); all five services behind a local TCP forwarder to the existing Postgres that the script closes (`/health` 503); user behind a forwarder to the existing RabbitMQ that is closed (OTP request 503 — the OTP is published to the broker).
5. **Log cross-check.** 139,818 request log lines after the start were read; of 1902 recorded requestIds, 1867 were found in the five api logs with the same status (1867 equal); the remaining 35 are the ten `/health` calls (health is not request-logged) and the 24 calls to the temporary instances (their own `log/tmp-*.log`).
6. **Harness notes (honest account).** Scripts live in `.agents/tasks/local/run/curl/` (`gen_matrix.py`, `t_matrix.sh`, `matrix/`). Final evidence comes from non-overlapping runs after the start time: the parallel group, then `p6`, `p9`, `p0` and `p8` alone. While developing the pass, one `p6` rerun overlapped with `p9`; its process lookup picked an older, still-draining Booking process, so that run's Booking-outage rows were invalid, and the overlapping `p9` run lost its temporary instance. Both were discarded: the lookup now takes the newest pid and waits for the old one to exit, and `p6` and `p9` were re-run alone. Booking, Payment and fake Midtrans were restarted several times by this pass (never left down). After the pass, one manual check (two anonymous `attendee-checks`, section 3 / R038) was run outside the recorded files to confirm the oracle behaviour.

## 2. Coverage

**249 rows: 234 PASS (94.0%), 10 exercised but mismatching, 4 not triggerable locally, 1 not applicable (contract text negates the row).** No row is left unexercised.

| Contract file | Rows | Exercised, status + errorCode match | Exercised, mismatch | Not triggerable locally | Not applicable |
|---|---:|---:|---:|---:|---:|
| booking.yaml | 97 | 90 | 6 | 1 | 0 |
| internal-booking.yaml | 43 | 42 | 1 | 0 | 0 |
| internal-payment.yaml | 3 | 3 | 0 | 0 | 0 |
| notification.yaml | 7 | 6 | 0 | 0 | 1 |
| payment.yaml | 30 | 29 | 1 | 0 | 0 |
| queue.yaml | 42 | 40 | 2 | 0 | 0 |
| user.yaml | 27 | 24 | 0 | 3 | 0 |
| **total** | **249** | **234** | **10** | **4** | **1** |

Per-row result and evidence (check name + requestId): `matrix.tsv`.

## 3. Mismatching rows (10 rows) and R028

Each "Reproduce" line is the exact request recorded by the check named next to it (tokens masked); the ids and tokens come from that check's setup in `curl/matrix/pN_*.py` (join → claim → session → hold, etc.). "Read" is checked against the code (file:line), not assumed. Buckets: **code bug** = the code should change; **contract error** = the contract documents something the code cannot or should not produce; **owner decision** = either side could move.

### R012 — POST /api/v1/booking-sessions → 410 SESSION_EXPIRED

- Contract (booking.yaml): `410 SESSION_EXPIRED`
- Observed: `403 ADMISSION_TOKEN_INVALID` (check "create session with expired admission token", requestId `b5a0eaa60472131a5de3994e87aa3cb0`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/booking-sessions' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p7-b-159129' -H 'Content-Type: application/json' -d '{"admissionToken": "eyJhbGciO…"}'`
- Read: **contract error (unreachable).** Activate() (admission_repo.go:76-88) updates `WHERE status='RESERVED' AND expires_at > now` and maps "no row" to `ErrAdmissionToken` (403); an expired admission token fails earlier in `Verify` (security.go:78) with the same 403. `SESSION_EXPIRED` is never returned by `POST /booking-sessions`.

### R013 — POST /api/v1/booking-sessions → 410 TURN_MISSED

- Contract (booking.yaml): `410 TURN_MISSED`
- Observed: `403 ADMISSION_TOKEN_INVALID` (check "create session 4 s after the claim deadline", requestId `e1f021758777f6f9954f54baabebbf12`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/booking-sessions' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p7-d-27952' -H 'Content-Type: application/json' -d '{"admissionToken": "eyJhbGciO…"}'`
- Read: **contract error (unreachable).** `ErrTurnMissed` is declared (port/inbound/errors.go:73) but has no caller in Booking. A session opened after the claim deadline gets the same 403 `ADMISSION_TOKEN_INVALID` (Activate matches no row). The "turn missed" case is reported by Queue on `claim` (R204, PASS), not by Booking.

### R030 — GET /api/v1/booking-sessions/{sessionId}/stream → 404 NOT_FOUND

- Contract (booking.yaml): `404 NOT_FOUND`
- Observed: `200` (check "stream unknown", requestId `82a97ae56d9ea0d5a9df7a6327387039`)
- Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/booking-sessions/ddc39617-c446-4275-a355-85098c293fc6/stream' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p1-s1-572363'`
- Read: **contract error / code gap (by design in code).** The SSE handler commits `200` + headers first and discards the usecase result (`_ = h.stream.Stream(...)`, sse_handler.go:50); only a malformed UUID can still produce a status (400). Unknown session and foreign session both end as an empty 200 stream. No data is leaked (the usecase returns before writing), so this is a status-code/contract gap, not a data exposure.

### R031 — GET /api/v1/booking-sessions/{sessionId}/stream → 403 VISITOR_MISMATCH

- Contract (booking.yaml): `403 VISITOR_MISMATCH`
- Observed: `200` (check "stream other visitor", requestId `53215df21f991b53ddf7ef839710b2c6`)
- Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/booking-sessions/d8ed1c1f-9cab-474d-a9b2-891a289f654b/stream' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p1-s2-266576'`
- Read: **contract error / code gap (by design in code).** Same mechanism as R030 (stream_session.go:63 returns `ErrVisitorMismatch` after the 200 is already on the wire).

### R038 — POST /api/v1/booking-sessions/{sessionId}/attendee-checks → 403 VISITOR_MISMATCH

- Contract (booking.yaml): `403 VISITOR_MISMATCH`
- Observed: `200` (check "attendee-check other visitor", requestId `cee778b4c8a08f054c29e9061b94227e`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/booking-sessions/d8ed1c1f-9cab-474d-a9b2-891a289f654b/attendee-checks' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p1-s2-266576' -H 'Content-Type: application/json' -d '{"nik": "3174********2425"}'`
- Read: **CODE BUG (missing ownership check).** `CheckAttendee` takes no visitor id and never compares it with the session owner (check_attendee.go:41-80; handler session_handler.go:87 passes only the path id). Any caller who knows a session UUID — with another visitor's token or none at all — gets 200 and consumes that session's attendee-check quota; verified live with no token against another visitor's session: a NIK already holding a ticket in the event answers `ALREADY_REGISTERED`, a free one `AVAILABLE` (NIK-registration oracle).

### R092 — GET /api/v1/bookings/{bookingId}/tickets → 403 VISITOR_MISMATCH

- Contract (booking.yaml): `403 VISITOR_MISMATCH`
- Observed: `404 NOT_FOUND` (check "tickets other visitor", requestId `41c1f41ebdd523864b11945bc378b132`)
- Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/bookings/c9c90a66-187e-4a2c-9fe2-d07679467731/tickets' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p1-t2-907985'`
- Read: **contract error (self-contradiction).** Ownership failures on tickets are 404 on purpose ("anyone else gets NOT_FOUND, as if the booking did not exist", get_tickets.go:31-47) and the route description in the same contract says "Selain itu 404". The 403 `VISITOR_MISMATCH` row contradicts that text. Remove the row (404 `NOT_FOUND` is R091 PASS).

### R120 — PATCH /internal/v1/admin/events/{eventId} → 409

- Contract (internal-booking.yaml): `409`
- Observed: `400 VALIDATION_ERROR` (check "admin patch on-sale maxTicketsPerOrder", requestId `4a179b729967b00091b2b4f809af9987`)
- Reproduce: `curl -s -i -X PATCH 'http://localhost:8082/internal/v1/admin/events/46430194-f7ee-4bf1-a9d3-9313d70c5567' -H 'Authorization: Bearer eyJhbGciO…' -H 'Content-Type: application/json' -d '{"maxTicketsPerOrder": 2}'`
- Read: **contract error (unreachable).** The locked columns (`max_tickets_per_order`, `max_tickets_per_buyer_nik`, `allow_mixed_categories`, trigger guard_event_rules, 0001_init.up.sql:38-50) are not part of `AdminEventConfigUpdate`; PATCH with those fields is rejected as unknown field (400 `VALIDATION_ERROR`) and the fields that PATCH does accept (TTLs, edit settings, city, genres, description) stay editable after sales open (200). So 409 cannot occur on this route.

### R161 — POST /api/v1/payment-page-sessions → 401 UNAUTHORIZED

- Contract (payment.yaml): `401 UNAUTHORIZED`
- Observed: `403 PAYMENT_PAGE_TOKEN_INVALID` (check "page-session garbage visitor token", requestId `c12e7016e4c21c3c4351d6b9c2ec7d21`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8083/api/v1/payment-page-sessions' -H 'X-Visitor-Token: not-a-token' -H 'Content-Type: application/json' -d '{"pageToken": "<masked>"}'`
- Read: **contract error (self-contradiction).** The operation declares `security: []` (anonymous; registered without auth middleware, payment_handler.go:37) and authenticates by the one-time page token only. A garbage visitor token is ignored and the response is the 403 `PAYMENT_PAGE_TOKEN_INVALID` row (R160, PASS). Remove the 401 row.

### R203 — POST /api/v1/queue-entries/{entryId}/claim → 410 EVENT_SOLD_OUT

- Contract (queue.yaml): `410 EVENT_SOLD_OUT`
- Observed: `404 NOT_FOUND` (check "claim after sold out", requestId `ad80aef11b6cc8c2d28c237e83a4879d`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8081/api/v1/queue-entries/71934691-ac22-43d6-aeb7-3a4a9d5851da/claim?eventId=9b4dcdb9-c5ab-4c83-8bfe-2c52970cf8f1' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p2-w-983725'`
- Read: **contract error or code gap (owner decision).** `ErrSoldOut` is returned only by join (join.go:211). The claim script maps NOT_FOUND/GONE to 404 (queue_store.go:268), and a sold-out waiting entry is ended with `SOLD_OUT` (visible on `GET /queue-entries/me`) and then answers claim with 404 `NOT_FOUND`. The 410 `EVENT_SOLD_OUT` row on claim is never produced.

### R205 — POST /api/v1/queue-entries/{entryId}/claim → 429 RATE_LIMITED

- Contract (queue.yaml): `429 RATE_LIMITED`
- Observed: `409 NOT_ADMITTED` (check "claim burst", requestId `dd376be9b9dabf66468b8d83c7d4e3fb`)
- Reproduce: `curl -s -i -X POST 'http://localhost:8081/api/v1/queue-entries/95134a95-6435-416d-992e-36751dfef452/claim?eventId=3216b9bf-ed33-4f45-99d5-481d39a7e0e7' -H 'X-Visitor-Token: eyJhbGciO…' -H 'X-Device-Id: dev-p8-b-19513'`
- Read: **contract error or code gap (owner decision).** No limiter is wired on claim: `WithLimiter` is applied to `me` and `heartbeat` only (queue_module.go:122-126). 140 claims in one burst by one visitor all returned 409 `NOT_ADMITTED`. Either add the limiter or drop the row.

### R028 — GET /booking-sessions/{sessionId}/stream → 503 `DEPENDENCY_UNAVAILABLE` (not triggerable)

The connection cap (hard-coded 1000 per instance, booking_module.go:143) and the `IncrConn` failure are evaluated inside the usecase after the 200 has been written, and the error is swallowed (sse_handler.go:50). A 503 status cannot reach the client; the only 503 in that handler is "response writer is not a Flusher". Not driven at runtime (needs >1000 concurrent streams); the same mechanism is observed live in R030/R031. Read: contract error / code gap, like R030.

## 4. Deviations inside rows that still PASS

These rows are satisfied by another trigger (usually a garbage token → 401), but a sibling trigger of the same row deviates. They matter more than most of the mismatching rows above.

- **D1 `GET /api/v1/booking-sessions/{sessionId}`** — contract: 401 without a visitor token (security: visitor cookie/header); observed: **200 with the full session** (check "session get no token", requestId `b4fe80b1f45ce159131019b40f5021ad`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/booking-sessions/d8ed1c1f-9cab-474d-a9b2-891a289f654b'`  
  Read: CODE BUG: `session.go:57` only compares owners when `visitorID != ""`, so an anonymous caller skips the check. Anyone who has (or guesses) a session UUID reads the session (masked buyer NIK, categories, booking snapshot). Garbage token -> 401 as documented.
- **D2 `PUT /api/v1/bookings/{bookingId}/items`** — contract: 401 without a visitor token; observed: **200 — the booking was edited** (check "edit no token", requestId `3c022488a348d465e7d04371fa0a9c5f`). Reproduce: `curl -s -i -X PUT 'http://localhost:8082/api/v1/bookings/03092add-14bd-40ae-b9a5-2824f056db11/items' -H 'Idempotency-Key: k-p1-1794203996516878458-6804' -H 'Content-Type: application/json' -d '{"version": 1, "items": [{"categoryId": "81c223de-476c-4fc9-999f-264f4265cff5", "attendee": {"nik": "3174********8524", "fullName": "Pemegang e0-0"}}]}'`  
  Read: CODE BUG (highest impact): `edit.go:46` has the same `cmd.VisitorID != ""` guard, so an unauthenticated PUT replaces the holders/categories of someone else's HELD booking when the booking UUID is known; `version` starts at 1 and a wrong guess just answers 409 `BOOKING_VERSION_CONFLICT`, so it is trivially probed.
- **D3 `POST /api/v1/booking-sessions/{sessionId}/attendee-checks`** — contract: 401 without a visitor token; 403 for another visitor; observed: **200 in both cases** (check "attendee-check no token", requestId `c1f1cd158abf28415d692f54933552c5`). Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/booking-sessions/d8ed1c1f-9cab-474d-a9b2-891a289f654b/attendee-checks' -H 'Content-Type: application/json' -d '{"nik": "3174********8496"}'`  
  Read: CODE BUG: see R038 (no ownership check at all).
- **D4 `POST /api/v1/bookings (hold)`** — contract: 401 without a visitor token; observed: **500 `INTERNAL_ERROR`** (check "hold no visitor token", requestId `d7cab34708fedb2adff8ae8a0cc2c258`). Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/bookings' -H 'Idempotency-Key: k-p1-1794203995748546657-82104' -H 'Content-Type: application/json' -d '{"sessionId": "de8dd3cb-3f03-4ab8-a194-514ca0ce4bf4", "buyer": {"nik": "3174********8809", "email": "buyer-p1-h1@sim.test", "emailConfirmation": "buyer-p1-h1@sim.test", "consent": true}, "items": [{"categoryId": "e6890432-7bb7-4dc8-84b2-54a2c2c1e459", "attendee": {"nik": "3174********2944", "fullNam'`  
  Read: CODE BUG: the idempotency store is keyed by visitor id; an empty id reaches Postgres as `uuid ""` (`idem: begin: ... invalid input syntax for type uuid: ""`, repositories.go:132). Should be 401; a 500 also pollutes error-rate alerts.
- **D5 `POST /api/v1/booking-sessions`** — contract: 401 without a visitor token; observed: **201 (session opened with the admission token alone)** (check "create session without visitor token", requestId `4e851f3c5e2f4e7b4ffb382862985f37`). Reproduce: `curl -s -i -X POST 'http://localhost:8082/api/v1/booking-sessions' -H 'Content-Type: application/json' -d '{"admissionToken": "eyJhbGciO…"}'`  
  Read: CODE vs CONTRACT: middleware.go:21-29 says this is intended ("session creation ... is authorised by the admission token"). The contract still lists the visitor scheme as required and documents 401. Decide which is true; if the admission token alone is enough, mark the operation `security: []` + admission token and drop the visitor requirement.
- **D6 `GET /api/v1/booking-sessions/current`** — contract: 401 without a visitor token; observed: **404 `NOT_FOUND`** (check "session current no token", requestId `a4f4e35f8985685300f30c6e7eb88ee1`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/booking-sessions/current?eventId=eae04d1f-bffc-4cdf-91b8-51aae7040214'`  
  Read: CODE vs CONTRACT: no visitor -> `FindActiveByVisitor("")` finds nothing -> 404. Safe, but the status differs from the documented 401.
- **D7 `GET /api/v1/bookings/{bookingId}; POST /api/v1/bookings/{bookingId}/cancel; POST /api/v1/booking-sessions/{sessionId}/cancel`** — contract: 401 without a visitor token; observed: **403 `VISITOR_MISMATCH`** (check "get booking no token", requestId `c4fd367041bf2b2a55ecd0d32e704507`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/bookings/0bb4b685-eb06-4e69-8d61-065637c9c9af'`  
  Read: CODE vs CONTRACT: access is denied (safe) but with 403 instead of 401 (empty id != owner id). Cancel session without token: 403, same cause (cancel_session.go:28).
- **D8 `GET /api/v1/booking-sessions/{sessionId}/stream`** — contract: 401 without a visitor token; observed: **200 with an empty stream** (check "stream no token", requestId `1c65e80b5c3305f5c88cd3402ac9cfe9`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/booking-sessions/d8ed1c1f-9cab-474d-a9b2-891a289f654b/stream'`  
  Read: Same mechanism as R030/R031.
- **D9 `GET /api/v1/bookings/{bookingId}/tickets`** — contract: 401 without a visitor token; observed: **404 `NOT_FOUND`** (check "tickets no token", requestId `c92ef9dfabdafcc524fb2f721c1367cf`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/api/v1/bookings/c9c90a66-187e-4a2c-9fe2-d07679467731/tickets'`  
  Read: By design (ticket-link flow, get_tickets.go:31); the 401 row passes with a garbage token. Contract could say so.
- **D10 `GET /internal/v1/bookings/{bookingId}/payable; POST .../payment-lock`** — contract: Global `serviceBearer`: "Service token Queue (scope service, kunci Queue)"; observed: **401 for a valid Queue service token; accepted only with a Payment-issued token (iss rbt-payment, aud rbt-booking)** (check "payable queue token (wrong caller)", requestId `ae09436dbad10d6e406a0a48316966f8`). Reproduce: `curl -s -i -X GET 'http://localhost:8082/internal/v1/bookings/5846ae61-4316-4bd5-b317-c105fb4e0e0d/payable' -H 'Authorization: Bearer eyJhbGciO…'`  
  Read: CONTRACT ERROR: internal_handler.go `paymentOnly` is deliberate (audit F2). The contract's security scheme text does not describe a Payment credential; the same holds for `deadline-extension`, which accepts the Queue token and answers 401 to a Payment token (observed, not a documented caller).

Garbage visitor token → `401 UNAUTHORIZED` was observed on every visitor-protected route (booking, payment, queue, notification), as documented.

## 5. Rows that could not be driven

| Row | Route | Reason |
|---|---|---|
| R028 (NOT_TRIGGERABLE) | GET /api/v1/booking-sessions/{sessionId}/stream → 503 DEPENDENCY_UNAVAILABLE | SSE handler commits 200 + headers before the usecase runs and discards the usecase error (sse_handler.go streamSession: `_ = h.stream.Stream(...)`), so the connection-cap/IncrConn 503 can never reach the status line; the cap itself is hard-coded to 1000 per instance (booking_module.go NewStreamSession(..., 1000)). Not driven at runtime (would need >1000 concurrent streams); see MISMATCH rows R030/R031 for the same mechanism observed live (unknown session / other visitor -> 200 with an empty stream). |
| R146 (NOT_APPLICABLE) | POST /api/v1/push-subscriptions → 401 UNAUTHORIZED | The contract text itself says the body errorCode is currently VALIDATION_ERROR, not UNAUTHORIZED (status 401 decides). Observed 401 VALIDATION_ERROR on the same route (row R145 PASS), so there is nothing further to exercise for UNAUTHORIZED. |
| R232 (NOT_TRIGGERABLE) | POST /api/v1/auth/otp/verify → 403 ACCOUNT_DISABLED | ACCOUNT_DISABLED needs a disabled account. No API disables an account and the brief forbids direct DB writes; a read-only query shows all 195 users.accounts rows are ACTIVE. |
| R237 (NOT_TRIGGERABLE) | POST /api/v1/auth/refresh → 403 ACCOUNT_DISABLED | Same as R232 (refresh of a disabled account). |
| R241 (NOT_TRIGGERABLE) | POST /api/v1/auth/logout → 500 INTERNAL_ERROR | Logout 500 INTERNAL_ERROR requires the Valkey denylist write to fail; the only way is to break the shared Valkey cluster (forbidden: infra must stay untouched). A temporary instance cannot isolate it because the denylist is in the shared cluster. |

## 6. Responses the code returned that the contract does not document

From the request logs after the start (status/route not in the contract for that service), including my deliberate probes of malformed bodies and unknown ids. No documented status was returned with an *undocumented errorCode* (0 such cases).

| Service | Route | Status | errorCode | Count | Example requestId | Note |
|---|---|---:|---|---:|---|---|
| booking | GET /internal/v1/bookings/{bookingId}/payable | 404 | NOT_FOUND | 2 | `771128430c753269903d50f445e19d9b` | status not documented for this route |
| booking | PATCH /internal/v1/admin/events/{eventId} | 404 | NOT_FOUND | 1 | `9078d874ac75f6bfd5e92f91d795e002` | status not documented for this route |
| booking | POST /api/v1/booking-sessions/{sessionId}/attendee-checks | 410 | SESSION_EXPIRED | 1 | `a25d9bacaaaa1ff03fc7dad754855d25` | status not documented for this route |
| booking | POST /api/v1/bookings | 403 | VISITOR_MISMATCH | 1 | `e9c558a1933898d707dd327914f5d1db` | status not documented for this route |
| booking | POST /api/v1/bookings | 500 | INTERNAL_ERROR | 1 | `d7cab34708fedb2adff8ae8a0cc2c258` | status not documented for this route |
| booking | POST /internal/v1/admin/events | 400 | VALIDATION_ERROR | 1 | `4e07eadd8d7733f923baa72173aafa8e` | status not documented for this route |
| booking | POST /internal/v1/allocations/{allocationId}:release | 400 | VALIDATION_ERROR | 1 | `6cab58d47eb59ed4460d26c003d2e395` | status not documented for this route |
| booking | POST /internal/v1/allocations/{allocationId}:release | 404 | NOT_FOUND | 3 | `73c9d9fb069a1e4eb5834746bb508e45` | status not documented for this route |
| booking | POST /internal/v1/allocations:batch | 400 | VALIDATION_ERROR | 2 | `685339114780083c99a86c29dfcd3803` | status not documented for this route |
| booking | POST /internal/v1/bookings/{bookingId}/deadline-extension | 400 | VALIDATION_ERROR | 1 | `fed1cc13514b4a6435a435977b836e8a` | status not documented for this route |
| booking | POST /internal/v1/bookings/{bookingId}/deadline-extension | 409 | DEADLINE_ALREADY_EXTENDED | 3 | `7cc7cbcd1ddd95263d9deb27fe365694` | status not documented for this route |
| booking | POST /internal/v1/bookings/{bookingId}/payment-lock | 400 | VALIDATION_ERROR | 1 | `bf3e63d09b079754141287499958c319` | status not documented for this route |
| booking | POST /internal/v1/bookings/{bookingId}/payment-lock | 404 | NOT_FOUND | 1 | `2972943f96b1378fb0b302b0e960e82e` | status not documented for this route |
| notification | DELETE /api/v1/push-subscriptions/{eventId} | 401 | VALIDATION_ERROR | 1 | `6c2d38fd3c7feb928893f4450bde3303` | status not documented for this route |
| notification | DELETE /api/v1/push-subscriptions/{eventId} | 500 | INTERNAL_ERROR | 1 | `04e2550d0bf441f9a3bd46f83e698574` | status not documented for this route |
| payment | POST /api/v1/payment-page-sessions | 400 | VALIDATION_ERROR | 1 | `fba8516b3d3678c01e201d293279db9d` | status not documented for this route |
| payment | POST /api/v1/payments/{bookingId}/attempts | 400 | VALIDATION_ERROR | 2 | `db02f1ee8462fb200dc25723bce4f00d` | status not documented for this route |
| payment | POST /api/v1/webhooks/midtrans | 400 | VALIDATION_ERROR | 2 | `4a4ec4411ba5ff7fd9bcf1c4c79e901c` | status not documented for this route |
| payment | POST /internal/v1/payments/{bookingId}/resolve | 400 | VALIDATION_ERROR | 2 | `f6e822a2f2ed989c33938ef192cc370a` | status not documented for this route |
| payment | POST /internal/v1/payments/{bookingId}/resolve | 401 | UNAUTHORIZED | 5 | `c95ff3aaac9e86a0b0fa8195226a31af` | status not documented for this route |

Read: the 400 `VALIDATION_ERROR` entries are generic malformed-body answers (contract omits them on internal and payment routes) — contract gaps; 404/409/401 on internal routes are legitimate answers the internal contracts leave out. The two entries that are not harmless: `POST /bookings` 500 (D4) and `DELETE /push-subscriptions/{eventId}` 500 (section 7). `POST /bookings` 403 `VISITOR_MISMATCH` appears when a visitor sends someone else's `sessionId` (reasonable; add to the contract).

## 7. Other observations (not matrix rows)

- **Service-token lifetime is not enforced on the Queue-key routes.** A Queue-signed service token with a 1 h lifetime is accepted by `GET /internal/v1/events/{eventId}/supply` (200), `allocations:batch`, `allocations/{id}:release`, `deadline-extension` (the latter three only failed on body validation / state, not on auth). The Payment-key routes (`payable`, `payment-lock`) and Payment's `resolve` reject the same >5 min token (401), because they go through `httpx.ServiceAuth` which caps the lifetime (`ServiceTokenMaxTTL`, pkg/httpx/auth.go:101-107); Queue-key routes use `security.BearerVerifier`/`withUser` which does not. Not a documented row; flagged because the contract does not mention either behaviour.
- **`GET /api/v1/queue-status/{eventId}` is 404 for a few seconds after an event is configured.** The status document is published by the queue worker; a fresh event answers 404 `NOT_FOUND` for roughly 3 s (7 polls of 0.5 s in the p2 run) although the event exists. Contract says 404 = "Event tidak ada". Clients that poll right after publish see a transient 404.
- **Access log labels.** Garbage-token 401s produced by the visitor middleware are logged with `route:"unmatched"` (it runs before routing), and `POST /internal/v1/allocations/{allocationId}:release` is registered/logged as `/internal/v1/allocations/{rest}`. Metrics/route dashboards keyed by contract path will not line up.
- **`DELETE /api/v1/push-subscriptions/{eventId}`.** Non-UUID eventId -> 500 `INTERNAL_ERROR` (subscription_repository.go:42, `invalid input syntax for type uuid`); no token -> 401 with errorCode `VALIDATION_ERROR` (same documented quirk as POST); unknown eventId -> 204. The contract documents only 204 for this operation.

## 8. Summary of findings

Code bugs (fix in code): D2 (unauthenticated `PUT /bookings/{id}/items` edits a booking), D1 (unauthenticated session read), R038/D3 (attendee-check without ownership), D4 (hold without token → 500), `DELETE /push-subscriptions/{eventId}` non-UUID → 500.  
Contract errors / dead rows: R012, R013, R030, R031, R092, R120, R161, R028; owner decision: R203, R205, D5, D6, D7, D10.  
Everything else in the matrix behaves as documented.

## 9. What could not be verified

- R232/R237 `ACCOUNT_DISABLED` and R241 logout `INTERNAL_ERROR` (reasons in section 5). Their code paths exist (auth.go:226, 288) but were not executed.
- R028 SSE 503 (section 3) — reasoning from code plus the live R030/R031 behaviour, not a 1001-connection test.
- R012/R013 were driven with an admission token 75 s old and with a session opened 4 s after the claim deadline; other ways to reach a 410 on that route were not found in the code (section 3), so none was tried.
- Email-based OTP per-IP-hour limit (1000/h, shared 127.0.0.1 bucket) was not exhausted; the `RATE_LIMITED` row on `otp/request` is covered by the per-email limit (6th request within the hour, 61 s apart), which returns the same status and errorCode.
- `/health` 503 was produced on temporary instances through a closed Postgres forwarder, not on the real processes (stopping Postgres is out of bounds); every real health endpoint answered 200 before and after.
- The 429 on booking `current` / `attendee-checks` was produced on a temporary booking instance with `RATE_LIMIT_PER_IP=5`; the real one has the limiter disabled by `start.sh`, so there it never answers 429.
- Replay/idempotency semantics, ticket-link (`?t=`) access, web cookie flows and the happy-path journeys are covered by the earlier suites (`run_all.sh`) and were not repeated here.
- Payload shapes of 2xx responses were not compared with the schemas; only status and errorCode.

## 10. State after the pass

All nine health endpoints (8081–8085, 8091–8094) answered 200 at the end; the only processes started or stopped were the ones listed in sections 1.3/1.4, each restored (pids file updated). Temporary instances and TCP forwarders were killed. No Docker container, Valkey key or table was created, removed or flushed by hand; test data was created through the APIs.
