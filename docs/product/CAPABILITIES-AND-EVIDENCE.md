# Kapabilitas Bisnis dan Bukti Rekayasa

> Bagian dari dokumentasi produk Rusuh Booking Tiket. Kembali ke [README](../../README.md).
> Diverifikasi 13 November 2026 terhadap kode `main` (`99fd2b2`) dan laporan di `tests/`.

**Label status**

| Label | Arti |
|---|---|
| IMPLEMENTED | Ada di kode sumber |
| VERIFIED | Ada bukti tes atau operasional |
| DEPLOYED | Berjalan di dev dan prod (PoC) |
| PARTIAL | Sebagian saja yang ada |
| PLANNED | Rekomendasi, belum ada |

## 1. Kapabilitas per kelompok bisnis

### 1.1 Manajemen event
- **Fungsi:** membuat event, mengatur tiket dan konfigurasi antrean, mengunggah gambar (image, banner, thumbnail; JPEG/PNG/WebP, maks 5 MB), menerbitkan, serta katalog publik dengan filter kota, genre, dan urutan harga.
- **Nilai bisnis:** promotor menyiapkan penjualan tanpa campur tangan tim teknis.
- **Teknis:** Booking `create_event`, `publish_event`, `update_event_config`, `upload_event_image`, `list_events`, `list_facets`; gambar ke S3 di prod. Queue `set_queue_settings`.
- **Status:** IMPLEMENTED, DEPLOYED (prod memuat 10 event). Admin hanya lewat API internal (`/internal/v1/admin/*`); **belum ada dashboard admin**.
- **Bukti:** TC-CAT-01/02/06 lulus ([TC-REPORT](../../tests/reports/TC-REPORT.md)); `docs/contracts/openapi/booking.yaml`.

### 1.2 Autentikasi dan identitas pengguna
- **Fungsi:** pembeli bisa sebagai tamu (NIK + email) atau login opsional lewat OTP email 6 digit tanpa password.
- **Nilai:** hambatan beli rendah, tetapi pembeli yang sering beli tidak perlu mengetik ulang data.
- **Teknis:** User `auth`, `profile`. OTP TTL 5 menit, maks 5 percobaan, cooldown 60 detik, rate limit per email dan IP. Refresh token dirotasi; pemakaian ulang token lama mencabut semua sesi akun (`handleReuse`). NIK dan email terenkripsi AES-256-GCM dengan hash HMAC.
- **Status:** IMPLEMENTED, VERIFIED (unit dan integrasi; coverage User 92,4%), DEPLOYED.

### 1.3 Virtual waiting room
- **Fungsi:** semua pembeli masuk antrean saat penjualan dibuka; posisi dan estimasi ditampilkan; heartbeat menjaga hanya pengantre aktif yang dipromosikan.
- **Nilai:** situs tetap responsif saat lonjakan; urutan adil.
- **Teknis:** Queue `join`, `status`, `heartbeat`, `leave`, `my_entry`; skrip Lua atomik di Valkey; indeks liveness di-shard (`pkg/queueshard`); Status Document di-cache di Cloudflare 1 detik.
- **Status:** IMPLEMENTED, VERIFIED (k6 lokal dan dev), DEPLOYED.
- **Bukti:** [k6 dev 3.000 VU](../../tests/k6/reports/dev/waiting/report.html), [ANALISIS-KAPASITAS](../../tests/k6/ANALISIS-KAPASITAS.md).

### 1.4 Antrean dan admission
- **Fungsi:** promotor menaikkan batch pengantre sesuai stok; pengantre mengklaim giliran dalam tenggat; token admission membuka halaman beli.
- **Nilai:** "Jangan PHP": pembeli yang masuk halaman beli sudah dijamin ada tiket untuknya.
- **Teknis:** Booking `allocate_batch` dipanggil **sebelum** `promote.lua`; `claim.lua` memeriksa visitor dan tenggat; lease promotor mencegah promosi ganda.
- **Status:** IMPLEMENTED, VERIFIED, DEPLOYED.
- **Bukti:** TC-ADM-01 "admission always reserves stock" lulus.
- **Celah diketahui:** token admission belum diikat ke visitor (PT-F-01, Medium) — lihat [pentest](../../tests/pentest/REPORT.md).

