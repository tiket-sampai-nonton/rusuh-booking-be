# Pengujian: rusuh-booking-be

Semua pengujian lintas service ada di folder ini. Test unit dan integrasi per service ada di masing-masing `services/<nama>/`.
Test Plan dan ID TC: [`docs/analisa/06-TP-RBT-001.md`](../docs/analisa/06-TP-RBT-001.md).

## Peta folder

| Folder | Isi | Cara menjalankan |
|---|---|---|
| `e2e/` | E2E Go lintas service (build tag `e2e`): perjalanan penuh sampai e-tiket, pending dan deny, webhook palsu/nominal salah/duplikat, hold duplikat, akses tidak sah, konkurensi stok, expiry dan settlement terlambat | `make test-e2e` atau `tools/e2e.sh` (`E2E_SLOW=1` untuk skenario expiry, sekitar 7 menit). Setiap respons divalidasi terhadap `docs/contracts/openapi` (matikan dengan `E2E_CONTRACT=0`) |
| `features/` | 31 skenario Gherkin (BDD), dijalankan oleh godog dari `e2e/` | ikut `tools/e2e.sh` |
| `ui/` | UI otomatis: Cucumber-js + Playwright (desktop dan mobile viewport) dan Appium (Android, belum dijalankan di perangkat) | lihat [`ui/README.md`](ui/README.md) |
| `k6/` | Uji beban waiting room dan API, analisis kapasitas dan perbandingan lingkungan | lihat [`k6/README.md`](k6/README.md) |
| `pentest/` | Rencana dan hasil pentest dengan alat open source (ZAP, nuclei, Schemathesis, testssl, trivy, gitleaks, semgrep) | lihat [`pentest/README.md`](pentest/README.md) |
| `reports/` | Generator laporan TC dan hasil yang sudah dirangkum | `node tests/reports/gen-tc-report.mjs` |

Yang di-commit ke repo hanya laporan (HTML dan Markdown), kode suite yang diperlukan untuk menjalankan tes, dan README ini. Keluaran mentah (CSV, JSON, log, trace, video, hasil scan) tidak di-commit dan dibuat ulang setiap run.

## Laporan HTML

GitHub menampilkan berkas `.html` sebagai kode sumber, bukan halaman. Untuk membacanya langsung di browser, buka tautan relatif di bawah dari clone lokal, atau gunakan `https://htmlpreview.github.io/?https://github.com/tiket-sampai-nonton/rusuh-booking-be/blob/<branch>/<path>` setelah branch di-push.

### k6 (waiting room dan API)

| Lingkungan | Jenis | Laporan ringkas | Analisis lengkap | Catatan |
|---|---|---|---|---|
| Lokal | smoke, e2e, concurrency, joinrate, load, spike, soak, stress, waiting, readonly | [`k6/reports/local/`](k6/reports/local/) (per folder: `report.html`) | `analysis.html` per folder | Stack Docker di laptop, 13 Nov 2026. Stress menunjukkan titik jenuh sekitar 1.300–1.800 pengantre. Rinciannya di [`k6/reports/local/RINGKASAN.md`](k6/reports/local/RINGKASAN.md) |
| Dev | waiting, 3.000 VU (profil C) | [`k6/reports/dev/waiting/report.html`](k6/reports/dev/waiting/report.html) | [`analysis.html`](k6/reports/dev/waiting/analysis.html) | Event Senja di Kota Tua Fest. 0% error HTTP, p95 181 ms, 190 req/dtk. Run 1.000 VU sebelumnya tertimpa run ini. Status FAIL di laporan berasal dari check yang tidak dievaluasi oleh skenario, bukan error server |
| Prod | waiting, 500 VU (profil C) | [`k6/reports/prod/waiting/report.html`](k6/reports/prod/waiting/report.html) | [`analysis.html`](k6/reports/prod/waiting/analysis.html) | Dihentikan otomatis sekitar 20 detik. 5,88% error: semuanya **429 pada join**, dari batas join per IP (bawaan 200 per menit). Bukan ukuran kapasitas. Lihat catatan di bawah |
| Dev dan prod | analisis kapasitas dan perbandingan | [`k6/ANALISIS-KAPASITAS.md`](k6/ANALISIS-KAPASITAS.md) | [`k6/ANALISIS-PERBANDINGAN.md`](k6/ANALISIS-PERBANDINGAN.md) | Model 100 ribu, 300 ribu, dan 1 juta pengantre. Angka yang belum terbukti ditandai tegas |

**Catatan prod (wajib dibaca sebelum mengutip angka):**
- Satu laptop = satu IP. Batas join per IP prod (`RATE_LIMIT_JOIN_PER_IP`, bawaan 200 per menit) membatasi join dari satu sumber. Ini perlindungan yang bekerja, bukan kegagalan. Dev memakai 100.000 karena semua trafik dev datang dari satu IP gateway.
- Karena itu, kapasitas join waiting room di prod **belum terukur**. Mengukurnya butuh banyak IP sumber atau perubahan batas di prod, dan keputusan perubahan itu di tangan pemilik.
- 300.000 pengantre tidak bisa dibangkitkan dari satu laptop. Itu butuh beberapa generator sekaligus, dan tidak akan diuji ke prod PoC.

