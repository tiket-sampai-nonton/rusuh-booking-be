# Implementation Plan: Booking Service

## Overview

Membangun domain bisnis Booking Service di atas boilerplate yang sudah ada (`services/booking`):
katalog, admission, sesi, hold, edit, payment-lock, settlement, expiry/resolve, tiket, SSE, admin, dan
rekonsiliasi — hexagonal + event sourcing (ADR-019) dengan urutan lock tetap (design §9.0).

Urutan mengikuti dependensi data: fondasi domain & adapter → katalog/admin → admission → sesi → hold →
edit → lock → settlement → sweeper → fitur pendukung. Setiap task selesai bila `make ci` hijau dan test
yang disebut lulus. Test invarian (Property 1–12) **wajib**, tidak opsional.

Prasyarat lintas spec: platform task 1 (`pkg/nik`), 2 (middleware auth), 3 (`pkg/outbox`), 6.1 (kunci).

## Tasks

- [x] 0. Fondasi yang sudah ada (audit 2026-10-21)
  - Migrasi `0001_init`, `0002_event_store` (14 tabel, trigger invarian, CHECK, indeks parsial)
  - Cobra `api`/`worker`/`migrate`/`healthcheck`; config fail-fast subset; `GET /health`; composition root
  - _Requirements: 4.3, 4.4, 4.5, 4.6, 11.4, 11.5, 11.6, 13.1, 13.2_

- [ ] 1. Fondasi domain, port, dan konfigurasi
  - [ ] 1.1 Value object & entity murni di `domain/` (std-lib): `Money`, `BookingStatus`, `AdmissionStatus`, `LockReason`, `EndReason`, `Version`; entity `Event`, `Category`, `Inventory`, `Supply`, `Admission`, `Booking`, `BookingItem`, `Ticket` dengan aturan transisi (`decide()` murni) sesuai design §6
    - _Requirements: 9.12, 7.3_
    - _Design: §2.1, §6_
  - [ ] 1.2 Error domain bertipe di `port/inbound/errors.go` + pemetaan ke `errorCode`/HTTP di `infrastructure/http` via `pkg/apperror` (tabel design §11)
    - _Requirements: 6.5, 8.5, 8.6, 8.7_
    - _Design: §11_
  - [ ] 1.3 Definisikan port inbound/outbound (design §2.1) dan regenerasi `_mock/` (`make mock`)
    - _Requirements: 11.1_
  - [ ] 1.4 Perluas `internal/config`: `PAYMENT_SERVICE_URL`, kunci token (publik Queue & User, privat ticket link & service token), `PII_HASH_KEY`, kunci enkripsi NIK pemegang, `VERIFY_GRACE_SECONDS`, `OUTAGE_EXTENSION_SECONDS`, `CANCEL_ENABLED`, `SSE_*`, `PII_RETENTION_DAYS_AFTER_EVENT` dengan validasi rentang SDD §4.10; update `.env.example`
    - _Requirements: 13.2, 9.10, 14.5, 15.5, 16.1, 16.3_
    - _Design: §12_
    - _Depends on: platform 6.1_

- [ ] 2. Adapter PostgreSQL, unit of work, event store, idempotensi
  - [ ] 2.1 Migrasi baru `0003_event_catalog_fields.up/down.sql`: `events.city text NOT NULL DEFAULT ''` + index `lower(city)`; `image_url`, `banner_url`, `thumbnail_url text NULL` (tidak mengubah `0001`/`0002`)
    - _Requirements: 1.3, 18.1, 18.3_
    - _Design: §4.1_
  - [ ] 2.2 `IUnitOfWork.Do` di `infrastructure/postgres`: tx pgx, `SET LOCAL lock_timeout/statement_timeout/app.actor`, helper lock berurutan (admission → booking → supply → quota → categories ASC), pemetaan SQLSTATE/constraint → error domain
    - _Requirements: 6.1, 6.10, 11.1_
    - _Design: §9.0, §11_
  - [ ] 2.3 Event store adapter memakai `pkg/eventstore` (`booking_events`), proyeksi read model, retry `ErrConcurrency` maks 3 (re-read + decide ulang)
    - _Requirements: 11.1, 11.2, 11.3_
    - _Design: §5_
  - [ ] 2.4 `IIdempotencyStore` (`idempotency_records`: klaim `ON CONFLICT`, `request_hash`, simpan respons, `expires_at`)
    - _Requirements: 6.6_
  - [ ] 2.5 `IPIIProtector`: HMAC NIK (`PII_HASH_KEY` bersama), mask, AES-256-GCM `attendee_nik_enc` via `pkg/crypto`
    - _Requirements: 5.12_
  - [ ] 2.6 Integration test: append-only ditolak DB, konflik versi → tepat satu menang, replay read model identik
    - **Property 10: Append-only & concurrency**
    - _Requirements: 11.2, 11.4_

