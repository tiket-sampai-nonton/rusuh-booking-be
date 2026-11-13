# Analisis kapasitas: load @ local (2026-11-13T01:51:51.006Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : L4: 300 pengantre serentak, katalog 30.0 it/s, beli 3.0 it/s
Maximum Tested Virtual Users      : 420
Peak Concurrent VUs               : 304
Maximum Observed RPS (1 s peak)   : 808.0
Maximum Level Avg RPS             : 348.3
Overall Avg RPS                   : 193.5
Overall P95 / P99                 : 2 ms / 4 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : L4: 300 pengantre serentak, katalog 30.0 it/s, beli 3.0 it/s
Maximum Sustainable RPS           : 348.3
Recommended Operating RPS (70%)   : 243.8
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
Hottest Container at Top Level    : queue-worker CPU avg 39%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| L1 | 50 pengantre serentak, katalog 5.0 it/s, beli 0.5 it/s | 58.8 | 2 ms | 6 ms | 0.00% | PASS |
| L2 | 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s | 117.6 | 2 ms | 4 ms | 0.00% | PASS |
| L3 | 200 pengantre serentak, katalog 20.0 it/s, beli 2.0 it/s | 291.4 | 2 ms | 4 ms | 0.00% | PASS |
| L4 | 300 pengantre serentak, katalog 30.0 it/s, beli 3.0 it/s | 348.3 | 1 ms | 3 ms | 0.00% | PASS |
