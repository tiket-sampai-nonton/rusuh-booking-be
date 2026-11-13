# Perf C8.4: sold-out drain from the sweeper (2026-11-13)

Scope: the drain of WAITING/STALE entries after `booking.sold_out`, moved from the consumer's 20-batch retry loop to a
per-tick sweeper budget (D-C8.4). No k6 run; k6 runs once at the end of the plan.

## Target

100k WAITING entries ended within one sweeper tick (< 5 s). The tick budget is `SoldOutMaxBatches` (200) x
`SoldOutBatchSize` (500) = 100,000 entries per event per tick. Sweeper-path threshold: checked in the final perf pass,
not in this file.

## What was measured

Command (valkey integration, local compose Valkey on port 6380, one run each):

    go test -tags integration -count=1 -run '^$' -bench BenchmarkSoldOutDrain_100k -benchtime=1x ./internal/queue/infrastructure/valkey/

| Run    | Code state                      | Store-path drain time (100k entries) | Entries ended |
|--------|---------------------------------|--------------------------------------|---------------|
| Before | HEAD 1fd3d44 (store unchanged)  | 2.97 s                               | 100,000       |
| After  | C8.4 (store unchanged)          | 3.21 s                               | 100,000       |

These are STORE-PATH numbers only. The timed part is the `EndWaitingSoldOut` loop (batch 500 until a short batch), run
directly against the store. The 100k joins that fill the set are outside the timer. The store call is not changed by this
task, so the two runs measure the same code path; the gap between them is single-sample noise.

## Not measured here

- The sweeper path: `Sweeper.Run` with its per-batch `GetSale` re-read (R28), the two-pass order (R30), and the per-tick
  budget. Those are covered by unit and integration tests for behaviour, not timed here.
- Therefore this file does not claim the sweeper-path threshold is met. It is checked in the final perf pass.
