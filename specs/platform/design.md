# Design — Platform (lintas service)

| Field | Value |
|-------|-------|
| **Spec** | platform |
| **Sumber** | SDD-RBT-001 §4.1.3 (struktur repo), §4.3 (dependency), §4.5.2 (outbox), §4.5.3 (event sourcing), §4.6.2 (messaging, retry, breaker), §4.9 (NIK) · HLD Deployment & Security view · ADR-010/011/012/019/022 |
| **Kode acuan** | `pkg/*`, `.github/workflows/ci.yml`, `.golangci.yml`, `Makefile`, `docker-compose.yaml`, `tools/*.sh` |

## Overview

Platform adalah satu Go module (`pkg/go.mod`) berisi utilitas tanpa logika bisnis, ditambah artefak
repo (kontrak, CI, deployment, test lintas service). Service bergantung pada `pkg/` lewat `go.work`
lokal dan versi module saat build isolation. Desain ini menambah paket yang direncanakan SDD §4.1.3
(`nik`, `outbox`, `telemetry`) dan melengkapi `httpx`/`logger`, tanpa mengubah API paket yang sudah ada.

## Architecture

```
services/<svc>/internal/delivery (composition root)
        │ wires
        ▼
┌─────────────────────────────── pkg/ (module bersama) ──────────────────────────────┐
│ httpx ── server, middleware(RequestID, Recover ✅ | Auth, RateLimit, ClientIP, Metrics ⏳) │
│ apperror ✅  logger ✅(+redaksi ⏳)  dotenv ✅  clock ✅                                  │
│ token ✅ (Signer/Verifier EdDSA, kid)   crypto ✅ (HMAC, AES-GCM, mask)   nik ⏳            │
│ postgres ✅ (pool, WithTx, SQLSTATE)   valkey ✅   migrate ✅ (embed, advisory lock)        │
│ rabbitmq ✅ (publisher confirm, consumer, retry, DLQ, envelope)   outbox ⏳ (Write + Relay)│
│ eventstore ✅ (Append/Load/Replay, tolak PII)   resilience ✅ (Breaker, Bulkhead)          │
│ telemetry ⏳ (registry, HTTP metrics, breaker metrics)                                      │
└────────────────────────────────────────────────────────────────────────────────────────┘
✅ ada [kode]   ⏳ direncanakan (SDD §4.1.3) — dibangun lewat tasks.md
```

Aturan dependency (SDD §4.3, `.golangci.yml`): `pkg/*` tidak boleh mengimpor `services/*`; domain
service hanya std-lib; adapter `infrastructure/*` boleh memakai `pkg/*`.

## Components and Interfaces

### 1. `pkg/nik` (Requirement 1)

```go
package nik

var (
    ErrLength   = errors.New("NIK_INVALID_LENGTH")
    ErrDigits   = errors.New("NIK_INVALID_CHARACTERS")
    ErrProvince = errors.New("NIK_INVALID_REGION")
    ErrBirth    = errors.New("NIK_INVALID_BIRTH_DATE")
    ErrSerial   = errors.New("NIK_INVALID_SERIAL")
)

// Validate memeriksa struktur NIK; tidak membuktikan NIK terdaftar (tanpa Dukcapil).
func Validate(s string, now time.Time) error
```

Algoritma persis seperti SDD-RBT §4.9 (provinsi: 11–19, 21, 31–36, 51–53, 61–65, 71–76, 81–82,
91–97). `now` di-inject (pakai `pkg/clock`) agar uji "tanggal di masa depan" deterministik.

**Catatan migrasi:** `services/user/internal/user/domain/valueobject/nik.go` saat ini memakai aturan
lebih longgar (provinsi rentang 11–94, tanpa cek kab/kec `00`, tanpa validasi tanggal kalender)
[kode]. `depguard` melarang `domain/**` mengimpor `pkg/`, tetapi `application/**` boleh (contoh:
`usecase/auth.go` memakai `pkg/apperror`) [kode: `.golangci.yml`]. Karena itu usecase memanggil
`nik.Validate` sebelum membangun value object; VO tetap memeriksa panjang/digit sebagai pertahanan
kedua. Ini menghindari duplikasi daftar provinsi di tiga service. *(Keputusan desain level platform;
tidak mengubah aturan bisnis.)*

