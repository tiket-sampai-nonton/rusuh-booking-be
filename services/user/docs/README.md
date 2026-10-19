# User Service

Akun opsional: login OTP email tanpa password, rotasi refresh token, profil (nama, NIK tersimpan terenkripsi). Guest checkout tetap jalan tanpa akun.

- Port: `8085` (`APP_PORT`)
- Schema PostgreSQL: `users` (role `svc_user`)
- Verb: api, `migrate up|down|status`, `healthcheck`

## Endpoint

| Method | Path | Keterangan |
|---|---|---|
| POST | `/api/v1/auth/otp/request` | Kirim kode 6 digit ke email (202, tanpa membocorkan apakah email terdaftar) |
| POST | `/api/v1/auth/otp/verify` | Tukar kode dengan access token (EdDSA, 15 menit) + refresh token (30 hari); akun dibuat saat login pertama |
| POST | `/api/v1/auth/refresh` | Rotasi refresh token; token lama yang dipakai ulang mencabut semua sesi |
| POST | `/api/v1/auth/logout` | Cabut sesi (idempoten, 204) |
| GET/PATCH | `/api/v1/me` | Profil: nama, NIK (disimpan terenkripsi, ditampilkan tersamar) |

Kontrak: `docs/contracts/openapi/user.yaml`. Email OTP dikirim langsung lewat SMTP (lokal: Mailpit http://localhost:8025).

## Menjalankan lokal

```bash
make infra keys                     # dari root repo: PostgreSQL, Valkey, RabbitMQ, Mailpit + kunci token lokal
cd services/user && cp .env.example .env
go run ./cmd migrate up
go run ./cmd api
curl localhost:8085/health
```

Desain lengkap: `docs/sdd/04-SDD-RBT-001.md`. Struktur folder mengikuti standar organisasi (ADR-016).
