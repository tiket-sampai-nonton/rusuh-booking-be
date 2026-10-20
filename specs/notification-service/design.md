# Design — Notification Service

Dokumen desain ini menerjemahkan `docs/sdd/SDD-NOTIFICATION-001.md` (LLD) ke
rancangan implementasi, dibumikan ke kode nyata di `services/notification/` dan
utilitas `pkg/`. Arsitektur: **event-driven worker** (consumer RabbitMQ) +
**REST tipis** (push-subscription API), disusun hexagonal (ports & adapters).

Mengikuti standar engineering: event-driven-worker didominasi klaster resiliensi
— at-least-once + idempotency + retry/backoff + error-classification + DLQ
(lih. `[[event-driven-worker]]`, `[[at-least-once-processing]]`,
`[[idempotency-and-dedup]]`, `[[retry-and-backoff]]`). Idiom Go per `[[go]]`.

> Status: consumer/adapter/usecase pengiriman masih **boilerplate** (lihat
> requirements.md). Desain di bawah adalah *target* yang mengisi
> `jobs()`/`Subscription` kosong, kecuali bagian yang ditandai **[implemented]**.

---

## 1. Arsitektur hexagonal

```
cmd/ (Cobra: api | worker | migrate | healthcheck)
  └─ internal/delivery/ (composition root: server.go, worker.go, clients.go)
       └─ internal/notification/
            ├─ port/
            │    ├─ inbound/   IHealthUsecase [impl], INotifyUsecase [target],
            │    │             ISubscriptionUsecase [target]
            │    └─ outbound/  IDependencyChecker [impl], IDeliveryRepository [target],
            │                  ISubscriptionRepository [target], IEmailSender [target],
            │                  IPushSender [target], ITemplateRenderer [target]
            ├─ application/usecase/  (satu usecase per file)
            ├─ domain/ (entity, valueobject, dto, mapper — stdlib only)
            └─ infrastructure/ (http, healthcheck [impl]; postgres, smtp, fcm,
                                rabbitmq, template [target])
```

Aturan (structure.md + code-principles): interface berawalan `I`, hanya di
`port/`; usecase bergantung pada port (DIP); konstruktor mengembalikan struct
konkret; `net/http` + mux Go 1.22; handler mengembalikan DTO lewat `domain/mapper`,
tidak pernah entity.

### 1.1 Dua verb, satu image

- `notification api` → `RunServer` (butuh Postgres; RabbitMQ untuk publish tidak
  dipakai — Notification tidak memproduksi event). Melayani push-subscription API
  + `GET /health`.
- `notification worker` → `RunWorker` (butuh Postgres + RabbitMQ). Mendaftarkan
  consumer booking.* dan queue.turn.*; juga menyajikan `GET /health` di
  `APP_PORT` (sudah ada di boilerplate).

## 2. Model data (PostgreSQL, schema `notification`)

Dibumikan ke migrasi nyata `deployments/migrations/0001_init.up.sql` (v4.6).
**File migrasi yang sudah diterapkan tidak boleh diubah**; perubahan skema lewat
migrasi baru `0002_*`.

### 2.1 `deliveries` — dedup + status kirim dalam satu tabel

```sql
CREATE TABLE deliveries (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_message_id   uuid NOT NULL,
  notification_type   text NOT NULL CHECK (notification_type IN
     ('BOOKING_PAID','BOOKING_EXPIRED','BOOKING_REVIEW','TURN_SOON','TURN_ARRIVED')),
  channel             text NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL','PUSH')),
  booking_id          uuid,
  queue_entry_id      uuid,
  recipient_hash      bytea NOT NULL,
  status              text NOT NULL CHECK (status IN ('PENDING','SENDING','SENT','RETRY','DEAD')),
  attempts            integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  provider_message_id text,
  last_error          text,
  next_attempt_at     timestamptz NOT NULL DEFAULT now(),
  sent_at             timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz,                       -- push telat dibuang
  UNIQUE (source_message_id, notification_type),         -- kunci idempotensi
  CHECK ((booking_id IS NOT NULL) OR (queue_entry_id IS NOT NULL)),
  CHECK ((channel = 'PUSH') = (notification_type IN ('TURN_SOON','TURN_ARRIVED')))
);
CREATE INDEX idx_deliveries_retry ON deliveries(status, next_attempt_at);
```

