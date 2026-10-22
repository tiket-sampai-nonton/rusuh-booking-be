# Implementation Plan: Queue Service

Owner: **DEV-2**. Penomoran Task 0–16 dari DEV-2 dipertahankan; isinya diselaraskan dengan
`requirements.md`/`design.md` terbaru. Sub-task **R** = perbaikan kode yang sudah ada. Panduan teknis
rinci versi DEV-2 ada di riwayat git (commit `88c9dc8`).

Status 2026-10-22: join, status document, posisi, claim, leave, promotor, sweeper, consumer, dan 5
skrip Lua sudah ada dan test unit lulus. Semua perbaikan R selesai kecuali yang bergantung pada
service lain (9.R1, 13.R1, 14.R1 diblokir) dan 16.1 (menunggu pemilihan library metrik).

## Tasks

- [x] 0. Config, kunci, scaffold modul
  - _Requirements: 10.2_

- [x] 1. Domain model — Entry, value object, state machine
  - [x] 1.R1 Status `REJECTED` tidak ada di kontrak `QueueEntry`. Untuk kuota habis pakai `ENDED` + `reason = BUYER_QUOTA_EXHAUSTED`; `REJECTED` cukup sebagai aksi audit
    - Dihapus dari `valueobject/status.go`, `CanTransitionTo`, `IsTerminal`, dan test; tidak ada di `queue.yaml`
    - _Requirements: 6.4, 5.5_

- [x] 2. Outbound ports + mocks
  - _Requirements: 10.1_

- [x] 3. Skrip Lua (join, promote, claim, leave, end_entry)
  - **Property 1: Gapless monotonic sequence**, **Property 4: One active entry per NIK / visitor / email**
  - _Requirements: 2.7, 2.8, 2.9, 2.10_

- [x] 4. QueueStore + RateLimiter (Valkey)
  - Ada: `queue_store.go`, `rate_limiter.go`
  - [x] 4.1 Integration test Lua dengan Valkey nyata (10.000 join paralel tanpa celah seq)
    - `queue_store_integration_test.go` (build tag `integration`): `TestQueueStore_JoinGaplessSeq` + `TestQueueStore_JoinNIKConstraint`
    - _Requirements: 2.6, 2.14_

- [x] 5. Repository Postgres — `event_settings` & `entry_audit`
  - _Requirements: 4.4, 4.6_

- [x] 6. Security adapter — token, verifikasi user token, PII
  - Ada: visitor/queue/admission token, user token, PII
  - [x] 6.R1 Validasi NIK sekarang hanya "16 digit". Ganti dengan `pkg/nik` (provinsi, kab/kec, tanggal lahir, nomor urut)
    - `join.go` `validateBuyerNik` memanggil `nik.Validate(nik, now)` setelah fast pre-check domain; test struktural ditambahkan
    - _Requirements: 2.4_
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

- [x] 7. Booking client (HTTP keluar) dengan circuit breaker
  - _Requirements: 6.1_

- [x] 8. Join + direct admission
  - Ada: `join.go`, handler join
  - [x] 8.R1 **Defect:** direct admission mengecek `Status == "RESERVED"`, padahal Booking membalas `outcome = "GRANTED"`. Akibatnya direct admission tidak pernah terjadi dan semua orang masuk `WAITING`. Hentikan juga pengiriman `units` (Booking yang menghitung)
    - **Property 3: No admission without reservation**
    - _Requirements: 2.5_
  - **Property 6: Fail closed**
  - _Requirements: 1.5, 1.6, 2.1, 2.2, 2.3, 2.6, 2.11, 2.12, 2.13, 2.14_

- [ ] 9. Status Document + Status Publisher
  - Ada: publisher + `GET /queue-status/{eventId}`
  - [ ] 9.R1 Publisher memanggil `GET /internal/v1/events/{id}/supply` yang belum ada di Booking; tunggu booking 4.R1, lalu uji zona dan `categories[].state`
    - Unit coverage done (mocked Supply + `TestStatusPublisher_WritesDocument`); integration test blocked on booking 4.R1.
    - _Depends on: booking 4.R1_
    - **Property 8: No PII in Status Document & events**
    - _Requirements: 4.5, 4.6, 4.7_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.8_

- [x] 10. Get My Entry — posisi eksak
  - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5_

