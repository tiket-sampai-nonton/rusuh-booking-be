# Design — Payment Service

| Field | Value |
|-------|-------|
| **Service** | Payment Service |
| **Spec ID** | SPEC-PAYMENT-DES-001 |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-PAYMENT-001.md` |
| **Kontrak** | `docs/contracts/openapi/payment.yaml`, `internal-payment.yaml`, `events/{payment.succeeded,payment.failed,booking.review}.v1.schema.json` |
| **Arsitektur** | Hexagonal (ports & adapters) + **Event Sourcing** (ADR-019) |
| **Port** | 8083 · **Schema/Role** `payment`/`svc_payment` |
| **Status implementasi** | **Boilerplate** — hanya slice health; sisanya TARGET |

> **Grounding:** data model mengikuti **migrasi nyata** `0001_init.up.sql` + `0002_event_store.up.sql`; API mengikuti **OpenAPI nyata** (`{meta,data}` envelope, enum metode `VA_BCA/VA_BNI/VA_BRI/VA_PERMATA/QRIS/GOPAY`, resolve `mode`/`outcome`). Di mana prosa SDD berbeda, kontrak & migrasi yang berlaku (file migrasi terterapkan tidak boleh diubah — `structure.md`).

---

## 1. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` & `code-principles.md`. Satu domain `payment`:

```
services/payment/internal/
├── config/                      [IMPL subset] APP_*/LOG_*/HTTP_*/POSTGRES_*/RABBITMQ_* (+TARGET PAYMENT_PROVIDER/MIDTRANS_*/...)
├── delivery/                    [IMPL] composition root (server.go, worker.go, clients.go, serve.go)
└── payment/
    ├── port/
    │   ├── inbound/   IUsecase (TARGET): IGetPaymentPage, ICreatePageToken, IExchangePageToken,
    │   │              ICreateAttempt, IGetPaymentStatus, IHandleWebhook, IResolvePayment,
    │   │              ICancelPayment, IRelayOutbox, IExpireAttempts, IOpenRefundCase
    │   └── outbound/  (TARGET): IAttemptEventStore, IAttemptReadModel, ICallbackStore,
    │              IPageTokenStore, IRefundCaseStore, IOutboxStore, IIdempotencyStore,
    │              IPaymentGateway, IBookingClient, IUnitOfWork, IClock
    ├── domain/
    │   ├── entity/      PaymentAttempt (agregat event-sourced), Callback, RefundCase, PageToken  [TARGET]
    │   ├── valueobject/ Money(amountMinor,currency), Method, AttemptStatus, Provider, Signature, Instruction  [TARGET]
    │   ├── dto/         request/response HTTP (mirror OpenAPI schemas)  [TARGET]
    │   └── mapper/      entity↔DTO (handler tak pernah kembalikan entity)  [TARGET]
    ├── application/usecase/  satu usecase per file  [TARGET]
    └── infrastructure/
        ├── http/        handler publik + internal + simulator, route Register(mux)  [TARGET]
        ├── postgres/     event store + read model + outbox + idempotency + callbacks (pgx v5)  [TARGET]
        ├── midtrans/     adapter IPaymentGateway nyata (charge/status/cancel + verify signature)  [TARGET]
        ├── simulator/    adapter IPaymentGateway lokal (PAYMENT_PROVIDER=simulator)  [TARGET]
        ├── rabbitmq/     publisher confirm (outbox) + consumer booking.review  [TARGET]
        └── healthcheck/  [IMPL]
```

Prinsip: accept interfaces/return structs; usecase bergantung pada `port/outbound`; perilaku baru lewat adapter baru; interface berawalan `I` hanya di `port/`; transaksi lewat `IUnitOfWork` dengan urutan lock tetap.

---

## 2. API Endpoints

Semua respons sukses dibungkus envelope `{meta, data}`; error memakai `ErrorEnvelope` `{meta:{code,message,requestId,errorCode,details},data:null}` (SRS §3.1.4; tak pernah memuat SQL/stack/secret/payload provider). Security: `rb_visitor` cookie **atau** `X-Visitor-Token` header; sebagian menerima `rb_pay` cookie.

