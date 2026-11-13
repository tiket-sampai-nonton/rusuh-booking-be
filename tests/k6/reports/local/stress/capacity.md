# Analisis kapasitas: stress @ local (2026-11-13T01:57:42.935Z)

Status: WARNING

```text
CAPACITY ANALYSIS

Maximum Tested Load               : S6: 1800 pengantre serentak, katalog 180.0 it/s, beli 18.0 it/s
Maximum Tested Virtual Users      : 2594
Peak Concurrent VUs               : 2570
Maximum Observed RPS (1 s peak)   : 2874.0
Maximum Level Avg RPS             : 1657.2
Overall Avg RPS                   : 936.0
Overall P95 / P99                 : 123 ms / 2004 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : S5: 1300 pengantre serentak, katalog 130.0 it/s, beli 13.0 it/s
Maximum Sustainable RPS           : 1523.5
Recommended Operating RPS (70%)   : 1066.5
Latency Knee (p95 > 2x awal / SLO): S6 (p95 2088 ms)
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : S6
Hottest Container at Top Level    : postgres CPU avg 147%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| S1 | 200 pengantre serentak, katalog 20.0 it/s, beli 2.0 it/s | 232.2 | 2 ms | 4 ms | 0.00% | PASS |
| S2 | 400 pengantre serentak, katalog 40.0 it/s, beli 4.0 it/s | 465.8 | 2 ms | 4 ms | 0.00% | PASS |
| S3 | 600 pengantre serentak, katalog 60.0 it/s, beli 6.0 it/s | 725.1 | 2 ms | 7 ms | 0.00% | PASS |
| S4 | 900 pengantre serentak, katalog 90.0 it/s, beli 9.0 it/s | 1040.5 | 2 ms | 4 ms | 0.00% | PASS |
| S5 | 1300 pengantre serentak, katalog 130.0 it/s, beli 13.0 it/s | 1523.5 | 3 ms | 11 ms | 0.00% | PASS |
| S6 | 1800 pengantre serentak, katalog 180.0 it/s, beli 18.0 it/s | 1657.2 | 2088 ms | 4916 ms | 0.00% | FAIL |
