---
inclusion: always
---

# Product — Rusuh Booking Tiket

- Sistem penjualan tiket konser untuk *war tiket*: Home → Waiting Room → Detail Ticket → Payment (web dan mobile).
- Klien: PT. Tiga Sekawan Angin Ribut. Penyusun: Tim PT. Tiket Sampai Nonton Sentosa.
- Prinsip: (1) jaga pengalaman pengguna apa pun kondisi backend; (2) **Jangan PHP**: pembeli yang sudah masuk detail dijamin ada tiket untuknya sampai journey selesai.
- Batas: maksimal 3 tiket per NIK per event; TTL booking dan TTL pembayaran 10 menit, server-authoritative.
- Sumber: `docs/prd/01-PRD-RBT-001-v5.0.md`, `docs/srs/02-SRS-RBT-001.md`.
