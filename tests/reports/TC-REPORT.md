# Laporan TC (test case): Rusuh Booking Tiket

Dibuat otomatis oleh `tests/reports/gen-tc-report.mjs` dari hasil eksekusi nyata. Commit dasar: `031b6c4` (working tree dapat memuat perubahan yang belum di-commit). Setiap angka di bawah berasal dari berkas hasil di `tests/reports/`; yang tidak dijalankan ditandai **BELUM DIJALANKAN**.

## 1. Ringkasan

| Jenis | Alat | Lingkungan | Dijalankan | PASS | FAIL | Dilewati | Waktu |
|---|---|---|---:|---:|---:|---:|---|
| API BDD | godog (Cucumber Go), `tests/features` | stack Docker lokal | 31 skenario | 29 | 2 | 0 | 2026-11-13 19:31 WIB |
| API E2E Go | `go test -tags e2e` (tes lain di `tests/e2e`) | stack Docker lokal | 10 tes | 10 | 0 | 0 | 2026-11-13 19:31 WIB |
| UI desktop, prod | Cucumber-js + Playwright (Chromium) | prod: rusuhbooking.my.id | 9 skenario | 9 | 0 | 0 | 2026-11-13 08:04 WIB |
| UI mobile (Pixel 7), prod | Cucumber-js + Playwright (Chromium) | prod: rusuhbooking.my.id | 10 skenario | 10 | 0 | 0 | 2026-11-13 08:06 WIB |
| UI desktop, dev | Cucumber-js + Playwright (Chromium) | dev: rusuh-booking.pages.dev | 9 skenario | 9 | 0 | 0 | 2026-11-13 19:33 WIB |
| UI mobile (Pixel 7), dev | Cucumber-js + Playwright (Chromium) | dev: rusuh-booking.pages.dev | 10 skenario | 10 | 0 | 0 | 2026-11-13 19:34 WIB |
| Appium (Android) | WebdriverIO + Appium UiAutomator2 | perangkat Android | **BELUM DIJALANKAN** (4 skenario ditulis; hanya dry-run struktur) | - | - | - | - |

Eksekusi API: `exit=1 durasi=449s selesai=2026-11-13 19:31 WIB commit=031b6c4 E2E_SLOW=1 token-service=diterbitkan-per-panggilan`.

## 2. API: skenario BDD (godog) dan tes Go e2e

Sumber skenario: `tests/features/*.feature` (tidak diubah). Dijalankan lewat **jalur resmi** `tools/e2e.sh` (`tests/reports/run-e2e-official.sh`): 5 service dibangun dan dijalankan di host terhadap infra Docker Compose, dengan `E2E_SLOW=1` dan override lingkungan (tanpa mengubah kode): `BOOKING_PAGE_BASE_URL=http://localhost:3000/book` (Queue; `.env` tidak memilikinya), `VALKEY_SERVER=[{"host":"localhost","port":6380}]` (Compose memetakan Valkey ke 6380), `POSTGRES_PORT=5433` (oleh skrip). `E2E_SERVICE_TOKEN` belum dipasang, sehingga dua tes payment-lock gagal. Keterkaitan FR/NFR/BR ditelusuri otomatis dari `02-SRS` dan `06-TP` (best effort, bukan klaim kepatuhan). Run sebelumnya terhadap stack Docker hasil override k6 **tidak dipakai** (bukan jalur resmi).

### 2.1 Skenario godog

