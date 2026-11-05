# Design-change note — anonymous-queue-booking (CHANGE 1 + CHANGE 2)

Status: revision 4 (addresses `design.change.review.json`, verdict CHANGES_REQUESTED; prior findings
1–8 CLOSED, now resolving the two new findings F9/F10: the `hb`/`hbstale` shard fan-out that omitted
`promote.lua`/`leave.lua`/`end_entry.lua`, and the two live `DRAWING` branches
(`heartbeat.lua`/`claim.lua`) the "no residual DRAWING branch" guarantee had missed).
Scope: a SHORT design *delta* over the already-approved anonymous-queue-booking implementation (all
uncommitted in this worktree). It covers ONLY the two changes the owner rejected at the human gate,
plus the doc/test updates those two changes force. Everything the owner approved stays exactly as
built; see the KEEP list in §3.

Authoritative inputs read: `anonymous-queue-booking-brief.md` (incl. the addendum and the owner's
final message "drop random draw. slowndown arent alllowed"), the current ADR-030 in
`docs/analisa/05-ADR-RBT.md` (§796), `docs/evidence/2026-11-04-anonymous-queue-booking.md`,
`docs/ai-log/README.md`, and the live Queue code (`services/queue/...`).

This note is implementable as-is: it names every file to delete/edit, the exact symbols to remove or
rename, and the load-test protocol with its pass gate. The companion ADR-030 amendment is §7.

---

## 1. The two changes (owner decisions, final)

**CHANGE 1 — DROP the opening-window random draw entirely.** Revert to plain FIFO-by-seq for ALL
joins, in and out of any window. Remove the draw mechanism, the `DRAWING` state and its null-field
contract, the related config and the Queue migration `0004_opening_window`, and mark the
DRAWING-state requirements and TC-ANON-20 (plus the other draw-only TCs) WITHDRAWN. The draw was only
ever "proposed, not objected to" (decision 8 / OQ-1); the owner has now rejected it. Removing the
complexity is preferred over keeping it behind a disabled flag.

**CHANGE 2 — FIX the promoter-tick slowdown; do not defer it.** The heartbeat load test recorded the
promoter tick at 1.26–1.38× base (+26–38%) against the required ≤ +20%; the fix (sharding) was logged
as "not built". This note designs heartbeat/liveness **sharding of the `hb` index** so the per-tick
cost falls within +20% of base, and REQUIRES proving it with the load test on the project-dedicated
Valkey container — never Redis `:6379` or the cluster `:7000–7005`. The ≤ +20% target is never
claimed met without that evidence; if real Valkey is unavailable the note (and the final report) must
say so explicitly rather than claim the target met.

---

## 2. Current state these changes act on (verified, not assumed)

- The draw is wired through the promoter: `Promoter.WithDraw(NewDraw(...))` and
  `Promoter.windowGate(...)` gate admission while `now < salesStartsAt + W` and run the draw once, and
  the status publisher and `me` go `null`/`drawing:true` until `IsDrawn` (`status_publisher.go
  ranks()`, `heartbeat.go`, `mapper/entry_mapper.go rankOf()`).
- The zone math was renamed from `*Seq` to `*Rank`. Integration test `TestJoin...` pins `W=0: rank =
  seq`, i.e. **with no draw, rank is identical to seq** for every entry. So the `*Rank` layer carries
  no information once the draw is gone; it is pure draw-entanglement.
- Liveness uses ONE global sorted set `hb` per event (`liveness.lua`, key `P..'hb'`). The promoter's
  `SweepLiveness` runs `ZRANGEBYSCORE P..'hb' -inf (staleBefore LIMIT 0 batch` every tick on that one
  key. Heartbeats `ZADD` the same key. The load-test evidence states the cause of the slowdown
  precisely: "Valkey satu thread melayani 3.333 skrip/dtk" — heartbeats and the sweep contend on one
  key served by a single Valkey thread. This is exactly what sharding relieves.
- Config: `QUEUE_DRAW_CHUNK` (`config/queue.go` Liveness.DrawChunk, default 2000, validated 1..10000),
  `event_settings.opening_window_seconds` (`entity.EventSettings.OpeningWindowSeconds`), Queue
  migration `0004_opening_window.{up,down}.sql`.

---

## 3. KEEP / REMOVE boundary (resolves finding #4)

"Everything else" is NOT left to interpretation. Iteration 2 bundled the draw with a `*Seq→*Rank`
rename of the zone math; that rename is draw-entangled and is REVERTED. Every other mechanism the
owner approved is RETAINED unchanged.

