# Design — Payment Service

| Field | Value |
|-------|-------|
| **Spec** | payment-service |
| **Sumber desain** | SDD-RBT-001 §4.4 (DDL), §4.5.2–4.5.3, **§4.6.2 (breaker, retry ownership)**, **§4.6.3 (aturan transaksi Midtrans, pemetaan status, resolve)**, §4.7.2 (diagram webhook); `docs/analisa/04c-SDD-PAYMENT-001.md`; ADR-007, ADR-010, ADR-011, ADR-019 |
| **Kontrak** | `docs/contracts/openapi/payment.yaml`, `internal-payment.yaml`, `internal-booking.yaml` (dipanggil), `events/{payment.succeeded,payment.failed,booking.review}.v1.schema.json` |
| **Arsitektur** | Hexagonal (ports & adapters) + **Event Sourcing** (ADR-019) |
| **Port** | 8083 · **Schema/Role** `payment`/`svc_payment` |
| **Status implementasi** | Boilerplate — hanya slice health; sisanya TARGET |

> **Grounding:** data model mengikuti migrasi `0001_init.up.sql` + `0002_event_store.up.sql`; API
> mengikuti `payment.yaml`. Detail API Midtrans adalah baseline desain dan **wajib diverifikasi
> terhadap dokumentasi resmi Midtrans** sebelum implementasi (SDD-RBT §4.6.3, OI-05).

---

## 1. Arsitektur Hexagonal

```
services/payment/internal/
├── config/                      [IMPL subset] (+TARGET PAYMENT_PROVIDER/MIDTRANS_*/PAYMENT_METHODS_ENABLED/…)
├── delivery/                    [IMPL] composition root (server.go, worker.go, clients.go, serve.go)
└── payment/
    ├── port/
    │   ├── inbound/   IGetPaymentPage, ICreatePageToken, IExchangePageToken, ICreateAttempt,
    │   │              IGetPaymentStatus, IHandleWebhook, IResolvePayment, IExpireAttempts,
    │   │              IOpenRefundCase, ISimulateScenario; errors.go
    │   └── outbound/  IAttemptEventStore, IAttemptReadModel, ICallbackStore, IPageTokenStore,
    │                  IRefundCaseStore, IOutboxWriter, IIdempotencyStore, IPaymentGateway,
    │                  IBookingClient, IUnitOfWork, IClock
    ├── domain/
    │   ├── entity/      PaymentAttempt (agregat event-sourced, decide()), Callback, RefundCase, PageToken
    │   ├── valueobject/ Money, Method, AttemptStatus, Provider, Instruction, OrderID
    │   ├── dto/ mapper/
    ├── application/usecase/  satu usecase per file
    └── infrastructure/
        ├── http/        handler publik + internal + simulator, Register(mux)
        ├── postgres/    event store (pkg/eventstore) + read model + callbacks + idempotency + refund
        ├── midtrans/    IPaymentGateway nyata (charge/status/cancel + verify signature) lewat breaker
        ├── simulator/   IPaymentGateway lokal (PAYMENT_PROVIDER=simulator)
        ├── bookingclient/ payable, payment-lock, deadline-extension (service token, breaker)
        ├── rabbitmq/    consumer booking.review.v1 (publish lewat pkg/outbox relay)
        └── healthcheck/ [IMPL]
```

Prinsip: accept interfaces/return structs; usecase bergantung pada `port/outbound`; perilaku baru lewat
adapter baru (simulator vs Midtrans); interface berawalan `I` hanya di `port/`; **panggilan eksternal
(Booking, Midtrans) tidak pernah di dalam transaksi DB**.

---

## 2. API Endpoints

Envelope `{meta, data}`; error `ErrorEnvelope` tanpa SQL/stack/secret/payload provider. Security:
`rb_visitor` cookie / `X-Visitor-Token` / cookie `rb_pay` (payment page) — middleware `pkg/httpx`.

