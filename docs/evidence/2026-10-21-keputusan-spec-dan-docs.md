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
