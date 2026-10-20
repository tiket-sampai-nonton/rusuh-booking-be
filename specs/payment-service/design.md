# Design — Payment Service

| Field | Value |
|-------|-------|
| **Service** | Payment Service |
| **Spec ID** | SPEC-PAYMENT-DES-001 |
| **Sumber kebenaran (LLD)** | `docs/sdd/SDD-PAYMENT-001.md` |
| **Arsitektur** | Hexagonal (ports & adapters) + **Event Sourcing** (ADR-019) |
| **Port** | 8083 · **Schema/Role** `payment`/`svc_payment` |
| **Status implementasi** | **Boilerplate** — hanya slice health; sisanya TARGET |

> **Catatan grounding:** data model di bawah mengikuti **migrasi nyata** `deployments/migrations/0001_init.up.sql` + `0002_event_store.up.sql`, yang lebih kaya dan sedikit berbeda dari contoh DDL di SDD §6 (mis. enum metode `VA_BCA/VA_BNI/VA_BRI/VA_PERMATA/QRIS/GOPAY`, kolom `amount_minor`, `attempt_no`, `provider_order_id`, status `CREATED/PENDING/UNKNOWN/SUCCEEDED/...`). Bila SDD dan migrasi berbeda, **migrasi yang berlaku** (file terterapkan tidak boleh diubah — structure steering).

---

## 1. Arsitektur Hexagonal

Mengikuti `.kiro/steering/structure.md` & `code-principles.md`. Satu domain `payment`:

```
services/payment/internal/
├── config/                      [IMPL] APP_*/LOG_*/POSTGRES_*/RABBITMQ_* (+TARGET MIDTRANS_*)
├── delivery/                    [IMPL] composition root (server.go, worker.go, clients.go)
└── payment/
    ├── port/
    │   ├── inbound/   IUsecase: IGetPaymentPage, ICreateAttempt, IHandleWebhook,
    │   │              IGetStatus, IResolvePayment, IPageToken, IExchangePageToken,
    │   │              ICancelPayment, IOutboxRelay, IExpirySweeper  [TARGET]
    │   └── outbound/  IAttemptEventStore, IAttemptReadModel, ICallbackStore,
    │              IPageTokenStore, IRefundCaseStore, IOutboxStore, IIdempotencyStore,
    │              IPaymentGateway (Midtrans|Simulator), IBookingClient, IUnitOfWork  [TARGET]
    ├── domain/
    │   ├── entity/      PaymentAttempt (agregat), Callback, RefundCase  [TARGET]
    │   ├── valueobject/ Money(amount_minor,currency), Method, AttemptStatus, Signature  [TARGET]
    │   ├── dto/         request/response HTTP  [TARGET]
    │   └── mapper/      entity↔DTO (handler tak pernah kembalikan entity)  [TARGET]
    ├── application/usecase/  satu usecase per file  [TARGET]
    └── infrastructure/
        ├── http/        handler publik + internal, route Register(mux)  [TARGET]
        ├── postgres/     event store + read model + outbox + idempotency (pgx v5)  [TARGET]
        ├── midtrans/     adapter gateway nyata  [TARGET]
        ├── simulator/    adapter gateway lokal (PAYMENT_PROVIDER=simulator)  [TARGET]
        ├── rabbitmq/     publisher confirm + consumer booking.review  [TARGET]
        └── healthcheck/  [IMPL]
```

Prinsip: accept interfaces/return structs; usecase bergantung pada `port/outbound`; perilaku baru lewat adapter baru; interface berawalan `I` hanya di `port/`.

---

## 2. Event Sourcing (ADR-019)

Agregat **PaymentAttempt** adalah event-sourced. `payment_events` = sumber kebenaran; `payment_attempts` = read model yang **diproyeksikan sinkron dalam transaksi yang sama** dengan append event.

**Event types:** `AttemptCreated`, `InstructionIssued`, `SettlementReceived`, `AttemptFailed`, `AttemptExpired`, `RefundRequired`.

**Aturan transaksi (satu DB transaction):**
1. Muat stream (`stream_id = attempt.id`), hitung `expected_version`.
2. Append event ke `payment_events` (PK `(stream_id, version)` → optimistic concurrency; bentrok = `ErrConcurrency`).
3. Proyeksikan read model `payment_attempts` + update `stream_version`.
4. Tulis `outbox_events` bila ada perubahan uang (settlement/failed).

`ErrConcurrency` ⇒ baca ulang stream lalu ulangi (code-principles). Payload event **tanpa** data kartu / server key / token plaintext (R11.3; ditolak `pkg/eventstore`). Trigger `trg_payment_events_append_only` menolak UPDATE/DELETE/TRUNCATE.

