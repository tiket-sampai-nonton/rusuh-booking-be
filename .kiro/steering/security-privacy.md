---
inclusion: always
---

# Keamanan dan Privasi

- NIK hanya disimpan sebagai hash HMAC dan versi tersamar; tidak pernah ditulis ke log. Akun User menyimpan NIK dan email terenkripsi AES-256-GCM + hash HMAC (kunci `PII_*`).
- Login opsional: OTP email 6 digit tanpa password (TTL 5 menit, maks 5 percobaan, cooldown 60 detik, rate limit per email dan IP); kode OTP tidak pernah lewat broker atau log.
- Refresh token dirotasi; token lama yang dipakai ulang mencabut semua sesi akun.
- Akses detail dan payment wajib token yang hanya terbit lewat antrean (NFR-07).
- Webhook payment diverifikasi signature; redirect/WebView/bridge bukan bukti pembayaran.
- Secret lewat env/SSM; jangan commit `.env`. Kredensial di `docker-compose.yaml` hanya untuk lokal.
- Sumber: SRS bagian keamanan dan HLD viewpoint Security.