### 1.5 Manajemen inventori tiket
- **Fungsi:** stok per kategori berkurang hanya lewat transaksi yang sah; unit yang tidak terpakai kembali ke stok.
- **Nilai:** tidak pernah oversell.
- **Teknis:** satu-satunya pengubah stok adalah Booking, dalam satu transaksi PostgreSQL dengan urutan kunci tetap dan CHECK constraint; `release_allocation`, sweeper.
- **Status:** IMPLEMENTED, VERIFIED (uji paralel stok di E2E), DEPLOYED.
- **Bukti:** TC-ADM-03 lulus; E2E konkurensi stok ([AUDIT-FINAL](../engineering/AUDIT-FINAL.md)).

### 1.6 Booking dan reservasi
- **Fungsi:** hold tiket (maks 3 per NIK pembeli per event, 1 per NIK pemegang), ubah pesanan sebelum bayar, ubah pemegang, batalkan; timer 5 menit ditentukan server; status real-time lewat SSE.
- **Nilai:** pembeli bisa mengoreksi pesanan; kuota mencegah calo.
- **Teknis:** Booking `hold`, `edit`, `edit_holder`, `cancel_booking`, `extend_deadline`, `stream_session`; event sourcing; `Idempotency-Key`.
- **Status:** IMPLEMENTED, VERIFIED, DEPLOYED.
- **Bukti:** TC-BKG-01/02/03/06/08/09, TC-EDT-01/03 lulus. TC-EDT-04 gagal karena token service belum dipasang di harness, bukan bug yang terbukti.

### 1.7 Pemrosesan pembayaran
- **Fungsi:** halaman bayar Midtrans Snap (VA, QRIS, e-wallet, kartu); timer 10 menit; rekonsiliasi saat webhook terlambat.
- **Nilai:** uang dan status booking selalu konsisten; pembayaran ganda tidak menerbitkan tiket ganda.
- **Teknis:** Payment `create_attempt`, `handle_webhook` (signature SHA-512 + cek nominal), `resolve_payment`; event sourcing + outbox. Pembayaran setelah kedaluwarsa masuk REVIEW tanpa tiket (ADR-007).
- **Status:** IMPLEMENTED, VERIFIED (Sandbox), DEPLOYED (Sandbox). **Midtrans production belum.**
- **Bukti:** E2E webhook palsu, nominal salah, duplikat lulus; tes `-tags sandbox` lulus (AUDIT-FINAL).

### 1.8 Penerbitan tiket
- **Fungsi:** e-tiket per pemegang dengan kode QR; halaman tiket hanya bisa dibuka pemilik atau lewat tautan email bertoken HMAC.
- **Nilai:** mencegah pencurian tiket lewat tebakan ID.
- **Teknis:** Booking `settle`, `get_tickets`; Notification `qrcode`.
- **Status:** IMPLEMENTED, VERIFIED, DEPLOYED.

### 1.9 Email dan notifikasi
- **Fungsi:** email OTP, giliran segera/tiba, e-tiket, booking kedaluwarsa, REVIEW; langganan push.
- **Nilai:** pembeli tidak kehilangan giliran atau tiket; gangguan email tidak membatalkan transaksi.
- **Teknis:** Notification `notify_*` (10 usecase), Resend dan SMTP, retry berjenjang dan DLQ, `emailguard`. Push FCM: adapter ada, **pemakaian di produksi belum diverifikasi**.
- **Status:** email IMPLEMENTED, VERIFIED, DEPLOYED (Resend Free, 100 per hari); push PARTIAL.

### 1.10 Administrasi dan operasi
- **Fungsi:** API admin berbasis allowlist email, metrik bisnis, dashboard dan alarm CloudWatch, budget alert.
- **Status:** IMPLEMENTED, DEPLOYED. Dashboard admin grafis: PLANNED.

## 2. Pemetaan tantangan bisnis ke kapabilitas