State machine:
```
CREATED --charge--> PENDING --webhook settlement/capture--> SUCCEEDED(SETTLED)
                      |--webhook deny/cancel/failure------> FAILED
                      |--TTL tanpa webhook / expire--------> EXPIRED
SUCCEEDED (setelah cancel) --> REFUND_REQUIRED --> REFUNDED
```
Status final (`SUCCEEDED/FAILED/EXPIRED`) tidak boleh di-downgrade oleh callback out-of-order (R4.5).

---

## 3. Data Model (migrasi nyata)

Schema `payment` (role `svc_payment` hanya melihat schema sendiri).

**`payment_events`** (0002) — event store append-only: `(stream_id, version)` PK, `event_type`, `schema_version`, `payload jsonb`, `metadata`, `occurred_at`, `global_seq` IDENTITY. Trigger append-only + no-truncate.

**`payment_attempts`** (0001, read model) — `id`, `booking_id`, `attempt_no` (`UNIQUE(booking_id,attempt_no)`), `method` (enum 6), `provider` (`MIDTRANS|SIMULATOR`), `provider_order_id UNIQUE`, `provider_transaction_id`, `status` (9 nilai), `amount_minor`, `currency`, `expires_at`, `instruction jsonb`, `settlement_time`, `stream_version`.
- Invarian: `uq_attempt_open_per_booking` (partial unique pada `status IN (CREATED,PENDING,UNKNOWN)`) ⇒ **at-most-once charge** (R3.2).
- `uq_attempt_provider_tx` partial unique pada `(provider, provider_transaction_id)`.
- CHECK: status settle-family wajib punya `settlement_time`.

**`payment_callbacks`** — idempotensi webhook: `UNIQUE(provider, fingerprint)`, `signature_valid`, `payload_digest`, `processing_result ∈ (RECEIVED,PROCESSED,DUPLICATE,REJECTED,ERROR)` (R4.1, R4.3).

**`payment_page_tokens`** — `token_hash` PK (hanya hash disimpan), `booking_id`, `visitor_id`, `expires_at`, `used_at` (R2).

**`refund_cases`** — `attempt_id UNIQUE` (idempotensi tanpa inbox), `reason ∈ (LATE_SETTLEMENT,SETTLED_AFTER_CANCEL,DUPLICATE_PAYMENT)`, `status ∈ (OPEN,REFUNDED,CLOSED)` (R7.3, R8).

**`idempotency_records`** — PK `(scope, owner_id, idempotency_key)`, `request_hash`, `state ∈ (IN_PROGRESS,COMPLETED)`, `response_status/body`, `expires_at` (R3.3).

**`outbox_events`** — `routing_key`, `aggregate_id`, `payload`, `available_at`, `published_at`, `publish_attempts`, `last_error`; index parsial pending (R5).

---

## 4. Payment Flows

### 4.1 Create Attempt (charge)
1. Validasi `Idempotency-Key` → `idempotency_records`; jika COMPLETED & request-hash cocok ⇒ replay respons (R3.3).
2. Cek payable ke Booking (`GET /internal/v1/bookings/{id}/payable`) + payment-lock; jika bukan HELD/LOCKED ⇒ `BOOKING_NOT_PAYABLE`/`BOOKING_EXPIRED`.
3. `uq_attempt_open_per_booking` ⇒ jika ada attempt terbuka ⇒ `ATTEMPT_IN_PROGRESS`.
4. **Circuit breaker** membungkus charge ke `IPaymentGateway`; OPEN ⇒ tolak cepat (R3.5).
5. Transaksi: append `AttemptCreated`+`InstructionIssued`, proyeksi read model (`provider_order_id = RBT-{bookingId}-{attemptNo}`), simpan idempotency.
6. Kembalikan instruksi (redirect/qr/va).

### 4.2 Webhook (satu-satunya bukti pembayaran)
1. Hitung `expected = SHA512(order_id + status_code + gross_amount + server_key)`; bandingkan dgn `signature_key`. **Tidak cocok/kosong ⇒ `WEBHOOK_SIGNATURE_INVALID`**, catat `signature_valid=false, processing_result=REJECTED` (R4.1, R4.2).
2. Hitung `fingerprint`; `INSERT ... ON CONFLICT (provider,fingerprint) DO NOTHING` ⇒ bila sudah ada, `DUPLICATE`, 200, idempoten (R4.3).
3. Petakan `transaction_status` → status attempt (App.A.4). Tolak downgrade dari status final (R4.5).
4. Transaksi: append event (`SettlementReceived`/`AttemptFailed`/`AttemptExpired`) + proyeksi + outbox (`payment.succeeded`/`payment.failed`) bila perubahan uang.
5. `processing_result=PROCESSED`.

