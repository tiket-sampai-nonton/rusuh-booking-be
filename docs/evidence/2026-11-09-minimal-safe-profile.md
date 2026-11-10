# Profil minimal aman "tanpa PHP" (2026-11-09)

Pertanyaan: sistem apa yang aman, tidak mem-PHP pembeli, dan tahan banting dengan spek minimal?

## Jawaban singkat
"Tidak bisa rusak" tidak ada. Yang bisa dijamin: **kebenaran tidak bergantung pada spek**; spek hanya menentukan
berapa banyak orang yang dilayani sebelum sistem menolak (429/503) alih-alih salah. Ini sesuai invarian 1-3, 5-6
(`.kiro/steering/invariants`): jaminan ada di transaksi PostgreSQL, script Lua atomik, dan UNIQUE constraint,
bukan di jumlah CPU.

## Bukti: invarian utama tetap lulus di spek sangat kecil
Postgres 16 dibatasi 1 vCPU / 256 MB / shared_buffers 64 MB / 30 koneksi; Valkey 0,5 vCPU / 128 MB.

| Invarian | Test | Hasil |
|---|---|---|
| 1 Tanpa oversell | `TestTC_BKG07_NoOversell` (100 hold paralel, stok 50 -> tepat 50 sukses), `TestHoldBuyerNIK_NoOversellUnderConcurrency` | PASS (-race) |
| 3 Kuota NIK lintas sesi | package `booking/infrastructure/postgres` penuh | PASS (184 s) |
| Event sourcing `ErrConcurrency` | `TestEventSourcing_ConcurrencyGuard` | PASS |
| 2 Tanpa PHP (admission replay, lease promotor, claim/leave, sold-out) | `services/queue/internal/queue/...` integration, Valkey nyata | PASS semua paket (worker 150 s) |

Perintah: `TEST_POSTGRES_DSN=... go test -tags integration -race -count=1 ./internal/booking/infrastructure/postgres/`
(booking) dan `TEST_VALKEY_SERVER=... TEST_POSTGRES_DSN=... go test -tags integration -race -count=1 ./internal/queue/...` (queue).

## Spek minimal yang disarankan (aman, bukan cepat)
- 1 host stateful (Postgres + Valkey + RabbitMQ): cukup t4g.small/medium untuk assessment (HLD sudah memakai t4g.medium).
- 1 instance Queue + 1 Core (Booking/Payment): t4g.small. Skala keluar hanya Queue (ASG min1 max3).
- Pengaman yang menjaga "tanpa PHP" saat kewalahan: `active_slot_capacity` kecil per event (admit hanya sebanyak
  stok yang bisa dilayani), rate limit per IP/visitor (fail-closed), join gate `503` bila Booking mati, TTL
  server-authoritative, outbox + DLQ.
- Aturan emas: saat kapasitas habis, **tolak di pintu antrean** (WAITING/429), jangan pernah admit melebihi slot.

## Yang TIDAK terbukti (jujur)
- Throughput join/s dan 1 juta waiter (SRS-Q-PERF-02/03, TC-PERF-01..05) tidak diukur di sesi ini; k6 harness ada di `tests/k6`.
- Host stateful tunggal = SPOF ketersediaan (bukan integritas data). Untuk "unbreakable" sebenarnya butuh
  Postgres replica/Multi-AZ, Valkey replica, RabbitMQ quorum; di luar "minimal spek".
- `make ci` penuh tidak dijalankan di sini (pkg/* butuh kredensial `rbt`).
