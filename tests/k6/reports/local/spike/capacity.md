# Analisis kapasitas: spike @ local (2026-11-10T06:53:43.750Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : recovery: 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s
Maximum Tested Virtual Users      : 1124
Peak Concurrent VUs               : 1067
Maximum Observed RPS (1 s peak)   : 1488.0
Maximum Level Avg RPS             : 1159.8
Overall Avg RPS                   : 464.3
Overall P95 / P99                 : 20 ms / 85 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : recovery: 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s
Maximum Sustainable RPS           : 194.5
Recommended Operating RPS (70%)   : 136.1
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
Recovery Time After Spike         : 6 s (bucket 5 s)
Hottest Container at Top Level    : rabbitmq CPU avg 11%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| base | 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s | 143.4 | 4 ms | 8 ms | 0.00% | PASS |
| spike | 1000 pengantre serentak, katalog 100.0 it/s, beli 10.0 it/s | 1159.8 | 29 ms | 106 ms | 0.00% | PASS |
| recovery | 100 pengantre serentak, katalog 10.0 it/s, beli 1.0 it/s | 194.5 | 5 ms | 24 ms | 0.00% | PASS |