### TC (tes case)

| Jenis | Laporan | Keterangan |
|---|---|---|
| Ringkasan TC (semua jenis) | [`reports/TC-REPORT.md`](reports/TC-REPORT.md) | Sumber kebenaran: hasil nyata per TC dengan sebab kegagalan |
| API (godog dan Go e2e) | [`reports/api/ringkasan-api.html`](reports/api/ringkasan-api.html) | Terakhir dijalankan 13 Nov 2026 di stack lokal dengan jalur resmi. 27 dari 31 skenario godog lulus. 4 gagal: 2 karena token service belum dipasang, 2 karena skenario masih menunggu 30 detik padahal grace verifikasi 180 detik (ADR-007) |
| UI web (prod dan dev, desktop dan mobile) | [`reports/ui/ringkasan-ui.html`](reports/ui/ringkasan-ui.html) | Gabungan empat run. Prod: 9 dari 9 desktop dan 10 dari 10 mobile. Dev: 9 dari 9 desktop dan 10 dari 10 mobile (run terakhir; galeri dev sudah memakai Pixabay) |
| Bukti tangkapan layar | [`reports/ui/evidence/`](reports/ui/evidence/) | Beberapa TC kunci (katalog, antrean, galeri) |
| Laporan Cucumber penuh | dibuat lokal | `cucumber-*.html` (1,6 sampai 2,8 MB per berkas) tidak di-commit; buat ulang dengan `cd tests/ui/playwright && npx cucumber-js` |
| Appium (Android) | belum dijalankan | 4 skenario sudah ditulis dan lolos dry-run. Tidak ada emulator atau perangkat di mesin ini |

### Pentest

| Jenis | Laporan | Keterangan |
|---|---|---|
| Laporan utama | [`pentest/REPORT.md`](pentest/REPORT.md) | Ringkasan eksekutif, tabel temuan, dan batasan. Tidak ada temuan High atau Critical. 3 Medium, 8 Low |
| Rencana | [`pentest/PLAN.md`](pentest/PLAN.md) | Ruang lingkup, aturan main, dan alat |
| ZAP API lokal (booking) | [`pentest/evidence/zap-api-lokal-2026-11-13-booking.html`](pentest/evidence/zap-api-lokal-2026-11-13-booking.html) | Contoh laporan ZAP. Lima service ada di [`pentest/results/zap-api-lokal-2026-11-13/`](pentest/results/zap-api-lokal-2026-11-13/) |
| ZAP baseline (dev, pasif) | [`pentest/evidence/zap-baseline-dev-2026-11-13.html`](pentest/evidence/zap-baseline-dev-2026-11-13.html) | Pasif, tanpa serangan aktif |
| TLS (dev) | [`pentest/evidence/testssl-dev-2026-11-13.html`](pentest/evidence/testssl-dev-2026-11-13.html) | Protokol dan cipher. TLS 1.0 dan 1.1 masih aktif |
| Ringkasan mentah | [`pentest/evidence/RINGKASAN-2026-11-13.md`](pentest/evidence/RINGKASAN-2026-11-13.md) | Angka per alat |

Yang belum diuji di pentest: prod (tidak discan, perlu persetujuan terpisah), dev aktif (dilarang), rate limit join tanpa override, dan ZAP terautentikasi.

## Menjalankan cepat

```bash
# API lintas service (stack Docker lokal dan host)
make test-e2e

# UI desktop dan mobile (prod, bisa diarahkan ke dev lewat BASE_URL)
cd tests/ui/playwright && npm install && npx cucumber-js

# k6 waiting room lokal
cd tests/k6 && npm run test:waiting
```

Sebelum menjalankan yang memakai stack Docker lokal, pastikan tidak ada tes lain yang berjalan. Tes yang memakai stack lokal bergantian lewat kunci `/tmp/rbt-stack.lock`.

## Konfigurasi yang perlu diketahui

- **Valkey lokal** dipublikasikan di host port `6380`, bukan `6379`. Skrip `tools/e2e.sh` masih mengharapkan `6379`, jadi jalur resmi perlu override (lihat catatan di `TC-REPORT.md`).
- **Postgres lokal** dipublikasikan di `5433`. `Makefile` dan `.env.example` masih `5432`.
- **E2E service token** (`E2E_SERVICE_TOKEN`) belum dipasang di `tools/e2e.sh`, jadi dua skenario payment-lock gagal dengan 401.

Temuan konfigurasi di luar kode (infra, CORS, rate limit, Cloudflare) dirangkum di README root.
