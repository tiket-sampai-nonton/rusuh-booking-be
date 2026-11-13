# Audit Fixes 2026-11-12 — Combined Plan (Booking + User detailed, Booking/Queue roadmap)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the defects found in the 2026-11-12 audits. **Part A (Booking) and Part B (User)** close every open Booking and
User finding of the service anomaly audit, task by task, each with a red test first. **Part C** (detailed 2026-11-13)
closes the remaining Booking/Queue findings (functionality review, performance audit, Queue anomalies) the same way; its perf
tasks add k6/benchmark before/after evidence.

This file merges and replaces `.git/feature-workflow/plan.md` (Audit Fixes — Roadmap + Phase 1) and the Booking + User
anomaly plan. Roadmap Phase 1 = Task B0; roadmap items already covered by a Part A/B task are not repeated in Part C.

**Architecture:** Fixes stay inside the existing layers: stock and state changes in Booking's unit of work
(`infrastructure/postgres/unit_of_work.go`) and repositories, flow rules in usecases, session/profile rules in User's
`auth.go` / `profile.go`. No new service, no new library. One Booking interface removal (`FindByJTI`), two small port
additions (`DeferVerification`, `SettleSpec.AmountMinor`), one User port change (`VerifyAccess` also returns the session id).
Parts A/B need no migration; Part C adds Booking 0013–0015 and Queue 0006 (see *DB / config / compatibility*).

**Tech Stack:** Go 1.26, pgx v5, PostgreSQL 16, Valkey (valkey-go), testify + mockgen; integration tests with build tag
`integration` + `TEST_POSTGRES_DSN`.

**Spec:** `docs/evidence/2026-11-12-service-anomaly-audit.md` (IDs `P*`), `docs/evidence/2026-11-12-performance-scalability-audit.md`
(IDs `PF-*`), functionality review of the 2026-11-12 session (IDs `F*`, listed in Part C); `specs/booking-service/design.md`
§7.2, §9.0, §9.5; SRS-F-16-03; `docs/contracts/`.

## Global Constraints

- Invariants 1–6 (`.kiro/steering/invariants.md`) keep holding; stock changes only inside one PostgreSQL transaction.
- Booking lock order stays admission → booking → supply → quota → category (§9.0).
- Outbox only for stock/money changes, in the same transaction. Event payloads carry no NIK/email/token plaintext.
- Test data: fake NIKs, `example.test` emails. Go via `GOWORK=off`. `gofmt -l` clean. Mocks only via `make mock`.
- Local Go is 1.18; run Go through Docker (verified: go1.26.11, `go build ./...` OK), from the repo root:
  `GO() { docker run --rm --network host -v "$PWD":/src -v rbt-gomod:/go/pkg/mod -v rbt-gobuild:/root/.cache/go-build -w /src/services/$SVC -e GOWORK=off -e GOTOOLCHAIN=auto -e TEST_POSTGRES_DSN -e TEST_VALKEY_SERVER golang:1.26-alpine go "$@"; }`
  with `SVC=booking` or `SVC=user`. Unit: `GO test ./...`; integration: `GO test -tags integration ./internal/.../postgres/`.
  No `-race` locally (alpine has no gcc); CI runs it.
- Test infra = this project's compose only (`docker compose up -d postgres valkey`, already up):
  `TEST_POSTGRES_DSN=postgres://rbt:rbt-local-only@localhost:5433/rbt?sslmode=disable`,
  `TEST_VALKEY_SERVER='[{"host":"localhost","port":6380}]'`. Never use host port 5432/6379 (another project's containers).
- **Commit after every task** on branch `feature/audit-fixes`, as the owner's git identity (`git config user.name` =
  Ahmad Nuzirwan; do not pass `--author`, do not change git config). Conventional commits, **no** `Co-Authored-By` trailer
  and no Claude attribution (project `CLAUDE.md`). Stage only the task's files (`git add <paths>`, never `-A`); never commit
  `jembut.md`, `tests/e2e/login_nik_test.go`, `.codegraph/`. No push, no PR.
- Owner decisions confirmed 2026-11-13: D-B3, D-B5, D-B6, D-B9, D-U1, D-U4 as written; D-B12 = keep old data; P2-23 = keep
  plaintext in `GET /me` (record it in U7).
- Every file here is owned by nuzirwan. Items owned by arnu12rahman are listed under *Coordination* and are not executed.

## Review Focus

1. **A webhook arrives during the cancel race.** Buyer presses cancel, Payment answers NOT_PAID, PaymentLock commits before
   `Expire(CANCELLED)` → the booking must stay payable. *(B2 test)*
2. **The sweeper meets a booking that is stuck on UNKNOWN.** After grace it must not be retried every tick ahead of newer due
   bookings. *(B5 test: 100 stuck rows + 1 new due row → the new row is returned)*
3. **Two browser tabs refresh at once.** The loser must get `SESSION_INVALID` and the winner's new session must stay alive.
   *(U1 test "concurrent refresh within grace")*
4. **Logout in one tab, refresh in another.** Only that session ends; other devices stay logged in. *(U1 test)*
5. **The same `payment.succeeded` is delivered twice with a wrong amount.** Exactly one REVIEW, no tickets, stock released
   once. *(B7 test, second call)*
6. **The sweeper retries an expire-with-review.** Second call is a no-op, stock released exactly once. *(B0: second call in
   `TestExpire_ReviewReleasesHeldStockOnce`)*
7. **REVIEW on an admission still `EDITABLE`.** Outstanding units released once, admission CONVERTED. *(B0: existing branch
   kept; the test asserts allocated stays 0)*
8. **`payment.succeeded` after a REVIEW decision.** Stays REVIEW, no tickets, no second release. *(B0: existing
   `TestSettle_AfterAReviewDecisionIsANoOp` stays green)*

Part C:

9. **A visitor queues for two events and rejoins one of them.** The other event's heartbeat/me/claim keep working with the
   new token; the old token stops there once the new one was used. *(C6.1 `TestTokenGen_MultiEventRejoinKeepsOtherEventValid`)*
10. **The promoter loses its lease after Booking granted.** The entry stays WAITING; when the reservation lapses, the next
    tick gets GRANTED again instead of ending the buyer. *(C6.6 `TestReserveBatch_LapsedUnclaimedReservationIsGrantedAgain`)*
11. **All free stock is allocated while admitted buyers are choosing.** Each admitted buyer still sees their own reserved
    units as available in the SSE stream. *(C5.1 `TestStream_AvailabilityAddsOwnReservedUnits`)*
12. **Stock returns after a sell-out.** The sale reopens inside the window; a visitor whose entry ended SOLD_OUT gets a new
    entry, not the old one. *(C5.3 `TestStatusPublisher_ReopensSaleWhenStockReturns`, C5.4 `TestSoldOutEntryIsTerminal`)*
13. **Holds run while a 100-entry batch reserves on the same event, and two Booking workers sweep the same backlog.** No
    deadlock, ledger exact, no row processed twice. *(C7.2 `TestReserveBatch_ConcurrentWithHoldsKeepsLedger`, C7.3
    `TestClaimDueSessions_TwoSweepersNeverShareRows`)*

---

## Part A — Booking (detailed)

### Task B0: Verify and close Phase 1 (P0-4, P0-5, P3-5) — code already in the working tree

The working tree already has `releaseHeldItems`, the REVIEW path releasing stock + `booking.journey.ended`, the
unknown-settlement-time rule, and three tests (`TestExpire_ReviewReleasesHeldStockOnce`,
`TestSettle_UnknownSettlementTimeIsTimely`, `TestSettle_KnownLateSettlementGoesToReviewAndReleases`). Nothing has run yet.

**Files:** `services/booking/internal/booking/infrastructure/postgres/unit_of_work.go`, `unit_of_work_integration_test.go`
(both modified, uncommitted).

- [ ] **Step 1: Prove the tests are red without the fix.** `git stash push services/booking/internal/booking/infrastructure/postgres/unit_of_work.go`,
  run `$GO test -tags integration -run 'TestExpire_ReviewReleasesHeldStockOnce|TestSettle_UnknownSettlementTimeIsTimely' ./internal/booking/infrastructure/postgres/`
  → both FAIL (held stock not released / status REVIEW). `git stash pop`.
- [ ] **Step 2: Run the whole postgres integration package** → PASS, including the existing
  `TestSettle_AfterExpiryGoesToReviewWithoutTicketsOrStockChange` and `TestSettle_AfterAReviewDecisionIsANoOp`.
  If the first one asserts "stock unchanged" on REVIEW, update its assertion to "stock released" (behaviour change of P0-5)
  and say so in the commit body.
- [ ] **Step 3: Commit** (after owner OK) `fix(booking): release held stock on REVIEW and treat unknown settlement time as timely`.

### Task B1: Session re-claim finds the admission by id (P1-3)

Queue signs the admission token with `jti = allocationId = admissions.id`; `Activate` already looks up by `id`, but the
re-claim fallback uses `FindByJTI` (column `admission_jti`, a different random UUID), so it never matches.

**Files:**
- Modify: `services/booking/internal/booking/application/usecase/session.go:47` — `FindByJTI` → `GetByID`.
- Remove: `FindByJTI` from `port/outbound/ports.go` (`IAdmissionRepo`), `infrastructure/postgres/admission_repo.go:53-57`,
  the fake in `application/usecase/fakes_test.go`, and its case in `postgres/repos_integration_test.go`; regenerate mocks.
- Test: `services/booking/internal/booking/application/usecase/session_test.go`

**Interfaces:** Produces: `IAdmissionRepo` without `FindByJTI` (no other caller).

- [ ] **Step 1: Write the failing test** `TestCreateSession_ReclaimOfActiveSessionReturnsIt`: verifier returns
  `claims{JTI: admissionID, VisitorID: "v-1"}`; `Activate` → `inbound.ErrAdmissionToken`; `GetByID(admissionID)` → ACTIVE
  admission of `v-1`. Assert `NoError`, `resp.SessionID == admissionID`. Second case: admission owned by `v-2` →
  `ErrorIs(err, inbound.ErrAdmissionToken)`.
- [ ] **Step 2: Run** `$GO test ./internal/booking/application/usecase/ -run TestCreateSession_Reclaim` → FAIL (`FindByJTI` called).
- [ ] **Step 3: Implement** the swap, remove `FindByJTI`, run `make mock`.
- [ ] **Step 4: Run** `$GO test ./...` and `$GO vet ./...` in `services/booking` → PASS.
- [ ] **Step 5: Commit** `fix(booking): find re-claimed session by admission id`.

### Task B2: Cancel is refused once payment started, checked under the row lock (P1-4)

**Files:**
- Modify: `infrastructure/postgres/unit_of_work.go` `expire()` step 3 (~L695-705) — also select `payment_started_at`;
  after the `IsPayable` check: `if spec.Reason == "CANCELLED" && paymentStarted != nil { return false, inbound.ErrBookingLocked }`.
- Test: `infrastructure/postgres/unit_of_work_integration_test.go`

- [ ] **Step 1: Write the failing test** `TestExpire_CancelAfterPaymentLockIsRefused`: `id := f.hold(t)`;
  `f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, CorrelationID: id})` → OK; then
  `Expire(ExpireSpec{BookingID: id, Reason: "CANCELLED", CorrelationID: id})` →
  `ErrorIs(err, inbound.ErrBookingLocked)`, `False(acted)`, status `PENDING_PAYMENT`, `f.stock(t)` unchanged from right after
  hold, 0 rows with `routing_key='booking.expired'`.
- [ ] **Step 2: Run** → FAIL (booking CANCELLED).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the postgres integration package + `$GO test ./internal/booking/application/usecase/ -run Cancel` → PASS.
- [ ] **Step 5: Commit** `fix(booking): refuse cancel when payment started inside the expire transaction`.

### Task B3: Deadline extension only while pending, recorded as an event; PaymentLock always stamps (P1-5)

Decision **D-B3** (owner OK at checkpoint): extension is allowed only in `PENDING_PAYMENT` and only while
`payment_expires_at > now()`. A VERIFYING booking is already past its deadline and must go through the sweeper.

**Files:**
- Modify: `domain/event/events.go` — add `TypeBookingDeadlineExtended = "BookingDeadlineExtended"` and
  `type BookingDeadlineExtended struct { BookingID string; Seconds int; Reason string }` (json `bookingId`, `seconds`, `reason`).
- Modify: `infrastructure/postgres/unit_of_work.go` `extendDeadline` (~L812) — lock the row
  (`SELECT status, version, payment_expires_at, deadline_extended_at … FOR UPDATE`); errors: not found → `ErrNotFound`;
  already extended → `ErrDeadlineAlreadyExtended`; status ≠ `PENDING_PAYMENT` → `ErrNotPayable`;
  `now >= payment_expires_at` → `ErrPaymentExpired`. Then UPDATE with `version = version+1` and `u.store.Append` the new event.
- Modify: `paymentLock` (~L905) — idempotent return only when `paymentStarted != nil`; a VERIFYING booking without a stamp
  gets stamped (Step 4 runs). Keep the `now.After(paymentExpiresAt)` refusal before it.
- Test: `unit_of_work_integration_test.go`

- [ ] **Step 1: Write failing tests:**
  - `TestExtendDeadline_RecordsEventAndRefusesVerifying`: hold → extend 120s → `NoError`; `payment_expires_at` moved by 120s;
    1 row in `booking.booking_events` with `event_type='BookingDeadlineExtended'`; second extend →
    `ErrDeadlineAlreadyExtended`. New booking moved to VERIFYING with `StartVerifying` (after setting
    `payment_expires_at = now() - 1s`) → extend → `ErrorIs(err, inbound.ErrNotPayable)`.
  - `TestPaymentLock_StampsAVerifyingBookingWithoutStamp`: booking set by SQL to `status='VERIFYING'`,
    `verification_until = now() + 1 min` (table CHECK), `payment_started_at = NULL`, `payment_expires_at = now() + 1 min` →
    `PaymentLock` → `Outcome == "OK"` and `payment_started_at IS NOT NULL`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** postgres integration package + `$GO test ./internal/booking/application/usecase/ -run 'Extend|PaymentLock'` → PASS.
- [ ] **Step 5: Commit** `fix(booking): extend deadline only while pending and record it as an event`.

### Task B4: Sold-out announcement survives the savepoint rollback (P1-6)

**Files:**
- Modify: `infrastructure/postgres/admission_repo.go:177-185` (`reserveOne`, full sold-out branch) — first
  `ROLLBACK TO SAVEPOINT entry`, then in a new `SAVEPOINT sold_out`: the existing `UPDATE booking.events SET status='SOLD_OUT' …`
  and `enqueueOutboxTx(… "booking.sold_out" …)`; on any error `ROLLBACK TO SAVEPOINT sold_out` (outcome stays `SOLD_OUT`;
  the next reserve detects it again). Write the outbox row at most once per batch: `ReserveBatch` passes a
  `soldOutAnnounced *bool` to `reserveOne`.
- Test: `infrastructure/postgres/reserve_release_integration_test.go`

- [ ] **Step 1: Write the failing test** `TestReserveBatch_SoldOutIsAnnouncedOnce`: event with capacity 1, sold by SQL
  (`sold_units = total_capacity`); `ReserveBatch` with 2 entries → both `SOLD_OUT`; exactly 1 outbox row with
  `routing_key='booking.sold_out'`.
- [ ] **Step 2: Run** → FAIL (0 rows).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): write sold-out status and event after the entry savepoint rollback`.

### Task B5: Expiry sweeper skips bookings still in grace and backs off UNKNOWN (P1-7)

Decision **D-B5**: `verification_until` doubles as "next check at" for VERIFYING rows; backoff after UNKNOWN or Payment
unreachable = **30 s** (`verifyBackoff`, constant in `expire.go`). The column is scheduling state, so no event is appended.

**Files:**
- Modify: `infrastructure/postgres/booking_repo.go:115` `DueBookingIDs` —
  `WHERE (status='PENDING_PAYMENT' AND payment_expires_at < $1) OR (status='VERIFYING' AND verification_until <= $1)
   ORDER BY COALESCE(verification_until, payment_expires_at) LIMIT $2`.
- Add: `func (r *BookingRepo) DeferVerification(ctx context.Context, bookingID string, until time.Time) error`
  (`UPDATE … SET verification_until=$2, updated_at=now() WHERE id=$1::uuid AND status='VERIFYING'`), declared on
  `outbound.IBookingRepo`; `make mock`; add to the usecase fake.
- Modify: `application/usecase/expire.go` — on `Resolve` error or `OutcomeUnknown`: call
  `DeferVerification(ctx, id, now.Add(verifyBackoff))` (its error joined) and return the original error.
- Tests: `postgres/repos_integration_test.go`, `application/usecase/` (new `expire_test.go` or existing usecase test file).

**Interfaces:** Produces `IBookingRepo.DeferVerification(ctx context.Context, bookingID string, until time.Time) error`.

- [ ] **Step 1: Write failing tests:**
  - `TestDueBookingIDs_SkipsVerifyingInGrace`: 3 VERIFYING rows with `verification_until = now()+1 min`, 1 PENDING_PAYMENT row
    with `payment_expires_at = now()-1 s`; `DueBookingIDs(now, 2)` → only the PENDING id.
  - `TestExpireBooking_UnknownDefersNextCheck`: VERIFYING booking past grace with `PaymentStartedAt` set; Resolve →
    `OutcomeUnknown` → `ErrorIs(err, inbound.ErrDependency)` and `DeferVerification(id, now+30s)` called once.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): sweeper skips bookings in grace and backs off unknown payments`.

