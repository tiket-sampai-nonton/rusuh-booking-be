# Evidence — Keputusan Spec, Merge, dan Struktur Docs (21 Oktober 2026)

Peserta: arnu12rahman (BE-1), nuzirwan (BE-2, lewat jawaban tertulis), dibantu Claude Code.

## Keputusan

1. **Format spec.** `specs/<service>/{requirements,design,tasks}.md` mengikuti format Kiro, ditambah spec `platform/`. Urutan prioritas sumber dan register konflik ada di `specs/README.md`.
2. **Merge task DEV-2.** Penomoran task DEV-2 dipertahankan (cocok dengan `.progress.json`); isi diselaraskan dengan spec terbaru. Perbaikan atas kode yang sudah ada ditulis sebagai sub-task "R".
3. **Pembagian kerja.** arnu12rahman: Notification, Payment, platform. nuzirwan: User, Queue, Booking.
4. **Format error.** Body hanya `{"meta":{"code","message"}}`; `message` boleh diawali kode error (`KODE: pesan`). `requestId` di header `X-Request-ID`. Detail dan payload request (sudah diredaksi) hanya di log. Alasan: respons sederhana, detail tidak bocor ke klien. Dampak: FE web/mobile perlu membaca kode dari `message`.
5. **Data pribadi (UU PDP).**
   - Email pembeli di Booking dienkripsi seperti di User (task booking 15.1, untuk nuzirwan).
   - Data pribadi booking dibersihkan 30 hari setelah event.
   - Token push dihapus saat penjualan berakhir.
   - Outbox yang sudah terkirim dihapus setelah 7 hari.
   - Log disimpan maksimal 30 hari (platform R13).
   - Angka akhir masih perlu dikonfirmasi klien.
6. **Penyederhanaan.** Event `RefundCompleted` di Payment dihapus (refund manual oleh Ops). Terraform, k6, dan tool rekonsiliasi/redrive menjadi prioritas rendah. Usulan respons status Payment yang lebih ramping diajukan ke FE (OQ-22).
7. **Struktur docs.** Dokumen planning dipindah ke folder datar `docs/analisa/`. `contracts/`, `ai-log/`, dan `evidence/` dipertahankan: ai-log wajib menurut aturan assessment (SRS-C-06), evidence wajib menurut steering `testing-review.md`.
8. **Pencatatan.** Setiap sesi AI dicatat di `docs/ai-log/`; setiap diskusi yang mengubah dokumen planning dicatat di `docs/evidence/`.
9. **`tools/` tetap di repo.** Script di dalamnya dipakai CI (`ci.yml`) dan `make cover`, jadi bukan hanya untuk lokal.

## Temuan verifikasi kode DEV-2 (dicatat sebagai rework)

- Booking: hold tidak mengurangi `allocated_units`; sweeper sesi tidak mengembalikan alokasi `ACTIVE`/`EDITABLE`; payload `booking.journey.ended` tidak cocok dengan schema; settlement tidak memakai `settlementTime`; endpoint `supply`, `:release`, `payable`, dan `payment-lock` belum ada.
- Queue: direct admission mengecek `RESERVED` padahal Booking membalas `GRANTED`; entry kuota habis menyumbat antrean; `queue.turn.soon` tidak pernah dipublikasikan.
- Notification: klaim delivery `ON CONFLICT DO NOTHING` membuat retry tidak pernah mengirim ulang; SMTP belum dibungkus breaker.

Rincian: `specs/*/tasks.md` (sub-task R) dan `specs/README.md` bagian "Status open question".

## Tambahan — merge `checkpoint #1` DEV-2 (21 Oktober 2026)

- DEV-2 menambah schema `otp.requested.v1`, `publicRef` di `Payable`, penerbitan `quota.exhausted` (saat `paid = max`) dan `sold_out`, serta penanganan `ALREADY_ACTIVE` di promotor Queue. Semua sesuai keputusan OQ-4, OQ-13, OQ-18, OQ-21.
- Usulan DEV-2 "Task 14" (status `REVIEW` pada attempt Payment) **tidak diambil**: migrasi `0001` tidak memuat status itu dan tidak boleh diubah. Nominal webhook tidak cocok → callback ditolak/dicatat tanpa mengubah state; `REVIEW` hanya status booking.
- Defect baru di kode DEV-2: query sold-out di `settle` membaca kolom `total_units` yang tidak ada (`total_capacity`). Dalam transaksi PostgreSQL, ini membuat transaksi aborted sehingga semua settlement gagal (booking 9.R3).
- Pengikatan admission token ke visitor kini hanya di usecase (token tidak lagi membawa visitor); aman selama pengecekan `admission.VisitorID` dipertahankan.

## Tambahan — observability lokal (21 Oktober 2026)

Keputusan: ADR-023 (OQ-5 ditutup). Log JSON ke stdout + access log + `trace_id`; metrik Prometheus; lokal memakai Prometheus, Loki, Grafana Alloy (pengganti Promtail yang end-of-life Maret 2026), dan Grafana.

Temuan: service belum punya access log (hanya error 5xx dan panic), jadi Loki hanya berisi sedikit log sampai platform 4.5 dibuat.

Verifikasi `make obs` (dijalankan): keempat komponen sehat; file log JSON masuk Loki dengan label `container`/`level`/`source`; pencarian `| json | requestId="…"` mengembalikan baris yang benar; log container RabbitMQ terbaca; Grafana memuat datasource Prometheus + Loki dan dua dashboard; query Grafana→Loki lewat proxy berstatus 200. Loki 3.5.0 menghasilkan error kosmetik "negative structured metadata bytes"; hilang setelah naik ke 3.5.7. Metrik belum bisa diuji karena `/metrics` belum dibuat (platform 4.2).

## Tambahan — penyedia email Resend (22 Oktober 2026)

Keputusan: email demo/nyata memakai Resend (HTTP API) dan lokal/CI tetap Mailpit, dipilih lewat `EMAIL_PROVIDER=mailpit|resend` (notification R13, task 0c). Nama env dipilih `EMAIL_PROVIDER` (bukan boolean `send_mail`) agar eksplisit dan dapat diperluas; default `mailpit` supaya tidak ada email keluar tanpa disengaja. Menyimpang dari SRS-IF-22 (SES sandbox) — SRS perlu diperbarui (K13).

Diverifikasi ke dokumentasi Resend: `POST https://api.resend.com/emails`, auth Bearer, `Idempotency-Key` (24 jam), balasan `{"id"}`; 403 bila domain belum terverifikasi atau mengirim ke alamat lain tanpa domain terverifikasi; 429 `rate_limit_exceeded`; batas rate default dan kuota harian gratis tidak tercantum di halaman yang dibaca. Belum diuji mengirim email nyata.

Pembaruan 22 Oktober: SES dihapus dari SRS-IF-22, HLD, handbook, diagram E2E, dan SDD Notification; diganti Resend. Akun Resend gratis: 100 email/hari, 3.000/bulan, sehingga pengembangan dan test memakai Mailpit dan Resend hanya untuk demo (notification R13.11).
