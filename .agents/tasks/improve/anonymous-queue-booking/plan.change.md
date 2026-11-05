# Plan (change) — anonymous-queue-booking: CHANGE 1 (drop draw) + CHANGE 2 (fix promoter-tick slowdown)

Author: tech-lead (plan). Scope: a SHORT ordered delta over the already-approved plan (`plan.md`) and the
approved design-change note (`design.md` rev 4 + ADR-030 amendment §7). It covers ONLY the two changes
the owner forced at the human gate ("drop random draw. slowndown arent alllowed") plus the doc/test
updates those two force. Everything else the owner approved stays exactly as built — see the KEEP list in
`design.md` §3; do NOT touch anonymous join, 3-units-per-turn, NIK-at-hold, the hold-accounting fix, token
device claim, me-ENDED-reason, or the NIK throttle beyond the field-name revert noted below.

Work happens in the existing worktree
`/home/nuzirwan/project/assesment/berebut-tiket/booking-be/rusuh-booking-be/.worktrees/improve/anonymous-queue-booking`
(branch `improve/anonymous-queue-booking`, base `feature/rework` @ 53e74da). **All changes stay
uncommitted: no commit, merge, rebase, push, stash or reset.** `git` reads use
`git -C <worktree>`.

Inputs read and grounded against live code: `design.md` (rev 4), `design.change.review.json`
(verdict CHANGES_REQUESTED — open findings **F11 HIGH** and **F12 NIT**; F11 is folded into this plan,
F12 is resolved in step 1), the brief, `plan.md`, `.kiro/steering`, and the live
`services/queue` tree (29 files carry a `DRAWING` token today; the `0004_opening_window` migration and all
draw files exist; `heartbeat_load_integration_test.go` exists).

## 0. Design decisions this plan settles (beyond the design note)

- **F11 (HIGH), Go-side DRAWING completeness.** The design `grep -ri DRAWING services/queue` = empty gate is
  unreachable from design §3's list: four LIVE Go branches it never names must also go to plain FIFO —
  `my_entry.go:101` (`|| ent.Status == StatusDrawing` in the NextHeartbeatMs guard), `my_entry.go:96`
  (`!doc.Drawing` zone-emit guard), `join.go:161` (`StatusDrawing` special-case suppressing direct
  admission), and `queue_store.go:448` (`else if e.Status != StatusDrawing` rank backfill). Because
  `StatusDrawing` is deleted, leaving these breaks the build. This plan schedules all four in step 2, plus
  the residual comment/test-row cleanup the grep lists. The empty-grep is the completion gate (step 3).
- **F12 (NIT), Lua `rank` field.** Decision: **drop the Lua/storage `rank` field entirely**, not leave it
  as-is. `join.lua` writes `seq` directly into the `waiting`/`stale` ZADD score and no longer `HSET`s a
  `rank` field; `heartbeat.lua`/`liveness.lua` score by `seq`; `queue_store.go hashToEntry` stops reading
  `m['rank']`. Rationale: with the draw gone `rank==seq` always, so a separate `rank` field is pure dead
  entanglement; a clean removal (over "leave as-is") is what keeps the empty-grep gate meaningful and
  matches design §3's "the `*Rank` layer carries no information once the draw is gone". This is the one
  place the KEEP list's field-name revert reaches into Lua/storage.
