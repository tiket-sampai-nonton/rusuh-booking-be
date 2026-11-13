# Benchmark performa: local

Dibuat dan diperbarui otomatis oleh `tests/k6/scripts/report.mjs` setiap run. Folder per jenis tes hanya menyimpan hasil
terbaru; perbandingan antar-run ada di riwayat di bawah. Teks di luar blok otomatis boleh diedit manual.

## Hasil terbaru per jenis

<!-- auto:terbaru:mulai -->
| Jenis | Waktu (WIB) | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | Status | Laporan |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| concurrency | 2026-11-13 08:48:13 | 99fd2b2ae9* | 1 | 126 | 1.9 | 39.0 | 10 ms | 15 ms | 18.25% | PASS | [analysis](concurrency/analysis.html) |
| e2e | 2026-11-13 08:47:54 | 99fd2b2ae9* | 3 | 177 | 9.3 | 60.0 | 10 ms | 159 ms | 0.00% | PASS | [analysis](e2e/analysis.html) |
| joinrate | 2026-11-13 08:49:19 | 99fd2b2ae9* | 200 | 97500 | 647.7 | 1513.0 | 2 ms | 9 ms | 0.00% | PASS | [analysis](joinrate/analysis.html) |
| load | 2026-11-13 08:51:51 | 99fd2b2ae9* | 420 | 67844 | 193.5 | 808.0 | 2 ms | 4 ms | 0.00% | PASS | [analysis](load/analysis.html) |
| readonly | 2026-11-13 09:14:54 | 99fd2b2ae9* | 1000 | 58435 | 180.6 | 522.0 | 1 ms | 4 ms | 0.00% | PASS | [analysis](readonly/analysis.html) |
| smoke | 2026-11-13 08:47:46 | 99fd2b2ae9* | 1 | 57 | 7.7 | 35.0 | 9 ms | 362 ms | 0.00% | PASS | [analysis](smoke/analysis.html) |
| soak | 2026-11-13 08:01:53 | 99fd2b2ae9* | 270 | 65708 | 182.5 | 747.0 | 2 ms | 5 ms | 0.00% | PASS | [analysis](soak/analysis.html) |
| spike | 2026-11-13 09:04:46 | 99fd2b2ae9* | 1120 | 103748 | 471.4 | 1498.0 | 2 ms | 4 ms | 0.00% | PASS | [analysis](spike/analysis.html) |
| stress | 2026-11-13 08:57:42 | 99fd2b2ae9* | 2594 | 394167 | 936.0 | 2874.0 | 123 ms | 2004 ms | 0.00% | WARNING | [analysis](stress/analysis.html) |
| waiting | 2026-11-13 09:13:17 | 99fd2b2ae9* | 3000 | 56571 | 586.6 | 1083.0 | 2 ms | 8 ms | 0.00% | PASS | [analysis](waiting/analysis.html) |

\* commit memiliki perubahan yang belum di-commit saat run.
<!-- auto:terbaru:selesai -->

## Riwayat benchmark

Satu baris per run, terbaru di atas. RPS berkelanjutan = level tertinggi yang memenuhi SLO (hanya tes beban).

