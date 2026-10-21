# Implementation Plan: Queue Service

## Overview

Membangun waiting room FIFO di atas boilerplate `services/queue`: join atomik via Lua, Status Document
CDN-first, posisi eksak, promotor tunggal per event yang hanya meng-admit setelah Booking `GRANTED`,
claim + admission token, leave + kompensasi saga, consumer event Booking, dan publish `queue.turn.*`.

Prasyarat lintas spec: platform task 1 (`pkg/nik`), 2 (middleware auth, rate limit, service token),
6.1 (kunci Queue); booking task 4 (`allocations:batch`, `:release`, `supply`) untuk task 6–7 di sini.

## Tasks

- [x] 0. Fondasi yang sudah ada (audit 2026-10-21)
  - Cobra verb, config D-24 fail-fast, `GET /health` (Valkey + PG + RabbitMQ), composition root, clients
  - Migrasi `0001_init` (`event_settings`, `entry_audit`) + `0002_account_ref`
  - _Requirements: 10.1, 10.2_

- [ ] 1. Fondasi domain & konfigurasi
  - [ ] 1.1 Entity `Entry` + state machine (design §5), value object `Seq`, `Status`, `Zone`, error domain (design §9) di `domain/` std-lib
    - _Requirements: 5.5, 6.14_
    - _Design: §5, §9_
  - [ ] 1.2 Perluas config: `BOOKING_SERVICE_URL`, kunci token (penerbit visitor/queue/admission, publik User, service token), `PII_HASH_KEY`, kunci enkripsi email, `ADMISSION_TOKEN_TTL_SECONDS` (< claim window), interval worker, rate limit
    - _Requirements: 10.2, 6.7_
    - _Design: §10_
    - _Depends on: platform 6.1_
  - [ ] 1.3 Port inbound/outbound + `make mock`; repository `event_settings` (cache) dan `entry_audit` (append)
    - _Requirements: 4.4, 4.6_

- [ ] 2. Lua scripts & adapter Valkey
  - [ ] 2.1 `join.lua` (constraint quota/NIK/visitor/email + `INCR seq` atomik, hash tag `{e:<eventId>}`), `promote.lua`, `claim.lua`, `leave.lua`, `end_entry.lua` sesuai design §4
    - _Requirements: 2.7, 2.8, 2.9, 2.10, 6.7, 7.1, 8.1, 8.2_
    - _Design: §3.1, §4_
  - [ ] 2.2 Integration test (Valkey nyata): 10.000 join paralel tanpa celah seq; constraint NIK/visitor/email; idempotensi join ulang
    - **Property 1: Gapless monotonic sequence**, **Property 4: One active entry per NIK / visitor / email**
    - _Requirements: 2.6, 2.7, 2.14_

- [ ] 3. Join & visitor identity
  - [ ] 3.1 Usecase + handler `POST /api/v1/events/{eventId}/queue-entries`: validasi email/konfirmasi/consent, `pkg/nik` (`NIK_INVALID`), `NIK_REQUIRED`, resolusi NIK per status login (`has_nik`/`nik_hash` dari user token), sale window, sold out
    - _Requirements: 1.3, 1.4, 2.1, 2.2, 2.3, 2.4, 2.11, 2.12_
    - _Depends on: platform 1, platform 2.2_
  - [ ] 3.2 Terbitkan visitor token + queue token; cookie `rb_visitor` (HttpOnly, Secure, SameSite=Lax) untuk web, `visitorToken` di body untuk mobile; `accountId` dibawa ke audit/Booking
    - _Requirements: 1.1, 1.2, 1.5, 1.6_
  - [ ] 3.3 Direct admission: entry terdepan + slot bebas → `allocations:batch` satu entry → `GRANTED` → `promote.lua` → `ADMITTED`; selain itu `WAITING`
    - **Property 3: No admission without reservation**
    - _Requirements: 2.5, 2.6_
    - _Design: §7.1 (direct admission)_
    - _Depends on: 6.1, booking 4.1_
  - [ ] 3.4 Fail closed: Valkey tak terjangkau → `503 DEPENDENCY_UNAVAILABLE`; idempotensi `Idempotency-Key`
    - **Property 6: Fail closed**
    - _Requirements: 2.13, 2.14_
  - [ ] 3.5 Test TC-GST-01..05, TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-USR-09/09a
    - _Requirements: 1.3, 1.4, 2.1, 2.5_

