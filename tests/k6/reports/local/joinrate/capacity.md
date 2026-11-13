# Analisis kapasitas: joinrate @ local (2026-11-13T12:38:18.284Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : J1500: 0 pengantre serentak, katalog 0.0 it/s, beli 1500.0 it/s
Maximum Tested Virtual Users      : 200
Peak Concurrent VUs               : 44
Maximum Observed RPS (1 s peak)   : 1501.0
Maximum Level Avg RPS             : 1500.0
Overall Avg RPS                   : 647.7
Overall P95 / P99                 : 1 ms / 3 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : J1500: 0 pengantre serentak, katalog 0.0 it/s, beli 1500.0 it/s
Maximum Sustainable RPS           : 1500.0
Recommended Operating RPS (70%)   : 1050.0
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
Hottest Container at Top Level    : queue-api CPU avg 41%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| J100 | 0 pengantre serentak, katalog 0.0 it/s, beli 100.0 it/s | 100.0 | 2 ms | 4 ms | 0.00% | PASS |
| J300 | 0 pengantre serentak, katalog 0.0 it/s, beli 300.0 it/s | 300.0 | 1 ms | 2 ms | 0.00% | PASS |
| J600 | 0 pengantre serentak, katalog 0.0 it/s, beli 600.0 it/s | 600.0 | 1 ms | 2 ms | 0.00% | PASS |
| J1000 | 0 pengantre serentak, katalog 0.0 it/s, beli 1000.0 it/s | 1000.0 | 1 ms | 2 ms | 0.00% | PASS |
| J1500 | 0 pengantre serentak, katalog 0.0 it/s, beli 1500.0 it/s | 1500.0 | 1 ms | 3 ms | 0.00% | PASS |
