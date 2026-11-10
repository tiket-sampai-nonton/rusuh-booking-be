# Evidence 2026-11-09: pass matriks kontrak (Batch 4)

Task `fix/contract-matrix`. Verifikasi saja: tidak ada perubahan di `services/`, `pkg/`, `docs/contracts/` atau migrasi. Laporan lengkap: `.agents/tasks/fix/contract-matrix/report.md`; matriks per baris: `matrix.tsv`; respons tak terdokumentasi: `undocumented.tsv`.

## Metode
- Matriks dibangkitkan dari kontrak saat ini (`.agents/tasks/local/run/curl/gen_matrix.py`): satu baris per metode × path × status × errorCode (token UPPER_SNAKE di deskripsi respons, termasuk kata tunggal seperti `UNAUTHORIZED`) + `/health` 200/503 tiap berkas. **249 baris** (matriks lama 237 baris memakai aturan yang lebih sempit).
- Setiap baris dijalankan dengan HTTP nyata ke stack yang sudah berjalan (skrip Python di `.agents/tasks/local/run/curl/matrix/`, dipakai ulang helper admin/OTP/queue/booking/payment dari `lib.sh`; token service ditandatangani dengan kunci `deployments/compose/keys/` seperti `pkg/token`). Bagian paralel: booking publik, queue, payment, user + notification, internal booking, timer nyata, instance sementara; lalu sendirian: outage (satu proses per kali, dipulihkan dengan biner/cwd/env yang sama seperti `start.sh`), rate limit, health.
- Instance sementara (port 18081-18085, dimatikan setelah selesai) hanya untuk kondisi yang tidak boleh dipaksakan pada stack bersama: booking dengan `RATE_LIMIT_PER_IP=5` (429), `/health` 503 lewat forwarder TCP ke Postgres yang sudah ada lalu ditutup, OTP 503 lewat forwarder ke RabbitMQ yang sudah ada lalu ditutup. Tidak ada container yang dibuat, dimulai, dihentikan, atau dihapus; Valkey tidak di-flush; tabel tidak dipangkas; data uji dibuat lewat API.
- Verifikasi silang ke log: 1902 requestId tercatat, 1867 ditemukan di log api dengan status sama; sisanya panggilan `/health` (tidak dilog) dan instance sementara.

## Hasil
249 baris: **234 sesuai**, **10 dijalankan tetapi berbeda**, **4 tidak bisa dipicu lokal**, **1 tidak berlaku**. Tidak ada baris belum dijalankan.

Berbeda: `POST /booking-sessions` 410 `SESSION_EXPIRED`/`TURN_MISSED` (kode: 403 `ADMISSION_TOKEN_INVALID`), SSE 404/403 (kode: 200 aliran kosong), attendee-check 403 (kode: 200, tanpa cek pemilik), tickets 403 (kode: 404 sesuai desain), PATCH event 409 (tidak terjangkau), payment-page-sessions 401 (rute anonim), claim 410 `EVENT_SOLD_OUT` (404) dan claim 429 (tidak ada limiter).
Tidak bisa dipicu: SSE 503, `ACCOUNT_DISABLED` (2 baris), logout 500. Tidak berlaku: push 401 `UNAUTHORIZED` (kontrak sendiri menyatakan kodenya `VALIDATION_ERROR`).

Temuan di dalam baris yang tetap lulus (varian tanpa token): `PUT /bookings/{id}/items` tanpa token 200 dan booking berubah; `GET /booking-sessions/{id}` tanpa token 200; `POST .../attendee-checks` tanpa token atau token visitor lain 200 (jawaban `ALREADY_REGISTERED`/`AVAILABLE` terverifikasi); `POST /bookings` tanpa token 500 (`idem: begin ... uuid ""`); `DELETE /push-subscriptions/{id}` non-UUID 500. Akar: `visitorID != ""` melewati cek pemilik (`session.go:57`, `edit.go:46`), `CheckAttendee` tanpa parameter visitor. Belum diperbaiki; keputusan pemilik.

## Tidak diverifikasi
Lihat bagian 9 laporan: `ACCOUNT_DISABLED` dan logout 500 tidak dijalankan; SSE 503 hanya dari pembacaan kode; `/health` 503 dan 429 booking pada instance sementara, bukan proses utama; limit OTP per-IP-jam tidak dihabiskan (baris yang sama tercakup oleh limit per-email); bentuk payload 2xx tidak dibandingkan dengan skema.

## Keadaan akhir
Sembilan endpoint health (8081-8085, 8091-8094) menjawab 200. Proses yang dihentikan sementara (Payment API, Booking API, fake Midtrans) dihidupkan kembali dengan biner/cwd/env yang sama dan `pids` diperbarui; instance sementara dan forwarder dimatikan.