### 2.1 `GET /api/v1/payments/{bookingId}/page` — Payment Page
- **Auth:** visitorCookie | visitorHeader | paymentPageCookie
- **200 `PaymentPage`:**
  ```json
  {
    "meta": { "code": 200, "requestId": "..." },
    "data": {
      "bookingId": "uuid",
      "methods": ["VA_BCA","VA_BNI","VA_BRI","VA_PERMATA","QRIS","GOPAY"],
      "amountMinor": 3000000,
      "paymentExpiresAt": "2026-10-19T10:15:00Z",
      "serverTime": "2026-10-19T10:05:00Z",
      "canEdit": true,
      "editableUntil": "2026-10-19T10:12:00Z",
      "currentAttempt": null
    }
  }
  ```
- **Errors:** 409 `BOOKING_NOT_PAYABLE`, 410 `BOOKING_EXPIRED`, 403 `VISITOR_MISMATCH`.

### 2.2 `POST /api/v1/payments/{bookingId}/page-tokens` — Mobile WebView Token
- **Auth:** visitorCookie | visitorHeader
- **201 `PageToken`:** `{ "pageToken": "one-time", "pageUrl": "https://api.rusuh.example/pay/{bookingId}?token=...", "expiresAt": "..." }`
- Simpan **hanya** `token_hash` (BYTEA), `visitor_id`, TTL `PAYMENT_PAGE_TOKEN_TTL` (default 60s).
- **Errors:** 409 `BOOKING_NOT_PAYABLE`.

### 2.3 `POST /api/v1/payment-page-sessions` — Token Exchange
- **Auth:** none (token di body adalah kredensial).
- **Request `PageSessionRequest`:** `{ "pageToken": "one-time" }`
- **204:** set cookie `rb_pay` (HttpOnly, Secure, SameSite=Lax); `used_at` ditandai (sekali pakai).
- **Errors:** 403 `PAYMENT_PAGE_TOKEN_INVALID` (salah/kedaluwarsa/sudah dipakai).

### 2.4 `POST /api/v1/payments/{bookingId}/attempts` — Create Charge
- **Auth:** visitorCookie | visitorHeader | paymentPageCookie
- **Header wajib:** `Idempotency-Key` (16–64 char, unik per tombol; retry pakai key sama).
- **Request `CreateAttemptRequest`:** `{ "method": "GOPAY" }`
- **201 `Attempt`:**
  ```json
  {
    "meta": { "code": 201 },
    "data": {
      "attemptNo": 1,
      "method": "GOPAY",
      "status": "PENDING",
      "bookingVersion": 3,
      "expiresAt": "2026-10-19T10:20:00Z",
      "instruction": { "type": "DEEPLINK", "deeplinkUrl": "https://..." }
    }
  }
  ```
  `instruction.type ∈ {VA, QR, DEEPLINK}` (VA → `bank`,`vaNumber`; QR → `qrString`; DEEPLINK → `deeplinkUrl`).
- **Errors:** 409 `BOOKING_NOT_PAYABLE` | `IDEMPOTENCY_KEY_REUSED` | `ATTEMPT_IN_PROGRESS`; 410 `BOOKING_EXPIRED`; 422 `PAYMENT_METHOD_UNAVAILABLE`; 503 `PAYMENT_PROVIDER_UNAVAILABLE` (gateway error/timeout/breaker OPEN).

### 2.5 `GET /api/v1/payments/{bookingId}/status` — Status
- **Auth:** visitorCookie | visitorHeader | paymentPageCookie
- **200 `PaymentStatus`:** `{ "bookingId": "...", "bookingStatus": "PAID", "attempt": { ...Attempt }, "serverTime": "..." }`
- Read-only dari read model; tidak pernah mengubah status.

### 2.6 `POST /api/v1/webhooks/midtrans` — Callback
- **Auth:** `midtransSignature` (field `signature_key` body, SHA-512).
- **Request `MidtransNotification`** (`additionalProperties: true`): `order_id`, `transaction_status`, `status_code`, `gross_amount`, `signature_key` (wajib) + `transaction_id`, `settlement_time`, `fraud_status`, `payment_type`.
- **200:** callback tersimpan durable di `payment_callbacks` sebelum 2xx (idempoten, out-of-order aman).
- **401 `WEBHOOK_SIGNATURE_INVALID`:** signature salah/kosong.

