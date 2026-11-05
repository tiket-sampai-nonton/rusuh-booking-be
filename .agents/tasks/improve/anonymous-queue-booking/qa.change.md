# QA Report — anonymous-queue-booking CHANGE 1 (drop draw) + CHANGE 2 (fix promoter-tick slowdown)

Reviewer: Senior QA (fresh context, independent). Worktree:
`/home/nuzirwan/.../anonymous-queue-booking` (branch `improve/anonymous-queue-booking`, base `feature/rework`
53e74da). All work uncommitted (per owner: "dont commit your work ill review first"). Git reads via `git -C`.

**Verdict: REJECT.** CHANGE 1 (drop draw) is complete and verified. CHANGE 2 does NOT meet the task's stated,
authoritative requirement "fix the promoter-tick slowdown to **<=+20% of base** … and PROVE it". The ≤+20%
ratio is NOT met — I reproduced it myself on real Valkey (ratio 1.27 and 1.36 in 2 of 4 B=16 runs). The
implementer did not falsely claim it met; they honestly recorded it as unreachable and the gate was
**redefined by an owner decision (option A)** to an absolute-latency + marginal-sweep criterion. That
redefinition is a human-gate call, not something evidence can satisfy, and it changes the acceptance
criterion the task text fixes. QA cannot sign off that the stated ≤+20% requirement is met; the real,
proven improvement (absolute tick ~3.3 ms → ~1.2 ms) and the honesty of the evidence are credited in full.

---

## 1. Discovery (project facts, not assumed)

- Steering/invariants: `.kiro/steering/*` loaded (structure, tech, invariants, security, code-principles,
  testing-review). Invariants #4 (timer server-authoritative), #2 (Jangan PHP / admission only when reserved)
  are the ones this change touches.
- Gate command (from `Makefile`): `make ci = build lint cover isolation contracts`. `cover` =
  `GO_TEST_TAGS=integration bash tools/check-coverage.sh <mod> 80` per module. Load test `TestHeartbeatLoad`
  is gated by `RUN_HEARTBEAT_LOAD=1` and is NOT part of `make ci`.
- `engineering-standards` MCP: available but not consulted for a specific page here; this review is grounded
  in the repo's own steering + ADR/SRS/TP, which are the binding standards for this project.
- `.codegraph/`: absent — codegraph rule N/A; used grep/read.
- Real infra present via `docker ps`: `aqb-valkey` (valkey 8.1-alpine, :16379) ✅, `aqb-pg` :15432,
  `aqb-rabbit` :15673. Forbidden servers present and correctly guarded against: `telkomsel_redis` :6379,
  cluster `valkey` :7000-7005. The load/shard tests `Fatal` on :6379/:7000-7005 (`assertDedicatedValkey`).

## 2. CHANGE 1 — drop the opening-window random draw — VERIFIED COMPLETE

| # | Requirement | Check | Result |
|---|---|---|---|
| 1 | Remove draw.go, draw_apply.lua, draw_finalize.lua | `ls` the four files | all absent ✅ |
| 2 | Remove DRAWING status + null-field contract | `grep -ri DRAWING services/queue` | empty (exit 1) ✅ |
| 3 | No Shuffle residue | `grep -ri shuffle services/queue` | empty (exit 1) ✅ |
| 4 | Remove config QUEUE_DRAW_CHUNK, OpeningWindow, WithDraw, IsDrawn | grep | empty ✅ |
| 5 | Delete migration 0004_opening_window | `ls 0004_*` | absent; `embed_test.go` asserts 3 migrations ✅ |
| 6 | Plain FIFO by seq for ALL joins; drop Lua `rank` field | `grep rank services/queue/assets/lua` | only comments remain; scoring by seq ✅ |
| 7 | No leftover DRAWING in FIFO position/zone math or status doc | code grep empty; `ZoneForSeq`, `*Seq` DTO fields restored | ✅ |
| 8 | Mark DRAWING reqs + TC-ANON-20 WITHDRAWN in reqs/design/SRS/TP | TP line 1345 TC-ANON-20 WITHDRAWN; ADR-030 Amendment §1; `grep DRAWING docs` only in WITHDRAWN/historical-decision lines | ✅ |
| 9 | Keep everything else (anon join, 3-units, NIK-at-hold, token claim, FIFO abuse sub-cases) | abuse-sim FIFO sub-cases a/a2/c/d present (`TestAbuseSim_*`); draw-only sub-cases + opening_window + liveness_draw tests deleted | ✅ |

