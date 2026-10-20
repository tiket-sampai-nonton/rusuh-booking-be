# Design — Booking Service

| Field | Value |
|-------|-------|
| **Spec** | booking-service |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-BOOKING-001.md`, `docs/adr/05-ADR-RBT.md` (ADR-019 event sourcing) |
| **Service** | `services/booking` (port 8082, schema `booking`, role `svc_booking`) |
| **Arsitektur** | Hexagonal (ports & adapters), event sourcing untuk agregat Booking |

## 1. Ringkasan & Status

Booking Service adalah otoritas tunggal stok tiket dan uang. Desain ini menggabungkan **hexagonal
architecture** (port inbound/outbound + adapter) dengan **event sourcing** (ADR-019): siklus booking
adalah log event append-only; read model `bookings` diproyeksikan sinkron; stok tetap relasional dan
diubah dalam transaksi yang sama. Dengan begitu invarian "tidak oversell" tidak bergantung pada replay.

**Status implementasi (audit 2026-10-19):**
- ✅ **Terimplementasi:** skema DB penuh + trigger invarian (`deployments/migrations/0001_init.up.sql`,
  `0002_event_store.up.sql`), CLI (`api`/`worker`/`migrate`/`healthcheck`), config fail-fast, `GET /health`.
- ❌ **Boilerplate / belum dibangun:** seluruh domain bisnis (katalog, sesi, hold, settlement, tiket,
  SSE), usecase, adapter PostgreSQL/RabbitMQ/HTTP domain, serta worker (`delivery/worker.go:jobs()` → `nil`).

Desain ini menjadi **target**; bagian yang belum ada ditandai *(belum)*.

## 2. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` dan SDD §4. Lapisan ditegakkan `depguard`; antar-service dicegah Go `internal/`.

```
                         ┌───────────────────── inbound adapters ─────────────────────┐
  HTTP (net/http)  ──►   │  infrastructure/http   (handler, Register(mux), DTO)        │
  RabbitMQ consumer ──►  │  infrastructure/rabbitmq (payment.succeeded / .failed)      │
  Worker ticker    ──►   │  infrastructure/worker  (sweeper, outbox relay)             │
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/inbound (I<Usecase>)
                         ┌───────────────────── application/usecase ──────────────────┐
                         │  satu usecase per file; bergantung pada port/outbound       │
                         │  (CreateSession, HoldBooking, EditBooking, Settle, Expire…) │
                         └───────────────┬─────────────────────────────────────────────┘
                                         ▼ port/outbound (I<Repo/Gateway/UoW>)
                         ┌───────────────────── outbound adapters ────────────────────┐
  PostgreSQL (pgx) ◄──   │  infrastructure/postgres (event store, repos, UoW)          │
  RabbitMQ (amqp)  ◄──   │  infrastructure/rabbitmq (publisher via outbox)             │
  Payment (HTTP)   ◄──   │  infrastructure/paymentclient (resolve)                     │
  Valkey           ◄──   │  infrastructure/valkey (SSE availability cache)             │
                         └─────────────────────────────────────────────────────────────┘
```

**Aturan (code-principles.md):**
- *Accept interfaces, return structs.* Konstruktor mengembalikan tipe konkret; usecase menerima port.
- Interface berawalan `I`, hanya didefinisikan di `port/` (atau `pkg/`).
- Satu usecase per file; perilaku baru lewat adapter baru.
- Handler tidak pernah mengembalikan entity — selalu DTO lewat `domain/mapper`.
- Logika Lua hanya di Queue (antrean); seluruh aturan bisnis stok/uang di Go + PostgreSQL.

### 2.1 Struktur paket (target)

```
internal/booking/
  port/inbound/      IEventCatalogUsecase, ISessionUsecase, IHoldUsecase, IEditUsecase,
                     ISettlementUsecase, IExpiryUsecase, ITicketUsecase, IBookingHistoryUsecase
  port/outbound/     IEventStore, IBookingRepo, IStockRepo (UoW), IOutboxWriter, IPaymentClient,
                     IAvailabilityCache, IQuotaRepo, IAdmissionRepo
  domain/entity/     Event, Category, Admission, Booking, BookingItem, Ticket, aturan state machine
  domain/valueobject NIK (hash/mask), Money (minor), TTL, Version
  domain/dto/        request/response publik
  domain/mapper/     entity ↔ DTO
  application/usecase satu file per usecase
  infrastructure/http      handler + Register(mux)
  infrastructure/postgres  event store + repo + IUnitOfWork (pgx tx)
  infrastructure/rabbitmq  consumer + publisher (relay outbox)
  infrastructure/valkey    cache ketersediaan untuk SSE
  infrastructure/healthcheck  ✅ sudah ada
```