### Task B6: Outbox relay claims rows and uses the row id as message id (P1-1, Booking part)

Decision **D-B6**: claim by lease, no long transaction: the claim pushes `available_at` 30 s ahead, so a second relay replica
skips the row; a crashed relay's rows reappear after 30 s. `messageId = outbox row id`, so a republish is deduplicated by
Notification (`source_message_id`).

**Files:**
- Modify: `infrastructure/postgres/outbox_writer.go` `Pending` → rename `Claim(ctx, limit int, lease time.Duration)`:
  `UPDATE booking.outbox_events SET available_at = now() + make_interval(secs => $2)
    WHERE id IN (SELECT id FROM booking.outbox_events WHERE published_at IS NULL AND available_at <= now()
                 ORDER BY available_at LIMIT $1 FOR UPDATE SKIP LOCKED)
   RETURNING id::text, routing_key, aggregate_id::text, schema_version, payload, publish_attempts`.
- Modify: `infrastructure/rabbitmq/consumer.go` — `OutboxStore.Pending` → `Claim(ctx, limit, lease)`; `RunOnce` passes
  `30*time.Second`; after `NewEnvelope`, set `env.MessageID = row.ID`.
- Tests: `infrastructure/rabbitmq/outbox_relay_test.go` (unit, fake store), `outbox_relay_integration_test.go`.

- [ ] **Step 1: Write failing tests:**
  - unit `TestOutboxRelay_MessageIDIsRowID`: fake store returns one row `ID: "0190…"`; the published envelope's `MessageID == "0190…"`.
  - integration `TestOutboxClaim_TwoRelaysNeverGetTheSameRow`: insert 10 pending rows; two `Claim(ctx, 10, 30s)` calls in
    parallel goroutines → union has 10 ids, intersection empty.
- [ ] **Step 2: Run** → FAIL (compile error / different id).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./internal/booking/infrastructure/rabbitmq/...` (unit + `-tags integration`) → PASS.
- [ ] **Step 5: Commit** `fix(booking): claim outbox rows with a lease and publish the row id as message id`.

### Task B7: Payment consumer: permanent errors and amount check (P2-8)

**Files:**
- Modify: `port/outbound/unit_of_work.go` `SettleSpec` — add `AmountMinor int64` (0 = unknown, skip the check).
- Modify: `application/usecase/settle.go` — pass `cmd.AmountMinor`.
- Modify: `unit_of_work.go` `settle()` — after loading `amount`: `mismatch := spec.AmountMinor > 0 && spec.AmountMinor != amount`;
  treat `mismatch` exactly like the late branch (REVIEW, `releaseHeldItems`, `booking.review`, `booking.journey.ended`).
- Modify: `infrastructure/rabbitmq/consumer.go` `handle` — wrap the Settle error in `pkgrabbitmq.Permanent` when
  `errors.Is(err, inbound.ErrNotFound)` or it is an `*apperror.Error` with `Code == inbound.CodeValidation`.
- Tests: `infrastructure/rabbitmq/consumer_test.go`, `unit_of_work_integration_test.go`.

**Interfaces:** Produces `outbound.SettleSpec.AmountMinor int64`.

- [ ] **Step 1: Write failing tests:**
  - `TestPaymentConsumer_UnknownOrMalformedBookingIsPermanent`: settle fake returns `inbound.ErrNotFound` →
    `rabbitmq.IsPermanent(err)` true; returns `inbound.Validation("bookingId", "…")` → true; returns `errors.New("db")` → false.
  - `TestSettle_AmountMismatchGoesToReviewOnce`: hold; `Settle` with `AmountMinor = amount+1` → `False(settled)`, status
    REVIEW, 0 tickets, stock all zero; repeat the same call → still 1 `booking.review` row, stock unchanged.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): review settlements with a wrong amount and stop retrying unknown bookings`.

### Task B8: Submit grace reachable over HTTP (P2-6)

**Files:**
- Modify: `application/usecase/hold.go:272` (`plan`) and `application/usecase/check_attendee.go:57` — replace
  `admission.IsExpired(now)` with `admission.PastGrace(now)`.
- Add: `func (a Admission) PastGrace(now time.Time) bool` in `domain/entity/admission.go`
  (`!now.Before(a.ExpiresAt.Add(time.Duration(a.CfgSubmitGraceSeconds) * time.Second))`).
- Tests: `domain/entity/booking_test.go` (or new `admission_test.go`), `application/usecase/hold_test.go`.

- [ ] **Step 1: Write failing tests:** table test `TestAdmission_PastGrace` (`ExpiresAt=T`, grace 10 s: `T-1s` false, `T+5s`
  false, `T+10s` true); `TestHold_InGraceIsAccepted`: admission `ExpiresAt = now-5s`, grace 10 s → plan passes and
  `uow.Hold` is called.
- [ ] **Step 2: Run** → FAIL (`SESSION_EXPIRED`).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(booking): accept hold and attendee check inside the submit grace window`.

### Task B9: Idempotency record IN_PROGRESS gets a short lease (P2-7)

Decision **D-B9**: an IN_PROGRESS record older than **60 s** (or any record past `expires_at`) may be taken over by the same
key. A client whose hold committed but whose `Complete` failed recovers the booking from `GET /sessions/{id}` (the existing
`booking` field), so no extra lookup is added.

**Files:**
- Modify: `infrastructure/postgres/repositories.go:125` `Begin` — `ON CONFLICT (scope, owner_id, idempotency_key) DO UPDATE
  SET request_hash=EXCLUDED.request_hash, state='IN_PROGRESS', response_status=NULL, response_body=NULL,
  created_at=now(), expires_at=EXCLUDED.expires_at
  WHERE idempotency_records.expires_at <= now()
     OR (idempotency_records.state='IN_PROGRESS' AND idempotency_records.created_at < now() - interval '60 seconds')`;
  `RowsAffected()==1` still means "fresh".
- Test: `postgres/repos_integration_test.go`.

- [ ] **Step 1: Write the failing test** `TestIdempotencyBegin_StaleInProgressIsTakenOver`: `Begin` → fresh; set
  `created_at = now() - interval '2 minutes'` by SQL; `Begin` again with a different hash → `found == false`. A
  COMPLETED record 2 minutes old → `found == true`, state COMPLETED.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): let a stale in-progress idempotency key be retried`.

### Task B10: Hold re-checks the event and uses the admission's rule snapshot (P2-9, P2-10)

`MaxTicketsPerOrder` needs no snapshot: `outstanding_units` was clamped to it at reservation and the hold transaction already
refuses `n > outstanding`. Only the mixed-category flag is read live today.

**Files:**
- Modify: `infrastructure/postgres/unit_of_work.go` `hold()` after the supply lock (Step 2, ~L83) — `SELECT status FROM
  booking.events WHERE id=$1::uuid`; `CANCELLED` → `inbound.ErrEventCancelled`.
- Modify: `application/usecase/hold.go:307` and `application/usecase/edit.go:139` — use `admission.CfgAllowMixedCategories`
  instead of `detail.Event.AllowMixedCategories` (edit: read the admission it already loads).
- Tests: `unit_of_work_integration_test.go`, `hold_test.go`, `edit_session_test.go`.

- [ ] **Step 1: Write failing tests:** `TestHold_EventCancelledInsideTransaction` (cancel event by SQL after `f.seed`, then
  `uow.Hold` → `ErrorIs(err, inbound.ErrEventCancelled)`, stock unchanged); `TestHold_MixedRuleComesFromAdmissionSnapshot`
  (event allows mixed, admission snapshot `CfgAllowMixedCategories=false`, two categories → `ErrMixedNotAllowed`).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): re-check event cancel in hold and use the admission rule snapshot`.

### Task B11: Small contract fixes (P3-4, P3-6)

**Files:**
- Modify: `unit_of_work.go` `expire()` EXPIRED path (~L795) — `outcome := "EXPIRED"; if spec.Reason == "CANCELLED" { outcome = "CANCELLED" }`
  (contract enum already has `CANCELLED`; Queue's `end_entry.lua` treats any non-PAID outcome the same).
- Modify: `infrastructure/postgres/booking_repo.go` `ListByAccount` — cursor `"<RFC3339Nano>_<id>"`;
  `WHERE … AND (b.created_at, b.id) < ($2, $3::uuid) ORDER BY b.created_at DESC, b.id DESC`. A cursor without `_`
  (old format) uses `id = 'ffffffff-ffff-ffff-ffff-ffffffffffff'`. Cursor stays an opaque string in `booking.yaml`.
- Tests: `unit_of_work_integration_test.go`, `repos_integration_test.go`.

- [ ] **Step 1: Write failing tests:** `TestExpire_BuyerCancelEndsJourneyAsCancelled` (payload `outcome` = `CANCELLED`);
  `TestListByAccount_SameCreatedAtIsNotSkipped` (3 bookings with identical `created_at`, `limit=2` → page 1 has 2, page 2 has 1,
  no id repeated).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): cancelled journey outcome and stable my-bookings paging`.

### Task B12: NIK hash format — parity test, old data kept (P1-19)

Decision **D-B12 (owner, 2026-11-13): keep old data unchanged** — no reset, no re-hash, no dual-hash migration. Accepted
residual risk, written into the audit: rows hashed before `14286cd` (`hash(nik)`) do not match the new `hash("nik:"+nik)`,
so a NIK that bought before the change can buy up to 3 more on the same event, and an attendee NIK used before the change
can be used once more. New data is consistent.

**Files:**
- Test: `services/booking/internal/booking/infrastructure/security/security_test.go` —
  `TestHashNIK_MatchesUserFormat`: with a fixed 32-byte test key, `HashNIK("3201010101900001")` equals
  `crypto.NewHasher(key).Hash("nik:3201010101900001")` (the same `pkg/crypto` call User makes; a cross-service import is not allowed).
- Modify: `docs/evidence/2026-11-12-service-anomaly-audit.md` — P1-19 row: "decided: old data kept (D-B12), residual risk as above".

- [ ] **Step 1: Write the test** → PASS expected (it pins current behaviour); then change the prefix locally to `"x:"` and see
  it FAIL, revert.
- [ ] **Step 2: Update the audit row.**
- [ ] **Step 3: Commit** `test(booking): pin NIK hash format to the User service format`.

---

## Part B — User (detailed)

### Task U1: Reuse detection only for rotated tokens, with a concurrent-refresh grace (P1-14, P1-15)

Decision **D-U1**: a token rotated **≤ 20 s** ago (`refreshGrace`, constant in `auth.go`) is a concurrent refresh, not
theft: answer `SESSION_INVALID`, revoke nothing. A logged-out token (`replaced_by IS NULL`) → `SESSION_INVALID`, revoke
nothing.

**Files:**
- Modify: `services/user/internal/user/domain/entity/account.go` `Session` — add `ReplacedBy *string`;
  `func (s Session) Rotated() bool { return s.ReplacedBy != nil }`.
- Modify: `infrastructure/postgres/repositories.go` `sessionCols` / `scanSession` — add `replaced_by::text`.
- Modify: `application/usecase/auth.go:271-276` — `if sess.Revoked() { if sess.Rotated() && now.Sub(*sess.RevokedAt) > refreshGrace { handleReuse … } return ErrSessionInvalid }`.
- Test: `application/usecase/auth_test.go` (`TestRefresh` subtests), `postgres` integration test for the scan.

- [ ] **Step 1: Write failing subtests in `TestRefresh`:**
  - "logged-out token does not revoke other sessions": session `RevokedAt=now-1h`, `ReplacedBy=nil` → `ErrSessionInvalid`,
    `RevokeAllForAccount` not called.
  - "concurrent refresh within grace": `RevokedAt=now-5s`, `ReplacedBy=&"s-2"` → `ErrSessionInvalid`, no revoke.
  - "reuse after grace revokes all": `RevokedAt=now-1min`, `ReplacedBy=&"s-2"` → `RevokeAllForAccount` called once, audit
    `AuditRefreshReused`.
- [ ] **Step 2: Run** `$GO test ./internal/user/application/usecase/ -run TestRefresh` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` + `$GO test -tags integration ./internal/user/infrastructure/postgres/` → PASS.
- [ ] **Step 5: Commit** `fix(user): treat only rotated tokens as reuse, with a 20s concurrent-refresh grace`.

### Task U2: Absolute session end survives rotation (P2-22)

**Files:**
- Modify: `application/usecase/auth.go` `openSession` — new last parameter `expiresAt time.Time`; zero → `now.Add(RefreshTTL)`.
  `VerifyOTP` passes zero; `Refresh` passes `sess.ExpiresAt`.
- Test: `auth_test.go`.

- [ ] **Step 1: Write the failing subtest** "rotation keeps the original expiry": active session `ExpiresAt=now+1h`;
  `Sessions.Rotate` is expected with `next.ExpiresAt == now+1h` (gomock matcher on the field).
- [ ] **Step 2: Run** → FAIL (`now+RefreshTTL`).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(user): keep the absolute session expiry across refresh rotation`.

### Task U3: Profile checks the account status and tolerates missing NIK ciphertext (P2-21, P3-22)

**Files:**
- Modify: `application/usecase/profile.go` — `GetProfile` and `UpdateProfile` load the account first and return
  `inbound.ErrAccountDisabled` when `!acc.Active()` (`UpdateProfile` gains one `GetByID` call). In `GetProfile`, when
  `nikEnc == nil` leave `resp.NIK` empty (masked value stays).
- Test: `application/usecase/profile_test.go`.