F11 (HIGH, design review) — the four live Go DRAWING branches (`my_entry.go:96/101`, `join.go:161`,
`queue_store.go:448`) — confirmed gone: `grep -ri DRAWING services/queue` is empty, and the package builds and
its tests pass (see §4). F12 (Lua `rank` field drop) confirmed: join.lua scores by seq, no `HSET rank`.

Note (minor, not blocking): ADR line 802 still carries the ORIGINAL decision (8) text describing DRAWING as a
live decision; it is immediately superseded by the "Amandemen ADR-030 — 2026-11-05" block (lines 806+) that
explicitly withdraws it. This is acceptable as decision-history provenance, not a live contract. Flagged only
so a future reader does not mistake (8) for current.

## 3. CHANGE 2 — promoter-tick slowdown — the ≤+20% requirement is NOT met

### 3.1 What the task text requires (authoritative)
"Fix the promoter-tick slowdown to **<=+20% of base** (prior +26–38%) and PROVE it with the load test on real
Valkey." Original owner message #12: "drop random draw. slowndown arent alllowed."

### 3.2 Independent reproduction on real `aqb-valkey` (:16379), B=16, 100k entries, 3,333 hb/s, 30 s
I ran `TestHeartbeatLoad` myself four times at B=16 and once at B=1 (never :6379/:7000-7005; guard enforced):

| run | B | tick_base | tick_load | **tick_ratio** | noop_ratio | marginal_sweep | p99 hb | err |
|---|---|---|---|---|---|---|---|---|
| 1 | 16 | 1.42 ms | 1.17 ms | **0.83** | 0.89 | 0.42 ms | 3.29 ms | 0 |
| 2 | 16 | 0.89 ms | 1.14 ms | **1.27** | 1.54 | 0.39 ms | 3.48 ms | 0 |
| 3 | 16 | 0.90 ms | 1.23 ms | **1.36** | 1.50 | 0.47 ms | 3.50 ms | 0 |
| 4 | 16 | 1.11 ms | 1.33 ms | **1.20** | 1.28 | 0.54 ms | 3.54 ms | 0 |
| ref | 1 | 0.90 ms | 1.09 ms | 1.21 | 1.28 | 0.34 ms | 3.94 ms | 0 |

**The loaded/base ratio exceeds 1.20 in 2 of 4 B=16 runs (1.27, 1.36).** The ≤+20% target is therefore NOT
met — it passes only on noise-favourable runs. This independently confirms the implementer's own §3b/§3c data
(ratio fluctuates 0.83–1.47). The `noop_ratio` (a trivial `GET` under the same load) is 0.89–1.54, which
independently proves the ≤+20% ratio is physically unreachable at sub-ms base latency on a single-threaded
Valkey node, for ANY tick design — not a fault of the implementation.

### 3.3 What WAS achieved and honestly proven
- Absolute loaded tick latency: ~1.1–1.3 ms, down from the pre-fix single-key build ~3.1–3.3 ms (evidence
  2026-11-04). A real, large improvement, reproduced here.
- Marginal sweep cost over a no-op: ~0.34–0.54 ms — the sweep itself is not the bottleneck.
- p99 heartbeat ≤ 3.6 ms (budget 200 ms), error rate 0, target rate achieved. Proven on real Valkey.
- Mechanism (verified in code + tests): `hb`/`hbstale` sharded by `crc16(entryId)%B` across all six mutation
  scripts; `liveness.lua` sweeps all shards in one EVAL; new `liveness_tick.lua` collapses the whole tick to
  one round-trip. `TestLivenessShard_*` pass at B=1 and B=16 (no stranded entries, revive restores seq).

### 3.4 Why this is a REJECT and not a PASS
The implementer did NOT claim the ≤+20% target met — the evidence, ADR amendment, and the rewritten
`TestHeartbeatLoad` all state the ratio gate is dropped and replaced by "option A" (absolute tick + marginal
sweep). That is honest and well-investigated. But:
- The task's authoritative acceptance criterion is a specific number (≤+20%), and that number is not met.
- `TestHeartbeatLoad` no longer asserts the ≤+20% ratio at all (it asserts `duringMed < 2.5 ms` and
  `marginal < 1.2 ms` only). So the suite that is supposed to "PROVE ≤+20%" cannot fail on a ≤+20% violation —
  the proof the task asks for does not exist as an executable gate.
- Redefining the acceptance criterion is an owner / human-gate decision. The context does record an owner
  "option A" decision (2026-11-05), but QA's job is to verify the stated requirement against evidence, and the
  stated requirement is unmet. Accepting the redefinition is the owner's call to make at the gate, explicitly,
  with eyes open — not something I can certify as "requirement met".

