# Design-Change Review (revision 4 target) — anonymous-queue-booking (CHANGE 1 + CHANGE 2)

Reviewer: design-review subagent (fresh read, verified against live Queue source in the worktree).
Target: `design.md` **revision 4** and its §7 ADR-030 amendment, at
`.worktrees/improve/anonymous-queue-booking/.agents/tasks/improve/anonymous-queue-booking/`.

This is a **revision**. The prior review (`design.change.review.json`, findings 1–10) is re-checked
first — findings 1–8 were already CLOSED, and findings **F9 (HIGH)** and **F10 (MEDIUM)** were the
open blockers. Revision 4 claims to resolve both. I verified those claims against the live Lua and Go
source. F9 and F10 are now **Closed**. However, a fresh repo-wide `grep -ri DRAWING services/queue`
(the acceptance gate the design itself commits to) surfaces a **new** gap the prior reviews and this
revision both miss: several *live* `DRAWING` code branches in Go files the §3 REMOVE list never names.
The design's own "grep … empty" gate is therefore unachievable by the edits it enumerates.

## Verdict: CHANGES_REQUESTED

Prior findings 1–10 are all **Closed** (F9/F10 verified resolved by revision 4). One NEW gap blocks
approval:

- **F11 [HIGH]** — the §3 acceptance gate "`grep -ri DRAWING services/queue` returns empty" cannot be
  met by the §3 REMOVE enumeration. A fresh grep finds live `StatusDrawing`/`doc.Drawing` **branches**
  (not comments) in `application/usecase/my_entry.go` (lines 96 and 101),
  `application/usecase/join.go` (line 161), and `infrastructure/valkey/queue_store.go` (`hashToEntry`,
  line 448) — none of which are named in §3. A coder following §3 literally leaves these in place; the
  stated gate then fails, or the branches silently survive (a leftover DRAWING branch the task
  forbids).

It is a concrete, bounded fix: add those three Go files (plus the residual DRAWING comments/test rows
the grep lists) to the §3 REMOVE enumeration, so the "grep empty" gate the design already commits to
is actually reachable.

---

## Re-check of prior findings

- **#1–#4 — CLOSED (Unchanged).** Target path, CHANGE 1 drafted, CHANGE 2 designed, KEEP/REMOVE
  boundary. Re-verified present in revision 4 §3/§4/§6.
- **#5 [MEDIUM] abuse test de-entangle — CLOSED (Unchanged).** §3 keeps the file, drops
  `WithDraw`/`fixedSeed`/`seed`, the `window`/`OpeningWindowSeconds` params, `join(id,windowed)` →
  `join(id)`, the `e.Rank` reads, and deletes only the two `TestAbuseSim_OpeningWindow*` sub-cases;
  §6 marks TC-ANON-19 scenario (e) WITHDRAWN with the row staying PASS. Matches the live test.
- **#6 [MEDIUM] load-test endpoint vs `:6379` — CLOSED (Unchanged).** §4.6 keeps the `aqb-valkey`
  (`valkey/valkey:8.1-alpine`, host 16379) out-of-band precondition, the test-only `TEST_VALKEY_SERVER`
  override, the fail-loud `:6379`/`:7000–7005` guard, and "container fails to start ⇒ NOT verified".
- **#7 [NIT] / #8 [NIT] — CLOSED (Unchanged).** §4.6 parameterises `TestHeartbeatLoad` by
  `QUEUE_LIVENESS_SHARDS` (B=1 oracle / B=16 gate) rather than "adding a sweep"; §8 keeps the
  `go build ./...` + `go vet -tags integration ./...` gate.