<!-- auto:riwayat -->
| Waktu (WIB) | Jenis | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | RPS berkelanjutan | Status |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-13 09:14:54 | readonly | 99fd2b2ae9* | 1000 | 58435 | 180.6 | 522.0 | 1 ms | 4 ms | 0.00% | 432.7 | PASS |
| 2026-11-13 09:13:17 | waiting | 99fd2b2ae9* | 3000 | 56571 | 586.6 | 1083.0 | 2 ms | 8 ms | 0.00% | 586.6 | PASS |
| 2026-11-13 09:11:40 | waiting | 99fd2b2ae9* | 1000 | 21374 | 223.5 | 426.0 | 2 ms | 5 ms | 0.00% | 223.5 | PASS |
| 2026-11-13 09:10:03 | waiting | 99fd2b2ae9* | 1000 | 24026 | 251.2 | 521.0 | 7 ms | 91 ms | 0.00% | 251.2 | PASS |
| 2026-11-13 09:08:27 | waiting | 99fd2b2ae9* | 1000 | 44772 | 470.5 | 918.0 | 1 ms | 4 ms | 0.00% | 470.5 | PASS |
| 2026-11-13 09:04:46 | spike | 99fd2b2ae9* | 1120 | 103748 | 471.4 | 1498.0 | 2 ms | 4 ms | 0.00% | 193.9 | PASS |
| 2026-11-13 08:57:42 | stress | 99fd2b2ae9* | 2594 | 394167 | 936.0 | 2874.0 | 123 ms | 2004 ms | 0.00% | 1523.5 | WARNING (breaking S6) |
| 2026-11-13 08:51:51 | load | 99fd2b2ae9* | 420 | 67844 | 193.5 | 808.0 | 2 ms | 4 ms | 0.00% | 348.3 | PASS |
| 2026-11-13 08:49:19 | joinrate | 99fd2b2ae9* | 200 | 97500 | 647.7 | 1513.0 | 2 ms | 9 ms | 0.00% | 1500.0 | PASS |
| 2026-11-13 08:48:13 | concurrency | 99fd2b2ae9* | 1 | 126 | 1.9 | 39.0 | 10 ms | 15 ms | 18.25% | - | PASS |
| 2026-11-13 08:47:54 | e2e | 99fd2b2ae9* | 3 | 177 | 9.3 | 60.0 | 10 ms | 159 ms | 0.00% | - | PASS |
| 2026-11-13 08:47:46 | smoke | 99fd2b2ae9* | 1 | 57 | 7.7 | 35.0 | 9 ms | 362 ms | 0.00% | - | PASS |
| 2026-11-13 08:42:48 | readonly | 99fd2b2ae9* | 50 | 1099 | 13.2 | 35.0 | 2 ms | 21 ms | 0.00% | 21.6 | PASS |
| 2026-11-13 08:40:17 | joinrate | 99fd2b2ae9* | 204 | 97496 | 647.6 | 1528.0 | 2 ms | 8 ms | 0.00% | 1500.0 | PASS |
| 2026-11-13 08:37:25 | waiting | 99fd2b2ae9* | 3000 | 305918 | 1792.2 | 2261.0 | 3 ms | 22 ms | 0.00% | 1792.2 | PASS |
| 2026-11-13 08:36:51 | e2e | 99fd2b2ae9* | 3 | 181 | 8.7 | 62.0 | 6 ms | 203 ms | 0.00% | - | PASS |
| 2026-11-13 08:36:37 | smoke | 99fd2b2ae9* | 1 | 61 | 5.7 | 38.0 | 6 ms | 48 ms | 0.00% | - | PASS |
| 2026-11-13 08:35:13 | e2e | 99fd2b2ae9* | 3 | 249 | 5.6 | 39.0 | 7 ms | 102 ms | 0.00% | - | WARNING |
| 2026-11-13 08:34:36 | smoke | 99fd2b2ae9* | 1 | 84 | 2.3 | 35.0 | 17 ms | 128 ms | 0.00% | - | FAIL (breaking all) |
| 2026-11-13 08:26:52 | stress | 99fd2b2ae9* | 1942 | 413955 | 984.4 | 2821.0 | 11 ms | 85 ms | 0.00% | 2049.4 | PASS |
| 2026-11-13 08:23:51 | waiting | 99fd2b2ae9* | 3000 | 306252 | 1787.6 | 2312.0 | 1 ms | 3 ms | 0.00% | 1787.6 | PASS |
| 2026-11-13 08:20:58 | waiting | 99fd2b2ae9* | 3000 | 86174 | 505.3 | 793.0 | 3 ms | 24 ms | 0.00% | 505.3 | PASS |
| 2026-11-13 08:18:05 | waiting | 99fd2b2ae9* | 3000 | 68201 | 398.8 | 621.0 | 2 ms | 9 ms | 0.00% | 398.8 | PASS |
| 2026-11-13 08:17:51 | waiting | 99fd2b2ae9* | 3000 | 3274 | 308.7 | 502.0 | 3 ms | 6 ms | 0.00% | 308.7 | PASS |
| 2026-11-13 08:17:20 | waiting | 99fd2b2ae9* | 1000 | 3513 | 129.0 | 200.0 | 6 ms | 14 ms | 0.00% | 129.0 | PASS |
| 2026-11-13 08:15:12 | waiting | 99fd2b2ae9* | 1000 | 31073 | 247.9 | 527.0 | 2 ms | 6 ms | 0.00% | 247.9 | PASS |
| 2026-11-13 08:12:07 | waiting | 99fd2b2ae9* | 1000 | 68528 | 545.1 | 915.0 | 2 ms | 7 ms | 0.00% | 545.1 | PASS |
| 2026-11-13 08:10:00 | waiting | 99fd2b2ae9* | 1000 | 34744 | 276.3 | 455.0 | 2 ms | 6 ms | 0.00% | 276.3 | PASS |
| 2026-11-13 08:07:54 | waiting | 99fd2b2ae9* | 1000 | 30289 | 241.1 | 455.0 | 2 ms | 5 ms | 0.00% | 241.1 | PASS |
| 2026-11-13 08:01:53 | soak | 99fd2b2ae9* | 270 | 65708 | 182.5 | 747.0 | 2 ms | 5 ms | 0.00% | 186.1 | PASS |
| 2026-11-13 07:58:11 | spike | 99fd2b2ae9* | 1128 | 103467 | 470.0 | 1602.0 | 46 ms | 164 ms | 0.00% | 197.2 | PASS |
| 2026-11-13 07:51:07 | stress | 99fd2b2ae9* | 2217 | 401763 | 954.4 | 3930.0 | 64 ms | 546 ms | 0.00% | 1040.7 | WARNING (breaking S5) |
| 2026-11-13 07:45:15 | load | 99fd2b2ae9* | 420 | 67907 | 193.7 | 952.0 | 2 ms | 4 ms | 0.00% | 348.7 | PASS |
| 2026-11-13 07:44:07 | concurrency | 99fd2b2ae9* | 1 | 128 | 1.9 | 39.0 | 7 ms | 96 ms | 17.97% | - | PASS |
| 2026-11-13 07:43:47 | e2e | 99fd2b2ae9* | 3 | 179 | 9.1 | 60.0 | 28 ms | 93 ms | 0.00% | - | PASS |
| 2026-11-13 07:43:39 | smoke | 99fd2b2ae9* | 1 | 58 | 7.5 | 38.0 | 11 ms | 56 ms | 0.00% | - | PASS |
| 2026-11-10 18:04:20 | smoke | aa2b071be9* | 1 | 54 | 10.7 | 37.0 | 15 ms | 163 ms | 0.00% | - | PASS |
| 2026-11-10 14:04:40 | waiting | aa2b071be9* | 200 | 2524 | 38.5 | 114.0 | 8 ms | 16 ms | 0.00% | 38.5 | PASS |
| 2026-11-10 14:03:51 | smoke | aa2b071be9* | 1 | 54 | 11.9 | 36.0 | 13 ms | 147 ms | 0.00% | - | PASS |
| 2026-11-10 13:57:40 | soak | aa2b071be9* | 270 | 58930 | 172.9 | 360.0 | 4 ms | 10 ms | 0.00% | 179.7 | PASS |
| 2026-11-10 13:53:43 | spike | aa2b071be9* | 1124 | 102192 | 464.3 | 1488.0 | 20 ms | 85 ms | 0.00% | 194.5 | PASS |
| 2026-11-10 13:45:53 | stress | aa2b071be9* | 2556 | 385486 | 915.6 | 3161.0 | 504 ms | 2119 ms | 0.00% | 1473.2 | WARNING (breaking S6) |
| 2026-11-10 13:39:38 | load | aa2b071be9* | 420 | 65687 | 187.4 | 385.0 | 5 ms | 15 ms | 0.10% | 348.3 | PASS |
| 2026-11-10 13:38:19 | concurrency | aa2b071be9* | 1 | 127 | 1.9 | 39.0 | 17 ms | 24 ms | 18.11% | - | PASS |
| 2026-11-10 13:38:04 | e2e | aa2b071be9* | 3 | 147 | 13.2 | 34.0 | 19 ms | 118 ms | 0.00% | - | PASS |

