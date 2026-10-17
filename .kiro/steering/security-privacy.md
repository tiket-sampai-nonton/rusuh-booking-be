---
inclusion: always
---

# Keamanan dan Privasi

- NIK hanya disimpan sebagai hash HMAC dan versi tersamar; tidak pernah ditulis ke log.
- Akses detail dan payment wajib token yang hanya terbit lewat antrean (NFR-07).
- Webhook payment diverifikasi signature; redirect/WebView/bridge bukan bukti pembayaran.
- Secret lewat env/SSM; jangan commit `.env`. Kredensial di `docker-compose.yaml` hanya untuk lokal.
- Sumber: SRS bagian keamanan dan HLD viewpoint Security.