- **Baseline for CHANGE 2** is `QUEUE_LIVENESS_SHARDS` B=1 (reproduces today's single-key +26–38%);
  the gate is B=16 ≤ +20%. Approach = shard the `hb`/`hbstale` liveness index (design §4); no alternative
  is reopened.

## 1. Test environment rule (applies to every verify command)

Project-dedicated Valkey ONLY — never Redis `:6379` or the cluster `:7000–7005`. Start the out-of-band
container and override the env for the test process only (never edit the Makefile/CI default):

```
docker run -d --name aqb-valkey -p 16379:6379 valkey/valkey:8.1-alpine
export TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]'
```

Every `-tags integration` Queue command below is run with that export. The load test (step 8) asserts the
endpoint and fails loudly on `:6379`/`:7000–7005`. If `aqb-valkey` cannot start, the step says so and the
≤ +20% target is NOT claimed met (design §4.6).

Compile-green rule: every step ends with `cd services/queue && go build ./... && go vet ./... &&
go vet -tags integration ./...` exit 0 (the draw removal edits both plain and `integration`-tagged files).

---

## 2. Ordered steps

- [ ] 1. **(CHANGE 1, Lua + storage, resolves F12) Revert the draw/rank entanglement in the Lua scripts and the Valkey hash.**
      Drop the `rank` field: `join.lua` writes `seq` into the `waiting`/`stale` ZADD score and removes its
      `HSET entry:{id} rank`; `heartbeat.lua` and `liveness.lua` score/read by `seq` not `rank`; delete the
      draw scripts `draw_apply.lua`, `draw_finalize.lua`; delete the `heartbeat.lua` `elseif st=='DRAWING'`
      revive arm (revive handles STALE→WAITING only); drop `or st=='DRAWING'` from the `claim.lua`
      NOT_ADMITTED guard; trim the `DRAWING` word from the `promote.lua` line-5 comment; in `liveness.lua`
      delete the `elseif st=='DRAWING'` STALE branch and the `DRAWING` arm of the end loop; remove the
      `join.lua` windowed branch.
      Files: `services/queue/assets/lua/{join,heartbeat,liveness,claim,promote}.lua`; delete
      `services/queue/assets/lua/{draw_apply,draw_finalize}.lua`;
      `services/queue/internal/queue/infrastructure/valkey/queue_store.go` (`hashToEntry` stops reading
      `m['rank']`; the `arr[6]`/`res.Rank` join-result parse at line 134–144 drops to `seq`).
      Verify: `cd services/queue && go build ./...` exit 0 (storage layer still compiles — note the Go
      `*Rank` symbols are renamed in step 2; do steps 1–2 as one buildable unit if build breaks here).

- [ ] 2. **(CHANGE 1, Go layer, incl. F11 sites) Remove the draw mechanism and DRAWING state; revert `*Rank`→`*Seq`; neutralize all four F11 live branches.**
      Delete `worker/draw.go` (the `Draw` type, `CryptoSeed`, `Gate`, `run`, `computeOrder`) and
      `valueobject.Shuffle` + its tests. Delete `valueobject.StatusDrawing` and its `CanTransitionTo` case.
      Revert the zone-math rename: `dto.StatusDocument.{HeadRank,GuaranteedUntilRank,PossibleUntilRank}` →
      `{HeadSeq,GuaranteedUntilSeq,PossibleUntilSeq}`, drop `StatusDocument.Drawing`; `valueobject.ZoneForRank`
      → `ZoneForSeq` (same formula); `IQueueStore.WaitingRankAt` → `WaitingSeqAt`; remove `entity.Entry.Rank`
      (seq is the order). Delete the draw ports on `IQueueStore` (`IsDrawn`, `PendingEntries`, `EnsureDrawSeed`,
      `GetDrawOrder`, `WriteDrawOrder`, `DrawProgress`, `DrawApply`, `DrawFinalize`), the `IDrawSource` port,
      `PendingEntry` if unused, and their Valkey impls (`drawApply`/`drawFinalize` handles). Remove
      `Promoter.WithDraw`, `Promoter.windowGate`, the `draw` field, the `WithDraw(...)` call in
      `delivery/queue_module.go`, and the `drawing`/null-while-drawing branches in `status_publisher.go ranks()`,
      `mapper.rankOf()`, `heartbeat.go` (the `res.Status != "DRAWING"` guard at line 51). Remove
      `entity.EventSettings.OpeningWindowSeconds`, the column from `event_settings_repo.go`, and `join.go`'s
      `Windowed`/`JoinParams.Windowed`.
      **F11 four branches** → plain FIFO: `my_entry.go:101` drop the `StatusDrawing` disjunct (WAITING/STALE
      only); `my_entry.go:96` drop the `!doc.Drawing` guard; `join.go:161` remove the `StatusDrawing`
      special-case so `JoinCreated` always runs the normal audit + `tryDirectAdmission` path;
      `queue_store.go:448` collapse `else if e.Status != StatusDrawing` so rank backfills to `seq`
      unconditionally. Clean the residual `DRAWING` comments/enum rows the grep lists
      (`entity/entry.go`, `dto/{entry_status,status_document,join_request}.go`, `valueobject/anon_values.go`
      comment on the KEPT `NextHeartbeatMs`, `worker/liveness.go`, `port/outbound/{types,queue_store}.go`).
      Regenerate mocks: `make mock`.
      Files: `services/queue/internal/queue/{domain/valueobject/{status,zone,anon_values}.go,
      domain/entity/{entry,event_settings}.go, domain/dto/{status_document,entry_status,join_request}.go,
      domain/mapper/entry_mapper.go, application/usecase/{heartbeat,my_entry,join}.go,
      infrastructure/valkey/queue_store.go, infrastructure/worker/{status_publisher,liveness}.go,
      infrastructure/postgres/event_settings_repo.go, port/outbound/{queue_store,types}.go,
      port/inbound/queue_usecase.go, delivery/queue_module.go}`; delete `worker/draw.go`; `_mock/**`
      (generated).
      Verify: `cd services/queue && go build ./... && go vet ./... && go vet -tags integration ./...` exit 0;
      `make mock` exit 0 and `git -C <worktree> diff --stat services/queue/_mock` shows only generated changes.

- [ ] 3. **(CHANGE 1, config + migration) Remove `QUEUE_DRAW_CHUNK`, delete the `0004_opening_window` migration, and prove no residual DRAWING.**
      Delete the `Liveness.DrawChunk` field (`config/queue.go:151`), its default (`load.go:112`), its
      validation (`config/queue.go:172-173`), and the `load_test.go` assertions (lines 100, 114). Because all
      work here is uncommitted and `0004` has not shipped (design §5), **delete** both
      `services/queue/deployments/migrations/0004_opening_window.{up,down}.sql` outright (no `0005` needed);
      state this choice in the evidence file.
      Files: `services/queue/internal/config/{queue,load,load_test}.go`; delete
      `services/queue/deployments/migrations/0004_opening_window.{up,down}.sql`.
      Verify: `cd services/queue && go build ./... && go vet ./... && go test -race -count=1 ./internal/config/...`
      pass; **`grep -ri DRAWING services/queue` returns empty** and
      `grep -ri Shuffle services/queue` returns empty (the completion gate for F11/F10; any hit is a defect to
      fix before continuing).

- [ ] 4. **(CHANGE 1, draw/window tests WITHDRAWN) Delete draw-only tests and de-entangle the abuse sim.**
      Delete `liveness_draw_integration_test.go` (both the valkey and worker copies, `TestDraw_*`),
      `opening_window_example_integration_test.go` (TC-ANON-20), `status_zones_test.go
      TestStatusPublisher_DrawingPublishesNulls`, `my_entry_heartbeat_test.go
      TestGetMyEntry_DrawingHasNullRankAndZone` and the "drawing: null rank" subtest; rename the surviving
      `*Rank` assertions to `*Seq` (coverage is preserved — rank==seq today). De-entangle
      `abuse_simulation_integration_test.go` (TC-ANON-19) per design §3: drop the `WithDraw(...)` call and the
      `seed`/`window` params + `fixedSeed` helper from `newSim`, change `join(id, windowed)` → `join(id)` and
      fix every call site, replace `e.Rank` reads with order-by-seq / who-is-admitted assertions, and DELETE
      the two draw-only sub-cases `TestAbuseSim_OpeningWindowGhostsAndRealBuyer` /
      `TestAbuseSim_OpeningWindowRealBuyerDrawnFirst`; sub-cases (a)/(a2)/(b)/(c)/(d) stay and keep running
      under FIFO.
      Files: delete
      `services/queue/internal/queue/infrastructure/{valkey,worker}/liveness_draw_integration_test.go`,
      `services/queue/internal/queue/infrastructure/worker/opening_window_example_integration_test.go`; edit
      `services/queue/internal/queue/infrastructure/worker/{status_zones_test.go,abuse_simulation_integration_test.go,fakestore_test.go,worker_test.go}`,
      `services/queue/internal/queue/application/usecase/my_entry_heartbeat_test.go`,
      `services/queue/internal/queue/domain/mapper/entry_mapper_test.go`,
      `services/queue/internal/queue/domain/valueobject/anon_values_test.go`,
      `services/queue/internal/queue/infrastructure/valkey/queue_store.go`-adjacent test fixtures as needed.
      Verify: `cd services/queue && go vet -tags integration ./...` exit 0 and
      `go test -race -count=1 ./internal/queue/domain/... ./internal/queue/application/...` pass (unit; FIFO
      zone/mapper assertions green); `go test -tags integration -race -count=1 -run TestAbuseSim ./internal/queue/infrastructure/worker/`
      passes with the kept FIFO sub-cases (real `aqb-valkey`).

- [ ] 5. **(CHANGE 2, config) Add `QUEUE_LIVENESS_SHARDS` and a Go `shard(entryID)=crc16(entryID)%B` helper.**
      New config `QUEUE_LIVENESS_SHARDS` (default 16, validated 1..256; B=1 reproduces today's single key).
      Add one `pkg`-level crc16 shard helper used by join, heartbeat, promote, leave, end and the sweeper so
      all agree. Test-first: config range table, and a table-driven test that `shard` is deterministic and
      in `[0,B)`.
      Files: `services/queue/internal/config/{queue,load,load_test}.go`; new `pkg/queueshard/shard.go` +
      `pkg/queueshard/shard_test.go` (or the existing `pkg/` location the repo uses for cross-cutting
      helpers — place beside similar helpers).
      Verify: `cd services/queue && go build ./... && go vet ./... && go test -race -count=1 ./internal/config/...`
      and from worktree root `go test -race -count=1 ./pkg/queueshard/...` pass.

- [ ] 6. **(CHANGE 2, Lua + Go write paths) Shard the `hb`/`hbstale` liveness index across all six scripts.**
      Go computes the shard and passes `hb:{s}`/`hbstale:{s}` as keys; each entry stores its shard at join
      (`HSET entry:{id} shard {s}`) and every mutation reads the stored shard (never recomputes). Route ALL
      SIX scripts (design §4.2/§4.4): `join.lua` seeds `ZADD hb:{s}`; `heartbeat.lua` WAITING `ZADD hb:{s}`
      and STALE→WAITING revive `ZREM hbstale:{s}`+`ZADD hb:{s}`; `liveness.lua` sweep ranges/`ZREM`/`ZADD`
      over the passed shard key; `promote.lua` admit-time `ZREM` uses the stored `hb:{s}` (not the gsub of the
      lock key); `leave.lua` and `end_entry.lua` cleanup loops use `hb:{s}`/`hbstale:{s}` for those two members
      and keep the unsharded global `stale`/`pending`. `stale`/`pending`/`waiting` ordering sets stay global.
      Files: `services/queue/assets/lua/{join,heartbeat,liveness,promote,leave,end_entry}.lua`;
      `services/queue/internal/queue/infrastructure/valkey/queue_store.go` (pass shard keys/ARGV to each
      script; write/read the `shard` hash field);
      `services/queue/internal/queue/infrastructure/worker/liveness.go` (sweeper fans out `s=0..B-1`).
      Verify: `cd services/queue && go build ./... && go vet ./... && go vet -tags integration ./...` exit 0.

- [ ] 7. **(CHANGE 2, behavioural proof) Integration tests: sharded liveness correct and stranded-entry-free at B=16 and B=1.**
      Test-first: STALE/revive/end behaviour identical at B=1 and B=16 (revive restores original seq
      position); heartbeat routes to the right shard; **after `promote` admits, `ZSCORE hb:{s}`=nil for the
      stored shard; after `leave` and after `end_entry` (SWEEP_MISSED / ALREADY_ACTIVE / Booking-outcome)
      both `hb:{s}` and `hbstale:{s}` no longer contain the entry and the next sweep does not re-process the
      terminal entry (staled/ended counts do not drift)** — design §4.5 finding-9 check; run the same at B=1
      for parity.
      Files: new
      `services/queue/internal/queue/infrastructure/valkey/liveness_shard_integration_test.go` (build tag
      `integration`); the KEEP liveness tests (`TestLiveness_StaleReviveEnd` etc.) parameterised by B.
      Verify: `cd services/queue && go test -tags integration -race -count=1 -run 'TestLiveness|TestLivenessShard' ./internal/queue/infrastructure/valkey/`
      passes at B=1 and B=16 against real `aqb-valkey`.

- [ ] 8. **(CHANGE 2, load-test gate — REQUIRED, proves ≤ +20%) Parameterise `TestHeartbeatLoad` by `QUEUE_LIVENESS_SHARDS`; run B=1 and B=16 on real Valkey.**
      Keep the recorded dataset (100k WAITING entries, 3,333 heartbeats/s for 30 s). Measure promoter-tick p50
      at idle = base and under load = loaded, same build; report `loaded/base`. **Pass gate: B=16
      `loaded/base ≤ 1.20`, p99 ≤ 200 ms, error rate ≤ 0.1%.** B=1 must still show the old +26–38% regression
      (proves the test detects it). The test asserts the connected endpoint and aborts on `:6379`/
      `:7000–7005`.
      Files: `services/queue/internal/queue/infrastructure/valkey/heartbeat_load_integration_test.go`.
      Verify: `cd services/queue && RUN_HEARTBEAT_LOAD=1 QUEUE_LIVENESS_SHARDS=1 go test -tags integration -run TestHeartbeatLoad -timeout 20m ./internal/queue/infrastructure/valkey/`
      records the old regression, and the same with `QUEUE_LIVENESS_SHARDS=16` records `loaded/base ≤ 1.20`,
      both on `TEST_VALKEY_SERVER=…:16379`. **If `aqb-valkey` cannot start, record "real Valkey unavailable,
      +20% target NOT verified" in the evidence and the final report — never claim the target met.**

- [ ] 9. **(Wiring + full gate) Wire shard config through the composition root and run the Queue gate.**
      Pass `QUEUE_LIVENESS_SHARDS` from config into the store/sweeper constructors in
      `internal/delivery/queue_module.go`; confirm no `draw`/`WithDraw`/`DrawChunk` reference remains.
      Files: `services/queue/internal/delivery/queue_module.go` (and any `cmd/*` that constructs the
      promoter/store directly).
      Verify: `cd services/queue && go build ./... && go vet ./... && golangci-lint run --config ../../.golangci.yml ./... && go test -race -count=1 ./...`
      all exit 0; `go test -tags integration -race -count=1 ./...` passes on real `aqb-valkey`;
      `GO_TEST_TAGS=integration bash tools/check-coverage.sh services/queue 80` (from worktree root) passes;
      `make isolation` and `make contracts` pass.

- [ ] 10. **(Docs — WITHDRAWN markings + amendment) Update ADR/SRS/TP/specs/frontend doc for both changes.**
      ADR (`docs/analisa/05-ADR-RBT.md`): apply the ADR-030 Amendment block (design §7) — draw WITHDRAWN,
      `*Rank`→`*Seq`, load-test consequence rewritten to the sharded ≤ +20% gate; bump the Status line.
      SRS (`02-SRS-RBT-001.md`): `SRS-F-25-02` → strict FIFO by seq for ALL joins (remove opening-window
      text); `SRS-F-05-09` → "zona dalam entri atas **seq**" (no rename); any DRAWING-state line →
      **WITHDRAWN (draw dropped, owner)**.
      TP (`docs/analisa/06-TP-RBT-001.md`): `TC-ANON-20`, `TC-ANON-08`, `TC-ANON-09` → **WITHDRAWN**;
      `TC-ANON-19` row stays PASS but scenario **(e)** marked WITHDRAWN (the two deleted sub-tests);
      `TC-ANON-05` → sharded ≤ +20% gate, PASS only with the step-8 evidence (or NOT-VERIFIED). Keep the TC
      IDs with a WITHDRAWN status line (traceability, don't delete).
      specs/queue-service `requirements.md`: drop the "jendela acak pembukaan PROVISIONAL" clause and
      "zona berbasis rank" → "zona berbasis seq"; add a bullet that liveness `hb` is sharded for the
      promoter-tick budget.
      Frontend (`docs/analisa/08a-frontend-integration.md`): delete the `drawing:true`/`state:DRAWING` step;
      restore `guaranteedUntilSeq`/`possibleUntilSeq`/`headSeq`; drop `DRAWING` from the heartbeat-while states.
      Files: `docs/analisa/{05-ADR-RBT.md,02-SRS-RBT-001.md,06-TP-RBT-001.md,08a-frontend-integration.md}`,
      `specs/queue-service/requirements.md`.
      Verify: `grep -ri DRAWING docs/analisa specs/queue-service` shows only explicit WITHDRAWN/historical
      status lines (no live contract or field reference); `make contracts` still passes.

- [ ] 11. **(Evidence + ai-log — mandatory, folders never deleted) Record the session and load-test result.**
      Append to `docs/evidence/<YYYY-MM-DD>-anonymous-queue-booking.md`: the draw removal, the `0004`
      delete decision, the empty `grep -ri DRAWING services/queue` proof, and BOTH load-test runs (B=1
      regression oracle + B=16 ratio, p50/p95/p99, container identity `aqb-valkey` on `:16379`) — or the
      explicit "real Valkey unavailable, +20% target NOT verified" statement. Add an iteration entry to
      `docs/ai-log/README.md`.
      Files: `docs/evidence/<YYYY-MM-DD>-anonymous-queue-booking.md`, `docs/ai-log/README.md`.
      Verify: both files updated (`ls docs/evidence/*-anonymous-queue-booking.md` lists the file; the ai-log
      has the new entry); run `make ci` once end-to-end and record its exit code (pkg/* stages that need
      Postgres/RabbitMQ may be stated as unavailable per the brief, but Queue build/vet/lint/unit -race/
      integration -race/coverage≥80 and `make contracts`/`make isolation` must pass).

---

## Digest

Build/test commands (from `Makefile`, verified): gate `make ci = build lint cover isolation contracts`;
per-service `cd services/queue && go build ./... && go vet ./... && go vet -tags integration ./...`,
`golangci-lint run --config ../../.golangci.yml ./...`, `go test -race -count=1 ./...`,
`go test -tags integration -race -count=1 ./...`, `GO_TEST_TAGS=integration bash tools/check-coverage.sh services/queue 80`
(worktree root), `make mock`. Load test: `RUN_HEARTBEAT_LOAD=1 QUEUE_LIVENESS_SHARDS={1,16} go test -tags integration -run TestHeartbeatLoad`.
Valkey = out-of-band `aqb-valkey` on host `:16379` with `TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]'`; NEVER `:6379` or `:7000–7005`.
Baseline today (not yet run by this plan): worktree has 167 modified entries (prior approved work, uncommitted); `git -C <worktree>` for all reads; no commit/merge/stash.

Files touched (block → what changes → covering test):

- `services/queue/assets/lua/join.lua` — score `waiting`/`stale` by `seq`, drop `HSET rank`, seed `hb:{s}`, drop windowed branch (steps 1,6) → `liveness_shard_integration_test.go`, `TestAbuseSim`.
- `services/queue/assets/lua/heartbeat.lua` — delete `elseif st=='DRAWING'` arm; WAITING/revive on `hb:{s}`/`hbstale:{s}`; score by seq (steps 1,6) → `TestLiveness_StaleReviveEnd`, `liveness_shard_integration_test.go`.
- `services/queue/assets/lua/liveness.lua` — delete DRAWING STALE branch + end-loop DRAWING arm; sweep over passed shard key (steps 1,6) → `liveness_shard_integration_test.go` at B=1/B=16.
- `services/queue/assets/lua/claim.lua` — drop `or st=='DRAWING'` from NOT_ADMITTED guard (step 1) → existing claim tests.
- `services/queue/assets/lua/promote.lua` — trim DRAWING comment; admit-time `ZREM hb:{s}` from stored shard (steps 1,6) → shard admit check in `liveness_shard_integration_test.go`.
- `services/queue/assets/lua/leave.lua`, `end_entry.lua` — cleanup loop uses `hb:{s}`/`hbstale:{s}`, global `stale`/`pending` (step 6) → leave/end shard check in `liveness_shard_integration_test.go`.
- delete `services/queue/assets/lua/{draw_apply,draw_finalize}.lua` (step 1).
- `services/queue/internal/queue/infrastructure/valkey/queue_store.go` — `hashToEntry` drops `m['rank']` (F11:448 → seq unconditionally); join-result parse drops `res.Rank`; write/read `shard`; pass shard keys to all six scripts (steps 1,2,6) → valkey integration tests.
- `services/queue/internal/queue/domain/valueobject/{status,zone,anon_values}.go` — delete `StatusDrawing`/`Shuffle`; `ZoneForRank`→`ZoneForSeq`; clean NextHeartbeatMs comment (step 2) → `anon_values_test.go`, `status_zones_test.go` renamed to `*Seq`.
- `services/queue/internal/queue/domain/entity/{entry,event_settings}.go` — remove `Entry.Rank`, `EventSettings.OpeningWindowSeconds` + DRAWING comments (step 2) → mapper/zone unit tests.
- `services/queue/internal/queue/domain/dto/{status_document,entry_status,join_request}.go` — `*Rank`→`*Seq`, drop `Drawing` field + comments (step 2) → mapper tests.
- `services/queue/internal/queue/domain/mapper/entry_mapper.go` — drop `rankOf`/drawing branch, compute zone on seq (step 2) → `entry_mapper_test.go` enum row cleaned.
- `services/queue/internal/queue/application/usecase/heartbeat.go` — drop `res.Status != "DRAWING"` guard (line 51), use seq (step 2) → `my_entry_heartbeat_test.go`.
- `services/queue/internal/queue/application/usecase/my_entry.go` — F11:96 drop `!doc.Drawing` guard; F11:101 drop `StatusDrawing` disjunct (step 2) → `my_entry_heartbeat_test.go`.
- `services/queue/internal/queue/application/usecase/join.go` — F11:161 remove `StatusDrawing` special-case (always audit + `tryDirectAdmission`); drop `Windowed` (step 2) → `TestAbuseSim`, join unit tests.
- `services/queue/internal/queue/infrastructure/worker/{status_publisher,liveness}.go` — remove drawing/null-while-drawing branches + DRAWING comment; sweeper fans out shards (steps 2,6) → `status_zones_test.go`, `liveness_shard_integration_test.go`.
- delete `services/queue/internal/queue/infrastructure/worker/draw.go` (step 2).
- `services/queue/internal/queue/infrastructure/postgres/event_settings_repo.go` — drop `opening_window_seconds` column (step 2) → config/event-settings tests.
- `services/queue/internal/queue/port/outbound/{queue_store,types}.go`, `port/inbound/queue_usecase.go` — delete draw ports/`IDrawSource`/`PendingEntry`, `WaitingRankAt`→`WaitingSeqAt`, clean DRAWING comments (step 2) → regenerated `_mock/**`.
- `services/queue/internal/config/{queue,load,load_test}.go` — remove `DrawChunk`/`QUEUE_DRAW_CHUNK`; add `QUEUE_LIVENESS_SHARDS` (1..256, default 16) (steps 3,5) → `load_test.go`.
- delete `services/queue/deployments/migrations/0004_opening_window.{up,down}.sql` (step 3).
- new `pkg/queueshard/shard.go` (+`_test.go`) — `shard(entryID)=crc16(entryID)%B` (step 5) → `shard_test.go`.
- `services/queue/internal/queue/infrastructure/valkey/heartbeat_load_integration_test.go` — parameterise by `QUEUE_LIVENESS_SHARDS`, endpoint assert, B=1 oracle / B=16 ≤1.20 gate (step 8) → itself (the load gate).
- new `services/queue/internal/queue/infrastructure/valkey/liveness_shard_integration_test.go` — shard correctness + stranded-entry check at B=1/B=16 (step 7).
- delete `services/queue/internal/queue/infrastructure/{valkey,worker}/liveness_draw_integration_test.go`, `worker/opening_window_example_integration_test.go`; edit `worker/{status_zones_test,abuse_simulation_integration_test,fakestore_test,worker_test}.go`, `application/usecase/my_entry_heartbeat_test.go`, `domain/mapper/entry_mapper_test.go`, `domain/valueobject/anon_values_test.go` (step 4) → the suites themselves.
- `services/queue/internal/delivery/queue_module.go` — drop `WithDraw(...)`, wire `QUEUE_LIVENESS_SHARDS` (steps 2,9) → `go build`/isolation.
- `docs/analisa/{05-ADR-RBT.md,02-SRS-RBT-001.md,06-TP-RBT-001.md,08a-frontend-integration.md}`, `specs/queue-service/requirements.md` — WITHDRAWN markings + amendment, `*Rank`→`*Seq` wording, sharded gate (step 10) → `make contracts`, grep check.
- `docs/evidence/<date>-anonymous-queue-booking.md`, `docs/ai-log/README.md` — session + load-test evidence (step 11).

Completion gates: `grep -ri DRAWING services/queue` and `grep -ri Shuffle services/queue` empty (F11/F10/F12);
B=16 `loaded/base ≤ 1.20` on real `aqb-valkey` (CHANGE 2) or an explicit "NOT verified"; `make ci` green
(pkg/* Postgres/RabbitMQ stages may be stated unavailable per the brief; Queue build/vet/lint/unit/integration/
coverage≥80 + contracts + isolation must pass).