- [ ] **Step 1: Write failing subtests:** `TestGetProfile` "disabled account" → `ErrAccountDisabled`, no unseal called;
  "has NIK but nik_enc NULL" → `NoError`, `resp.NIK == ""`, `resp.NIKMasked` set; `TestUpdateProfile` "disabled account" →
  `ErrAccountDisabled`, `UpdateProfile` repo not called.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(user): refuse profile access for disabled accounts and handle missing NIK ciphertext`.

### Task U4: NIK change is rate limited and ends the old access token (P1-16, P2-19)

Decisions **D-U4**: at most **3 NIK changes per account per 24 h** (limiter key `profile:nik:acct:<id>`, limiter error →
fail closed with `ErrRateLimited`, because this is a security control). `409 NIK_ALREADY_LINKED` stays (the FE needs it;
the limit bounds enumeration to 3 probes/day/account). After a NIK change the current session id is denylisted for
`AccessTTL + Leeway`; the client gets 401, calls `POST /auth/refresh`, and the new access token carries the new `nik_hash`
(`openSession` already reads it from the DB). Name-only changes do neither.

**Files:**
- Modify: `port/inbound` `IAccessTokenVerifier.VerifyAccess(tok string) (accountID, sessionID string, err error)`;
  `infrastructure/security/security.go:178` returns `c.SessionID`; the auth middleware in `infrastructure/http/auth_handler.go`
  stores the session id in the context next to the account id (`sessionIDFrom(ctx)`).
- Modify: `inbound.IProfileUsecase.UpdateProfile(ctx, accountID, sessionID string, req)`; `Profile` gains
  `limiter outbound.IRateLimiter`, `denylist revocation.Denylist`, `accessTTL time.Duration` (wired in
  `delivery/user_module.go`).
- Modify: `docs/contracts/openapi/user.yaml` — `PATCH /me`: add `429`, and a description line "after a NIK change the
  current access token is revoked; refresh to get a token with the new NIK".
- Tests: `profile_test.go`, `infrastructure/http` handler test, `security` test.

**Interfaces:** Produces `VerifyAccess(tok string) (string, string, error)` and
`UpdateProfile(ctx context.Context, accountID, sessionID string, req dto.UpdateProfileRequest) (dto.ProfileResponse, error)`.

- [ ] **Step 1: Write failing tests:** `TestUpdateProfile` "4th NIK change in 24h" → limiter `Allowed=false` →
  `ErrRateLimited`, repo not called; "NIK change revokes current access token" → `Denylist.Revoke(ctx, "sid-1", AccessTTL+Leeway)`
  called after a successful update; "name only" → limiter and denylist not called; "limiter error" → `ErrRateLimited`.
- [ ] **Step 2: Run** → FAIL (compile).
- [ ] **Step 3: Implement**, `make mock`.
- [ ] **Step 4: Run** `$GO test ./...` in `services/user`, `make contracts` → PASS.
- [ ] **Step 5: Commit** `fix(user): rate limit NIK changes and revoke the access token that carries the old NIK`.

### Task U5: OTP lock answers OTP_LOCKED until the code would have expired (P3-16)

**Files:**
- Modify: `infrastructure/valkey/otp_store.go` `verifyLua` — on lock: `DEL KEYS[1]`, keep `KEYS[2]` (counter) with the code's
  remaining TTL; at the top: `if not stored then local n = tonumber(redis.call('GET', KEYS[2]) or '0'); if n > tonumber(ARGV[2]) then return {3, n} end; return {0, 0} end`.
  `startLua` already deletes `KEYS[2]`, so requesting a new code unlocks.
- Test: `infrastructure/valkey` integration test (existing file for the OTP store).

- [ ] **Step 1: Write the failing test** `TestOTPVerify_LockedStaysLockedUntilNewCode`: max 5; 6 wrong attempts → verdict 3;
  7th attempt (right code) → verdict 3 (not 0); `Start` a new code → verify OK.
- [ ] **Step 2: Run** `$GO test -tags integration ./internal/user/infrastructure/valkey/` → FAIL (7th returns 0).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `fix(user): keep OTP locked until a new code is requested`.

### Task U6: Input hygiene for email and full name (P3-17, P3-18)

**Files:**
- Modify: `domain/valueobject/email.go` — local part must be printable ASCII (`0x21..0x7E`); plus-addressing is still
  accepted (stripping it would merge real distinct mailboxes).
- Modify: `application/usecase/profile.go` — `fullName` rejects any rune with `unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)`
  → `inbound.Validation("fullName", errNameChars)` (`"nama berisi karakter tidak diizinkan"`).
- Tests: `domain/valueobject` email test, `profile_test.go`.

- [ ] **Step 1: Write failing tests:** email table: `"ánu@example.test"`, `"a b@example.test"` → `ErrInvalidEmail`;
  `"a+b@example.test"` → valid. Name: `"Budi‮Santoso"`, `"Budi\x07"` → validation error; `"Budi Santoso"` ok.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `$GO test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(user): reject non-ASCII email local parts and control characters in names`.

### Task U7: Decisions and docs (P2-23, P3-19, P2-20, audit, ai-log)

- [ ] **P2-23** — Owner decision on plaintext `email`/`nik` in `GET /me` (introduced on purpose in `d04cf0d`). Record it as an
  ADR entry in `docs/analisa/05-ADR-RBT.md`, one line in `.kiro/steering/security-privacy.md`, and the `user.yaml` response
  description; plus `docs/evidence/2026-11-12-me-plaintext-pii.md` (steering change ⇒ evidence file, per testing-review.md).
  Check that no body logging exists on `/me` (`httpx.AccessLog` logs no bodies — confirm by reading it).
- [ ] **P3-19** — Won't fix: the `email` claim is used by Queue (`admin_verifier.go:63`) and Booking (`security.go:157`) for
  the admin allow-list. Note this in the audit.
- [ ] **P3-20** — informational, no change.
- [ ] Audit file: add a "Re-check after fixes" table marking each finding of this plan resolved (commit hash) or decided.
- [ ] `docs/ai-log/README.md`: one row for the session.
- [ ] Final gate: `make ci` (or the three `commands.env` commands for booking and user) → PASS; drift SQL on the local stack
  shows zero drift (roadmap AC-6).

---

## Part C — Booking/Queue roadmap (detailed 2026-11-13)

Order = severity, then dependency: phases run C2 → C9, tasks in the order written (exception: in C8 run C8.12 before
C8.5). Items handled by a Part A task are listed in *Coverage* only. AC ids (`AC-Cx.y`) and finding ids stay in each title.

**Runner** (repo root; Global Constraints apply):

```bash
gob() { SVC=booking bash .superpowers/sdd/2026-11-12-audit-fixes/go.sh "$@"; }
goq() { SVC=queue   bash .superpowers/sdd/2026-11-12-audit-fixes/go.sh "$@"; }
```

`B/` = `services/booking/internal/booking/`, `Q/` = `services/queue/internal/queue/`, `lua/` = `services/queue/assets/lua/`.
Integration: `gob test -tags integration ./internal/booking/infrastructure/postgres/` (Postgres 5433),
`goq test -tags integration ./internal/queue/infrastructure/valkey/` (Valkey 6380). Port changes → `make mock` in that service.

**Migrations:** Booking 0013 (C4.1), 0014 (C7.3), 0015 (C7.8); Queue 0006 (C8.10). Each ships `.down.sql`.

**Contracts (owner, 2026-11-13):** no contract change unless the AC itself is one. Every step below that touches
`docs/contracts/` is marked **Owner confirms before execution**; the code part of that task does not depend on it.

**Perf evidence procedure (C7, C8; owner allows RabbitMQ + k6):**
1. `docker compose up -d rabbitmq` (≈60 s), then `bash tests/k6/scripts/stack-local.sh up`.
2. *Before*: on the task's base commit, run the named k6 command from `tests/k6/` and/or the named Go benchmark; copy
   `run.json` commit, the metric and its value into the evidence file's Before table.
3. *After*: implement, rebuild the stack, same command → After table, threshold, PASS/FAIL.
4. Evidence file `docs/evidence/<run-date>-<name>.md` (name given per task); then `git restore tests/k6/reports`
   (run.sh overwrites committed reports; only the evidence file is committed).
5. k6 and benchmarks never gate CI; each perf task also has a correctness test that runs in `go test`.

Each phase's last task adds one row to `docs/ai-log/README.md` in its commit.

### Phase C2 — Booking: lifecycle leftovers

### Task C2.1: Stock transactions retry serialization failures and deadlocks (AC-C2.1 PF-P3-3, AC-C2.2 F6)

Decision **D-C2.2 — Owner confirms before execution:** keep the extend-deadline cap 1–600 s. It matches
`specs/booking-service/requirements.md:386` and `internal-booking.yaml` (`minimum: 1, maximum: 600`) and is already
pinned by `TestExtendDeadline_InvalidSeconds_TooLarge` (601 → validation). No code change.

**Files:** Modify `B/infrastructure/postgres/unit_of_work.go` `retry` (~L1036) — loop condition uses new
`func retryable(err error) bool` = `errors.Is(err, eventstore.ErrConcurrency) || pkgpg.IsCode(err, pkgpg.CodeSerialization)
|| pkgpg.IsCode(err, pkgpg.CodeDeadlock)`; `maxRetries` stays 3. Test: `B/infrastructure/postgres/errors_test.go` (unit).

- [ ] **Step 1: Failing test** `TestRetryable`: table — `eventstore.ErrConcurrency` → true; `&pgconn.PgError{Code: "40001"}` →
  true; `"40P01"` → true; `fmt.Errorf("x: %w", &pgconn.PgError{Code: "40P01"})` → true; `"23505"` → false; `errors.New("db")` → false.
- [ ] **Step 2: Run** `gob test ./internal/booking/infrastructure/postgres/ -run TestRetryable` → FAIL (undefined).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package → PASS (`TestLockOrder_HoldEditExpire` stays green).
- [ ] **Step 5: Commit** `fix(booking): retry stock transactions on serialization failure and deadlock`.

### Phase C3 — Booking: hold and session leftovers

### Task C3.1: Hold, edit and payment lock use the injected clock (AC-C3.1 F-clock)

Decision **D-C3.1:** a zero `Now` falls back to `time.Now().UTC()` (`func clockOr(t time.Time) time.Time`), so internal
callers and old tests keep working; the three usecases always pass `uc.clock.Now()`.

**Files:**
- Modify `B/port/outbound/unit_of_work.go` — add `Now time.Time` to `ReplaceSpec` and `PaymentLockSpec` (`HoldSpec` has it).
- Modify `B/infrastructure/postgres/unit_of_work.go` — `hold` L73/L77, `replace` L295, `paymentLock` L934 read
  `clockOr(spec.Now)` instead of `time.Now().UTC()` (payment_started_at is stamped with it too).
- Modify `B/application/usecase/edit.go:79` and `payment_lock.go` — set `Now: uc.clock.Now()`.
- Tests: `unit_of_work_integration_test.go`, `uow_more_integration_test.go`, `payment_lock_test.go`.

**Interfaces:** Produces `ReplaceSpec.Now`, `PaymentLockSpec.Now time.Time`.

- [ ] **Step 1: Failing tests:**
  - `TestHold_UsesSpecClock`: `spec := f.holdSpec(id)`; `spec.Now = time.Now().Add(10*time.Minute)` (seeded admission
    expires in 5 min, grace 5 s) → `ErrorIs(err, inbound.ErrSessionExpired)`, `f.stock(t)` unchanged.
  - `TestPaymentLock_UsesSpecClock`: hold; `PaymentLock` with `Now = payment_expires_at + 1s` → `Outcome == "PAYMENT_EXPIRED"`,
    `payment_started_at IS NULL`.
  - `TestReplaceItems_UsesSpecClock`: hold (admission EDITABLE); `ReplaceItems` with `Now = admission expires_at + 1 min` →
    `ErrorIs(err, inbound.ErrSessionExpired)`.
  - `TestPaymentLock_PassesClock` (usecase): fixed clock `T` → the uow fake receives `spec.Now == T`.
- [ ] **Step 2: Run** → FAIL (compile / real clock used).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): use the injected clock inside hold, edit and payment lock`.

### Task C3.2: Attendee-check limit is counted atomically (AC-C3.2 F-attendee)

**Files:**
- Modify `B/port/outbound/ports.go` `IAttendeeChecker.RecordCheck(ctx context.Context, admissionID string, nikHash []byte,
  maxChecks int) (AttendeeCheckRecord, error)`; add `type AttendeeCheckRecord struct { Counted, LimitReached bool; Used int }`.
- Modify `B/infrastructure/postgres/repositories.go` `RecordCheck` (~L95) — in the existing tx: dedupe INSERT; repeat NIK →
  `{Counted: false, Used: <current attendee_checks>}`; new NIK → `UPDATE booking.admissions SET attendee_checks =
  attendee_checks + 1 WHERE id=$1::uuid AND attendee_checks < $2 RETURNING attendee_checks`; no row → return a sentinel so
  the tx rolls back the dedupe row, result `{LimitReached: true, Used: maxChecks}`.
- Modify `B/application/usecase/check_attendee.go` — `LimitReached` → `{Result: checkLimit, ChecksRemaining: 0}`;
  `remaining = max(maxChecks - rec.Used, 0)` (no longer from the stale `admission.AttendeeChecks`).
- Update the fake in `fakes_test.go` and `TestTicketQuotaAndAttendeeRepos`.

- [ ] **Step 1: Failing tests:** `TestRecordCheck_ConcurrentNeverExceedsMax` (integration): one admission, `maxChecks = 3`,
  10 goroutines with distinct NIK hashes → exactly 3 `Counted`, 7 `LimitReached`; `admissions.attendee_checks = 3`;
  3 rows in `booking.attendee_checks`. `TestCheckAttendee_LimitFromRepository` (unit): fake returns `LimitReached` →
  `Result == checkLimit`, `ChecksRemaining == 0`, `IsRegistered` not called.
- [ ] **Step 2: Run** → FAIL (compile).
- [ ] **Step 3: Implement**, `make mock`.
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): count attendee checks atomically against the session limit`.

### Phase C4 — Booking: event config, status, audit (migration 0013)

### Task C4.1: Event rules lock when the sales window opens (AC-C4.1 F-lock)

Decision **D-C4.1:** the admin PATCH body keeps not accepting the rule fields (contract unchanged), so
`EVENT_RULES_LOCKED` is reached by unpublish (existing) and by the trigger on any rule write (TC-CFG-02, integration).

**Files:**
- Create `services/booking/deployments/migrations/0013_rules_lock_by_time.up.sql`:
  1. `SELECT set_config('app.actor', 'migration-0013', true);`
  2. Backfill while the old guard still allows it: `UPDATE booking.events SET rules_locked_at = sales_starts_at
     WHERE rules_locked_at IS NULL AND sales_starts_at <= now();`
  3. `CREATE OR REPLACE FUNCTION booking.guard_event_rules()`: `locked := OLD.rules_locked_at IS NOT NULL OR now() >=
     OLD.sales_starts_at`; when `locked` and (`max_tickets_per_order`, `max_tickets_per_buyer_nik` or
     `allow_mixed_categories` changed, or `OLD.rules_locked_at IS NOT NULL AND NEW.rules_locked_at IS DISTINCT FROM
     OLD.rules_locked_at`) → `RAISE EXCEPTION 'EVENT_RULES_LOCKED event=%' … ERRCODE 'check_violation'`; when `locked AND
     OLD.rules_locked_at IS NULL` → `NEW.rules_locked_at := now()`; keep `NEW.updated_at := now()`. No `status` test.
  4. `CREATE OR REPLACE FUNCTION booking.lock_rules_on_insert()`: `IF now() >= NEW.sales_starts_at AND NEW.rules_locked_at IS NULL`.
- Create `0013_rules_lock_by_time.down.sql` — restore both 0001 function bodies; backfilled values stay (comment says so).
- Test: create `B/infrastructure/postgres/migration_0013_integration_test.go` (pattern of `migration_0009_integration_test.go`).

- [ ] **Step 1: Failing tests:**
  - `TestRulesLock_StartsAtSalesStartRegardlessOfStatus`: insert a `DRAFT` event with `sales_starts_at = now() - 1 min` →
    `UPDATE booking.events SET max_tickets_per_order = 2` errors and `mapEventWriteErr(err, "x")` is `inbound.ErrRulesLocked`;
    a `DRAFT` event with `sales_starts_at = now() + 1 h` → same UPDATE succeeds, `rules_locked_at IS NULL`;
    `EventWriteRepo.UpdateConfig` with only `SelectionTTLSeconds` on the started event → `NoError`.
  - `TestMigration0013_BackfillsStartedEvents`: migrate to 12, insert a started `DRAFT` event (`rules_locked_at` NULL),
    migrate to 13 → `rules_locked_at = sales_starts_at`; one `config_audit` row with `changed_by = 'migration-0013'`.
- [ ] **Step 2: Run** → FAIL (UPDATE succeeds / no migration 13).
- [ ] **Step 3: Implement** both files.
- [ ] **Step 4: Run** postgres integration package (+ `migrations/embed_test.go` via `gob test ./...`) → PASS.
- [ ] **Step 5: Commit** `fix(booking): lock event rules when the sales window opens`.

### Task C4.2: SOLD_OUT comes from supply in catalog list, filter and detail (AC-C4.2 F-status)

Decision **D-C4.2:** catalog rule = `held_units + sold_units >= total_capacity` (with `total_capacity > 0`). It is exactly
today's detail rule (Σ category `capacity − held − sold` = `total − held − sold`, kept equal by
`check_supply_matches_categories`), so detail behaviour does not change and list/filter now agree with it. Queue's
`soldOut` (C5.2) is stricter on purpose: it ends the queue.

**Files:**
- Modify `B/infrastructure/postgres/event_repo.go` — const `stockSoldOutSQL = "EXISTS (SELECT 1 FROM booking.event_supply s
  WHERE s.event_id = e.id AND s.total_capacity > 0 AND s.held_units + s.sold_units >= s.total_capacity)"`; appended to
  `eventCols` (scanned last in `scanEvent`, before `minPrice`/extras) into new `entity.Event.StockSoldOut bool`; in
  `eventFilterWhere`, after the `CLOSED` branch: `WHEN <stockSoldOutSQL> THEN 'SOLD_OUT'` (no `$n` inside, so `filterWhereAt` is unaffected).
- Modify `B/domain/mapper/booking_mapper.go` — `eventStatus`: inside the window, `e.StockSoldOut` → `SOLD_OUT`; delete
  `summaryWithStock`, `ToEventDetail` uses `ToEventSummary`.
- Modify `B/infrastructure/postgres/admission_repo.go` `announceSoldOutTx` — drop the `UPDATE booking.events SET status='SOLD_OUT'`
  (the `booking.sold_out` outbox row from B4 stays).
- Tests: `booking_mapper_test.go`, `repos_integration_test.go`, `reserve_release_integration_test.go`.