### 2.1 `GET /api/v1/payments/{bookingId}/page` (R1)
- **200 `PaymentPage`:** `{ bookingId, methods[], amountMinor, paymentExpiresAt, serverTime, canEdit, editableUntil, currentAttempt }`.
- Data dari `GET /internal/v1/bookings/{id}/payable` (Booking otoritatif untuk nominal & deadline).
- `methods` = `PAYMENT_METHODS_ENABLED`; metode dengan breaker charge OPEN ditandai gangguan (SRS-F-19-02).
- **Errors:** `409 BOOKING_NOT_PAYABLE`; bukan pemilik / kedaluwarsa → OQ-19.

### 2.2 `POST /api/v1/payments/{bookingId}/page-tokens` (R2)
- **201 `PageToken`:** `{ pageToken, pageUrl: "/pay/{bookingId}?ppt=<token>&embedded=1", expiresAt }` (SRS-F-18-02).
- Simpan **hanya** `token_hash` (SHA-256), `visitor_id`, `booking_id`, `expires_at` (TTL `PAYMENT_PAGE_TOKEN_TTL_SECONDS`).
- **Errors:** `409 BOOKING_NOT_PAYABLE`.

### 2.3 `POST /api/v1/payment-page-sessions` (R2)
- **Request:** `{ pageToken }` → **204** + cookie `rb_pay` (HttpOnly, Secure, SameSite=Lax); `UPDATE … SET used_at = now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at > now()` (sekali pakai, atomik).
- **Errors:** `403 PAYMENT_PAGE_TOKEN_INVALID`.

### 2.4 `POST /api/v1/payments/{bookingId}/attempts` (R3)
- **Header:** `Idempotency-Key` (16–64). **Request:** `{ method }`.
- **201 `Attempt`:** `{ attemptNo, method, status, bookingVersion, expiresAt, instruction }`; `instruction.type ∈ {VA, QR, DEEPLINK}`.
- **Errors (kontrak):** `409 BOOKING_NOT_PAYABLE | IDEMPOTENCY_KEY_REUSED`; `422 PAYMENT_METHOD_UNAVAILABLE`; `503 PAYMENT_PROVIDER_UNAVAILABLE`. Kode lain SDD-PAYMENT (`ATTEMPT_IN_PROGRESS`, `BOOKING_EXPIRED`) → OQ-19.

### 2.5 `GET /api/v1/payments/{bookingId}/status` (R6)
- **200 `PaymentStatus`:** `{ bookingId, bookingStatus, attempt, serverTime }`; read-only.

### 2.6 `POST /api/v1/webhooks/midtrans` (R4)
- **Auth:** `signature_key` (SHA-512). Body `MidtransNotification` (`additionalProperties: true`).
- **200** setelah callback tersimpan durable; **401 `WEBHOOK_SIGNATURE_INVALID`**.

### 2.7 `POST /api/v1/local-simulator/payments/{attemptId}/scenario` (R10)
- Aktif hanya pada lingkungan yang diizinkan (OQ-20); selain itu `404 NOT_FOUND`.
- `{ scenario ∈ SETTLE|SETTLE_LATE|EXPIRE|DENY|TIMEOUT|DUPLICATE_WEBHOOK|OUT_OF_ORDER }` → **202**.

### 2.8 `POST /internal/v1/payments/{bookingId}/resolve` (R7)
- **Auth:** `serviceBearer` (ADR-011). **Request:** `{ mode ∈ {CHECK, CHECK_AND_CANCEL} }`.
- **200:** `{ outcome ∈ {SUCCEEDED, NOT_PAID, NO_ATTEMPT, UNKNOWN}, settlementTime?, attemptId? }` (§9.3).

### 2.9 `GET /health` — **[IMPL]**

### 2.10 Error Codes

| errorCode | HTTP | Endpoint | Pemicu |
|-----------|------|----------|--------|
| `BOOKING_NOT_PAYABLE` | 409 | page, page-tokens, attempts | Booking bukan `PENDING_PAYMENT` atau `payment-lock` → `BOOKING_NOT_PAYABLE`/`PAYMENT_EXPIRED` |
| `IDEMPOTENCY_KEY_REUSED` | 409 | attempts | key sama, `request_hash` beda |
| `PAYMENT_METHOD_UNAVAILABLE` | 422 | attempts | metode di luar `PAYMENT_METHODS_ENABLED` |
| `PAYMENT_PROVIDER_UNAVAILABLE` | 503 | attempts | gateway error/timeout/breaker OPEN |
| `PAYMENT_PAGE_TOKEN_INVALID` | 403 | page-sessions | token salah/kedaluwarsa/dipakai |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | webhook | signature salah/kosong |
| `UNAUTHORIZED` | 401 | internal | service token tidak valid |
| `NOT_FOUND` | 404 | simulator, (bukan pemilik — OQ-19) | |
| `DEPENDENCY_UNAVAILABLE` | 503 | page, attempts | Booking/DB tidak terjangkau |