### REMOVE (draw-only; delete or revert)
- `services/queue/internal/queue/infrastructure/worker/draw.go` — the `Draw` type, `CryptoSeed`,
  `Gate`, `run`, `computeOrder`. Delete the file.
- `services/queue/assets/lua/draw_apply.lua`, `draw_finalize.lua` — delete.
- `services/queue/internal/queue/domain/valueobject/status.go`: delete `StatusDrawing` and its
  `CanTransitionTo` case; remove `DRAWING` from any set/branch.
- `valueobject.Shuffle` (seeded Fisher-Yates/ChaCha8) and its tests `anon_values_test.go`
  `TestShuffle_*` — delete; nothing else calls `Shuffle` once the draw is gone (verify with a
  repo-wide search before deleting).
- Zone-math rename **reverted** `*Rank → *Seq`: `dto.StatusDocument.{HeadRank,GuaranteedUntilRank,
  PossibleUntilRank}` → `{HeadSeq,GuaranteedUntilSeq,PossibleUntilSeq}`; `dto.EntryStatus.Rank/Zone`
  stay but are computed on seq; `valueobject.ZoneForRank` → `ZoneForSeq` (same formula, param renamed);
  `IQueueStore.WaitingRankAt` → `WaitingSeqAt`; `entity.Entry.Rank` removed (seq is the order).
- `IQueueStore` draw methods: `IsDrawn`, `PendingEntries`, `EnsureDrawSeed`, `GetDrawOrder`,
  `WriteDrawOrder`, `DrawProgress`, `DrawApply`, `DrawFinalize`, and the `IDrawSource` port — delete
  from `port/outbound`, their Valkey implementations in `queue_store.go` (`drawApply`, `drawFinalize`
  Lua handles), and regenerate mocks (`make mock`). `PendingEntry` struct deleted if unused elsewhere.
- `Promoter.WithDraw`, `Promoter.windowGate`, the `draw *Draw` field, and the `WithDraw(...)` call in
  `delivery/queue_module.go`. The promoter admits strict FIFO every tick with no gate.
- `StatusDocument.Drawing` field and every `drawing`/`null-while-drawing` branch in
  `status_publisher.go ranks()`, `mapper.rankOf()`, `heartbeat.go` (the `res.Status != "DRAWING"`
  guard), and the `Entry.Stale`-vs-`DRAWING` handling.
