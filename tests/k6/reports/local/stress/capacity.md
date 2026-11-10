# Analisis kapasitas: stress @ local (2026-11-10T06:45:53.413Z)

Status: WARNING

```text
CAPACITY ANALYSIS

Maximum Tested Load               : S6: 1800 pengantre serentak, katalog 180.0 it/s, beli 18.0 it/s
Maximum Tested Virtual Users      : 2556
Peak Concurrent VUs               : 2496
Maximum Observed RPS (1 s peak)   : 3161.0
Maximum Level Avg RPS             : 1708.6
Overall Avg RPS                   : 915.6
Overall P95 / P99                 : 504 ms / 2119 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : S5: 1300 pengantre serentak, katalog 130.0 it/s, beli 13.0 it/s
Maximum Sustainable RPS           : 1473.2
Recommended Operating RPS (70%)   : 1031.2
Latency Knee (p95 > 2x awal / SLO): S6 (p95 1985 ms)
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : S6
Hottest Container at Top Level    : postgres CPU avg 118%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| S1 | 200 pengantre serentak, katalog 20.0 it/s, beli 2.0 it/s | 240.9 | 4 ms | 13 ms | 0.00% | PASS |
| S2 | 400 pengantre serentak, katalog 40.0 it/s, beli 4.0 it/s | 466.0 | 40 ms | 177 ms | 0.00% | PASS |
| S3 | 600 pengantre serentak, katalog 60.0 it/s, beli 6.0 it/s | 699.0 | 3 ms | 35 ms | 0.00% | PASS |
| S4 | 900 pengantre serentak, katalog 90.0 it/s, beli 9.0 it/s | 1052.2 | 24 ms | 128 ms | 0.00% | PASS |
| S5 | 1300 pengantre serentak, katalog 130.0 it/s, beli 13.0 it/s | 1473.2 | 12 ms | 153 ms | 0.00% | PASS |
| S6 | 1800 pengantre serentak, katalog 180.0 it/s, beli 18.0 it/s | 1708.6 | 1985 ms | 3809 ms | 0.00% | FAIL |
