# Panduan Lokal & Implementasi — Rusuh Booking Tiket

Untuk: arnu12rahman (Notification, Payment, platform). Versi 2026-10-21.

Dokumen ini menjelaskan cara menyiapkan lingkungan lokal, urutan coding, dan di mana mencari jawaban
saat ragu. Detail desain ada di `specs/`; dokumen ini hanya menunjukkan jalannya.

---

## 0. Gambaran alur sistem (singkat)

1. **Join antrean** (Queue). Pembeli mengisi NIK dan email, lalu mendapat nomor urut.
2. **Admission** (Queue → Booking). Saat giliran tiba, Queue meminta Booking mereservasi tiket, dan Booking menyisihkan `units` (maks 3). Queue lalu mengirim push "giliran tiba" lewat Notification.
3. **Sesi pilih** (Booking). Pembeli punya 5 menit untuk memilih kategori dan mengisi NIK pemegang, lalu menekan "Lanjut" (hold). TTL pembayaran 10 menit dimulai sejak hold.
4. **Bayar** (Payment). Pembeli memilih metode. Payment memanggil `payment-lock` ke Booking (pesanan dikunci), lalu membuat transaksi di Midtrans.
5. **Settlement**:
   - Midtrans mengirim webhook ke Payment.
   - Payment menerbitkan `payment.succeeded`.
   - Booking menandai PAID dan menerbitkan tiket serta `booking.paid`.
   - Notification mengirim email e-tiket.
6. **Expiry.** Kalau tidak dibayar, Booking meminta Payment memastikan status di Midtrans (resolve), lalu melepas stok dan menerbitkan `booking.expired`. Notification mengirim email.

Antar service berkomunikasi lewat HTTP internal (dengan service token) dan event RabbitMQ (exchange
`rusuh.events`). Stok dan uang hanya diubah oleh Booking.

---

## 1. Persiapan infra lokal

### Yang perlu dipasang di laptop
- Docker Desktop (sudah ada: server 29.5.2) — dipakai untuk semua infra.
- Go 1.26+ (sudah ada: 1.27.2), `make`, `openssl`.
- `golangci-lint` v2 untuk `make lint`.
- Opsional: `ngrok` atau `cloudflared` (hanya saat menguji webhook Midtrans Sandbox).

### Image yang dipakai (dari `docker-compose.yaml`)
- `postgres:16-alpine` — port 5432. Satu database `rbt`, satu schema per service.
- `valkey/valkey:8.1-alpine` — port 6379. Antrean, rate limit, cache, OTP.
- `rabbitmq:3.13-management-alpine` — port 5672; UI di http://localhost:15672 (guest/guest).
- `axllent/mailpit:v1.21` — SMTP 1025; inbox di http://localhost:8025.
- Image service `rusuh-booking-<service>:local`, dibangun dari `services/<service>/deployments/Dockerfile` saat `make up`.

### Yang **tidak** perlu untuk lokal
- **Terraform / AWS** — hanya untuk demo cloud (platform task 11, prioritas rendah).
- **Observability (Prometheus/Grafana)** — belum ada; menunggu keputusan library metrik (OQ-5).
- **FCM** — di lokal push cukup dicatat ke log (notification task 0b.R1).
- **Midtrans** — mulai dengan simulator (`PAYMENT_PROVIDER=simulator`, payment task 12). Akun Sandbox baru dibutuhkan saat payment task 9/13:
  1. Daftar di dashboard Midtrans, mode Sandbox, lalu ambil Server Key dan Client Key. Simpan di `.env` dan jangan pernah di-commit.
  2. Midtrans perlu URL publik untuk webhook. Jalankan `ngrok http 8083`, lalu isi Payment Notification URL di dashboard dengan `https://<ngrok>/api/v1/webhooks/midtrans`.

### Langkah menyalakan lokal
```bash
cd rusuh-booking-be
make keys                 # buat kunci token lokal (deployments/compose/keys/, tidak di-commit)
make infra                # PostgreSQL, Valkey, RabbitMQ, Mailpit
docker compose ps         # pastikan semua "healthy"/"running"
```

Jalankan satu service dari source (cara utama saat coding):
```bash
cd services/notification
cp .env.example .env      # sekali saja; isi variabel yang belum ada (lihat catatan di bawah)
go run ./cmd migrate up
go run ./cmd api          # terminal 1
go run ./cmd worker       # terminal 2 (consumer/sweeper)
curl localhost:8084/health
```