### 2. Middleware HTTP (Requirement 2)

| Middleware | Input | Output ke context | Gagal |
|------------|-------|-------------------|-------|
| `VisitorAuth(verifier)` | cookie `rb_visitor` → fallback header `X-Visitor-Token` | `Visitor{ID, BuyerNikHash, Email}` | `401 UNAUTHORIZED` |
| `UserAuth(verifier)` (opsional per route) | `Authorization: Bearer` | `User{AccountID, HasNIK, NIKHash}` | `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer` |
| `ServiceAuth(verifier, allowedIssuers)` | `Authorization: Bearer` di `/internal/v1/*` | `Caller{Service}` | `401 UNAUTHORIZED` |
| `RateLimit(store, keyFn, limit, window)` | key dari IP/email/visitor | — | `429 RATE_LIMITED` + `Retry-After` |
| `ClientIP(trustProxy)` | `RemoteAddr` / `X-Forwarded-For` | `ClientIP` | — |
| `Metrics(registry)` | route template | — | — |

Middleware hanya menerima interface kecil (`ITokenVerifier`, `IRateStore`) yang didefinisikan di
`pkg/httpx`, sesuai aturan "interface hanya di `port/` atau `pkg/`". Admission token **tidak**
diverifikasi middleware — ia body `CreateSessionRequest.admissionToken` dan diverifikasi usecase
Booking (sekali pakai lewat `admissions.admission_jti UNIQUE`).

Service token: `ServiceTokenSource{signer, iss, aud, ttl=5m}` menyimpan token terakhir dan menandatangani
ulang saat sisa umur < 30 s. Klien HTTP internal menambahkan `Authorization` dan `X-Request-ID`.

### 3. `pkg/outbox` (Requirement 3)

```go
type Message struct { RoutingKey, AggregateID string; SchemaVersion int; Payload any }

// Write menyisipkan satu baris ke <schema>.outbox_events memakai tx pemanggil.
func Write(ctx context.Context, tx postgres.Tx, m Message) error

type Relay struct { DB *pgxpool.Pool; Publisher *rabbitmq.Publisher; Producer string; Batch int; Clock clock.Clock }
// RunOnce dipanggil sebagai Job di loop worker (internal/delivery/worker.go).
func (r *Relay) RunOnce(ctx context.Context) error
```

```
RunOnce:
  BEGIN
  SELECT id, routing_key, aggregate_id, schema_version, payload, occurred_at
    FROM outbox_events
   WHERE published_at IS NULL AND available_at <= now()
   ORDER BY available_at
   FOR UPDATE SKIP LOCKED LIMIT $batch            -- idx_*_outbox_pending (parsial)
  for each row:
     env = Envelope{messageId=row.id, eventType=routing_key tanpa ".v1", schemaVersion,
                    occurredAt, producer, aggregateId, payload}
     err = publisher.Publish(ctx, row.routing_key, env)       -- confirm
     ok  → UPDATE … SET published_at = now()
     err → UPDATE … SET publish_attempts = publish_attempts+1, last_error = left(err,500),
                        available_at = now() + backoff(publish_attempts) ± jitter
  COMMIT
```

Kolom `outbox_events` di Booking dan Payment identik [kode: migrasi `0001_init`], sehingga satu
implementasi melayani keduanya. `messageId = outbox_events.id` memberi consumer hilir kunci dedup
stabil (Notification `deliveries.source_message_id`).

### 4. `pkg/telemetry` & redaksi log (Requirement 4)

- Registry dan handler metrik dipasang di composition root (`/metrics` di port yang sama atau port ops
  terpisah — diputuskan bersama ADR library, OQ-5). Edge **tidak** merutekan endpoint metrik.
