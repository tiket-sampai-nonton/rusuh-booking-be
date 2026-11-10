# Benchmark performa: local

Dibuat dan diperbarui otomatis oleh `tests/k6/scripts/report.mjs` setiap run. Folder per jenis tes hanya menyimpan hasil
terbaru; perbandingan antar-run ada di riwayat di bawah. Teks di luar blok otomatis boleh diedit manual.

## Hasil terbaru per jenis

<!-- auto:terbaru:mulai -->
| Jenis | Waktu (WIB) | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | Status | Laporan |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| concurrency | 2026-11-10 13:38:19 | aa2b071be9* | 1 | 127 | 1.9 | 39.0 | 17 ms | 24 ms | 18.11% | PASS | [analysis](concurrency/analysis.html) |
| e2e | 2026-11-10 13:38:04 | aa2b071be9* | 3 | 147 | 13.2 | 34.0 | 19 ms | 118 ms | 0.00% | PASS | [analysis](e2e/analysis.html) |
| load | 2026-11-10 13:39:38 | aa2b071be9* | 420 | 65687 | 187.4 | 385.0 | 5 ms | 15 ms | 0.10% | PASS | [analysis](load/analysis.html) |
| smoke | 2026-11-10 18:04:20 | aa2b071be9* | 1 | 54 | 10.7 | 37.0 | 15 ms | 163 ms | 0.00% | PASS | [analysis](smoke/analysis.html) |
| soak | 2026-11-10 13:57:40 | aa2b071be9* | 270 | 58930 | 172.9 | 360.0 | 4 ms | 10 ms | 0.00% | PASS | [analysis](soak/analysis.html) |
| spike | 2026-11-10 13:53:43 | aa2b071be9* | 1124 | 102192 | 464.3 | 1488.0 | 20 ms | 85 ms | 0.00% | PASS | [analysis](spike/analysis.html) |
| stress | 2026-11-10 13:45:53 | aa2b071be9* | 2556 | 385486 | 915.6 | 3161.0 | 504 ms | 2119 ms | 0.00% | WARNING | [analysis](stress/analysis.html) |
| waiting | 2026-11-10 14:04:40 | aa2b071be9* | 200 | 2524 | 38.5 | 114.0 | 8 ms | 16 ms | 0.00% | PASS | [analysis](waiting/analysis.html) |

\* commit memiliki perubahan yang belum di-commit saat run.
<!-- auto:terbaru:selesai -->

## Riwayat benchmark

Satu baris per run, terbaru di atas. RPS berkelanjutan = level tertinggi yang memenuhi SLO (hanya tes beban).

<!-- auto:riwayat -->
| Waktu (WIB) | Jenis | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | RPS berkelanjutan | Status |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-10 18:04:20 | smoke | aa2b071be9* | 1 | 54 | 10.7 | 37.0 | 15 ms | 163 ms | 0.00% | - | PASS |
| 2026-11-10 14:04:40 | waiting | aa2b071be9* | 200 | 2524 | 38.5 | 114.0 | 8 ms | 16 ms | 0.00% | 38.5 | PASS |
| 2026-11-10 14:03:51 | smoke | aa2b071be9* | 1 | 54 | 11.9 | 36.0 | 13 ms | 147 ms | 0.00% | - | PASS |
| 2026-11-10 13:57:40 | soak | aa2b071be9* | 270 | 58930 | 172.9 | 360.0 | 4 ms | 10 ms | 0.00% | 179.7 | PASS |
| 2026-11-10 13:53:43 | spike | aa2b071be9* | 1124 | 102192 | 464.3 | 1488.0 | 20 ms | 85 ms | 0.00% | 194.5 | PASS |
| 2026-11-10 13:45:53 | stress | aa2b071be9* | 2556 | 385486 | 915.6 | 3161.0 | 504 ms | 2119 ms | 0.00% | 1473.2 | WARNING (breaking S6) |
| 2026-11-10 13:39:38 | load | aa2b071be9* | 420 | 65687 | 187.4 | 385.0 | 5 ms | 15 ms | 0.10% | 348.3 | PASS |
| 2026-11-10 13:38:19 | concurrency | aa2b071be9* | 1 | 127 | 1.9 | 39.0 | 17 ms | 24 ms | 18.11% | - | PASS |
| 2026-11-10 13:38:04 | e2e | aa2b071be9* | 3 | 147 | 13.2 | 34.0 | 19 ms | 118 ms | 0.00% | - | PASS |