Port service: Queue 8081, Booking 8082, Payment 8083, Notification 8084, User 8085.

Atau jalankan semuanya lewat container (untuk uji end-to-end):
```bash
make up                   # build image + migrasi + api/worker semua service
make logs                 # ikuti log
make down                 # matikan dan hapus volume (data hilang)
```

Catatan: `.env.example` Notification belum memuat `SMTP_*`, `FCM_*`, dan `PII_*` yang dipakai kode
DEV-2, dan compose User masih memakai variabel SMTP padahal User kini mengirim OTP lewat RabbitMQ.
Keduanya diperbaiki di langkah pertama coding (lihat §2).

---

## 2. Urutan coding (scope kamu)

Setiap task dikerjakan dengan workflow `/feature`. Workflow ini akan:
1. merencanakan dan meminta persetujuanmu;
2. menulis test dulu, lalu kode;
3. menjalankan review keamanan;
4. menjalankan quality gate (`go test`, lint);
5. memperbaiki sendiri bila gagal, maksimal 3 putaran.

Contoh:
```
/feature notification task 0a.R1 (specs/notification-service/tasks.md): klaim delivery kondisional agar retry mengirim ulang
```

Urutan yang disarankan:

1. **Lokal bisa jalan.** Lengkapi `.env.example` dan compose untuk Notification dan User, lalu pastikan `make up` hijau dan `/health` semua service `UP`.
2. **platform 2.6 + 4.4** — format error `code` + `message`, `requestId` di header, dan redaksi data pribadi di log. Satu perubahan di `pkg/`, berlaku untuk lima service.
3. **notification 0a.R1** — bug retry email (paling berdampak), lalu 8.R1 (breaker SMTP) dan 0b.R1 (push ke log).
4. **platform 5.2** — schema kontrak `otp.requested.v1` dan `user-service` di enum `producer` (bersamaan dengan user 1.R1 milik nuzirwan).
5. **platform 3** — pindahkan relay outbox Booking ke `pkg/outbox`, supaya Payment bisa memakainya.
6. **payment** — urutan Task 0 → 12 (simulator) → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 11 → 10 → 13. Selama booking Task 13 (`payable`/`payment-lock`) belum dibuat nuzirwan, pakai klien Booking palsu di test.
7. **platform 13 + notification 12 + payment 14** — job retensi data pribadi.
8. **platform 12** — penyelarasan routing key, bersama nuzirwan dalam satu PR.

Definisi "selesai" untuk setiap task:
- `make ci` hijau (build, lint, test `-race`, coverage ≥ 80%, build isolation);
- checkbox task di `specs/<service>/tasks.md` diubah ke `[x]`;
- satu baris di `docs/ai-log/README.md`;
- bila keputusan desain berubah, catatan di `docs/evidence/` dan `specs/` diperbarui;
- PR direview pemilik lain sesuai `.github/CODEOWNERS`.

Prinsip kode supaya mudah dijelaskan:
- satu usecase per file, dan nama fungsi yang menjelaskan maksudnya;
- respons persis seperti kontrak (tanpa field tambahan) dan query hanya mengambil kolom yang dipakai;
- error ke klien hanya `code` + `message`; detail di log, dan log tidak pernah memuat NIK, email, token, atau kode OTP;
- tidak ada panggilan HTTP/gateway di dalam transaksi database.

---

## 3. Di mana mencari jawaban

- **Apa yang harus dibuat:** `specs/<service>/requirements.md`. Requirement *N*, kriteria *N.M*.
- **Bagaimana membuatnya:** `specs/<service>/design.md` (SQL, state machine, alur).
- **Langkah kerja dan status:** `specs/<service>/tasks.md`.
- **Konflik dan pertanyaan terbuka:** `specs/README.md`.
- **Bentuk API dan event yang mengikat:** `docs/contracts/openapi/*.yaml`, `docs/contracts/events/*.schema.json`.
- **Latar belakang bisnis dan aturan "shall":** `docs/analisa/01-PRD…`, `02-SRS…`.
- **Arsitektur dan alasan keputusan:** `docs/analisa/03-HLD…`, `05-ADR…`.
- **Desain rinci (lock order, SQL, sweeper):** `docs/analisa/04-SDD-RBT-001.md` §4.4–4.10.
- **Daftar test case (TC-xxx):** `docs/analisa/06-TP-RBT-001.md`.
- **Integrasi Midtrans:** `docs/analisa/08b-payment-gateway-integration.md`.