| TC ID | Judul | Hasil | Durasi (dtk) | FR/NFR/BR terkait | Sebab kegagalan |
|---|---|---|---:|---|---|
| TC-ADM-01 | admission always reserves stock | PASS | 0.0 | - |  |
| TC-ADM-03 | unused units return to supply after hold | PASS | 0.0 | - |  |
| TC-BKG-01 | buyer can hold up to 3 tickets with different attendee NIKs | PASS | 0.0 | BR-02, FR-14, BR-01 |  |
| TC-BKG-02 | attendee NIK must be unique per event | PASS | 0.0 | - |  |
| TC-BKG-03 | duplicate attendee NIK in same order is rejected | PASS | 0.0 | - |  |
| TC-BKG-06 | hold is idempotent with same Idempotency-Key | PASS | 0.0 | FR-16 |  |
| TC-BKG-08 | hold with invalid NIK structure is rejected | PASS | 0.0 | FR-15 |  |
| TC-BKG-09 | attendee check does not leak NIK | PASS | 0.0 | FR-13 |  |
| TC-CAT-01 | list all events returns 200 | PASS | 0.0 | - |  |
| TC-CAT-02 | city filter is case-insensitive | PASS | 0.0 | - |  |
| TC-CAT-06 | sort by price ascending returns 200 | PASS | 0.0 | - |  |
| TC-EDT-01 | change items before paying | PASS | 0.0 | FR-21, BR-05, FR-16 |  |
| TC-EDT-03 | edit limit reached | PASS | 0.0 | - |  |
| TC-EDT-04 | edit locked after payment-lock | PASS | 0.0 | - |  |
| TC-EDT-06 | concurrent edits with same version — exactly one wins | PASS | 0.0 | - |  |
| TC-EVT-01 | list events returns an events array | PASS | 0.0 | FR-01, FR-03 |  |
| TC-EVT-04 | event detail returns categories | PASS | 0.0 | FR-11 |  |
| TC-EVT-05 | category availability is live | PASS | 0.0 | FR-12 |  |
| TC-GST-01 | anonymous join returns a visitor token | PASS | 0.0 | FR-13 |  |
| TC-GST-02 | legacy buyer fields in the join body are ignored | PASS | 0.0 | FR-13, FR-15 |  |
| TC-GST-04 | same visitor joining again gets the same entry | PASS | 0.0 | - |  |
| TC-PAY-04 | booking expires when TTL lapses | FAIL | 30.1 | BR-06, FR-23 | (b) Perubahan perilaku disengaja, ADR-007 (amandemen 2026-11-12): booking masuk VERIFYING dan baru EXPIRED setelah `VERIFY_GRACE_SECONDS` (default 180 s, `services/booking/internal/booking/application/usecase/expire.go:95-103`). Skenario masih meminta EXPIRED dalam 30 s; pada run ini status tertinggal di VERIFYING. Bukti perilaku akhir: `TestExpiredPayment_LateSettlementGoesToReviewWithoutTickets` lulus pada run yang sama (EXPIRED tercapai setelah sekitar 310 s). Keputusan pemilik: ubah asersi di `tests/features/cancel.feature` ke batas tenggang, atau tetap. |
| TC-PAY-07 | late payment on expired booking becomes REVIEW | FAIL | 30.1 | - | (b) Sama dengan TC-PAY-04: langkah "expire by sweeper" menunggu 30 s dan gagal sebelum langkah REVIEW (sisa langkah dilewati). Perilaku REVIEW tanpa tiket setelah pembayaran terlambat tercakup oleh `TestExpiredPayment_LateSettlementGoesToReviewWithoutTickets` (lulus). |
| TC-PAY-09a | cancel unpaid unlocked booking | PASS | 0.0 | - |  |
| TC-PAY-09b | cancel locked booking is rejected | PASS | 0.0 | - |  |
| TC-QUE-02 | direct admission when slots available | PASS | 0.0 | FR-05 |  |
| TC-SES-01 | booking response has server-authoritative paymentExpiresAt | PASS | 0.0 | BR-03, FR-17, FR-19 |  |
| TC-USR-01 | buy without login works | PASS | 0.1 | - |  |
| TC-USR-02 | OTP is sent for any email | PASS | 0.5 | - |  |
| TC-USR-03 | wrong OTP is rejected | PASS | 0.0 | - |  |
| TC-USR-05 | correct OTP issues tokens | PASS | 0.5 | - |  |

### 2.2 Tes Go e2e lain

| TC ID | Judul | Hasil | Durasi (dtk) |
|---|---|---|---:|
| TestPayment_PendingThenFailedNeverPaysOrIssuesTickets | Payment PendingThenFailedNeverPaysOrIssuesTickets | PASS | 9.1 |
| TestHold_DuplicateRequestsAndUnauthorizedAccess | Hold DuplicateRequestsAndUnauthorizedAccess | PASS | 2.5 |
| TestExpiredPayment_LateSettlementGoesToReviewWithoutTickets | ExpiredPayment LateSettlementGoesToReviewWithoutTickets | PASS | 315.5 |
| TestConcurrentBuyers_NeverOversell | ConcurrentBuyers NeverOversell | PASS | 12.6 |
| TestAdminImageUpload | AdminImageUpload | PASS | 5.0 |
| TestJourney_LoginToPaidTicketAndEmail | Journey LoginToPaidTicketAndEmail | PASS | 11.3 |
| TestAdmin_OnlyAllowListedAccountsCanCreateEvents | Admin OnlyAllowListedAccountsCanCreateEvents | PASS | 2.5 |
| TestLogout_RevokesTheAccessTokenAtOnce | Logout RevokesTheAccessTokenAtOnce | PASS | 0.5 |
| TestCancelledEvent_LeavesTheListButStaysReadable | CancelledEvent LeavesTheListButStaysReadable | PASS | 5.0 |
| TestQueueSettings_StrictJoinAndAdminEndpoint | QueueSettings StrictJoinAndAdminEndpoint | PASS | 3.0 |

