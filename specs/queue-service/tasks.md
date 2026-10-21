# Implementation Plan: Queue Service

Owner: **DEV-2**. Penomoran Task 0–16 dari DEV-2 dipertahankan; isinya diselaraskan dengan
`requirements.md`/`design.md` terbaru. Sub-task **R** = perbaikan kode yang sudah ada. Panduan teknis
rinci versi DEV-2 ada di riwayat git (commit `88c9dc8`).

Status 2026-10-21: join, status document, posisi, claim, leave, promotor, sweeper, consumer, dan 5
skrip Lua sudah ada dan test unit lulus. Perbaikan paling penting: 8.R1 (direct admission tidak
pernah berhasil), 11.R1 (entry kuota habis menyumbat kepala antrean), dan 13.R1 (bergantung pada
perbaikan payload di booking 12.R1).

## Tasks

- [x] 0. Config, kunci, scaffold modul
  - _Requirements: 10.2_

- [x] 1. Domain model — Entry, value object, state machine
  - [ ] 1.R1 Status `REJECTED` tidak ada di kontrak `QueueEntry`. Untuk kuota habis pakai `ENDED` + `reason = BUYER_QUOTA_EXHAUSTED`; `REJECTED` cukup sebagai aksi audit
    - _Requirements: 6.4, 5.5_

- [x] 2. Outbound ports + mocks
  - _Requirements: 10.1_

- [x] 3. Skrip Lua (join, promote, claim, leave, end_entry)
  - **Property 1: Gapless monotonic sequence**, **Property 4: One active entry per NIK / visitor / email**
  - _Requirements: 2.7, 2.8, 2.9, 2.10_

- [ ] 4. QueueStore + RateLimiter (Valkey)
  - Ada: `queue_store.go`, `rate_limiter.go`
  - [ ] 4.1 Integration test Lua dengan Valkey nyata (10.000 join paralel tanpa celah seq)
    - _Requirements: 2.6, 2.14_

- [x] 5. Repository Postgres — `event_settings` & `entry_audit`
  - _Requirements: 4.4, 4.6_

- [ ] 6. Security adapter — token, verifikasi user token, PII
  - Ada: visitor/queue/admission token, user token, PII
  - [ ] 6.R1 Validasi NIK sekarang hanya "16 digit". Ganti dengan `pkg/nik` (provinsi, kab/kec, tanggal lahir, nomor urut)
    - _Requirements: 2.4_
    - _Depends on: platform 1_
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

- [x] 7. Booking client (HTTP keluar) dengan circuit breaker
  - _Requirements: 6.1_

- [ ] 8. Join + direct admission
  - Ada: `join.go`, handler join
  - [ ] 8.R1 **Defect:** direct admission mengecek `Status == "RESERVED"`, padahal Booking membalas `outcome = "GRANTED"`. Akibatnya direct admission tidak pernah terjadi dan semua orang masuk `WAITING`. Hentikan juga pengiriman `units` (Booking yang menghitung)
    - **Property 3: No admission without reservation**
    - _Requirements: 2.5_
  - **Property 6: Fail closed**
  - _Requirements: 1.5, 1.6, 2.1, 2.2, 2.3, 2.6, 2.11, 2.12, 2.13, 2.14_

- [ ] 9. Status Document + Status Publisher
  - Ada: publisher + `GET /queue-status/{eventId}`
  - [ ] 9.R1 Publisher memanggil `GET /internal/v1/events/{id}/supply` yang belum ada di Booking; tunggu booking 4.R1, lalu uji zona dan `categories[].state`
    - _Depends on: booking 4.R1_
    - **Property 8: No PII in Status Document & events**
    - _Requirements: 4.5, 4.6, 4.7_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.8_

- [x] 10. Get My Entry — posisi eksak
  - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5_

- [ ] 11. Promotor + claim + claim sweeper
  - Ada: `promoter.go`, `sweeper.go`, claim handler
  - [ ] 11.R1 **Defect:** entry yang dibalas `BUYER_QUOTA_EXHAUSTED` atau `ALREADY_ACTIVE` tetap `WAITING` di depan, jadi promotor mengirim entry yang sama setiap tick dan menyumbat antrean. Akhiri entry (`ENDED` + `reason`) dan bersihkan constraint key (OQ-18 diputuskan: jangan di-admit)
    - _Requirements: 6.4, 6.13_
  - [ ] 11.R2 Sweeper tidak boleh menerbitkan `booking.journey.ended`, karena itu event milik Booking. Cukup tandai `MISSED` dan lepas slot; Booking melepas alokasinya sendiri (K7)
    - _Requirements: 6.10_
  - [ ] 11.R3 Satu promotor per event lewat lease Valkey (`SET NX PX`) agar dua instance worker tidak promote bersamaan
    - **Property 7: Single promoter**
    - _Requirements: 6.12_
  - [ ] 11.R4 `NO_SUPPLY_NOW`: berhenti di entry itu pada tick yang sama (jaga FIFO)
    - **Property 2: FIFO admission**
    - _Requirements: 6.3, 6.6_
  - _Requirements: 6.1, 6.2, 6.5, 6.7, 6.8, 6.9, 6.14_

- [ ] 12. Publish event giliran
  - Ada: `event_publisher.go` (`queue.turn.soon`, `queue.turn.arrived`)
  - [ ] 12.R1 `PublishTurnSoon` belum dipanggil di mana pun. Panggil sekali per entry saat posisi ≤ `preTurnNotifyPosition` (mis. dari Status Publisher)
    - _Requirements: 6.11, 9.1_
  - _Requirements: 9.2, 9.3, 9.4, 9.5_

- [ ] 13. Consumer event Booking
  - Ada: consumer `journey.ended`, `quota.exhausted`, `sold_out`
  - [ ] 13.R1 Setelah booking 12.R1 diperbaiki, uji ulang end-to-end. Entry `ADMITTED` + `UNCLAIMED` → `MISSED`
    - _Depends on: booking 12.R1_
    - **Property 5: Idempotent consumers**
    - _Requirements: 8.1, 8.2, 8.7_
  - _Requirements: 8.3, 8.4, 8.5, 8.6_

- [ ] 14. Leave & pindah entry
  - Ada: `DELETE /queue-entries/{entryId}`
  - [ ] 14.R1 Entry `ADMITTED` yang ditinggalkan harus memanggil `POST /internal/v1/allocations/{id}:release` (kompensasi saga) dengan retry
    - _Depends on: booking 4.R1_
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

## Notes
- Lua hanya untuk atomisitas antrean; aturan lain di Go.
- Routing key dan format error mengikuti platform task 12 dan 2.6.
