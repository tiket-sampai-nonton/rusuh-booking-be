# Benchmark performa: dev

Dibuat dan diperbarui otomatis oleh `tests/k6/scripts/report.mjs` setiap run. Folder per jenis tes hanya menyimpan hasil
terbaru; perbandingan antar-run ada di riwayat di bawah. Teks di luar blok otomatis boleh diedit manual.

## Hasil terbaru per jenis

<!-- auto:terbaru:mulai -->
| Jenis | Waktu (WIB) | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | Status | Laporan |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| waiting | 2026-11-13 10:33:51 | 99fd2b2ae9* | 3000 | 18237 | 190.4 | 468.0 | 181 ms | 253 ms | 0.00% | FAIL | [analysis](waiting/analysis.html) |

\* commit memiliki perubahan yang belum di-commit saat run.
<!-- auto:terbaru:selesai -->

## Riwayat benchmark

Satu baris per run, terbaru di atas. RPS berkelanjutan = level tertinggi yang memenuhi SLO (hanya tes beban).

<!-- auto:riwayat -->
| Waktu (WIB) | Jenis | Commit | VU maks | Request | RPS rata-rata | RPS puncak | p95 | p99 | Error | RPS berkelanjutan | Status |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-13 10:33:51 | waiting | 99fd2b2ae9* | 3000 | 18237 | 190.4 | 468.0 | 181 ms | 253 ms | 0.00% | - | FAIL (breaking all) |
| 2026-11-13 10:31:38 | waiting | 99fd2b2ae9* | 1000 | 24717 | 259.6 | 390.0 | 162 ms | 249 ms | 0.00% | - | FAIL (breaking all) |

## Analisis

(tulis manual)
