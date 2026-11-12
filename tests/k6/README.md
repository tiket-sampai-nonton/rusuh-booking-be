# Uji performa k6: rusuh-booking-be

Uji beban end-to-end terhadap lima service (queue, booking, payment, notification, user) dengan alur bisnis nyata:
katalog, waiting room, admission, sesi booking, hold, pembayaran, e-tiket, dan akun OTP. Benchmark dan analisis:
[reports/local/RINGKASAN.md](reports/local/RINGKASAN.md).

## Struktur

```text
tests/k6/
├── config/
│   ├── environments.js   target per TEST_ENV (local: port per service; dev/prod: satu domain + tunnel admin)
│   ├── profiles.js       jadwal level beban untuk load, stress, spike, soak
│   └── thresholds.js     SLO (bisa diubah lewat env) dan submetric per level/endpoint
├── lib/
│   ├── http.js           satu pintu request: header klien, tag endpoint/level, check, kelas respons
│   ├── data.js           NIK/email fiktif, event uji lewat API admin, login OTP via Mailpit
│   ├── flows.js          alur bisnis (browse, wait, admit, hold, attempt, webhook, account)
│   └── summary.js        handleSummary: summary.json + konteks uji
├── scenarios/
│   ├── smoke.js          1 VU: semua service dan endpoint utama, satu transaksi sampai PAID
│   ├── e2e.js            alur bisnis penuh dengan validasi hasil akhir (PAID, e-tiket, email)
│   ├── concurrency.js    request kembar: idempotency, token sekali pakai, stok, webhook ganda, seq unik
│   ├── traffic.js        campuran trafik realistis untuk load / stress / spike / soak
│   └── waiting-room.js   pola polling waiting room, profil A/B/C (FE sekarang vs sesudah perbaikan)
├── scripts/
│   ├── run.sh            jalankan satu tes + sampler sumber daya + laporan (menimpa hasil jenis yang sama)
│   ├── report.mjs        analysis.html, capacity.md, CSV (Node, tanpa dependensi)
│   └── stack-local.sh    nyalakan stack lokal dengan override uji beban
├── env/local.compose.yaml  override compose khusus uji lokal
├── dashboards/report-template.html
├── reports/{local,dev,prod}/
│   ├── RINGKASAN.md      hasil terbaru per jenis + riwayat benchmark semua run + analisis manual
│   └── <jenis>/          hasil run terbaru jenis itu (ditimpa setiap run)
└── package.json          perintah npm (tanpa dependensi)
```

## Persiapan

1. **k6** >= 0.49 (dashboard web bawaan): `brew install k6`. **Node** >= 18. **Docker** untuk stack lokal.
2. Stack lokal: `npm run stack:up` (dari folder ini) atau `bash tests/k6/scripts/stack-local.sh up` dari root repo.
   Override `env/local.compose.yaml` hanya untuk lokal:
   - Rate limit per IP booking/payment dimatikan, batas join dan OTP per IP dinaikkan, karena semua trafik k6 datang dari
     satu IP. Tanpa ini yang terukur adalah rate limiter, bukan service.
   - `ADMIN_EMAILS=@example.test` agar akun uji bisa membuat event uji.
3. Server key Midtrans Sandbox dibaca dari `services/payment/.env` ke environment proses, dipakai untuk compose dan untuk
   menandatangani webhook uji. Nilainya tidak pernah dicetak atau ditulis ke laporan.

## Menjalankan

```bash
cd tests/k6
npm run test:smoke        # wajib lulus sebelum tes beban
npm run test:e2e          # E2E_VUS=3 E2E_ITERATIONS=2
npm run test:concurrency  # PARALLEL=10
npm run test:load
npm run test:stress
npm run test:spike
npm run test:soak         # SOAK_MINUTES=30 (bawaan)
npm run test:waiting      # PROFILE=A|B|C VUS=200 RAMP=30s HOLD=60s
npm run test:all          # smoke (harus lulus), lalu e2e, concurrency, load, stress, spike (soak terpisah)
```

