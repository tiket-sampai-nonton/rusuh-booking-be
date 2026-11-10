# Analisis kapasitas: soak @ local (2026-11-10T06:57:40.835Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : soak: 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s
Maximum Tested Virtual Users      : 270
Peak Concurrent VUs               : 151
Maximum Observed RPS (1 s peak)   : 360.0
Maximum Level Avg RPS             : 179.7
Overall Avg RPS                   : 172.9
Overall P95 / P99                 : 4 ms / 10 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : soak: 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s
Maximum Sustainable RPS           : 179.7
Recommended Operating RPS (70%)   : 125.8
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
p95 menit pertama -> terakhir     : 6 ms -> 4 ms
Memori payment-api                : 14.3 -> 15.0 MB (+5%)
Memori valkey                     : 33.2 -> 34.4 MB (+4%)
Memori booking-worker             : 15.8 -> 16.1 MB (+2%)
Memori user-api                   : 22.3 -> 22.5 MB (+1%)
Hottest Container at Top Level    : queue-api CPU avg 9%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| soak | 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s | 179.7 | 4 ms | 10 ms | 0.00% | PASS |
