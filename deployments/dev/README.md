# Deploy dev (VPS)

Satu VPS Linux (x86_64 atau arm64) menjalankan semuanya dengan Docker Compose: Caddy (TLS, satu-satunya pintu di 80/443), 5 service (api + worker + migrasi), PostgreSQL, Valkey, RabbitMQ, dan observability (Prometheus, Loki, Alloy, Grafana). Langkah menyiapkan server dan domain ada di `PANDUAN-INFRA-DEV-VPS-DAN-AWS.md` (di folder induk repo).

| File | Fungsi |
|---|---|
| `docker-compose.dev.yaml` | stack dev; image dari GHCR (`IMAGE_PREFIX`, `IMAGE_TAG`), rahasia dari `/opt/rusuh/env/*.env`, tanpa port ke host selain Caddy |
| `Caddyfile` | TLS otomatis dan routing path (queue, booking, payment, notification, user, `/media`); `/metrics` dan `/internal/*` tidak dirutekan; `/internal/v1/admin/*` hanya dari `ADMIN_ALLOW_IPS` |
| `env/*.env.example` | template konfigurasi per service dan infra |
| `gen-secrets.sh` | membuat kunci Ed25519, rahasia acak, dan file env di `/opt/rusuh` (idempoten, tidak menimpa) |
| `deploy.sh` | dijalankan di server oleh CI: periksa konfigurasi, `pull`, `up -d` (migrasi ikut), smoke test |
| `housekeeping.sh`, `backup.sh`, `systemd/`, `install-timers.sh` | pembersihan harian (03:00) dan `pg_dump` harian (02:30) |
| `prometheus.dev.yml`, `loki.dev.yaml`, `alloy.dev.alloy` | konfigurasi observability untuk dev (retensi 7 hari) |
| `postgres-passwords.sh` | memberi password role service saat volume Postgres pertama dibuat |

## Pertama kali

1. Server siap (hardening, Docker, user `deploy`) dan DNS `dev.<domain>` mengarah ke VPS.
2. Jalankan workflow `ci` (Run workflow, `deploy_env=dev`) sekali. Ia mengirim file ke `/opt/rusuh/repo`, lalu `deploy.sh` berhenti dengan pesan bahwa konfigurasi belum ada. Itu disengaja.
3. Di server, sebagai `deploy`:
   ```bash
   cd /opt/rusuh/repo/deployments/dev
   DEV_DOMAIN=dev.rusuhbooking.my.id ACME_EMAIL=anda@contoh.id ADMIN_EMAILS=anda@contoh.id \
   MIDTRANS_SERVER_KEY=... RESEND_API_KEY=... bash gen-secrets.sh
   ```
   Isi nilai `@@SET_...@@` yang masih tersisa (skrip menampilkan daftarnya), mis. alamat FE untuk `PAYMENT_*_RETURN_URL` dan `TICKET_LINK_BASE_URL`.
4. Pasang timer (sebagai admin): `sudo bash /opt/rusuh/repo/deployments/dev/install-timers.sh`.
5. Jalankan workflow `ci` dengan `deploy_env=dev` lagi: image ditarik, migrasi jalan, smoke test `GET /api/v1/events` harus 200.

## Aturan

- Jangan menaruh rahasia di repo atau di chat; `gen-secrets.sh` menulisnya hanya di `/opt/rusuh/env` (0600).
- `housekeeping.sh` memakai `docker image prune -af` dan hanya untuk VPS; **jangan dijalankan di laptop**. Skrip itu tidak pernah menghapus volume.
- Rotasi kunci: hapus file di `/opt/rusuh/keys` atau `/opt/rusuh/env/.generated` hanya dengan sadar; mengganti `PII_ENC_KEY` membuat email terenkripsi lama tidak terbuka.