If the owner ratifies option A (absolute+marginal criterion, ratio unreachable on one Valkey node), this
becomes a PASS with zero code changes — the engineering goal (responsive tick under load) is genuinely met
and proven. That ratification must be explicit; this review surfaces it rather than assuming it.

## 4. Gate / coverage evidence (independently re-run)
- `GO_TEST_TAGS=integration bash tools/check-coverage.sh services/queue 80` on real aqb-valkey → **exit 0,
  queue logic coverage 85.6% ≥ 80%**. All queue packages compiled and integration tests passed (valkey 80.8%,
  worker 88.6%, usecase 90.2%, config 92.9%). Build/vet/lint green per implementer; coverage gate re-verified
  here.
- `TestLivenessShard_*`, `TestLiveness_StaleReviveEnd`, `TestAbuseSim_*` (FIFO) pass on real Valkey.
- Pre-existing `pkg/rabbitmq` flake (`TestConsume_TransientFailureIsRetriedThenAcked`): OUT OF SCOPE per the
  brief — package untouched by this change. Noted, NOT a gate failure.
- `make ci` note: the default `TEST_VALKEY_SERVER` in the Makefile points at `:6379` (forbidden shared Redis),
  so a bare `make ci` would run the `cover` integration stage against the wrong server; it must be run with
  `TEST_VALKEY_SERVER` overridden to aqb-valkey :16379 (as I did). Not a defect in the change, but the "make
  ci green" claim is only valid with that override — worth stating so the green is not misread.

## 5. Adversarial invariant checks (one break attempt each, mapped to steering invariants)
- **Invariant #4 (timer/position server-authoritative), FIFO integrity after draw removal:** attempted to find
  a leftover DRAWING branch that could skew position/zone math → `grep -ri DRAWING services/queue` empty;
  `ZoneForSeq` + `*Seq` DTO restored; abuse-sim FIFO sub-cases (real-buyer-ahead-of-ghosts admitted first tick)
  pass. No FIFO skew. ✅
- **Stranded-entry under sharding (data integrity of the liveness index):** `TestLivenessShard_NoStranded*`
  and `*StaleReviveEndParity` pass at B=1 and B=16 — admit/leave/end clear the correct shard, revive restores
  original seq, terminal entries not re-processed. ✅
- **Forbidden-server guard (CHANGE 2 precondition):** `assertDedicatedValkey` Fatals on :6379 and :7000-7005;
  both forbidden containers are actually running on this host, so the guard is load-bearing and verified real. ✅
- **Slowdown claim (the core CHANGE 2 invariant "slowdown arent allowed"):** broke the ≤+20% claim by direct
  reproduction (ratio 1.27, 1.36). The slowdown IS present as a ratio; it is only "allowed" under the owner's
  redefined criterion. ✅ (this is the REJECT driver)

## 6. Reproduction commands
- CHANGE 1 gate: `grep -ri DRAWING services/queue` → empty (exit 1); `grep -ri shuffle services/queue` → empty.
- Coverage: `cd <worktree> && GO_TEST_TAGS=integration TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]'
  bash tools/check-coverage.sh services/queue 80` → `queue: coverage logika 85.6% (ambang 80%)`, exit 0.
- Load test (the ≤+20% proof that is missing): `cd services/queue && RUN_HEARTBEAT_LOAD=1
  QUEUE_LIVENESS_SHARDS=16 TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]' go test -tags integration
  -run TestHeartbeatLoad -v -count=1 ./internal/queue/infrastructure/valkey/` → PASS, but `tick_ratio` logged as
  1.27 / 1.36 on repeat runs (expected ≤ 1.20; actual > 1.20). The assertion that fires is the absolute/marginal
  criterion, not the ratio.

## 7. Summary
- CHANGE 1: PASS — draw fully removed, FIFO-by-seq restored, docs WITHDRAWN, nothing else disturbed, coverage
  85.6%, integration green on real Valkey.
- CHANGE 2: REJECT — the authoritative ≤+20% requirement is unmet (reproduced: ratio up to 1.36 on real
  Valkey) and `TestHeartbeatLoad` no longer asserts it, so the required proof does not exist as a gate. The
  honest, well-evidenced finding is that ≤+20% is physically unreachable on a single Valkey node; the real
  improvement (abs tick ~3.3 ms → ~1.2 ms, 0 errors) is proven. Converting this to PASS requires an EXPLICIT
  owner ratification of the redefined (option A) criterion — a human-gate decision, not a QA certification.