### 2.7 `POST /api/v1/local-simulator/payments/{attemptId}/scenario` — Simulator (local/ci)
- **Auth:** none; aktif hanya `APP_ENV=local|ci`, selain itu 404 `NOT_FOUND`.
- **Request `SimulatorScenario`:** `{ "scenario": "SETTLE" }` — enum `SETTLE|SETTLE_LATE|EXPIRE|DENY|TIMEOUT|DUPLICATE_WEBHOOK|OUT_OF_ORDER`.
- **202:** skenario dijalankan (mendorong webhook sintetis tanpa Midtrans nyata).

### 2.8 `POST /internal/v1/payments/{bookingId}/resolve` — Internal Resolve
- **Auth:** `serviceBearer` (JWT EdDSA, ADR-011) — dipanggil sweeper Booking.
- **Request `ResolveRequest`:** `{ "mode": "CHECK_AND_CANCEL" }` — `mode ∈ {CHECK, CHECK_AND_CANCEL}`.
- **200 `ResolveResponse`:** `{ "outcome": "NOT_PAID", "settlementTime": null, "attemptId": "uuid" }` — `outcome ∈ {SUCCEEDED, NOT_PAID, NO_ATTEMPT, UNKNOWN}`.
  - `SUCCEEDED` + `settlementTime`: pembayaran ditemukan (kemungkinan buka refund case bila sudah dibatalkan).
  - `NOT_PAID`: dibatalkan di gateway (mode CHECK_AND_CANCEL).
  - `NO_ATTEMPT`: tidak ada attempt.
  - `UNKNOWN`: gateway degraded / breaker OPEN → Booking tahan stok (VERIFYING) & retry nanti.

### 2.9 `GET /health` — Ops (D-23)
- `200`/`503` `Health` `{status,service,checkedAt,dependencies[]}`; ping Postgres & RabbitMQ dengan `HEALTH_TIMEOUT`. **[IMPL]**

### 2.10 Error Codes (katalog lengkap)

| errorCode | HTTP | Endpoint | Pemicu |
|-----------|------|----------|--------|
| `BOOKING_NOT_PAYABLE` | 409 | page/attempt | Booking bukan HELD/LOCKED |
| `BOOKING_EXPIRED` | 410 | page/attempt | TTL booking habis |
| `VISITOR_MISMATCH` | 403 | page/attempt/status | Visitor tak cocok (TC-SEC-03) |
| `PAYMENT_PAGE_TOKEN_INVALID` | 403 | page-sessions | Token salah/expired/dipakai (TC-SEC-01) |
| `IDEMPOTENCY_KEY_REUSED` | 409 | attempt | Key sama, request-hash beda |
| `ATTEMPT_IN_PROGRESS` | 409 | attempt | Attempt terbuka sudah ada (`uq_attempt_open_per_booking`) |
| `PAYMENT_METHOD_UNAVAILABLE` | 422 | attempt | Metode tak aktif/didukung |
| `PAYMENT_PROVIDER_UNAVAILABLE` | 503 | attempt | Gateway error/timeout / breaker OPEN |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | webhook | Signature salah/kosong (TC-PAY-11) |
| `NOT_FOUND` | 404 | simulator | Simulator mati di env ini |

Error dipetakan via `pkg/apperror` di `infrastructure/http`; handler selalu kembalikan DTO lewat `domain/mapper`, tak pernah entity.

---

## 3. Data Model (migrasi nyata)

Schema `payment` (role `svc_payment` hanya melihat schema sendiri).

