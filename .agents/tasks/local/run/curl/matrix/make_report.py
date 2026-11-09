#!/usr/bin/env python3
"""Generate .agents/tasks/fix/contract-matrix/report.md from matrix.tsv + recorded calls + undocumented.tsv. Prose judgements live here."""
import os, re, json, glob, collections, csv
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.abspath(os.path.join(HERE, '../../../../fix/contract-matrix'))
rows = list(csv.DictReader(open(os.path.join(OUT, 'matrix.tsv')), delimiter='\t'))
recs = []
for f in sorted(glob.glob(os.path.join(HERE, '..', 'results', 'matrix', '*.jsonl'))):
    for l in open(f):
        r = json.loads(l); r['file'] = os.path.basename(f)[:-6]; recs.append(r)
start = open(os.path.join(OUT, 'pass-start.txt')).read().strip()
xc = json.load(open(os.path.join(OUT, 'log-crosscheck.json')))
und = list(csv.DictReader(open(os.path.join(OUT, 'undocumented.tsv')), delimiter='\t'))


def mask(s):
    s = re.sub(r'\b(\d{4})\d{8}(\d{4})\b', r'\1********\2', s)            # NIK-like numbers
    s = re.sub(r'"code": ?"\d{6}"', '"code":"******"', s)
    s = re.sub(r'"(pageToken|refreshToken|accessToken|visitorToken|admissionToken)": ?"[^"]{12,}"', r'"\1": "<masked>"', s)
    s = re.sub(r'(eyJ[A-Za-z0-9_-]{6})[A-Za-z0-9_.-]+', r'\1…', s)
    return s


def curl_of(name_part=None, row=None):
    for r in recs:
        if name_part and r['name'] == name_part:
            return mask(r['curl']), r['rid'], r['status'], r['code']
    return '', '', '', ''


def esc(s):
    return s.replace('|', '\\|')

FILES = ['booking.yaml', 'internal-booking.yaml', 'internal-payment.yaml', 'notification.yaml', 'payment.yaml', 'queue.yaml', 'user.yaml']
cnt = {f: collections.Counter() for f in FILES}
for r in rows:
    cnt[r['contract']][r['result']] += 1
tot = collections.Counter(r['result'] for r in rows)
N = len(rows)