Variabel lingkungan:

| Variabel | Arti |
|---|---|
| `TEST_ENV` | `local` (bawaan), `dev`, `prod`. Prod butuh `CONFIRM_PROD=yes` |
| `BASE_URL` | domain API untuk dev/prod |
| `ADMIN_URL` | API admin booking lewat tunnel (dev: `deployments/dev/dev-tunnel.sh`, prod: `aws-admin-tunnel.sh`), bawaan `http://127.0.0.1:18082` |
| `QUEUE_ADMIN_URL` | API admin queue untuk `PUT queue-settings` event uji (lokal otomatis `:8081`; dev/prod bawaan `http://127.0.0.1:18081`, tunnel bawaan hanya meneruskan booking sehingga perlu `-L` tambahan ke port queue) |
| `QUEUE_ACTIVE_SLOTS` | `activeSlotCapacity` event uji (bawaan 1000) |
| `AUTH_TOKEN` | token akun admin untuk dev/prod (di lokal login OTP otomatis lewat Mailpit) |
| `LOAD_FACTOR` | pengali semua level beban (mis. `0.5`) |
| `SLO_P95_MS`, `SLO_P99_MS`, `SLO_ERROR_RATE`, `SLO_CHECK_RATE` | SLO untuk PASS/WARNING/FAIL |

Di dev/prod: login OTP dan webhook bertanda tangan tidak tersedia, sehingga E2E berhenti di attempt `PENDING` (bayar manual
di simulator Midtrans). Sumber daya server tidak diukur (NOT MEASURED), dan email di dev/prod benar-benar terkirim, jadi
jangan menjalankan E2E dengan domain email sungguhan.

## Keamanan data uji

- Setiap tes membuat event sendiri bernama `[UJI BEBAN k6] ...` lewat API admin dan membatalkannya di `teardown`.
  Data lama tidak disentuh.
- NIK fiktif yang lolos validasi format; email `@example.test` (domain yang tidak pernah dikirim ke internet; di lokal semua
  masuk Mailpit).
- Pembeli beban (`buy`) selalu membatalkan hold, jadi tidak ada pembayaran. Hanya E2E dan concurrency yang membuat attempt di
  Midtrans **Sandbox**, dalam jumlah kecil; pelunasan memakai webhook bertanda tangan lokal, bukan uang sungguhan.
- Rem darurat: stress, spike, dan soak berhenti otomatis bila error melewati 30%.
- `summary.json` tidak memuat data `setup` (yang berisi token admin).

## Membaca laporan

Setiap run **menimpa** folder `reports/<env>/<jenis>/`, jadi yang di-commit selalu hasil terbaru. Perbandingan antar-run
tidak hilang: `report.mjs` menambah satu baris ke tabel **Riwayat benchmark** di `reports/<env>/RINGKASAN.md` (waktu, commit,
VU, request, RPS, p95/p99, error, RPS berkelanjutan, status) dan membuat ulang tabel **Hasil terbaru per jenis**. Bagian lain
RINGKASAN.md (mis. Analisis) ditulis manual dan tidak pernah ditimpa. Commit bertanda `*` berarti ada perubahan yang belum
di-commit saat run.

Isi folder per jenis:

| File | Isi |
|---|---|
| `analysis.html` | A. ringkasan eksekutif, B. skenario, C. hasil (grafik VU/RPS/latensi/error, per level, per endpoint), D. analisis kapasitas, E. bottleneck dan rekomendasi |
| `report.html` | dashboard deret waktu bawaan k6 (tidak dibuat untuk run sangat pendek) |
| `capacity.md` | blok analisis kapasitas + tabel level |
| `result.json` | angka utama run, sumber tabel di RINGKASAN.md |
| `summary.json` | metrik agregat k6 + konteks uji (jenis, env, SLO, jadwal level) |
| `metrics.json.gz` | sampel mentah per request (tidak di-commit) |
| `levels.csv`, `endpoints.csv`, `timeseries.csv` | angka yang dipakai grafik |
| `resources.csv`, `infra.csv` | CPU/memori per container, koneksi dan lock wait PostgreSQL, backlog RabbitMQ, Valkey (lokal) |
| `run.json` | commit, hash skrip k6, versi k6, parameter: menautkan laporan ke kode yang menjalankannya |

