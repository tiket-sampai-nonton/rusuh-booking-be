- **TC-UI-EVT-02 gagal di dev pada run 10:24 (desktop dan mobile): data dev, bukan bug aplikasi.** Galeri event dev saat itu berisi URL `picsum.photos` (event "Senja di Kota Tua Fest"), yang tidak terjangkau dari mesin pengujian (kode `000`). Data dev sudah diganti ke foto Pixabay (scan 19 event: 0 referensi `picsum`), dan TC-UI-EVT-02 lulus di desktop dan mobile pada run berikutnya.
- **TC-UI-QUE-01 desktop gagal sekali pada run penuh dev (10:39): `waitForURL` checkout timeout 60 dtk.** Halaman tetap di `/waiting/...` tanpa berpindah ke checkout. Run ulang skenario yang sama lulus, dan run penuh berikutnya juga lulus 9/9. Penyebab belum dipastikan; kemungkinan waktu tunggu antrean di dev. Pantau: bila muncul lagi, periksa antrean dev dan log queue-api.
- **TC-UI-CAT-01: jumlah event di UI = event API selain `CLOSED`.** Di dev API mengembalikan 19 event (9 AVAILABLE, 6 NOT_STARTED, 4 CLOSED) dan halaman Jelajah menampilkan 15. Perilaku ini konsisten (FE menyembunyikan event CLOSED secara bawaan), sehingga asersi skenario memakai jumlah event aktif. Versi awal asersi membandingkan dengan seluruh event API dan gagal di dev; diperbaiki, bukan disembunyikan.
- **Tidak ada bug aplikasi yang ditemukan** oleh suite UI pada prod maupun dev. Selektor mobile awalnya salah (tombol kembali di mobile berupa ikon tanpa teks); diperbaiki dengan `data-testid="checkout-back"`, yang memang disediakan FE.
- Pada run pertama, `TC-UI-QUE-02` mobile gagal karena selektor tersebut dan berhasil setelah diperbaiki; hasil di tabel adalah run terakhir.

## Uji ulang setelah perbaikan audit (13 Nov 2026, 19:00–19:45 WIB, commit `031b6c4`)

Kode: `main` setelah PR #37 (perbaikan audit Booking, Queue, User). Stack lokal, E2E penuh dengan `E2E_SLOW=1`.

| Hasil | Sebelum | Sesudah |
|---|---|---|
| BDD (godog) | 27 dari 31 lulus | **29 dari 31 lulus** |
| Item uji Go e2e | 37 lulus, 5 gagal | 39 lulus, 3 gagal (dua skenario BDD dan induknya `TestE2E`) |
| UI dev (desktop, mobile) | 9/9, 10/10 | 9/9, 10/10 |

**Yang sekarang lulus:** TC-EDT-04 dan TC-PAY-09b. Penyebab kegagalan sebelumnya memang harness, dan terbukti: token service harus **diterbitkan per panggilan**, karena `ServiceTokenMaxTTL` adalah 5 menit sehingga satu token di `E2E_SERVICE_TOKEN` tidak bisa dipakai satu run penuh (±7 menit).

**Yang masih gagal:** TC-PAY-04 dan TC-PAY-07. Asersi menunggu 30 detik, sedangkan perilaku yang diterima di ADR-007 memberi masa tenggang verifikasi 180 detik. Keputusan pemilik: ubah asersi ke batas tenggang atau tetap.

**Temuan baru dari uji ulang (bukan bug fungsi, tetapi harus diketahui):**
- **Cache baca katalog 2 detik** (commit `50d790b`, keputusan desain D-C7.7): detail dan daftar publik bisa tertinggal sampai 2 detik setelah publish, cancel, atau unggah gambar. Tanpa penyesuaian, 9 tes e2e yang sebelumnya lulus gagal (TestAdminImageUpload, TestCancelledEvent, TestHold, TestJourney, TestPayment_Pending, TestExpiredPayment, TestConcurrentBuyers, TestQueueSettings, TestAdmin_OnlyAllowListed), karena mereka membaca detail publik langsung sesudah mutasi. Dengan jeda 2,5 detik pada tiga titik di helper tes, semuanya lulus.
- **Kebersihan data uji OTP:** `ReadOTP` mengambil email pertama yang cocok di Mailpit, jadi email lama dari run sebelumnya terbaca lebih dulu (TC-USR-05 gagal dengan OTP_INVALID). Selain itu limiter OTP per IP penuh bila E2E diulang berkali-kali dalam waktu dekat (429 pada TC-USR-02, TC-USR-05, TestQueueSettings). Valkey dan Mailpit lokal direset sebelum run akhir.
- **Konfigurasi `.env` lokal:** `services/queue/.env` perlu `BOOKING_PAGE_BASE_URL` dan `QUEUE_PROMOTE_BATCH_MAX` paling besar 100 (batas baru 1 sampai 100); tanpa itu Queue menolak start.

**Syarat angka di atas:** perbaikan harness (penerbitan token per panggilan dan jeda cache) ada di working tree dan **belum di-commit ke repo**. Hasil `main` tanpa perbaikan itu: 9 tes lama gagal karena cache dan 2 skenario payment-lock gagal 401.