- [x] 11. Promotor + claim + claim sweeper
  - Ada: `promoter.go`, `sweeper.go`, claim handler
  - [x] 11.R1 **Defect (selesai):** `ALREADY_ACTIVE` kini mengakhiri entry dan dicatat sebagai peringatan (`promoter.go`) ✅. `BUYER_QUOTA_EXHAUSTED` kini juga mengakhiri entry (`ENDED` + `reason`) dan membersihkan constraint key, sehingga promotor tidak lagi mengirim ulang entry yang sama setiap tick ✅. `end_entry.lua` memperlakukan outcome `ALREADY_ACTIVE` sebagai `ENDED` ✅. Test `TestPromoter_AlreadyActiveEndsEntry` ditambahkan.
    - _Requirements: 6.4, 6.13_
  - [x] 11.R2 Sweeper tidak boleh menerbitkan `booking.journey.ended`, karena itu event milik Booking. Cukup tandai `MISSED` dan lepas slot; Booking melepas alokasinya sendiri (K7)
    - `sweeper.go` dihapus panggilan `PublishJourneyEnded`; field `pub` dihapus dari `Sweeper`; wiring di `queue_module.go` diperbarui; test diperbarui ke `TestSweeper_MarksMissedOnly`
    - _Requirements: 6.10_
  - [x] 11.R3 Satu promotor per event lewat lease Valkey (`SET NX PX`) agar dua instance worker tidak promote bersamaan
    - `AcquirePromoterLease`/`ReleasePromoterLease` ditambahkan ke `IQueueStore`; diimplementasikan di `queue_store.go`; dipanggil di `promoter.runEvent`; test `TestPromoter_SkipsEventWhenLeaseHeld` ditambahkan
    - **Property 7: Single promoter**
    - _Requirements: 6.12_
  - [x] 11.R4 `NO_SUPPLY_NOW`: berhenti di entry itu pada tick yang sama (jaga FIFO)
    - `case "NO_SUPPLY_NOW": noSupply = true` ditambahkan; `promoteCands` dibersihkan bila `noSupply`; test `TestPromoter_NoSupplyNowSkipsPromotion` ditambahkan
    - **Property 2: FIFO admission**
    - _Requirements: 6.3, 6.6_
  - _Requirements: 6.1, 6.2, 6.5, 6.7, 6.8, 6.9, 6.14_

- [x] 12. Publish event giliran
  - Ada: `event_publisher.go` (`queue.turn.soon`, `queue.turn.arrived`)
  - [x] 12.R1 `PublishTurnSoon` belum dipanggil di mana pun. Panggil sekali per entry saat posisi ≤ `preTurnNotifyPosition` (mis. dari Status Publisher)
    - _Requirements: 6.11, 9.1_
  - _Requirements: 9.2, 9.3, 9.4, 9.5_

- [x] 13. Consumer event Booking
  - Ada: consumer `journey.ended`, `quota.exhausted`, `sold_out`
  - [ ] 13.R1 Setelah booking 12.R1 diperbaiki, uji ulang end-to-end. Entry `ADMITTED` + `UNCLAIMED` → `MISSED`
    - Consumer code + unit tests done (`consumer_test.go`); e2e retest pending booking 12.R1 (Booking belum menerbitkan `booking.quota.exhausted`).
    - _Depends on: booking 12.R1_
    - **Property 5: Idempotent consumers**
    - _Requirements: 8.1, 8.2, 8.7_
  - _Requirements: 8.3, 8.4, 8.5, 8.6_

- [x] 14. Leave & pindah entry
  - Ada: `DELETE /queue-entries/{entryId}`
  - [x] 14.R1 Entry `ADMITTED` yang ditinggalkan harus memanggil `POST /internal/v1/allocations/{id}:release` (kompensasi saga) dengan retry
    - `ReleaseAllocation` ditambahkan ke `IBookingClient` + `client.go`; `leave.go` memanggil release bila status `ADMITTED`; wiring diperbarui; test ditambahkan
    - _Depends on: booking 4.R1 (runtime; Queue-side code selesai)_
    - _Requirements: 7.2_
  - [ ] 14.2 Pindah entry via email — ditunda (OQ-3)
    - _Requirements: 7.4_
  - _Requirements: 7.1, 7.3, 7.5_

- [x] 15. Wiring rate limit
  - _Requirements: 3.1, 3.2, 3.3, 3.4_

- [ ] 16. Metrik & verifikasi akhir
  - [ ] 16.1 Metrik `rbt_queue_*` setelah library dipilih (OQ-5); log tanpa NIK/email/token
    - _Requirements: 10.3, 10.4_
    - _Depends on: platform 4.2_

- [ ] 17. Retensi data pribadi (keputusan 2026-10-21)
  - [ ] 17.1 Saat penjualan berakhir: `EXPIRE` semua key `q:{e:<eventId>}:*`; purge `entry_audit` setelah masa retensi. Sekarang key antrean tidak punya TTL sama sekali
    - _Depends on: platform 13.1_
    - _Requirements: 10.5_

## Notes
- Lua hanya untuk atomisitas antrean; aturan lain di Go.
- Routing key dan format error mengikuti platform task 12 dan 2.6.
