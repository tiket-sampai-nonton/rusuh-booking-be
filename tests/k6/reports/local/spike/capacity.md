# Analisis kapasitas: spike @ local (2026-11-13T02:04:46.920Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : recovery: 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s
Maximum Tested Virtual Users      : 1120
Peak Concurrent VUs               : 1001
Maximum Observed RPS (1 s peak)   : 1498.0
Maximum Level Avg RPS             : 1149.1
Overall Avg RPS                   : 471.4
Overall P95 / P99                 : 2 ms / 4 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : recovery: 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s
Maximum Sustainable RPS           : 193.9
Recommended Operating RPS (70%)   : 135.7
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
Recovery Time After Spike         : 8 s (bucket 5 s)
Hottest Container at Top Level    : queue-worker CPU avg 54%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| base | 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s | 115.4 | 1 ms | 4 ms | 0.00% | PASS |
| spike | 1000 pengantre serentak, katalog 100.0 it/s, beli 10.0 it/s | 1149.1 | 1 ms | 4 ms | 0.00% | PASS |
| recovery | 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s | 193.9 | 1 ms | 3 ms | 0.00% | PASS |