# ---- judgements ---------------------------------------------------------------------------------------------------
MISM = {
 'R012': ('contract error (unreachable)', 'Activate() (admission_repo.go:76-88) updates `WHERE status=\'RESERVED\' AND expires_at > now` and maps "no row" to `ErrAdmissionToken` (403); an expired admission token fails earlier in `Verify` (security.go:78) with the same 403. `SESSION_EXPIRED` is never returned by `POST /booking-sessions`.', 'create session with expired admission token'),
 'R013': ('contract error (unreachable)', '`ErrTurnMissed` is declared (port/inbound/errors.go:73) but has no caller in Booking. A session opened after the claim deadline gets the same 403 `ADMISSION_TOKEN_INVALID` (Activate matches no row). The "turn missed" case is reported by Queue on `claim` (R204, PASS), not by Booking.', 'create session 4 s after the claim deadline'),
 'R030': ('contract error / code gap (by design in code)', 'The SSE handler commits `200` + headers first and discards the usecase result (`_ = h.stream.Stream(...)`, sse_handler.go:50); only a malformed UUID can still produce a status (400). Unknown session and foreign session both end as an empty 200 stream. No data is leaked (the usecase returns before writing), so this is a status-code/contract gap, not a data exposure.', 'stream unknown'),
 'R031': ('contract error / code gap (by design in code)', 'Same mechanism as R030 (stream_session.go:63 returns `ErrVisitorMismatch` after the 200 is already on the wire).', 'stream other visitor'),
 'R038': ('CODE BUG (missing ownership check)', '`CheckAttendee` takes no visitor id and never compares it with the session owner (check_attendee.go:41-80; handler session_handler.go:87 passes only the path id). Any caller who knows a session UUID — with another visitor\'s token or none at all — gets 200 and consumes that session\'s attendee-check quota; verified live with no token against another visitor\'s session: a NIK already holding a ticket in the event answers `ALREADY_REGISTERED`, a free one `AVAILABLE` (NIK-registration oracle).', 'attendee-check other visitor'),
 'R092': ('contract error (self-contradiction)', 'Ownership failures on tickets are 404 on purpose ("anyone else gets NOT_FOUND, as if the booking did not exist", get_tickets.go:31-47) and the route description in the same contract says "Selain itu 404". The 403 `VISITOR_MISMATCH` row contradicts that text. Remove the row (404 `NOT_FOUND` is R091 PASS).', 'tickets other visitor'),
 'R120': ('contract error (unreachable)', 'The locked columns (`max_tickets_per_order`, `max_tickets_per_buyer_nik`, `allow_mixed_categories`, trigger guard_event_rules, 0001_init.up.sql:38-50) are not part of `AdminEventConfigUpdate`; PATCH with those fields is rejected as unknown field (400 `VALIDATION_ERROR`) and the fields that PATCH does accept (TTLs, edit settings, city, genres, description) stay editable after sales open (200). So 409 cannot occur on this route.', 'admin patch on-sale maxTicketsPerOrder'),
 'R161': ('contract error (self-contradiction)', 'The operation declares `security: []` (anonymous; registered without auth middleware, payment_handler.go:37) and authenticates by the one-time page token only. A garbage visitor token is ignored and the response is the 403 `PAYMENT_PAGE_TOKEN_INVALID` row (R160, PASS). Remove the 401 row.', 'page-session garbage visitor token'),
 'R203': ('contract error or code gap (owner decision)', '`ErrSoldOut` is returned only by join (join.go:211). The claim script maps NOT_FOUND/GONE to 404 (queue_store.go:268), and a sold-out waiting entry is ended with `SOLD_OUT` (visible on `GET /queue-entries/me`) and then answers claim with 404 `NOT_FOUND`. The 410 `EVENT_SOLD_OUT` row on claim is never produced.', 'claim after sold out'),
 'R205': ('contract error or code gap (owner decision)', 'No limiter is wired on claim: `WithLimiter` is applied to `me` and `heartbeat` only (queue_module.go:122-126). 140 claims in one burst by one visitor all returned 409 `NOT_ADMITTED`. Either add the limiter or drop the row.', 'claim burst'),
}
NT_NOTE = {}