## 3. UI web: Cucumber-js + Playwright (Chromium, headless)

Target: FE prod `https://rusuhbooking.my.id` yang memanggil `https://api.rusuhbooking.my.id` (data event dipilih dinamis dari API, jadi suite dapat diarahkan ke dev atau lokal lewat `BASE_URL` dan `API_URL`). Aksi aman: membaca katalog dan detail, masuk antrean sampai checkout, lalu keluar dan melepas sesi. **Tidak ada pembayaran dan tidak ada booking** yang dibuat. Laporan HTML dengan tangkapan layar: `tests/reports/ui/cucumber-desktop.html` dan `cucumber-mobile.html`.

Penanda **(baru)**: ID `TC-UI-*` belum ada di `06-TP-RBT-001.md` (usulan penambahan). `TC-EVT-01` dan `TC-EVT-04` ada di Test Plan dengan alat yang direncanakan Katalon/Appium; di sini dijalankan dengan Playwright.

### 3.1 UI desktop, prod (1280x900)

| TC ID | Judul | Hasil | Durasi (dtk) | FR/NFR/BR terkait |
|---|---|---|---:|---|
| TC-UI-QUE-01 (baru) | masuk antrean membawa pengunjung ke halaman checkout | PASS | 50.8 | - |
| TC-UI-QUE-02 (baru) | keluar dari checkout melepas sesi | PASS | 15.6 | - |
| TC-UI-QUE-03 (baru) | formulir kosong tidak mengirim booking | PASS | 53.2 | - |
| TC-EVT-01 | beranda menampilkan event yang terbit | PASS | 6.3 | FR-01, FR-03 |
| TC-UI-CAT-01 (baru) | halaman Jelajah menampilkan jumlah event aktif yang sama dengan API | PASS | 7.5 | - |
| TC-EVT-04 | detail event menampilkan kategori dan harga | PASS | 9.4 | FR-11 |
| TC-UI-EVT-01 (baru) | gambar event memuat tanpa gambar rusak | PASS | 7.7 | - |
| TC-UI-EVT-02 (baru) | detail event menampilkan pengisi acara, lokasi, dan galeri | PASS | 12.4 | - |
| TC-UI-NET-01 (baru) | tidak ada error CORS atau permintaan API gagal di browser | PASS | 12.2 | - |

### 3.2 UI mobile (Pixel 7), prod (viewport dan user-agent mobile, bukan perangkat nyata)

| TC ID | Judul | Hasil | Durasi (dtk) | FR/NFR/BR terkait |
|---|---|---|---:|---|
| TC-UI-QUE-01 (baru) | masuk antrean membawa pengunjung ke halaman checkout | PASS | 12.2 | - |
| TC-UI-QUE-02 (baru) | keluar dari checkout melepas sesi | PASS | 15.5 | - |
| TC-UI-QUE-03 (baru) | formulir kosong tidak mengirim booking | PASS | 52.5 | - |
| TC-EVT-01 | beranda menampilkan event yang terbit | PASS | 5.9 | FR-01, FR-03 |
| TC-UI-CAT-01 (baru) | halaman Jelajah menampilkan jumlah event aktif yang sama dengan API | PASS | 7.1 | - |
| TC-EVT-04 | detail event menampilkan kategori dan harga | PASS | 8.4 | FR-11 |
| TC-UI-EVT-01 (baru) | gambar event memuat tanpa gambar rusak | PASS | 8.9 | - |
| TC-UI-EVT-02 (baru) | detail event menampilkan pengisi acara, lokasi, dan galeri | PASS | 9.5 | - |
| TC-UI-NET-01 (baru) | tidak ada error CORS atau permintaan API gagal di browser | PASS | 8.7 | - |
| TC-UI-MOB-01 (baru) | tampilan mobile tidak memiliki scroll horizontal | PASS | 4.2 | - |