- [ ] 4. Rate limiting
  - [ ] 4.1 Pasang `pkg/httpx.RateLimit` (Valkey) untuk join (per email + per IP) dan `GET queue-entries/me`; `Retry-After`; tidak untuk `queue-status`
    - _Requirements: 3.1, 3.2, 3.3, 3.4_
    - _Depends on: platform 2.4_

- [ ] 5. Status Document & posisi
  - [ ] 5.1 Worker Status Publisher (design §7.2): `servingSeq`, zona (`guaranteedUntilSeq = ceil(free/maxPerOrder)` entri terdepan, `possibleUntilSeq`), `removedBuckets`, `poll`, `nextPollAfterMs` (memendek saat stok rendah), `categories[].state`, `preTurnNotifyPosition`; `SET q:{e}:status EX 1`; supply dari Booking di-cache 1 s
    - _Requirements: 4.3, 4.4, 4.5, 4.6, 4.7_
    - _Design: §7.2_
    - _Depends on: booking 4.3_
  - [ ] 5.2 `GET /api/v1/queue-status/{eventId}` + `Cache-Control: public, max-age=1, stale-while-revalidate=2`; 404 event tak ada
    - _Requirements: 4.1, 4.2, 4.8_
  - [ ] 5.3 `GET /api/v1/queue-entries/me?eventId=`: `exactPosition = seq − servingSeq − removedAhead`, zona, `claimDeadline`/`redirect` saat `ADMITTED`, `reason` untuk status terminal
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5_
  - [ ] 5.4 Test TC-QUE-03, TC-QUE-04, TC-QUE-10..12, TC-QUE-15; tidak ada PII di Status Document
    - **Property 8: No PII in Status Document & events**
    - _Requirements: 4.5, 4.6, 4.7_

- [ ] 6. Promotor & admission
  - [ ] 6.1 Klien Booking internal (`allocations:batch`, `:release`, `supply`) dengan service token + breaker Queue→Booking + bulkhead
    - _Requirements: 6.1, 7.2_
    - _Design: §2.7_
    - _Depends on: platform 2.3, booking 4.1, booking 4.3_
  - [ ] 6.2 Lease promotor per event (`SET NX PX`, perpanjang per tick)
    - **Property 7: Single promoter**
    - _Requirements: 6.12_
    - _Design: §7.1_
  - [ ] 6.3 Promotor tick: ambil N terdepan, `allocations:batch`, pemetaan outcome (`GRANTED` → promote; `NO_SUPPLY_NOW` → berhenti; `BUYER_QUOTA_EXHAUSTED` → `ENDED`+`SADD`; `SOLD_OUT` → tutup antrean), audit `ADMITTED`/`REJECTED`
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6_
  - [ ] 6.4 `ALREADY_ACTIVE` — **blocked: OQ-18** (sementara tetap `WAITING` + alert)
    - _Requirements: 6.13_
  - [ ] 6.5 Integration test FIFO lintas promosi, promote hanya setelah `GRANTED`, dua instance worker (TC-QUE-05, TC-QUE-08, TC-QUE-14, TC-PERF-04)
    - **Property 2: FIFO admission**, **Property 3: No admission without reservation**, **Property 7: Single promoter**
    - _Requirements: 6.2, 6.6, 6.12_

