---
inclusion: always
---

# Invarian (tidak boleh dilanggar)

1. Tidak ada oversell: stok berubah hanya di Booking Service dalam satu transaksi PostgreSQL.
2. Jangan PHP: admission ke detail hanya bila alokasi tiket untuk pembeli sudah direservasi.
3. Kuota 3 tiket per NIK per event, termasuk booking aktif lain dan lintas sesi/perangkat.
4. Timer booking dan pembayaran server-authoritative; klien hanya menampilkan.
5. Pembayaran sukses diproses idempoten; status akhir hanya dari webhook/rekonsiliasi.
6. Kegagalan notifikasi tidak membatalkan transaksi (retry + DLQ).
