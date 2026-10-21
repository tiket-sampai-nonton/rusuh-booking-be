---
inclusion: always
---

# Product — Rusuh Booking Tiket

- Sistem penjualan tiket konser untuk *war tiket*: Home → Waiting Room → Detail Ticket → Payment (web dan mobile).
- Klien: PT. Tiga Sekawan Angin Ribut. Penyusun: Tim PT. Tiket Sampai Nonton Sentosa.
- Prinsip: (1) jaga pengalaman pengguna apa pun kondisi backend; (2) **Jangan PHP**: pembeli yang sudah masuk detail dijamin ada tiket untuknya sampai journey selesai.
- Batas: maksimal 3 tiket per NIK pembeli per event, 1 tiket per NIK pemegang; TTL Booking 5 menit dan TTL Pembayaran 10 menit, server-authoritative.
- Pembeli bisa guest (NIK + email) atau login opsional lewat OTP email (User Service) untuk menyimpan NIK, email, dan nama.
- Sumber: `docs/analisa/01-PRD-RBT-001-v5.0.md`, `docs/analisa/02-SRS-RBT-001.md`.