- [ ] **Step 1: Failing tests:** extend `TestEventStatusFollowsTheSalesWindowAndStock`: `StockSoldOut` in window → `SOLD_OUT`;
  before start → `NOT_STARTED`; stored `CANCELLED` wins. `TestEventRepo_ListAndFilterDeriveSoldOutFromSupply`: event in
  window, `held_units + sold_units = total_capacity` set by SQL → `ListEvents(Status: "SOLD_OUT")` returns it,
  `Status: "AVAILABLE"` does not, `GetDetail` maps to `SOLD_OUT`, `events.status` column unchanged.
  `TestReserveBatch_SoldOutIsAnnouncedOnce` additionally asserts `events.status` unchanged.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): derive SOLD_OUT from supply in the catalog list and filter`.

### Task C4.3: Admin writes record the actor; a cancelled event refuses config and publish (AC-C4.3 F-actor, AC-C4.4 F-cancel admin part)

Decision **D-C4.3:** the actor (token subject = account id; no email, so no PII in `config_audit`) is passed explicitly
through the signatures, not through the context.

**Files:**
- Modify `B/port/outbound/ports.go` — `IEventWriteRepo.UpdateConfig(ctx, eventID, actor string, req dto.UpdateEventConfigRequest) error`;
  `IEventPublicationRepo.SetPublished(ctx, eventID, actor string, published bool) error`, `Cancel(ctx, eventID, actor string) error`.
- Modify `B/port/inbound/usecases.go` — `IUpdateEventConfigUsecase.Update(ctx, eventID, actor string, req)`;
  `IPublishEventUsecase.Publish/Unpublish/Cancel(ctx, eventID, actor string) error`; usecases pass `actor` through.
- Modify `B/infrastructure/http/admin_handler.go` — pass `accountIDFrom(r.Context())` (set by `withUser`).
- Modify `B/infrastructure/postgres/event_write_repo.go` — `UpdateConfig`, `SetPublished`, `Cancel` each run in
  `pkgpg.WithTx`: `SELECT status FROM booking.events WHERE id=$1::uuid FOR UPDATE` (no row / `22P02` → `ErrNotFound`);
  `CANCELLED` → `inbound.ErrEventCancelled` for UpdateConfig and SetPublished (Cancel stays idempotent);
  `SELECT set_config('app.actor', $1, true)`; then the existing UPDATE.
- **Owner confirms before execution:** `docs/contracts/openapi/internal-booking.yaml` — add `409 EVENT_CANCELLED` to
  `adminPublishEvent`, and name it in the 409 descriptions of `adminUpdateEvent` / `adminUnpublishEvent`.
- Tests: `repos_integration_test.go`, `more_handlers_test.go`, usecase tests (signature updates).

- [ ] **Step 1: Failing tests:** `TestEventWriteRepo_AuditRecordsActor`: `UpdateConfig(ctx, id, "acct-1", {SelectionTTLSeconds: 400})`
  → the `config_audit` row for `selection_ttl_seconds` has `changed_by = 'acct-1'`; `SetPublished(…, "acct-2", true)` → row
  with `changed_by = 'acct-2'`. `TestEventWriteRepo_CancelledEventRefusesConfigAndPublish`: `Cancel` then `UpdateConfig` and
  `SetPublished(true)` → `ErrorIs(err, inbound.ErrEventCancelled)`, row unchanged. `TestAdminHandler_PassesTokenSubjectAsActor`:
  fake verifier subject `acct-9` → fake usecase receives `actor == "acct-9"` for PATCH, publish, unpublish, cancel.
- [ ] **Step 2: Run** → FAIL (compile).
- [ ] **Step 3: Implement**, `make mock`.
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package (+ `make contracts` if the yaml step was confirmed) → PASS.
- [ ] **Step 5: Commit** `fix(booking): record the admin actor in config audit and refuse changes to cancelled events`.

### Task C4.4: Reserve only inside the sales window; a cancelled event ends the queue (AC-C4.4 F-window, F-cancel)

Decision **D-C4.4a:** `ReserveBatch` reads the event once per batch, before the entry loop: not found, unpublished, or
`at < sales_starts_at` → every entry `NO_SUPPLY_NOW`; `CANCELLED` or `at >= sales_ends_at` → every entry `SOLD_OUT`
(no `booking.sold_out` row: that event means "nothing can come back"; Queue closes its sale on the `SOLD_OUT` outcome and
C8.4's drain job ends the waiting entries).
Decision **D-C4.4b — Owner confirms before execution (buyer-visible):** hold refuses `now < sales_starts_at`
(`inbound.ErrSessionExpired`, defensive: no admission can exist then); after `sales_ends_at` an admission reserved before
the end may still hold until its own deadline (invariant 2, "Jangan PHP"). The hard-stop alternative is the same check with
`!now.Before(SalesEndsAt)`.

**Files:** Modify `B/infrastructure/postgres/admission_repo.go` `ReserveBatch` (gate before the loop);
`B/application/usecase/hold.go` `plan` (~L276, after `GetDetail`). Tests: `reserve_release_integration_test.go`, `hold_test.go`.

- [ ] **Step 1: Failing tests:** `TestReserveBatch_OutsideSalesWindow`: `sales_starts_at` in the future → `NO_SUPPLY_NOW`,
  `allocated_units = 0`; `sales_ends_at` in the past → `SOLD_OUT`, 0 admissions, 0 outbox rows.
  `TestReserveBatch_CancelledEventIsSoldOut`: cancelled event → `SOLD_OUT`, 0 admissions. `TestHold_BeforeSalesStartIsRefused`
  → `ErrSessionExpired`, `uow.Hold` not called. `TestHold_AfterSalesEndWithLiveAdmissionIsAccepted` (pins D-C4.4b).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `gob test ./...` + postgres integration package → PASS.
- [ ] **Step 5: Commit** `fix(booking): reserve only inside the sales window and end the queue of a cancelled event`.

### Task C4.5: Document the event settings the admin API does not set (AC-C4.5 F-dead-cols, F-admin-fields)

Owner decision 2026-11-13: no column is dropped, no code change. Decision **D-C4.5:** the text goes into
`internal-booking.yaml` (the admin schemas `AdminEventCreateRequest` / `AdminEventConfigUpdate` live there, not in
`booking.yaml`). **Owner confirms before execution** (contract file, description only).

- [ ] **Step 1:** Add one description paragraph listing the fixed defaults from `0001_init.up.sql`: `timezone = Asia/Jakarta`,
  `selection_warning_seconds = 60`, `ticket_categories.currency = IDR`, `claim_window_seconds = 180`,
  `selection_submit_grace_seconds = 5`, `max_attendee_checks_per_session = 10`.
- [ ] **Step 2: Run** `make contracts` → PASS.
- [ ] **Step 3: Commit** `docs(contracts): document the event settings the admin API does not set` (+ ai-log row for C2–C4).

### Phase C5 — Availability & sale state (Booking + Queue + contract)

### Task C5.1: Category availability is capped by free event stock; snapshot skips cancelled events (AC-C5.1 F-avail, AC-C5.3 F-snapshot)

Decision **D-C5.1 — Owner confirms before execution (deviates from the AC wording for the SSE):** public detail shows
`min(category free, event free)` as written. The in-session SSE shows `min(category free, event free +
admission.OutstandingUnits)`: those units are already reserved for this buyer (invariant 2); with the literal formula every
admitted buyer sees 0 in every category as soon as all free stock is allocated, while their own hold would succeed.

**Files:**
- Modify `B/infrastructure/postgres/event_repo.go` `GetDetail` — join `booking.event_supply s`; `available = LEAST(GREATEST(ci.capacity
  - ci.held_quantity - ci.sold_quantity, 0), GREATEST(s.total_capacity - s.allocated_units - s.held_units - s.sold_units, 0))`.
- Modify `OnSaleAvailability` — filter `e.status NOT IN ('CLOSED','CANCELLED') AND e.published_at IS NOT NULL`; return
  per event `outbound.AvailabilitySnapshot{EventFree int; Categories []CategoryAvailabilitySnap}` (category value = category free).
- Modify `B/port/outbound/ports.go` — add `AvailabilitySnapshot`; `IAvailabilitySource.OnSaleAvailability(ctx, now) (map[string]AvailabilitySnapshot, error)`;
  `IAvailabilityCache.GetAvailability(ctx, eventID) (AvailabilitySnapshot, error)`, `SetAvailability(ctx, eventID, AvailabilitySnapshot, ttl) error`.
- Modify `B/infrastructure/valkey/availability_cache.go`, `B/application/usecase/sweepers.go` (`ProjectAvailability`),
  `stream_session.go` — frame `categories[i].available = min(c.Available, snap.EventFree + adm.OutstandingUnits)`;
  the SSE wire shape (`{categories:[{categoryId, available}], serverTime}`) is unchanged.

- [ ] **Step 1: Failing tests:** `TestEventRepo_DetailAvailabilityIsCappedByEventFree`: category capacity 10, event
  `allocated_units = 8` → detail available 2; plus 1 held → 1. Extend `TestEventRepo_OnSaleAvailability_ClampsAndHonoursTheSalesWindow`:
  a cancelled on-sale event is absent; `EventFree` reported. `TestStream_AvailabilityAddsOwnReservedUnits`: snapshot
  `{EventFree: 0, cat 5}`, admission `OutstandingUnits 3` → frame available 3; `OutstandingUnits 0` → 0.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**, `make mock`.
- [ ] **Step 4: Run** `gob test ./...` + postgres and valkey integration packages → PASS.
- [ ] **Step 5: Commit** `fix(booking): cap category availability by free event stock`.

### Task C5.2: Supply snapshot reports free units and real sold/total (AC-C5.2 F-supply)

Owner decision 2026-11-13: no new contract field; `internal-booking.yaml` shape unchanged. Its `Supply` schema already
differs from `dto.SupplySnapshot` (pre-existing drift, left as is). Decision **D-C5.2:** category `sold = sold_quantity`
(paid only), `total = capacity`; `soldOut = CANCELLED || sold_units >= total_capacity` (free + allocated + held = 0);
`remainingTickets = freeUnits` (0 when cancelled). The stored `SOLD_OUT` status is no longer read.

**Files:**
- Modify `B/domain/entity/event.go` — `CategoryDetail` gains `Capacity, Sold int` (filled by `GetDetail`); `Supply.SoldOut()`
  becomes `s.TotalCapacity-s.SoldUnits <= 0`.
- Modify `B/application/usecase/supply.go`.
- **Owner confirms before execution:** `docs/contracts/fixtures/internal-booking-supply.json` — `remainingTickets: 12 → 2`
  (= `freeUnits`; categories stay `sold 0` because the fixture's ledger has `sold = 0`).
- Tests: `supply_test.go`, `B/domain/entity/booking_test.go:75`, `supply_handler_test.go`; Queue
  `Q/infrastructure/bookingclient/client_test.go` (fixture expectation).

- [ ] **Step 1: Failing tests:** `TestGetEventSupply_RemainingIsFreeUnits`: supply total 10, allocated 4, held 2, sold 1 →
  `RemainingTickets 3`, `FreeUnits 3`, `SoldOut false`, categories carry `Sold`/`Total` from the detail.
  `TestGetEventSupply_SoldOutOnlyWhenNothingCanReturn`: free 0 + allocated 2 → `SoldOut false`; sold = total → true;
  `CANCELLED` → true, remaining 0. Update `TestGetEventSupply_SoldOut`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `gob test ./...`, `goq test ./internal/queue/infrastructure/bookingclient/` → PASS.
- [ ] **Step 5: Commit** `fix(booking): report free units and real per-category sold/total in the supply snapshot`.

### Task C5.3: Queue reopens the sale when stock returns; LIMITED uses real numbers (AC-C5.4 F-limited, AC-C5.5 P1-12)

Decision **D-C5.3:** reopen (`SetSale(ctx, eventID, dto.SaleOpen, false)`) only when `sale.SoldOut && !supply.SoldOut &&
supply.RemainingTickets > 0 && SalesStartsAt <= now < SalesEndsAt`, so an ended or cancelled sale never reopens (Booking
reports cancelled as `soldOut`). `categoriesFrom` already derives LIMITED from `Sold`/`Total`; C5.2 makes the inputs real.

**Files:** Modify `Q/infrastructure/worker/status_publisher.go` `runEvent` (after the Supply read; the doc uses the
reopened state). Tests: `Q/infrastructure/worker/status_zones_test.go` (fake store in `fakestore_test.go`).

- [ ] **Step 1: Failing tests:** `TestStatusPublisher_ReopensSaleWhenStockReturns`: sale `{CLOSED, soldOut true}`, supply
  `{SoldOut false, RemainingTickets 3, window open}` → one `SetSale(OPEN, false)`, doc `SoldOut false`; same with the window
  ended → no `SetSale`. `TestCategoriesFrom_LimitedWithRealSoldTotal`: pct 5 — `{Sold 96, Total 100}` → LIMITED,
  `{90, 100}` → AVAILABLE, `{100, 100}` → SOLD_OUT.
- [ ] **Step 2: Run** `goq test ./internal/queue/infrastructure/worker/ -run 'Reopens|LimitedWithReal'` → FAIL (first).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): reopen the sale when Booking reports free stock again`.

### Task C5.4: SOLD_OUT is terminal in every script and releases its allocation (AC-C5.6 P1-11, AC-C5.7 P2-17)

**Files:**
- Modify `Q/domain/valueobject/release.go` `ShouldRelease` — add `"SOLD_OUT"` (STALE stays non-terminal).
- Modify `lua/heartbeat.lua:16`, `lua/leave.lua:11`, `lua/join.lua:48` (`active`) and `:103` (4b guard) — add `SOLD_OUT` to the
  terminal lists; `lua/claim.lua` — `if st == 'SOLD_OUT' then return {'ERR','SOLD_OUT'} end` before the NOT_FOUND fallthrough.
- Modify `Q/infrastructure/valkey/queue_store.go` `Claim` — `"SOLD_OUT"` → `entity.ErrSoldOut` (410 `EVENT_SOLD_OUT`, already mapped).
- Tests: `release` unit test, `Q/infrastructure/valkey/leave_claim_integration_test.go`.

- [ ] **Step 1: Failing tests:** `TestShouldRelease` adds `SOLD_OUT → true`, `STALE → false`. `TestSoldOutEntryIsTerminal`:
  join, `EndWaitingSoldOut` → `Heartbeat` tag `NOT_FOUND`; `Leave` → IGNORED, status stays `SOLD_OUT`; `Claim` →
  `ErrorIs(err, entity.ErrSoldOut)`; `Join` with the same token → `CREATED` (new entry id).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `goq test ./...` + valkey integration package → PASS.
- [ ] **Step 5: Commit** `fix(queue): treat SOLD_OUT as terminal in heartbeat, leave, claim and join` (+ ai-log row for C5).

### Phase C6 — Queue: correctness & security

### Task C6.1: Visitor tokens stay valid across events (AC-C6.1 P0-8)

Decision **D-C6.1:** the generation becomes a monotonic ratchet and every key stays in the event slot (cluster-safe):
rotation sets `vtok = max(stored+1, nowMs)`; a token gen **greater** than the stored one is accepted and ratchets the stored
gen up (`SET … KEEPTTL`); a smaller gen is `INVALID`; absent record = never joined. Residual (unchanged from today): after a
REATTACH rotation in event A, the old token keeps working in event B until the new token is first used there.

**Files:**
- Modify `lua/join.lua` — normalisation (L62-69): `tg < serverGen → v = ''`, `tg > serverGen → SET vtok tg KEEPTTL`;
  `rotate(v)`: `g = math.max(gen(v) + 1, tonumber(now))`, `SET vtok string.format('%d', g) EX tokenTTL`.
- Modify `lua/heartbeat.lua:9` — numeric compare + the same ratchet.
- Create `lua/token_gen.lua` (KEYS[1] = vtok key, ARGV[1] = gen) → `1` OK (ratchets when higher), `0` INVALID, `-1` ABSENT;
  register in `lua/embed.go`.
- Modify `Q/port/outbound/queue_store.go` — replace `TokenGen` with `CheckTokenGen(ctx context.Context, eventID, visitorID string,
  gen int64) (TokenGenCheck, error)`; `type TokenGenCheck int` with `TokenGenOK`, `TokenGenInvalid`, `TokenGenAbsent`.
- Modify `Q/infrastructure/valkey/queue_store.go`, `Q/application/usecase/my_entry.go` `checkTokenGen` (Absent → `ErrNotFound`,
  Invalid → `ErrVisitorTokenInvalid`; claim and leave use it already).
- Tests: `Q/infrastructure/valkey/visitor_token_integration_test.go`, `my_entry` unit test.

**Interfaces:** Produces `IQueueStore.CheckTokenGen` (used again by C8.9).

