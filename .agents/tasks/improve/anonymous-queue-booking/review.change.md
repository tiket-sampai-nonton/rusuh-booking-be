# Principal review — anonymous-queue-booking CHANGE 1 (drop draw) + CHANGE 2 (fix promoter-tick slowdown)

Reviewer: Principal Engineer, fresh context, independent. Reviewed the uncommitted working tree in
`/home/nuzirwan/.../.worktrees/improve/anonymous-queue-booking` via
`git -C … diff feature/rework` (base `feature/rework` 53e74da; all work uncommitted per owner
"dont commit your work ill review first"). I did NOT author or edit this code. Per the brief I did
NOT re-run the full suites; I re-ran only the cheap CHANGE-1 removal gates (greps / file-existence)
to confirm the mechanical claims, and I read the QA verdict and the implementer's recorded `make ci`
and load-test evidence.

**VERDICT: NEEDS_WORK (not approved).**

Driver: **CHANGE 2's authoritative acceptance criterion — promoter tick `loaded/base ≤ 1.20`
(≤ +20% of base) — is NOT met and is NOT proven.** QA independently reproduced ratios of 1.27 and
1.36 on real `aqb-valkey` (:16379) at B=16. The implementer's own evidence and AI-log honestly record
the target as unproven and escalated. The acceptance gate was then redefined to a different
criterion ("option A": absolute tick + marginal sweep) attributed to an "owner decision 2026-11-05"
that does **not** appear in the authoritative owner messages. The brief is explicit:
"Approve only with QA PASS and gate-command evidence present." **QA is REJECT (`approved:false`)**, and
my independent review confirms the stated requirement is unmet. CHANGE 1 is complete and verified and
would pass on its own; the gate is binary, so the change set as a whole is NEEDS_WORK.

The implementer's work is honest and well-investigated — this is not a false-claim finding. The real,
large improvement (absolute loaded tick ~3.3 ms → ~1.2 ms, 0 errors, p99 ≤ 3.6 ms) is genuine and
proven. But QA cannot certify, and I cannot approve, a numeric requirement the task fixes as
authoritative when that number is not met and the proof the task asks for no longer exists as an
executable gate. Converting CHANGE 2 to PASS requires an **explicit owner ratification** of the
redefined criterion — a human-gate decision, not something this review or the evidence can supply.

---

## 1. Discovery (project facts, verified not assumed)

- **Standards / invariants:** `.kiro/steering/*` are the binding standards (structure, tech,
  invariants, security-privacy, code-principles, testing-review, product). The invariants this change
  touches: #2 (Jangan PHP / admission only when reserved), #4 (timer & position server-authoritative).
- **Gate command (`Makefile` line 98):** `make ci = build lint cover isolation contracts`.
  `cover` (line 65-66) = `GO_TEST_TAGS=integration bash tools/check-coverage.sh <mod> 80` per module.
  **`TestHeartbeatLoad` is gated by `RUN_HEARTBEAT_LOAD=1` and is NOT part of `make ci`** — so a green
  `make ci` does NOT prove CHANGE 2. The only proof of CHANGE 2 is the load test, which no longer
  asserts the ≤ +20% ratio.
- **engineering-standards MCP:** available. Queried `topic:"performance acceptance criteria load
  test"` and `topic:"verification evidence"` — both returned no matching page. The wiki has no
  standard on performance-gate acceptance; the binding standards here are the repo steering + ADR/SRS/TP
  and the authoritative task text. Stated so per the gate checklist.
- **.codegraph/:** present in the main repo root but absent in this worktree; the reviewed changes are
  uncommitted in the worktree, so the index would not reflect them. I used git diff + grep + read on
  the worktree directly (codegraph rule exception: index does not cover the uncommitted worktree).
- **Real infra (per QA `docker ps`):** project-dedicated `aqb-valkey` (valkey 8.1, :16379),
  `aqb-pg` :15432, `aqb-rabbit` :15673. Forbidden servers also running and correctly guarded against:
  Redis :6379 and cluster :7000-7005 (`assertDedicatedValkey` Fatals on them — verified in the test
  source).

---

## 2. CHANGE 1 — drop the opening-window random draw — VERIFIED COMPLETE (would pass on its own)

I re-ran the mechanical gates myself (cheap, deterministic):