DEV = [  # (title, route, contract, observed, name in recs, judgement)
 ('D1', 'GET /api/v1/booking-sessions/{sessionId}', '401 without a visitor token (security: visitor cookie/header)', '200 with the full session', 'session get no token',
  'CODE BUG: `session.go:57` only compares owners when `visitorID != ""`, so an anonymous caller skips the check. Anyone who has (or guesses) a session UUID reads the session (masked buyer NIK, categories, booking snapshot). Garbage token -> 401 as documented.'),
 ('D2', 'PUT /api/v1/bookings/{bookingId}/items', '401 without a visitor token', '200 — the booking was edited', 'edit no token',
  'CODE BUG (highest impact): `edit.go:46` has the same `cmd.VisitorID != ""` guard, so an unauthenticated PUT replaces the holders/categories of someone else\'s HELD booking when the booking UUID is known; `version` starts at 1 and a wrong guess just answers 409 `BOOKING_VERSION_CONFLICT`, so it is trivially probed.'),
 ('D3', 'POST /api/v1/booking-sessions/{sessionId}/attendee-checks', '401 without a visitor token; 403 for another visitor', '200 in both cases', 'attendee-check no token',
  'CODE BUG: see R038 (no ownership check at all).'),
 ('D4', 'POST /api/v1/bookings (hold)', '401 without a visitor token', '500 `INTERNAL_ERROR`', 'hold no visitor token',
  'CODE BUG: the idempotency store is keyed by visitor id; an empty id reaches Postgres as `uuid ""` (`idem: begin: ... invalid input syntax for type uuid: ""`, repositories.go:132). Should be 401; a 500 also pollutes error-rate alerts.'),
 ('D5', 'POST /api/v1/booking-sessions', '401 without a visitor token', '201 (session opened with the admission token alone)', 'create session without visitor token',
  'CODE vs CONTRACT: middleware.go:21-29 says this is intended ("session creation ... is authorised by the admission token"). The contract still lists the visitor scheme as required and documents 401. Decide which is true; if the admission token alone is enough, mark the operation `security: []` + admission token and drop the visitor requirement.'),
 ('D6', 'GET /api/v1/booking-sessions/current', '401 without a visitor token', '404 `NOT_FOUND`', 'session current no token',
  'CODE vs CONTRACT: no visitor -> `FindActiveByVisitor("")` finds nothing -> 404. Safe, but the status differs from the documented 401.'),
 ('D7', 'GET /api/v1/bookings/{bookingId}; POST /api/v1/bookings/{bookingId}/cancel; POST /api/v1/booking-sessions/{sessionId}/cancel', '401 without a visitor token', '403 `VISITOR_MISMATCH`', 'get booking no token',
  'CODE vs CONTRACT: access is denied (safe) but with 403 instead of 401 (empty id != owner id). Cancel session without token: 403, same cause (cancel_session.go:28).'),
 ('D8', 'GET /api/v1/booking-sessions/{sessionId}/stream', '401 without a visitor token', '200 with an empty stream', 'stream no token',
  'Same mechanism as R030/R031.'),
 ('D9', 'GET /api/v1/bookings/{bookingId}/tickets', '401 without a visitor token', '404 `NOT_FOUND`', 'tickets no token',
  'By design (ticket-link flow, get_tickets.go:31); the 401 row passes with a garbage token. Contract could say so.'),
 ('D10', 'GET /internal/v1/bookings/{bookingId}/payable; POST .../payment-lock', 'Global `serviceBearer`: "Service token Queue (scope service, kunci Queue)"', '401 for a valid Queue service token; accepted only with a Payment-issued token (iss rbt-payment, aud rbt-booking)', 'payable queue token (wrong caller)',
  'CONTRACT ERROR: internal_handler.go `paymentOnly` is deliberate (audit F2). The contract\'s security scheme text does not describe a Payment credential; the same holds for `deadline-extension`, which accepts the Queue token and answers 401 to a Payment token (observed, not a documented caller).'),
]

OTHER_OBS = [
 ('Service-token lifetime is not enforced on the Queue-key routes', 'A Queue-signed service token with a 1 h lifetime is accepted by `GET /internal/v1/events/{eventId}/supply` (200), `allocations:batch`, `allocations/{id}:release`, `deadline-extension` (the latter three only failed on body validation / state, not on auth). The Payment-key routes (`payable`, `payment-lock`) and Payment\'s `resolve` reject the same >5 min token (401), because they go through `httpx.ServiceAuth` which caps the lifetime (`ServiceTokenMaxTTL`, pkg/httpx/auth.go:101-107); Queue-key routes use `security.BearerVerifier`/`withUser` which does not. Not a documented row; flagged because the contract does not mention either behaviour.', ['supply ttl>5m', 'allocations batch ttl>5m', 'deadline-extension ttl>5m']),
 ('`GET /api/v1/queue-status/{eventId}` is 404 for a few seconds after an event is configured', 'The status document is published by the queue worker; a fresh event answers 404 `NOT_FOUND` for roughly 3 s (7 polls of 0.5 s in the p2 run) although the event exists. Contract says 404 = "Event tidak ada". Clients that poll right after publish see a transient 404.', ['status doc']),
 ('Access log labels', 'Garbage-token 401s produced by the visitor middleware are logged with `route:"unmatched"` (it runs before routing), and `POST /internal/v1/allocations/{allocationId}:release` is registered/logged as `/internal/v1/allocations/{rest}`. Metrics/route dashboards keyed by contract path will not line up.', []),
 ('`DELETE /api/v1/push-subscriptions/{eventId}`', 'Non-UUID eventId -> 500 `INTERNAL_ERROR` (subscription_repository.go:42, `invalid input syntax for type uuid`); no token -> 401 with errorCode `VALIDATION_ERROR` (same documented quirk as POST); unknown eventId -> 204. The contract documents only 204 for this operation.', ['push unsubscribe non-uuid (observe)']),
]

