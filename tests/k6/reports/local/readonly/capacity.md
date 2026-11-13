# Analisis kapasitas: readonly @ local (2026-11-13T02:14:54.667Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : V1000: 1000 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s
Maximum Tested Virtual Users      : 1000
Peak Concurrent VUs               : 1000
Maximum Observed RPS (1 s peak)   : 522.0
Maximum Level Avg RPS             : 432.7
Overall Avg RPS                   : 180.6
Overall P95 / P99                 : 1 ms / 4 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : V1000: 1000 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s
Maximum Sustainable RPS           : 432.7
Recommended Operating RPS (70%)   : 302.9
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
Hottest Container at Top Level    : queue-worker CPU avg 33%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| V50 | 50 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s | 21.6 | 1 ms | 3 ms | 0.00% | PASS |
| V200 | 200 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s | 86.2 | 1 ms | 2 ms | 0.00% | PASS |
| V500 | 500 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s | 215.3 | 1 ms | 9 ms | 0.00% | PASS |
| V1000 | 1000 pengantre serentak, katalog 0.0 it/s, beli 0.0 it/s | 432.7 | 1 ms | 4 ms | 0.00% | PASS |
