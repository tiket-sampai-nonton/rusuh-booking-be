# Implementation Plan: Booking Service

Owner: **DEV-2**. File ini menggabungkan task DEV-2 (penomoran Task 1–14 dipertahankan, sama dengan
yang dipakai skill `implement-service`) dengan keputusan di `requirements.md`/`design.md` terbaru.

Status per 2026-10-21 setelah inspeksi kode: sebagian besar alur publik sudah ada, tetapi beberapa
bagian masih mengikuti desain lama dan ada defect yang memutus integrasi dengan Queue dan Payment.
Sub-task berawalan **R** adalah perbaikan (rework) terhadap kode yang sudah ada. Panduan teknis rinci
versi DEV-2 (SQL, daftar file) tetap ada di riwayat git (commit `88c9dc8`).

Prioritas rework: 6.R1, 10.R1, 12.R1 (memutus alur end-to-end) → 4.R1 dan Task 13 (memblokir Queue
dan Payment) → sisanya.

## Tasks

- [x] 0. Fondasi (sudah ada sebelum DEV-2): migrasi `0001`/`0002`, CLI, config, `/health`
  - _Requirements: 4.3, 4.4, 4.5, 4.6, 11.4, 11.5, 11.6, 13.1, 13.2_

- [x] 1. Domain core — value object, entity, error catalog
  - Ada: `domain/valueobject/*`, `domain/entity/*`, `domain/event/events.go`, error di `port/inbound`
  - [ ] 1.R1 Validasi NIK pakai `pkg/nik` (aturan SDD §4.9). VO sekarang mengikuti aturan lama "provinsi 01..99"
    - _Requirements: 5.1, 5.2, 5.12_
    - _Depends on: platform 1_
  - _Requirements: 9.12, 11.3_

- [ ] 2. Katalog event `GET /api/v1/events`
  - Ada: `list_events.go`, `catalog_handler.go`, `event_repo.go`
  - [ ] 2.R1 Filter `city` sekarang `venue ILIKE`. Tambah migrasi `0003` (`events.city` + kolom gambar), lalu filter ke `city` secara case-insensitive
    - _Requirements: 1.3_
  - _Requirements: 1.1, 1.2, 1.4, 1.5, 1.6, 1.7, 1.8_

- [x] 3. Detail event `GET /api/v1/events/{eventId}`
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6_

- [ ] 4. Admission & alokasi (Jangan PHP)
  - Ada: `allocate_batch.go`, `admission_repo.go` (`ALREADY_ACTIVE`, idempoten per `queueEntryId`), `POST /internal/v1/allocations:batch`, create/get/current session
  - [ ] 4.R1 Endpoint yang dipanggil Queue belum ada: `GET /internal/v1/events/{eventId}/supply` (Status Publisher Queue gagal tanpa ini) dan `POST /internal/v1/allocations/{allocationId}:release`
    - _Requirements: 17.1, 17.2, 17.3_
  - [ ] 4.R2 Saat `free + allocated + held = 0`: tandai event `SOLD_OUT` dan tulis outbox `booking.sold_out`
    - _Requirements: 3.5_
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.6, 3.7, 3.8, 3.9, 3.10, 4.1, 4.2, 7.1, 7.2, 8.9_

- [x] 5. Cek NIK pemegang `POST .../attendee-checks`
  - Mengikuti kontrak: `200 {result: CHECK_LIMIT_REACHED}` (OQ-1 sudah diputuskan)
  - _Requirements: 5.3, 5.4, 5.5, 5.6_