# ---- build text --------------------------------------------------------------------------------------------------
L = []
w = L.append
w('# Contract matrix pass (Batch 4) — report')
w('')
w(f'Repo `rusuh-booking-be`, branch `feature/rework` @ `469f478`. Pass started **{start}**; coverage is measured only from requests after that time. Verification only: nothing under `services/`, `pkg/`, `docs/contracts/` or migrations was changed.')
w('')
w('## 1. Method')
w('')
w(f'1. **Matrix.** `curl/gen_matrix.py` parses `docs/contracts/openapi/*.yaml` (7 files, current content) into one row per *method × path × status × errorCode*; errorCodes are the backticked UPPER_SNAKE tokens of the response description (single words such as `UNAUTHORIZED` included, so this is larger than the earlier 237-row matrix); a response with no code in its text is one row that is satisfied by the status alone. `/health` 200/503 of every contract file is included. **{N} rows.**')
w('2. **Driving.** `curl/matrix/` (Python `requests`, real HTTP to the running stack; `mlib.py` records every call with its contract row, observed status/errorCode, `X-Request-Id` and a masked curl). Parts: `p1` booking public, `p2` queue, `p3` payment (+ internal resolve), `p4` user + notification, `p5` internal booking (admin + service routes with service tokens signed with `deployments/compose/keys/*` exactly like `pkg/token`), `p7` real timers (session TTL, admission token, claim window, OTP TTL and per-email limit), `p9` temporary instances. `p1–p5,p7,p9` ran in parallel; then alone: `p6` outages, `p8` rate limits, `p0` health. Entry point `run_matrix.sh`; `build_matrix.py` joins rows and records; `analyze_logs.py` cross-checks the service request logs.')
w('3. **Outages (p6).** One local process at a time, restored with the same binary/cwd/env as `start.sh` and `pids` updated: Payment API (booking cancel 503), fake Midtrans (attempt 503), Booking API (simulator 503, queue join with a cold sale-window cache 503, queue-settings PUT 503). No container was touched.')
w('4. **Temporary instances (p9).** The shared stack must not be put in these states, so extra instances of the same binaries ran on spare ports and were killed afterwards: booking with `RATE_LIMIT_PER_IP=5` (429 on `current` and `attendee-checks`; the main booking runs with the limiter off); all five services behind a local TCP forwarder to the existing Postgres that the script closes (`/health` 503); user behind a forwarder to the existing RabbitMQ that is closed (OTP request 503 — the OTP is published to the broker).')
w(f'5. **Log cross-check.** {xc["requests"]:,} request log lines after the start were read; of {xc["recorded"]} recorded requestIds, {xc["found"]} were found in the five api logs with the same status ({xc["status_equal"]} equal); the remaining {xc["recorded"]-xc["found"]} are the ten `/health` calls (health is not request-logged) and the 24 calls to the temporary instances (their own `log/tmp-*.log`).')
w('6. **Harness notes (honest account).** Scripts live in `.agents/tasks/local/run/curl/` (`gen_matrix.py`, `t_matrix.sh`, `matrix/`). Final evidence comes from non-overlapping runs after the start time: the parallel group, then `p6`, `p9`, `p0` and `p8` alone. While developing the pass, one `p6` rerun overlapped with `p9`; its process lookup picked an older, still-draining Booking process, so that run\'s Booking-outage rows were invalid, and the overlapping `p9` run lost its temporary instance. Both were discarded: the lookup now takes the newest pid and waits for the old one to exit, and `p6` and `p9` were re-run alone. Booking, Payment and fake Midtrans were restarted several times by this pass (never left down). After the pass, one manual check (two anonymous `attendee-checks`, section 3 / R038) was run outside the recorded files to confirm the oracle behaviour.')
w('')
w('## 2. Coverage')
w('')
w(f'**{N} rows: {tot["PASS"]} PASS ({tot["PASS"]/N*100:.1f}%), {tot["MISMATCH"]} exercised but mismatching, {tot["NOT_TRIGGERABLE"]} not triggerable locally, {tot["NOT_APPLICABLE"]} not applicable (contract text negates the row).** No row is left unexercised.')
w('')
w('| Contract file | Rows | Exercised, status + errorCode match | Exercised, mismatch | Not triggerable locally | Not applicable |')
w('|---|---:|---:|---:|---:|---:|')
for f in FILES:
    c = cnt[f]; w(f'| {f} | {sum(c.values())} | {c["PASS"]} | {c["MISMATCH"]} | {c["NOT_TRIGGERABLE"]} | {c["NOT_APPLICABLE"]} |')
