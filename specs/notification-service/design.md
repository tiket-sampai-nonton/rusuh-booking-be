# Design — Notification Service

Dokumen desain ini menerjemahkan `docs/analisa/04d-SDD-NOTIFICATION-001.md` v1.1 (LLD) ke
rancangan implementasi, **dibumikan ke kode nyata** di `services/notification/`,
kontrak `docs/contracts/`, dan utilitas `pkg/`. Arsitektur: **event-driven
worker** (consumer RabbitMQ) + **REST tipis** (push-subscription API), disusun
hexagonal (ports & adapters).

Standar engineering: klaster resiliensi event-driven — at-least-once +
idempotency + retry/backoff + error-classification + DLQ
(`[[event-driven-worker]]`, `[[at-least-once-processing]]`,
`[[idempotency-and-dedup]]`, `[[retry-and-backoff]]`, `[[error-classification]]`).
Idiom Go per `[[go]]`.

> Status: consumer/adapter/usecase pengiriman masih **boilerplate**
> (`jobs()` mengembalikan `nil`; tidak ada `Subscription` didaftarkan). Desain di
> bawah adalah *target* yang mengisi itu, kecuali bagian bertanda **[implemented]**.

---

## 1. Arsitektur hexagonal

```
cmd/ (Cobra: api | worker | migrate | healthcheck)
  └─ internal/delivery/ (composition root: server.go, worker.go, clients.go, serve.go)
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
konkret (accept interfaces, return structs); `net/http` + mux Go 1.22; handler
mengembalikan DTO lewat `domain/mapper`, tidak pernah entity. Lapisan ditegakkan
`depguard`.

### 1.1 Dua verb, satu image

- `notification api` → `RunServer`. Butuh Postgres (push-subscription) — RabbitMQ
  untuk publish **tidak** dipakai (Notification tidak memproduksi event). Melayani
  push-subscription API + `GET /health`.
- `notification worker` → `RunWorker`. Butuh Postgres + RabbitMQ. Mendaftarkan
  consumer `booking.*` + `queue.turn.*` + `otp.requested`; juga menyajikan `GET /health` di
  `APP_PORT` (sudah ada di boilerplate).

---

## 2. API Endpoints

Base: `http://localhost:8084` (lokal) / `https://api.rusuh.example` (edge).
Keamanan: `visitorHeader` = header `X-Visitor-Token` (kecuali `/health`).
Sumber: `docs/contracts/openapi/notification.yaml`.

### 2.1 `POST /api/v1/push-subscriptions` — subscribe

- **Auth:** `X-Visitor-Token` (wajib). `visitor_id` diturunkan dari token.
- **Request body** (`PushSubscription`):

```json
{
  "eventId": "uuid",
  "platform": "ANDROID",          // ANDROID | IOS
  "pushToken": "fcm-device-token"  // maxLength 4096
}
```

- **Responses:**
  - `204 No Content` — tersimpan terenkripsi (`push_token_enc`), dihapus saat
    penjualan berakhir (`expires_at`). Idempoten: re-subscribe = upsert.
  - `400 VALIDATION_ERROR` — field invalid / `PUSH_TOKEN_INVALID`.

### 2.2 `DELETE /api/v1/push-subscriptions/{eventId}` — unsubscribe

- **Auth:** `X-Visitor-Token` (wajib).
- **Path param:** `eventId` (uuid).
- **Responses:**
  - `204 No Content` — dihapus (atau no-op bila tidak ada; idempoten).

### 2.3 `GET /health` — ops [implemented]

- **Auth:** tidak ada (D-23).
- **Responses** (`Health` schema):

```json
{
  "status": "UP",                        // UP | DOWN
  "service": "notification",
  "checkedAt": "2026-10-20T04:00:00Z",
  "dependencies": [
    { "name": "postgres", "status": "UP", "latencyMs": 3 },
    { "name": "rabbitmq", "status": "UP", "latencyMs": 5 }
  ]
}
```

  - `200` semua dependency UP; `503` ada yang DOWN (field `reason` terisi).

### 2.4 Error envelope (SRS §3.1.4)

```json
{
  "meta": {
    "code": 400,
    "message": "payload tidak valid",
    "requestId": "uuid",
    "errorCode": "VALIDATION_ERROR",
    "details": { "pushToken": "wajib diisi" }
  },
  "data": null
}
```

Tidak pernah memuat SQL, stack trace, secret, atau payload provider.

---

## 3. Data Models (PostgreSQL, schema `notification`)

Dibumikan ke migrasi nyata `deployments/migrations/0001_init.up.sql` (v4.6).
**File migrasi yang sudah diterapkan tidak boleh diubah**; perubahan skema lewat
migrasi baru `0002_*`.

### 3.1 `deliveries` — dedup + status kirim dalam satu tabel

```sql
CREATE TABLE deliveries (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_message_id   uuid NOT NULL,
    notification_type   text NOT NULL CHECK (notification_type IN
       ('BOOKING_PAID','BOOKING_EXPIRED','BOOKING_REVIEW','TURN_SOON','TURN_ARRIVED')),
    channel             text NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL','PUSH')),
    booking_id          uuid,
    queue_entry_id      uuid,
    recipient_hash      bytea NOT NULL,                 -- HMAC email/visitor, bukan plaintext
    status              text NOT NULL CHECK (status IN ('PENDING','SENDING','SENT','RETRY','DEAD')),
    attempts            integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    provider_message_id text,
    last_error          text,
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz,                    -- push telat dibuang (bukan di-retry)
    UNIQUE (source_message_id, notification_type),      -- KUNCI IDEMPOTENSI
    CHECK ((booking_id IS NOT NULL) OR (queue_entry_id IS NOT NULL)),
    CHECK ((channel = 'PUSH') = (notification_type IN ('TURN_SOON','TURN_ARRIVED')))
);
CREATE INDEX idx_deliveries_retry ON deliveries(status, next_attempt_at);
```