- [ ] 6. Hold atomik
  - Ada: `hold.go`, `unit_of_work.Hold`, idempotency store, test hold
  - [ ] 6.R1 **Defect:** hold menambah `held_units` tanpa mengurangi `allocated_units`, jadi unit terhitung dua kali dan CHECK supply menolak hold yang sah saat stok sudah teralokasi penuh. Ubah jadi `allocated -= n, held += n`
    - **Property 1: No oversell**, **Property 2: Jangan PHP**
    - _Requirements: 6.1, 3.6_
  - [ ] 6.R2 Urutan lock jadi `admissions → event_supply → buyer_quotas → category_inventories (ASC)`. Sekarang admission tidak dikunci dan urutannya supply → category → quota
    - _Requirements: 6.1_
    - _Design: §9.0_
  - [ ] 6.R3 Setelah hold, admission → `EDITABLE`; bila edit dimatikan atau hold masuk di masa grace, langsung kunci dan lepas sisa unit
    - _Requirements: 6.8, 7.4_
  - [ ] 6.R4 Test paralel TC-BKG-07 (100 hold pada 50 tiket → tepat 50) dengan PostgreSQL nyata
    - **Property 3: Buyer quota & attendee uniqueness**
    - _Requirements: 6.5, 5.7, 5.8, 5.9, 5.10_
  - _Requirements: 6.2, 6.3, 6.4, 6.6, 6.7, 6.9, 6.10, 7.5_

- [ ] 7. Sesi, timer & SSE
  - Ada: create/current/get session dengan `expiresAt`/`serverTime`
  - [ ] 7.1 SSE `GET /booking-sessions/{id}/stream` + cache ketersediaan Valkey (belum ada). Nama event menunggu OQ-2
    - _Requirements: 16.1, 16.2, 16.3, 16.4, 16.5, 16.6_
  - [ ] 7.2 `POST /booking-sessions/{id}/cancel` (keluar tanpa membeli)
    - _Requirements: 15.1_
  - **Property 9: Server-authoritative timers**
  - _Requirements: 7.1, 7.2_

- [ ] 8. Ubah pesanan & pemulihan sesi
  - Ada: `edit.go`, `PUT /bookings/{id}/items`, `unit_of_work.ReplaceItems`
  - [ ] 8.R1 Kunci admission dulu; tolak bila admission bukan `EDITABLE`, lewat grace, atau batas edit habis. Sesuaikan juga `allocated`/`held` di `event_supply` saat jumlah item berubah
    - **Property 4: Edit atomicity**
    - _Requirements: 8.1, 8.3, 8.4, 8.6, 8.7_
  - [ ] 8.R2 `GET /api/v1/bookings/{id}` belum ada (dipakai halaman bayar untuk polling status)
    - _Requirements: 10.4, 8.10_
  - _Requirements: 8.2, 8.5, 8.8, 8.9_

- [ ] 9. Settlement (`payment.succeeded`)
  - Ada: consumer, `settle.go`, `unit_of_work.Settle` (idempoten lewat status != PAID + `ON CONFLICT` tiket)
  - [ ] 9.R1 Teruskan `settlementTime` dan `amountMinor` dari event. Sekarang `settlement_time` diisi waktu proses
    - _Requirements: 9.1, 9.2_
  - [ ] 9.R2 Cek ketepatan waktu: `settlementTime > payment_expires_at` → REVIEW (lepas hold); booking EXPIRED/CANCELLED → REVIEW; nominal tidak cocok → REVIEW + alert (OQ-17)
    - **Property 7: Timeliness by settlement time**
    - _Requirements: 9.3, 9.4_
  - **Property 6: Idempotent settlement**
  - _Requirements: 9.5, 9.6, 10.1_

- [ ] 10. Worker sweeper sesi & expiry + resolve
  - Ada: `sweepers.go`, `expire.go`, `admission_repo.ExpireSession`, payment client
  - [ ] 10.R1 **Defect sweeper sesi:** `ACTIVE`/`EDITABLE` tidak mengembalikan `allocated_units` (stok bocor permanen) dan tidak menulis `booking.journey.ended` (slot Queue tidak pernah lepas). Terapkan design §7.1: `ACTIVE` → EXPIRED + lepas alokasi; `EDITABLE` → `CONVERTED(SELECTION_EXPIRED)` + lepas sisa unit, booking tetap bisa dibayar
    - **Property 11: Journey end exactly once**
    - _Requirements: 7.3, 7.6, 12.4_
  - [ ] 10.R2 Expiry: PENDING_PAYMENT lewat deadline → `VERIFYING` dulu, lalu resolve `CHECK_AND_CANCEL`. `SUCCEEDED` tepat waktu → PAID (sekarang REVIEW); `UNKNOWN` → tunggu sampai `VERIFY_GRACE_SECONDS`
    - **Property 8: No release before payment certainty**
    - _Requirements: 9.7, 9.8, 9.9, 9.10, 9.11, 5.11_
  - [ ] 10.R3 Config `PAYMENT_SERVICE_URL`, `VERIFY_GRACE_SECONDS`, breaker ke Payment
    - _Requirements: 13.2_

