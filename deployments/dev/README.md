# Deploy dev (VPS)

Satu VPS Linux (x86_64 atau arm64) menjalankan semuanya dengan Docker Compose: Caddy (TLS, satu-satunya pintu di 80/443), 5 service (api + worker + migrasi), PostgreSQL, Valkey, RabbitMQ, dan observability (Prometheus, Loki, Alloy, Grafana). Langkah menyiapkan server dan domain ada di `PANDUAN-INFRA-DEV-VPS-DAN-AWS.md` (di folder induk repo).

| File | Fungsi |
|---|---|
| `docker-compose.dev.yaml` | stack dev; image dari GHCR (`IMAGE_PREFIX`, `IMAGE_TAG`), rahasia dari `/opt/rusuh/env/*.env`, tanpa port ke host selain Caddy |
| `Caddyfile` | TLS otomatis dan routing path (queue, booking, payment, notification, user, `/media`); `/metrics` dan `/internal/*` tidak dirutekan; `/internal/v1/admin/*` hanya dari `ADMIN_ALLOW_IPS` |
| `env/*.env.example` | template konfigurasi per service dan infra |
| `server-bootstrap.sh` | menyiapkan server kosong (sekali, root): swap, user deploy, UFW, fail2ban, Docker |
| `gen-secrets.sh` | membuat kunci Ed25519, rahasia acak, dan file env di `/opt/rusuh` (idempoten, tidak menimpa) |
| `push-config.sh` | dari laptop: kirim file konfigurasi ke server tanpa workflow |
| `preflight.sh` | pemeriksaan sebelum deploy (env lengkap, DNS, port, disk, compose valid) |
| `compose.sh` | pembungkus `docker compose` untuk operasi harian di server |
| `deploy.sh` | dijalankan di server oleh CI: periksa konfigurasi, `pull`, `up -d` (migrasi ikut), smoke test |
| `housekeeping.sh`, `backup.sh`, `systemd/`, `install-timers.sh` | pembersihan harian (03:00) dan `pg_dump` harian (02:30) |
| `prometheus.dev.yml`, `loki.dev.yaml`, `alloy.dev.alloy` | konfigurasi observability untuk dev (retensi 7 hari) |
| `postgres-passwords.sh` | memberi password role service saat volume Postgres pertama dibuat |

## Pertama kali (satu kali deploy)

Rincian perintah demi perintah ada di `PANDUAN-INFRA-DEV-VPS-DAN-AWS.md` bagian 2A. Ringkasnya:

1. Server siap (`server-bootstrap.sh`, user `deploy`, Docker) dan DNS `dev` serta `grafana.dev` mengarah ke VPS. Variabel dan secret GitHub terisi: `DEV_HOST`, `DEV_USER`, `DEV_SSH_PORT`, `DEV_SSH_KEY`, `DEV_SSH_KNOWN_HOSTS`.
2. Dari laptop: `SSH_TARGET=rusuh-dev-deploy bash deployments/dev/push-config.sh` (mengirim file ke `/opt/rusuh/repo`).
3. Di server sebagai `deploy`: `gen-secrets.sh` (isi `DEV_DOMAIN`, `ACME_EMAIL`, `ADMIN_EMAILS`, `MIDTRANS_SERVER_KEY`, `RESEND_API_KEY`; dua kunci terakhir lewat `read -s`).
4. Di server sebagai `root`: `bash /opt/rusuh/repo/deployments/dev/install-timers.sh`.
5. Di server sebagai `deploy`: `bash /opt/rusuh/repo/deployments/dev/preflight.sh` sampai `SIAP DEPLOY`.
6. GitHub Actions: Run workflow `ci`, `deploy_env=dev` (boleh dari cabang fitur). Job `login GHCR` memakai token run, jadi tidak perlu PAT.
7. Operasi harian di server: `./compose.sh ps | logs | restart | up -d --force-recreate <service>`.

## Batasan di dev (VPS dengan gateway bersama)

Gateway penyedia meneruskan HTTPS tanpa mengubahnya, sehingga aplikasi melihat semua pengunjung berasal dari satu IP (`10.10.0.1`). Karena itu batas per-IP dilonggarkan di template env dev (`RATE_LIMIT_PER_IP=3000`, `RATE_LIMIT_JOIN_PER_IP=100000`, `OTP_MAX_PER_IP_HOUR=100000`); batas per email (OTP, join antrean) tetap berlaku. Ini batasan lingkungan dev, bukan perilaku produksi: di AWS IP klien terbaca dan nilai bawaan dipakai. Caddy sengaja tidak dikonfigurasi `trusted_proxies`, karena header `X-Forwarded-For` hanya ada di jalur HTTP (yang hanya mengalihkan ke HTTPS).

## Aturan

- Jangan menaruh rahasia di repo atau di chat; `gen-secrets.sh` menulisnya hanya di `/opt/rusuh/env` (0600).
- `housekeeping.sh` memakai `docker image prune -af` dan hanya untuk VPS; **jangan dijalankan di laptop**. Skrip itu tidak pernah menghapus volume.
- Rotasi kunci: hapus file di `/opt/rusuh/keys` atau `/opt/rusuh/env/.generated` hanya dengan sadar; mengganti `PII_ENC_KEY` membuat email terenkripsi lama tidak terbuka.