### 3.3 UI desktop, dev (1280x900)

| TC ID | Judul | Hasil | Durasi (dtk) | FR/NFR/BR terkait |
|---|---|---|---:|---|
| TC-UI-QUE-01 (baru) | masuk antrean membawa pengunjung ke halaman checkout | PASS | 10.8 | - |
| TC-UI-QUE-02 (baru) | keluar dari checkout melepas sesi | PASS | 9.1 | - |
| TC-UI-QUE-03 (baru) | formulir kosong tidak mengirim booking | PASS | 10.6 | - |
| TC-EVT-01 | beranda menampilkan event yang terbit | PASS | 2.2 | FR-01, FR-03 |
| TC-UI-CAT-01 (baru) | halaman Jelajah menampilkan jumlah event aktif yang sama dengan API | PASS | 4.2 | - |
| TC-EVT-04 | detail event menampilkan kategori dan harga | PASS | 5.1 | FR-11 |
| TC-UI-EVT-01 (baru) | gambar event memuat tanpa gambar rusak | PASS | 4.3 | - |
| TC-UI-EVT-02 (baru) | detail event menampilkan pengisi acara, lokasi, dan galeri | PASS | 6.6 | - |
| TC-UI-NET-01 (baru) | tidak ada error CORS atau permintaan API gagal di browser | PASS | 4.2 | - |

### 3.4 UI mobile (Pixel 7), dev (viewport dan user-agent mobile, bukan perangkat nyata)

| TC ID | Judul | Hasil | Durasi (dtk) | FR/NFR/BR terkait |
|---|---|---|---:|---|
| TC-UI-QUE-01 (baru) | masuk antrean membawa pengunjung ke halaman checkout | PASS | 8.0 | - |
| TC-UI-QUE-02 (baru) | keluar dari checkout melepas sesi | PASS | 9.3 | - |
| TC-UI-QUE-03 (baru) | formulir kosong tidak mengirim booking | PASS | 10.3 | - |
| TC-EVT-01 | beranda menampilkan event yang terbit | PASS | 2.1 | FR-01, FR-03 |
| TC-UI-CAT-01 (baru) | halaman Jelajah menampilkan jumlah event aktif yang sama dengan API | PASS | 4.0 | - |
| TC-EVT-04 | detail event menampilkan kategori dan harga | PASS | 4.8 | FR-11 |
| TC-UI-EVT-01 (baru) | gambar event memuat tanpa gambar rusak | PASS | 4.1 | - |
| TC-UI-EVT-02 (baru) | detail event menampilkan pengisi acara, lokasi, dan galeri | PASS | 6.4 | - |
| TC-UI-NET-01 (baru) | tidak ada error CORS atau permintaan API gagal di browser | PASS | 4.8 | - |
| TC-UI-MOB-01 (baru) | tampilan mobile tidak memiliki scroll horizontal | PASS | 2.2 | - |

### 3.5 Bukti tangkapan layar (dipilih, `tests/reports/ui/evidence/`)

- [prod-desktop-TC-EVT-01.jpg](ui/evidence/prod-desktop-TC-EVT-01.jpg)
- [prod-desktop-TC-EVT-04.jpg](ui/evidence/prod-desktop-TC-EVT-04.jpg)
- [prod-desktop-TC-UI-QUE-01.jpg](ui/evidence/prod-desktop-TC-UI-QUE-01.jpg)
- [prod-mobile-TC-EVT-04.jpg](ui/evidence/prod-mobile-TC-EVT-04.jpg)
- [prod-mobile-TC-UI-QUE-01.jpg](ui/evidence/prod-mobile-TC-UI-QUE-01.jpg)
- [dev-desktop-TC-UI-EVT-02.jpg](ui/evidence/dev-desktop-TC-UI-EVT-02.jpg)

### 3.6 Catatan dan temuan