- [ ] 3. Katalog & detail event (publik)
  - [ ] 3.1 Usecase + handler `GET /api/v1/events` (filter `timeframe`/`city`/`status`/`sort`, irisan AND, tanpa `DRAFT`, `minPriceMinor`, `serverTime`), header cache 2 s
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8_
    - _Design: §3.1_
    - _Depends on: 2.1_
  - [ ] 3.2 `GET /api/v1/events/{eventId}`: `available = capacity − held − sold`, kategori habis tetap muncul, 400/404
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6_
  - [ ] 3.3 Test: filter per TC-CAT-01..06, ketersediaan tidak dikurangi `allocated` (TC-EVT-05)
    - _Requirements: 1.2, 1.5, 2.2_
  - Catatan: perilaku filter tak dikenal — **OQ-14**; implementasi awal mengikuti default (abaikan) sampai kontrak diputuskan

- [ ] 4. Admission internal (dipanggil Queue)
  - [ ] 4.1 `POST /internal/v1/allocations:batch`: tx pendek per entry urut `seq`, aturan outcome & `units`, snapshot `cfg_*`, idempoten per `queueEntryId`, `ALREADY_ACTIVE` dari indeks unik
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 4.1_
    - _Design: §3.4, §9.1_
    - _Depends on: 2.2, platform 2.3_
  - [ ] 4.2 Deteksi sold out (`free + allocated + held = 0`) → `events.status = SOLD_OUT` + outbox `booking.sold_out.v1` sekali
    - _Requirements: 3.5, 12.3_
    - _Depends on: platform 3.1_
  - [ ] 4.3 `GET /internal/v1/events/{eventId}/supply` dan `POST /internal/v1/allocations/{allocationId}:release` (RESERVED → RELEASED, idempoten)
    - _Requirements: 17.1, 17.2, 17.3_
  - [ ] 4.4 Integration test SDD T1 (stok 7, maks 3 → 3,3,1,`NO_SUPPLY_NOW`), T5, T7, idempotensi ulang batch (TC-ADM-01, TC-ADM-04)
    - **Property 1: No oversell**
    - _Requirements: 3.1, 3.2, 3.3_

- [ ] 5. Sesi pemilihan
  - [ ] 5.1 `POST /api/v1/booking-sessions`: verifikasi admission token (kunci Queue, visitor cocok, `jti` sekali pakai), `RESERVED → ACTIVE`, `expires_at = now + cfg_selection_ttl`; panggilan ulang mengembalikan sesi sama tanpa memperpanjang TTL; 403/410
    - _Requirements: 3.8, 3.9, 3.10, 7.1_
    - _Design: §3.2_
    - _Depends on: platform 2.1_
  - [ ] 5.2 `GET /api/v1/booking-sessions/{sessionId}` dan `GET /api/v1/booking-sessions/current?eventId=` dengan pemetaan `phase`/`reason` (design §3.2), rate limit `current` (429 + `Retry-After`)
    - _Requirements: 7.2, 8.9, 8.10_
    - _Depends on: platform 2.4_
  - [ ] 5.3 Test snapshot config: ubah TTL event saat sesi berjalan → sesi lama memakai snapshot (TC-CFG-01, SDD T18)
    - _Requirements: 4.1, 4.2_
  - [ ] 5.4 Test TC-SES-01/03 (refresh tidak memperpanjang), TC-SES-05 (pemulihan), TC-SEC-01 (replay admission token)
    - **Property 9: Server-authoritative timers**
    - _Requirements: 7.1, 3.10_

- [ ] 6. Cek NIK pemegang
  - [ ] 6.1 `POST /api/v1/booking-sessions/{sessionId}/attendee-checks`: `pkg/nik`, dedupe `attendee_checks`, batas `max_attendee_checks_per_session`, NIK pesanan sendiri = `AVAILABLE`, tidak mengunci NIK, `422 NIK_INVALID` tanpa mengurangi jatah
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_
    - _Design: §3.2_
    - _Depends on: platform 1_
  - [ ] 6.2 Test SDD T3, T8, T12 (TC-BKG-08, TC-BKG-09)
    - _Requirements: 5.3, 5.4, 5.5_
  - Catatan: kode HTTP batas cek — **OQ-1 (blocked untuk respons batas)**

