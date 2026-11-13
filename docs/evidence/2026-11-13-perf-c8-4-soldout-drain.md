# Perf C8.4: sold-out drain from the sweeper (2026-11-13)

Scope: the drain of WAITING/STALE entries after `booking.sold_out`, moved from the consumer's 20-batch retry loop to a
per-tick sweeper budget (D-C8.4). No k6 run; k6 runs once at the end of the plan.

## Threshold

100k WAITING entries ended within one sweeper tick (< 5 s). The tick budget is `SoldOutMaxBatches` (200) x
`SoldOutBatchSize` (500) = 100,000 entries per event per tick.

## Measurement

Command (valkey integration, local compose Valkey on port 6380, one run each):

    go test -tags integration -count=1 -run '^$' -bench BenchmarkSoldOutDrain_100k -benchtime=1x ./internal/queue/infrastructure/valkey/

| Run   | Code state                          | Drain time (100k entries) | Result |
|-------|-------------------------------------|---------------------------|--------|
| Before | HEAD 1fd3d44 (store unchanged)     | 2.97 s                    | 100,000 ended |
| After  | C8.4 (store unchanged)             | 3.21 s                    | 100,000 ended |

The timed part is the `EndWaitingSoldOut` loop (batch 500 until a short batch). The 100k joins that fill the set are
outside the timer. The store call is not changed by this task, so the two runs measure the same code path; the
difference is run-to-run noise on a single sample. Both are under the 5 s threshold, with about 1.5x headroom.

## Caveat

The benchmark measures the store-side drain only. The per-tick budget and the sweeper cadence decide whether a tick
can finish the drain; this run shows the drain itself fits in the 5 s threshold.