---

## 3. Data Model (migrasi nyata)

### 3.1 `payment_attempts` (read model, 0001)
`id`, `booking_id`, `attempt_no > 0`, `method` (6 enum), `provider` (`MIDTRANS|SIMULATOR`),
`provider_order_id UNIQUE`, `provider_transaction_id`, `status` ∈ {`CREATED, PENDING, UNKNOWN,
SUCCEEDED, FAILED, CANCELLED, EXPIRED, REFUND_REQUIRED, REFUNDED`}, `amount_minor`, `currency`,
`expires_at`, `instruction jsonb`, `settlement_time`, `last_provider_status`, `stream_version` (0002).
- `UNIQUE (booking_id, attempt_no)`; CHECK settle-family ⇒ `settlement_time`.
- **`uq_attempt_open_per_booking`** WHERE `status IN ('CREATED','PENDING','UNKNOWN')` ⇒ at-most-once charge (I-2).
- `uq_attempt_provider_tx` parsial.

### 3.2 `payment_events` (event store, 0002)
PK `(stream_id, version)`; `event_type ~ '^[A-Z][A-Za-z]+$'`; `payload` tanpa data kartu/server key/token;
trigger append-only + no-truncate (I-5).

### 3.3 `payment_callbacks` — `UNIQUE (provider, fingerprint)`; `processing_result ∈ {RECEIVED, PROCESSED, DUPLICATE, REJECTED, ERROR}`.
`fingerprint = SHA-256(order_id | transaction_status | status_code | transaction_id | settlement_time)`
[desain: duplikat persis = fingerprint sama; progres status = fingerprint baru].

### 3.4 `payment_page_tokens` — `token_hash PK`, `booking_id`, `visitor_id`, `expires_at`, `used_at`.
### 3.5 `refund_cases` — `attempt_id UNIQUE` (I-8), `reason ∈ {LATE_SETTLEMENT, SETTLED_AFTER_CANCEL, DUPLICATE_PAYMENT}`, `status ∈ {OPEN, REFUNDED, CLOSED}`.
### 3.6 `idempotency_records` — PK `(scope, owner_id, idempotency_key)`; scope `create_attempt`, owner = visitorId.
### 3.7 `outbox_events` — sama dengan Booking; dipakai `pkg/outbox`.

Tanpa `consumer_inbox` (v4.6): `booking.review` idempoten lewat `refund_cases.attempt_id UNIQUE`.

---

## 4. Event Sourcing (ADR-019)

Stream = `payment_attempts.id`. Setiap transisi status = satu event; proyeksi + outbox dalam transaksi
yang sama; `ErrConcurrency` ⇒ baca ulang, ulangi.

| Event | Transisi | Dipicu oleh | Outbox |
|-------|----------|-------------|--------|
| `AttemptCreated` (`method`, `amountMinor`, `bookingVersion`, `orderId`) | ∅ → CREATED | ICreateAttempt, sebelum charge | — |
| `InstructionIssued` (`type`, `bank?`, `vaMasked?`) | CREATED/UNKNOWN → PENDING | charge OK / rekonsiliasi UNKNOWN menemukan pending | — |
| `AttemptUnknown` (`cause`) | CREATED → UNKNOWN | charge timeout/error ambigu | — |
| `SettlementReceived` (`settlementTime`, `providerTxId`) | PENDING/UNKNOWN/CANCELLED/EXPIRED → SUCCEEDED | webhook `settlement` / resolve / sweeper CHECK | `payment.succeeded.v1` |
| `AttemptFailed` (`reason`) | PENDING/UNKNOWN → FAILED | webhook `deny`/`failure` | `payment.failed.v1` |
| `AttemptCancelled` | PENDING/UNKNOWN/CREATED → CANCELLED | ganti metode, resolve `CHECK_AND_CANCEL`, webhook `cancel` | — |
| `AttemptExpired` | PENDING/UNKNOWN → EXPIRED | webhook `expire` / sweeper | — |
| `RefundRequired` (`reason`) | SUCCEEDED → REFUND_REQUIRED | consumer `booking.review` | — |

