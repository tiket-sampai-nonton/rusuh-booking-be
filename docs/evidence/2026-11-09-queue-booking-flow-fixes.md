# Evidence 2026-11-09: perbaikan alur Queue → Booking (T5, T6, T1, T2)

Cabang `fix/queue-booking-flow-t5-t6-t1-t2` (basis `feature/rework@5d2f7b6`). Temuan asal: `docs/happy-path-endpoints.md` (T1, T2, T5, T6).

## Keputusan pemilik (2026-11-09)

- **OQ-1 = (b):** `DELETE /api/v1/queue-entries/{id}` pada entry CLAIMED ditolak dengan `409 ENTRY_CLAIMED`. Klien membatalkan lewat
  `POST /booking-sessions/{id}/cancel`. Tidak pernah 204 senyap. Menggantikan sebagian ADR-028 (QSH-OQ-3) dan baris TC-QUE-23/23b.
- **OQ-5 = YA:** `:release` (internal) juga menerbitkan `booking.journey.ended` dalam transaksi pelepasan (jalur bersama dengan cancel).
  Tidak ada event bila admission sudah terminal. Untuk pelepasan yang diprakarsai Queue, event tiba setelah entry berakhir dan dijawab
  `IGNORED` oleh `end_entry.lua` (satu pesan terkonfirmasi tambahan).

## Perubahan perilaku yang terlihat (ditandai agar ditinjau)

1. **Cancel pada admission EDITABLE** (sudah ada hold): sebelumnya menandai admission RELEASED dan melepas antrean padahal pesanan masih bisa dibayar.
   Sekarang `409 BOOKING_LOCKED`, tanpa perubahan status/stok/event (invarian 1 dan 2). `:release` internal memetakan ini ke `released=false`
   (kontrak internal tidak berubah). Ini perubahan perilaku yang tidak disebut eksplisit oleh pemilik; dilindungi TC-QUE-37 (P3).
2. **Cancel sesi mengirim `booking.journey.ended` outcome `LEFT_WITHOUT_BUYING`**, `:release` mengirim `UNCLAIMED` (nilai enum yang sudah ada;
   skema event tidak berubah). Baris SDD §8.1 diselaraskan ke skema (`{eventId, queueEntryId, outcome}`).
3. **Deadline sesi** = waktu aktivasi (token admission dipakai) + `cfg_selection_ttl_seconds`; sebelumnya reservasi + jendela klaim.
   Tanpa migrasi. Jendela klaim tetap membatasi RESERVED → ACTIVE.
4. **Join gate** di Queue: `409 EVENT_NOT_ON_SALE` (sebelum `salesStartsAt` atau sejak `salesEndsAt`), `410 EVENT_SOLD_OUT`
   (`CLOSED` + `soldOut`), `503 DEPENDENCY_UNAVAILABLE` bila jendela belum diketahui dan Booking tidak terjangkau (gagal tertutup).
   Presedensi: belum mulai 409 > habis 410 > berakhir 409. `start <= now < end` terbuka.

5. **`booking.sold_out` hanya bila free + allocated + held = 0** (SRS-F-09-05, requirement 5.6). Ditemukan saat uji curl hidup (T14): `reserveOne`
   langkah 7 menandai SOLD_OUT dan menerbitkan `booking.sold_out` begitu satu reservasi mengambil unit bebas terakhir (padahal reservasi itu
   bisa kembali), dan `settle` memakai `allocated + held + sold >= total` (alokasi pembeli lain masih bisa kembali). Sebelumnya tak terlihat
   karena join tidak membaca flag sold-out; dengan join gate, pembeli berikutnya ditolak 410 walau tiket masih bisa kembali. Diperbaiki ke aturan
   yang sudah tertulis di SRS; skema event tidak berubah, hanya saat terbitnya. Di luar touch-set rencana (`unit_of_work.go`), dicatat di sini.
   Test: `TestSoldOut_OnlyWhenNothingCanReturn` (merah 2 dari 4 kasus sebelum perbaikan).

## Catatan desain dan trade-off

- **Propagasi jendela penjualan:** jendela di-cache di hash `q:{e}:sale` oleh `StatusPublisher` (tiap tick, dari `Booking.Supply`). Perubahan jendela oleh admin
  baru terlihat di gate dalam satu interval publisher (default 1 s). Cache dingin = satu `Supply` baca saja.
- **Replay `Idempotency-Key` setelah habis** menjawab 410 (gate berjalan sebelum `Store.Join`); entry yang sudah ada tidak terpengaruh, `queue-entries/me` tetap bekerja.
- **Baris in-flight (3.7):** admission ACTIVE saat deploy mempertahankan `expires_at` tersimpan (tanpa backfill); dijaga test.
- **Requirement 5.5 (jendela balapan sold-out), keputusan pemilik pada gerbang:** konsumer `booking.sold_out` kini menutup sale hash lalu
  mengakhiri aktif setiap entry WAITING dan STALE event itu dengan status `SOLD_OUT` (`end_sold_out.lua`, enum `queue.yaml`; `me` = state `ENDED`,
  status `SOLD_OUT`, reason `SOLD_OUT`). ADMITTED/CLAIMED tidak disentuh (Jangan PHP). Idempoten (kirim ganda tidak mengubah apa pun), dibatasi
  batch 500 dan 20 batch per delivery; sisanya dilanjutkan lewat error retryable (bukan Permanent). Status Document tetap `soldOut=true`.
- **`docs/happy-path-endpoints.md`** untracked di checkout utama; cabang ini melacaknya (salinan di sini). Salinan di checkout utama akan konflik hanya bila diubah terpisah; ambil versi cabang saat merge.

## Bukti

Perintah gate, exit code, ringkasan test, dan hash commit: `.agents/tasks/fix/queue-booking-flow-t5-t6-t1-t2/verification.md`.
Merah-lalu-hijau tiap task terekam di sana. Hasil curl hidup (T14) dicatat di bawah.
