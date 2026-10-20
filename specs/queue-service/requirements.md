# Requirements — Queue Service

| Field | Value |
|-------|-------|
| Spec | queue-service |
| Sumber kebenaran | `docs/sdd/SDD-QUEUE-001.md` (v1.1), PRD v5.4, SRS v1.3, HLD v1.6 |
| Service | Queue Service (`services/queue`), port 8081, schema `queue`, role `svc_queue` |
| Owner | BE-1 |

Queue Service mengelola *waiting room* war tiket: menahan lonjakan join (hingga 20.000/detik) dengan Valkey sebagai state store, menjaga FIFO murni, dan memasukkan (admit) pembeli ke Booking secara terkendali agar database transaksi terlindungi di belakang admission. Prinsip: **FIFO murni**, **fail closed** (join/claim ditolak saat Valkey down, bukan gagal diam), dan **CDN-first** (Status Document di-cache 1 detik).

---

## Status Implementasi (Deltas)

Status kode per 2026-10-19: **boilerplate**. Logika bisnis antrean **belum** ada; baru rangka service standar organisasi yang berfungsi.

| Area | Status | Bukti di kode |
|------|--------|---------------|
| Cobra verb (`api`, `worker`, `migrate`, `healthcheck`) | ✅ Implemented | `cmd/*.go` |
| Config D-24 (`APP_*`, `POSTGRES_*`, `VALKEY_*`, `RABBITMQ_*`) + validasi fail-fast | ✅ Implemented | `internal/config/{config,load}.go` |
| `GET /health` (ping Postgres, Valkey, RabbitMQ) | ✅ Implemented | `internal/queue/infrastructure/http/health_handler.go`, `.../healthcheck/checker.go` |
| Composition root server & worker | ✅ Implemented (worker `jobs()` kosong) | `internal/delivery/{server,worker}.go` |
| Migrasi `event_settings` + `entry_audit` | ✅ Implemented | `deployments/migrations/0001_init.up.sql`, `0002_account_ref.up.sql` |
| Join / Direct Admission | ❌ Not implemented | — |
| Status Document, Get My Entry, Claim, Leave | ❌ Not implemented | — |
| Worker Promotor / Status Publisher / Claim Sweeper | ❌ Not implemented | `jobs()` mengembalikan `nil` |
| Lua `join.lua` dan skrip atomik lain | ❌ Not implemented | belum ada di `assets/` |
| Outbound ke Booking (`allocations:batch`, `supply`) | ❌ Not implemented | — |
| Event publish/consume (RabbitMQ) | ❌ Not implemented | — |

> **Delta skema penting:** migrasi `0001_init.up.sql` yang ada **lebih kaya** dari SDD §5.1. `event_settings` aktual memakai kolom `active_slot_capacity`, `near_front_multiplier`, `status_publish_interval_ms`, `poll_far/mid/near_ms`, `one_active_per_visitor/email`, `category_limited_threshold_pct`, `pre_turn_notify_enabled` (bukan `max_active_slots`/`claim_window_seconds`/`polling_interval_ms` seperti SDD). `entry_audit` memakai `id bigserial`, kolom `action` (`JOINED|ADMITTED|CLAIMED|MISSED|LEFT|COMPLETED|ENDED|SOLD_OUT|CLOSED|REJECTED`) + `metadata jsonb`, **bukan** kolom timestamp per-transisi SDD. Spec ini memakai **skema migrasi aktual** sebagai sumber data model, dan menandai SDD §5.1 sebagai referensi konseptual. Requirement di bawah adalah **target**; acceptance criteria berlaku saat fitur dibangun.

---

## Requirement 1 — Guest Session / Visitor Identity

**User Story:** Sebagai pengunjung (guest maupun login), saya ingin perangkat saya dikenali lewat visitor token, agar entry antrean saya terikat ke perangkat dan tidak bisa diduplikasi.

Trace: SRS-F-01-01..09, SRS-F-27-10. TC: TC-GST-01..05, TC-USR-09, TC-USR-09a.

### Acceptance Criteria (EARS)

1. WHEN join diterima tanpa header `X-Visitor-ID`, THE Queue Service SHALL menolak dengan `400 VALIDATION_ERROR`.
2. WHEN join berhasil untuk guest, THE Queue Service SHALL menerbitkan visitor token dan mengembalikannya ke klien.
3. IF request membawa `Authorization: Bearer <userToken>` yang valid dengan NIK tersimpan (`token.HasNik`), THEN THE Queue Service SHALL memakai `token.nikHash` dan mengabaikan `body.buyerNik`.
4. IF request login tanpa NIK tersimpan (`token.HasNik == false`), THEN THE Queue Service SHALL mewajibkan `body.buyerNik` sama seperti guest.
5. WHILE visitor sudah punya entry aktif di event yang sama, THE Queue Service SHALL menolak join baru dengan `409 VISITOR_ALREADY_IN_QUEUE`.

---

## Requirement 2 — Queue Join & Direct Admission