### 3.1 `payment_attempts` (read model, 0001)
`id uuid PK`, `booking_id`, `attempt_no int CHECK >0`, `method` (CHECK IN 6 enum), `provider` (`MIDTRANS|SIMULATOR`), `provider_order_id text UNIQUE`, `provider_transaction_id`, `status` (CHECK IN 9: `CREATED,PENDING,UNKNOWN,SUCCEEDED,FAILED,CANCELLED,EXPIRED,REFUND_REQUIRED,REFUNDED`), `amount_minor bigint CHECK >=0`, `currency char(3) DEFAULT 'IDR'`, `expires_at`, `instruction jsonb`, `settlement_time`, `last_provider_status`, `created_at/updated_at`, `stream_version int` (0002, versi stream terakhir diproyeksikan).
- `UNIQUE (booking_id, attempt_no)`.
- `CHECK (status NOT IN ('SUCCEEDED','REFUND_REQUIRED','REFUNDED') OR settlement_time IS NOT NULL)` — status settle-family wajib punya `settlement_time`.
- `idx_attempts_booking (booking_id, attempt_no DESC)`.
- `uq_attempt_provider_tx` — UNIQUE parsial `(provider, provider_transaction_id)` WHERE `provider_transaction_id IS NOT NULL`.
- **`uq_attempt_open_per_booking`** — UNIQUE parsial `(booking_id)` WHERE `status IN ('CREATED','PENDING','UNKNOWN')` ⇒ **at-most-once charge** (I-2).

### 3.2 `payment_events` (event store, 0002)
`stream_id uuid` (= `payment_attempts.id`), `version int CHECK >0`, `event_type text CHECK ~ '^[A-Z][A-Za-z]+$'`, `schema_version smallint`, `payload jsonb` (**tanpa data kartu/server key/token**), `metadata jsonb`, `occurred_at`, `global_seq bigint IDENTITY`. **PK `(stream_id, version)`** → optimistic concurrency. `uq_payment_events_global_seq`, `idx_payment_events_type_time`.
- Trigger `trg_payment_events_append_only` (BEFORE UPDATE/DELETE → `restrict_violation`) + `trg_payment_events_no_truncate` (I-5).

### 3.3 `payment_callbacks` (idempotensi webhook, 0001)
`id`, `provider`, `fingerprint bytea`, `provider_order_id`, `normalized_status`, `signature_valid bool`, `amount_minor`, `payload_digest bytea`, `received_at`, `processing_result` (CHECK `RECEIVED|PROCESSED|DUPLICATE|REJECTED|ERROR`). **`UNIQUE (provider, fingerprint)`** (I-3). `idx_callbacks_order (provider_order_id, received_at DESC)`.

### 3.4 `payment_page_tokens` (0001)
`token_hash bytea PK` (hanya hash), `booking_id`, `visitor_id`, `expires_at`, `used_at`, `created_at`, `CHECK (expires_at > created_at)`.

### 3.5 `refund_cases` (0001)
`id`, **`attempt_id uuid UNIQUE REFERENCES payment_attempts(id) ON DELETE RESTRICT`** (idempotensi tanpa inbox, I-8), `booking_id`, `reason` (CHECK `LATE_SETTLEMENT|SETTLED_AFTER_CANCEL|DUPLICATE_PAYMENT`), `status` (CHECK `OPEN|REFUNDED|CLOSED`), `amount_minor`, `opened_at`, `resolved_at`, `resolved_by`, `note`.

### 3.6 `idempotency_records` (0001)
PK `(scope, owner_id, idempotency_key)`, `request_hash bytea`, `state` (CHECK `IN_PROGRESS|COMPLETED`), `response_status`, `response_body jsonb`, `created_at`, `expires_at`.

### 3.7 `outbox_events` (0001)
`id`, `routing_key`, `aggregate_id`, `schema_version`, `payload jsonb`, `occurred_at`, `available_at`, `published_at`, `publish_attempts`, `last_error`. `idx_payment_outbox_pending (available_at) WHERE published_at IS NULL`.

> **v4.6:** tidak ada `consumer_inbox`; konsumsi `booking.review` idempoten lewat `refund_cases.attempt_id UNIQUE`.

---

## 4. Event Sourcing (ADR-019)

Agregat **PaymentAttempt** event-sourced. `payment_events` = sumber kebenaran; `payment_attempts` = read model diproyeksikan **sinkron dalam transaksi yang sama** dengan append event.

**Event types (dipetakan ke state transition):**