**Keputusan kunci (v4.6, §9.8): tidak ada tabel `consumer_inbox`.** Baris
`deliveries` sendiri adalah dedup key sekaligus catatan status.
`UNIQUE (source_message_id, notification_type)` + `ON CONFLICT DO NOTHING`
menggantikan inbox (`[[idempotency-and-dedup]]`; code-principles "consumer
idempoten tanpa tabel inbox"). Dua CHECK terakhir menjamin konsistensi:
email/push harus punya referensi yang benar, dan channel PUSH persis untuk tipe
TURN_*.

### 3.2 `push_subscriptions`

```sql
CREATE TABLE push_subscriptions (
    visitor_id     uuid NOT NULL,
    event_id       uuid NOT NULL,
    platform       text NOT NULL CHECK (platform IN ('ANDROID','IOS')),
    push_token_enc bytea NOT NULL,      -- dienkripsi; token bukan data publik
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,-- dihapus setelah penjualan berakhir
    PRIMARY KEY (visitor_id, event_id)
);
```

> Divergensi SDD: platform di kode hanya `ANDROID|IOS` (web tidak di-push); SDD
> §4.2 menyebut `WEB` — migrasi v4.4 menang.

---

## 4. Consumer Jobs (jantung service)

Memakai `pkg/rabbitmq` apa adanya: `Connection.Consume(ctx, Subscription, logger)`
men-declare topologi (quorum queue + `.retry.<delay>` + `.dlq` lewat
`DeclareTopology`), set QoS prefetch, menjalankan `cfg.Workers` goroutine, dan
memanggil `handle` (yang melakukan retry/DLQ via `Decide`). Yang Notification
bangun hanya `Handler` per event + mendaftarkan `Subscription`.

### 4.1 Wiring (mengisi `jobs()`/`Subscription` yang kosong)

`RunWorker` kini loop `jobs()` yang `return nil`. Target: daftarkan dua
subscription dan jalankan tiap `Consume` sebagai goroutine di bawah root context
(graceful-shutdown saat `ctx.Done`), selain tetap menyajikan `/health`:

```go
// Nama queue & routing key berversi mengikuti SDD-RBT §4.6.2 (K8); satu queue per tujuan
// sehingga DLQ dan redrive dapat dilakukan per jenis notifikasi.
subs := []rabbitmq.Subscription{
    {Queue: "notification.booking-paid",    RoutingKeys: []string{"booking.paid.v1"},    Handler: bookingPaid(deps)},
    {Queue: "notification.booking-expired", RoutingKeys: []string{"booking.expired.v1"}, Handler: bookingExpired(deps)},
    {Queue: "notification.booking-review",  RoutingKeys: []string{"booking.review.v1"},  Handler: bookingReview(deps)},
    {Queue: "notification.turn-soon",       RoutingKeys: []string{"queue.turn.soon.v1"}, Handler: turnSoon(deps)},
    {Queue: "notification.turn-arrived",    RoutingKeys: []string{"queue.turn.arrived.v1"}, Handler: turnArrived(deps)},
    // OQ-4: schema otp.requested.v1 belum ada di kontrak; nama queue dari SDD-NOTIFICATION §5.4
    {Queue: "notification.otp",             RoutingKeys: []string{"otp.requested.v1"},   Handler: otpHandler(deps)}, // tanpa retry
}
for _, s := range subs {
    s := s
    go func() {
        if err := conn.Consume(ctx, s, logger); err != nil {
            logger.Error("consume berhenti", "queue", s.Queue, "error", err)
        }
    }()
}
```

`source_message_id` = `Envelope.MessageID`. Handler mem-*dispatch* per
`Delivery.RoutingKey`.

### 4.2 Peta job consumer

| Routing key | Queue | notification_type | channel | Aksi |
|-------------|-------|-------------------|---------|------|
| `booking.paid.v1` | `notification.booking-paid` | BOOKING_PAID | EMAIL | render `eticket` → kirim email |
| `booking.expired.v1` | `notification.booking-expired` | BOOKING_EXPIRED | EMAIL | render `expired` → kirim email |
| `booking.review.v1` | `notification.booking-review` | BOOKING_REVIEW | EMAIL | render `review` → kirim email |
| `queue.turn.soon.v1` | `notification.turn-soon` | TURN_SOON | PUSH | lookup subscription → kirim push |
| `queue.turn.arrived.v1` | `notification.turn-arrived` | TURN_ARRIVED | PUSH | cek deadline → lookup → kirim push |
| `otp.requested.v1` | `notification.otp` | — (tidak dicatat) | EMAIL | decrypt emailEnc/codeEnc → render otp_login → kirim email |

**Keputusan (kode DEV-2):** OTP **tidak** dicatat di `deliveries` (CHECK `notification_type` tidak memuat `OTP_LOGIN`). Dedup OTP tidak diperlukan karena kode baru selalu dibuat per permintaan dan email OTP tidak di-retry. Migrasi `0002` hanya dibutuhkan bila nanti pelacakan OTP diminta.

### 4.3 Handler logic dengan idempotency (state guard + ON CONFLICT)

Alur satu handler email (`[[at-least-once-processing]]` + `[[idempotency-and-dedup]]`):

```go
func (h *NotifyHandler) HandleBookingPaid(ctx context.Context, d rabbitmq.Delivery) error {
    var p BookingPaidPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decode booking.paid: %w", err)) // poison → DLQ
    }
    if err := p.Validate(); err != nil {
        return rabbitmq.Permanent(err)                                        // payload tak lengkap → DLQ
    }

    // 1) Guard idempotensi: klaim baris sebelum efek samping non-idempoten.
    inserted, err := h.repo.ClaimDelivery(ctx, Delivery{
        SourceMessageID: d.Envelope.MessageID,
        Type:            "BOOKING_PAID", Channel: "EMAIL",
        BookingID:       &p.BookingID,
        RecipientHash:   hmacEmail(p.BuyerEmail),
        Status:          "SENDING",
    })
    if err != nil { return err }     // transien (DB) → retry
    if !inserted { return nil }      // duplikat → ack (R8)

    // 2) Render + kirim.
    body, rerr := h.tmpl.Render("eticket", p)
    if rerr != nil {
        _ = h.repo.MarkDead(ctx, d.Envelope.MessageID, "BOOKING_PAID", rerr.Error())
        return rabbitmq.Permanent(rerr)                                       // template rusak → DLQ
    }
    providerID, serr := h.email.Send(ctx, p.BuyerEmail, subjectEticket(p), body)
    if serr != nil {
        _ = h.repo.BumpAttempt(ctx, d.Envelope.MessageID, "BOOKING_PAID", serr.Error())
        return serr                                                           // SMTP transien → retry
    }

    // 3) Tandai terkirim.
    return h.repo.MarkSent(ctx, d.Envelope.MessageID, "BOOKING_PAID", providerID)
}
```

`ClaimDelivery` (repository Postgres, parameterized):

```sql
-- Klaim kondisional: baris baru, atau lanjutan percobaan (RETRY/PENDING), atau SENDING basi (lease habis).
INSERT INTO deliveries (source_message_id, notification_type, channel, booking_id, queue_entry_id,
                        recipient_hash, status, attempts, next_attempt_at)
VALUES ($1,$2,$3,$4,$5,$6,'SENDING',1, now() + $lease)
ON CONFLICT (source_message_id, notification_type) DO UPDATE
   SET status = 'SENDING', attempts = deliveries.attempts + 1, next_attempt_at = now() + $lease
 WHERE deliveries.status IN ('PENDING','RETRY')
    OR (deliveries.status = 'SENDING' AND deliveries.next_attempt_at < now())
RETURNING id;
-- baris kembali = klaim berhasil (kirim); kosong = SENT/DEAD/SENDING aktif → ack tanpa kirim (R8)
```
`BumpAttempt` (kirim gagal transien) → `status = 'RETRY'`, `last_error` (tanpa PII); `MarkDead` →
`DEAD`; `MarkSent` → `SENT`, `sent_at`, `provider_message_id`. `$lease` ≥ timeout SMTP + margin
[nilai: 2 × `SMTP_TIMEOUT`, keputusan desain].

Ini **state guard** yang code-principles minta: tidak ada tabel inbox; kunci unik + update kondisional
= dedup. Klaim dilakukan **sebelum** kirim (efek samping non-idempoten).

> **Perbaikan dari spec lama:** versi `ON CONFLICT DO NOTHING` membuat redelivery dari
> `<queue>.retry.*` selalu dianggap duplikat sehingga email tidak pernah dikirim ulang setelah SMTP
> pulih (melanggar R2.2, TC-MSG-01/04). Crash setelah klaim tetapi sebelum kirim: pesan belum di-ack →
> broker mengirim ulang → baris `SENDING` dengan lease habis diklaim ulang; tidak perlu job reset terpisah.

### 4.4 Handler push dengan cek deadline (`queue.turn.arrived`)

```go
func (h *NotifyHandler) HandleTurnArrived(ctx context.Context, d rabbitmq.Delivery) error {
    var p TurnArrivedPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(err)
    }
    if time.Now().After(p.ClaimDeadline) {
        // push telat tidak berguna: catat expires_at, ack & buang (R5)
        _ = h.repo.MarkExpired(ctx, d.Envelope.MessageID, "TURN_ARRIVED")
        return nil
    }
    sub, err := h.subs.Get(ctx, p.VisitorID, p.EventID)
    if err != nil { return err }          // transien → retry
    if sub == nil { return nil }          // tidak ada subscription → ack (R4/R5)

    inserted, err := h.repo.ClaimDelivery(ctx, Delivery{
        SourceMessageID: d.Envelope.MessageID, Type: "TURN_ARRIVED", Channel: "PUSH",
        QueueEntryID: &p.QueueEntryID, RecipientHash: hmacVisitor(p.VisitorID), Status: "SENDING",
    })
    if err != nil { return err }
    if !inserted { return nil }           // duplikat → ack (R8)

    msg := fcm.Message{
        Token: sub.Token,  // didekripsi dari push_token_enc
        Notification: fcm.Notification{Title: "Giliran Anda Tiba!",
            Body: fmt.Sprintf("Segera claim dalam %d menit", minsUntil(p.ClaimDeadline))},
        Data: map[string]string{
            "eventId": p.EventID, "queueEntryId": p.QueueEntryID,
            "type": "TURN_ARRIVED", "claimDeadline": p.ClaimDeadline.Format(time.RFC3339),
        },
    }
    providerID, serr := h.push.Send(ctx, msg)
    if serr != nil {
        if fcm.IsTokenInvalid(serr) {
            _ = h.subs.Delete(ctx, p.VisitorID, p.EventID) // bersihkan token mati
            _ = h.repo.MarkDead(ctx, d.Envelope.MessageID, "TURN_ARRIVED", serr.Error())
            return nil                                      // ack, bukan retry (R4/R5)
        }
        _ = h.repo.BumpAttempt(ctx, d.Envelope.MessageID, "TURN_ARRIVED", serr.Error())
        return serr                                         // FCM transien → retry
    }
    return h.repo.MarkSent(ctx, d.Envelope.MessageID, "TURN_ARRIVED", providerID)
}
```

`queue.turn.soon` sama, tanpa cek deadline dan dengan judul/body "Giliran
Sebentar Lagi".

### 4.5 Handler OTP (`otp.requested`) — time-sensitive, no retry

```go
func (h *NotifyHandler) HandleOTPRequested(ctx context.Context, d rabbitmq.Delivery) error {
    var p OTPRequestedPayload
    if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decode otp.requested: %w", err))
    }

    // Cek waktu — OTP yang sudah expired tidak perlu dikirim
    if time.Now().After(p.ExpiresAt) {
        h.log.Warn("OTP expired, skip", "messageId", d.Envelope.MessageID)
        return nil // ack, buang
    }

    // Dekripsi email dan kode menggunakan PII_ENC_KEY (shared dengan User Service)
    email, err := h.pii.UnsealEmail(p.EmailEnc)
    if err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decrypt email: %w", err))
    }
    code, err := h.pii.UnsealOTP(p.CodeEnc)
    if err != nil {
        return rabbitmq.Permanent(fmt.Errorf("decrypt code: %w", err))
    }

    // Render template
    body, err := h.tmpl.Render("otp_login", map[string]any{
        "Code":      code,
        "ExpiresAt": p.ExpiresAt,
        "Locale":    p.Locale,
    })
    if err != nil {
        return rabbitmq.Permanent(err)
    }

    // Kirim email — TIDAK retry (time-sensitive)
    _, err = h.email.Send(ctx, email, "Kode Login Anda", body)
    if err != nil {
        h.log.Error("OTP email failed", "error", err) // log error only, no retry
        return nil // ack anyway — OTP sudah di Valkey, user bisa request ulang
    }

    return nil
}
```

> **Catatan:** OTP email tidak menggunakan retry queue karena time-sensitive (TTL 5 menit). Jika SMTP gagal, user dapat meminta OTP baru. Event langsung di-ack setelah attempt pertama.

---

## 5. Email Templates

Lokasi: `assets/templates/` (embed via `embed.FS`), dirender `text/template`
oleh `ITemplateRenderer`. Render gagal = `rabbitmq.Permanent` (poison → DLQ).

### 5.1 `eticket` — dipicu `booking.paid`

Variabel (dari `booking.paid.v1` payload): `.PublicRef`, `.Event.Name`,
`.Event.StartsAt`, `.Event.Venue`, `.Tickets[]{.TicketCode,.AttendeeName,
.AttendeeNikMasked,.CategoryCode}`, `.TicketLinkToken`.

```
Subject: [RBT] E-Tiket Anda — {{.Event.Name}}

Halo,

Pembayaran Anda berhasil! Referensi: {{.PublicRef}}

Event : {{.Event.Name}}
Tanggal: {{.Event.StartsAt}}
Venue  : {{.Event.Venue}}

Tiket:
{{range .Tickets}}- {{.CategoryCode}} | {{.TicketCode}} | {{.AttendeeName}} | {{.AttendeeNikMasked}}
{{end}}
Lihat e-tiket: {{.TicketLinkToken}}

Tunjukkan kode tiket di pintu masuk. Terima kasih — Tim Rusuh Booking Tiket
```

### 5.2 `expired` — dipicu `booking.expired`

Variabel (dari `booking.expired.v1`): `.PublicRef`, `.Reason`
(`PAYMENT_TIMEOUT|SESSION_EXPIRED|CANCELLED`).

```
Subject: [RBT] Booking Kedaluwarsa — {{.PublicRef}}

Halo,

Booking Anda ({{.PublicRef}}) berakhir: {{.Reason}}.
Silakan coba lagi jika tiket masih tersedia.

Terima kasih — Tim Rusuh Booking Tiket
```

### 5.3 `review` — dipicu `booking.review`

Variabel (dari `booking.review.v1`): `.BookingId`, `.AmountMinor` (format IDR),
`.AttemptHint` (opsional).

```
Subject: [RBT] Pembayaran Perlu Ditinjau

Halo,

Kami mendeteksi pembayaran yang perlu ditinjau.
Booking : {{.BookingId}}
Jumlah  : Rp {{formatIDR .AmountMinor}}
{{if .AttemptHint}}Catatan : {{.AttemptHint}}{{end}}

Tim kami akan memproses dalam 3–5 hari kerja. Terima kasih — Tim Rusuh Booking Tiket
```

### 5.4 `otp_login` — dipicu `otp.requested`

Variabel (dari `otp.requested.v1` payload, setelah dekripsi): `.Code`, `.ExpiresAt`, `.Locale`.

```
Subject: Kode Login Anda

Halo,

Kode login Anda: {{.Code}}

Kode ini berlaku hingga {{formatTime .ExpiresAt}}.
Jangan bagikan kode ini kepada siapa pun.

Terima kasih — Tim Rusuh Booking Tiket
```

---

## 6. Push Notification (FCM)

Adapter `infrastructure/fcm` (`IPushSender`) memakai FCM HTTP v1 dengan
`FCM_PROJECT_ID` dan kredensial `FCM_CREDENTIALS_PATH`. Token didekripsi dari
`push_subscriptions.push_token_enc` saat kirim. Error token-invalid dikenali
(`fcm.IsTokenInvalid`) untuk memicu hapus subscription.

### 6.1 Payload `queue.turn.soon`

```json
{
  "token": "<decrypted fcm token>",
  "notification": { "title": "Giliran Sebentar Lagi", "body": "Bersiap, giliran Anda hampir tiba" },
  "data": { "eventId": "uuid", "queueEntryId": "uuid", "type": "TURN_SOON" }
}
```

### 6.2 Payload `queue.turn.arrived`

```json
{
  "token": "<decrypted fcm token>",
  "notification": { "title": "Giliran Anda Tiba!", "body": "Segera claim dalam 3 menit" },
  "data": {
    "eventId": "uuid",
    "queueEntryId": "uuid",
    "type": "TURN_ARRIVED",
    "claimDeadline": "2026-10-20T04:03:00Z"
  }
}
```

Guard kirim: (1) `claimDeadline` belum lewat (hanya arrived); (2) subscription
ada; (3) token valid — selain itu skip/hapus (lihat §4.4).

---

## 7. Retry & DLQ

Sepenuhnya ditangani `pkg/rabbitmq.Decide` + `Connection.handle`:

- **Topologi** (`DeclareTopology`): queue utama quorum + satu retry queue per
  delay unik (`<queue>.retry.<delay>` dengan `x-message-ttl` = delay dan
  dead-letter kembali ke queue utama) + `<queue>.dlq`.
- **Transien → retry:** `Decide` mengembalikan `ActionRetry` → republish ke
  `<queue>.retry.<delay>`; tier = `delays[attempt-1]` dari
  `RABBITMQ_RETRY_DELAYS=5s,30s,2m`; header `x-rbt-attempt` dinaikkan,
  `x-rbt-last-error` dan `x-rbt-queue` diset.
- **Habis / permanen → DLQ:** `attempt > RABBITMQ_MAX_RETRIES` (default 3) **atau**
  `IsPermanent(err)` → `ActionDeadLetter` → `<queue>.dlq`.
- **Poison:** `rabbitmq.Permanent(err)` untuk payload rusak / schema tak dikenal /
  template gagal → langsung DLQ tanpa membakar retry. `safeCall` membungkus panic
  handler sebagai Permanent juga.
- **Tahan broker-flaky:** bila republish ke retry/DLQ gagal, `handle` melakukan
  `Nack(requeue=true)` agar pesan tidak hilang (menjaga at-least-once).
- **Error-classification** (`[[error-classification]]`): decode/validate/template =
  permanent; SMTP/FCM/DB unavailable = transien.

Semantik `Decide` (dari `pkg/rabbitmq/retry.go`): `attempt` mulai dari 1; gagal
ke-1 → retry tier 1 (5s), ke-2 → 30s, ke-3 → 2m, ke-4 (`> maxRetries=3`) → DLQ.

---

## 8. Delivery State Machine

```
            ClaimDelivery (insert, atau update kondisional dari PENDING/RETRY/SENDING-basi)
  (new) ───────────────────────────────────► SENDING
                                                 │
                 kirim sukses ──────────────────►│──► SENT   (sent_at, provider_message_id)
                                                 │
                 transien (SMTP/FCM/DB) ────────►│──► RETRY  (attempts++, last_error, next_attempt_at)
                                                 │             │  (republish ke retry queue)
                                                 │             └──► SENDING (retry diproses ulang)
                                                 │
                 permanen / attempts>max ───────►│──► DEAD   (last_error; pesan ke DLQ)
                                                 │
                 push telat (claimDeadline lewat)►│──► (expires_at diset; ack & buang)
```

- `PENDING` = default kolom (default `now()` pada `next_attempt_at`); handler
  langsung mengklaim ke `SENDING`.
- `RETRY` ↔ `SENDING` mengikuti siklus retry queue RabbitMQ.
- `SENT` dan `DEAD` adalah terminal. `DEAD` memicu alert DLQ, bukan rollback
  transaksi sumber (Invarian §6).

---

## 9. Invarian §6 — notifikasi tidak membatalkan transaksi

Konsekuensi desain: Notification **tidak** memegang `IUnitOfWork`, **tidak**
menulis stok/uang, **tidak** mempublikasikan event balik ke Booking/Payment.
Kegagalan terburuk = pesan ke DLQ + alert; transaksi sumber tetap final.
Pengiriman = *best-effort terhadap transaksi*, *required terhadap dirinya
sendiri* (`[[best-effort-vs-required]]`).

---

## 10. Event Contracts (consumed)

Exchange **`rusuh.events`** (topic). Envelope `pkg/rabbitmq.Envelope` /
`envelope.schema.json`:

```json
{
  "messageId": "uuid",
  "eventType": "booking.paid",
  "schemaVersion": 1,
  "occurredAt": "2026-10-20T04:00:00Z",
  "producer": "booking-service",      // queue-service | booking-service | payment-service
  "aggregateId": "uuid",
  "correlationId": "uuid",
  "payload": { }
}
```

`source_message_id` = `messageId`. Berikut payload nyata (dari
`docs/contracts/events/`, **menggantikan** SDD Appendix A yang usang):

### 10.1 `booking.paid.v1` (producer `booking-service`)

```json
{
  "bookingId": "uuid",
  "publicRef": "RBT-ABC123",
  "buyerEmail": "buyer@example.id",
  "event": { "eventId": "uuid", "name": "Konser ABC", "startsAt": "2026-11-15T19:00:00Z", "venue": "GBK" },
  "tickets": [
    { "ticketCode": "TKT-001", "attendeeName": "Budi", "attendeeNikMasked": "3174********0001", "categoryCode": "VIP" }
  ],
  "ticketLinkToken": "opaque-token"
}
```
Required: `bookingId, publicRef, buyerEmail, tickets, ticketLinkToken`.
`additionalProperties: false`. Tidak ada NIK plaintext/secret.

### 10.2 `booking.expired.v1` (producer `booking-service`)

```json
{ "bookingId": "uuid", "publicRef": "RBT-ABC123", "buyerEmail": "buyer@example.id", "reason": "PAYMENT_TIMEOUT" }
```
Required: `bookingId, publicRef, buyerEmail, reason`. `reason ∈ {PAYMENT_TIMEOUT, SESSION_EXPIRED, CANCELLED}`.

### 10.3 `booking.review.v1` (producer `booking-service`)

```json
{ "bookingId": "uuid", "attemptHint": "late-webhook", "buyerEmail": "buyer@example.id", "amountMinor": 3000000 }
```
Required: `bookingId, buyerEmail, amountMinor`. `amountMinor` = minor unit IDR (int64 ≥ 0).

### 10.4 `queue.turn.soon.v1` (producer `queue-service`)

```json
{ "eventId": "uuid", "queueEntryId": "uuid", "visitorId": "uuid" }
```
Required: semua. Token push **tidak** di payload → dilookup dari `push_subscriptions(visitor_id, event_id)`.

### 10.5 `queue.turn.arrived.v1` (producer `queue-service`)

```json
{ "eventId": "uuid", "queueEntryId": "uuid", "visitorId": "uuid", "claimDeadline": "2026-10-20T04:03:00Z" }
```
Required: semua. `claimDeadline` → guard §4.4.

### 10.6 `otp.requested.v1` (producer `user-service`)

```json
{
  "emailEnc": "base64(AES-256-GCM encrypted email)",
  "codeEnc": "base64(AES-256-GCM encrypted 6-digit code)",
  "expiresAt": "2026-10-20T13:50:00Z",
  "locale": "id"
}
```
Required: `emailEnc, codeEnc, expiresAt`. `locale` default `id`. Email dan kode **dienkripsi** — Notification Service mendekripsi menggunakan `PII_ENC_KEY` yang sama dengan User Service.

---

## 11. Sequence Diagrams

### 11.1 Email flow (`booking.paid`)

```
Booking ──booking.paid.v1──► rusuh.events ──► notification.booking-paid
                                                      │
                                           worker (Consume → handle)
                                                      │
                        unmarshal + validate payload ─┤ (gagal → Permanent → DLQ)
                                                      │
                 ClaimDelivery (klaim kondisional §4.3) ┤
                        ├─ kosong ─► ack (SENT/DEAD/SENDING aktif, R8)
                        └─ id ─────► SENDING
                                                      │
                             tmpl.Render("eticket") ──┤ (gagal → MarkDead + Permanent → DLQ)
                                                      │
                                   SMTP Send (SES) ───┤
                        ├─ error transien ─► BumpAttempt + return err ─► retry queue (5s→30s→2m) ─► DLQ
                        └─ sukses ─► MarkSent(SENT, provider_message_id) ─► ack
```

### 11.2 Push flow (`queue.turn.arrived`)

```
Queue ──queue.turn.arrived.v1──► rusuh.events ──► notification.turn-arrived
                                                      │
                                           worker (Consume → handle)
                                                      │
                        now > claimDeadline? ─ ya ───► MarkExpired + ack (buang, R5)
                               │ tidak
             subs.Get(visitorId,eventId) ─ nil ─────► ack (tidak ada subscription, R4/R5)
                               │ ada
                 ClaimDelivery (kondisional) ─ kosong ► ack (R8)
                               │ id (SENDING)
                     fcm.Send(push) ────────┤
                        ├─ token invalid ─► subs.Delete + MarkDead + ack
                        ├─ transien ──────► BumpAttempt + return err ─► retry/DLQ
                        └─ sukses ─► MarkSent(SENT) ─► ack
```

### 11.3 OTP email flow (`otp.requested`)

```
User ──otp.requested.v1──► rusuh.events ──► notification.otp      (schema: OQ-4)
                                                │
                                     worker (Consume → handle)
                                                │
               now > expiresAt? ─ ya ──────────► ack (buang, expired)
                      │ tidak
        pii.UnsealEmail(emailEnc) ─ err ───────► Permanent → DLQ
        pii.UnsealOTP(codeEnc)    ─ err ───────► Permanent → DLQ
                      │ ok
           tmpl.Render("otp_login") ─ err ─────► Permanent → DLQ
                      │ ok
                SMTP Send ─────────┤
                    ├─ error ──────► log + ack (no retry, time-sensitive)
                    └─ sukses ─────► ack
```

---

## 12. Error Catalog

### 12.1 API push-subscription (HTTP, via `pkg/apperror`)

| errorCode | HTTP | Kondisi |
|-----------|------|---------|
| `VALIDATION_ERROR` | 400 | Field invalid / `pushToken` kosong / `platform` di luar `ANDROID\|IOS` |
| `PUSH_TOKEN_INVALID` | 400 | Token FCM tidak valid saat register |
| `EVENT_NOT_FOUND` | 404 | Event tidak ada (bila divalidasi) |
| `SUBSCRIPTION_EXISTS` | 409 | Sudah subscribe (bila bukan upsert) |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Unsubscribe tanpa subscription (bila bukan no-op) |
| (internal) | 500 | Error tak terduga → generic; detail hanya di log |

Handler mengembalikan DTO via `domain/mapper`, bukan entity; error envelope tak
pernah membocorkan SQL/stack trace/secret (`[[api-design]]`, `[[security-and-authz]]`).

### 12.2 Consumer (klasifikasi → tindakan)

| Kondisi | Klasifikasi | Tindakan |
|---------|-------------|----------|
| Decode envelope/payload gagal | Permanent | DLQ (`rabbitmq.Permanent`) |
| `Envelope.Validate()` gagal | Permanent | DLQ |
| Payload tak lengkap / schema tak dikenal | Permanent | DLQ |
| Template gagal render | Permanent | `MarkDead` + DLQ |
| FCM token invalid | "skip" | hapus subscription + `MarkDead` + ack |
| `claimDeadline` lewat | "skip" | `MarkExpired` + ack |
| SMTP/FCM/DB unavailable | Transien | `BumpAttempt` + retry queue (5s→30s→2m) |
| Attempt > `MAX_RETRIES` (3) | — | DLQ |
| Panic handler | Permanent | DLQ (`safeCall`) |
| Republish retry/DLQ gagal | — | `Nack(requeue=true)` (tidak hilang) |

---

## 13. Adapter outbound

- **SMTP (`infrastructure/smtp`)** — `net/smtp` PlainAuth ke SES
  (`SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TIMEOUT`, nama SDD-RBT §6.1); lokal via Mailpit (SMTP 1025, UI 8025).
  Dibungkus `pkg/resilience` breaker `smtp` (timeout 3 s, ambang 5, cooldown 30 s, probe 1, bulkhead 10 —
  PROPOSED SDD-RBT §4.6.2). Implementasi `IEmailSender`.
- **Push (`infrastructure/fcm`, `infrastructure/pushlog`)** — `PUSH_PROVIDER=fcm`: HTTP v1 ke
  `FCM_PROJECT_ID` (`FCM_CREDENTIALS_PATH`); `PUSH_PROVIDER=log` (lokal/CI, SRS-IF-23): menulis
  "push terkirim" ke log tanpa token. Implementasi `IPushSender`; `IsTokenInvalid` memicu hapus subscription.
- **Template (`infrastructure/template`)** — `text/template` dari `assets/templates/`
  (`eticket`, `expired`, `review`) di-embed `embed.FS`. Implementasi `ITemplateRenderer`.
- **Postgres (`infrastructure/postgres`)** — `pgxpool` (`pkg/postgres`), query
  parameterized, timeout per query, repository `deliveries` & `push_subscriptions`.

---

## 14. Konfigurasi (env, D-24) — sebagian implemented

Dari `internal/config/load.go` (fail-fast; semua error dilaporkan sekaligus):

| Env | Default | Catatan |
|-----|---------|---------|
| `APP_ENV` / `APP_NAME` / `APP_PORT` | local / notification / 8084 | `APP_ENV ∈ {local,dev,staging,prod}` |
| `LOG_LEVEL` / `LOG_FORMAT` | info / json | |
| `HTTP_SHUTDOWN_TIMEOUT` / `HEALTH_TIMEOUT` / `WORKER_INTERVAL` | 15s / 2s / 5s | semua > 0 wajib |
| `POSTGRES_*` (+`POSTGRES_SCHEMA=notification`) | svc_notification, db `rbt` | pool `MAX_CONNS=10` |
| `RABBITMQ_URL` | amqp://guest:guest@localhost:5672/ | |
| `RABBITMQ_EXCHANGE` / `_TYPE` | **rusuh.events** / topic | |
| `RABBITMQ_WORKERS` / `_PREFETCH` | 4 / 20 | |
| `RABBITMQ_MAX_RETRIES` | 3 | |
| `RABBITMQ_RETRY_DELAYS` | 5s,30s,2m | retry berjenjang |
| `RABBITMQ_CONFIRM_TIMEOUT` | 5s | publisher confirm |

**Target (belum di config [kode] — spec lama keliru menandai `PII_ENC_KEY` sebagai implemented):**
`SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TIMEOUT`; `PUSH_PROVIDER=fcm|log` (default `log` lokal/CI);
`FCM_PROJECT_ID`, `FCM_CREDENTIALS_PATH`; `PII_ENC_KEY` (hex 32 byte, sama dengan User untuk dekripsi
`otp.requested`); kunci HMAC `recipient_hash`; kunci enkripsi `push_token_enc`; kunci publik Queue
(verifikasi visitor token di API push-subscription); breaker SMTP. Template di-embed (`assets/templates/`),
tanpa `TEMPLATE_PATH`. Semua secret lewat env/SSM.

---

## 15. Observability

Logging `slog` terstruktur (bawa `correlationId`/`messageId`), **tanpa** PII/token
plaintext (R11). Log keputusan cabang: "duplikat, skip", "kirim gagal, retry",
"ke DLQ".

Metrik (SDD §9.1) — target:

| Metrik | Tipe | Label |
|--------|------|-------|
| `rbt_notification_deliveries_total` | Counter | type, channel, status |
| `rbt_notification_email_latency` | Histogram | type |
| `rbt_notification_push_latency` | Histogram | type |
| `rbt_notification_consumer_lag` | Gauge | queue |
| `rbt_notification_dlq_size` | Gauge | queue |

Alert (SDD §9.2): `NotificationDLQNotEmpty` (dlq_size>0 5m), `*FailRate`,
`*ConsumerLag`. DLQ tidak kosong = sinyal utama ada notifikasi tak terkirim
(bukan kegagalan transaksi). Health per
`[[health-checks-liveness-readiness]]`: `/health` deep (ping dependency).

---

## 16. Strategi testing

Per testing-review.md + `[[testing-strategy]]`:

- **Unit (usecase/handler)** — table-driven testify, mock port outbound dari
  `_mock/` (mockgen). Wajib menguji:
  - TC-MSG-03: event ganda → `ClaimDelivery` konflik → satu email saja (idempoten).
  - TC-MSG-04: gagal transien lalu sukses → `x-rbt-attempt` naik → ack.
  - TC-TKT-01/02: render e-tiket memuat `ticketCode`, `attendeeName`, NIK tersamar.
  - R5: deadline lewat → skip; token invalid → hapus subscription.
  - Poison payload → `rabbitmq.Permanent` → `Decide` = DeadLetter.
- **Repository** — klaim kondisional: baris baru → id; `RETRY`/`SENDING` basi → id; `SENT`/`DEAD`/`SENDING` aktif → kosong;
  integration adapter (build tag `integration`, Postgres+RabbitMQ nyata) di stage
  `case-coverage` untuk TC-MSG-01 (SMTP down → retry) dan TC-MSG-02 (DLQ redrive).
- **Coverage** ≥ 80% (`COVERAGE_MIN`); `cmd/`, `internal/delivery/`,
  `internal/config/`, `_mock/` dikecualikan (bootstrap/wiring).
- **E2E/BDD** — alur Queue→Booking→Payment→Notification saat flow pertama siap.

---

## 16a. Correctness Properties

### Property 1: At most one send after success
Untuk setiap jumlah pengiriman ulang pesan yang sama, tidak ada pengiriman tambahan setelah delivery `SENT` tercatat.
**Validates: Requirements 8.1, 8.2, 6.1**

### Property 2: Retry eventually sends
Untuk setiap kegagalan SMTP transien yang pulih sebelum retry terakhir, email akhirnya terkirim tepat satu kali tercatat `SENT`.
**Validates: Requirements 2.2, 9.1, 9.4**

### Property 3: Poison goes straight to DLQ
Untuk setiap payload rusak atau `schemaVersion` tak dikenal, pesan masuk DLQ tanpa melewati retry queue dan delivery (bila ada) `DEAD`.
**Validates: Requirements 1.3, 9.3, 12.3**

### Property 4: Late push discarded
Untuk setiap `queue.turn.arrived.v1` yang diproses setelah `claimDeadline`, tidak ada push terkirim.
**Validates: Requirement 5.2**

### Property 5: Notification failure never affects source
Untuk setiap kegagalan Notification (SMTP/FCM/DB), status booking/payment/akun tidak berubah dan Notification tidak menerbitkan event.
**Validates: Requirements 2.2, 9.2**

### Property 6: No secret in logs
Untuk setiap log record, tidak ada kode OTP, token push, alamat email, atau NIK plaintext.
**Validates: Requirements 11.3, 12.5**

---

## 17. Menjalankan lokal

```bash
make infra keys          # Postgres, RabbitMQ, Mailpit + kunci lokal
cd services/notification && cp .env.example .env
go run ./cmd migrate up
go run ./cmd worker &     # consumer booking.* + queue.turn.*
go run ./cmd api &        # endpoint push-subscription + /health
curl localhost:8084/health
# email lokal muncul di Mailpit http://localhost:8025
```

---

## 18. Referensi

- LLD: `docs/analisa/04d-SDD-NOTIFICATION-001.md` (§1–§12, Appendix A–D).
- OpenAPI: `docs/contracts/openapi/notification.yaml`.
- Event: `docs/contracts/events/{booking.paid,booking.expired,booking.review,queue.turn.soon,queue.turn.arrived}.v1.schema.json`, `envelope.schema.json`.
- SRS §3.2.20–§3.2.21, §3.2.25; Test Plan `docs/analisa/06-TP-RBT-001.md`.
- Kode: `services/notification/`, `pkg/rabbitmq` (consumer, retry, envelope),
  `pkg/postgres`, `pkg/apperror`, `pkg/eventstore`.