- [ ] 7. Hold atomik
  - [ ] 7.1 `IHoldUsecase` + `POST /api/v1/bookings`: tx design §9.2 (idempotency, lock order, `decide()`, `allocated→held`, kuota, item `HELD`, `BookingHeld`, admission `EDITABLE`, `payment_expires_at`)
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.9, 5.7, 5.8, 5.9, 5.10, 7.5_
    - _Design: §9.2_
    - _Depends on: 2.2, 2.3, 2.4, 2.5, 5.1_
  - [ ] 7.2 Idempotensi (`create_booking`), `IDEMPOTENCY_KEY_REUSED`, satu booking per sesi
    - _Requirements: 6.6, 6.7_
  - [ ] 7.3 Kunci langsung saat `cfg_edit_enabled = false` atau hold di masa grace; lepas sisa unit; `BookingLocked`
    - _Requirements: 6.8, 7.4_
  - [ ] 7.4 `lock_timeout`/`statement_timeout` → `503 DEPENDENCY_UNAVAILABLE` tanpa hold setengah jadi
    - _Requirements: 6.10_
  - [ ] 7.5 Integration test TC-BKG-07: 100 hold paralel pada 50 tiket terakhir → tepat 50; SDD T2, T4, T6, T16, T17, T20; TC-BKG-01..06, TC-BKG-10..12
    - **Property 1: No oversell**, **Property 2: Jangan PHP**, **Property 3: Buyer quota & attendee uniqueness**
    - _Requirements: 6.5, 3.6, 3.7, 5.7, 5.8, 5.9_

- [ ] 8. Ubah pesanan
  - [ ] 8.1 `IEditUsecase` + `PUT /api/v1/bookings/{bookingId}/items`: tx design §9.3 dengan `SAVEPOINT`, `keepFromItemId`, hitung ulang nominal, `version++`, `edit_count++`, `BookingItemsReplaced`
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.8_
    - _Design: §9.3_
    - _Depends on: 7.1_
  - [ ] 8.2 Penolakan `BOOKING_VERSION_CONFLICT`, `BOOKING_LOCKED`, `EDIT_LIMIT_REACHED`; percobaan gagal tidak dihitung
    - _Requirements: 8.5, 8.6, 8.7_
  - [ ] 8.3 Integration test SDD T10, T11, T13; TC-EDT-01..03, TC-EDT-06
    - **Property 4: Edit atomicity**
    - _Requirements: 8.1, 8.4, 8.8_

- [ ] 9. Data tagihan, payment-lock, perpanjangan deadline (dipanggil Payment)
  - [ ] 9.1 `GET /internal/v1/bookings/{bookingId}/payable`
    - _Requirements: 14.1, 14.6_
    - _Design: §3.4_
  - [ ] 9.2 `POST /internal/v1/bookings/{bookingId}/payment-lock`: tx design §9.4, idempoten per `attemptKey`, outcome `OK/BOOKING_NOT_PAYABLE/PAYMENT_EXPIRED`, status booking tetap `PENDING_PAYMENT`
    - _Requirements: 14.2, 14.3, 14.4_
    - _Design: §9.4_
    - _Depends on: 8.1_
  - [ ] 9.3 `POST /internal/v1/bookings/{bookingId}/deadline-extension`: sekali, maks `OUTAGE_EXTENSION_SECONDS`
    - _Requirements: 14.5_
  - [ ] 9.4 Integration test SDD T14, T21 + balapan edit vs lock (TC-EDT-04)
    - **Property 5: Lock serialization**
    - _Requirements: 14.2, 14.3, 8.6_

- [ ] 10. Checkpoint — alur hold sampai lock
  - Pastikan test task 2–9 lulus, coverage logika ≥ 80%, `make ci` hijau; tanyakan ke user bila OQ-1/OQ-2/OQ-14 masih terbuka

- [ ] 11. Settlement (consumer `payment.succeeded.v1` / `payment.failed.v1`)
  - [ ] 11.1 `ISettlementUsecase`: tx design §9.5 (guard status, cabang tepat waktu → `PAID` + tiket + outbox; terlambat / sudah final → `REVIEW`)
    - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.12, 10.1_
    - _Design: §9.5_
    - _Depends on: 9.2, 14.2_
  - [ ] 11.2 `rabbitmq.Subscription` queue `booking.payment-succeeded` (`payment.succeeded.v1`) dan `booking.payment-failed` (no-op) di `RunWorker`; payload rusak → `rabbitmq.Permanent`
    - _Requirements: 9.5, 9.6_
    - _Design: §8.1_
  - [ ] 11.3 Integration test kirim ganda, webhook telat tapi tepat waktu, settlement terlambat, setelah `EXPIRED` (TC-PAY-03, TC-PAY-06, TC-PAY-07)
    - **Property 6: Idempotent settlement**, **Property 7: Timeliness by settlement time**
    - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5_
  - Catatan: perilaku mismatch nominal — **OQ-17**