w(f'| **total** | **{N}** | **{tot["PASS"]}** | **{tot["MISMATCH"]}** | **{tot["NOT_TRIGGERABLE"]}** | **{tot["NOT_APPLICABLE"]}** |')
w('')
w('Per-row result and evidence (check name + requestId): `matrix.tsv`.')
w('')
w('## 3. Mismatching rows (10 rows) and R028')
w('')
w('Each "Reproduce" line is the exact request recorded by the check named next to it (tokens masked); the ids and tokens come from that check\'s setup in `curl/matrix/pN_*.py` (join → claim → session → hold, etc.). "Read" is checked against the code (file:line), not assumed. Buckets: **code bug** = the code should change; **contract error** = the contract documents something the code cannot or should not produce; **owner decision** = either side could move.')
w('')
by = {r['id']: r for r in rows}
for rid in sorted(MISM):
    r = by[rid]; kind, why, nm = MISM[rid]
    cu, rq, st, cd = curl_of(nm)
    w(f'### {rid} — {r["method"]} {r["path"]} → {r["status"]} {r["errorCode"]}'.rstrip())
    w('')
    w(f'- Contract ({r["contract"]}): `{r["status"]}{" " + r["errorCode"] if r["errorCode"] else ""}`')
    w(f'- Observed: `{st}{" " + cd if cd else ""}` (check "{nm}", requestId `{rq}`)')
    w(f'- Reproduce: `{cu}`' if cu else '- Reproduce: see matrix.tsv')
    w(f'- Read: **{kind}.** {why}')
    w('')
w('### R028 — GET /booking-sessions/{sessionId}/stream → 503 `DEPENDENCY_UNAVAILABLE` (not triggerable)')
w('')
w('The connection cap (hard-coded 1000 per instance, booking_module.go:143) and the `IncrConn` failure are evaluated inside the usecase after the 200 has been written, and the error is swallowed (sse_handler.go:50). A 503 status cannot reach the client; the only 503 in that handler is "response writer is not a Flusher". Not driven at runtime (needs >1000 concurrent streams); the same mechanism is observed live in R030/R031. Read: contract error / code gap, like R030.')
w('')
w('## 4. Deviations inside rows that still PASS')
w('')
w('These rows are satisfied by another trigger (usually a garbage token → 401), but a sibling trigger of the same row deviates. They matter more than most of the mismatching rows above.')
w('')
for (i, route, contract, obs, nm, jud) in DEV:
    cu, rq, st, cd = curl_of(nm)
    w(f'- **{i} `{route}`** — contract: {contract}; observed: **{obs}** (check "{nm}", requestId `{rq}`). Reproduce: `{cu}`  ')
    w(f'  Read: {jud}')
w('')
w('Garbage visitor token → `401 UNAUTHORIZED` was observed on every visitor-protected route (booking, payment, queue, notification), as documented.')
w('')
w('## 5. Rows that could not be driven')
w('')
w('| Row | Route | Reason |')
w('|---|---|---|')
for r in rows:
    if r['result'] in ('NOT_TRIGGERABLE', 'NOT_APPLICABLE'):
        w(f'| {r["id"]} ({r["result"]}) | {r["method"]} {r["path"]} → {r["status"]} {r["errorCode"]} | {esc(r["evidence"])} |')
