# Perf C8.8: promoter reads candidates in one call and publishes after the lease (2026-11-13)

Scope: `buildEntries` reads every candidate of a tick with one `QueueStore.GetEntries` call (pipelined HGETALL, PF-P2-2),
replacing one `GetEntry` per candidate. `turn.arrived` is published after the promoter lease is released. No k6 run;
k6 runs once at the end of the plan.

## Target

Promoter tick over 200 waiting candidates: <= 100 ms (the promoter interval).

## What was measured

Command (queue worker integration, local compose Valkey on port 6380, three runs each, 30 iterations per run):

    go test -tags integration -count=3 -run '^$' -bench 'BenchmarkPromoterTick_200$' -benchtime=30x ./internal/queue/infrastructure/worker/

The benchmark joins 200 entries on the real Valkey, then times `Promoter.Run` with a Booking stub that has no stock, so
every candidate answers NO_SUPPLY_NOW and the candidate set is the same on every tick. `roundtrips/op` counts the Valkey
`Do` and `DoMulti` calls the store makes per tick.

| Run    | Code state                              | ms per tick (3 runs)        | Valkey round trips per tick |
|--------|-----------------------------------------|-----------------------------|-----------------------------|
| Before | HEAD 1198091 (GetEntry per candidate)   | 136.9, 137.4, 121.4         | 204                         |
| After  | C8.8 (GetEntries, one batch read)       | 4.81, 4.81, 4.60            | 5                           |

Threshold: the Before runs exceed 100 ms on this machine; the After runs are below it.

## Caveat

Both runs use loopback Valkey, where one round trip costs far less than across a network. The saving in the table is
therefore a floor on the effect: on a network with higher round-trip time, the 200 per-candidate reads cost
proportionally more, and the single batch read does not. These numbers do not measure production latency.

## Not covered

- `turn.arrived` now leaves the lease. Its publish timing is checked by a unit test (`TestPromoter_PublishesAfterLeaseRelease`),
  not by this benchmark. The publish path is not timed here.
- Only the promoter tick is measured. The sweeper and the liveness tick are unchanged by this task.