- [ ] 12. Session sweeper
  - [ ] 12.1 Job sweeper (design §7.1): `RESERVED` → `EXPIRED(UNCLAIMED)`, `ACTIVE` → `EXPIRED(SESSION_EXPIRED)`, `EDITABLE` → `CONVERTED(SELECTION_EXPIRED)`; `SKIP LOCKED LIMIT 200`; outbox `journey.ended`
    - _Requirements: 7.3, 7.4, 7.6, 12.4_
    - _Design: §7.1_
  - [ ] 12.2 Integration test TC-SES-04, TC-SES-08, TC-EDT-05, SDD T15/T22; satu `journey.ended` per admission
    - **Property 11: Journey end exactly once**
    - _Requirements: 7.3, 12.4_
  - Catatan: `outcome` untuk sesi habis — **OQ-15** (sementara `EXPIRED`)

- [ ] 13. Administrasi event (internal/seed)
  - [ ] 13.1 `POST /internal/v1/admin/events` (event + kategori + inventori + supply satu tx, `app.actor`), default `EventConfig`
    - _Requirements: 18.1, 18.4, 18.5_
    - _Design: §3.6_
    - _Depends on: 2.1, platform 2_
  - [ ] 13.2 `PATCH /internal/v1/admin/events/{eventId}` → `EVENT_RULES_LOCKED` 409; audit via trigger (TC-CFG-02..04)
    - _Requirements: 18.2, 4.3, 4.4, 4.5, 4.6_
  - [ ] 13.3 `POST /internal/v1/admin/events/{eventId}/images` via `IImageStore` — **blocked: OQ-16** (penyimpanan lokal)
    - _Requirements: 18.3_
  - [ ] 13.4 Baca konfigurasi efektif per event — **blocked: OQ-12** (endpoint belum ada di kontrak)
    - _Requirements: 4.7_

- [ ] 14. Expiry sweeper + resolve ke Payment
  - [ ] 14.1 `IPaymentClient` (`infrastructure/paymentclient`): `POST /internal/v1/payments/{id}/resolve`, service token, breaker + bulkhead (PROPOSED SDD §4.6.2), error/OPEN → `UNKNOWN`
    - _Requirements: 9.10_
    - _Design: §3.5_
    - _Depends on: platform 2.3_
  - [ ] 14.2 Job expiry fase A (`PENDING_PAYMENT` lewat deadline → `VERIFYING`, `PaymentVerifying`) dan fase B (resolve di luar tx → PAID / EXPIRED / tetap VERIFYING / EXPIRED setelah `verification_until`)
    - _Requirements: 9.7, 9.8, 9.9, 9.10, 9.11, 5.11_
    - _Design: §7.2_
  - [ ] 14.3 Integration test TC-PAY-04, TC-PAY-08 (Payment down → VERIFYING menahan stok, hasil benar setelah pulih), TC-PAY-02
    - **Property 8: No release before payment certainty**
    - _Requirements: 9.7, 9.9, 9.10_

- [ ] 15. Tiket, ticket link, dan riwayat
  - [ ] 15.1 `ITicketLinkSigner` (EdDSA, `bookingId`, exp = tanggal event + 7 hari) dipakai di outbox `booking.paid.v1`
    - _Requirements: 10.5_
  - [ ] 15.2 `GET /api/v1/bookings/{bookingId}` dan `/tickets` untuk pemilik visitor, ticket link `t`, atau user pemilik; `409 BOOKING_NOT_PAYABLE`; `404` untuk bukan pemilik
    - _Requirements: 10.2, 10.3, 10.4_
  - [ ] 15.3 `GET /api/v1/me/bookings`: `account_id = token.sub`, cursor, `limit` 1..50; `401` token tidak valid
    - _Requirements: 10.6, 10.7_
    - _Depends on: platform 2.2_
  - [ ] 15.4 Test TC-TKT-01/03, TC-USR-10 (tanpa kebocoran lintas akun)
    - _Requirements: 10.1, 10.6_