- [ ] 11. Integritas event sourcing
  - Ada: append + retry `ErrConcurrency` maks 3 di unit of work
  - [ ] 11.1 Integration test append-only, konflik versi, replay read model
    - **Property 10: Append-only & concurrency**
    - _Requirements: 11.1, 11.2_
  - [ ] 11.2 Query rekonsiliasi untuk `tools/reconcile`
    - _Requirements: 11.7_
    - _Depends on: platform 9.1_

- [ ] 12. Outbox + relay
  - Ada: `outbox_writer.go`, relay di worker
  - [ ] 12.R1 **Defect integrasi:** payload `booking.journey.ended` sekarang `{bookingId, outcome}`. Queue (dan schema) butuh `{eventId, queueEntryId, outcome}`, jadi pesan ditolak dan slot antrean tidak pernah dibebaskan
    - _Requirements: 12.3, 12.7_
  - [ ] 12.R2 Test kontrak semua payload terhadap `docs/contracts/events/*.schema.json`
    - _Requirements: 12.7_
  - [ ] 12.R3 `booking.quota.exhausted`: pemicu sekarang `held + paid = max`, tetapi Queue tidak pernah menghapus NIK dari set, jadi NIK tetap terblokir walau hold expired. Pilih: terbitkan saat `paid = max`, atau tambah event pembuka blokir (OQ-13)
    - _Requirements: 12.5_
  - _Requirements: 12.1, 12.2, 12.6_

- [ ] 13. API internal untuk Payment — payable, payment-lock, deadline-extension (belum ada; **memblokir Payment**)
  - `payment-lock` tidak mengubah status booking: isi `payment_started_at`, kunci admission `CONVERTED(PAYMENT_STARTED)`, lepas sisa unit (design §9.4)
  - Sertakan `publicRef` di `Payable` untuk nomor order Midtrans (OQ-21)
  - **Property 5: Lock serialization**
  - _Requirements: 14.1, 14.2, 14.3, 14.4, 14.5, 14.6, 8.6_

- [ ] 14. Admin seed API + verifikasi akhir
  - [ ] 14.1 `POST/PATCH /internal/v1/admin/events` (+ upload gambar, OQ-16)
    - _Requirements: 18.1, 18.2, 18.3, 18.4, 18.5, 4.7_
  - [ ] 14.2 Tiket & riwayat: ticket link token di `booking.paid`, akses via query `t`
    - _Requirements: 10.2, 10.3, 10.5, 10.6, 10.7_
  - [ ] 14.3 Batal pesanan (resolve dulu, SHOULD)
    - _Requirements: 15.2, 15.3, 15.4, 15.5_
  - [ ] 14.4 Metrik (setelah OQ-5) dan log tanpa NIK/email/token
    - **Property 12: Monotonic status**
    - _Requirements: 13.3, 13.4, 13.5_

- [ ] 15. Perlindungan & retensi data pribadi (keputusan 2026-10-21)
  - [ ] 15.1 Migrasi baru: `buyer_email_enc`, `buyer_email_hash`, `buyer_email_masked` di `admissions` & `bookings`; backfill; kosongkan plaintext (lepas `NOT NULL`). Kolom plaintext di-drop di migrasi berikutnya setelah rilis stabil
    - _Requirements: 19.1, 19.2_
  - [ ] 15.2 Job purge PII: `PII_RETENTION_DAYS_AFTER_EVENT` setelah event → hapus ciphertext email & NIK pemegang, nama → tersamar, hash dipertahankan
    - _Requirements: 19.3, 19.5_
    - _Depends on: platform 13.1_
  - [ ] 15.3 Job purge outbox: hapus baris terkirim > 7 hari
    - _Requirements: 19.4_

## Notes
- Routing key tanpa `.v1` dipakai konsisten di semua service; penyelarasannya dikerjakan sekali di platform task 12 supaya tidak pecah sebagian.
- Error response mengikuti format ringkas di platform task 2.6.