Nama mengikuti SDD-RBT §4.5.3 (`AttemptCreated`, `InstructionIssued`, `SettlementReceived`,
`AttemptFailed`, `AttemptExpired`, `RefundRequired`); `AttemptUnknown` dan `AttemptCancelled`
ditambahkan karena migrasi memuat status `UNKNOWN` dan `CANCELLED`. Penutupan refund oleh Ops cukup
mengubah `refund_cases.status` (refund dilakukan manual di dashboard Midtrans), jadi tidak ada event
`RefundCompleted` dan status attempt `REFUNDED` tidak dipakai — disederhanakan 2026-10-21. Spec lama memakai `AttemptPending`/`AttemptSettled`
— diganti agar selaras SDD.

### 4.1 State Machine
```
   ∅ ─AttemptCreated─▶ CREATED ─InstructionIssued─▶ PENDING ─SettlementReceived─▶ SUCCEEDED ─RefundRequired─▶ REFUND_REQUIRED
                          │                            │  ├─AttemptFailed────▶ FAILED
                          │ timeout ambigu             │  ├─AttemptCancelled─▶ CANCELLED ──(settle di sela cancel)──▶ SUCCEEDED
                          ▼                            │  └─AttemptExpired───▶ EXPIRED   ──(settle terlambat)───────▶ SUCCEEDED
                       UNKNOWN ──(status check)────────┘
```
`SUCCEEDED` dari `CANCELLED`/`EXPIRED` diizinkan karena gateway adalah sumber kebenaran uang (pembayaran
di sela pembatalan, SDD C.4); Booking yang memutuskan `REVIEW`. Status `SUCCEEDED`, `FAILED`,
`REFUNDED` tidak pernah turun (R4.6).

---

## 5. Midtrans Integration (SDD-RBT §4.6.3)

### 5.1 Charge (adapter `midtrans`)
- `order_id = RB-{publicRef}-{attemptNo}` (deterministik, unik) — Δ3. `Payable` di `internal-booking.yaml` belum memuat `publicRef` → **OQ-21** (tambah field additive, atau pakai `bookingId`).
- `gross_amount = amountMinor` versi terkunci (IDR tanpa desimal).
- `custom_expiry` = sisa detik sampai `paymentExpiresAt` (dibulatkan ke bawah, minimal 1 menit sesuai batas Midtrans — **verifikasi**), sehingga expiry gateway tidak melewati deadline booking (Δ5).
- Pemetaan: `GOPAY→gopay`, `QRIS→qris`, `VA_BCA/BNI/BRI→bank_transfer{bank}`, `VA_PERMATA→permata|bank_transfer`. Fallback Snap `enabled_payments=[metode]` bila Core API metode tidak tersedia di Sandbox.
- Response → `Instruction`: VA `{type:VA, bank, vaNumber}`, QRIS `{type:QR, qrString}`, GoPay `{type:DEEPLINK, deeplinkUrl}` (web: QR).

### 5.2 Webhook verification (I-1)
```
expected = SHA512(order_id + status_code + gross_amount + MIDTRANS_SERVER_KEY)   -- constant-time compare
salah/kosong → callback{signature_valid=false, REJECTED} → 401
order_id tak dikenal / gross_amount ≠ amount_minor / environment beda → callback{ERROR|REJECTED}, 200 tanpa efek
```

### 5.3 Pemetaan Status

| Midtrans | Attempt | Event | Dampak Booking |
|----------|---------|-------|----------------|
| `pending` | PENDING | `InstructionIssued` (bila belum) | tetap menunggu |
| `settlement` | SUCCEEDED | `SettlementReceived` | `payment.succeeded.v1` → PAID/REVIEW |
| `capture` + `fraud_status=accept` | SUCCEEDED | `SettlementReceived` | hanya bila kartu aktif (di luar baseline) |
| `deny`, `failure` | FAILED | `AttemptFailed` | `payment.failed.v1`, booking menunggu TTL |
| `cancel` | CANCELLED | `AttemptCancelled` | booking menunggu TTL |
| `expire` | EXPIRED | `AttemptExpired` | — |
| `refund`, `partial_refund`, `chargeback` | dicatat (`last_provider_status`) | — | alert Ops, di luar MVP |