- [ ] 16. Rekonsiliasi
  - [ ] 16.1 Query read-only design §7.4 (`supply_equation`, `category_equation`, `quota_vs_items`, `tickets_vs_paid`, `outbox_pending_age`, `readmodel_vs_events`) diekspos untuk `tools/reconcile`
    - _Requirements: 11.7_
    - _Design: §7.4_
    - _Depends on: platform 9.1_
  - [ ] 16.2 Test: injeksi inkonsistensi buatan → pemeriksaan melapor (TC-OPS-01)
    - _Requirements: 11.7_

- [ ] 17. SSE ketersediaan
  - [ ] 17.1 Job cache `booking:avail:{eventId}` tiap `SSE_AVAILABILITY_INTERVAL_MS` (bila `SSE_ENABLED`)
    - _Requirements: 16.2_
    - _Design: §7.5_
  - [ ] 17.2 Handler `GET /api/v1/booking-sessions/{sessionId}/stream`: `availability` saat berubah, event sisa waktu, heartbeat, batas koneksi per instance → 503, satu koneksi per sesi, tanpa cache
    - _Requirements: 16.1, 16.3, 16.4, 16.6_
  - [ ] 17.3 Event penutup saat sesi berakhir — **blocked sebagian: OQ-2** (nama event `timer`/`session`, event `closed`)
    - _Requirements: 16.5_
  - [ ] 17.4 Test TC-SSE-01..03
    - _Requirements: 16.1, 16.3_

- [ ] 18. Keluar tanpa membeli & batal pesanan (SHOULD)
  - [ ] 18.1 `POST /api/v1/booking-sessions/{sessionId}/cancel` → `EXPIRED(LEFT_WITHOUT_BUYING)`, alokasi kembali, outbox `journey.ended`, 204
    - _Requirements: 15.1_
  - [ ] 18.2 `POST /api/v1/bookings/{bookingId}/cancel`: resolve dulu (design §9.6), `UNKNOWN` → 503, `CANCELLED` + `BookingCancelled` + outbox; flag `CANCEL_ENABLED`
    - _Requirements: 15.2, 15.3, 15.4, 15.5, 5.11_
    - _Depends on: 14.1, 11.1_
  - [ ] 18.3 Integration test TC-PAY-09 (sudah bayar → PAID; belum → CANCELLED + slot dibebaskan)
    - _Requirements: 15.2_

- [ ] 19. Publikasi event & relay
  - [ ] 19.1 Pasang `pkg/outbox.Relay` sebagai job worker; producer `booking-service`
    - _Requirements: 12.1, 12.2, 12.6_
    - _Depends on: platform 3_
  - [ ] 19.2 Test kontrak payload outbox terhadap `docs/contracts/events/*.schema.json` + envelope (semua 6 routing key)
    - _Requirements: 12.3, 12.7_
  - [ ] 19.3 Penerbitan `booking.quota.exhausted.v1` — **blocked: OQ-13** (titik pemicu)
    - _Requirements: 12.5_
  - [ ] 19.4 Failure test TC-CAP-03: broker mati → booking tetap commit; setelah pulih lag kembali 0
    - _Requirements: 12.6_

- [ ] 20. Observability
  - [ ] 20.1 Metrik design §13 via `pkg/telemetry` (tanpa label ID berkardinalitas tinggi) + aturan alert
    - _Requirements: 13.3, 13.4_
    - _Depends on: platform 4.2_
  - [ ] 20.2 Test log scan: tidak ada NIK/email/token di log (TC-SEC-03)
    - **Property 12: Monotonic status** (assert di test proyeksi), PII via platform Property 6
    - _Requirements: 13.5, 9.12_

- [ ] 21. Final checkpoint
  - Semua Property 1–12 punya test yang lulus; contract test TC-API-01/02 untuk `booking.yaml` + `internal-booking.yaml`
  - `make ci` hijau; evidence TC-BKG-07 dan TC-PERF-04 disimpan di `docs/evidence/`
  - Tanyakan ke user untuk OQ yang masih memblokir task 6, 13.3, 13.4, 17.3, 19.3

## Notes

- Task bertanda **blocked** menunggu keputusan di `specs/README.md` (OQ-x); jangan diimplementasikan dengan asumsi.
- Tidak ada task yang mengubah migrasi `0001`/`0002`; perubahan skema hanya lewat `0003+`.
- Satu usecase per file; handler selalu DTO lewat `domain/mapper`; interface hanya di `port/`.
- Retensi PII (`PII_RETENTION_DAYS_AFTER_EVENT`) belum punya task — menunggu OQ-11.