## Istilah

- **VU (virtual user)**: satu "pengguna" k6 yang menjalankan skrip berulang. Bukan jumlah pengguna sungguhan.
- **RPS**: request HTTP per detik yang benar-benar diproses. Satu iterasi pengguna terdiri dari beberapa request.
- **Throughput**: jumlah kerja per waktu (RPS atau iterasi/detik).
- **Latensi**: waktu respons satu request. **p95** = 95% request lebih cepat dari angka ini; **p99** = 99%.
- **Error rate**: porsi request dengan status tak terduga atau timeout. 404/409 yang memang jawaban benar tidak dihitung.
- **Dropped iterations**: iterasi yang seharusnya dimulai pada laju target tetapi tidak sempat; tanda server melambat atau
  generator kekurangan VU.

**Pengguna serentak vs laju request.** `ramping-vus` (pengantre) memodelkan jumlah orang yang aktif bersamaan; bila server
melambat, request per detik ikut turun. `ramping-arrival-rate` (katalog, pembeli) memodelkan kedatangan per detik yang tetap
walau server melambat, sehingga saturasi terlihat sebagai latensi naik dan dropped iterations. Karena itu VU tidak sama dengan
RPS, dan kapasitas disimpulkan dari RPS, latensi, error rate, dropped iterations, dan sumber daya, bukan dari jumlah VU.

## Menentukan kapasitas

- **Maximum tested capacity**: level tertinggi yang dijalankan.
- **Maximum observed throughput**: RPS tertinggi yang teramati (puncak 1 detik dan rata-rata level).
- **Maximum sustainable capacity**: level tertinggi yang, beserta semua level di bawahnya, memenuhi SLO.
- **Recommended operating capacity**: 70% dari kapasitas berkelanjutan, menyisakan ruang untuk lonjakan.
- **Latency knee**: level pertama dengan p95 lebih dari 2 kali level awal atau melewati SLO.
- **Breaking point**: level pertama berstatus FAIL.

## Membaca bottleneck

Bandingkan pertumbuhan beban dengan pertumbuhan sumber daya per level (tabel di bagian E). Container yang CPU-nya naik jauh
lebih cepat dari beban adalah kandidat bottleneck. Jika endpoint yang paling lambat semuanya menulis ke database dan PostgreSQL
memanas atau ada sesi menunggu lock, bottleneck ada di database; jika hanya satu service yang memanas dan endpoint-nya saja yang
lambat, bottleneck ada di aplikasi itu. Backlog RabbitMQ yang tumbuh berarti worker tertinggal.

## PASS / WARNING / FAIL

Per level: **PASS** bila p95, p99, error rate, dan checks memenuhi SLO. **WARNING** bila melewati SLO tetapi masih dalam
2 kali batas latensi dan error. **FAIL** selain itu. Status keseluruhan:

- smoke, e2e, load, soak: level terburuk. Smoke dan concurrency menuntut semua check bisnis lulus.
- stress: PASS bila semua level lulus, WARNING bila breaking point ditemukan setelah minimal satu level lulus, FAIL bila
  level pertama sudah gagal.
- spike: FAIL bila level pemulihan tidak lulus; WARNING bila lonjakan gagal tetapi sistem pulih.

Status hanya PASS bila angka memenuhi SLO; skrip yang sekadar berjalan tanpa error tidak otomatis PASS.