`settlement_time` dikonversi ke UTC sesuai zona waktu yang dinyatakan Midtrans (verifikasi saat implementasi).

### 5.4 Environments

| Lingkungan | `PAYMENT_PROVIDER` | Kunci |
|------------|--------------------|-------|
| CI / offline | `simulator` | — |
| Lokal + demo | `midtrans` | Sandbox `SB-Mid-server-…` (SSM di demo) |

---

## 6. Circuit Breaker (`pkg/resilience`, K9)

`pkg/resilience.BreakerConfig` **tidak** punya default — `New` menolak `FailureThreshold < 1`,
`Cooldown ≤ 0`, `HalfOpenProbes < 1` [kode]. Nilai awal dari SDD-RBT §4.6.2 (PROPOSED), via env:

| Breaker | Timeout | Ambang | Cooldown | Probe | Bulkhead | Retry |
|---------|--------:|-------:|---------:|------:|---------:|-------|
| `midtrans_charge` | 3 s | 5 | 20 s | 1 | 20 | 0 (tanpa blind create) |
| `midtrans_status` (status + cancel) | 2 s | 5 | 15 s | 1 | 10 | maks 2 |
| `booking_internal` (payable, lock, extension) | 3 s [asumsi: SDD tidak punya kolom Payment→Booking; pakai nilai Booking→Payment] | 5 | 10 s | 1 | 20 | 0 |

`IsFailure`: error transport/5xx/timeout; 4xx bisnis dan `context.Canceled` tidak dihitung.
`OnStateChange` → `breaker_state{name}`. Breaker lokal per instance.

---

## 7. Worker Jobs

Verb `worker`, `WORKER_INTERVAL` (5 s); semua job `FOR UPDATE SKIP LOCKED`.

### 7.1 Outbox Relay — `pkg/outbox.Relay.RunOnce` (platform R3), producer `payment-service`.

### 7.2 Attempt Expiry Sweeper
```
SELECT payment_attempts WHERE status IN ('PENDING','UNKNOWN') AND expires_at < now() FOR UPDATE SKIP LOCKED
per attempt (di luar tx): status = gateway.Status(order_id)       -- breaker status
  settlement → tx: SettlementReceived + outbox payment.succeeded
  pending/unknown → gateway.Cancel → tx: AttemptExpired
  breaker OPEN → lewati, coba tick berikut
```

### 7.3 UNKNOWN Reconciler
Attempt `UNKNOWN` belum kedaluwarsa: `gateway.Status(order_id)` tiap tick → `InstructionIssued` (pending),
`SettlementReceived`, atau `AttemptFailed`/`AttemptCancelled`. Tidak pernah membuat order baru (SRS-F-14-05).

---

## 8. Event Contracts

Envelope `{ messageId, eventType, schemaVersion, occurredAt, producer = "payment-service", aggregateId,
correlationId?, payload }` (Δ7); `messageId = outbox_events.id`.

### 8.1 Published

| Routing key | Consumer queue | Payload (schema v1) |
|-------------|----------------|---------------------|
| `payment.succeeded.v1` | `booking.payment-succeeded` | `{ bookingId, attemptId, settlementTime, amountMinor, method }` |
| `payment.failed.v1` | `booking.payment-failed` | `{ bookingId, attemptId, reason }` |

### 8.2 Consumed

| Routing key | Queue | Payload | Aksi |
|-------------|-------|---------|------|
| `booking.review.v1` | `payment.booking-review` | `{ bookingId, attemptHint?, buyerEmail, amountMinor }` | pilih attempt `SUCCEEDED` booking (atau `attemptHint`); `INSERT refund_cases … ON CONFLICT (attempt_id) DO NOTHING`; append `RefundRequired` |