- **#9 [HIGH] shard fan-out omitted `promote.lua`/`leave.lua`/`end_entry.lua` — CLOSED (verified
  resolved).** Revision 4 §4.2 now enumerates **all six** scripts that mutate the liveness index and
  §4.4 gives per-script routing: `promote.lua` admit-time `ZREM` → `hb:{s}`, `leave.lua`/`end_entry.lua`
  use per-shard names for the `hb`/`hbstale` members of their `{hb,hbstale,stale,pending}` loop while
  `stale`/`pending` stay global, shard read from the stored `shard` on the entry hash; §4.5/§8 add a
  `B=16`+`B=1` behavioural check that admit/leave/end remove from the correct shard and no terminal
  entry is re-processed. **Verified against live source**: `promote.lua` line 35 (`ZREM` of the
  gsub-derived `hb`, comment "admitted entries leave the liveness set"); `leave.lua` line 19
  (`for _, z in ipairs({'hb','hbstale','stale','pending'}) do ZREM ARGV[4]..z`); `end_entry.lua`
  `dropSets()` (same loop); `heartbeat.lua` WAITING+STALE `ZADD P..'hb'`; `join.lua` `ZADD P..'hb'`
  seed; `liveness.lua` sweep over `P..'hb'`/`P..'hbstale'`. The six-script enumeration is accurate and
  the routing is feasible. ✓
- **#10 [MEDIUM] "no residual DRAWING branch" omitted `heartbeat.lua`/`claim.lua` — CLOSED (verified
  resolved, but see F11 for what it still misses).** §3 now deletes the `heartbeat.lua`
  `elseif st=='DRAWING'` arm (verified live at lines 26–29), drops `or st=='DRAWING'` from the
  `claim.lua` NOT_ADMITTED guard (verified live at line 10), and trims the `promote.lua` line-5 comment.
  The two Lua branches F10 named are now covered. ✓ The *enumeration is still incomplete for the Go
  side* — that is the new F11, not a re-open of F10 (F10 was scoped to the two Lua branches).

---

## New finding (revision 4)

### 11. [HIGH] The §3 "`grep -ri DRAWING services/queue` empty" acceptance gate is unachievable by the §3 REMOVE list — live Go DRAWING branches in `my_entry.go`, `join.go`, and `queue_store.go` are never named
**Where:** `design.md` §3 (acceptance check "`grep -ri DRAWING services/queue` returns empty") and §8
("No-residual-DRAWING gate") and §7 ADR ("`grep -ri DRAWING services/queue` is empty"); vs. a fresh
grep over the live tree.

**Problem.** The design commits to a single mechanical gate — the repo-wide grep must be empty — but
its §3 REMOVE enumeration names only a subset of the DRAWING sites. A fresh `grep -ri DRAWING
services/queue` returns **live code branches** in files §3 does not mention:

- **`application/usecase/my_entry.go` line 96** — `if !doc.Drawing && doc.GuaranteedUntilRank != nil
  && doc.PossibleUntilRank != nil {` (a live zone-emit guard keyed on `doc.Drawing`).
- **`application/usecase/my_entry.go` line 101** — `if ent.Status == valueobject.StatusWaiting ||
  ent.Status == valueobject.StatusStale || ent.Status == valueobject.StatusDrawing {` (a live
  heartbeat-states disjunct; the `StatusDrawing` arm is a real branch).
- **`application/usecase/join.go` line 161** — `if res.Outcome == outbound.JoinCreated &&
  ent.Status == valueobject.StatusDrawing {` (a live branch that suppresses direct admission for a
  window entry).
- **`infrastructure/valkey/queue_store.go` line 448** (`hashToEntry`) — `} else if e.Status !=
  valueobject.StatusDrawing {` (a live rank-backfill branch).

§3 names `status.go`, `join.lua`, `liveness.lua`, `heartbeat.go` (the `res.Status != "DRAWING"`
guard), `status_publisher.go`, `mapper.rankOf()`, the two Lua branches from F10, `StatusDocument.Drawing`,
`PendingEntries`, and the draw-only tests — but NOT `my_entry.go`, NOT `join.go` (this branch), and NOT
the `hashToEntry` branch in `queue_store.go`. So a coder who edits exactly what §3 lists will leave
four live `StatusDrawing`/`doc.Drawing` branches in place. Once `StatusDrawing` is deleted from
`status.go` (§3 does delete it), `my_entry.go:101`, `join.go:161`, and `queue_store.go:448` **stop
compiling** (undefined `valueobject.StatusDrawing`), and `my_entry.go:96` keeps a dead `doc.Drawing`
guard — so the §8 build gate (`go build ./...`) breaks on edits §3 never scheduled. Either way the §3
"grep empty" gate is not reachable from the §3 REMOVE list, and the task requirement "position math
and the status document have NO leftover DRAWING branches" is not guaranteed.

