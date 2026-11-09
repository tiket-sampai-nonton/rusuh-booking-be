# Uji nyata curl: happy path dan edge case semua endpoint (2026-11-09)

## Permintaan

Pemilik meminta: build ulang semua service, jalankan api + worker + migrasi, uji dengan curl berdasarkan
`docs/happy-path-endpoints.md`, tambahkan endpoint yang belum tercakup ke dokumen itu, uji nyata, paralel bila perlu.

## Lingkungan

- Kode: `feature/rework` @ `5d2f7b6`, tanpa perubahan kode produksi.
- 5 service dibangun ulang (`go build`), `migrate up` per service (Payment menerapkan 4 migrasi pertama kali; lainnya 0),
  api + worker berjalan di host lewat `.agents/tasks/local/run/start.sh`. 9 endpoint health 200.
- Infra yang sudah berjalan: PostgreSQL `:5432` db `berebut`, Valkey cluster `7000-7005`, RabbitMQ `:5672`, Mailpit.
- `services/payment/.env` dibuat (diabaikan git) dari `.env.example`; Midtrans diganti gateway tiruan
  `.agents/tasks/local/run/fake_midtrans.py` (`:18089`) karena kunci sandbox tidak tersedia. Gateway tiruan menerapkan
  `POST /snap/v1/transactions`, `GET /v2/{order}/status`, `POST /v2/{order}/cancel`, dan mengirim notifikasi HTTP
  bertanda tangan `SHA512(order_id+status_code+gross_amount+serverKey)` ke webhook Payment.
- Penyesuaian lokal satu-IP: `RATE_LIMIT_JOIN_PER_IP=300`, `RATE_LIMIT_PER_IP=0` (booking/payment),
  `OTP_MAX_PER_IP_HOUR=1000`. Batas per IP OTP bawaan (20/jam) terbukti menolak (429) sebelum dinaikkan.

## Skrip

`.agents/tasks/local/run/curl/`: `lib.sh` (helper), `t_guest.sh`, `t_user.sh`, `t_admin.sh`, `t_edge_queue.sh`,
`t_edge_hold.sh`, `t_edge_pay.sh`, `t_misc.sh`, `t_slow.sh` (timer nyata, ~7 menit, bagian paralel),
`t_ratelimit.sh`, `t_dependency.sh` (menghentikan Payment API sebentar), `run_all.sh`. Suite independen berjalan paralel,
masing-masing membuat event sendiri lewat admin API. Data uji hanya lewat endpoint (ditambah baca DB untuk verifikasi).

## Hasil

224 cek lulus, 14 gagal; 14 kegagalan = 8 temuan (T1–T8) yang dicatat di `docs/happy-path-endpoints.md` §Temuan.
Kegagalan awal yang ternyata salah skrip (bukan service) diperbaiki dan diuji ulang: `current` butuh `?eventId=`,
`selectionTtlSeconds=60` ditolak CHECK `events_check3` (peringatan 60 dtk harus < TTL), desain uji `QUANTITY_INVALID`,
dan parsing kode `BUYER_NIK_THROTTLED`.

Temuan yang dicek ke kode (bukan asumsi):
- T5: `AdmissionRepo.Release` (dipakai `CancelSession`) tidak menulis outbox `booking.journey.ended`; `leave.lua` baris
  12 mengabaikan entri `CLAIMED` (`IGNORED`, QSH-OQ-3) tetapi handler tetap menjawab 204. Slot tidak bebas setelah 392 dtk.
- T6: `AdmissionRepo.Activate` hanya menyetel `status='ACTIVE', claimed_at`; `expires_at` tetap = reservasi + 180 dtk,
  padahal SDD (diagram state) menyatakan TTL Booking mulai saat klaim.
- T1/T2: `ErrNotOnSale`/`ErrSoldOut` hanya didefinisikan dan dipetakan, tidak dikembalikan oleh join; `join.lua` tidak
  membaca hash sale (`saleState=CLOSED soldOut=1` sudah tersedia).
- T7: `KeepFromItemID` hanya ada di DTO, tidak dibaca di usecase edit.
- T8: tidak ada kode untuk `/api/v1/local-simulator/...`.

## Tidak diverifikasi

Midtrans sandbox asli (dipakai gateway tiruan), timeout gateway, push FCM nyata (provider log), perangkat native,
konfigurasi produksi (CloudFront/WAF).

## Data tersisa

Event uji ("Guest Journey", "Edge …", "Slow …", "Rate Limit", dll.) beserta booking, tiket, dan entri antrean tetap di
database `berebut` dan Valkey `7000-7005`.