Penentuan `reason`: `booking.review.v1` tidak membawa alasan [kontrak]. Payment menurunkannya dari datanya:
attempt lain booking itu sudah `SUCCEEDED` lebih dulu → `DUPLICATE_PAYMENT`; attempt `CANCELLED`/`EXPIRED`
sebelum settle → `SETTLED_AFTER_CANCEL`; selain itu → `LATE_SETTLEMENT` [asumsi desain; konfirmasi Ops].
Retry berjenjang + DLQ; payload rusak → `rabbitmq.Permanent(err)`.

---

## 9. Sequence Diagrams

### 9.1 Charge Flow (Δ6)
```
Klien            Payment                                 Booking                 Gateway        DB
  │ POST attempts  │ tx0: klaim idempotency (IN_PROGRESS) ──────────────────────────────────────▶│
  │ (Idem-Key)────▶│ payment-lock {attemptKey=Idem-Key} ──▶│ (lock, sisa unit dilepas)            │
  │                │◀── OK(version, amountMinor, paymentExpiresAt)                                │
  │                │ tx1: ada attempt terbuka metode lain? → gateway.Cancel (di luar tx) → AttemptCancelled
  │                │ tx1: AttemptCreated(CREATED, order_id=RB-{publicRef}-{n}) ─────────────────▶│
  │                │ breaker.Do(charge, custom_expiry ≤ paymentExpiresAt) ─────────────▶│         │
  │                │◀── instruction ─────────────────────────────────────────────────────│         │
  │                │ tx2: InstructionIssued(PENDING) + idempotency=COMPLETED ───────────────────▶│
  │◀─ 201 Attempt ─│
```
- Timeout/error ambigu → tx2: `AttemptUnknown`; 503; reconciler (§7.3) menyelesaikan order yang sama.
- Breaker OPEN sebelum charge → `AttemptCancelled` (attempt CREATED ditutup) + `deadline-extension` (sekali, bila belum ada instruksi) → 503.
- `payment-lock` bukan OK → idempotency COMPLETED dengan 409 → `409 BOOKING_NOT_PAYABLE`.

### 9.2 Webhook Flow
```
Midtrans → Payment: POST webhook
  verify signature (constant-time) → salah: callback REJECTED, 401
  INSERT callback ON CONFLICT (provider, fingerprint) DO NOTHING → konflik: DUPLICATE, 200
  validasi order/amount/environment/transisi legal
  tx: event (§5.3) + proyeksi + outbox (bila uang) ; callback PROCESSED
  200
(worker) relay → payment.succeeded.v1 / payment.failed.v1
```

### 9.3 Resolve Flow (Δ4)
```
Booking → Payment: POST /internal/v1/payments/{bookingId}/resolve {mode}   (service token)
  attempt = terbuka atau terakhir; tidak ada → NO_ATTEMPT
  s = breaker.Do(status)   OPEN/timeout → UNKNOWN
  s = settlement → (tx SettlementReceived bila belum) → SUCCEEDED + settlementTime
  mode = CHECK → NOT_PAID (bila pending/expired/cancelled/failed)
  mode = CHECK_AND_CANCEL ∧ belum dibayar → gateway.Cancel → status ulang:
        settlement → SUCCEEDED ; cancelled/expired terkonfirmasi → tx AttemptCancelled → NOT_PAID ; tidak menjawab → UNKNOWN
```
Resolve **tidak** membuka refund case.

---

## 10. Konfigurasi

**[IMPL]** `APP_*`, `LOG_*`, `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL`, `POSTGRES_*`
(`payment`/`svc_payment`), `RABBITMQ_*`.

**[TARGET]** (+ `Validate()` fail-fast):
`PAYMENT_PROVIDER=simulator|midtrans`; `MIDTRANS_SERVER_KEY`, `MIDTRANS_CLIENT_KEY`,
`MIDTRANS_ENVIRONMENT=sandbox|production`, `MIDTRANS_BASE_URL` (sandbox: `https://api.sandbox.midtrans.com`);
breaker per tabel §6: `MIDTRANS_{CHARGE|STATUS}_{TIMEOUT|FAILURE_THRESHOLD|COOLDOWN|BULKHEAD}`;
`PAYMENT_METHODS_ENABLED=VA_BCA,VA_BNI,VA_BRI,QRIS,GOPAY` (SDD §4.10);
`PAYMENT_PAGE_TOKEN_TTL_SECONDS=60` (30–120); `BOOKING_SERVICE_URL`; `TOKEN_PUBLIC_KEY_FILE` = kunci publik Queue untuk verifikasi visitor (publik
Queue), kunci privat service token. Spec lama: `MIDTRANS_CB_FAILURE_THRESHOLD=3`, `…RECOVERY_TIMEOUT=60s`
— diganti (K9).