The grep also lists residual DRAWING **comments and test rows** the enumeration omits, which likewise
keep the gate non-empty: `entity/entry.go` lines 14 & 18; `dto/entry_status.go` lines 8, 10, 13;
`dto/status_document.go` line 9 (comment above the `Drawing` field §3 does delete); `dto/join_request.go`
line 19; `valueobject/anon_values.go` line 53 and `anon_values_test.go` line 117 (the `NextHeartbeatMs`
"DRAWING / unknown rank" comment + test — `NextHeartbeatMs` is KEPT, so these survive unless edited);
`mapper/entry_mapper_test.go` line 45 (`{valueobject.StatusDrawing, "DRAWING", false}` — a test row
that also stops compiling once the enum is gone); `worker/liveness.go` line 23;
`port/outbound/types.go` lines 23, 46, 63 and `port/outbound/queue_store.go` line 47 (doc comments on
`Rank`/`SweepLiveness`).

**Fix.** Make the §3 REMOVE list complete enough that the gate it promises can actually pass:
1. Add the four live Go branches and route them to plain FIFO:
   - `my_entry.go:101` — drop the `|| ent.Status == valueobject.StatusDrawing` arm (WAITING/STALE
     only); `my_entry.go:96` — drop the `!doc.Drawing &&` guard (the `Drawing` field is removed in §3,
     so the guard is both dead and a compile break).
   - `join.go:161` — remove the `ent.Status == valueobject.StatusDrawing` special-case so a
     `JoinCreated` entry always takes the normal audit + `tryDirectAdmission` path (there is no window
     entry anymore).
   - `queue_store.go:448` (`hashToEntry`) — collapse the `else if e.Status != valueobject.StatusDrawing`
     branch; with no DRAWING state every entry's rank backfills to `seq` unconditionally.
2. Add the residual DRAWING comments/test rows the grep lists (entity/dto/port doc comments, the
   `NextHeartbeatMs` comment+test, the `entry_mapper_test.go` enum row) to the "clean up / delete"
   scope, or explicitly state they are edited as part of the owning symbol's removal.
3. Keep the `grep -ri DRAWING services/queue` gate — but as written it is the *proof* the enumeration
   is complete, so the enumeration must list every hit the grep currently returns (or the hit's owning
   edit). Optionally add the same for the `*Rank` revert: `grep -ri '\bRank\b' services/queue` should
   show only the renamed `*Seq` symbols after §3's revert (see NIT 12).