- [ ] **Step 1: Failing tests:**
  - `TestTokenGen_MultiEventRejoinKeepsOtherEventValid`: join A (no token) → g1; join B with token g1; `EndEntry(B, PAID)`;
    rejoin B with g1 → CREATED, `g2 > g1`; `Heartbeat(A, g2)` → OK; `CheckTokenGen(A, g2)` → OK; then `CheckTokenGen(A, g1)` → Invalid.
  - `TestTokenGen_ReattachRotatesAndRatchets`: account REATTACH in A → g3; `CheckTokenGen(A, g2)` → Invalid;
    `CheckTokenGen(B, g3)` → OK, then `CheckTokenGen(B, g2)` → Invalid.
  - `TestGetMyEntry_TokenGenOutcomes` (unit): Absent → `ErrNotFound`; Invalid → `ErrVisitorTokenInvalid`.
- [ ] **Step 2: Run** → FAIL (A's calls 401 after B rotates).
- [ ] **Step 3: Implement**, `make mock`.
- [ ] **Step 4: Run** `goq test ./...` + valkey integration package → PASS.
- [ ] **Step 5: Commit** `fix(queue): keep visitor tokens valid across events with a monotonic generation`.

### Task C6.2: Guest idempotency keys are scoped to the device or client IP (AC-C6.2 P1-13)

Decision **D-C6.2:** guest scope = device hash, else a keyed hash of the client IP (C6.3's IP); never the raw IP. Without
either, a guest's Idempotency-Key is ignored (no replay). Residual: two guests behind one IP, both without `X-Device-Id`,
sending the same random 16–64-char key — needs a leaked key.

**Files:**
- Modify `Q/infrastructure/security/visitor_auth.go` — `func (a *VisitorAuthenticator) GuestScope(r *http.Request, clientIP string) string`
  = `DeviceHash(r)` if non-empty, else `"ip-" + hex(HMAC(key, "queue.ip:"+clientIP))[:32]`, `""` without a hasher; add it to
  the `VisitorTokenAuthenticator` interface in `Q/infrastructure/http/helpers.go`.
- Modify `inbound.JoinInput` + `GuestScope string`; `outbound.JoinParams` + `GuestScope string`; `join_handler.go` sets it;
  `join.go` passes it; `queue_store.go` `Join` — guest scope `"n:" + GuestScope`, empty → `idem = ""`.

- [ ] **Step 1: Failing tests:** `TestJoin_GuestIdempotencyIsScoped` (valkey): guest `{IdemKey K, GuestScope "d1"}` CREATED;
  `{K, "d2"}` → CREATED, different entry and visitor; `{K, "d1"}` again → EXISTING, same entry; `{K, ""}` → CREATED (no replay).
  `TestJoinHandler_GuestScopeFromDeviceOrIP`: with `X-Device-Id` → scope = device hash; without → prefix `ip-`, never
  contains the raw IP.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `goq test ./...` + valkey integration package → PASS.
- [ ] **Step 5: Commit** `fix(queue): scope guest idempotency keys to the device or client IP`.

### Task C6.3: Client IP from the trusted hop count; rate limits validated at start (AC-C6.3 P1-2 Queue, AC-C6.7 P2-18)

Decision **D-C6.3:** `APP_TRUSTED_PROXY_HOPS` default 1 (= today's rightmost entry), range 1–5 like User; the production
value (2 for CloudFront → ALB) and the ALB SG are deployment changes → *Coordination*.

**Files:**
- Modify `services/queue/internal/config/config.go` (`App.TrustedProxyHops int`, validated 1–5) and `load.go`
  (`r.Int("APP_TRUSTED_PROXY_HOPS", 1)`).
- Modify `services/queue/internal/config/queue.go` `RateLimit.Validate` — `JoinPerIP`, `MePerVisitor`, `HeartbeatPerVisitor`
  must be ≥ 1 (`"RATE_LIMIT_JOIN_PER_IP %d harus > 0"` etc.).
- Modify `Q/infrastructure/http/join_handler.go` — `NewJoinHandler(uc, trustProxy bool, hops int)`, `httpx.ClientIPHops(r, h.trustProxy, h.hops)`;
  `services/queue/internal/delivery/queue_module.go:135` passes `cfg.App.TrustedProxyHops`.

- [ ] **Step 1: Failing tests:** `TestLoad_RateLimitsMustBePositive` (each of the three = 0 → error naming the variable);
  `TestLoad_TrustedProxyHops` (default 1; 6 → error); `TestJoinHandler_ClientIPUsesHops`: XFF `1.1.1.1, 2.2.2.2`, hops 2 →
  usecase `ClientIP == "1.1.1.1"`; hops 1 → `"2.2.2.2"`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): derive the client IP from the trusted hop count and validate rate limits at start`.

### Task C6.4: `oneActivePerVisitor=false` is rejected (AC-C6.4 P2-14)

Decision **D-C6.4:** reject rather than guard with `vlast`: `false` has no supported use. A stored row with `false` makes the
next PUT on that event fail validation until it sends `true`.

**Files:** Modify `Q/domain/entity/settings_rules.go` `ValidateSettings` — `!s.OneActivePerVisitor` →
`ValidationError{Field: "oneActivePerVisitor", Message: "harus true"}`. Tests: `settings_rules_test.go`, `queue_settings_test.go`.

- [ ] **Step 1: Failing tests:** `TestValidateSettings_OneActivePerVisitorMustBeTrue`; PUT with `oneActivePerVisitor:false`
  → validation error (usecase test). Update existing cases that set it to false.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): reject oneActivePerVisitor=false`.

### Task C6.5: A CLAIMED slot has an upper bound (AC-C6.5 P2-15)

Decision **D-C6.5 (corrected during execution, Ruling R17):** `QUEUE_CLAIMED_SLOT_MAX_SECONDS` default **7200**
(Booking maxima: selection 1800 + submit grace 30 + payment 3600 + B3 extension 600 + VERIFY_GRACE 600 ≈ 6630, + margin;
the original 5700 ignored the extension and the verify grace), validated ≥ 600: no event config can lose a live buyer's slot. A
CLAIMED entry past its bound ends as `ENDED` / `endReason = CLAIM_TIMEOUT`; Booking is not called.

**Files:**
- Modify `lua/claim.lua` — ARGV[4] = claimMaxMs; `HSET … 'claimExpiresMs', now+max`; `ZADD active XX now+max` (was `inf`).
- Modify `lua/end_entry.lua` SWEEP_MISSED/CLAIMED branch (L46-48) — `claimExpiresMs` present and `now >= it` → end path
  (`ENDED`, `CLAIM_TIMEOUT`, same cleanup as class A); absent (legacy) → keep `ZADD XX inf`, IGNORED; else IGNORED.
- Modify `IQueueStore.Claim(ctx, eventID, entryID, visitorID string, nowMs, claimMaxMs int64)`; `ClaimTurn` gets the value via
  `NewClaimTurn(…, claimMax time.Duration)`; config `WorkerTuning.ClaimedSlotMax` in `queue.go`/`load.go`.

- [ ] **Step 1: Failing test** `TestEndEntry_ClaimedPastBoundIsEnded` (valkey, `end_entry_integration_test.go`): claim at t0 with
  max 1000 ms → `ExpiredActive(t0+999)` excludes it, `ExpiredActive(t0+1000)` includes it; `EndEntry(SWEEP_MISSED, t0+1000)` →
  OK `ENDED`, active ZCARD 0; a later `EndEntry(PAID)` → IGNORED. Config test: 599 → error.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**, `make mock`. **Step 4: Run** `goq test ./...` + valkey integration → PASS.
- [ ] **Step 5: Commit** `fix(queue): bound the lifetime of a CLAIMED slot`.

### Task C6.6: A reservation that lapsed before admission is granted again (AC-C6.6 P2-16)

Decision **D-C6.6:** reconcile in Booking. An `EXPIRED` admission with `end_reason = 'UNCLAIMED'` was never activated
(only RESERVED rows expire as UNCLAIMED), so Queue never admitted that entry (promote failed: lease lost or error). Booking
grants it again instead of `ALREADY_ACTIVE` (which made the promoter end a live buyer). `RELEASED` rows (Queue said the
entry is gone) and activated rows are never re-granted. Queue already ignores `journey.ended(UNCLAIMED)` for a WAITING
entry (`end_entry.lua` class C); a test pins it.

**Files:** Modify `B/infrastructure/postgres/admission_repo.go` `reserveOne` replay branch (~L113) — also select
`end_reason`; `EXPIRED` + `UNCLAIMED` → run the normal supply checks, then `UPDATE booking.admissions SET status='RESERVED',
units=$2, outstanding_units=$2, reserved_at=$3, expires_at=$3 + claim window, ended_at=NULL, end_reason=NULL WHERE id=$1::uuid
AND status='EXPIRED'` instead of the INSERT (CHECK `(status IN active) = (ended_at IS NULL)` holds). Tests:
`reserve_release_integration_test.go`; Queue `end_entry_integration_test.go`.

- [ ] **Step 1: Failing tests:** `TestReserveBatch_LapsedUnclaimedReservationIsGrantedAgain`: reserve E → GRANTED;
  `ExpireSession` → EXPIRED/UNCLAIMED; reserve E again → GRANTED, same allocation id, status RESERVED, `ended_at` NULL,
  `allocated_units` back to the granted units. `TestReserveBatch_ReleasedReservationIsNotGrantedAgain`: `Release` →
  `ALREADY_ACTIVE`. Queue `TestEndEntry_UnclaimedForWaitingIsIgnored`: WAITING entry → IGNORED, still in `waiting`.
- [ ] **Step 2: Run** → FAIL (first). **Step 3: Implement.** **Step 4: Run** both services' unit + integration packages → PASS.
- [ ] **Step 5: Commit** `fix(booking): grant again a reservation that lapsed before Queue admitted the entry`.

### Task C6.7: Claim is rate limited; a missing `eventId` is 400 (AC-C6.8 P3-11)

Decision **D-C6.7:** add the limiter (contract keeps its 429), reusing the `me` budget (`RATE_LIMIT_ME_PER_VISITOR` /
`_WINDOW`, kind `"claim"`): claim is called once per turn, so no new env is needed.

**Files:** Modify `Q/application/usecase/claim.go` — `func (u *ClaimTurn) WithLimiter(l outbound.IRateLimiter, limit int,
window time.Duration) *ClaimTurn` (uses `visitorLimit`, checked after `checkTokenGen`); wire in `queue_module.go`.
Modify `claim_handler.go`, `leave_handler.go`, `my_entry_handler.go` — empty or non-UUID `eventId` → `shared.ValidationErrors{{Field:
"eventId", …}}` (400) before the usecase.

- [ ] **Step 1: Failing tests:** `TestClaim_RateLimited` (limiter false → 429 `RATE_LIMITED`, `store.Claim` not called);
  `TestVisitorRoutes_MissingEventIDIs400` (claim, leave, me → 400, usecase not called).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): rate limit claim and answer 400 for a missing eventId`.

### Task C6.8: Heartbeat resolves its shard inside the script (AC-C6.9 P3-12)

**Files:** Modify `lua/heartbeat.lua` — read `HGET ek 'shard'` and build `P..'hb:'..sh` / `P..'hbstale:'..sh` (as
`liveness_tick.lua` does); only an entry without a `shard` field (legacy) uses ARGV[5]/[6]. Modify `QueueStore.Heartbeat`
(`queue_store.go:580`) — a `GET vlast` error other than nil-reply is returned (`fmt.Errorf("valkey: get vlast: %w", err)`),
not ignored.

- [ ] **Step 1: Failing tests** (valkey): `TestHeartbeat_UsesStoredShard`: entry joined by a store with 4 shards,
  heartbeat through a store with 8 shards (shard count changed on redeploy) → the stored shard's `hb:<s>` gets the new score,
  no other `hb:` key gains the id. `TestHeartbeat_VlastReadErrorIsReturned` (closed client → error, not OK).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** valkey integration package → PASS.
- [ ] **Step 5: Commit** `fix(queue): resolve the heartbeat shard inside the script`.

### Task C6.9: Join re-reads the clock after the Booking call (AC-C6.10 P3-14)

**Files:** Modify `Q/application/usecase/join.go` `tryDirectAdmission` (~L280) — `now = u.d.Clock.Now()` right after
`ReserveBatch`; deadline fallback, `Promote` nowMs and the ADMITTED audit use it; `JoinedAtMs` keeps the first reading.

- [ ] **Step 1: Failing test** `TestJoin_DirectAdmissionUsesClockAfterBooking`: test clock returns T0 until the Booking stub's
  `ReserveBatch` moves it to T0+3s → `Promote` receives `nowMs == (T0+3s).UnixMilli()`; claim deadline fallback = T0+3s+ClaimWindow.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): re-read the clock after the Booking call in direct admission`.

### Task C6.10: The status document is published even when ranks fail (AC-C6.11 P3-15)

**Files:** Modify `Q/infrastructure/worker/status_publisher.go` `runEvent` — on a `ranks` error keep the previous document's
`HeadSeq`/`GuaranteedUntilSeq`/`PossibleUntilSeq` (extract the existing `!haveSupply` copy into `keepPrevious(ctx, eventID, *doc)`),
still `SetStatusDoc`, and return the ranks error after publishing.

- [ ] **Step 1: Failing test** `TestStatusPublisher_RanksErrorStillPublishes`: fake `WaitingCount` error, previous doc with
  boundaries → `SetStatusDoc` called once with those boundaries; `Run` returns the error.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Commit** `fix(queue): publish a degraded status document when ranks fail`.

### Task C6.11: Settings PUT drops the settings cache after commit (AC-C6.12 F-qcache, F-qlock)

Decision **D-C6.11 (F-qlock) — Owner confirms before execution:** the sale-open lock is not lifted after `salesEndsAt`:
TC-CFG-07 requires it to stay locked, and after C6.4 the only locked field that can still change is `oneActivePerEmail`,
which nothing reads (C6.13). Lifting it changes nothing observable.

**Files:** Add `outbound.ISettingsCacheInvalidator { Invalidate(ctx context.Context, eventID string) error }`;
`Q/infrastructure/valkey/event_settings_cache.go` `Invalidate` (DEL `q:{e:<id>}:settings`); `set_queue_settings.go`
`WithCache(inv)` — called after a successful `Apply`; an error is logged (`slog.Warn`) and the PUT still succeeds (TTL 30 s
bounds staleness); wire in `queue_module.go`.

- [ ] **Step 1: Failing tests:** `TestEventSettingsCache_InvalidateDropsKey` (valkey); `TestSetQueueSettings_InvalidatesCacheAfterApply`
  (called once after Apply; not called when Apply fails).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**, `make mock`. **Step 4: Run** `goq test ./...` + valkey integration → PASS.
- [ ] **Step 5: Commit** `fix(queue): drop the cached settings after a settings change` (+ ai-log row for C6).

**AC-C6.13 (F-qdead) — owner decision 2026-11-13 (final):** `statusPublishIntervalMs` and `oneActivePerEmail` stay exactly
as they are in code, `queue.yaml` and the DB (no wiring, no removal, no migration). The only change considered was setting
`preTurnNotifyPosition = 0` when `preTurnNotifyEnabled = false`: **not done** — `doc.PreTurnNotifyPosition` is also the
near-zone size of the heartbeat cadence (`my_entry.go:36`, `heartbeat.go:60` via `valueobject.NextHeartbeatMs`), so zeroing
it would drop near-front clients to the slow base heartbeat. Separating the two needs either a new status-document field
(contract change) or a settings read on every heartbeat/`me` (hot path, PF-P0-3); owner decision.

### Phase C7 — Booking: scale

Owner decision 2026-11-13: RabbitMQ may be started locally and k6 run for before/after evidence (procedure above).

### Task C7.1: Less work while the supply row is locked in hold, settle and expire (AC-C7.1 PF-P0-4)

Decision **D-C7.1:** the lock order stays admission → booking → supply → quota → category (Global Constraints, §9.0): the
supply lock stays where it is, the statements after it shrink. Holding supply last would need every transaction re-ordered
and the owner's OK. Item ids for a hold are generated in Go (`HoldLine.ItemID`, set by the usecase with `ids.NewID()`), so a
multi-row INSERT needs no ordered RETURNING.

**Files:** Modify `B/infrastructure/postgres/unit_of_work.go`, `B/port/outbound/unit_of_work.go` (`HoldLine.ItemID string`),
`B/application/usecase/hold.go`.
- **hold:** event status read joins the admission lock (`… JOIN booking.events e … FOR UPDATE OF a`, before supply). After
  the supply lock: quota upsert+lock in one `INSERT … ON CONFLICT (event_id, buyer_nik_hash) DO UPDATE SET updated_at =
  booking.buyer_quotas.updated_at RETURNING held_quantity, paid_quantity, max_quantity`; category lock (unchanged); event
  append(s) before the `bookings` INSERT so it is written with its final version (no follow-up UPDATE); items in one
  `INSERT … SELECT … FROM unnest(…)`; counters in one statement (data-modifying CTEs over `category_inventories` via
  `unnest($cats::uuid[], $n::int[])` and `buyer_quotas`, outer `UPDATE booking.event_supply … RETURNING allocated_units,
  held_units`); admission UPDATE. CHECK names are unchanged, so `mapHoldErr` keeps working.
- **settle / expire:** `heldByCategory`, item ids and the event name/venue read move before the supply lock; after it:
  append, `bookings` UPDATE, items `UPDATE … RETURNING id`, tickets one `INSERT … SELECT unnest($ids, $codes) ON CONFLICT
  DO NOTHING`, one counters CTE whose `RETURNING` feeds the sold-out check (no extra SELECT), outbox rows in one multi-row
  INSERT (`enqueueOutboxMany`); `releaseHeldItems` uses the same counters CTE.

- [ ] **Step 1: Correctness test (CI)** `TestUnitOfWork_StatementsAfterSupplyLock` (integration): pool with a `pgx.QueryTracer`
  counting statements from the supply `FOR UPDATE` to COMMIT → hold ≤ 8, settle (PAID) ≤ 9, expire ≤ 8 (record the
  baseline counts in the evidence file). Must stay green: `unit_of_work_integration_test.go`, `uow_more_integration_test.go`,
  `hold_accounting_integration_test.go`, `TestSupplyLedger_Property`, `TestLockOrder_HoldEditExpire`.
- [ ] **Step 2: Benchmark** `BenchmarkHold_OneEvent` (new `bench_integration_test.go`, tag `integration`): one event, 16
  parallel workers, each op = one hold of a fresh admission. All holds serialise on the supply row, so ns/op ≈ supply-lock
  hold time. Run `gob test -tags integration -run '^$' -bench BenchmarkHold_OneEvent -benchtime 2000x ./internal/booking/infrastructure/postgres/`
  → Before; `npm run test:load` (traffic.js, scenario `buy`) → Before: `http_req_duration{name:booking.hold}` p95 and
  max lock-wait sessions from `infra.csv`.
- [ ] **Step 3: Run** Step 1 → FAIL (counts above the limits). **Step 4: Implement.**
- [ ] **Step 5: Run** `gob test ./...` + postgres integration package → PASS; repeat Step 2 → After.
  **Threshold:** ≤ 3 ms/op (AC; audit baseline 8–12 ms, ceiling 80–120 holds/s per event); hold p95 not worse than Before.
  Evidence `docs/evidence/<run-date>-perf-c7-1-supply-lock.md`.
- [ ] **Step 6: Commit** `perf(booking): shorten the supply-locked part of hold, settle and expire`.

### Task C7.2: ReserveBatch locks supply once per batch (AC-C7.2 PF-P1-1)

Decision **D-C7.2:** cap 100 entries per batch (`inbound.Validation("entries", "maksimal 100 entri")`, 400). Queue's
`QUEUE_PROMOTE_BATCH_MAX` default becomes 100, validated 1–100, in the same task (otherwise the promoter gets 400). An entry
that still carries a buyer NIK hash (legacy field; Queue stopped sending it with ADR-030) keeps the per-entry path under the
same lock. A unique violation in the bulk INSERT (concurrent batch for the same entry) fails the batch; Queue retries next
tick and the replay path answers it.

**Files:** Modify `B/application/usecase/allocate_batch.go` (cap); `B/infrastructure/postgres/admission_repo.go` `ReserveBatch`:
gate (C4.4) → one replay lookup `… WHERE queue_entry_id = ANY($1::uuid[])` (before the lock) → `SELECT … FROM
booking.event_supply … FOR UPDATE` once → grants computed in Go in entry order (C6.6 re-grants included) → one aggregate
`UPDATE booking.event_supply SET allocated_units = allocated_units + $2 …` → one `INSERT INTO booking.admissions … SELECT …
FROM unnest(…) CROSS JOIN booking.events ev WHERE ev.id = $1 RETURNING queue_entry_id, id, expires_at`; sold-out announced
once (B4). Modify `services/queue/internal/config/load.go:101` (default 100) and `queue.go` `WorkerTuning.Validate`.
**Owner confirms before execution:** `internal-booking.yaml` `AllocationBatchRequest.entries.maxItems: 100` + a `400` response.

- [ ] **Step 1: Correctness tests (CI):** `TestReserveBatch_HundredEntriesOneSupplyUpdate` (integration, tracer): 100
  anonymous entries, free 150 → first 50 GRANTED with 3 units, rest `NO_SUPPLY_NOW`, `allocated_units = 150`, exactly one
  `UPDATE booking.event_supply` and one INSERT into admissions. `TestReserveBatch_ConcurrentWithHoldsKeepsLedger`: 10 holds
  in parallel with a 100-entry batch on one event → no `40P01`, `allocated_units = Σ outstanding_units`, `held_units` =
  HELD items. `TestAllocateBatch_MoreThan100Is400` (usecase).
  `TestLoad_PromoteBatchMaxCappedAt100` (Queue config). Existing ReserveBatch / Activate / Release suites stay green.
- [ ] **Step 2: Before:** `BenchmarkReserveBatch_100` (one 100-entry batch per op) and
  `PROFILE=A VUS=2000 npm run test:waiting` (`http_req_duration{name:queue.join}` p95, lock waits in `infra.csv`).
- [ ] **Step 3: Run** Step 1 → FAIL. **Step 4: Implement.**
- [ ] **Step 5: Run** `gob test ./...`, `goq test ./...` + Booking postgres integration → PASS; After.
  **Threshold:** ≤ 50 ms per 100-entry batch (audit: 6–8 RTT per entry, 500 entries ≈ 1.5–3 s of supply lock).
  Evidence `docs/evidence/<run-date>-perf-c7-2-reserve-batch.md`.
- [ ] **Step 6: Commit** `perf(booking): reserve a batch under one supply lock with bulk statements`.

### Task C7.3: Sweepers claim rows with a lease, run in their own loops and drain while full (AC-C7.3 PF-P1-2, PF-P2-8)

Decision **D-C7.3:** same lease pattern as D-B6. New nullable column `sweep_lease_until` on `admissions` and `bookings`
(scheduling state, no event). Claims: `UPDATE … SET sweep_lease_until = … WHERE id IN (SELECT id … ORDER BY … LIMIT $2
FOR UPDATE SKIP LOCKED) RETURNING …`, lease 15 s. Session predicate is sargable `expires_at < $1` (index
`idx_admissions_due`); grace is re-checked under the row lock in `ExpireSession` (in grace → no-op), and an in-grace row's
lease is set to its grace end (`CASE WHEN expires_at + grace > $1 THEN expires_at + grace ELSE $1 + lease END`), so it
returns exactly when due. Booking predicate stays D-B5 plus the lease condition.

**Files:**
- Create `services/booking/deployments/migrations/0014_sweep_lease.up.sql` / `.down.sql` (ADD / DROP COLUMN on both tables).
- Modify `B/port/outbound/ports.go` — `IAdmissionRepo.ClaimDueSessions(ctx, now time.Time, limit int) ([]entity.Admission, error)`
  replaces `DueSessions`; `IBookingDueScanner.ClaimDueBookings(ctx, now time.Time, limit int) ([]string, error)` replaces `DueBookingIDs`.
- Modify `admission_repo.go`, `booking_repo.go`; `B/application/usecase/sweepers.go` — repeat while `len(batch) == sweepBatch`,
  at most `maxSweepRounds = 20` per call.
- Modify `services/booking/internal/delivery/worker.go` — each job on its own goroutine and ticker (same shape as
  `services/queue/internal/delivery/worker.go` `runJobs`/`runJob`), replacing the serial `loop`.

- [ ] **Step 1: Correctness tests (CI):** `TestClaimDueSessions_TwoSweepersNeverShareRows` (20 due; two concurrent claims of
  20 → union 20, intersection 0); `TestClaimDueSessions_InGraceComesBackAtGraceEnd` (ACTIVE, `expires_at = now-1s`, grace 5 s →
  claimed, `ExpireSession` no-op, claim at `now` → empty, at `now+5s` → returned); `TestClaimDueBookings_LeaseHidesClaimedRows`;
  `TestSweepSessions_RepeatsWhileBatchFull` (fake returns 100, 100, 3 → 3 claims, 203 expirations);
  `TestRunJobs_SlowJobDoesNotDelayOthers`.
- [ ] **Step 2: Before:** `npm run test:spike`; 30 s after it ends:
  `SELECT count(*) FROM booking.admissions WHERE status IN ('RESERVED','ACTIVE','EDITABLE') AND expires_at < now() - interval '60 seconds'`.
- [ ] **Step 3: Run** Step 1 → FAIL. **Step 4: Implement**, `make mock`.
- [ ] **Step 5: Run** `gob test ./...` + postgres integration package → PASS; After.
  **Threshold:** the backlog query returns 0 (audit: ~20 rows/s ceiling). Evidence `docs/evidence/<run-date>-perf-c7-3-sweepers.md`.
- [ ] **Step 6: Commit** `perf(booking): claim due rows with a lease and run each worker job on its own loop`.

### Task C7.4: Outbox relay drains while batches are full (AC-C7.4 PF-P0-5 Booking part)

**Files:** Modify `B/infrastructure/rabbitmq/consumer.go` `RunOnce` — loop the existing body (as `runBatch(ctx) (int, error)`)
while it claimed `r.batch` rows, at most `maxBatchesPerRun = 50`, stop on ctx done. It already runs as its own job after C7.3.
Faster confirms need `pkg/rabbitmq` (PF-P0-7) → *Coordination*.

- [ ] **Step 1: Correctness test (CI)** `TestOutboxRelay_DrainsWhileBatchFull` (unit, fake store): 100, 100, 7 rows → 3 claims,
  207 publishes; endless full batches → stops after 50.
- [ ] **Step 2: Before:** `npm run test:spike`, sampling `SELECT count(*) FROM booking.outbox_events WHERE published_at IS NULL`
  every 5 s; `BenchmarkOutboxRelay_5000Rows` (`outbox_relay_integration_test.go`, RabbitMQ up) → rows/s.
- [ ] **Step 3: Run** Step 1 → FAIL. **Step 4: Implement.** **Step 5: Run** rabbitmq unit + integration tests → PASS; After.
  **Threshold:** pending back to 0 within 10 s after the spike; ≥ 200 rows/s (audit ceiling ~20 events/s).
  Evidence `docs/evidence/<run-date>-perf-c7-4-outbox-relay.md`.
- [ ] **Step 6: Commit** `perf(booking): drain the outbox while claimed batches are full`.

### Task C7.5: SSE streams outlive the write timeout and end cleanly; connection count per instance (AC-C7.5 PF-P1-3, PF-P2-6)

Decision **D-C7.5:** the cap counts per process (`atomic.Int64` on `StreamSession`; one API process per container), so no
Valkey key and no TTL drift. `IncrConn`/`DecrConn` are removed from `IAvailabilityCache` and the valkey adapter.

**Files:** Modify `B/infrastructure/http/sse_handler.go` — `stdhttp.NewResponseController(w).SetWriteDeadline(time.Time{})`
before `WriteHeader`; `B/application/usecase/stream_session.go` — frame writes return errors, the loop returns on a write
error or when `adm.PastGrace(now)` (after one last timer frame); `conns atomic.Int64` with `Add(1)` / deferred `Add(-1)`;
`instance` field removed; `ports.go`, `valkey/availability_cache.go` (+ its integration test).

- [ ] **Step 1: Correctness tests (CI):** `TestStream_ExitsOnWriteError` (writer fails on the first write → returns within one
  tick, counter back to 0); `TestStream_ExitsAfterSessionExpiry`; `TestStream_ConnectionCapPerInstance` (maxConn 1 → second
  concurrent stream `ErrDependency`; counter 0 after both end); `TestSSEHandler_ClearsWriteDeadline` (httptest server with
  `WriteTimeout` 1 s, heartbeat 200 ms → frames still arrive after 2 s).
- [ ] **Step 2: Before:** k6 has no SSE client, so: `curl -N` one stream for 90 s against the local stack (records when it is
  cut) and `go_goroutines` from Booking `/metrics` before and 60 s after 50 parallel `curl -N` streams are killed.
- [ ] **Step 3: Run** Step 1 → FAIL. **Step 4: Implement**, `make mock`. **Step 5: Run** `gob test ./...` → PASS; After.
  **Threshold:** stream not cut at 30 s (audit: `WriteTimeout` kills it); `go_goroutines` back to its baseline ± 5.
  Evidence `docs/evidence/<run-date>-perf-c7-5-sse.md`.
- [ ] **Step 6: Commit** `fix(booking): keep SSE streams past the write timeout and count connections per instance`.

### Task C7.6: Funnel metrics are cached and limited to events on sale (AC-C7.6 PF-P2-7)

Decision **D-C7.6:** cache 30 s (lower end of the AC's 30–60 s; dashboards lag ≤ 30 s); no new index — one scan per 30 s
per instance is acceptable.

**Files:** Modify `B/infrastructure/telemetry/funnel_collector.go` — `NewFunnelCollector(stats outbound.IFunnelStats, now func() time.Time)`,
results reused for 30 s (mutex); `B/infrastructure/postgres/funnel_stats.go` — both counts filtered by `event_id IN (SELECT id
FROM booking.events WHERE status <> 'CANCELLED' AND sales_starts_at <= now() AND sales_ends_at > now())`.

- [ ] **Step 1: Correctness tests (CI):** `TestFunnelCollector_CachesFor30s` (two scrapes within 30 s → 1 stats call; at +31 s
  → 2); `TestFunnelStats_OnlyEventsOnSale` (integration; ended event's rows absent).
- [ ] **Step 2: Before:** during `npm run test:load`, 10 scrapes of `/metrics` with `curl -w '%{time_total}'`.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement.** **Step 5: Run** `gob test ./...` + integration → PASS; After.
  **Threshold:** ≤ 1 funnel query per 30 s per instance (unit test) and scrape time lower than Before.
  Evidence `docs/evidence/<run-date>-perf-c7-6-funnel-metrics.md`.
- [ ] **Step 6: Commit** `perf(booking): cache funnel metrics and count only events on sale`.

### Task C7.7: Catalog reads are cached in process with singleflight; the list is bounded (AC-C7.7 PF-P2-9)

Decision **D-C7.7:** TTL 2 s per key (matches the existing `max-age=2`); hard `LIMIT 500` on the list query — keyset
pagination needs new query parameters (contract change), not planned. Hold reads the same cached `GetDetail`: safe, the hold
transaction re-checks cancel (P2-9) and stock under locks, and prices/rules do not change after the sale opens.
`golang.org/x/sync` moves from indirect to direct in `services/booking/go.mod`.

**Files:** Create `B/infrastructure/cache/event_cache.go` — `type EventReadCache struct` embedding `outbound.IEventRepo`,
overriding `ListEvents` (key = `listKey(q dto.EventListQuery) string`: status, city, timeframe, sorted genres, start/end as
RFC3339 or empty, sort — never pointer values) and `GetDetail` (key = event id)
with a TTL map + `singleflight.Group`; `NewEventReadCache(src outbound.IEventRepo, ttl time.Duration, now func() time.Time) *EventReadCache`.
Modify `event_repo.go` `ListEvents` (`LIMIT 500`); `services/booking/internal/delivery/booking_module.go` (catalog, detail and
hold usecases get the cache).

- [ ] **Step 1: Correctness tests (CI):** `TestEventReadCache_SingleflightAndTTL` (50 concurrent `GetDetail` → 1 source call;
  after +2.1 s → 2; different ids separate); `TestEventReadCache_ListKeyIsTheQuery`.
- [ ] **Step 2: Before:** `npm run test:load`, scenario `browse`: `http_req_duration{name:events.list}` and
  `{name:events.detail}` p95, sustained RPS (RINGKASAN row), Postgres CPU (`resources.csv`).
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement.** **Step 5: Run** `gob test ./...` → PASS; After.
  **Threshold:** both p95 < 500 ms (SLO, `thresholds.js`) at every PASS level and Postgres CPU below Before.
  Evidence `docs/evidence/<run-date>-perf-c7-7-catalog-cache.md`.
- [ ] **Step 6: Commit** `perf(booking): cache catalog reads in process with singleflight`.

### Task C7.8: Cleanup job for outbox, idempotency and attendee checks (AC-C7.8 PF-P2-17 Booking part, F-cleanup)

Decision **D-C7.8:** published outbox rows kept 7 days; idempotency rows deleted once past `expires_at`; `attendee_checks`
deleted 1 day after their admission ended; batches of 1000, `SKIP LOCKED`, repeat while full (max 20 rounds).

**Files:**
- Create `services/booking/deployments/migrations/0015_retention_indexes.up.sql` / `.down.sql`:
  `idx_booking_outbox_published ON booking.outbox_events(published_at) WHERE published_at IS NOT NULL`,
  `idx_admissions_ended ON booking.admissions(ended_at) WHERE ended_at IS NOT NULL` (`idx_booking_idem_expiry` exists).
- Create `B/infrastructure/postgres/retention.go` — `PurgePublishedOutbox(ctx, before time.Time, limit int) (int, error)`,
  `PurgeExpiredIdempotency(ctx, now time.Time, limit int) (int, error)`, `PurgeAttendeeChecks(ctx, endedBefore time.Time,
  limit int) (int, error)`, each `DELETE … WHERE ctid IN (SELECT ctid … LIMIT $n FOR UPDATE SKIP LOCKED)`; port `outbound.IRetention`.
- Create `B/application/usecase/purge.go` (`Purge.Run(ctx) error`); add job `retention` in `delivery/worker.go`.

- [ ] **Step 1: Tests (CI):** `TestRetention_PurgesOnlyWhatIsDue` (integration; rows on both sides of each boundary);
  `TestPurge_RepeatsWhileBatchFull` (unit).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**, `make mock`. **Step 4: Run** `gob test ./...` + integration → PASS.
- [ ] **Step 5: Evidence** (no k6 metric applies): row counts of the three tables before/after one job run on the local stack
  after `npm run test:load` → `docs/evidence/<run-date>-perf-c7-8-retention.md`. **Threshold:** 0 rows past each boundary.
- [ ] **Step 6: Commit** `feat(booking): purge published outbox, expired idempotency and old attendee checks` (+ ai-log row for C7).

### Phase C8 — Queue: scale

Run C8.12 before C8.5 (the async writer needs the module's shutdown hook).

### Task C8.1: Per-visitor key layout (AC-C8.1 PF-P0-3) — Owner confirms before execution

Decision **D-C8.1:** recommend deferring the hash-tag re-layout until Valkey runs in cluster mode (infra PF-P0-1/2,
*Coordination*). On one node every key shares one thread, so moving keys gains nothing, and join/promote/claim/leave
scripts would lose atomicity (they touch entry hashes and event sets together). The single-node part of PF-P0-3 (`me` from
the status doc + entry in one call, in-process doc) is C8.9. If the owner wants it now: move `vtok`/`vlast` to
`q:{v:<visitorId>}:…`, keep entry hashes on the event slot, check/ratchet the generation (C6.1) with `token_gen.lua` on the
visitor slot before each event script; evidence = `PROFILE=A VUS=2000 npm run test:waiting` with Valkey
`INFO commandstats` per node, threshold from the audit (event-slot ops/s below ~150k).

### Task C8.2: Ended entries and finished events get a TTL (AC-C8.2 PF-P1-4, P3-13)

Decision **D-C8.2:** an entry hash gets `PEXPIRE 86400000` when it turns terminal (pointer keys are already deleted then);
the fixed per-event keys get `EXPIRE 86400` once the sale has ended and `active` is empty. Retention is a Go constant
`entryRetention = 24 * time.Hour` passed as an ARGV.

**Files:** Modify `lua/end_entry.lua`, `lua/leave.lua`, `lua/end_sold_out.lua`, `lua/liveness.lua`, `lua/liveness_tick.lua`
(END path), `lua/claim.lua` (MISSED path) — `PEXPIRE` on the entry hash at each terminal write; each Go caller passes
`entryRetention.Milliseconds()` as a new last ARGV. Add
`QueueStore.RetireEvent(ctx context.Context, eventID string, ttl time.Duration) error` (`DoMulti` EXPIRE on `seq`, `waiting`,
`stale`, `pending`, `active`, `hb:0..B-1`, `hbstale:0..B-1`, `status`, `sale`, `unused`, `promoter:lock`) to `IQueueStore`;
status publisher calls it when `now >= SalesEndsAt` and `ActiveCount == 0`.

- [ ] **Step 1: Tests (CI):** `TestTerminalEntriesGetRetention` (valkey): for EndEntry(PAID), Leave, EndWaitingSoldOut,
  liveness END, claim MISSED → `PTTL(entry)` in (0, 86400000]; a WAITING entry → -1. `TestRetireEvent_ExpiresFixedKeys`;
  `TestStatusPublisher_RetiresOnlyEndedEmptyEvents` (worker, fake).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**, `make mock`. **Step 4: Run** `goq test ./...` + valkey integration → PASS.
- [ ] **Step 5: Evidence:** after `PROFILE=A VUS=2000 npm run test:waiting`, scan `*entry:*` keys of ended entries: count
  with TTL -1 (Before vs After). **Threshold:** 0 (audit: 250–400 MB per 1M visitors never reclaimed).
  `docs/evidence/<run-date>-perf-c8-2-ttl.md`.
- [ ] **Step 6: Commit** `perf(queue): expire ended entries and the keys of finished events`.

### Task C8.3: Pre-turn notify uses a seq watermark outside the promoter lease (AC-C8.3 PF-P1-5)

Decision **D-C8.3:** at most once per seq: the watermark advances (atomically, `notify_window.lua` returns the previous value
and stores `max(prev, upTo)`) before publishing; a crash in between loses those `turn.soon` messages (best effort, INV-6)
instead of re-sending to thousands. Per-entry `notified_soon:` keys and `WasNotifiedSoon`/`MarkNotifiedSoon` are removed.

**Files:** Create `lua/notify_window.lua`; `QueueStore.AdvanceNotifyWatermark(ctx, eventID string, upToSeq int64) (int64, error)`,
`WaitingBetween(ctx, eventID string, afterSeq, upToSeq int64, limit int) ([]string, error)`; `Q/infrastructure/worker/promoter.go`
— `notifyPreTurn` leaves the `defer` in `runEvent` (L135) and runs from `Run` after the lease is released: `upTo` = seq at
index `notifyPosition-1` (`WaitingSeqAt`), ids = `WaitingBetween(prev, upTo)`, entries via `GetEntry` per id (C8.8 switches
this to `GetEntries`).

- [ ] **Step 1: Tests (CI):** `TestNotifyWatermark_AdvancesOnce` (valkey; two concurrent advances to 50 → one gets 0, the
  other 50); `TestPromoter_PreTurnNotifiesOnlyNewSeqsOutsideLease` (fake: tick 1 window seqs 1..10 → 10 publishes; tick 2 same
  → 0; window to 15 → 5; every publish after `ReleasePromoterLease` in the recorded call order).
- [ ] **Step 2: Before:** `PROFILE=A VUS=2000 npm run test:waiting`; Valkey `INFO commandstats` `cmdstat_exists` calls and the
  promoter `LEASE_LOST` counter. **Step 3: Run** → FAIL. **Step 4: Implement**, `make mock`.
- [ ] **Step 5: Run** `goq test ./...` + valkey integration → PASS; After. **Threshold:** `cmdstat_exists` calls/s down ≥ 90 %
  and `LEASE_LOST` = 0 (audit: ~30k serial RTT/s under the lease). `docs/evidence/<run-date>-perf-c8-3-preturn.md`.
- [ ] **Step 6: Commit** `perf(queue): notify pre-turn by seq watermark outside the promoter lease`.

### Task C8.4: Sold-out drain runs in the sweeper with a per-tick budget (AC-C8.4 PF-P1-6)

Decision **D-C8.4:** no stored cursor: each batch removes what it scanned from `waiting`/`stale` (poison ids included,
`end_sold_out.lua`), so the head of the set is the cursor. Budget 200 batches × `SoldOutBatchSize` (500) per event per tick
replaces the DLQ-bounded retry loop. The drain runs for every event with `sale.SoldOut` (this also ends the queue of a
cancelled or ended event, D-C4.4a).

**Files:** Modify `Q/infrastructure/rabbitmq/consumer.go` `handleSoldOut` — `SetSale(CLOSED, true)` + one batch, return nil
(`SoldOutMaxBatches` error removed); `Q/infrastructure/worker/sweeper.go` `runEvent` — when `GetSale(...).SoldOut`, call
`EndWaitingSoldOut` while `Scanned == batch`, within the budget.

- [ ] **Step 1: Tests (CI):** `TestHandleSoldOut_ClosesSaleWithoutRetryLoop` (fake returns full batches → nil after one);
  `TestSweeper_DrainsSoldOutWaiting` (3 full + 1 short → drained in one tick; budget respected).
- [ ] **Step 2: Before:** `BenchmarkSoldOutDrain_100k` (valkey integration, 100k WAITING entries) — time to end all.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement.** **Step 5: Run** `goq test ./...` + valkey integration → PASS; After.
  **Threshold:** 100k entries ended within one sweeper tick (< 5 s) (audit: ~40k max before DLQ).
  `docs/evidence/<run-date>-perf-c8-4-soldout-drain.md`.
- [ ] **Step 6: Commit** `perf(queue): drain sold-out waiting entries from the sweeper`.

### Task C8.5: `entry_audit` written by an async batch writer (AC-C8.5 PF-P1-7)

Decision **D-C8.5:** best effort: buffer 10 000, flush every 200 ms or 500 rows with `pgx.CopyFrom`, drop when full and count
it in `rbt_queue_entry_audit_dropped_total`; `entry_audit` is an operational trail, not the source of truth.

**Files:** Create `Q/infrastructure/postgres/entry_audit_async.go` — `type AsyncEntryAudit struct` implementing
`outbound.IEntryAuditRepo`; `NewAsyncEntryAudit(pool *pgxpool.Pool, buffer, batch int, every time.Duration) *AsyncEntryAudit`,
`Run(ctx context.Context)`, `Close(ctx context.Context) error` (flush on shutdown). Wire in `queue_module.go` and stop it in
`queueModule.Close` (C8.12).

- [ ] **Step 1: Tests (CI):** `TestAsyncEntryAudit_BatchesAndDropsWhenFull` (fake inserter: 1200 appends → flushes ≤ 500;
  buffer 10 with a blocked inserter → drops counted); `TestAsyncEntryAudit_CopyFromWritesRows` (postgres integration).
- [ ] **Step 2: Before:** `PROFILE=A VUS=2000 npm run test:waiting`: `http_req_duration{name:queue.join}` p95, Postgres
  connections (`infra.csv`). **Step 3: Run** → FAIL. **Step 4: Implement.**
- [ ] **Step 5: Run** `goq test ./...` + postgres integration → PASS; After. **Threshold:** join p95 < 500 ms (SLO) and no
  synchronous INSERT on the join path (audit: one or two per join). `docs/evidence/<run-date>-perf-c8-5-audit-writer.md`.
- [ ] **Step 6: Commit** `perf(queue): write entry audit rows in asynchronous batches`.

### Task C8.6: Sale window "known" flag, singleflight on cold reads; deprecated queue token (AC-C8.6 PF-P1-20, PF-P3-2)

Decision **D-C8.6:** `SetSaleWindow` also stores `windowAtMs`; `checkSale` treats the window as known for 30 s even when
Booking returned zero values, and the cold path runs behind a `singleflight.Group` keyed by event id; the settings cache
Postgres fallback gets its own `singleflight.Group`. **Owner confirms before execution:** stop signing `queueToken`
(`join.go:371`) — `queue.yaml` still says it is issued for one more release; the field is optional, so the shape is unchanged.

**Files:** Modify `Q/application/usecase/join.go` (`checkSale`, ~L182), `Q/infrastructure/valkey/queue_store.go`
(`SetSaleWindow`, `GetSale`; `outbound.SaleState.WindowAtMs int64`), `Q/infrastructure/valkey/event_settings_cache.go`.

- [ ] **Step 1: Tests (CI):** `TestJoin_ColdSaleWindowOneBookingCall` (50 concurrent joins, empty cache → `Supply` once);
  `TestJoin_ZeroWindowIsKnownFor30s`; `TestEventSettingsCache_SingleflightOnMiss` (valkey; 20 concurrent misses → source once).
- [ ] **Step 2: Before:** `PROFILE=A VUS=2000 npm run test:waiting`; Booking `/metrics` request count on the supply route.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement.** **Step 5: Run** `goq test ./...` + valkey integration → PASS; After.
  **Threshold:** ≤ 1 supply call per event per 30 s per Queue instance from join (audit: one per join when the window is
  unknown). `docs/evidence/<run-date>-perf-c8-6-sale-window.md`.
- [ ] **Step 6: Commit** `perf(queue): cache the sale window as known and collapse cold reads`.

### Task C8.7: Liveness tick has one budget across shards (AC-C8.7 PF-P2-1)

Decision **D-C8.7:** `QUEUE_LIVENESS_BATCH_MAX` becomes the total per tick (not per shard); the start shard rotates per tick
(in-process counter in `Liveness`), so every shard is reached.

**Files:** Modify `lua/liveness_tick.lua` and `lua/liveness.lua` (ARGV start shard; stop when staled + ended reaches the
budget); `QueueStore.SweepLivenessTick(…, startShard int)`; `Q/infrastructure/worker/liveness.go`; config comment.

- [ ] **Step 1: Tests (CI):** `TestLivenessTick_TotalBudgetAcrossShards` (16 shards × 100 silent entries, budget 300 →
  staled 300; rotating start covers all shards within 6 ticks).
- [ ] **Step 2: Before:** `BenchmarkLivenessTick_MassDisconnect` (32k silent entries, B = 16) → ms per call.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement.** **Step 5: Run** valkey integration → PASS; After.
  **Threshold:** ≤ 10 ms per call (audit: 50–200 ms block). `docs/evidence/<run-date>-perf-c8-7-liveness.md`.
- [ ] **Step 6: Commit** `perf(queue): bound the liveness tick with one budget across shards`.

### Task C8.8: Promoter reads candidates in one call and publishes after the lease (AC-C8.8 PF-P2-2)

Batched broker confirms need `pkg/rabbitmq` (PF-P0-7) → *Coordination*; here the publishes leave the lease.

**Files:** Add `QueueStore.GetEntries(ctx context.Context, eventID string, ids []string) (map[string]entity.Entry, error)`
(`DoMulti` HGETALL; missing ids omitted) to `IQueueStore`; `promoter.go` `buildEntries` uses it; `runEvent` returns the
promoted entries and `Run` publishes `turn.arrived` after the lease is released.

- [ ] **Step 1: Tests (CI):** `TestPromoter_FetchesCandidatesInOneCall`; `TestPromoter_PublishesAfterLeaseRelease`;
  `TestGetEntries_MissingIdsOmitted` (valkey).
- [ ] **Step 2: Before:** `BenchmarkPromoterTick_200` (200 candidates, Booking stub) → ms per tick.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement**, `make mock`. **Step 5: Run** → PASS; After.
  **Threshold:** tick ≤ 100 ms (the promoter interval; audit: tick far longer than the interval).
  `docs/evidence/<run-date>-perf-c8-8-promoter.md`.
- [ ] **Step 6: Commit** `perf(queue): fetch promoter candidates in one call and publish after the lease`.

### Task C8.9: Status document in process; `me` in one script; tighter `me` limit (AC-C8.9 PF-P2-3)

Decision **D-C8.9:** doc refreshed lazily every 250 ms per event (`atomic.Pointer` + singleflight), the status endpoint serves
the raw bytes (`json.RawMessage`, response shape unchanged); `RATE_LIMIT_ME_PER_VISITOR` default 120 → **60**/min (the
fastest legal poll: `pollNearMs` ≥ 1000 ms); deployment env values → *Coordination*.

**Files:** Create `Q/infrastructure/valkey/status_doc_cache.go` (`NewStatusDocCache(store outbound.IQueueStore, ttl time.Duration,
now func() time.Time)`, `Get(ctx, eventID) (raw []byte, doc dto.StatusDocument, ok bool, err error)`); port
`outbound.IStatusDocReader`; `inbound.IStatusUsecase.GetStatus(ctx, eventID string) (json.RawMessage, error)` (status handler
writes it as `data`); `my_entry.go`/`heartbeat.go` `readDoc` via the cache. Create
`lua/me.lua` — token check + ratchet (C6.1), `visitor:` → `vlast:` resolve, HGETALL, live position (ZCOUNT) in one EVAL;
`QueueStore.MyEntry(ctx, eventID, visitorID string, gen int64) (outbound.MyEntryResult, error)`; config default in `load.go:94`.

- [ ] **Step 1: Tests (CI):** `TestStatusDocCache_RefreshesEvery250ms` (100 gets within 250 ms → 1 store read; after 260 ms → 2);
  `TestMyEntryScript_MatchesMultiCallResult` (valkey: WAITING, ADMITTED, ENDED, absent, invalid gen);
  `TestGetMyEntry_OneStoreCall`; `TestLoad_MeLimitDefault60`.
- [ ] **Step 2: Before:** `PROFILE=A VUS=2000 npm run test:waiting` and `PROFILE=C`: `http_req_duration{name:queue.me}` and
  `{name:queue.status}` p95, Valkey ops/s (`infra.csv`). **Step 3: Run** → FAIL. **Step 4: Implement**, `make mock`.
- [ ] **Step 5: Run** `goq test ./...` + valkey integration → PASS; After. **Threshold:** Valkey commands per `me` ≤ 3 (limiter
  + one EVAL; audit: 5–8 sequential RTT) and both p95 < 500 ms. `docs/evidence/<run-date>-perf-c8-9-me-status.md`.
- [ ] **Step 6: Commit** `perf(queue): serve the status document from memory and answer me in one script`.

### Task C8.10: Workers iterate only active events (AC-C8.10 PF-P2-4, F-qretire)

Decision **D-C8.10:** an event is iterated until 1 day after its sale ends (time for drains, liveness and C8.2 retirement);
an event whose window is not known yet stays iterated until the status publisher writes it.

**Files:** Create `services/queue/deployments/migrations/0006_event_settings_sale_window.up.sql` / `.down.sql` (nullable
`sales_starts_at`, `sales_ends_at` on `event_settings`). Add to `IEventSettingsRepo`: `ListActiveEventIDs(ctx, now time.Time)
([]string, error)` (`WHERE sales_ends_at IS NULL OR sales_ends_at > $1 - interval '1 day'`) and `UpdateSaleWindow(ctx,
eventID string, starts, ends time.Time) error`; a 5 s in-process cache for the list; promoter, sweeper, liveness and status
publisher use it; the status publisher writes the window only when it differs from the last value it wrote (in-memory map).

- [ ] **Step 1: Tests (CI):** `TestListActiveEventIDs_DropsEventsEndedOverADay` (postgres integration);
  `TestStatusPublisher_WritesSaleWindowOnlyOnChange` (worker).
- [ ] **Step 2: Before:** 50 configured past events, idle 60 s: Booking supply-route calls/s (`/metrics`).
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement**, `make mock`. **Step 5: Run** → PASS; After.
  **Threshold:** 0 supply calls for retired events (audit: every configured event, every second).
  `docs/evidence/<run-date>-perf-c8-10-active-events.md`.
- [ ] **Step 6: Commit** `perf(queue): iterate only active events in the workers`.

### Task C8.11: Visitor token verification cost measured (AC-C8.11 PF-P2-5)

**Files:** Create `Q/infrastructure/security/visitor_auth_bench_test.go` — `BenchmarkResolveToken` (Ed25519 verify + device HMAC).

- [ ] **Step 1: Run** `goq test -run '^$' -bench BenchmarkResolveToken ./internal/queue/infrastructure/security/`; plus Queue CPU %
  from `resources.csv` during `PROFILE=A VUS=2000 npm run test:waiting`.
- [ ] **Step 2: Evidence** `docs/evidence/<run-date>-perf-c8-11-token-verify.md`: cores at 50k rps = ns/op × 50 000 / 1e9.
  **Budget:** ≤ 1 core per 50k rps (≤ 20 µs/op; audit estimate ~50 µs). Over budget → record a follow-up; no code change here.
- [ ] **Step 3: Commit** `test(queue): measure visitor token verification cost`.

### Task C8.12: One module per process; publisher closed on shutdown (AC-C8.12 PF-P3-1)

**Files:** Modify `services/queue/internal/delivery/server.go` (`NewHandler(cfg config.Config, mod *queueModule, logger *slog.Logger)`),
`worker.go` (`RunWorker` and `jobs` build the module once), `queue_module.go` — keep the publisher on the module and add
`func (m *queueModule) Close() error` (publisher, then connection; nil-safe); `RunServer`/`RunWorker` `defer mod.Close()`.

- [ ] **Step 1: Tests (CI):** `TestNewHandler_UsesGivenModule` (no dial when a module is passed); `TestQueueModule_CloseIsNilSafe`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `goq test ./...` → PASS.
- [ ] **Step 5: Evidence:** RabbitMQ connections of one worker process (`rabbitmqctl list_connections`) Before/After →
  **threshold** 1 per process (audit: a second module dials again) → `docs/evidence/<run-date>-perf-c8-12-module.md`.
- [ ] **Step 6: Commit** `fix(queue): build one module per process and close the publisher on shutdown` (+ ai-log row for C8).

### Phase C9 — Data & operations

### Task C9.1: Stock drift query, metric and lifecycle property test (AC-C9.1 F-drift)

Decision **D-C9.1:** one SQL file is the single source (`//go:embed stock_drift.sql`), used by the repo, the metric and C9.3.
Kinds: `allocated` (supply vs Σ `outstanding_units` of RESERVED/ACTIVE/EDITABLE), `held` / `sold` (supply vs HELD / PAID
items), `category_held` / `category_sold` (inventory vs items per category), `quota_held` (buyer quota vs HELD items of that
buyer's bookings). Metric `rbt_booking_stock_drift{kind}` = Σ |delta| over events, cached 60 s (C7.6 pattern). The alert
rule lives in `deployments/` → *Coordination*.

**Files:** Create `B/infrastructure/postgres/stock_drift.sql`, `stock_drift.go` (`StockDrift(ctx) ([]outbound.StockDrift, error)`,
`type StockDrift struct { EventID, Kind string; Delta int }` in `ports.go`), `B/infrastructure/telemetry/drift_collector.go`;
register next to the funnel collector.

- [ ] **Step 1: Tests (CI):** `TestStockDrift_ReportsInjectedDrift` (`held_units + 1` by SQL → `{Kind: "held", Delta: 1}`);
  `TestStockDrift_ZeroAfterRandomLifecycles` (pattern of `TestSupplyLedger_Property`, seed 11, 2 categories, 40 admissions,
  concurrent random hold / ReplaceItems / PaymentLock+Settle / late Settle (REVIEW) / Expire PAYMENT_TIMEOUT / Expire CANCELLED
  / Release / ExpireSession → `StockDrift` empty); `TestDriftCollector_CachesFor60s`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**, `make mock`. **Step 4: Run** `gob test ./...` + integration → PASS.
- [ ] **Step 5: Commit** `feat(booking): stock drift query, metric and lifecycle property test`.

### Task C9.2: Local stack on current migrations (AC-C9.2 F-env) — owner step

- [ ] Owner runs `migrate status` for booking and queue (the schema is sometimes changed by hand: ask before `up`), then
  `migrate up`; expected Booking 0015, Queue 0006. Versions are recorded in C9.3's evidence. No commit.

### Task C9.3: Spike run and drift evidence (AC-C9.3)

- [ ] **Step 1:** `npm run test:spike` on the local stack (Perf evidence procedure, steps 1 and 4).
- [ ] **Step 2:** 60 s after the run: `psql … -f services/booking/internal/booking/infrastructure/postgres/stock_drift.sql`.
- [ ] **Step 3:** `docs/evidence/<run-date>-spike-drift.md`: spike verdict (README rules), drift rows, migration versions.
  **Threshold:** 0 drift rows; spike recovery level PASS.
- [ ] **Step 4: Commit** `docs(evidence): spike run and stock drift after Part C` (+ ai-log row for C9).

**AC-C9.4 (P1-19):** not planned — owner kept old data (D-B12). Revisit only if the residual quota risk becomes unacceptable.

---

## Coordination (owned by arnu12rahman — not executed in this plan)

- **P1-17** One `PII_ENC_KEY` seals account data and the OTP transport ciphertext. Proposal: User seals OTPs with a new
  `OTP_TRANSPORT_KEY`; Notification opens with it; both deploy together (OTP TTL 5 min bounds the overlap). Key-id byte /
  HKDF per purpose is a `pkg/crypto` change.
- **P1-1** Notification dedup is already keyed on `source_message_id`; B6 makes that id stable. Confirm no Notification change is needed.
- **P2-20** `/metrics` on the public mux: restrict at the ALB listener rule (Terraform) or move to a separate listener.
- **P1-2** `pkg/httpx` / Terraform part: User `APP_TRUSTED_PROXY_HOPS` vs CloudFront → ALB topology (ALB SG only the CDN prefix list).
- **Notification:** P0-1..3, P2-1..5, P3-1..3, PF-P1-12..15, PF-P2-11..14, PF-P3-5.
- **Payment:** P0-6, P0-7, P1-8..10, P2-11..13, P3-7..10, PF-P0-8, PF-P1-18, PF-P1-19, PF-P2-10, PF-P3-4.
- **`pkg/` & infra:** PF-P0-1, PF-P0-2, PF-P0-5 (`pkg/outbox`), PF-P0-6, PF-P0-7, PF-P0-9, PF-P1-8..11, PF-P2-18, PF-P3-7..9; P3-21 toolchain.
- **Part C needs (from nuzirwan's tasks):** Queue `APP_TRUSTED_PROXY_HOPS=2` per deployment + ALB SG limited to the CDN
  prefix list (C6.3); `QUEUE_PROMOTE_BATCH_MAX` ≤ 100 and `RATE_LIMIT_ME_PER_VISITOR` (new default 60) in deployment env
  files if set there (C7.2, C8.9); optional `QUEUE_CLAIMED_SLOT_MAX_SECONDS` (C6.5); alert rule on
  `rbt_booking_stock_drift > 0` (C9.1); batched confirms in `pkg/rabbitmq` for the Booking relay and `turn.arrived` (C7.4,
  C8.8, PF-P0-7); Valkey cluster mode before C8.1.
- **User performance items** (nuzirwan, not yet planned): PF-P1-16, PF-P1-17, PF-P2-15, PF-P2-16, PF-P3-6 — add as Phase C10 when started.

## Coverage

| Finding | Task | Finding | Task | Finding | Task |
|---|---|---|---|---|---|
| P0-4, P0-5, P3-5 | B0 | P1-3 | B1 | P1-4 | B2 |
| P1-5, F6 (part) | B3 | P1-6 | B4 | P1-7 | B5 |
| P1-1 (Booking) | B6 | P2-8 | B7 | P2-6 | B8 |
| P2-7, F-idem | B9 | P2-9, P2-10 | B10 | P3-4, P3-6 | B11 |
| P1-19 | B12 (+C9.4) | P1-14, P1-15 | U1 | P2-22 | U2 |
| P2-21, P3-22 | U3 | P1-16, P2-19 | U4 | P3-16 | U5 |
| P3-17, P3-18 | U6 | P2-23, P3-19, P3-20 | U7 | PF-P3-3, F6 (cap) | C2.1 |
| F-clock | C3.1 | F-attendee | C3.2 | F-lock | C4.1 |
| F-status | C4.2 | F-actor, F-cancel (admin) | C4.3 | F-window, F-cancel (reserve/hold) | C4.4 |
| F-dead-cols, F-admin-fields | C4.5 | F-avail, F-snapshot | C5.1 | F-supply | C5.2 |
| F-limited, P1-12 | C5.3 | P1-11, P2-17 | C5.4 | P0-8 | C6.1 |
| P1-13 | C6.2 | P1-2 (Queue), P2-18 | C6.3 | P2-14 | C6.4 |
| P2-15 | C6.5 | P2-16 | C6.6 | P3-11 | C6.7 |
| P3-12 | C6.8 | P3-14 | C6.9 | P3-15 | C6.10 |
| F-qcache, F-qlock | C6.11 | F-qdead | C6.13 note (owner: no change) | PF-P0-4 | C7.1 |
| PF-P1-1 | C7.2 | PF-P1-2, PF-P2-8 | C7.3 | PF-P0-5 (Booking) | C7.4 |
| PF-P1-3, PF-P2-6 | C7.5 | PF-P2-7 | C7.6 | PF-P2-9 | C7.7 |
| PF-P2-17 (Booking), F-cleanup | C7.8 | PF-P0-3 | C8.1 (owner) + C8.9 | PF-P1-4, P3-13 | C8.2 |
| PF-P1-5 | C8.3 | PF-P1-6 | C8.4 | PF-P1-7 | C8.5 |
| PF-P1-20, PF-P3-2 | C8.6 | PF-P2-1 | C8.7 | PF-P2-2 | C8.8 |
| PF-P2-3 | C8.9 | PF-P2-4, F-qretire | C8.10 | PF-P2-5 | C8.11 |
| PF-P3-1 | C8.12 | F-drift | C9.1 | F-env | C9.2 (owner step) |
| k6 spike + drift evidence | C9.3 | P1-17, P2-20, P1-18 (resolved), Notification, Payment, `pkg/` | Coordination | | |

## Security & sensitive data

- No task writes plaintext NIK/email to logs, events or fixtures; tests use fake NIKs and `example.test`.
- B7 and B0 release stock on REVIEW; an Ops "honour payment" decision must re-acquire stock (unchanged from the Settle-late path).
- U4 fails closed on limiter errors; U1 narrows revoke-all, so the 20 s grace must stay short (a stolen token replayed inside
  20 s of the real rotation is not detected — accepted trade-off, written in D-U1).
- C6.1: a token rotated by REATTACH in one event stays usable in another event until the new token is used there (same as
  today). C6.2 stores only a keyed hash of the client IP. C4.3 records the account id (not the email) as audit actor.
- C8.5 may drop `entry_audit` rows under overload (counted); it is not a security log.

## DB / config / compatibility

- No migration. New domain event type `BookingDeadlineExtended` (event-store CHECK accepts it).
- Port changes: `IAdmissionRepo.FindByJTI` removed; `IBookingRepo.DeferVerification` added; `SettleSpec.AmountMinor` added;
  User `VerifyAccess` and `UpdateProfile` signatures change → `make mock` in both services.
- Contract: `user.yaml` `PATCH /me` adds 429 + revocation note; my-bookings cursor format changes but stays opaque.
- Behaviour changes to announce to FE: NIK change → next call 401 → refresh; extend-deadline refused for VERIFYING bookings;
  unknown-time payments now settle PAID instead of REVIEW (B0).
- `booking.journey.ended` on REVIEW (B0) is an existing event type with an existing consumer (Queue) — additive.
- **Part C migrations:** Booking 0013 (rules lock by time + backfill), 0014 (`sweep_lease_until` on admissions and bookings),
  0015 (retention indexes); Queue 0006 (sale window on `event_settings`). All additive; down files provided.
- **Part C port changes** (`make mock`): Booking `ReplaceSpec.Now`, `PaymentLockSpec.Now`, `HoldLine.ItemID`,
  `IAttendeeChecker.RecordCheck`, `IEventWriteRepo`/`IEventPublicationRepo`/admin usecases gain `actor`,
  `AvailabilitySnapshot` in the availability ports (`IncrConn`/`DecrConn` removed), `ClaimDueSessions`/`ClaimDueBookings`,
  `IRetention`, `StockDrift`; Queue `CheckTokenGen` (replaces `TokenGen`), `JoinParams.GuestScope`, `Claim(…, claimMaxMs)`,
  `ISettingsCacheInvalidator`, `RetireEvent`, notify watermark, `GetEntries`, `MyEntry`, `ListActiveEventIDs`/`UpdateSaleWindow`.
- **Part C contract steps, all Owner confirms before execution:** 409 `EVENT_CANCELLED` on admin publish (C4.3), fixed-default
  description (C4.5), supply fixture `remainingTickets` (C5.2), `entries.maxItems: 100` + 400 (C7.2), stop issuing
  `queueToken` (C8.6). No other Part C task touches `docs/contracts/`.
- **Part C behaviour to announce:** catalog list/filter show SOLD_OUT like detail (C4.2); detail availability is capped by free
  event stock (C5.1); supply `remainingTickets` = free units (C5.2); a sold-out sale can reopen (C5.3); `oneActivePerVisitor=false`
  → 400 (C6.4); claim can answer 429, missing `eventId` → 400 (C6.7); guests without device id or IP get no idempotent replay
  (C6.2); defaults `QUEUE_PROMOTE_BATCH_MAX` 100, `RATE_LIMIT_ME_PER_VISITOR` 60, `QUEUE_LIVENESS_BATCH_MAX` now per tick.

## Verification / tooling note

- Local Go is 1.18; the repo needs 1.26.11, so the gate cannot run on the host. Run through `golang:1.26-alpine` (already
  pulled) with `--network host` and `TEST_POSTGRES_DSN` pointing at the compose Postgres; `-race` needs gcc (CI, or a
  non-alpine `golang:1.26` image). `.git/feature-workflow/commands.env` currently targets `services/user` only — extend it to
  `services/booking` before running `/feature` on Part A.
- The local DB is behind on migrations; integration tests create their own DB from migrations, so they are unaffected.