---

## 11. Observability

Metrik: `rbt_payment_attempts_total{method,status}`, `rbt_payment_webhooks_total{status,result}`,
`rbt_payment_settlements_total{method}`, `rbt_payment_midtrans_latency_seconds{operation}`,
`breaker_state{name}`. Alert: `PaymentCircuitOpen` (OPEN 2m, Critical), `PaymentWebhookErrorRate`
(>5%/5m), `PaymentSettlementLag` (>5m), `PaymentRefundCasesOpen` (>10). Log tanpa NIK/email/server
key/token/payload provider mentah (I-7).

---

## 12. Correctness Properties

### Property 1: At most one open attempt
Untuk setiap booking dan setiap urutan request bersamaan, paling banyak satu attempt berstatus `CREATED`/`PENDING`/`UNKNOWN`.
**Validates: Requirements 3.3, 3.6, 3.10**

### Property 2: No charge before lock
Untuk setiap panggilan charge ke gateway, ada `payment-lock` `OK` sebelumnya untuk booking itu dan `gross_amount` = `amountMinor` yang dikembalikan lock.
**Validates: Requirements 3.1, 3.2**

### Property 3: Signature is the only proof
Untuk setiap transisi ke `SUCCEEDED`, ada callback `signature_valid = true` atau hasil status check gateway; tidak ada endpoint klien yang menghasilkan `SUCCEEDED`.
**Validates: Requirements 4.1, 4.3, 6.2**

### Property 4: Webhook idempotency & monotonic status
Untuk setiap urutan callback (duplikat, out-of-order), status akhir sama dengan menerapkan progres sah sekali; `SUCCEEDED`/`FAILED`/`REFUNDED` tidak pernah turun; `payment.succeeded` ditulis ke outbox paling banyak sekali per attempt.
**Validates: Requirements 4.4, 4.5, 4.6, 5.1**

### Property 5: Gateway expiry within booking deadline
Untuk setiap charge, waktu kedaluwarsa transaksi di gateway ≤ `paymentExpiresAt`.
**Validates: Requirement 3.4**

### Property 6: No duplicate order on ambiguous timeout
Untuk setiap timeout charge, tidak ada `provider_order_id` baru dibuat sampai attempt `UNKNOWN` direkonsiliasi.
**Validates: Requirement 3.8**

### Property 7: Refund case idempotent
Untuk setiap jumlah pengiriman ulang `booking.review.v1`, tepat satu `refund_cases` per attempt.
**Validates: Requirement 8.1**

### Property 8: Resolve never blocks
Untuk setiap resolve saat gateway tidak sehat, respons `UNKNOWN` dikembalikan dalam batas timeout breaker.
**Validates: Requirements 7.4, 9.3**

## 13. Testing

- **Unit:** signature (valid/salah/kosong), pemetaan status, `decide()` state machine, idempotency, proyeksi, breaker config, penurunan `reason` refund.
- **Integration (`-tags integration`, PG + RabbitMQ):** append-only, `uq_attempt_open_per_booking` paralel, dedup callback, relay + confirm, refund idempoten, `ErrConcurrency`.
- **Simulator E2E:** semua skenario §2.7 terhadap Booking nyata di compose.
- **TC:** TC-PAY-01..11, TC-MOB-01..03, TC-SEC-01..03, TC-RES-01/02, TC-EDT-04.
- **Coverage:** logika ≥ 80%.

## 14. Build & Verify
`make infra keys` → `cd services/payment && cp .env.example .env` (`PAYMENT_PROVIDER=simulator`) →
`go run ./cmd migrate up` → `go run ./cmd api & go run ./cmd worker &` → `curl -fsS localhost:8083/health`;
`make ci` hijau.