Keputusan kunci (v4.6, §9.8): **tidak ada tabel `consumer_inbox`**. Baris
`deliveries` sendiri adalah dedup key sekaligus catatan status. `UNIQUE
(source_message_id, notification_type)` + `ON CONFLICT DO NOTHING` menggantikan
inbox (lih. `[[idempotency-and-dedup]]`, code-principles "consumer idempoten tanpa
tabel inbox").

### 2.2 `push_subscriptions`

```sql
CREATE TABLE push_subscriptions (
  visitor_id     uuid NOT NULL,
  event_id       uuid NOT NULL,
  platform       text NOT NULL CHECK (platform IN ('ANDROID','IOS')),
  push_token_enc bytea NOT NULL,     -- dienkripsi; token bukan data publik
  created_at     timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  PRIMARY KEY (visitor_id, event_id)
);
```

> Divergensi SDD: platform di kode hanya `ANDROID|IOS` (web tidak di-push); SDD
> §4.2 menyebut `WEB` — kode (migrasi v4.4) menang.

## 3. Event yang di-consume

Exchange **`rusuh.events`** (topic), per `RABBITMQ_EXCHANGE`. Envelope mengikuti
`pkg/rabbitmq.Envelope` (`messageId`, `eventType`, `schemaVersion`, `occurredAt`,
`producer`, `aggregateId`, `correlationId`, `payload`).

| Event (routing key) | Producer | notification_type | channel |
|---------------------|----------|-------------------|---------|
| `booking.paid` | Booking | BOOKING_PAID | EMAIL |
| `booking.expired` | Booking | BOOKING_EXPIRED | EMAIL |
| `booking.review` | Booking | BOOKING_REVIEW | EMAIL |
| `queue.turn.soon` | Queue | TURN_SOON | PUSH |
| `queue.turn.arrived` | Queue | TURN_ARRIVED | PUSH |

Dua subscription (dua queue) sesuai SDD §5.2:

- `notification.booking.events` ← `booking.paid`, `booking.expired`, `booking.review`
- `notification.queue.events` ← `queue.turn.soon`, `queue.turn.arrived`

`source_message_id` = `Envelope.MessageID`.

## 4. Desain consumer (jantung service)

Memakai `pkg/rabbitmq` apa adanya: `Connection.Consume(ctx, Subscription, logger)`
men-declare topologi (quorum queue + `.retry.<delay>` + `.dlq` lewat
`DeclareTopology`), QoS prefetch, dan menjalankan `Workers` goroutine. Yang dibangun
Notification hanya `Handler` per event dan mendaftarkan `Subscription` di
`worker.go`.

### 4.1 Wiring (mengisi `jobs()` yang kosong)

`RunWorker` saat ini hanya loop `jobs()` kosong. Target: daftarkan subscription
consumer. Pola yang diusulkan — jalankan `Consume` per subscription sebagai
goroutine di bawah root context, selain tetap menyajikan `/health`:

```go
subs := []rabbitmq.Subscription{
    {Queue: "notification.booking.events",
     RoutingKeys: []string{"booking.paid", "booking.expired", "booking.review"},
     Handler: bookingHandler(deps)},
    {Queue: "notification.queue.events",
     RoutingKeys: []string{"queue.turn.soon", "queue.turn.arrived"},
     Handler: turnHandler(deps)},
}
// setiap Consume → goroutine; batalkan semua saat ctx.Done (graceful-shutdown).
```

### 4.2 Idempotent consumer tanpa inbox (state guard + ON CONFLICT DO NOTHING)

Alur satu handler (per `[[at-least-once-processing]]` + `[[idempotency-and-dedup]]`):

```go
func (h *NotifyHandler) HandleBookingPaid(ctx context.Context, d rabbitmq.Delivery) error {
    var p BookingPaidPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decode booking.paid: %w", err)) // poison → DLQ
    }

    // 1) Guard idempotensi: INSERT ... ON CONFLICT DO NOTHING.
    //    rowsAffected==0 → sudah/ sedang diproses → ack tanpa kirim ulang.
    inserted, err := h.repo.ClaimDelivery(ctx, Delivery{
        SourceMessageID: d.Envelope.MessageID,
        Type:            "BOOKING_PAID", Channel: "EMAIL",
        BookingID:       &p.BookingID,
        RecipientHash:   hmacEmail(p.BuyerEmail),
        Status:          "SENDING",
    })
    if err != nil { return err }                 // transien → retry
    if !inserted { return nil }                  // duplikat → ack (R6)

    // 2) Render + kirim (efek samping non-idempoten dijaga oleh klaim di atas).
    body, rerr := h.tmpl.Render("eticket", p)
    if rerr != nil {
        _ = h.repo.MarkDead(ctx, d.Envelope.MessageID, "BOOKING_PAID", rerr.Error())
        return rabbitmq.Permanent(rerr)          // template rusak → DLQ (R1)
    }
    if serr := h.email.Send(ctx, decrypt(p.BuyerEmail), subjectEticket(p), body); serr != nil {
        _ = h.repo.BumpAttempt(ctx, d.Envelope.MessageID, "BOOKING_PAID", serr.Error())
        return serr                              // SMTP transien → retry queue (R2, R7)
    }

    // 3) Tandai terkirim.
    return h.repo.MarkSent(ctx, d.Envelope.MessageID, "BOOKING_PAID", providerID)
}
```

`ClaimDelivery` (repository Postgres, parameterized):

```sql
INSERT INTO deliveries (source_message_id, notification_type, channel, booking_id,
                        recipient_hash, status, attempts)
VALUES ($1,$2,$3,$4,$5,'SENDING',0)
ON CONFLICT (source_message_id, notification_type) DO NOTHING;
-- rowsAffected: 1 = klaim berhasil, 0 = duplikat (skip)
```

Ini adalah **state guard** code-principles minta: tidak ada tabel inbox; konflik
di kunci unik + `DO NOTHING` = dedup. Karena efek samping (kirim email/push)
**non-idempoten**, klaim baris dilakukan **sebelum** kirim — postur pessimistic
ringan via conditional insert (lih. `[[decision-optimistic-vs-pessimistic-deduplication-posture]]`).

> Catatan race: jika proses crash setelah klaim (`SENDING`) tapi sebelum kirim,
> baris `SENDING` yang tidak lagi diproses dibersihkan oleh job rekonsiliasi
> berbasis `idx_deliveries_retry(status, next_attempt_at)` (reset ke RETRY bila
> `next_attempt_at` lampau), sehingga tidak ada e-tiket yang hilang diam-diam.

### 4.3 Push dengan cek deadline

`queue.turn.arrived`: jika `time.Now().After(claimDeadline)` → ack & buang
(set `expires_at`, tidak kirim); jika tidak ada subscription → ack; jika FCM
melaporkan token invalid → hapus subscription + ack (bukan retry). Selain itu
sama dengan 4.2 (klaim `TURN_ARRIVED`/`PUSH` lalu kirim).

### 4.4 Retry berjenjang & DLQ

Sepenuhnya ditangani `pkg/rabbitmq.Decide` + `handle`:

- Handler mengembalikan error transien → `ActionRetry` → republish ke
  `<queue>.retry.<delay>` (delay dari `RABBITMQ_RETRY_DELAYS=5s,30s,2m`), header
  `x-rbt-attempt` naik. Retry queue ber-TTL + dead-letter kembali ke queue utama.
- `attempt > RABBITMQ_MAX_RETRIES` (default 3) **atau** `IsPermanent(err)` →
  `ActionDeadLetter` → `<queue>.dlq`.
- `rabbitmq.Permanent(err)` untuk payload rusak / schema tak dikenal → langsung
  DLQ, tanpa membakar retry (R7). `safeCall` juga membungkus panic handler sebagai
  Permanent.
- Error-classification: poison (decode/validate/template) = permanent; SMTP/FCM/DB
  unavailable = transien (retry). Lih. `[[error-classification]]`.

Ketahanan tambahan: bila **republish ke retry/DLQ gagal**, `handle` melakukan
`Nack(requeue=true)` agar pesan tidak hilang — menjaga at-least-once meski broker
sedang bermasalah.

## 5. Invarian: notifikasi tidak membatalkan transaksi

Konsekuensi desain (Invarian §6): Notification **tidak** memegang `IUnitOfWork`,
**tidak** menulis stok/uang, **tidak** mempublikasikan event balik ke Booking/
Payment. Kegagalan terburuk = pesan ke DLQ + alert; transaksi sumber tetap final.
Ini membuat pengiriman notifikasi murni *best-effort terhadap transaksi* namun
*required terhadap dirinya sendiri* (retry sampai SENT atau DEAD) — lih.
`[[best-effort-vs-required]]`.

## 6. Adapter outbound

- **SMTP (`infrastructure/smtp`)** — `net/smtp` PlainAuth ke SES (`SMTP_HOST/PORT/
  USER/PASSWORD/FROM`); lokal via Mailpit (port 1025, UI 8025). Timeout per kirim.
  Implementasi `IEmailSender`.
- **FCM (`infrastructure/fcm`)** — HTTP v1 ke `FCM_PROJECT_ID` dengan kredensial
  `FCM_CREDENTIALS_PATH`. Implementasi `IPushSender`; mengenali error
  token-invalid untuk memicu hapus subscription.
- **Template (`infrastructure/template`)** — `text/template` dari `assets/templates/`
  (`eticket`, `expired`, `review`), di-embed via `embed.FS`. Implementasi
  `ITemplateRenderer`; render gagal = permanent.
- **Postgres (`infrastructure/postgres`)** — `pgxpool` (sudah via `pkg/postgres`),
  query parameterized, timeout per query, repository `deliveries` &
  `push_subscriptions`.

## 7. Peta error (HTTP, API push-subscription)

Via `pkg/apperror`, error domain bertipe dipetakan di `infrastructure/http`:

| errorCode | HTTP | Kondisi |
|-----------|------|---------|
| `VALIDATION_ERROR` | 400 | Field tidak valid / `PUSH_TOKEN_INVALID` |
| `EVENT_NOT_FOUND` | 404 | Event tidak ada |
| `SUBSCRIPTION_EXISTS` | 409 | Sudah subscribe (atau upsert idempoten) |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Subscription tidak ada |

Error internal → generic 500 + detail di log (tidak membocorkan internal, per
`[[api-design]]`, `[[security-and-authz]]`). Handler mengembalikan DTO via
`domain/mapper`, bukan entity.

## 8. Konfigurasi (env, D-24) [implemented]

Dari `internal/config/load.go` (tervalidasi fail-fast):

| Env | Default | Catatan |
|-----|---------|---------|
| `APP_ENV` / `APP_PORT` | local / 8084 | |
| `HEALTH_TIMEOUT` / `WORKER_INTERVAL` | 2s / 5s | > 0 wajib |
| `POSTGRES_*` (+`POSTGRES_SCHEMA=notification`) | svc_notification | pool `MAX_CONNS` |
| `RABBITMQ_URL` | amqp://guest:guest@localhost:5672/ | |
| `RABBITMQ_EXCHANGE` / `_TYPE` | rusuh.events / topic | |
| `RABBITMQ_WORKERS` / `_PREFETCH` | 4 / 20 | |
| `RABBITMQ_MAX_RETRIES` | 3 | 0–10; > 0 butuh retry delays |
| `RABBITMQ_RETRY_DELAYS` | 5s,30s,2m | retry berjenjang |
| `RABBITMQ_CONFIRM_TIMEOUT` | 5s | publisher confirm |

Target tambahan (belum di config): `SMTP_*`, `FCM_*`, `TEMPLATE_PATH`,
`PII_*` (kunci HMAC/AES untuk `recipient_hash` & dekripsi email). Semua secret
lewat env/SSM, tidak di-commit (security-privacy.md).

## 9. Observability

Logging `slog` terstruktur (key/value, bawa `correlationId`/`messageId`), tidak
pernah PII/token plaintext (R11). Log keputusan di cabang: "duplikat, skip",
"kirim gagal, retry", "ke DLQ".

Metrik (SDD §9.1) — target:

| Metrik | Tipe | Label |
|--------|------|-------|
| `rbt_notification_deliveries_total` | Counter | type, channel, status |
| `rbt_notification_email_latency` | Histogram | type |
| `rbt_notification_push_latency` | Histogram | type |
| `rbt_notification_consumer_lag` | Gauge | queue |
| `rbt_notification_dlq_size` | Gauge | queue |

Alert (SDD §9.2): `NotificationDLQNotEmpty` (dlq_size>0 5m), `*FailRate`,
`*ConsumerLag`. DLQ tidak kosong = sinyal utama bahwa ada notifikasi yang tidak
terkirim (bukan kegagalan transaksi). Health per `[[health-checks-liveness-readiness]]`:
`/health` deep (ping dependency), shallow saat shutdown untuk drain.

## 10. Strategi testing

Per testing-review.md + `[[testing-strategy]]` (dan working-preferences: unit test
mock **semua** dependency; tidak ada integration test di layer unit):

- **Unit (usecase/handler)** — table-driven testify, mock port outbound dari
  `_mock/` (mockgen). Wajib menguji:
  - TC-MSG-03: event ganda → `ClaimDelivery` konflik → satu email saja (idempoten).
  - TC-MSG-04: gagal transien lalu sukses → `x-rbt-attempt` naik → ack.
  - R1: render e-tiket memuat kode tiket/QR (TC-TKT-01).
  - R3: deadline lewat → skip; token invalid → hapus subscription.
  - Poison payload → `rabbitmq.Permanent` → `Decide` = DeadLetter.
- **Repository (`deliveries`/`push_subscriptions`)** — `ON CONFLICT DO NOTHING`
  mengembalikan rowsAffected benar; diuji dengan mock pgx (`pgxmock`) di layer
  unit; integration adapter (build tag `integration`, Postgres+RabbitMQ nyata)
  terpisah di stage `case-coverage` untuk TC-MSG-01/02 (SMTP down→retry, DLQ
  redrive).
- **Coverage** — dinaikkan dengan test nyata, bukan exclusion; `cmd/`,
  `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan (bootstrap/
  wiring). Ambang 80% (`COVERAGE_MIN`).
- **E2E/BDD** — alur Queue→Booking→Payment→Notification saat flow pertama siap.

## 11. Menjalankan lokal

```bash
make infra keys          # Postgres, RabbitMQ, Mailpit + kunci lokal
cd services/notification && cp .env.example .env
go run ./cmd migrate up
go run ./cmd worker &     # consumer booking.* + queue.turn.*
go run ./cmd api &        # endpoint push-subscription + /health
curl localhost:8084/health
# email lokal muncul di Mailpit http://localhost:8025
```

## 12. Referensi

- LLD: `docs/sdd/SDD-NOTIFICATION-001.md` (§1–§12, Appendix A–D).
- OpenAPI: `docs/contracts/openapi/notification.yaml`.
- SRS §3.2.20–§3.2.21, §3.2.25; Test Plan `docs/test/06-TP-RBT-001.md`.
- Kode: `services/notification/`, `pkg/rabbitmq` (consumer, retry, envelope),
  `pkg/postgres`, `pkg/apperror`, `pkg/eventstore`.