## Analisis pass 2026-11-13 (lokal, berurutan)

Sebelas profil dijalankan berurutan dalam satu sesi (08:47 sampai 09:20 WIB) di bawah kunci `/tmp/rbt-stack.lock`, dengan
kode `99fd2b2ae9` dan working tree yang berubah (99 berkas belum di-commit; lihat `run.json`). Semua kontainer tetap
`healthy` di setiap jeda antar-profil (13 dari 13). Soak tidak dijalankan di pass ini; angka soak di tabel berasal dari
run 08:01 yang sebelum perubahan skrip terakhir.

**Mesin dan stack.** Apple M4, 10 core, 16 GiB. Docker Desktop: 8 vCPU, 7,75 GiB, Docker Engine 29.5.2. k6 0.57.0.
Stack: compose repo + `env/local.compose.yaml` (batas per IP dilonggarkan). PostgreSQL 16, Valkey, RabbitMQ, Mailpit,
lima service (api dan worker). k6 berjalan di mesin yang sama dengan stack.

| Profil | Hasil | Angka kunci |
|---|---|---|
| smoke | PASS | 57 request, p95 9 ms (p99 362 ms: satu sampel awal, dingin) |
| e2e | PASS | 177 request, semua check bisnis 100% (transaksi sampai PAID, e-tiket, email) |
| concurrency | PASS | checks 100%; "gagal 18,25%" adalah jawaban 503 `DEPENDENCY_UNAVAILABLE` yang memang diharapkan untuk Idempotency-Key kembar yang masih diproses |
| joinrate | PASS | sampai **1.500 join/dtk**: 97.491 iterasi selesai, 0 terputus, p95 2,5 ms; belum jenuh |
| load (L1 sampai L4) | PASS | 420 VU, 194 RPS rata-rata, p95 1,3 ms di L4 |
| spike | PASS | lonjakan 1.000 pengantre (1.120 VU), 1.149 RPS di puncak level; pemulihan PASS |
| waiting A 1.000 VU | PASS | 44.772 request, 470 RPS, p95 1 ms |
| waiting B 1.000 VU | PASS | 24.026 request, 251 RPS, p95 7 ms, p99 91 ms |
| waiting C 1.000 VU | PASS | 21.374 request, 224 RPS (sekitar 2,1 kali lebih sedikit request daripada A) |
| waiting C 3.000 VU | PASS | 56.571 request, 587 RPS, p95 2 ms; folder ini menyimpan run ini |
| readonly 1.000 VU | PASS | 433 RPS di level 1.000 (katalog + queue-status tanpa cache), p95 1,1 ms |
| stress | WARNING | breaking point di S6 (lihat di bawah) |