| Tantangan | Kapabilitas | Nilai bisnis | Status |
|---|---|---|---|
| Lonjakan trafik saat penjualan dibuka | Virtual waiting room + cache edge | Situs tetap hidup, antrean adil | VERIFIED (lokal, dev) |
| Pembeli sudah masuk tetapi tiket habis | Alokasi sebelum admission | Kepercayaan pembeli ("Jangan PHP") | VERIFIED |
| Oversell | Transaksi stok tunggal dengan kunci terurut | Integritas stok | VERIFIED |
| Calo memborong tiket | Kuota per NIK lintas sesi dan perangkat | Distribusi tiket lebih merata | VERIFIED |
| Callback pembayaran ganda atau palsu | Signature + idempotensi + event sourcing | Tidak ada tiket ganda atau tiket tanpa bayar | VERIFIED |
| Webhook terlambat | Rekonsiliasi + masa tenggang verifikasi + REVIEW | Pembeli yang sudah bayar tidak dirugikan | VERIFIED |
| Email gagal | Outbox, retry, DLQ | Transaksi tetap sah, email dikirim ulang | IMPLEMENTED |
| Kebocoran data pribadi | NIK hanya hash dan terenkripsi, tidak pernah di log | Kepatuhan UU PDP | IMPLEMENTED |

## 3. Matriks bukti kualitas

| Area | Alat | Hasil | Bukti |
|---|---|---|---|
| Unit + integrasi | `go test -race`, testify, Postgres/Valkey/RabbitMQ nyata | Lulus; coverage logika pkg 90,6 · queue 83,6 · booking 81,0 · payment 90,0 · notification 82,7 · user 92,4 (ambang 80%) | [AUDIT-FINAL](../engineering/AUDIT-FINAL.md) (31 Okt 2026), stage `case-coverage` CI |
| E2E API (Go) | `go test -tags e2e`, validasi respons ke OpenAPI | 10 dari 10 lulus (13 Nov 2026) | [TC-REPORT](../../tests/reports/TC-REPORT.md) |
| BDD API | godog, 31 skenario Gherkin | 27 lulus, 4 gagal karena harness (token service belum dipasang; asersi 30 dtk vs grace 180 dtk ADR-007) | [TC-REPORT](../../tests/reports/TC-REPORT.md), [HTML](../../tests/reports/api/ringkasan-api.html) |
| UI web | Cucumber-js + Playwright (desktop dan Pixel 7) | Prod 9/9 dan 10/10; dev 9/9 dan 10/10 | [ringkasan UI](../../tests/reports/ui/ringkasan-ui.html) |
| UI Android native | Appium + WebdriverIO | **Belum dijalankan** (4 skenario ditulis) | [TC-REPORT](../../tests/reports/TC-REPORT.md) |
| Uji beban | k6 | Lokal 3.000 VU p95 3 ms 0% error; dev 3.000 VU p95 181 ms 0% error; prod 500 VU dihentikan oleh batas per IP | [tests/README](../../tests/README.md) |
| Keamanan aplikasi | ZAP, nuclei, Schemathesis, testssl, semgrep, uji logika bisnis | 0 High/Critical; 3 Medium; 8 Low/Info | [pentest REPORT](../../tests/pentest/REPORT.md) |
| Analisis statis | golangci-lint + depguard, go vet, gosec (gagal pada HIGH), SonarCloud | Gerbang CI lulus di `main` | `.github/workflows/ci.yml` |
| Kerentanan dependensi | govulncheck, Trivy fs, Dependabot | Gerbang CI lulus; temuan Trivy x/text dan x/mod sedang diperbaiki di branch audit (PT-F-08) | pentest PT-F-08 |
| Image container | Trivy image (HIGH/CRITICAL), SBOM CycloneDX, hadolint, base distroless nonroot | Lulus | stage `scan-package` |
| Rahasia | gitleaks | Lulus | stage `secret-detection` |
| Kontrak | OpenAPI 3.1 dan JSON Schema 2020-12 | `make contracts` lulus | `docs/contracts` |
| Pipeline | GitHub Actions `ci-ok` | 5 run terakhir di `main` sukses (11 sampai 12 Nov 2026) | tab Actions |