- **TC-UI-EVT-02 gagal di dev pada run 10:24 (desktop dan mobile): data dev, bukan bug aplikasi.** Galeri event dev saat itu berisi URL `picsum.photos` (event "Senja di Kota Tua Fest"), yang tidak terjangkau dari mesin pengujian (kode `000`). Data dev sudah diganti ke foto Pixabay (scan 19 event: 0 referensi `picsum`), dan TC-UI-EVT-02 lulus di desktop dan mobile pada run berikutnya.
- **TC-UI-QUE-01 desktop gagal sekali pada run penuh dev (10:39): `waitForURL` checkout timeout 60 dtk.** Halaman tetap di `/waiting/...` tanpa berpindah ke checkout. Run ulang skenario yang sama lulus, dan run penuh berikutnya juga lulus 9/9. Penyebab belum dipastikan; kemungkinan waktu tunggu antrean di dev. Pantau: bila muncul lagi, periksa antrean dev dan log queue-api.
- **TC-UI-CAT-01: jumlah event di UI = event API selain `CLOSED`.** Di dev API mengembalikan 19 event (9 AVAILABLE, 6 NOT_STARTED, 4 CLOSED) dan halaman Jelajah menampilkan 15. Perilaku ini konsisten (FE menyembunyikan event CLOSED secara bawaan), sehingga asersi skenario memakai jumlah event aktif. Versi awal asersi membandingkan dengan seluruh event API dan gagal di dev; diperbaiki, bukan disembunyikan.
- **Tidak ada bug aplikasi yang ditemukan** oleh suite UI pada prod maupun dev. Selektor mobile awalnya salah (tombol kembali di mobile berupa ikon tanpa teks); diperbaiki dengan `data-testid="checkout-back"`, yang memang disediakan FE.
- Pada run pertama, `TC-UI-QUE-02` mobile gagal karena selektor tersebut dan berhasil setelah diperbaiki; hasil di tabel adalah run terakhir.


## 4. Appium (Android): BELUM DIJALANKAN

Suite di `tests/ui/appium/` (WebdriverIO + Cucumber + UiAutomator2) memuat 3 skenario Chrome mobile (`TC-EVT-01`, `TC-EVT-04`, `TC-MOB-01` sebagian: hanya sampai checkout, tanpa pembayaran dan tiket) dan 1 kerangka native (`TC-MOB-NATIVE-01`). Mesin pembuat laporan ini tidak memiliki Android SDK, emulator, maupun Xcode, sehingga **tidak satu pun dijalankan di perangkat**. Yang divalidasi hanya: `npm install` berhasil, `wdio.conf.js` termuat, dan `cucumber-js --dry-run` memetakan seluruh langkah ke definisinya (4 skenario, 12 langkah). Selektor native belum diverifikasi terhadap build nyata. Cara menjalankan: `tests/ui/README.md`.

## 5. Keterbatasan

- **Appium belum dijalankan** (lihat bagian 4). Hasil mobile di bagian 3.2 adalah emulasi viewport dan user-agent di Chromium, bukan perangkat Android atau iOS sungguhan.
- **Tidak ada uji pembayaran di UI**: pembayaran memakai Midtrans Sandbox dan menahan stok; jalur itu hanya dicakup tes API (godog/Go e2e) pada stack lokal.
- Suite UI membuat sesi antrean nyata di prod (3 skenario per profil, 6 sesi); semuanya dilepas lewat tombol keluar dan, bila gagal, oleh pengaman di hook. Sesi yang tersisa kedaluwarsa sendiri dalam 5 menit.
- Keterkaitan FR/NFR/BR di tabel API ditelusuri otomatis dan dapat tidak lengkap (rentang seperti `TC-CAT-01..06` di Test Plan tidak diurai).
- Mayoritas TC di `06-TP-RBT-001.md` (sekitar 500 baris referensi) memakai alat lain (unit, integration, k6, inspeksi). Laporan ini hanya mencakup yang dijalankan di sini, bukan seluruh Test Plan.
- Cakupan UI bersifat smoke fungsional (katalog, detail, antrean, checkout), bukan regresi penuh seluruh FE.

## 6. Cara mengulang

```bash
# UI (prod; ganti BASE_URL/API_URL untuk lingkungan lain)
cd tests/ui/playwright && npm install && npx playwright install chromium
npx cucumber-js            # desktop
npx cucumber-js -p mobile  # mobile
# API (stack lokal: bash tests/k6/scripts/stack-local.sh up)
bash tests/reports/run-godog-local.sh
# laporan ini
node tests/reports/gen-tc-report.mjs
```
