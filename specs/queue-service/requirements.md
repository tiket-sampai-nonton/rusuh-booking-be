# Requirements — Queue Service

| Field | Value |
|-------|-------|
| Spec | queue-service |
| Sumber kebenaran | Kode + kontrak aktual (`services/queue/`, `docs/contracts/openapi/queue.yaml`, event schema `docs/contracts/events/`), lalu `docs/sdd/SDD-QUEUE-001.md` (v1.1), PRD v5.4, SRS v1.3, HLD v1.6 |
| Prinsip grounding | **Kode & kontrak menang** untuk apa yang sudah ada; SDD dipakai sebagai referensi konseptual dan ditandai bila berbeda. |

Queue Service mengelola *waiting room* war tiket: menahan lonjakan join (hingga 20.000/detik) dengan **Valkey** sebagai state store panas, menjaga **FIFO murni**, dan memasukkan (admit) pembeli ke Booking secara terkendali agar database transaksi terlindungi di belakang admission. Tiga prinsip arsitektur:

1. **FIFO murni** — nomor antrean (`seq`) monotonik, tidak pernah loncat, urutan admission = urutan `seq`.
2. **Fail closed** — saat Valkey tidak terjangkau, join/claim **ditolak** (`503 DEPENDENCY_UNAVAILABLE`), bukan gagal diam.
3. **CDN-first** — Status Document di-cache 1 detik di CloudFront; jutaan polling tidak menyentuh origin.

---

## 1. Service Metadata

| Atribut | Nilai | Bukti |
|---------|-------|-------|
| Service | Queue Service | `services/queue` |
| Document ID | SDD-QUEUE-001 v1.1 | `docs/sdd/SDD-QUEUE-001.md` |
| Port | 8081 | `config/load.go` (`APP_PORT` default 8081), `.env.example` |
| Schema PostgreSQL | `queue` | `POSTGRES_SCHEMA=queue` |
| DB role | `svc_queue` | `POSTGRES_USER=svc_queue` |
| Owner | BE-1 | SDD header, README |
| Region | ap-southeast-3 (Jakarta) | SDD header |
| Verb CLI (Cobra) | `api`, `worker`, `migrate up\|down\|status`, `healthcheck` | `cmd/*.go` |
| Dependency | PostgreSQL 16 (audit + settings), Valkey 8.1 (state antrean, atomik Lua, rate limit), RabbitMQ (event publish/consume) | `clients.go`, `config.go` |
| Outbound HTTP | Booking Service (`/internal/v1/allocations:batch`, `/internal/v1/events/{id}/supply`) | SDD §4.1–4.2, `internal-booking.yaml` |
| Exchange event | `rusuh.events` (topic) | `RABBITMQ_EXCHANGE` |
| Token admission | EdDSA (`pkg/token`), sekali-pakai, TTL 60 detik | `queue.yaml` ClaimResponse |

---

## 2. Status Implementasi (Delta: implemented vs target)

Status kode per 2026-10-20: **boilerplate standar organisasi berfungsi**; logika bisnis antrean **belum** ada.

| Area | Status | Bukti / catatan |
|------|--------|-----------------|
| Cobra verb (`api`, `worker`, `migrate`, `healthcheck`) | ✅ Implemented | `cmd/{api,worker,migrate,healthcheck,root,main}.go` |
| Config D-24 + validasi fail-fast (semua error sekaligus) | ✅ Implemented | `internal/config/{config,load}.go`, `load_test.go` |
| `GET /health` (ping Postgres + Valkey + RabbitMQ) | ✅ Implemented | `infrastructure/http/health_handler.go`, `infrastructure/healthcheck/checker.go`, `application/usecase/check_health.go` |
| Composition root server (`NewHandler` + route registrar) | ✅ Implemented | `internal/delivery/server.go` (hanya `HealthHandler.Register`) |
| Composition root worker (`loop` atas `jobs()`) | ✅ Implemented; `jobs()` mengembalikan `nil` | `internal/delivery/worker.go` |
| Clients (PG/VK/MQ, buka hanya yang diperlukan) | ✅ Implemented | `internal/delivery/clients.go` |
| Migrasi `event_settings` + `entry_audit` + `account_id` | ✅ Implemented | `deployments/migrations/0001_init.up.sql`, `0002_account_ref.up.sql`, `embed.go` |
| Shared `ValidationError` | ✅ Implemented | `internal/shared/entity/validation_error.go` |
| Join / Direct Admission + `join.lua` | ❌ Not implemented | — |
| Status Document + Status Publisher worker | ❌ Not implemented | — |
| Get My Entry (posisi eksak) | ❌ Not implemented | — |
| Claim + `claim.lua` + admission token (EdDSA) | ❌ Not implemented | — |
| Leave + Move entry | ❌ Not implemented | — |
| Promotor worker + `promote.lua` + Booking client | ❌ Not implemented | — |
| Claim Sweeper worker | ❌ Not implemented | — |
| Event publish (`queue.turn.soon`, `queue.turn.arrived`) | ❌ Not implemented | schema tersedia di `docs/contracts/events/` |
| Event consume (`booking.journey.ended`, `booking.quota.exhausted`, `booking.sold_out`) | ❌ Not implemented | schema tersedia; consumer idempoten tanpa inbox |
| Rate limit (Valkey, per email + IP) | ❌ Not implemented | — |
| Metrics (`rbt_queue_*`) | ❌ Not implemented | — |