- [ ] 7. Claim, claim sweeper, leave
  - [ ] 7.1 `POST /api/v1/queue-entries/{entryId}/claim`: `claim.lua`, admission token EdDSA sekali pakai (`jti, eventId, entryId, allocationId, visitorId`), `bookingUrl`; `410 TURN_MISSED` / `410 EVENT_SOLD_OUT`
    - _Requirements: 6.7, 6.8, 6.9_
  - [ ] 7.2 Claim Sweeper: `ADMITTED` lewat deadline → `MISSED` via `end_entry.lua`, rilis slot, audit; **tanpa** publish `booking.*`
    - _Requirements: 6.10, 6.14_
    - _Design: §7.3_
  - [ ] 7.3 `DELETE /api/v1/queue-entries/{entryId}` (`WAITING`/`ADMITTED`) → `LEFT`, cleanup constraint, rilis slot, 204; rejoin dapat seq baru; 404
    - _Requirements: 7.1, 7.3, 7.5_
  - [ ] 7.4 Release retrier (`q:{e}:release_pending` → `allocations/{id}:release`)
    - _Requirements: 7.2_
    - _Design: §7.4_
  - [ ] 7.5 Pindah entry ke perangkat lain — **blocked: OQ-3** (tidak ada endpoint di `queue.yaml`)
    - _Requirements: 7.4_
  - [ ] 7.6 Test TC-QUE-09 (missed turn), TC-QUE-17 (leave & rejoin), TC-SEC-01 (replay admission token ditolak Booking)
    - _Requirements: 6.8, 6.10, 7.1, 7.3_

- [ ] 8. Checkpoint — alur join sampai claim
  - Test task 2–7 lulus; `make ci` hijau; tanyakan ke user bila OQ-3/OQ-18 masih terbuka

- [ ] 9. Consumer event Booking
  - [ ] 9.1 Subscription `queue.journey-ended` (`booking.journey.ended.v1`): `CLAIMED` → `COMPLETED`/`ENDED`; `ADMITTED`+`UNCLAIMED` → `MISSED`; terminal → no-op
    - _Requirements: 8.1, 8.2_
    - _Design: §6.2_
  - [ ] 9.2 Subscription `queue.quota-exhausted` (`SADD`) dan `queue.sold-out` (tutup antrean, `soldOut=true`)
    - _Requirements: 8.3, 8.4_
  - [ ] 9.3 Payload rusak → `rabbitmq.Permanent`; retry berjenjang + DLQ dari `pkg/rabbitmq`
    - _Requirements: 8.5, 8.6_
  - [ ] 9.4 Integration test kirim ganda ketiga event (TC-QUE-05), sold out ≤ 5 s ke Status Document (TC-PERF-05)
    - **Property 5: Idempotent consumers**
    - _Requirements: 8.7_

- [ ] 10. Publish event giliran
  - [ ] 10.1 Publisher `queue.turn.soon.v1` (sekali per entry saat posisi ≤ `preTurnNotifyPosition` dan `pre_turn_notify_enabled`) dan `queue.turn.arrived.v1` (saat promote), envelope `producer = queue-service`, publisher confirm, gagal → log + lanjut
    - _Requirements: 6.11, 9.1, 9.2, 9.4, 9.5_
    - _Depends on: platform 5.4_
  - [ ] 10.2 Test validasi payload terhadap schema (tanpa PII)
    - **Property 8: No PII in Status Document & events**
    - _Requirements: 9.3_

- [ ] 11. Observability
  - [ ] 11.1 Metrik design §11 (`rbt_queue_*`, label `event_id`/`status` saja) + alert `QueuePromotorStalled`, `QueueUnclaimedRateHigh` (> 5% → naikkan claim window untuk penjualan berikutnya, SRS-F-25-13), `QueueValkeyLatencyHigh`
    - _Requirements: 10.3_
    - _Depends on: platform 4.2_
  - [ ] 11.2 Test log scan tanpa NIK/email/token (TC-SEC-03)
    - _Requirements: 10.4_

- [ ] 12. Final checkpoint
  - Property 1–8 punya test lulus; contract test `queue.yaml` (TC-API-01/02); load test TC-PERF-01..03 tercatat di `docs/evidence/`
  - Tanyakan ke user untuk OQ-3, OQ-13, OQ-18

## Notes

- Lua hanya untuk atomisitas antrean; aturan bisnis lain di Go (`code-principles.md`).
- Queue tidak memakai outbox; event `queue.turn.*` best effort (ADR-010, SDD §4.6.2).
- Queue **tidak** pernah menerbitkan event `booking.*` (K7).