Catatan: `waiting` menimpa folder yang sama tiap run, jadi analysis.html untuk A, B, dan C 1.000 VU tidak disimpan. Angkanya
ada di tabel di atas dan di riwayat.

**Stress, per level** (pengantre serentak + katalog/dtk + beli/dtk; angka dari `stress/levels.csv`):

| Level | Beban | RPS | p95 | p99 | Error | Total CPU (core-setara M4) | Postgres CPU avg (maks) | Status |
|---|---|---:|---:|---:|---:|---:|---:|---|
| S1 | 200 + 20 + 2 | 232 | 2 ms | 4 ms | 0% | 0,78 | - | PASS |
| S2 | 400 + 40 + 4 | 466 | 2 ms | 4 ms | 0% | 0,97 | - | PASS |
| S3 | 600 + 60 + 6 | 725 | 2 ms | 7 ms | 0% | 1,21 | - | PASS |
| S4 | 900 + 90 + 9 | 1.041 | 2 ms | 4 ms | 0% | 1,35 | 17% (25%) | PASS |
| S5 | 1.300 + 130 + 13 | 1.524 | 3 ms | 11 ms | 0% | 1,90 | 41% (123%) | PASS |
| S6 | 1.800 + 180 + 18 | 1.657 | 2.088 ms | 4.916 ms | 0% | 4,37 | 147% (308%) | FAIL (latensi) |

Lock wait PostgreSQL: 0 sampai S5, maksimum 7 sesi di S6. Maks queue-api 129% dan booking-api 159% di S6.

Bacaan:
- **Titik jenuh teramati: S5 sampai S6.** S5 adalah kapasitas berkelanjutan (sekitar 1.524 RPS, 70% = sekitar 1.070 RPS).
  S6 melewati SLO p95 2 detik, tanpa error 5xx atau timeout.
- **Bukan batas CPU host.** Dari S5 ke S6 RPS naik 9%, tetapi CPU total seluruh kontainer naik 130% (1,9 ke 4,4 core-setara)
  pada host yang punya 8 vCPU. Kerja per request membengkak, dan lock wait Postgres baru muncul di S6. Pola ini konsisten
  dengan kontensi baris stok (hipotesis dari pass 2026-11-10, belum terbukti: perlu `pg_stat_statements` dan query lock).
- Titik jenuh **tidak stabil antar-run**: run 07:51 (kode lebih awal) gagal di S5 dengan WARNING, run 08:26 PASS penuh
  (1.942 VU maksimum), run 08:57 gagal di S6, dan run 2026-11-10 gagal di S6. Satu pengukuran tidak cukup untuk klaim
  kapasitas; kapasitas di sini adalah rentang S5 sampai S6.
- Di S5, seluruh kontainer menjumlah sekitar 1,9 core setara M4. Ini kalibrasi kasar untuk perencanaan, bukan ukuran
  t4g (lihat `ANALISIS-KAPASITAS.md`).

**Batas pass ini:** k6 dan stack berbagi satu laptop (k6 memakai CPU yang sama dengan service); sampler Docker tiap 4 detik;
angka join dibatasi laju per IP (100.000 per menit, sekitar 1.667 per detik), jadi uji di atas 1.500 join/dtk menyentuh
batas itu, bukan kapasitas service.

## Analisis run lokal 2026-11-10 (riwayat)

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