| Event | Dari→Ke | Dipicu oleh |
|-------|---------|-------------|
| `AttemptCreated` | ∅→CREATED | ICreateAttempt (sebelum charge) |
| `AttemptPending` | CREATED→PENDING | charge berhasil + instruksi terbit |
| `AttemptSettled` | PENDING→SUCCEEDED | webhook `settlement`/`capture` / resolve SUCCEEDED |
| `AttemptFailed` | PENDING→FAILED | webhook `deny`/`cancel`/`failure` / cancel user |
| `AttemptExpired` | PENDING→EXPIRED | webhook `expire` / sweeper TTL / resolve NOT_PAID |

(SDD §7 juga menyebut `InstructionIssued`/`SettlementReceived`/`RefundRequired`; spec ini memakai 5 event di atas sebagai kanonik, dengan `AttemptSettled` setelah cancel → `REFUND_REQUIRED` via proyeksi.)

**Aturan transaksi (satu DB transaction, via `IUnitOfWork`):**
1. Muat stream (`stream_id = attempt.id`), hitung `expected_version = payment_attempts.stream_version`.
2. Append event ke `payment_events` (PK `(stream_id, version)`; bentrok ⇒ `ErrConcurrency`).
3. Proyeksikan read model `payment_attempts` + set `stream_version = version`.
4. Tulis `outbox_events` bila perubahan uang (`AttemptSettled`→`payment.succeeded`, `AttemptFailed`→`payment.failed`).

`ErrConcurrency` ⇒ baca ulang stream lalu ulangi (code-principles). Payload event **tanpa** data kartu/server key/token plaintext (ditolak `pkg/eventstore`).

### 4.1 State Machine
```
         ICreateAttempt            charge OK
   ∅ ───────────────▶ CREATED ───────────────▶ PENDING
                                                   │
             ┌─────────────────────────────────────┼─────────────────────────────┐
   webhook settlement|capture          webhook deny|cancel|failure        webhook expire / TTL sweeper
             ▼                                     ▼                               ▼
         SUCCEEDED                              FAILED                          EXPIRED
             │ (settle setelah cancel/expired → resolve/refund)
             ▼
      REFUND_REQUIRED ───(diproses tim)──▶ REFUNDED
```
Status final `SUCCEEDED/FAILED/EXPIRED/REFUNDED` **tidak boleh di-downgrade** oleh callback out-of-order (R4.5). `UNKNOWN` adalah status transien (charge error/timeout) yang di-resolve oleh webhook atau `resolve`.

---

## 5. Midtrans Integration

### 5.1 Charge API (adapter `midtrans`)
Panggil Midtrans Core API `/v2/charge` lewat circuit breaker. `order_id = RBT-{bookingId}-{attemptNo}` (deterministik, mendukung idempotensi gateway). `gross_amount` dari `amount_minor` (IDR tanpa desimal).

GoPay (DEEPLINK):
```json
{ "payment_type": "gopay",
  "transaction_details": { "order_id": "RBT-{bookingId}-{n}", "gross_amount": 1500000 },
  "gopay": { "enable_callback": true, "callback_url": "https://app.rusuh.example/payment/callback" },
  "item_details": [ { "id": "cat-uuid", "name": "VIP - Konser ABC", "price": 1500000, "quantity": 1 } ],
  "customer_details": { "email": "buyer@example.com" } }
```
Bank Transfer (VA):
```json
{ "payment_type": "bank_transfer",
  "transaction_details": { "order_id": "RBT-{bookingId}-{n}", "gross_amount": 1500000 },
  "bank_transfer": { "bank": "bca" },
  "custom_expiry": { "expiry_duration": 900, "unit": "second" } }
```
Response → `Instruction`: GoPay/ShopeePay → `{type:DEEPLINK, deeplinkUrl}`; QRIS → `{type:QR, qrString}`; VA → `{type:VA, bank, vaNumber}`.

**Pemetaan metode spec → payment_type Midtrans:** `GOPAY→gopay`, `QRIS→qris`, `VA_BCA→bank_transfer{bca}`, `VA_BNI→bank_transfer{bni}`, `VA_BRI→bank_transfer{bri}`, `VA_PERMATA→bank_transfer{permata}`.