- Konvensi nama: `rbt_<service>_<nama>_<unit>`; metrik service didefinisikan di design service.
- `pkg/logger`: `slog.HandlerOptions.ReplaceAttr` meredaksi kunci sensitif (case-insensitive,
  termasuk nested group) menjadi `"[REDACTED]"`; test memastikan NIK 16 digit dan email tidak muncul.

### 5. Kontrak (Requirement 5)

| Artefak | Lokasi | Validasi |
|---------|--------|----------|
| OpenAPI 3.1 (7 file) | `docs/contracts/openapi/` | kin-openapi load + lint (evidence v1.5 sudah memakai kin-openapi) |
| JSON Schema event (10 + envelope) | `docs/contracts/events/` | kompilasi schema 2020-12 + contoh payload |
| Breaking-change check | PR diff | bandingkan dengan `main` (path/field/enum dihapus → gagal) |
| Contract test runtime | `tests/contract/` | respons service vs schema (TC-API-01/02) |

Penambahan yang dibutuhkan (OQ-4): `otp.requested.v1.schema.json` (`emailEnc`, `codeEnc`, `expiresAt`,
`locale?`; `additionalProperties:false`) dan `producer` enum + `user-service`.

### 6. Kunci (Requirement 6)

| Penerbit | Token | Verifikator | File kunci lokal (`make keys`) |
|----------|-------|-------------|--------------------------------|
| User | access token | User, Queue, Booking | ada [kode] |
| Queue | visitor, queue, admission | Queue, Booking, Payment | ⏳ |
| Payment | payment page token | Payment | ⏳ |
| Booking | ticket link token | Booking | ⏳ |
| Queue, Booking, Payment | service token | penerima | ⏳ |

Rotasi: publish kunci publik baru dengan `kid` baru ke verifikator → tukar kunci privat penerbit →
hapus `kid` lama setelah TTL token terpanjang lewat.

### 7. Lingkungan lokal & edge (Requirement 7)

`deployments/edge/nginx.conf` (layanan `edge` di compose, port publik tunggal):

| Path | Upstream | Cache |
|------|----------|-------|
| `/api/v1/events/*/queue-entries`, `/api/v1/queue-*` | queue-api:8081 | `queue-status` 1 s |
| `/api/v1/events`, `/api/v1/events/{id}` | booking-api:8082 | 2 s, cache key termasuk `timeframe,city,status,sort,page,size` |
| `/api/v1/booking-sessions/*/stream` | booking-api:8082 | **off**, `proxy_buffering off` |
| `/api/v1/booking-sessions*`, `/api/v1/bookings*`, `/api/v1/me/bookings` | booking-api:8082 | off |
| `/api/v1/payments*`, `/api/v1/payment-page-sessions`, `/api/v1/webhooks/*`, `/api/v1/local-simulator/*` | payment-api:8083 | off |
| `/api/v1/push-subscriptions*` | notification-api:8084 | off |
| `/api/v1/auth/*`, `/api/v1/me` | user-api:8085 | off |
| `/internal/*`, `/metrics` | — | **403** |

Konflik path: `/api/v1/me` (User) vs `/api/v1/me/bookings` (Booking) diselesaikan dengan prefix
terpanjang (`location ^~ /api/v1/me/bookings`).

### 8. CI (Requirement 8)

Stage yang ada [kode]: `changes → code-quality → secret-detection → verify-code-quality → case-coverage →
code-rank → build → package → scan-package → scan-package-verify → publish → deployment-dev/prod → ci-ok`.
Tambahan: job `contracts` (R5.1–5.2) dan `docs-diagrams` (render Mermaid, R8.2) yang dipicu
`tools/detect-changes.sh` bila `docs/` atau `specs/` berubah, dan menjadi syarat `ci-ok`.

### 9. Tooling operasional (Requirement 9)

- `tools/reconcile`: CLI Cobra; tiap service mengekspos pemeriksaan read-only sebagai query SQL di
  schema miliknya (dijalankan dengan role service itu) + pemeriksaan lintas service lewat endpoint
  internal; output metrik `rbt_reconcile_violations{check}` dan laporan JSON. `--apply` hanya untuk
  perbaikan yang punya definisi dry-run, mencatat aktor (`app.actor`) dan diff.