- **Live `DRAWING` branches in the Lua scripts (resolves finding #10).** The "no residual DRAWING
  branch" guarantee was incomplete — two scripts still carry a live `DRAWING` arm that must be deleted:
  - `heartbeat.lua` lines 26–29: delete the whole `elseif st == 'DRAWING'` arm (the `hbstale`→`hb`
    revive + `HSET ... 'stale','0'`). After this the revive logic handles ONLY `STALE → WAITING`; a
    status that can no longer occur must not keep a code path. (This script is ALSO edited by CHANGE 2
    §4.4 to shard its `hb` write — make both edits in one pass; see §4.4's heartbeat note.)
  - `claim.lua` line 10: drop `or st == 'DRAWING'` from
    `if st == 'WAITING' or st == 'STALE' or st == 'DRAWING' then return {'ERR','NOT_ADMITTED'}` so the
    NOT_ADMITTED guard reads `st == 'WAITING' or st == 'STALE'` only.
  - `promote.lua` line 5 comment `-- ... STALE/DRAWING dilewati ...` is cosmetic (not a branch), but
    trim `DRAWING` from it too so the audit below is clean.

  Taken together with the `status.go`/`join.lua`/`liveness.lua`/Go-layer removals above, the status
  doc and position math MUST have NO residual `DRAWING` branch after this change. **Acceptance check
  (explicit guarantee for findings #2 and #10): `grep -ri DRAWING services/queue` returns empty**
  (parallel to the `Shuffle` repo-search rule). Any surviving hit is a defect to fix before the gate.
- Config: `QUEUE_DRAW_CHUNK` (`config/queue.go` field `DrawChunk`, `load.go` default, `queue.go`
  validation, `load_test.go` assertions) — delete.
- `event_settings.opening_window_seconds`: see §5 for the migration decision (neutralize, not edit the
  applied file). `entity.EventSettings.OpeningWindowSeconds`, the repo column list in
  `event_settings_repo.go`, and `join.go`'s `Windowed:` param and `JoinParams.Windowed` / the
  `join.lua` windowed branch — delete.
- Liveness `liveness.lua`: delete the `elseif st == 'DRAWING'` STALE branch and the `DRAWING` arm of
  the end loop; the comment "live WAITING/DRAWING" becomes "live WAITING". (Sharding of the same file
  is CHANGE 2, §4.)
- Tests that assert draw behaviour: `liveness_draw_integration_test.go TestDraw_*`,
  `opening_window_example_integration_test.go` (TC-ANON-20), `status_zones_test.go
  TestStatusPublisher_DrawingPublishesNulls`, `my_entry_heartbeat_test.go
  TestGetMyEntry_DrawingHasNullRankAndZone` and the "drawing: null rank" subtest — delete. Rename the
  remaining `*Rank` assertions to `*Seq` (they keep their coverage because rank==seq today).
- `services/queue/internal/queue/infrastructure/worker/abuse_simulation_integration_test.go`
  (TC-ANON-19) — **de-entangle, do NOT delete** (resolves finding #5). The abuse behaviour is KEEP, so
  the file stays and its FIFO scenarios keep running; only its draw coupling is removed. Concretely:
  - Drop the `WithDraw(worker.NewDraw(store, fixedSeed(seed), 500))` call on the promoter in `newSim`
    (the `seed` param and the local `fixedSeed` helper become unused — remove them from `newSim`'s
    signature and every caller; delete `fixedSeed` if nothing else uses it after the draw removal).
  - Remove the `window int` param to `newSim` and the `OpeningWindowSeconds: window` field from the
    `fakeSettings` it builds (the field itself is deleted in the `EventSettings` REMOVE above).
  - Change `func (s *sim) join(id string, windowed bool)` to `func (s *sim) join(id string)`; drop the
    `Windowed: windowed` field from the `JoinParams` it fills and update every call site to the
    one-arg form (the many `s.join(ghostID(i), false)` / `s.join("real", false)` calls lose the
    `false`; the windowed `true` calls only appear in the draw-only sub-cases removed next).
  - Replace the `e.Rank == 1` / `e.Rank` reads (which only existed to assert draw order) — the kept
    sub-cases assert on admission *order by seq* and on *who* is admitted, not on a `Rank` field, which
    is removed with the zone-rename revert.
  - **Mark these two sub-cases WITHDRAWN and delete them** (draw-only, resolves finding #5(b) — they
    test the opening window itself, which no longer exists): `TestAbuseSim_OpeningWindowGhostsAndRealBuyer`
    and `TestAbuseSim_OpeningWindowRealBuyerDrawnFirst`. They are recorded under TC-ANON-19's
    opening-window scenario (e); §6 marks that scenario WITHDRAWN. The remaining sub-cases
    (a)/(a2)/(b)/(c)/(d) — heartbeating/claiming ghosts bound stock, real buyer ahead/behind, STALE
    ghosts skipped — are pure FIFO and KEEP, de-entangled per the bullets above.

### KEEP (approved; unchanged by this note)
- Anonymous join (`join.lua`, per-visitor and per-account dedup, re-attach, associate,
  `Idempotency-Key`, 200-vs-201).
- Heartbeat + STALE/revive/ENDED `STALE_TIMEOUT` liveness (the sweeper under the promoter lease; an
  admitted entry never in the liveness set). Its *storage layout* changes in CHANGE 2 (sharded `hb`),
  but the behaviour — STALE after 90 s, revive at the original position, ENDED after 300 s only
  without an allocation — is unchanged and its tests keep passing.
- 3-units-per-admission (`entryUnits = 3`, `outstanding_units`, partial grant, `max_tickets_per_order`
  clamp, jumper adoption).
- NIK-at-hold, the hold-accounting fix (`outstanding_units` consumed from the admission's own
  reservation only), `422 BUYER_NIK_LIMIT_REACHED` with the admission kept, the NIK attempt
  budget/throttle.
- Visitor-token generation + device claim (HMAC `X-Device-Id`, generation compared on join/heartbeat/
  me/claim/leave).
- `me` ENDED with a machine-readable `reason` (LEFT, STALE_TIMEOUT, CLAIM_WINDOW_EXPIRED,
  JOURNEY_ENDED), 404 only for a never-existing / other-principal entry.
- Zones remain **entry-count over order** (G = ceil(free/3), P = ceil((free+allocated+held)/3)); only
  the field names revert to seq. The empty-zone = 0 and Booking-down = keep-last-boundary rules stay.

---

## 4. CHANGE 2 — heartbeat/liveness `hb` sharding (chosen approach)

### 4.1 Why sharding, and why it is sufficient
The slowdown is contention on a single key. 3,333 heartbeats/s each `ZADD` the one `hb` key while the
promoter's per-tick `SweepLiveness` runs a `ZRANGEBYSCORE` + per-id `ZREM`/`ZADD` on the same key,
all serialized by Valkey's single command thread for that key. Splitting `hb` into `B` shard keys
(`hb:0 … hb:{B-1}`) with each entry routed by a stable hash spreads both the heartbeat writes and the
sweep reads across `B` independent keys. Per-tick sweep cost per shard drops to ~1/B of today's, and
heartbeat writes stop queuing behind the sweep's long range scan. Alternatives considered and
rejected: (a) *lower heartbeat frequency* — already rate-limited (§3 KEEP); pushing it lower hurts
turn-soon detection and does not remove the single-key contention; (b) *move liveness off the
promoter lease into its own worker* — does not reduce the per-key serialization, just moves it; (c)
*Valkey cluster* — the project is a single dedicated container (tech steering: `VALKEY_SERVER`), and
the brief forbids using the unrelated cluster `:7000–7005`. Sharding one logical index across keys on
one node is the minimal change that attacks the measured cause, and is exactly the fallback the prior
design already named (`crc16(entryId) % B`).

### 4.2 Shard key and count
- Shard key: `shard(entryID) = crc16(entryID) % B`, computed identically in Go and Lua. Lua: use the
  entry id bytes through a crc16 helper embedded in the script (or a precomputed shard passed as an
  ARGV from Go, which is simpler and avoids a Lua crc16 — see 4.4). Go: `pkg`-level helper so the
  sweeper, the heartbeat path, and join all agree.
- `B` is config `QUEUE_LIVENESS_SHARDS` (new; default 16; validated 1..256). `B=1` reproduces today's
  single-key behaviour exactly (safe fallback / A-B baseline). The sweep fans out over all `B` shards
  each tick under the same promoter lease; the lease still fences the whole fan-out.
- All liveness keys become per-shard: `hb` → `hb:{s}`, `hbstale` → `hbstale:{s}`. The ordering sets
  `waiting`/`stale`/`pending` are UNCHANGED (ordering is global by seq; only the liveness/last-signal
  index shards).
- **Every script that mutates the liveness index is in scope — not just the three obvious ones
  (resolves finding #9).** Six live scripts touch `hb`/`hbstale`; ALL must route through the entry's
  shard key, or a shard-unaware `ZREM` silently no-ops and strands the entry in its shard:
  - `heartbeat.lua` — `ZADD hb` / `ZREM hbstale` on heartbeat/revive. (§4.4)
  - `join.lua` — seeds the liveness entry with `ZADD hb`. (§4.4)
  - `liveness.lua` — the sweep `ZRANGEBYSCORE`/`ZREM`/`ZADD` over `hb` and `hbstale`. (§4.3)
  - `promote.lua` line 35 — `ZREM (…'hb') entryId` when an entry is admitted ("admitted entries leave
    the liveness set"). MUST become `ZREM hb:{s}`; otherwise an ADMITTED entry stays in `hb:{s}` and
    the next sweep re-processes it, violating the KEEP invariant "an admitted entry is never in the
    liveness set." (§4.4)
  - `leave.lua` line 19 — `for z in {'hb','hbstale','stale','pending'} do ZREM ARGV[4]..z, id`. (§4.4)
  - `end_entry.lua` line 9 (`dropSets()`) — the same `{'hb','hbstale','stale','pending'}` ZREM loop,
    called from the SWEEP_MISSED, ALREADY_ACTIVE and Booking-outcome paths. (§4.4)

  For `leave.lua`/`end_entry.lua`, the loop set `{'hb','hbstale','stale','pending'}` must use the
  **per-shard names for the `hb`/`hbstale` members** (`hb:{s}`, `hbstale:{s}`) while `stale`/`pending`
  stay the unsharded global ordering sets. If these three are left removing from the unsharded
  `P..'hb'`/`P..'hbstale'` (keys that no longer receive writes), the `ZREM`s match nothing: ADMITTED
  entries linger in `hb:{s}` (KEEP-invariant break) and LEFT/ENDED entries linger so the sweep keeps
  re-processing terminal entries and the `queue_entries_stale`/self-heal counts drift. This would make
  CHANGE 2 a regression, not a fix.

### 4.3 Promoter tick fan-out
`SweepLiveness` iterates `s = 0..B-1`, running the (sharded) `liveness.lua` once per shard with
`P..'hb:'..s` / `P..'hbstale:'..s`, each with its own `LIMIT 0 batch`. The lease token is checked
once at the start of each script call (fence unchanged). Because each call touches only one shard key
plus the per-entry hashes it already touched, total work per tick is the same count of entries but
spread across `B` keys, so no single key blocks heartbeats for the whole scan. `batch` stays
per-shard (so total swept per tick can be up to `B*batch`; document this and keep `batch` default
such that `B*batch` ≈ today's throughput so memory/latency per call stays bounded).

### 4.4 Liveness write paths (all six scripts, resolves finding #9)
Chosen mechanism: **Go computes the shard and passes `hb:{s}` / `hbstale:{s}` as KEYS/ARGV** to every
liveness script. This keeps crc16 in one Go home (no Lua crc16 to test separately), and the scripts
stay key-agnostic. The entry hash stores its shard at join (`HSET entry:{id} shard {s}`) so any path
that mutates the liveness index — including the sweep's self-heal, revive, admit, leave and end —
addresses the right shard by **reading the stored `shard` rather than recomputing** crc16.

Per-script routing (matching the §4.2 enumeration):
- `heartbeat.lua` — the `ZADD P..'hb'` (WAITING) and the `STALE → WAITING` revive's `ZREM P..'hbstale'`
  + `ZADD P..'hb'` take the entry's `hb:{s}`/`hbstale:{s}`. (After the §3 F10 edit this script has
  only the WAITING and STALE arms; make the shard edit on those two arms.)
- `join.lua` — the liveness seed `ZADD` takes `hb:{s}` for the new entry's shard.
- `liveness.lua` — see §4.3 (sweep fans out over all shards).
- `promote.lua` — the admit-time `ZREM` currently derives `hb` from the lock key via `string.gsub`;
  replace that with the `hb:{s}` key Go passes for the candidate's stored shard, so an admitted entry
  is removed from the shard it actually lives in.
- `leave.lua` / `end_entry.lua` — Go passes the entry's `hb:{s}`/`hbstale:{s}`; the cleanup loop uses
  those per-shard names for the `hb`/`hbstale` members and the unsharded `stale`/`pending` for the
  other two (ordering sets are global).

Because the shard is persisted on the entry hash, a terminal/admit path never needs the visitor's
live crc16 input and cannot pick the wrong shard after a revive moved the entry around.

### 4.5 Invariants preserved
Sharding changes only *where* a last-signal score is stored, never the liveness rules. STALE/revive/
end thresholds, the "never end an entry holding an allocation" guarantee, and the lease fence are
untouched. Revive still restores the entry at its original seq-position (the `waiting`/`stale` sets
are unsharded). This is a pure storage-layout change; the behavioural tests in the KEEP list must
pass unchanged against `B=16` and `B=1`.

**Shard-removal behavioural check (resolves finding #9).** Add an integration assertion at `B=16`
that each liveness-index mutation removes the entry from the *correct* shard, so a stranded-entry
regression is caught before the load test: (a) after `promote` admits an entry, `ZSCORE hb:{s}` for
its stored shard is nil (admitted entry not in the liveness set); (b) after `leave` and after
`end_entry` (SWEEP_MISSED / ALREADY_ACTIVE / Booking-outcome), both `hb:{s}` and `hbstale:{s}` for the
entry's shard contain it no more, and a following sweep does not re-process the terminal entry
(`staled`/`ended` counts do not drift). Run the same assertions at `B=1` to prove parity with today's
single-key behaviour.

### 4.6 Load-test protocol and pass gate (REQUIRED; resolves finding #3)
The target is PROVEN, not asserted. Reuse `TestHeartbeatLoad`
(`internal/queue/infrastructure/valkey/`, `RUN_HEARTBEAT_LOAD=1`). Its `tick()` already acquires the
promoter lease, reads top-200, and calls `SweepLiveness(... batch 1000)`; the real CHANGE-2 delta is
NOT adding a sweep but routing that sweep through the sharded keys and running the A/B comparison
(resolves finding #7): **parameterise `TestHeartbeatLoad` by `QUEUE_LIVENESS_SHARDS` and run it at
`B=1` (the +26–38% regression oracle) and `B=16` (the ≤+20% gate); the tick's existing `SweepLiveness`
call fans out over the `B` shard keys and the heartbeat workers `ZADD` their entry's shard key.**

- **Setup precondition (MANDATORY — resolves finding #6).** The repo default `TEST_VALKEY_SERVER`
  (Makefile line 15 and `.github/workflows/ci.yml` line 223) is `[{"host":"localhost","port":6379}]`,
  and the `docker-compose.yaml` `valkey` service also binds `6379` — but on THIS machine `localhost:6379`
  is an **unrelated project's Redis** and `:7000–7005` is another project's cluster; both are FORBIDDEN
  by the brief. The project-dedicated Valkey is therefore an **out-of-band container, not a compose
  service**, and must be started explicitly for this run:
  1. Start it: `docker run -d --name aqb-valkey -p 16379:6379 valkey/valkey:8.1-alpine`
     (image and host port 16379 exactly as the prior evidence `docs/evidence/2026-11-04-anonymous-queue-booking.md`
     §1 records).
  2. Override the env for the test process ONLY — do not change the Makefile/CI default:
     `TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]'`. Running without this override targets
     the forbidden `:6379` Redis and MUST NOT be used.
  3. The test asserts the endpoint it connected to and **fails loudly** if it resolves to `:6379` or
     `:7000–7005`, so a forgotten override aborts rather than measuring the wrong server.
- Dataset: 100,000 WAITING entries, 3,333 heartbeats/s for 30 s (same as the recorded run, so the
  before/after is comparable).
- Comparison method: measure promoter-tick p50 at idle (no heartbeat load) = **base**, then under the
  3,333/s heartbeat load = **loaded**, for the SAME build. Report `loaded/base`. Run both `B=1`
  (reproduces the recorded +26–38%, the regression oracle) and `B=16`.
- **Pass gate: with `B=16`, `loaded/base ≤ 1.20` (≤ +20%)**, with p99 ≤ 200 ms and error rate ≤ 0.1%
  retained (both already pass). The `B=1` run must still show the old regression, proving the test can
  detect it (otherwise the test is not measuring the right thing).
- Evidence: record both runs (ratios, p50/p95/p99, container identity) in
  `docs/evidence/<date>-anonymous-queue-booking.md` and flip TC-ANON-05 to PASS only when the `B=16`
  ratio is ≤ 1.20 on real `aqb-valkey`. **If `aqb-valkey` cannot be started (resolves finding #6's
  unavailable case), the evidence and the final report MUST state "real Valkey unavailable, +20%
  target NOT verified" — the target is never claimed met without the measured ratio on the dedicated
  container.**

---

## 5. Migration `0004_opening_window` — neutralize, do not edit an applied file

Steering (structure): *"file migrasi yang sudah diterapkan tidak boleh diubah"*. Decision: add a NEW
forward migration `services/queue/deployments/migrations/0005_drop_opening_window.{up,down}.sql` that
`ALTER TABLE event_settings DROP COLUMN IF EXISTS opening_window_seconds;` (up) and re-adds it with
the old default (down, for reversibility). Rationale over the alternatives: editing `0004` is
forbidden; leaving the column as dead metadata invites drift and keeps `OpeningWindowSeconds` alive in
the entity/repo. A clean forward drop is the only option consistent with "applied migrations are
immutable" and "no leftover DRAWING branches". If `0004` has NOT yet been applied to any shared
environment (it is uncommitted in this worktree), the simpler path is to delete `0004_opening_window.
{up,down}.sql` outright; since all work here is uncommitted and pre-merge, **delete `0004` directly**
and skip `0005`. (Pick delete-0004; it is uncommitted. The `0005` drop is the fallback only if the
column has already shipped — state which applies in the evidence.)

---

## 6. Doc + test updates the two changes force (WITHDRAWN markings, resolves finding #2)

All in the worktree copies under `docs/analisa/` and `specs/`:

- **ADR (`docs/analisa/05-ADR-RBT.md`)**: amend ADR-030 per §7 — strike decision (8) opening-window
  draw, change the zone-rename note "`*Seq` → `*Rank`" back to `*Seq`, and rewrite the Consequence
  about the load test from "GAGAL (1.26–1.38×), sharding not built" to the sharded design + the ≤+20%
  gate proven on `aqb-valkey`. The ADR-030 table row (line 48) traceability stays.
- **SRS (`02-SRS-RBT-001.md`)**: `SRS-F-25-02` delta row — remove the opening-window PROVISIONAL text,
  state strict FIFO by seq for ALL joins. `SRS-F-05-09` delta — change "field `*Rank` menggantikan
  `*Seq`" to "zona dalam entri atas **seq**" (no rename). Any SRS line that introduced a DRAWING state
  → mark **WITHDRAWN (draw dropped, owner 2026-11-xx)**.
- **TP (`06-TP-RBT-001.md`)**: `TC-ANON-20` (`TestOpeningWindowExample_VXY`) → **WITHDRAWN**;
  `TC-ANON-08` (draw fairness Spearman/chi-square) → **WITHDRAWN**; `TC-ANON-09` (W=0/DRAWING/late-
  joiner/resumable draw) → **WITHDRAWN**. `TC-ANON-19` (abuse simulation): the row stays PASS and the
  TC is KEPT, but its **opening-window scenario (e)** — covered by the deleted sub-tests
  `TestAbuseSim_OpeningWindowGhostsAndRealBuyer` / `TestAbuseSim_OpeningWindowRealBuyerDrawnFirst` — is
  marked **WITHDRAWN (draw dropped)**; scenarios (a)/(a2)/(b)/(c)/(d) remain PASS under FIFO.
  `TC-ANON-05` row: change the result from "tick promotor
  ≤+20% GAGAL (1,26–1,38×)" to the sharded ≤+20% gate, PASS only with evidence (§4.6). Keep the TC
  IDs with a WITHDRAWN status line (don't silently delete — traceability).
- **specs/queue-service/requirements.md**: the two summary bullets (lines 365–368) drop the "jendela
  acak pembukaan PROVISIONAL" clause and the "zona berbasis rank" phrasing → "zona berbasis seq";
  add a bullet that liveness `hb` is sharded for the promoter-tick budget.
- **Frontend integration (`08a-frontend-integration.md`)**: delete step 5 (`drawing:true` / `state:
  DRAWING`); in steps 4 and 7 the fields are `guaranteedUntilSeq`/`possibleUntilSeq`/`headSeq` again;
  step 3 drops `DRAWING` from the heartbeat-while states.
- **ai-log (`docs/ai-log/README.md`)** + **evidence**: a new iteration entry recording the draw
  removal and the sharding fix with its load-test result (or the explicit "Valkey unavailable"
  statement). Per the testing-review steering, log this session and add the
  `docs/evidence/YYYY-MM-DD-<topik>.md` entry; neither folder is deleted.

---

## 7. ADR-030 amendment (the standalone design-change the review asked for)

Append to `docs/analisa/05-ADR-RBT.md` ADR-030 as an **Amendment (2026-11-xx, owner gate)** block:

> **Amendment — draw dropped, promoter-tick slowdown fixed (supersedes decision 8 and the load-test
> consequence of ADR-030).**
>
> 1. **Opening-window random draw WITHDRAWN.** Decision (8) is withdrawn by the owner ("drop random
>    draw"). All joins are strict FIFO by `seq`, in or out of any window. The `DRAWING` state, the
>    seeded draw (`draw.go`, `draw_apply.lua`, `draw_finalize.lua`, `Shuffle`), the `*Rank` zone
>    rename (reverted to `*Seq`), `QUEUE_DRAW_CHUNK`, and `event_settings.opening_window_seconds`
>    (migration `0004_opening_window`) are removed. Human-gate item (a) and (f)-draw are closed as
>    WITHDRAWN. No `DRAWING` branch remains in the status document or position math — including the
>    `heartbeat.lua` `elseif st=='DRAWING'` revive arm and the `claim.lua` `or st=='DRAWING'`
>    NOT_ADMITTED disjunct, both deleted; `grep -ri DRAWING services/queue` is empty.
> 2. **Promoter-tick slowdown fixed by sharding `hb`, not deferred.** The liveness last-signal index
>    `hb`/`hbstale` is sharded into `B` keys (`hb:{crc16(entryId)%B}`, config `QUEUE_LIVENESS_SHARDS`
>    default 16); the promoter sweep fans out over the `B` shards under the same lease. ALL six scripts
>    that mutate the liveness index — `heartbeat.lua`, `join.lua`, `liveness.lua`, `promote.lua`
>    (admit-time `ZREM`), `leave.lua`, `end_entry.lua` — route through the entry's per-shard key (read
>    from the stored `shard` on the entry hash), so no admitted/left/ended entry is ever stranded in a
>    shard. Target: the promoter tick stays ≤ +20% of base under the 100k-entry / 3,333-hb/s load. This is PROVEN on the
>    project-dedicated Valkey container (`aqb-valkey`, never Redis `:6379` or cluster `:7000–7005`);
>    the ≤ +20% claim is only made with the measured `loaded/base ≤ 1.20` evidence, and absence of real
>    Valkey is stated explicitly rather than claimed met. Human-gate item (f)-slowdown is closed as
>    FIXED+PROVEN (or NOT-VERIFIED if Valkey was unavailable).
>
> Everything else in ADR-030 (anonymous join, quantity guarantee, NIK-at-hold, hold-accounting fix,
> token device claim, me-ENDED-reason, NIK throttle, liveness STALE/revive/end behaviour) is
> unchanged.

Also change the ADR-030 **Status** line to note the amendment date and that gate items (a) and (f)
are resolved.

---

## 8. Testability

- **Unit**: `ZoneForSeq`, `mapper` seq/zone (no DRAWING branch), promoter FIFO tick without a gate,
  config validation without `QUEUE_DRAW_CHUNK` and with `QUEUE_LIVENESS_SHARDS` bounds. All pure,
  fast, table-driven (testing-review steering).
- **Integration (real Valkey `aqb-valkey`)**: sharded liveness STALE/revive/end identical at `B=1` and
  `B=16`; heartbeat routing to the right shard; **admit (`promote`) / `leave` / `end_entry` remove the
  entry from the correct `hb:{s}`/`hbstale:{s}` shard, with no re-processing of terminal entries on the
  next sweep (finding #9 behavioural check, §4.5)**; sweep fan-out under the lease fence; and the load
  test (§4.6) as the regression gate. No draw integration tests remain.
- **No-residual-DRAWING gate (finding #10)**: `grep -ri DRAWING services/queue` MUST return empty after
  the §3 edits — part of the same pre-gate audit as the `Shuffle` repo-search.
- A design that could not be load-tested on real Valkey would be unacceptable here; the protocol in
  §4.6 is the gate, and "Valkey unavailable" is a stated outcome, never a silent pass.
- **Build-tag footprint (resolves finding #8).** The draw removal edits unit tests that live in
  packages compiled under BOTH plain `go test` and `-tags integration` (`anon_values_test.go`
  `TestShuffle_*`, `status_zones_test.go`, `my_entry_heartbeat_test.go`), while whole
  `*_integration_test.go` files it deletes are `//go:build integration`. A partial edit that leaves a
  dangling `Shuffle`/`*Rank` reference breaks one tag set but not the other. Gate: after the edits,
  **both `go build ./...` and `go vet -tags integration ./...` MUST be clean in `services/queue`**
  before the load test runs (and `abuse_simulation_integration_test.go`, being `//go:build integration`
  and de-entangled per §3, is covered by the `-tags integration` vet).

---

## 9. Responses to the review findings (`design.change.review.json`)

Prior findings 1–8 were CLOSED by the reviewer across revisions 2–3; findings **9 and 10** are the
open set this revision (4) resolves.

- **#1–#8** — RESOLVED (CLOSED by reviewer across rev 2–3): target path, CHANGE 1 drafted, CHANGE 2
  addressed, KEEP/REMOVE boundary, the de-entangled `abuse_simulation_integration_test.go`, the
  `aqb-valkey` load-test precondition, the `TestHeartbeatLoad` A/B wording, and the build-tag gate.
  See §3, §4, §4.6, §8. No further action.
- **#9 (HIGH) `hb`/`hbstale` shard fan-out omitted `promote.lua`/`leave.lua`/`end_entry.lua`** —
  RESOLVED. §4.2 now enumerates ALL SIX scripts that mutate the liveness index and states that
  `promote.lua` (admit-time `ZREM`), `leave.lua` and `end_entry.lua` must route through the entry's
  per-shard `hb:{s}`/`hbstale:{s}` (reading the stored `shard`), with `stale`/`pending` staying global;
  §4.4 gives the per-script routing; §4.5/§8 add a `B=16` (and `B=1` parity) behavioural check that
  admit/leave/end remove the entry from the correct shard and the next sweep does not re-process
  terminal entries. Verified against live source: `promote.lua` line 35, `leave.lua` line 19,
  `end_entry.lua` `dropSets()` line 9.
- **#10 (MEDIUM) "no residual DRAWING branch" omitted the live `heartbeat.lua`/`claim.lua` arms** —
  RESOLVED. §3 REMOVE now deletes the `heartbeat.lua` `elseif st=='DRAWING'` arm (revive handles only
  `STALE → WAITING`), drops `or st=='DRAWING'` from the `claim.lua` NOT_ADMITTED guard, and trims the
  `promote.lua` comment; the acceptance check is `grep -ri DRAWING services/queue` empty (§3, §8). The
  §7 ADR "No DRAWING branch remains" clause now names these two Lua branches. Verified against live
  source: `heartbeat.lua` lines 26–29, `claim.lua` line 10.