### 5.2 Webhook Signature Verification (I-1)
```
expected = SHA512(order_id + status_code + gross_amount + MIDTRANS_SERVER_KEY)
if hmacEqual(expected, notification.signature_key) == false || signature_key == "" {
    simpan callback {signature_valid:false, processing_result:REJECTED}
    return 401 WEBHOOK_SIGNATURE_INVALID
}
```
`gross_amount` diambil apa adanya dari body (string, mis. `"1500000.00"`). Bandingkan konstan-waktu. **Satu-satunya bukti pembayaran** (redirect/WebView/bridge bukan bukti).

### 5.3 Status Mapping (App.A.4)

| Midtrans `transaction_status` | Attempt status | Event | Aksi |
|-------------------------------|----------------|-------|------|
| `pending` | PENDING | `AttemptPending` | tunggu webhook berikut |
| `settlement` | SUCCEEDED | `AttemptSettled` | outbox `payment.succeeded` |
| `capture` (`fraud_status=accept`) | SUCCEEDED | `AttemptSettled` | outbox `payment.succeeded` |
| `deny` | FAILED | `AttemptFailed` | outbox `payment.failed` |
| `cancel` | FAILED | `AttemptFailed` | user cancelled |
| `failure` | FAILED | `AttemptFailed` | gateway error |
| `expire` | EXPIRED | `AttemptExpired` | TTL lewat |

### 5.4 Environments

| Environment | `PAYMENT_PROVIDER` | Server Key |
|-------------|--------------------|-----------|
| Lokal (simulator) | `simulator` | N/A (endpoint `/local-simulator/...`) |
| Lokal/Staging (Midtrans) | `midtrans` | Sandbox `SB-Mid-server-xxx` |
| Produksi | `midtrans` | `Mid-server-xxx` |

Webhook URL dikonfigurasi di Midtrans Dashboard → Payment Notification URL → `https://api.rusuh.example/api/v1/webhooks/midtrans`.

---

## 6. Circuit Breaker (`pkg/resilience`)

Menggunakan `pkg/resilience.Breaker` (consecutive-failure, thread-safe, lokal per instance). Bungkus **setiap** panggilan sinkron ke gateway (charge, status check, cancel).

**Config (`BreakerConfig`):** `Name`, `FailureThreshold=3` (default), `Cooldown=60s`, `HalfOpenProbes=1`, `Timeout` (dari config; 0 = tanpa batas tambahan), `IsFailure` (default: semua error kecuali `context.Canceled`), `OnStateChange` (untuk metrik `rbt_payment_circuit_state`).

**State machine (`Breaker.Do`):**
```
CLOSED ──(gagal beruntun ≥ FailureThreshold)──▶ OPEN
OPEN   ──(Cooldown berlalu, dicek saat Do/State)──▶ HALF_OPEN
HALF_OPEN ──(probe sukses)──▶ CLOSED
HALF_OPEN ──(probe gagal)──▶ OPEN
HALF_OPEN ──(probe in-flight ≥ HalfOpenProbes)──▶ tolak cepat ErrOpen
OPEN   ──(selama cooldown)──▶ tolak cepat ErrOpen
```
Charge saat OPEN → usecase mengembalikan 503 `PAYMENT_PROVIDER_UNAVAILABLE` tanpa menyentuh gateway (R3.5). `resolve` saat OPEN → `outcome=UNKNOWN` (R7.4). Opsional `Bulkhead` (max konkurensi) → penuh `ErrBulkheadFull` (R9.3, TC-RES-02).

---

## 7. Worker Jobs

Dijalankan oleh verb `worker` (`internal/delivery/worker.go`), interval dasar `WORKER_INTERVAL` (default 5s).

### 7.1 Outbox Relay
- Query `outbox_events WHERE published_at IS NULL AND available_at <= now()` (`idx_payment_outbox_pending`) urut `available_at`.
- Publish ke `rusuh.events` (topic) dengan **publisher confirm** (`RABBITMQ_CONFIRM_TIMEOUT`).
- Sukses → set `published_at`. Gagal → naik `publish_attempts`, set `last_error`, jadwalkan ulang `available_at` sesuai `RABBITMQ_RETRY_DELAYS` (`5s,30s,2m`); setelah `RABBITMQ_MAX_RETRIES` → DLQ.

