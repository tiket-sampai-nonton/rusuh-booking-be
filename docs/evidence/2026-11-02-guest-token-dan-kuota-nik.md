# Kajian token guest dan kuota per NIK (SCRUM-54)

Tanggal: 2026-11-02. Hasil analisis kode dan pengujian E2E di dev; belum ada perubahan kode. Keputusan produk di bawah menunggu pemilik.

## Yang ada sekarang
- **Visitor token** (scope `visitor`, TTL 24 jam, `sub` = visitorID) terbit saat join antrean. Syarat join hanya NIK dan email di body; email dan NIK tidak diverifikasi kepemilikannya.
- **Admission token** (60 detik, sekali pakai) membuka sesi booking. **Queue token** (1 jam) terbit tiap join tetapi tidak ditemukan pemeriksaannya di Queue (perlu dikonfirmasi).
- Kepemilikan booking, status pembayaran, dan tiket diperiksa di server lewat visitorID, akun pemilik, atau tautan email bertoken HMAC.

## Yang sudah terbukti (E2E dev, 2 Nov 2026)
- Pembeli lain tidak bisa membaca tiket (404) dan status pembayaran (404) milik booking orang lain; membaca, membatalkan, dan mengubah booking orang lain 403 `VISITOR_MISMATCH`.
- Token visitor palsu: 401. Webhook dengan signature palsu: 401.
- Join dengan NIK yang sama dua kali: 409 `NIK_ALREADY_IN_QUEUE`. Rate limit join per email: 5 per menit, ke-6 dst 429 `RATE_LIMITED` (`Retry-After: 60`).

## Risiko
1. **Kuota per NIK bisa dihabiskan pihak lain.** Karena NIK tidak diverifikasi, siapa pun yang tahu NIK orang lain dapat masuk antrean atas nama NIK itu dan menempati kuota (`maxTicketsPerBuyerNik`) atau slot antrean pemiliknya. Belum diuji, disimpulkan dari alur join.
2. **TTL visitor token 24 jam** panjang untuk token tanpa identitas; dibatasi oleh status entri di server, tetapi token yang bocor tetap membuka entri/booking itu selama masa berlaku entri.
3. `me`/`claim` yang bukan milik token dijawab 404 (disengaja, tidak membocorkan keberadaan), sedangkan `hold` dan `sesi` tanpa visitor token dijawab 403 `VISITOR_MISMATCH`, bukan 401. Tidak ada kredensial semestinya 401; ini perbedaan dengan kontrak yang perlu diputuskan.

## Opsi (urut dari yang termurah)
- Pertahankan TTL, andalkan status entri dan rate limit yang ada. Tidak ada perubahan.
- Perpendek TTL visitor token ke durasi sesi belanja (mis. beberapa jam) dan perbarui saat `me` dipanggil.
- Verifikasi email (OTP) sebelum pembayaran atau sebelum kuota per NIK dikunci, supaya hanya pemilik email yang bisa menahan stok.
- Captcha atau rate limit per NIK dan per IP di join.
- Lepas kuota NIK otomatis bila booking tidak dibayar sampai batas waktu (sudah terjadi lewat expiry; perlu diuji pada kasus entri MISSED).

## Pertanyaan untuk pemilik produk
- Apakah guest tanpa verifikasi email dapat diterima untuk skala assessment?
- Apakah missing token pada hold/sesi perlu 401 (sesuai kontrak) atau 403 tetap?
- Apakah `queue token` dipakai FE; jika tidak, hapus dari respons join.