## 3. Desain Event Sourcing (ADR-019)

### 3.1 Prinsip

`booking_events` adalah **sumber kebenaran** siklus booking. Tabel `bookings`/`booking_items`/`tickets`
adalah **read model** yang diproyeksikan **sinkron di transaksi yang sama**. Stok
(`category_inventories`, `event_supply`, `buyer_quotas`) tetap relasional dan diubah di transaksi yang
sama — sehingga invarian "tidak oversell" ditegakkan oleh CHECK DB, bukan oleh replay event.

### 3.2 Satu transaksi: append + proyeksi + stok + outbox

Setiap command yang mengubah state booking berjalan lewat `IUnitOfWork`/`IBookingTx` dengan **urutan
lock tetap** (code-principles.md) dan melakukan empat hal dalam **satu** transaksi PostgreSQL:

```
BEGIN
  1) append event      → INSERT booking_events (stream_id, version=expected+1, event_type, payload)
                          UNIQUE(stream_id, version) menjaga optimistic concurrency
  2) proyeksi read model→ UPSERT bookings / booking_items / tickets (bookings.version := version)
  3) perubahan stok     → UPDATE category_inventories/event_supply/buyer_quotas (CHECK anti-oversell)
  4) outbox             → INSERT outbox_events (hanya untuk perubahan stok atau uang)
COMMIT
```

Jika salah satu gagal, seluruh transaksi di-rollback — tidak ada partial write, tidak ada oversell.

### 3.3 ErrConcurrency → baca ulang + ulangi

Append memakai `expected_version`. Bila dua command bersaing pada stream yang sama, INSERT kedua
melanggar `UNIQUE(stream_id, version)` dan PostgreSQL mengembalikan SQLSTATE `23505`. Adapter memetakan
ini ke `ErrConcurrency`. Usecase menanganinya dengan **re-read + retry** (bukan menimpa):

```go
const maxRetries = 3
for attempt := 0; attempt < maxRetries; attempt++ {
    state, ver, err := store.Load(ctx, streamID) // baca ulang versi terakhir
    if err != nil { return err }
    newState, event := decide(state, cmd)        // keputusan domain murni
    err = uow.Do(ctx, func(tx Tx) error {
        return tx.Append(streamID, ver+1, event) // + proyeksi + stok + outbox
    })
    if errors.Is(err, ErrConcurrency) { continue } // stream berubah → ulang
    return err
}
return ErrConcurrencyExhausted
```

### 3.4 Privasi payload event

Payload `booking_events` **tidak boleh** memuat NIK plaintext, email, atau token — hanya `*_nik_hash`
(HMAC) dan `*_nik_masked`. Aturan ini ditegakkan `pkg/eventstore` (menolak payload ber-PII) dan oleh
desain tabel (kolom plaintext tidak masuk payload). NIK pemegang disimpan terenkripsi AES-256-GCM
(`attendee_nik_enc`) + hash HMAC pada `booking_items`, bukan di event store.

### 3.5 Tipe event & append-only

Tipe event: `BookingHeld`, `BookingItemsReplaced`, `BookingLocked`, `PaymentVerifying`, `BookingPaid`,
`BookingExpired`, `BookingCancelled`, `BookingReview`. Trigger `trg_booking_events_append_only` menolak
`UPDATE`/`DELETE`, dan `trg_booking_events_no_truncate` menolak `TRUNCATE` — jejak audit tak dapat diubah.

## 4. Model Data (otoritatif: migrasi)

> Penamaan di bawah mengikuti **kode migrasi** (lebih kaya dari prosa SDD §6).