| # | Requirement | Check I ran | Result |
|---|---|---|---|
| 1 | Remove draw.go, draw_apply.lua, draw_finalize.lua | file-existence | all absent ✅ |
| 2 | Remove migration 0004_opening_window | `ls migrations/` | only 0001–0003 remain; `embed_test` asserts 3 ✅ |
| 3 | No DRAWING state / null-field contract | `grep -ri DRAWING services/queue` | empty (exit 1) ✅ |
| 4 | No Shuffle residue | `grep -ri shuffle services/queue` | empty (exit 1) ✅ |
| 5 | No draw config/API residue | `grep -rn "Windowed\|OpeningWindow\|WithDraw\|NewDraw\|DrawChunk\|IsDrawn\|fixedSeed\|draw_apply\|draw_finalize" services/queue` | empty (exit 1) ✅ |
| 6 | Plain FIFO by seq; drop Lua `rank` field | `grep -rn rank services/queue/assets/lua` | only comments ("no rank"); scoring by seq ✅ |
| 7 | `*Seq` DTO + ZoneForSeq restored | grep domain/port | `HeadSeq/GuaranteedUntilSeq/PossibleUntilSeq`, `ZoneForSeq`, `WaitingSeqAt` present ✅ |
| 8 | Mark DRAWING reqs + TC-ANON-20 WITHDRAWN in SRS/TP/ADR/specs | grep docs | TP TC-ANON-08/09/20 WITHDRAWN, TC-ANON-19 scenario (e) WITHDRAWN; SRS-F-25-02 & F-05-09 WITHDRAWN/corrected to seq; ADR-030 Amendment §1 ✅ |
| 9 | Everything else kept intact | grep booking/queue | BUYER_NIK_LIMIT_REACHED, outstanding_units, buyer_nik_hash, me-ENDED reasons (LEFT/STALE_TIMEOUT/CLAIM_WINDOW_EXPIRED/JOURNEY_ENDED), NIK throttle/budget all present ✅ |

**System-wide / contract impact (checked beyond the diff):**
- `docs/contracts/openapi/queue.yaml`: `state` enum = WAITING/STALE/ADMITTED/CLAIMED/ENDED — **no
  DRAWING**. Zone boundaries are `headSeq/guaranteedUntilSeq/possibleUntilSeq` (not `*Rank`). The
  surviving `rank` property is the legitimate per-entry FIFO-by-seq admission-order field (the design
  §3 explicitly KEEPS `EntryStatus.Rank` computed on seq) — correctly described "FIFO ketat atas seq".
  Contract aligned.
- `docs/analisa/08a-frontend-integration.md`: step 5 (`drawing:true`/`state:DRAWING`) deleted; the
  `*Rank` fields reverted to `*Seq`. Aligned.

This matches QA's QA-CH1-01 (INFO, complete). The one QA nit (QA-CH1-02: ADR line ~802 still shows the
original decision (8) text) is accurate — it is immediately superseded by the "Amandemen ADR-030 —
2026-11-05" block that explicitly withdraws it, so it reads as decision-history provenance, not a live
contract. **Minor, non-blocking** (see §4).

---

## 3. CHANGE 2 — promoter-tick slowdown — the ≤ +20% requirement is NOT met (BLOCKING)

### 3.1 The authoritative acceptance criterion
- Task text: "Fix the promoter-tick slowdown to **<=+20% of base** (prior +26–38%) via the approach in
  the design, PROVEN with the load test on real Valkey." Owner message #12: "slowndown arent alllowed."
- The design the brief points to states its own pass gate in §4.6 verbatim: "**Pass gate: with `B=16`,
  `loaded/base ≤ 1.20` (≤ +20%)** … The `B=1` run must still show the old regression." So the ratio is
  the authoritative gate from BOTH the task text and the cited design — it is not an external
  re-interpretation.

### 3.2 Finding CH2-1 (HIGH, blocking) — ≤ +20% ratio NOT met on real Valkey
QA independently reproduced `TestHeartbeatLoad` four times at B=16 on real `aqb-valkey` (:16379), 100k
entries, 3,333 hb/s, 30 s: `tick_ratio` = 0.83, **1.27**, **1.36**, 1.20 — **2 of 4 runs exceed 1.20**.
The ≤ +20% target passes only on noise-favourable runs. The implementer's own evidence
(`docs/evidence/2026-11-05-…-change.md` §3b/§3c) and AI-log iteration 4 concur: "rasio `loaded/base`
≤ 1,20 **BELUM tercapai** (B=16 ≈1,35; B=1 ≈1,46)" and "Target ≤+20% **TIDAK diklaim tercapai** …
DIESKALASIKAN". I accept this as reproduced by a credible independent run; I did not re-run it myself
per the brief's "do not re-run the suites" instruction.
- **Rule violated:** task text "fix … to <=+20% of base … PROVEN"; design §4.6 "Pass gate … loaded/base
  ≤ 1.20"; owner message #12 "slowndown arent alllowed".