> Redirect/WebView/bridge **bukan** bukti pembayaran. Status final hanya dari webhook atau rekonsiliasi/`resolve` (invarian #5).

### 4.3 Resolve (dari Booking expiry sweep)
`POST /internal/v1/payments/{bookingId}/resolve action=CHECK_AND_CANCEL` → query gateway via circuit breaker → `NO_PAYMENT` (cancel di gateway) / `PAYMENT_FOUND` / `REFUND_REQUIRED` (buka `refund_cases SETTLED_AFTER_CANCEL`). Gateway degraded ⇒ CB open, tunda retry (R7.4).

### 4.4 Settlement & Outbox Relay
Settlement ⇒ outbox `payment.succeeded`. Worker relay (interval 1s) publish ke RabbitMQ exchange `rusuh.events` (topic) dengan **publisher confirm**; tandai `published_at`; gagal → retry berjenjang `RABBITMQ_RETRY_DELAYS`, DLQ setelah `RABBITMQ_MAX_RETRIES`.

---

## 5. Error Map (→ `pkg/apperror`, di `infrastructure/http`)

| errorCode | HTTP | Pemicu |
|-----------|------|--------|
| `BOOKING_NOT_PAYABLE` | 409 | Booking bukan HELD/LOCKED |
| `BOOKING_EXPIRED` | 410 | TTL booking habis |
| `BOOKING_NOT_FOUND` | 404 | Booking tak ada |
| `ATTEMPT_IN_PROGRESS` | 409 | Attempt terbuka sudah ada |
| `PAYMENT_METHOD_UNAVAILABLE` | 400 | Metode tak didukung |
| `GATEWAY_ERROR` | 502 | Midtrans error / CB open |
| `GATEWAY_TIMEOUT` | 504 | Midtrans timeout |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | Signature salah/kosong (TC-PAY-11) |
| `DUPLICATE_CALLBACK` | 200 | Fingerprint sudah diproses |
| `PAYMENT_PAGE_TOKEN_INVALID` | 403 | Token WebView salah/expired/dipakai |
| `VISITOR_MISMATCH` | 403 | Visitor tak cocok |

Domain error bertipe dipetakan ke `errorCode`; handler selalu kembalikan DTO lewat `domain/mapper`, tak pernah entity.

---

## 6. Konfigurasi

**[IMPL]** `APP_*`, `LOG_*`, `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT`, `WORKER_INTERVAL`, `POSTGRES_*` (+`POSTGRES_SCHEMA=payment`), `RABBITMQ_*` (exchange `rusuh.events` topic, retry `5s,30s,2m`, confirm).

**[TARGET]** `PAYMENT_PROVIDER=simulator|midtrans`, `MIDTRANS_SERVER_KEY`, `MIDTRANS_CLIENT_KEY`, `MIDTRANS_ENVIRONMENT`, `MIDTRANS_WEBHOOK_PATH`, `MIDTRANS_CB_FAILURE_THRESHOLD=3`, `MIDTRANS_CB_RECOVERY_TIMEOUT=60s`, `BOOKING_SERVICE_URL`, `TOKEN_PUBLIC_KEY_PATH`, `PAYMENT_PAGE_TOKEN_TTL=60s`. Semua divalidasi fail-fast di `config.Validate()`.

---

## 7. Observability (SDD §12)

Metrics: `rbt_payment_attempts_total{method,status}`, `rbt_payment_webhooks_total{status,result}`, `rbt_payment_settlements_total{method}`, `rbt_payment_midtrans_latency{operation}`, `rbt_payment_circuit_state` (0/1/2).
Alerts: `PaymentCircuitOpen` (OPEN 2m, Critical), `PaymentWebhookErrorRate` (>5% 5m), `PaymentSettlementLag` (>5m), `PaymentRefundCasesOpen` (>10). Log terstruktur (slog JSON) dengan RequestID; **tidak pernah** mencatat NIK/server key/token.

---

## 8. Testing (testing-review steering)

- **Unit** (testify, table-driven, mock dari `_mock/`): verifikasi signature, mapping status App.A.4, state machine (tolak downgrade), circuit breaker (CLOSED→OPEN→HALF_OPEN), idempotency replay, proyeksi event→read model.
- **Integration** (build tag `integration`, Postgres+RabbitMQ nyata): append-only trigger, `uq_attempt_open_per_booking`, dedup callback (`UNIQUE(provider,fingerprint)`), outbox relay + publisher confirm, refund case idempoten.
- **TC kunci:** TC-PAY-01 (charge GoPay), TC-PAY-05 (webhook settle → `payment.succeeded`), TC-PAY-06 (race sebelum expiry), TC-PAY-08 (gateway down → CB), TC-PAY-09 (cancel), TC-PAY-11 (signature invalid → 401), TC-SEC-01/02/03, TC-MOB-01/02/03.
- **Coverage:** ambang logika 80% (`make cover`); `cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan.

## 9. Build & Verify

Build: `make infra keys` → `cd services/payment && cp .env.example .env` (set `PAYMENT_PROVIDER=simulator`) → `go run ./cmd migrate up` → `go run ./cmd api & go run ./cmd worker &` → `curl -fsS localhost:8083/health`.
Verify: `make build && make lint && go test ./... -race && make test-integration && make cover`; gerbang akhir `make ci` hijau.