### 7.2 Attempt Expiry Sweeper
- Query `payment_attempts WHERE status='PENDING' AND expires_at < now()`.
- Untuk tiap attempt: append `AttemptExpired`, proyeksi → EXPIRED; bila gateway reachable, `cancel` di gateway (lewat breaker). Jika justru ditemukan settle → buka refund case (`SETTLED_AFTER_CANCEL`).
- Idempoten: re-sweep attempt yang sudah EXPIRED di-skip (status final).

---

## 8. Event Contracts

### 8.1 Published
| Event | routing_key | Konsumer | Payload (schema) |
|-------|-------------|----------|------------------|
| Payment Succeeded | `payment.succeeded` | Booking | `{bookingId, attemptId, settlementTime, amountMinor, method}` (`payment.succeeded.v1`) |
| Payment Failed | `payment.failed` | Booking | `{bookingId, attemptId, reason}` (`payment.failed.v1`) |

Dibungkus envelope event standar (`eventId`, `eventType`, `occurredAt`, `schemaVersion`, `payload`); payload mengikuti skema JSON (`additionalProperties:false`, tanpa PII/secret).

### 8.2 Consumed
| Event | Producer | Payload | Tujuan |
|-------|----------|---------|--------|
| `booking.review` | Booking | `{bookingId, attemptHint?, buyerEmail, amountMinor}` (`booking.review.v1`) | Buka `refund_cases` idempoten (I-8); payload rusak → `rabbitmq.Permanent(err)` → DLQ (R8.2) |

---

## 9. Sequence Diagrams

### 9.1 Charge Flow
```
Klien            Payment             Booking           Gateway(Midtrans)        DB
  │ POST attempts  │                   │                     │                  │
  │ (Idem-Key)────▶│ cek idempotency ──┼─────────────────────┼─────────────────▶│ (IN_PROGRESS)
  │                │ payment-lock ─────▶│                     │                  │
  │                │◀── payable/version │                     │                  │
  │                │ breaker.Do(charge) ┼────────────────────▶│                  │
  │                │◀── instruction ────┼─────────────────────│                  │
  │                │ TX: append AttemptCreated+AttemptPending, proyeksi, idempotency=COMPLETED ─▶│
  │◀─ 201 Attempt ─│                   │                     │                  │
```
Gateway error/timeout → breaker++ ; attempt=UNKNOWN ; 503 `PAYMENT_PROVIDER_UNAVAILABLE`.

### 9.2 Webhook Flow
```
Midtrans          Payment                                   DB              RabbitMQ
  │ POST webhook────▶│ verify SHA512(order_id+status_code+gross_amount+serverKey)
  │                  │   invalid/empty → callback{REJECTED} ─▶│ ; 401 WEBHOOK_SIGNATURE_INVALID
  │                  │ INSERT callback ON CONFLICT(provider,fingerprint) DO NOTHING ─▶│
  │                  │   conflict → DUPLICATE ; 200 (idempoten)
  │                  │ map status (A.4); tolak downgrade status final
  │                  │ TX: append AttemptSettled/Failed/Expired + proyeksi + outbox ─▶│
  │◀─ 200 ───────────│                                                        │
  │                  │ (worker) relay outbox ──────────────────────────────────────▶│ payment.succeeded
```

### 9.3 Resolve Flow
```
Booking(sweeper)   Payment                       Gateway            DB
  │ POST resolve ───▶│ (serviceBearer)             │                 │
  │ {mode}           │ load attempt ───────────────┼────────────────▶│
  │                  │ breaker.Do(status check) ──▶│                 │
  │                  │◀── settle? / not found ─────│                 │
  │                  │ CHECK_AND_CANCEL & !paid → cancel di gateway   │
  │                  │ paid-after-cancel → refund_cases(SETTLED_AFTER_CANCEL), REFUND_REQUIRED ─▶│
  │                  │ breaker OPEN → outcome=UNKNOWN                 │
  │◀─ 200 {outcome,settlementTime,attemptId} ──────│                 │
```

---

## 10. Konfigurasi

