# admissionRatePerMin diukur dari admisi nyata (2026-11-13)

Temuan: `admissionRatePerMin` di Status Document ada di DTO dan kontrak, tetapi tidak pernah diisi (selalu `0`), sehingga
ETA klien (FR-09, SRS-F-05-10, COULD) tidak pernah tampil. `design.md` §7.2 menyebut nilainya "dari supply", padahal supply
Booking tidak punya data laju admisi.

## Keputusan (diskusi dengan pemilik, nuzirwan)

- Laju diukur, bukan diturunkan dari konfigurasi. Slot dipegang dari admisi sampai journey berakhir, jadi laju =
  kapasitas / lama journey nyata. TTL claim, seleksi, dan pembayaran yang dapat dikonfigurasi (disnapshot per admisi)
  sudah tercermin di hasil ukur.
- Tetap `integer` (pemilik: ETA hanya ditampilkan dalam menit). Dibulatkan ke terdekat.
- Jendela berbasis jumlah: menit penuh terbaru sampai terkumpul ≥ 10 admisi, mundur maksimal 15 menit. Kapasitas kecil
  (mis. 3 slot, sekitar 0,75 admisi/menit) tetap mendapat estimasi kasar (`1`) setelah sekitar 13 menit, bukan nol karena
  sampel 5 menit terlalu sedikit.
- `0` (ETA disembunyikan) saat laju bukan estimasi yang berguna: supply tak terbaca, sold out, `freeUnits = 0`, penjualan
  belum buka, active melebihi kapasitas (surut setelah kapasitas diturunkan), atau < 10 admisi dalam jendela.
- Lonjakan pengisian slot saat pembukaan penjualan dan saat `active_slot_capacity` berubah tidak dihitung: menit sebelum
  momen itu + 1 menit penuh dibuang. Perubahan kapasitas dicatat di hash `q:{e}:sale` (`capacity`, `capacitySinceMs`).
- ETA tetap dihitung klien tiap polling (`posisi / laju`); tidak ada field ETA di server.

## Bukti

- Unit: `TestStatusPublisher_AdmissionRate` (10 kasus), `TestStatusPublisher_AdmissionRateRecordsCapacityChange`,
  `TestStatusPublisher_AdmissionRateReadFailureStillPublishes`; RED sebelum implementasi, GREEN sesudah.
- Integrasi Valkey (compose lokal, port 6380): `TestPromote_CountsAdmissionsPerMinute`, `TestSaleCapacity_RoundTrip`;
  seluruh paket `valkey` dan `worker` dengan tag `integration` lulus (termasuk tes promote lama setelah indeks KEYS bergeser).
- `go vet` + `go test -race -count=1 ./...` queue hijau; `make contracts` hijau.