| Tabel | Peran | Invarian kunci (CHECK/trigger) |
|-------|-------|--------------------------------|
| `events` | katalog + konfigurasi | aturan terkunci saat `AVAILABLE` (`rules_locked_at`, trigger `guard_event_rules`); `config_audit` otomatis |
| `ticket_categories` | kategori + `price_minor` | `UNIQUE(event_id, code)`, `UNIQUE(event_id, id)` |
| `category_inventories` | stok per kategori | `held_quantity + sold_quantity <= capacity` |
| `event_supply` | stok tingkat event | `allocated + held + sold <= total_capacity`; `version` (optimistic) |
| `admissions` | alokasi + sesi | 1 aktif per `(event,visitor)` & `(event,buyer_nik_hash)`; snapshot `cfg_*`; state machine via CHECK |
| `attendee_checks` | dedupe cek NIK | PK `(admission_id, attendee_nik_hash)` |
| `buyer_quotas` | kuota pembeli | `held + paid <= max_quantity (≤3)` |
| `bookings` | read model booking | status ∈ {PENDING_PAYMENT,VERIFYING,PAID,EXPIRED,CANCELLED,REVIEW}; `version` = versi event terakhir |
| `booking_items` | 1 baris/tiket | `uq_attendee_nik_per_event` (HELD/PAID), `uq_booking_item_nik_active` |
| `tickets` | e-tiket | `public_code` unik, 1 per `booking_item` |
| `booking_events` | **event store** | `PK(stream_id, version)`, append-only, `payload` tanpa PII |
| `outbox_events` | outbox | index parsial `published_at IS NULL` |
| `idempotency_records` | dedupe request | PK `(scope, owner_id, idempotency_key)`, `state` IN_PROGRESS/COMPLETED |

**Supply = Σ kategori:** constraint trigger deferred (`check_supply_matches_categories`) memastikan
`event_supply.total_capacity = Σ category_inventories.capacity` saat COMMIT — dasar jaminan "masuk pasti dapat tiket".

## 5. State Machine

**Admission:** `RESERVED → ACTIVE → EDITABLE → CONVERTED`; cabang akhir `EXPIRED`/`RELEASED`
(`end_reason` ∈ UNCLAIMED/SESSION_EXPIRED/LEFT_WITHOUT_BUYING/CONVERTED). Ditegakkan CHECK:
`(status IN RESERVED/ACTIVE/EDITABLE) = (ended_at IS NULL)` dan `(status=CONVERTED) = (lock_reason IS NOT NULL)`.

**Booking:** `PENDING_PAYMENT → VERIFYING → PAID`; cabang `EXPIRED`/`CANCELLED`/`REVIEW`.
Transisi hanya lewat append event + proyeksi.

## 6. Alur Utama

### 6.1 Admission (reservasi, Jangan PHP)
```
Queue ─POST /internal/v1/allocations:batch─► Booking
  tx: event_supply.allocated += units; INSERT admissions(RESERVED, cfg snapshot)
  idempoten per queue_entry_id (UNIQUE). → token admission (one-time 60s) dikembalikan ke Queue
```

### 6.2 Buka sesi & hold
```
Client ─POST /booking-sessions (Bearer admissionToken)─► admission RESERVED→ACTIVE, balas expiresAt+serverTime
Client ─POST /bookings (Idempotency-Key)─► usecase Hold:
  validasi NIK + kuota + max per order
  tx: append BookingHeld (v1) + proyeksi bookings(PENDING_PAYMENT) + items(HELD)
      + category.held += n + supply.held += n + buyer_quotas.held += n + outbox
  CHECK DB menolak jika melebihi capacity → 409 CATEGORY_SOLD_OUT / NO_SUPPLY
```

### 6.3 Settlement (dari Payment)
```
RabbitMQ payment.succeeded ─► consumer (idempoten: guard status != PAID)
  tx: append BookingPaid + proyeksi bookings(PAID) + INSERT tickets
      + held→sold (kategori, supply, buyer_quotas) + outbox(booking.paid → Notification)
```

### 6.4 Expiry sweeper (worker, 10s)
```
bookings PENDING_PAYMENT/VERIFYING & payment_expires_at < now:
  jika terkunci → POST /internal/v1/payments/{id}/resolve (ke Payment)
    tidak ada bayar → append BookingExpired + held→free + outbox(booking.expired)
    ada bayar       → append BookingReview + status REVIEW + outbox(booking.review)
```

### 6.5 Outbox relay (worker, 1s)
```
SELECT outbox_events WHERE published_at IS NULL ORDER BY available_at
  publish ke RabbitMQ (exchange topic rusuh.events) → SET published_at
  gagal → publish_attempts++, last_error, retry siklus berikut
```