### 2.1 Delta penting Kode/Kontrak vs SDD (kode/kontrak menang)

| Topik | SDD mengatakan | Kode/Kontrak aktual (dipakai spec ini) |
|-------|----------------|----------------------------------------|
| `event_settings` kolom | `max_active_slots`, `claim_window_seconds`, `polling_interval_ms` | `active_slot_capacity`, `near_front_multiplier`, `status_publish_interval_ms`, `poll_far/mid/near_ms`, `one_active_per_visitor/email`, `category_limited_threshold_pct`, `pre_turn_notify_enabled` (`0001_init.up.sql`) |
| `entry_audit` | kolom timestamp per-transisi (`admitted_at`, `claimed_at`, …) + `outcome` | `id bigserial`, kolom `action` enum + `metadata jsonb` + `account_id`; append-only |
| JoinRequest body | `{ buyerNik?, email }` | `{ buyerNik?, email, emailConfirmation(req), consentNik(req=true) }` (`queue.yaml`) |
| Join 422 | `NIK_REQUIRED` | `NIK_INVALID`, `EMAIL_MISMATCH`, `NIK_REQUIRED` |
| Join 503 | tidak eksplisit | `DEPENDENCY_UNAVAILABLE` (fail-closed, retry dengan Idempotency-Key) |
| Claim auth | `Authorization: Bearer <queueToken>` | visitor cookie `rb_visitor` **atau** header `X-Visitor-Token` |
| Claim gagal | `403 CLAIM_DEADLINE_PASSED`, `403 ENTRY_NOT_ADMITTED` | `410 TURN_MISSED`, `410 EVENT_SOLD_OUT` |
| ClaimResponse | `{ admissionToken, expiresAt }` | `{ admissionToken, bookingUrl }` (TTL 60s didokumentasikan pada field) |
| Leave sukses | `200` | `204 No Content` |
| StatusDocument | `{ servingSeq, waitingCount, activeSlots, maxActiveSlots, saleStatus, config.pollingIntervalMs }` | `saleState` (`NOT_STARTED\|OPEN\|CLOSED`), `servingSeq`, `guaranteedUntilSeq`, `possibleUntilSeq`, `soldOut`, `remainingTickets`, `admissionRatePerMin`, `removedBuckets`, `nextPollAfterMs`, `poll{farMs,midMs,nearMs,farAbove,midAbove}`, `preTurnNotifyPosition`, `categories[]`, `updatedAt` |
| Status enum | `WAITING\|ADMITTED\|CLAIMED\|REJECTED\|UNCLAIMED\|ENDED` | `WAITING\|ADMITTED\|CLAIMED\|MISSED\|LEFT\|SOLD_OUT\|CLOSED\|COMPLETED\|ENDED` (QueueEntry) |
| Turn events payload | `{ entryId, eventId, pushToken, position/claimDeadline }` | `{ eventId, queueEntryId, visitorId[, claimDeadline] }` — **tanpa** pushToken/position (Notification me-resolve kontak) |
| Event key naming | `entryId` | `queueEntryId` (semua event schema) |
| Outcome alokasi Booking (spec lama) | `RESERVED`, `QUOTA_EXHAUSTED`, entry `REJECTED` | **`GRANTED`, `NO_SUPPLY_NOW`, `SOLD_OUT`, `BUYER_QUOTA_EXHAUSTED`, `ALREADY_ACTIVE`** (`internal-booking.yaml`); status entry tidak punya `REJECTED` → `ENDED` + `reason` (K6) |
| Claim sweeper (spec lama) | publish `booking.journey.ended(UNCLAIMED)` | Queue **tidak** menerbitkan event `booking.*`; Booking session sweeper yang menerbitkannya saat claim window habis (SDD-RBT C.5/C.10, K7) |
| Direct admission (spec lama) | slot bebas + supply snapshot → `ADMITTED` | Wajib `allocations:batch` → `GRANTED` lebih dulu (invarian #2, SRS-F-04-07, SRS-F-06-02) |
| Routing key | `queue.turn.soon` | `queue.turn.soon.v1`, `queue.turn.arrived.v1`; consume `booking.*.v1` (SDD-RBT §4.6.2, K8) |

> Requirement di bawah adalah **target**. Acceptance criteria berlaku saat fitur dibangun; yang sudah `Implemented` ditandai eksplisit.

## Glossary

- **Entry** — satu antrean pembeli per event (`q:{e}:entry:<entryId>`), status `WAITING → ADMITTED → CLAIMED → COMPLETED/ENDED` atau terminal `MISSED/LEFT/SOLD_OUT/CLOSED`.
- **seq** — nomor urut FIFO monotonik per event (`INCR` atomik di `join.lua`).
- **servingSeq** — seq terdepan yang sedang dilayani; posisi = `seq − servingSeq − removedAhead`.
- **Active slot** — kapasitas entry `ADMITTED`/`CLAIMED` bersamaan (`event_settings.active_slot_capacity`).
- **Admission / allocation** — reservasi unit di Booking untuk entry (`allocationId = admissions.id`); entry hanya `ADMITTED` bila Booking membalas `GRANTED`.
- **Claim window / claimDeadline** — batas klaim giliran (dari Booking `claimDeadline`, default 180 s).
- **Status Document** — JSON agregat per event tanpa data pribadi, di-cache CDN 1 s.
- **Zona** — `GUARANTEED` (`seq ≤ guaranteedUntilSeq`), `POSSIBLE`, `NOT_ENOUGH`, atau habis.
- **Visitor / queue / admission token** — JWT EdDSA terbitan Queue (HLD tabel Token).

---

## Requirement 1 — Guest Session / Visitor Identity

**User Story:** Sebagai pengunjung (guest maupun login), saya ingin perangkat saya dikenali lewat visitor token, agar entry antrean saya terikat ke perangkat dan tidak bisa diduplikasi.

- **SRS trace:** SRS-F-01-01..09, SRS-F-27-10, SRS-F-27-10a
- **TC trace:** TC-GST-01, TC-GST-03, TC-GST-04, TC-USR-09, TC-USR-09a
- **Error codes:** `VALIDATION_ERROR` (400), `VISITOR_ALREADY_IN_QUEUE` (409)

### Acceptance Criteria (EARS)

1. WHEN join berhasil untuk guest (web), THE Queue Service SHALL menerbitkan visitor token dan menyetelnya sebagai cookie `rb_visitor` (HttpOnly, Secure, SameSite=Lax).
2. WHEN join berhasil untuk klien mobile, THE Queue Service SHALL mengembalikan `visitorToken` di body (`QueueEntry.visitorToken`) untuk disimpan di secure storage.
3. IF request membawa `Authorization: Bearer <userToken>` EdDSA valid dengan NIK tersimpan (`token.has_nik == true`), THEN THE Queue Service SHALL memakai `token.nik_hash` dan **mengabaikan** `body.buyerNik`, serta membawa `accountId` ke audit dan ke Booking.
4. IF request login tanpa NIK tersimpan (`token.has_nik == false`), THEN THE Queue Service SHALL mewajibkan `body.buyerNik` sama seperti guest.
5. WHILE visitor sudah punya entry aktif di event yang sama DAN `event_settings.one_active_per_visitor == true`, THE Queue Service SHALL menolak join baru dengan `409 VISITOR_ALREADY_IN_QUEUE`.
6. WHEN entry sebelumnya pada visitor yang sama sudah berakhir (status terminal), THE Queue Service SHALL mengizinkan join baru dari perangkat itu.

---

## Requirement 2 — Queue Join & Direct Admission

**User Story:** Sebagai pembeli, saya ingin masuk antrean sebuah event dengan NIK + email, agar mendapat nomor urut FIFO atau langsung masuk jika slot tersedia dan tidak ada pengantre lebih awal.

- **SRS trace:** SRS-F-04-01..10, FR-05 (direct admission)
- **TC trace:** TC-QUE-01, TC-QUE-02, TC-QUE-07, TC-QUE-08, TC-GST-01, TC-GST-02, TC-GST-05
- **Endpoint:** `POST /api/v1/events/{eventId}/queue-entries`

### Acceptance Criteria (EARS)

1. WHEN body `email` kosong ATAU `email != emailConfirmation`, THE Queue Service SHALL menolak dengan `422 EMAIL_MISMATCH` (atau `400 VALIDATION_ERROR` bila field tidak ada).
2. IF `consentNik != true`, THEN THE Queue Service SHALL menolak dengan `400 VALIDATION_ERROR`.
3. IF `buyerNik` wajib (guest atau login tanpa NIK) tetapi kosong, THEN THE Queue Service SHALL menolak dengan `422 NIK_REQUIRED`.
4. IF `buyerNik` gagal validasi struktur `pkg/nik` (16 digit, provinsi, kab/kec, tanggal lahir, nomor urut — platform R1), THEN THE Queue Service SHALL menolak dengan `422 NIK_INVALID` (SRS-F-11-01, TC-GST-02).
5. WHEN join valid, tidak ada pengantre lebih awal, slot aktif tersedia, DAN Booking membalas `GRANTED` untuk entry itu lewat `allocations:batch`, THE Queue Service SHALL langsung memberi `201` dengan `status = ADMITTED`, `claimDeadline`, dan `queueToken`; bila Booking membalas selain `GRANTED` atau tidak terjangkau, entry tetap `WAITING` (SRS-F-04-07, invarian #2).
6. WHEN join valid tetapi harus menunggu, THE Queue Service SHALL memberi `201` dengan `status = WAITING` dan `seq` monotonik tanpa celah.
7. THE Queue Service SHALL menjalankan seluruh pengecekan constraint join (quota_exhausted, NIK, visitor, email) dan `INCR seq` secara **atomik** lewat satu skrip Lua (`join.lua`) dengan semua KEYS pada hash tag `{e:<eventId>}` yang sama.
8. IF NIK sudah punya entry aktif di event, THEN THE Queue Service SHALL menolak dengan `409 NIK_ALREADY_IN_QUEUE`.
9. IF email sudah punya entry aktif di event DAN `event_settings.one_active_per_email == true`, THEN THE Queue Service SHALL menolak dengan `409 EMAIL_ALREADY_IN_QUEUE`.
10. IF NIK ada di set `q:{e}:quota_exhausted`, THEN THE Queue Service SHALL menolak dengan `409 BUYER_QUOTA_EXHAUSTED`.
11. IF `saleState == CLOSED` karena sold out, THEN THE Queue Service SHALL menolak dengan `410 EVENT_SOLD_OUT`.
12. IF jendela penjualan belum/sudah tutup (`now < sale_opens_at` atau `now >= sale_closes_at`), THEN THE Queue Service SHALL menolak dengan `409 EVENT_NOT_ON_SALE`.
13. WHILE Valkey tidak dapat dijangkau, THE Queue Service SHALL menolak join dengan `503 DEPENDENCY_UNAVAILABLE` (fail closed) dan menyarankan retry dengan `Idempotency-Key` yang sama.
14. WHEN join diterima ulang dengan `Idempotency-Key`/`X-Request-ID` yang sama dan entry aktif sudah ada untuk kunci unik itu, THE Queue Service SHALL mengembalikan entry yang sama (bukan membuat duplikat).

---

## Requirement 3 — Rate Limiting Join & Polling

**User Story:** Sebagai operator, saya ingin membatasi laju request per email dan per IP, agar antrean tahan abuse saat lonjakan.

- **SRS trace:** SRS-F-04-09
- **TC trace:** [TP] Inspection (tidak ada TC dedicated; diverifikasi via code review)
- **Error codes:** `RATE_LIMITED` (429, + header `Retry-After`)

### Acceptance Criteria (EARS)

1. WHEN laju join melampaui ambang per email atau per IP, THE Queue Service SHALL menolak dengan `429 RATE_LIMITED` dan menyertakan header `Retry-After` (detik).
2. WHEN laju `GET /api/v1/queue-entries/me` melampaui ambang, THE Queue Service SHALL menolak dengan `429 RATE_LIMITED`.
3. THE Queue Service SHALL menghitung rate limit di Valkey agar konsisten lintas instance.
4. THE Queue Service SHALL **tidak** me-rate-limit `GET /api/v1/queue-status/{eventId}` di origin (polling diserap CDN; keadilan diatur server lewat field `poll` di Status Document — TC-QUE-14).

---

## Requirement 4 — Queue Status Document (CDN Cached)

**User Story:** Sebagai klien polling, saya ingin mengambil status antrean agregat yang di-cache CDN, agar jutaan polling tidak menyentuh origin dan interval polling dikendalikan server.

- **SRS trace:** SRS-F-05-01..11
- **TC trace:** TC-QUE-03, TC-QUE-10, TC-QUE-11, TC-QUE-12, TC-QUE-15, TC-PERF-01, TC-PERF-03
- **Endpoint:** `GET /api/v1/queue-status/{eventId}` (public, no auth)

### Acceptance Criteria (EARS)

1. WHEN `GET /api/v1/queue-status/{eventId}` dipanggil, THE Queue Service SHALL mengembalikan Status Document dengan field wajib `eventId`, `saleState`, `servingSeq`, `soldOut`, `updatedAt`, serta field zona/polling bila tersedia.
2. THE Queue Service SHALL menyetel header `Cache-Control: public, max-age=1, stale-while-revalidate=2` sehingga CloudFront menyimpan dokumen 1 detik.
3. THE worker Status Publisher SHALL memperbarui `q:{e}:status` dengan TTL 1 detik pada interval `event_settings.status_publish_interval_ms`.
4. THE Queue Service SHALL mengendalikan interval polling klien lewat objek `poll` (`farMs`, `midMs`, `nearMs`, `farAbove`, `midAbove`) dan `nextPollAfterMs` (server-authoritative); klien hanya menyimpan cadangan bila field tidak ada (TC-QUE-15).
5. THE Status Document SHALL menghitung zona: `guaranteedUntilSeq` (zona **Dijamin**) dan `possibleUntilSeq` (zona **Peluang**) dari stok, alokasi, dan `near_front_multiplier`; saat sisa stok < 3 batas Dijamin dibulatkan `ceil` (TC-QUE-11).
6. THE Status Document SHALL memuat status kategori (`categories[].state ∈ {AVAILABLE,LIMITED,SOLD_OUT}`) dengan ambang `event_settings.category_limited_threshold_pct` (TC-QUE-12).
7. THE Status Document SHALL **tidak** memuat data pribadi (NIK, email, token).
8. IF event tidak ada, THEN THE Queue Service SHALL menjawab `404 NOT_FOUND`.

---

## Requirement 5 — Get My Entry (Exact Position)

**User Story:** Sebagai pembeli dalam antrean, saya ingin melihat posisi persis dan zona saya, agar tahu kapan giliran tiba.

- **SRS trace:** SRS-F-05-01..11
- **TC trace:** TC-QUE-03, TC-QUE-04, TC-QUE-06, TC-QUE-14
- **Endpoint:** `GET /api/v1/queue-entries/me?eventId={eventId}` (auth: cookie `rb_visitor` atau header `X-Visitor-Token`)

### Acceptance Criteria (EARS)

1. WHEN dipanggil dengan visitor valid, THE Queue Service SHALL mengembalikan `EntryStatus` dengan `entryId`, `seq`, `status`, `exactPosition`, dan `zone ∈ {GUARANTEED,POSSIBLE,NOT_ENOUGH}`.
2. THE `exactPosition` SHALL dihitung = `mySeq - servingSeq - removedAhead` (memperhitungkan entry yang keluar di depan).
3. WHEN status entry `ADMITTED`, THE Queue Service SHALL menyertakan `claimDeadline` dan `redirect` (ke booking) di `EntryStatus`.
4. IF entry tidak ditemukan untuk visitor itu, THEN THE Queue Service SHALL menjawab `404 NOT_FOUND`.
5. IF entry sudah `MISSED`/`LEFT`/`SOLD_OUT`/`CLOSED`, THEN THE Queue Service SHALL mengembalikan `status` tersebut beserta `reason` agar klien menampilkan pesan yang tepat.

---

## Requirement 6 — Admission, Promotion & Claim

**User Story:** Sebagai pembeli, saya ingin dipromosikan secara FIFO lalu meng-claim giliran untuk masuk booking session, agar saya mendapat admission token sekali-pakai.

- **SRS trace:** SRS-F-06-01..08, SRS-Q-FUN-03 (FIFO)
- **TC trace:** TC-QUE-05, TC-QUE-08, TC-QUE-09, TC-QUE-13, TC-QUE-14, TC-PERF-04
- **Endpoint:** `POST /api/v1/queue-entries/{entryId}/claim` (auth: visitor cookie/header)
- **Error codes:** `TURN_MISSED` (410), `EVENT_SOLD_OUT` (410), `RATE_LIMITED` (429), `NOT_FOUND` (404)

### Acceptance Criteria (EARS)

1. WHILE ada slot bebas (`active_slot_capacity - len(active) > 0`), THE worker Promotor SHALL mengambil N entry teratas dari `q:{e}:waiting` **sesuai urutan seq** (ZRANGEBYSCORE naik) dan meminta reservasi ke Booking lewat `POST /internal/v1/allocations:batch` (service token, breaker Queue→Booking: timeout 500 ms, ambang 5, cooldown 5 s, bulkhead 50 — PROPOSED SDD-RBT §4.6.2).
2. WHEN Booking membalas `GRANTED` untuk sebuah entry, THE Queue Service SHALL memindahkan entry ke `q:{e}:active` (score = `claimDeadline` dari Booking), menyetel `status = ADMITTED`, menyimpan `allocationId`, dan mem-publish `queue.turn.arrived.v1`.
3. WHEN Booking membalas `NO_SUPPLY_NOW`, THE Queue Service SHALL mempertahankan entry di `waiting` pada `seq` yang sama (tidak kehilangan posisi) dan mencoba lagi pada siklus berikutnya; entry ber-seq lebih besar dalam batch yang sama juga tidak di-admit pada siklus itu.
4. WHEN Booking membalas `BUYER_QUOTA_EXHAUSTED`, THE Queue Service SHALL menambah `buyerNikHash` ke `q:{e}:quota_exhausted`, mengakhiri entry (`status = ENDED`, `reason = BUYER_QUOTA_EXHAUSTED`, audit `REJECTED`), dan membersihkan constraint key.
5. WHEN Booking membalas `SOLD_OUT`, THE Queue Service SHALL menutup antrean (`saleState = CLOSED`, `soldOut = true`) dan menyetel entry `WAITING` menjadi `SOLD_OUT`.
6. THE Queue Service SHALL menjaga FIFO lintas promosi: tidak ada entry ber-seq lebih besar yang di-admit sebelum entry ber-seq lebih kecil yang masih eligible (SRS-Q-FUN-03; TC-QUE-08, TC-QUE-14).
7. WHEN `POST .../claim` dipanggil dengan visitor valid DAN status entry `ADMITTED` DAN `now < claimDeadline`, THE Queue Service SHALL menerbitkan `admissionToken` EdDSA sekali-pakai (TTL `ADMISSION_TOKEN_TTL_SECONDS`, default 60, < claim window; klaim `jti, eventId, entryId, allocationId, visitorId`) + `bookingUrl`, dan menyetel status `CLAIMED` secara atomik (`claim.lua`).
8. IF `now >= entry.claimDeadline` atau status sudah `MISSED`, THEN THE Queue Service SHALL menolak claim dengan `410 TURN_MISSED`.
9. IF event sudah sold out saat claim, THEN THE Queue Service SHALL menolak dengan `410 EVENT_SOLD_OUT`.
10. WHEN deadline claim lewat tanpa claim, THE worker Claim Sweeper SHALL menyetel status `MISSED`, menghapus dari `active`, dan merilis slot Queue; **tidak** mem-publish event `booking.*` — Booking melepas alokasinya sendiri dan menerbitkan `booking.journey.ended.v1 (UNCLAIMED)` (K7, SRS-F-06-06).
11. WHEN posisi entry mencapai `preTurnNotifyPosition` DAN `pre_turn_notify_enabled == true`, THE Queue Service SHALL mem-publish `queue.turn.soon.v1` sekali (TC-QUE-13).
12. THE Promotor SHALL berjalan **satu per event** di seluruh instance worker (lease/lock terdistribusi di Valkey), sehingga dua instance tidak mem-promote event yang sama bersamaan (SRS-F-06-01).
13. WHEN Booking membalas `ALREADY_ACTIVE`, THE Queue Service SHALL memperlakukan entry sesuai keputusan OQ-18 *(belum ditetapkan dokumen mana pun)*; sampai diputuskan, entry dibiarkan `WAITING` dan dicatat sebagai anomali (alert).
14. Klaim giliran yang terlewat SHALL tidak memberi kesempatan kedua atau prioritas; pembeli boleh antre ulang dari belakang bila kuota masih ada (SRS-F-06-07).

---

## Requirement 7 — Leave Queue & Move Entry

**User Story:** Sebagai pembeli, saya ingin keluar antrean atau memindahkan entry ke perangkat lain lewat tautan email, agar saya fleksibel tanpa kehilangan tiket.

- **SRS trace:** SRS-F-13-03 (leave), SRS-F-01-10 (move)
- **TC trace:** TC-QUE-17 (leave), TC-QUE-16 (move)
- **Endpoint:** `DELETE /api/v1/queue-entries/{entryId}` (auth: visitor cookie/header)
- **Error codes:** `NOT_FOUND` (404), `ENTRY_MOVED` (perangkat lama)

### Acceptance Criteria (EARS)

1. WHEN `DELETE /api/v1/queue-entries/{entryId}` dipanggil pada entry `WAITING`/`ADMITTED` milik visitor itu, THE Queue Service SHALL menyetel status `LEFT`, menghapus constraint keys (nik/visitor/email), merilis slot bila sedang `ADMITTED`, dan menjawab `204 No Content`.
2. WHEN entry `ADMITTED` ditinggalkan, THE Queue Service SHALL memanggil `POST /internal/v1/allocations/{allocationId}:release` (`reason = QUEUE_ENTRY_GONE`) sebagai kompensasi saga (ADR-018); kegagalan panggilan SHALL dicoba ulang oleh worker sampai `released` diterima atau Booking sweeper mengakhiri admission pada claim deadline.
3. WHEN pembeli yang sudah `LEFT` join kembali, THE Queue Service SHALL memberinya `seq` baru (dari belakang antrean).
4. WHEN entry dipindahkan ke perangkat baru lewat tautan email, THE Queue Service SHALL memindahkan kepemilikan ke visitor baru dengan **posisi/seq sama**, menonaktifkan tautan lama, dan perangkat lama SHALL melihat `ENTRY_MOVED` (TC-QUE-16). *(SHOULD; tidak ada endpoint di `queue.yaml` — **OQ-3, blocked**.)*
5. IF entry tidak ditemukan, THEN THE Queue Service SHALL menjawab `404 NOT_FOUND`.

---

## Requirement 8 — Event Consumption (Idempoten, tanpa inbox)

**User Story:** Sebagai sistem, saya ingin bereaksi atas event Booking secara idempoten, agar slot dirilis dan antrean ditutup konsisten tanpa tabel inbox.

- **SRS trace:** SRS-F-10-01..03, invarian §5 & §6
- **TC trace:** TC-QUE-05 (kirim ganda), TC-PERF-05 (sold-out notif ≤ 5s)
- **Events consumed:** `booking.journey.ended`, `booking.quota.exhausted`, `booking.sold_out`

### Acceptance Criteria (EARS)

1. WHEN `booking.journey.ended.v1` dikonsumsi DAN entry berstatus `CLAIMED`, THE Queue Service SHALL merilis slot dan menyetel entry ke status terminal sesuai `outcome` (`PAID → COMPLETED`, lainnya → `ENDED`) secara idempoten (`end_entry.lua` guard transisi); WHEN entry masih `ADMITTED` dan `outcome = UNCLAIMED`, entry SHALL menjadi `MISSED` dan slot dirilis (balapan dengan Claim Sweeper aman).
2. WHEN `booking.journey.ended.v1` dikonsumsi untuk entry yang sudah terminal, THE Queue Service SHALL mengabaikannya tanpa error (idempoten).
3. WHEN `booking.quota.exhausted.v1` dikonsumsi, THE Queue Service SHALL menambah `payload.buyerNikHash` ke `q:{e}:quota_exhausted` lewat `SADD` (idempoten). *(Kapan Booking menerbitkannya: OQ-13.)*
4. WHEN `booking.sold_out` dikonsumsi, THE Queue Service SHALL menyetel `saleState = CLOSED`, `soldOut = true`, dan menolak waiting berikutnya.
5. IF payload rusak / tak dapat diparse, THEN THE Queue Service SHALL mengembalikan `rabbitmq.Permanent(err)` agar langsung ke DLQ (bukan retry tak berujung).
6. THE consumer SHALL retry berjenjang (`RABBITMQ_RETRY_DELAYS`) dan masuk DLQ setelah `RABBITMQ_MAX_RETRIES`.
7. THE konsumsi SHALL terbukti idempoten lewat test kirim-ganda (TC-QUE-05).

---

## Requirement 9 — Event Publication

**User Story:** Sebagai sistem, saya ingin mempublikasikan event giliran, agar Notification bisa mengirim push/email tanpa Queue tahu detail kontak.

- **SRS trace:** SRS-F-06-05, SRS-F-25-10..12 (turn notification)
- **TC trace:** TC-QUE-13, TC-PERF-05
- **Events published:** `queue.turn.soon.v1`, `queue.turn.arrived.v1`

### Acceptance Criteria (EARS)

1. WHEN entry mencapai posisi pra-giliran, THE Queue Service SHALL mem-publish `queue.turn.soon.v1` dengan payload `{ eventId, queueEntryId, visitorId }`.
2. WHEN entry di-admit (giliran tiba), THE Queue Service SHALL mem-publish `queue.turn.arrived.v1` dengan payload `{ eventId, queueEntryId, visitorId, claimDeadline }`.
3. THE payload event SHALL **tidak** memuat NIK plaintext, email, secret, atau token (ditegakkan kontrak `additionalProperties:false` + validasi payload sebelum publish, platform R5.4).
4. THE event SHALL dibungkus envelope (`messageId`, `eventType`, `schemaVersion`, `occurredAt`, `producer = queue-service`, `aggregateId`, `payload`), dipublikasikan langsung ke exchange `rusuh.events` (topic) dengan konfirmasi publisher — Queue **tidak** memakai outbox (ADR-010).
5. IF publish gagal, THEN THE Queue Service SHALL mencatat dan melewatinya (push best effort, SDD-RBT §4.6.2 "Retry Ownership"); kegagalan tidak membatalkan admission.

---

## Requirement 10 — Health & Operability

**User Story:** Sebagai operator, saya ingin endpoint health tunggal, konfigurasi tervalidasi, dan metrik antrean, agar ALB dan observability dapat memantau service.

- **SRS trace:** SDD §10, D-23, D-24
- **TC trace:** integrasi health
- **Endpoint:** `GET /health` (200 UP / 503 DOWN)

### Acceptance Criteria (EARS)

1. WHEN `GET /health` dipanggil, THE Queue Service SHALL mengembalikan `200 UP` bila Valkey, PostgreSQL, dan RabbitMQ sehat, atau `503 DOWN` bila ada yang gagal, dengan header `Cache-Control: no-store`. *(Implemented.)*
2. THE Queue Service SHALL memvalidasi seluruh konfigurasi saat start dan gagal cepat, melaporkan semua error sekaligus. *(Implemented.)*
3. THE Queue Service SHALL mengekspos metrik `rbt_queue_joins_total`, `rbt_queue_claims_total`, `rbt_queue_waiting_count`, `rbt_queue_active_count`, `rbt_queue_promotions_total`, `rbt_queue_unclaimed_total`. *(Not implemented.)*
4. THE Queue Service SHALL **tidak** menuliskan NIK/email/token plaintext ke log. *(Enforced oleh security-privacy steering.)*

---

## Invarian yang ditegakkan oleh service ini

| # (invariants.md) | Invarian | Requirement penegak |
|---|----------|---------------------|
| 2 | Jangan PHP: admission hanya bila alokasi direservasi | Req 2.5, 6.1–6.2 (promote/direct admission hanya setelah `GRANTED`) |
| 3 | Kuota 3 tiket/NIK/event lintas sesi | Req 2.8, 2.10, 6.4, 8.3 |
| 4 | Timer booking/pembayaran server-authoritative | Req 4.4, 6.7–6.10 (claimDeadline, poll server) |
| 5 | Pembayaran sukses idempoten (dari webhook/rekonsiliasi) | Req 8.1–8.2 (konsumsi `journey.ended` idempoten) |
| 6 | Kegagalan notifikasi tidak membatalkan transaksi | Req 9 (publish fire-and-forget; retry+DLQ di Notification) |
| — | FIFO murni, seq tanpa celah | Req 2.6–2.7, 6.1, 6.6 |
| — | Fail closed saat Valkey down | Req 2.13 |

---

## Build & Verify

### Build (jalankan)

```bash
# Dari root repo: infra + kunci token lokal
make infra keys

cd services/queue
cp .env.example .env
go run ./cmd migrate up        # terapkan 0001_init, 0002_account_ref
go run ./cmd api &             # HTTP di :8081
go run ./cmd worker &          # Promotor / Status Publisher / Claim Sweeper (saat dibangun)
```

### Verify (pemeriksaan)

```bash
curl -fsS localhost:8081/health            # 200 UP (implemented)
cd services/queue
go build ./...                             # kompilasi
go test ./... -race                        # unit test (race)
make test-integration                      # Valkey/PG/RabbitMQ nyata (build tag integration)
make lint                                  # golangci-lint v2 + depguard (lapisan hexagonal)
make cover                                 # ambang coverage logika 80% (CI case-coverage)
make mock                                  # regen _mock/ bila port berubah
```

### Verifikasi fitur (saat dibangun)

| Target | TC | Cara |
|--------|----|----|
| Atomisitas `join.lua` + constraint NIK/visitor/email | TC-GST-03..05, TC-QUE-07 | Integration (Valkey nyata) |
| 10.000 join paralel tanpa celah seq, urutan admission = seq | TC-QUE-08, TC-QUE-14 | Integration + k6 |
| FIFO lintas promosi | TC-QUE-14 | Integration |
| Claim deadline → MISSED | TC-QUE-09 | godog |
| Consumer idempoten kirim-ganda | TC-QUE-05 | Integration (consumer) |
| Zona Dijamin/Peluang + `ceil` sisa < 3 | TC-QUE-10, TC-QUE-11 | Unit + integration |
| Status Document flat load | TC-PERF-01, TC-PERF-03 | k6 |
| Sold-out notification ≤ 5s | TC-PERF-05 | k6 |