## Analisis

Analisis run lokal 2026-11-10 (k6 dan semua service di satu laptop: 10 core host, Docker 8 vCPU / 7,75 GB; stack dengan override `tests/k6/env/local.compose.yaml`).

### Validasi bisnis

- **E2E (6 transaksi)**: admitted, claim, sesi, hold `PENDING_PAYMENT`, attempt di Midtrans Sandbox `PENDING`, webhook
  settlement diterima, booking `PAID`, tepat satu e-tiket, status pembayaran `PAID/SUCCEEDED`, email e-tiket sampai di Mailpit,
  alur akun OTP (me, riwayat, refresh, logout). Semua 100%.
- **Concurrency (15 invariant)**: 10 join serentak dengan `seq` unik; admission token kembar menghasilkan tepat satu sesi;
  hold kembar (key sama) tepat satu booking; hold kedua di sesi yang sama ditolak; attempt kembar tepat satu `attemptNo`;
  10 webhook settlement bersamaan menghasilkan satu `PAID` dan satu e-tiket; reservasi dan hold tidak melebihi stok 4;
  sisa stok tidak negatif. Semua 100%.

### Analisis kapasitas (stress)

Beban per level: pengantre serentak (VU) + katalog (iterasi/s) + pembeli (iterasi/s). Angka diukur hanya selama hold.

| Level | Beban | RPS | p95 | p99 | Error | CPU teratas (rata-rata, 100% = 1 core) | Status |
|---|---|---:|---:|---:|---:|---|---|
| S1 | 200 VU, 20 + 2 it/s | 240,9 | 4 ms | 13 ms | 0% | - | PASS |
| S2 | 400 VU, 40 + 4 it/s | 466,0 | 40 ms | 177 ms | 0% | - | PASS |
| S3 | 600 VU, 60 + 6 it/s | 699,0 | 3 ms | 35 ms | 0% | - | PASS |
| S4 | 900 VU, 90 + 9 it/s | 1.052,2 | 24 ms | 128 ms | 0% | queue-api 29%, rabbitmq 14%, booking-api 13%, postgres 13% | PASS |
| S5 | 1.300 VU, 130 + 13 it/s | 1.473,2 | 12 ms | 153 ms | 0% | queue-api 40%, postgres 29%, booking-api 23% | PASS |
| S6 | 1.800 VU, 180 + 18 it/s | 1.708,6 | 1.985 ms | 3.809 ms | 0% | **postgres 118%**, queue-api 106%, booking-api 84% | FAIL (latensi) |

```text
CAPACITY ANALYSIS (lokal, satu laptop)

Maximum Tested Capacity          : S6 = 1.800 pengantre serentak + 180 iterasi katalog/s + 18 pembeli/s (2.556 VU k6)
Maximum Observed Throughput      : 3.161 RPS puncak 1 detik; 1.708,6 RPS rata-rata level S6
Maximum Sustainable Capacity     : S5 = 1.300 pengantre + 130 katalog/s + 13 pembeli/s, sekitar 1.473 RPS (p95 12 ms, 0% error)
Recommended Operating Capacity   : sekitar 1.030 RPS (70% dari S5) di mesin setara
Latency Knee                     : S6 (p95 naik dari 12 ms ke 1.985 ms)
Error Rate Starts Rising         : tidak teramati (0% di semua level; degradasi berupa latensi dan dropped iterations)
Breaking Point                   : S6, throughput berhenti naik (1.709 RPS, bukan sekitar 2.050) dan 1.339 iterasi terbuang
```