## 7. Peta Error (domain → HTTP via `pkg/apperror`)

| Error domain | Code | HTTP | Pemicu |
|--------------|------|------|--------|
| ErrNotFound | `NOT_FOUND` / `BOOKING_NOT_FOUND` | 404 | resource tidak ada |
| ErrValidation | `VALIDATION_ERROR` | 400 | field tidak valid |
| ErrMaxTickets | `MAX_TICKETS_EXCEEDED` | 400 | item > maxTicketsPerOrder |
| ErrSessionExpired | `SESSION_EXPIRED` | 410 | > admissions.expires_at |
| ErrBookingExpired | `BOOKING_EXPIRED` | 410 | > payment_expires_at |
| ErrBookingLocked | `BOOKING_LOCKED` | 409 | edit setelah payment_started_at |
| ErrMaxEdits | `MAX_EDITS_EXCEEDED` | 409 | edit_count ≥ cfg_max_edits |
| ErrCategorySoldOut | `CATEGORY_SOLD_OUT` | 409 | CHECK held+sold>capacity |
| ErrNoSupply | `NO_SUPPLY` | 409 | supply habis |
| ErrNIKInvalid | `NIK_INVALID` | 422 | format NIK salah |
| ErrNIKHasTicket | `NIK_ALREADY_HAS_TICKET` | 409 | `uq_attendee_nik_per_event` |
| ErrBuyerQuota | `BUYER_QUOTA_EXCEEDED` | 409 | kuota pembeli habis |
| ErrAttendeeCheckLimit | `ATTENDEE_CHECK_LIMIT` | 429 | cek NIK > batas |
| ErrAdmissionToken | `ADMISSION_TOKEN_INVALID`/`_EXPIRED` | 403 | token salah/expired |
| ErrVisitorMismatch | `VISITOR_MISMATCH` | 403 | visitor beda |
| ErrIdempotencyConflict | `IDEMPOTENCY_CONFLICT` | 409 | key sama, body beda |
| ErrConcurrency | *(internal, retry)* | — | konflik versi event store (23505) |

Error bertipe dipetakan di `infrastructure/http`; handler selalu balas DTO, tidak pernah entity.

## 8. Konfigurasi (`internal/config`, D-24, fail-fast)

Divalidasi saat start (`Config.Validate()` — ✅ sudah ada). Variabel aktual dari `.env.example`:

| Kelompok | Variabel |
|----------|----------|
| App | `APP_ENV` (local/dev/staging/prod), `APP_NAME`, `APP_PORT=8082` |
| Log | `LOG_LEVEL`, `LOG_FORMAT` |
| HTTP/Worker | `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL` (semua > 0) |
| Postgres | `POSTGRES_HOST/PORT/USER/PASSWORD/DATABASE/SSLMODE/SCHEMA=booking/MAX_CONNS` |
| Valkey | `VALKEY_SERVER` (JSON), `VALKEY_USERNAME/PASSWORD/TLS/DB/PREFIX_ENV` |
| RabbitMQ | `RABBITMQ_URL/EXCHANGE=rusuh.events/EXCHANGE_TYPE=topic/WORKERS/PREFETCH/MAX_RETRIES/RETRY_DELAYS/CONFIRM_TIMEOUT` |

*Target tambahan (SDD §10, belum di config.go):* `PAYMENT_SERVICE_URL`, `BOOKING_TOKEN_PRIVATE_KEY_PATH`,
`TOKEN_PUBLIC_KEY_PATH`, `BOOKING_TTL_SECONDS`, `PAYMENT_TTL_SECONDS`, `MAX_TICKETS_PER_ORDER`,
`MAX_EDITS`, `SSE_ENABLED`, `SSE_CACHE_INTERVAL_MS`. Semua tetap wajib divalidasi saat start.

Secret lewat env/SSM; `.env` tidak di-commit (security-privacy.md).

## 9. Resilience (consumer & retry)

Mengikuti `code-principles.md` dan standar resilience:
- Consumer `payment.succeeded` **idempoten tanpa tabel inbox** — guard state (`status != PAID`,
  `ON CONFLICT DO NOTHING` pada proyeksi/tiket). Uji kirim ganda wajib.