**[IMPL]** `APP_ENV/APP_NAME/APP_PORT(8083)`, `LOG_LEVEL/LOG_FORMAT`, `HTTP_SHUTDOWN_TIMEOUT(15s)`, `HEALTH_TIMEOUT(2s)`, `WORKER_INTERVAL(5s)`, `POSTGRES_*` (+`POSTGRES_SCHEMA=payment`, `POSTGRES_USER=svc_payment`), `RABBITMQ_*` (`RABBITMQ_EXCHANGE=rusuh.events` topic, `RABBITMQ_RETRY_DELAYS=5s,30s,2m`, `RABBITMQ_MAX_RETRIES=3`, `RABBITMQ_CONFIRM_TIMEOUT=5s`, workers/prefetch).

**[TARGET] (harus ditambah ke `config.go`/`load.go`/`.env.example` + `Validate()` fail-fast):**
`PAYMENT_PROVIDER=simulator|midtrans`, `MIDTRANS_SERVER_KEY`, `MIDTRANS_CLIENT_KEY`, `MIDTRANS_ENVIRONMENT=sandbox|production`, `MIDTRANS_BASE_URL`, `MIDTRANS_WEBHOOK_PATH=/api/v1/webhooks/midtrans`, `MIDTRANS_CB_FAILURE_THRESHOLD=3`, `MIDTRANS_CB_RECOVERY_TIMEOUT=60s`, `MIDTRANS_CB_HALFOPEN_PROBES=1`, `MIDTRANS_TIMEOUT`, `BOOKING_SERVICE_URL=http://booking:8082`, `BOOKING_SERVICE_TOKEN`/`TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL=60s`.

---

## 11. Observability (SDD §12)

Metrics: `rbt_payment_attempts_total{method,status}`, `rbt_payment_webhooks_total{status,result}`, `rbt_payment_settlements_total{method}`, `rbt_payment_midtrans_latency{operation}`, `rbt_payment_circuit_state` (0=closed,1=open,2=half-open via `OnStateChange`).
Alerts: `PaymentCircuitOpen` (OPEN 2m, Critical), `PaymentWebhookErrorRate` (>5% 5m, Warning), `PaymentSettlementLag` (>5m, Warning), `PaymentRefundCasesOpen` (>10, Warning). Log slog JSON dengan RequestID; **tidak pernah** NIK/email/server key/token/payload provider mentah (I-7, TC-SEC-03).

---

## 12. Testing (testing-review steering)

- **Unit** (testify, table-driven, mock `_mock/`): verifikasi signature (valid/salah/kosong), mapping status A.4, state machine (tolak downgrade final & UNKNOWN resolve), idempotency replay vs `IDEMPOTENCY_KEY_REUSED`, proyeksi event→read model, breaker CLOSED→OPEN→HALF_OPEN→CLOSED, worker relay/sweeper.
- **Integration** (build tag `integration`, Postgres+RabbitMQ nyata): trigger append-only (UPDATE/DELETE/TRUNCATE ditolak), `uq_attempt_open_per_booking`, dedup callback `UNIQUE(provider,fingerprint)`, outbox relay + publisher confirm, refund case idempoten (`booking.review` 2×), optimistic concurrency `ErrConcurrency`.
- **Simulator** (`/local-simulator/.../scenario`): `SETTLE/SETTLE_LATE/EXPIRE/DENY/TIMEOUT/DUPLICATE_WEBHOOK/OUT_OF_ORDER` untuk E2E lokal tanpa Midtrans.
- **TC kunci:** TC-PAY-01/03/04/05/06/07/08/09/10/11, TC-SEC-01/02/03, TC-MOB-01/02, TC-RES-01/02, TC-OPS-01.
- **Coverage:** ambang logika 80% (`make cover`); `cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan.

---

## 13. Build & Verify

Build: `make infra keys` → `cd services/payment && cp .env.example .env` (set `PAYMENT_PROVIDER=simulator`) → `go run ./cmd migrate up` → `go run ./cmd api & go run ./cmd worker &` → `curl -fsS localhost:8083/health`.
Verify: `make build && make lint && go test ./... -race && make test-integration && make cover`; gerbang akhir `make ci` hijau.