- **Reproduction:** `cd services/queue && RUN_HEARTBEAT_LOAD=1 QUEUE_LIVENESS_SHARDS=16
  TEST_VALKEY_SERVER='[{"host":"localhost","port":16379}]' go test -tags integration -run
  TestHeartbeatLoad -v -count=1 ./internal/queue/infrastructure/valkey/` → logs `tick_ratio` > 1.20 on
  repeat runs (expected ≤ 1.20).

### 3.3 Finding CH2-2 (HIGH, blocking) — the required proof no longer exists as an executable gate, and the gate was redefined without a traceable owner ratification
`heartbeat_load_integration_test.go` no longer asserts the ratio. For `shards > 1` it asserts
`assert.Less(duringMed, 2500µs)` (absolute) and `assert.Less(marginal, 1200µs)` (marginal sweep); the
ratio is "logged for transparency only (gate is absolute+marginal, not ratio)". So the suite that is
supposed to "PROVE ≤ +20%" **cannot fail on a ≤ +20% violation** — the proof the task requires does not
exist as a gate (I read the full test source; confirmed).

The redefinition ("option A") is recorded in the ADR-030 Amendment §2, the TP TC-ANON-05 row (flipped
to "LULUS"), and the evidence §3d as a "keputusan pemilik 2026-11-05, opsi A" (owner decision). **I
checked the authoritative owner messages (the Original user request #1–#12): there is no message in
which the owner chose "option A" or agreed to redefine the gate to absolute+marginal.** The last owner
statement on this topic (#12) is "drop random draw. slowndown arent alllowed" — if anything a
hardening of the ≤ +20% line, not a relaxation. The AI-log itself is candid: iteration 4 says the two
options were "diserahkan ke pemilik — DIESKALASIKAN" (escalated to the owner), and only the subsequent
iteration-4b entry asserts the owner "chose" option A. That ratification is not present in the
authoritative record this review is bound to verify against.
- **Rule violated:** task text "PROVE it with the load test"; QA must verify the *stated* requirement,
  not a redefined one. Also the `learnings`/human-gate principle that acceptance-criterion changes are
  the owner's explicit call — here it is asserted as decided but is not traceable to an owner message.
- **Reproduction:** `grep -n 'assert' services/queue/internal/queue/infrastructure/valkey/heartbeat_load_integration_test.go`
  → assertions are `duringMed < 2500µs` and `marginal < 1200µs`; no `loaded/base <= 1.20` assertion.

### 3.4 Finding CH2-3 (MEDIUM, note not independent-block) — implementation deviates from "the approach in the design"
The brief says to fix the slowdown "**via the approach in the design**". The design §4.3/§4.4 approach is
sharded `hb`/`hbstale` with the sweep fanning out `liveness.lua` **once per shard**. The implementation
instead collapses the whole tick into a **single round-trip** `liveness_tick.lua` (new script, not in
the design) plus a single-EVAL `liveness.lua` that loops all shards. This is a reasonable and
well-argued engineering improvement (it is the real source of the absolute-latency win), but it is a
different mechanism than the cited design, and it is the mechanism that still fails to reach the design's
own ≤ 1.20 gate. This does not independently block (the sharding IS present and the single-round-trip is
defensible), but it is part of why the design's stated gate was not met with the design's stated
approach, and it should be reconciled when the acceptance criterion is settled.
- **Rule:** task text "via the approach in the design"; design §4.3/§4.4 (per-shard fan-out) vs shipped
  `liveness_tick.lua` single round-trip.
- **Reproduction:** `ls services/queue/assets/lua/liveness_tick.lua` (exists; not named in design); the
  design §4.3 describes `SweepLiveness` fan-out, the test calls `SweepLivenessTick` (one round-trip).

### 3.5 What WAS genuinely achieved (credited in full)
- Absolute loaded tick ~1.1–1.3 ms, down from pre-fix single-key ~3.1–3.3 ms — a real, large win,
  reproduced by QA.
- Sharding is correct and does not strand entries: all six liveness-mutation scripts route through the
  entry's stored shard; `TestLivenessShard_NoStranded*` and `*StaleReviveEndParity` pass at B=1 and
  B=16 (verified these are the behavioural guards the design §4.5 required; honours invariant #2 "an
  admitted entry is never in the liveness set" and #4 revive-at-original-seq).
- p99 heartbeat ≤ 3.6 ms (budget 200 ms), error rate 0, target rate achieved on real Valkey.
- The root-cause analysis (single-thread Valkey + one auto-pipelined connection → every tick op,
  including a bare GET, queues ~0.3–0.5 ms behind 3,333 hb/s; GET floor ≈ 1.59) is systematic and
  convincing. It is a legitimate basis to argue the ≤ +20% ratio is unreachable on one Valkey node —
  but that is an argument for the owner to change the acceptance criterion, which the owner has not
  (traceably) done.

---

## 4. Non-blocking findings

- **CH-NIT-1 (QA-CH1-02):** ADR-030 decision (8) original text remains above the Amendment block;
  acceptable as provenance, but a future reader could misread (8) as live. Suggest a one-line
  "(SUPERSEDED — see Amendment 2026-11-05 §1)" inline marker on (8). Rule: doc-clarity/traceability.
- **CH-NIT-2 (QA-ENV-01):** `Makefile` line 15 default `TEST_VALKEY_SERVER ?= [{"host":"localhost",
  "port":6379}]` points the `cover` integration stage at the forbidden shared Redis. A bare `make ci`
  runs `cover` against :6379; the green is only valid with `TEST_VALKEY_SERVER` overridden to
  aqb-valkey :16379 (the load/shard tests Fatal on :6379, but the broader `cover` stage does not). Not
  a defect introduced by this change, but it means "make ci green" must be qualified with the override.
  Rule: testing-review steering (integration tests on project-dedicated infra).

---

## 5. Gate / QA evidence check (per the brief, read not re-run)

- **QA verdict (`qa.change.json`): `approved: false`** — REJECT, with two HIGH CHANGE-2 findings
  (QA-CH2-01, QA-CH2-02) matching my CH2-1/CH2-2. The brief: "Approve only with QA PASS." QA did not
  pass → I cannot approve.
- **Gate-command evidence:** the implementer's evidence §4 records `go build/vet/vet-integration/
  golangci-lint` exit 0, `go test -race ./...` (unit) green, `make contracts` green, and queue logic
  coverage **85.6% ≥ 80%** on real aqb-valkey; QA independently re-ran the coverage gate (exit 0,
  85.6%). So the `make ci` family is green on the dedicated container. **But** `make ci` does not
  include `TestHeartbeatLoad`, so it is not evidence for CHANGE 2 — and CHANGE 2 is where the gate
  fails.
- **Pre-existing `pkg/rabbitmq` flake:** out of scope per the brief (package untouched); NOT counted as
  a gate failure.

---

## 6. Path to approval (what would flip this to APPROVED)

Either of the following, explicitly:
1. **Meet the stated gate:** make `loaded/base ≤ 1.20` at B=16 hold consistently on real Valkey and
   re-add a `TestHeartbeatLoad` assertion that fails below that bar — i.e. the design §4.6 gate as an
   executable check — with multi-run evidence (not a single noise-favourable run). The root-cause
   analysis suggests this likely requires the design's "option B" (liveness index on a separate Valkey
   thread/instance), which the brief/design scope out.
2. **Owner ratifies the redefined criterion:** an explicit, traceable owner decision to replace
   ≤ +20% with the absolute-tick + marginal-sweep criterion. The AI-log shows this was *escalated*
   (iteration 4) and then *asserted* as decided (iteration 4b), but the authoritative owner messages do
   not contain the ratification. With a real owner yes recorded, this becomes PASS with zero code
   changes — the engineering goal (responsive tick under load) is genuinely met and proven.

CHANGE 1 needs no further work.

---

## 7. Senior self-check
- Checked consumers/contracts, not only the diff: queue.yaml (`state` enum, zone fields, `rank`
  semantics), frontend-integration doc, booking-side KEEP items, migration count. ✅
- Every blocking finding cites a rule (task text / design §4.6 / owner message #12 / human-gate
  principle) and has a reproduction. ✅
- Wiki consulted (no matching page; stated). ✅
- Project bookkeeping checked: AI-log + evidence present and, notably, honest about the unmet target. ✅
- I did not author or edit this code. ✅
