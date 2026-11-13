# Analisis kapasitas: soak @ local (2026-11-13T01:01:53.638Z)

Status: PASS

```text
CAPACITY ANALYSIS

Maximum Tested Load               : soak: 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s
Maximum Tested Virtual Users      : 270
Peak Concurrent VUs               : 150
Maximum Observed RPS (1 s peak)   : 747.0
Maximum Level Avg RPS             : 186.1
Overall Avg RPS                   : 182.5
Overall P95 / P99                 : 2 ms / 5 ms
Overall Error Rate                : 0.00%

Maximum Sustainable Capacity      : soak: 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s
Maximum Sustainable RPS           : 186.1
Recommended Operating RPS (70%)   : 130.3
Latency Knee (p95 > 2x awal / SLO): tidak teramati
Error Rate Starts Rising          : tidak teramati
Breaking Point (status FAIL)      : tidak tercapai di rentang uji
p95 menit pertama -> terakhir     : 1 ms -> 1 ms
Memori rabbitmq                   : 239.7 -> 264.4 MB (+10%)
Memori notification-worker        : 18.3 -> 18.6 MB (+2%)
Memori notification-api           : 11.3 -> 11.4 MB (+2%)
Memori payment-worker             : 10.7 -> 10.9 MB (+1%)
Hottest Container at Top Level    : queue-worker CPU avg 27%
```

| Level | Beban | RPS | p95 | p99 | Error | Status |
|---|---|---:|---:|---:|---:|---|
| soak | 150 pengantre serentak, katalog 15.0 it/s, beli 1.5 it/s | 186.1 | 2 ms | 5 ms | 0.00% | PASS |