- **Retry berjenjang** `RABBITMQ_RETRY_DELAYS=5s,30s,2m`; **DLQ** setelah `RABBITMQ_MAX_RETRIES`.
- Payload rusak → `rabbitmq.Permanent(err)` (langsung DLQ, tidak retry).
- Kegagalan notifikasi tidak membatalkan transaksi (invarian 6) — booking.paid/expired lewat outbox.

## 10. Observability (SDD §11)

**Metrik:** `rbt_booking_sessions_total{event_id,status}`, `rbt_booking_holds_total{event_id,status}`,
`rbt_booking_settlements_total{event_id}`, `rbt_booking_expirations_total{event_id,reason}`,
`rbt_booking_stock_gauge{event_id,type}`, `rbt_booking_event_store_version{aggregate_id}`.

**Alert:** `BookingStockNegative` (counter<0, Critical), `BookingExpiryRateHigh` (>20%/10m, Warning),
`BookingOutboxLag` (>30s/5m, Warning), `BookingDBLatencyHigh` (p99>100ms/5m, Warning).

**Health:** `GET /health` → 200 UP / 503 DOWN (ALB + container HEALTHCHECK). ✅ Terimplementasi.
Log terstruktur (slog JSON); jangan pernah log NIK/email/token (security-privacy.md).

## 11. Strategi Testing (testing-review.md, SDD §14)

- **Unit** testify + table-driven, mock dari `_mock/` (mockgen). Setiap usecase punya test regresi (D-27).
  Fokus: keputusan domain murni (`decide`), peta error, validasi NIK (Lampiran A.1).
- **Integration** build tag `integration` dengan PostgreSQL/Valkey/RabbitMQ nyata (`make test-integration`):
  - TC-BKG-07 — 100 hold paralel pada 50 tiket terakhir → **tepat 50 sukses** (no oversell).
  - TC-ES-01..05 — event store append-only, replay, konflik versi → ErrConcurrency + retry.
  - TC-ADM-01/02 — reservasi + jaminan Jangan PHP. TC-BKG-01..03 — kuota & NIK unik.
  - TC-CFG-01 — perubahan config tidak memengaruhi sesi berjalan (snapshot).
  - TC-PAY-03/04/06..08 — settlement idempoten, expiry + resolve/REVIEW.
  - Consumer kirim ganda — idempotensi guard state.
- **Contract** TC-API-01/02 terhadap `docs/contracts/openapi/booking.yaml` + `internal-booking.yaml`.
- **Coverage** logika ≥ 80% (`make cover`); `cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan.
- `make ci` wajib lolos sebelum PR; setiap SRS-F punya TC di `docs/test/06-TP-RBT-001.md`.

## 12. Build (run) & Verify

**Run (lokal):**
```bash
make infra keys                       # PostgreSQL, Valkey, RabbitMQ, kunci token
cd services/booking && cp .env.example .env
go run ./cmd migrate up               # terapkan migrasi (embed, advisory lock)
go run ./cmd api &                    # HTTP :8082
go run ./cmd worker &                 # sweeper + outbox relay (saat dibangun)
curl localhost:8082/health            # 200 UP
```

**Verify:**
```bash
make build          # compile semua modul
make test           # unit test -race
make test-integration  # + PostgreSQL/Valkey/RabbitMQ (tag integration)
make cover          # ambang coverage logika 80%
make lint           # golangci-lint v2 + depguard (penegak lapisan)
make isolation      # build GOWORK=off (tanpa go.work)
make ci             # seluruh gerbang CI lokal
```
Gerbang CI: `code-quality` (gofmt/mod tidy, mock sinkron, golangci-lint, go vet, gosec, govulncheck,
hadolint, gitleaks) → `verify-code-quality` → `case-coverage` (unit+integration -race, coverage 80%) →
`code-rank` (SonarCloud) → `build`/isolation → `package`/`scan-package`.

## 13. Referensi

- LLD: `docs/sdd/SDD-BOOKING-001.md` · ADR-019 event sourcing: `docs/adr/05-ADR-RBT.md`
- Kontrak: `docs/contracts/openapi/booking.yaml`, `internal-booking.yaml`
- HLD §4.4.3 (Selection Session), §4.4.4 (Hold & Settlement)
- Steering: `.kiro/steering/{structure,code-principles,tech,security-privacy,invariants,testing-review}.md`
- Util lintas service: `pkg/{eventstore,apperror,httpx,postgres,valkey,rabbitmq,migrate,token,crypto}`