w('')
w('## 6. Responses the code returned that the contract does not document')
w('')
w('From the request logs after the start (status/route not in the contract for that service), including my deliberate probes of malformed bodies and unknown ids. No documented status was returned with an *undocumented errorCode* (0 such cases).')
w('')
w('| Service | Route | Status | errorCode | Count | Example requestId | Note |')
w('|---|---|---:|---|---:|---|---|')
for u in und:
    w(f'| {u["service"]} | {u["method"]} {u["route"]} | {u["status"]} | {u["errorCode"]} | {u["count"]} | `{u["exampleRequestId"]}` | {u["note"]} |')
w('')
w('Read: the 400 `VALIDATION_ERROR` entries are generic malformed-body answers (contract omits them on internal and payment routes) — contract gaps; 404/409/401 on internal routes are legitimate answers the internal contracts leave out. The two entries that are not harmless: `POST /bookings` 500 (D4) and `DELETE /push-subscriptions/{eventId}` 500 (section 7). `POST /bookings` 403 `VISITOR_MISMATCH` appears when a visitor sends someone else\'s `sessionId` (reasonable; add to the contract).')
w('')
w('## 7. Other observations (not matrix rows)')
w('')
for (t, d, _) in OTHER_OBS:
    w(f'- **{t}.** {d}')
w('')
w('## 8. Summary of findings')
w('')
w('Code bugs (fix in code): D2 (unauthenticated `PUT /bookings/{id}/items` edits a booking), D1 (unauthenticated session read), R038/D3 (attendee-check without ownership), D4 (hold without token → 500), `DELETE /push-subscriptions/{eventId}` non-UUID → 500.  ')
w('Contract errors / dead rows: R012, R013, R030, R031, R092, R120, R161, R028; owner decision: R203, R205, D5, D6, D7, D10.  ')
w('Everything else in the matrix behaves as documented.')
w('')
w('## 9. What could not be verified')
w('')
w('- R232/R237 `ACCOUNT_DISABLED` and R241 logout `INTERNAL_ERROR` (reasons in section 5). Their code paths exist (auth.go:226, 288) but were not executed.')
w('- R028 SSE 503 (section 3) — reasoning from code plus the live R030/R031 behaviour, not a 1001-connection test.')
w('- R012/R013 were driven with an admission token 75 s old and with a session opened 4 s after the claim deadline; other ways to reach a 410 on that route were not found in the code (section 3), so none was tried.')
w('- Email-based OTP per-IP-hour limit (1000/h, shared 127.0.0.1 bucket) was not exhausted; the `RATE_LIMITED` row on `otp/request` is covered by the per-email limit (6th request within the hour, 61 s apart), which returns the same status and errorCode.')
w('- `/health` 503 was produced on temporary instances through a closed Postgres forwarder, not on the real processes (stopping Postgres is out of bounds); every real health endpoint answered 200 before and after.')
w('- The 429 on booking `current` / `attendee-checks` was produced on a temporary booking instance with `RATE_LIMIT_PER_IP=5`; the real one has the limiter disabled by `start.sh`, so there it never answers 429.')
w('- Replay/idempotency semantics, ticket-link (`?t=`) access, web cookie flows and the happy-path journeys are covered by the earlier suites (`run_all.sh`) and were not repeated here.')
w('- Payload shapes of 2xx responses were not compared with the schemas; only status and errorCode.')
w('')
w('## 10. State after the pass')
w('')
w('All nine health endpoints (8081–8085, 8091–8094) answered 200 at the end; the only processes started or stopped were the ones listed in sections 1.3/1.4, each restored (pids file updated). Temporary instances and TCP forwarders were killed. No Docker container, Valkey key or table was created, removed or flushed by hand; test data was created through the APIs.')
open(os.path.join(OUT, 'report.md'), 'w').write(mask('\n'.join(L)) + '\n')
print('report.md written,', len(L), 'lines')