### Jawaban pertanyaan akhir

1. **Pengguna serentak yang tertangani**: sampai **1.300 pengantre serentak** ditambah 130 iterasi katalog per detik dan
   13 pembeli per detik (S5) dalam SLO (p95 < 500 ms, p99 < 1 s, error < 1%). Ini kapasitas laptop lokal, bukan produksi.
2. **RPS yang tertangani**: sekitar **1.470 RPS** berkelanjutan; puncak sesaat 3.161 RPS.
3. **Latensi mulai naik signifikan**: di **S6** (1.800 pengantre, sekitar 1.700 RPS). p95 naik dari 12 ms ke 1,99 s.
4. **Error rate mulai naik**: **tidak teramati**. Sampai S6 tidak ada 5xx atau timeout; sistem melambat, tidak gagal.
5. **Recommended operating capacity**: sekitar **1.000 RPS** (70% dari S5) di mesin setara.
6. **Bottleneck utama**: **PostgreSQL pada jalur tulis transaksional**. Di S6 CPU Postgres naik 4 kali (29% ke 118%) saat beban
   naik 1,4 kali, dan endpoint paling lambat semuanya menulis ke database: `booking.hold` p95 2,9 s, `session.create` 2,0 s,
   `booking.cancel` 1,0 s. Sampler mencatat sampai 4 sesi menunggu lock saat spike. Hipotesis (belum terbukti penuh): semua
   pembeli satu event memperbarui baris stok yang sama (`event_supply`), sehingga terjadi kontensi baris.
   `queue.me` menyumbang porsi request terbesar (114.843 dari 385.486 di stress).
7. **Yang dioptimasi sebelum produksi atau trafik naik**:
   - Ukur latensi query dan lock (aktifkan `pg_stat_statements`, `make obs`) lalu konfirmasi atau tolak hipotesis kontensi stok.
   - Polling bertingkat dan zona jauh tanpa `/me` di FE (poin 2 dan 3) untuk memangkas porsi `queue.me`.
   - Ulangi stress di dev/AWS dengan k6 di mesin terpisah, karena di sini k6 dan stack berebut CPU yang sama.

### Temuan lain

- `queue-status` menjawab 404 sampai pengantre pertama join dan worker menerbitkan Status Document (tiap 1 detik). Klien
  harus memperlakukan 404 sebagai "belum ada antrean", bukan error.
- Request kembar dengan Idempotency-Key sama yang masih diproses: booking menjawab 503 `DEPENDENCY_UNAVAILABLE` (sesuai
  kontrak: ulangi dengan key yang sama). Payment menjawab 503 `DEPENDENCY_UNAVAILABLE`, sedangkan kontrak payment untuk 503
  hanya menyebut `PAYMENT_PROVIDER_UNAVAILABLE`: penyimpangan kecil kontrak.
- Rate limit booking dan payment (300/menit per IP) disimpan di memori proses: dengan lebih dari satu instance, batas efektif
  per IP menjadi kelipatan jumlah instance.
- RabbitMQ lokal berisi antrean sisa tes integrasi (`it.outbox-*`, tanpa consumer); bukan backlog service.
- Dashboard bawaan k6 (`report.html`) tidak dibuat untuk run yang sangat pendek (smoke, e2e); `analysis.html` tetap ada.

### Keterbatasan

- Satu mesin untuk k6 dan semua service: angka adalah batas bawah dan tidak mewakili kapasitas produksi.
- Soak hanya 5 menit (memori naik 1 sampai 5%, p95 stabil); soak penuh 30 menit atau lebih belum dijalankan.
- Latensi query database dan pemakaian connection pool per service: NOT MEASURED.
- Pembayaran ditutup dengan webhook bertanda tangan lokal; Midtrans tidak pernah mengonfirmasi pembayaran (Sandbox, tanpa uang).