### 12. [NIT] The `*Rank → *Seq` revert does not say what to do with the Lua `rank` hash field or the `ZADD waiting <rank>` scores
**Where:** `design.md` §3 ("Zone-math rename **reverted** `*Rank → *Seq`", "`entity.Entry.Rank`
removed"); vs. `join.lua` (`HSET ... 'rank', rank`, `ZADD P..'waiting', rank, id`), `heartbeat.lua`
(`HGET ek 'rank'`), `liveness.lua` (`HGET ek 'rank'`; `ZADD P..'stale', rank`), `queue_store.go`
`hashToEntry` (`m["rank"]`).

**Problem.** §3 reverts the Go-layer `*Rank` names to `*Seq` and removes `entity.Entry.Rank`, but the
Lua scripts and the Valkey hash still carry a `rank` field and score `waiting`/`stale` by it. With the
draw gone `rank == seq` always, so FIFO is preserved regardless — this is not a correctness bug — but
the design's stated intent ("the `*Rank` layer carries no information once the draw is gone") is not
fully carried through to the Lua/storage layer, and a reader may expect the `rank` hash field to also
go. Leaving it is defensible (internal key name), but the design should say so explicitly so the coder
does not either (a) leave a confusing dead `rank` field or (b) rip it out and change the `waiting`
score semantics. This is cosmetic and does not gate.

**Fix.** Add one line to §3: "the Valkey `rank` hash field and the `ZADD waiting/stale <rank>` scores
are LEFT AS-IS (they already equal `seq` for every entry once the draw is gone); only the Go-facing
`*Rank` symbols are renamed to `*Seq`." Or, if a clean removal is wanted, state that `join.lua` writes
`seq` directly and the `rank` field is dropped from the hash and from `hashToEntry`.

---

## Verified Assumptions (checked against live source in the worktree)
- **F9 six-script enumeration is accurate.** `promote.lua` line 35 `ZREM` of the gsub-derived `hb`
  with comment "admitted entries leave the liveness set"; `leave.lua` line 19
  `for _, z in ipairs({'hb','hbstale','stale','pending'}) do ZREM ARGV[4]..z`; `end_entry.lua`
  `dropSets()` identical loop (called from SWEEP_MISSED / ALREADY_ACTIVE / Booking-outcome paths);
  `heartbeat.lua` WAITING + STALE arms `ZADD P..'hb'`; `join.lua` `ZADD P..'hb'` seed; `liveness.lua`
  sweep over `P..'hb'`/`P..'hbstale'`. Exactly the six scripts §4.2/§4.4 route through shard keys. ✓
- **F10 two Lua DRAWING branches are live and now enumerated.** `heartbeat.lua` lines 26–29
  (`elseif st == 'DRAWING'`), `claim.lua` line 10 (`or st == 'DRAWING'`), `promote.lua` line 5 comment.
  §3 deletes all three. ✓
- `draw_apply.lua` / `draw_finalize.lua` exist (DRAWING-guarded) and are deleted by §3. ✓
- `join.lua` windowed branch (lines 94, 100: `status, rank = 'DRAWING', 0`; `ZADD P..'pending'`; the
  `drawn`/`rankseq` reads) is the one §3 deletes with the windowed branch; removing it also removes the
  `pending`/`drawn`/`rankseq` draw plumbing. ✓
- `valueobject/status.go` lines 20–21 (`StatusDrawing Status = "DRAWING"`) and line 50
  (`case StatusDrawing:` in `CanTransitionTo`) exist and are deleted by §3. ✓
- `liveness.lua` DRAWING branches (lines 25, 38) + the line-3 comment are the ones §3 removes. ✓
- `heartbeat.go:51` (`res.Status != "DRAWING"` guard), `status_publisher.go:109` (`doc.Drawing = true`),
  `mapper/entry_mapper.go:54,56` (`rankOf` DRAWING guard) exist and are named by §3. ✓

## Unverified / Wrong Assumptions
- **The design asserts (§3/§7/§8) that `grep -ri DRAWING services/queue` will be empty after the §3
  edits, but the §3 REMOVE list omits live DRAWING branches in `my_entry.go` (96, 101), `join.go`
  (161), and `queue_store.go` `hashToEntry` (448), plus residual DRAWING comments/test rows. The gate
  is therefore not reachable from the enumerated edits, and deleting `StatusDrawing` (which §3 does)
  breaks `go build` on the un-enumerated references.** → Finding 11.
- The `*Rank → *Seq` revert's reach into the Lua `rank` hash field and `waiting`/`stale` scores is
  unspecified; harmless for FIFO but ambiguous for the implementer. → Finding 12 (NIT).
- Not independently re-verified this pass (relied on prior review, which checked against source):
  the non-existence of `crc16` / `QUEUE_LIVENESS_SHARDS`, the `QUEUE_DRAW_CHUNK` config removal
  surface, the `TEST_VALKEY_SERVER` defaults in Makefile/CI/compose, and that migration
  `0004_opening_window` is untracked. These underpin F9's shard work, §4.6's precondition and §5's
  migration decision; the prior review marked them verified and nothing in revision 4 contradicts them.

---

## Summary
Prior findings 1–10: all CLOSED (F9/F10 independently verified resolved against the live Lua/Go
source this pass). New finding: **1 HIGH (F11)**, **1 NIT (F12)**. HIGH+MEDIUM = 1 > 0 ⇒
**CHANGES_REQUESTED**. F11 is tight: the design already commits to a mechanical
`grep -ri DRAWING services/queue` = empty gate, so make the §3 REMOVE list enumerate every hit that
grep returns today — in particular the live branches in `my_entry.go` (96/101), `join.go` (161), and
`queue_store.go` `hashToEntry` (448), which otherwise both fail the gate and break `go build` once
`StatusDrawing` is deleted. Address F11 (and optionally the F12 NIT on the Lua `rank` field) and
re-request review.