- `tools/redrive`: memindahkan pesan `<queue>.dlq` → `<queue>` dengan batas jumlah, mencatat aktor dan
  `messageId` ke log audit (tanpa payload).

### 10. Test lintas service (Requirement 10) & cloud (Requirement 11)

`tests/features/*.feature` (godog) memakai compose + simulator; `tests/k6/*.js` memakai staging atau
compose dengan profil load; evidence ke `docs/evidence/YYYY-MM-DD-<topik>.md`. Terraform mengikuti
modul pada SDD §4.1.3; state remote tidak di-commit (SRS-C-06).

## Data Models

Platform tidak memiliki schema sendiri. Tabel yang disentuh paket bersama (milik schema service):

| Tabel | Paket | Service |
|-------|-------|---------|
| `outbox_events` | `pkg/outbox` | booking, payment |
| `booking_events`, `payment_events` | `pkg/eventstore` ✅ | booking, payment |
| `schema_migrations` (runner) | `pkg/migrate` ✅ | semua |

## Error Handling

| Kondisi | Perilaku |
|---------|----------|
| Token tidak valid / kedaluwarsa | `401 UNAUTHORIZED` (envelope standar), tidak membocorkan alasan detail |
| Rate limit | `429 RATE_LIMITED` + `Retry-After` |
| Valkey (rate store) tidak tersedia | **fail closed** untuk endpoint join/claim (Queue memutuskan), fail open hanya bila route mendeklarasikannya |
| Broker tidak tersedia saat relay | baris tetap pending, backoff; tidak ada error ke klien |
| Payload event melanggar schema saat publish | tidak dipublish, `last_error` diisi, alert; bug produsen, bukan retry tanpa batas |

## Correctness Properties

### Property 1: NIK validator determinism
Untuk setiap string `s` dan waktu `now`, `nik.Validate(s, now)` selalu mengembalikan hasil yang sama, dan NIK yang lolos selalu 16 digit dengan tanggal lahir valid ≤ `now`.
**Validates: Requirements 1.1, 1.2, 1.5**

### Property 2: No lost outbox event
Untuk setiap baris `outbox_events` yang di-commit, pada akhirnya baris itu `published_at IS NOT NULL` setelah broker pulih, dan tidak ada baris yang dihapus tanpa terbit.
**Validates: Requirements 3.2, 3.3, 3.4**

### Property 3: Outbox single publisher per cycle
Untuk dua relay yang berjalan bersamaan, satu baris tidak dipublish keduanya dalam siklus yang sama (`SKIP LOCKED`).
**Validates: Requirement 3.5**

### Property 4: Internal API closed
Untuk setiap request ke `/internal/v1/*` tanpa service token valid, respons selalu `401`, dan edge tidak pernah meneruskan `/internal/*`.
**Validates: Requirements 2.3, 7.2, 11.2**

### Property 5: Same auth for web and mobile
Untuk token visitor yang sama, middleware menghasilkan identitas context yang sama baik dari cookie maupun header.
**Validates: Requirement 2.1**

### Property 6: PII never logged
Untuk setiap log record yang berisi atribut ber-kunci sensitif, output handler tidak memuat nilai aslinya.
**Validates: Requirements 4.3, 4.4**

### Property 7: Additive contracts
Untuk setiap PR, kontrak `v1` hasil perubahan adalah superset kompatibel dari versi `main`.
**Validates: Requirement 5.2**

## Testing Strategy

| Level | Cakupan |
|-------|---------|
| Unit (testify, table-driven) | `pkg/nik` korpus SDD §4.9; middleware (cookie vs header, token kedaluwarsa, issuer tak dikenal); service token cache; redaksi logger; backoff relay |
| Integration (`-tags integration`) | `pkg/outbox` dengan PostgreSQL + RabbitMQ nyata (publish, confirm gagal, dua relay paralel); rate limiter Valkey |
| Contract | CI job `contracts`; `tests/contract/` runtime |
| E2E / load / failure | `tests/features`, `tests/k6`, skenario TC-CAP-01..03, evidence |