**User Story:** Sebagai pembeli, saya ingin masuk antrean sebuah event dengan NIK + email, agar mendapat nomor urut FIFO atau langsung masuk jika slot tersedia.

Trace: SRS-F-04-01..10. TC: TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08.

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/events/{eventId}/queue-entries` diterima dengan `email` kosong, THE Queue Service SHALL menolak dengan `422 VALIDATION_ERROR` (email wajib untuk semua auth state).
2. IF `buyerNik` wajib (guest atau login tanpa NIK) tetapi kosong, THEN THE Queue Service SHALL menolak dengan `422 NIK_REQUIRED`.
3. IF `buyerNik` tidak 16 digit numerik, THEN THE Queue Service SHALL menolak dengan `422 NIK_INVALID`.
4. WHEN join valid dan slot aktif tersedia, THE Queue Service SHALL langsung memberi status `201 ADMITTED`.
5. WHEN join valid tetapi slot penuh, THE Queue Service SHALL memasukkan ke antrean dengan status `201 WAITING` dan nomor `seq` monotonik tanpa celah.
6. THE Queue Service SHALL menjalankan seluruh pengecekan constraint join (quota, NIK, visitor, email) dan penambahan sequence secara **atomik** lewat satu skrip Lua (`join.lua`).
7. IF NIK sudah punya entry aktif di event, THEN THE Queue Service SHALL menolak dengan `409 NIK_ALREADY_IN_QUEUE`.
8. IF email sudah punya entry aktif di event, THEN THE Queue Service SHALL menolak dengan `409 EMAIL_ALREADY_IN_QUEUE`.
9. IF NIK ada di set `quota_exhausted`, THEN THE Queue Service SHALL menolak dengan `409 BUYER_QUOTA_EXHAUSTED`.
10. IF event sold out, THEN THE Queue Service SHALL menolak dengan `410 EVENT_SOLD_OUT`.
11. IF jendela penjualan tertutup (`now < sale_opens_at` atau `now >= sale_closes_at`), THEN THE Queue Service SHALL menolak dengan `409 EVENT_NOT_ON_SALE`.
12. WHILE Valkey tidak dapat dijangkau, THE Queue Service SHALL menolak join (fail closed), bukan menerima diam-diam.

---

## Requirement 3 — Rate Limiting Join

**User Story:** Sebagai operator, saya ingin membatasi laju join per email dan per IP, agar antrean tahan abuse saat lonjakan.

Trace: SRS-F-04-09. TC: [TP] (Inspection — no dedicated TC; rate limit verified via code review).

### Acceptance Criteria (EARS)

1. WHEN laju join melampaui ambang per email atau per IP, THE Queue Service SHALL menolak dengan `429 RATE_LIMITED` dan menyertakan header `Retry-After`.
2. THE Queue Service SHALL menghitung rate limit di Valkey agar konsisten lintas instance.

---

## Requirement 4 — Queue Status Document (CDN Cached)

**User Story:** Sebagai klien polling, saya ingin mengambil status antrean agregat yang di-cache CDN, agar jutaan polling tidak menyentuh origin.

Trace: SRS-F-05-01..11. TC: TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-10, TC-QUE-12, TC-QUE-15.

### Acceptance Criteria (EARS)

1. WHEN `GET /api/v1/queue-status/{eventId}` dipanggil, THE Queue Service SHALL mengembalikan Status Document (`servingSeq`, `waitingCount`, `activeSlots`, `maxActiveSlots`, `saleStatus`, `updatedAt`, `config.pollingIntervalMs`).
2. THE Queue Service SHALL menyetel header cache sehingga CloudFront menyimpan dokumen selama 1 detik.
3. THE worker Status Publisher SHALL memperbarui `q:{e}:status` dengan TTL 1 detik pada interval terkonfigurasi.
4. THE Queue Service SHALL mengendalikan interval polling klien lewat field `config` di Status Document (server-authoritative).

---

## Requirement 5 — Get My Entry (Exact Position)

**User Story:** Sebagai pembeli dalam antrean, saya ingin melihat posisi persis saya, agar tahu kapan giliran tiba.

Trace: SRS-F-05-01..11. TC: TC-QUE-04, TC-QUE-14.

### Acceptance Criteria (EARS)

1. WHEN `GET /api/v1/queue-entries/me?eventId={eventId}` dipanggil dengan `X-Visitor-Token` atau cookie `rb_visitor` valid, THE Queue Service SHALL mengembalikan status entry dan posisi = `mySeq - servingSeq`.
2. IF entry tidak ditemukan, THEN THE Queue Service SHALL menjawab `404 ENTRY_NOT_FOUND`.

---

## Requirement 6 — Admission, Promotion & Claim

**User Story:** Sebagai pembeli, saya ingin dipromosikan secara FIFO lalu meng-claim giliran untuk masuk booking session, agar saya mendapat admission token sekali-pakai.

Trace: SRS-F-06-01..08, SRS-Q-FUN-03. TC: TC-QUE-09, TC-QUE-13, TC-QUE-14, TC-QUE-08.

### Acceptance Criteria (EARS)

1. WHILE ada slot bebas (`maxActiveSlots - len(active) > 0`), THE worker Promotor SHALL mengambil N entry teratas dari `waiting` **sesuai urutan seq** dan meminta reservasi ke Booking.
2. WHEN Booking membalas `RESERVED`, THE Queue Service SHALL memindahkan entry ke `active`, menyetel status `ADMITTED`, dan mem-publish `queue.turn.arrived`.
3. THE Queue Service SHALL menjaga urutan FIFO lintas promosi: tidak ada entry ber-seq lebih besar yang di-admit sebelum entry ber-seq lebih kecil yang masih eligible (SRS-Q-FUN-03).
4. WHEN `POST /api/v1/queue-entries/{entryId}/claim` dipanggil dengan queueToken valid dan status entry `ADMITTED`, THE Queue Service SHALL menerbitkan `admissionToken` sekali-pakai dengan `expiresAt` dan status `CLAIMED`.
5. IF `now >= entry.claimDeadline`, THEN THE Queue Service SHALL menolak claim dengan `403 CLAIM_DEADLINE_PASSED`.
6. IF status entry bukan `ADMITTED`, THEN THE Queue Service SHALL menolak claim dengan `403 ENTRY_NOT_ADMITTED`.
7. WHEN deadline claim lewat tanpa claim, THE worker Claim Sweeper SHALL menyetel status `UNCLAIMED`, menghapus dari `active`, dan mem-publish `booking.journey.ended` dengan outcome `UNCLAIMED`.

---

## Requirement 7 — Leave Queue & Move Entry

**User Story:** Sebagai pembeli, saya ingin keluar antrean atau memindahkan entry ke perangkat lain lewat tautan email, agar saya fleksibel tanpa kehilangan tiket.

Trace: SRS-F-13-03 (leave), SRS-F-01-10 (move). TC: TC-QUE-17, TC-QUE-16.

### Acceptance Criteria (EARS)

1. WHEN `DELETE /api/v1/queue-entries/{entryId}` dipanggil pada entry `WAITING`, THE Queue Service SHALL menyetel status `REJECTED`/`LEFT`, menghapus constraint keys, dan mengizinkan join ulang dari belakang antrean.
2. WHEN entry dipindahkan ke perangkat baru lewat tautan email, THE Queue Service SHALL memindahkan kepemilikan entry dan menonaktifkan tautan lama.

---

## Requirement 8 — Event Consumption (Idempoten)

**User Story:** Sebagai sistem, saya ingin bereaksi atas event Booking secara idempoten, agar slot dirilis dan antrean ditutup konsisten tanpa tabel inbox.

Trace: SRS-F-10-01..03, invarian §5 & §6. TC: TC-QUE-05.

### Acceptance Criteria (EARS)

1. WHEN `booking.journey.ended` dikonsumsi DAN `entry.status == CLAIMED`, THE Queue Service SHALL merilis slot dan menyetel entry `ENDED`.
2. WHEN `booking.quota.exhausted` dikonsumsi, THE Queue Service SHALL menambah `buyerNikHash` ke set `quota_exhausted` (idempoten lewat `SADD`).
3. WHEN `booking.sold_out` dikonsumsi, THE Queue Service SHALL menyetel `saleStatus = SOLD_OUT` dan menolak waiting berikutnya.
4. THE Queue Service SHALL menjamin konsumsi idempoten tanpa tabel inbox (guard state + operasi Lua/`SADD`), diverifikasi dengan test kirim ganda.

---

## Requirement 9 — Health & Operability

**User Story:** Sebagai operator, saya ingin endpoint health tunggal dan metrik antrean, agar ALB dan observability dapat memantau service. (**Implemented** untuk health.)

Trace: SDD §10. TC: integrasi health.

### Acceptance Criteria (EARS)

1. WHEN `GET /health` dipanggil, THE Queue Service SHALL mengembalikan `200 UP` bila Valkey, PostgreSQL, dan RabbitMQ sehat, atau `503 DOWN` bila ada yang gagal. *(Implemented.)*
2. THE Queue Service SHALL memvalidasi seluruh konfigurasi saat start dan gagal cepat bila tidak valid. *(Implemented.)*
3. THE Queue Service SHALL mengekspos metrik `rbt_queue_joins_total`, `rbt_queue_claims_total`, `rbt_queue_waiting_count`, `rbt_queue_active_count`, `rbt_queue_promotions_total`, `rbt_queue_unclaimed_total`. *(Not implemented.)*

---

## Invariant yang ditegakkan

| # | Invarian | Requirement penegak |
|---|----------|---------------------|
| 2 | Jangan PHP: admission hanya bila alokasi direservasi | Req 6.2 |
| 3 | Kuota 3 tiket/NIK/event lintas sesi | Req 2.7, 2.9, 8.2 |
| 4 | Timer server-authoritative | Req 4.4, 6.5, 6.7 |
| — | FIFO murni, seq tanpa celah | Req 2.5, 2.6, 6.1, 6.3 |
| — | Fail closed saat Valkey down | Req 2.12 |
