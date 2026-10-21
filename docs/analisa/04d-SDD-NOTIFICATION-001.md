# Software Design Description — Notification Service

| Field | Value |
|-------|-------|
| **Service** | Notification Service |
| **Document ID** | SDD-NOTIFICATION-001 |
| **Version** | 1.1 |
| **Date** | 2026-10-20 |
| **Status** | Draft |
| **Owner** | BE-1 |
| **Port** | 8084 |
| **Schema** | `notification` (role `svc_notification`) |
| **Region** | ap-southeast-3 (Jakarta) |

## Change History

| Version | Date | Author | Summary |
|---------|------|--------|---------|
| 1.1 | 2026-10-20 | BE-1 | Menambahkan consumer otp.requested untuk OTP email login |
| 1.0 | 2026-10-19 | BE-1 | Initial version |

---

## 1. Overview

Notification Service mengelola pengiriman email dan push notification. Service ini consume domain events dan mengirim notifikasi ke pembeli melalui SMTP (SES) dan FCM.

**Prinsip utama:**
- At-least-once delivery — retry sampai berhasil atau masuk DLQ
- Idempotent processing — tidak kirim duplikat
- Template-based — email dari template, mudah diubah

---

## 2. Responsibilities

| Capability | SRS Reference | Description |
|------------|---------------|-------------|
| E-ticket Email | §3.2.20 | Kirim e-tiket setelah payment |
| Expiry Email | §3.2.17 | Kirim notif pembayaran expired |
| Review Email | §3.2.17 | Kirim notif pembayaran terlambat |
| OTP Login Email | §3.2.27 | Kirim kode OTP untuk login |
| Turn Soon Push | §3.2.25 | Push "giliran sebentar lagi" |
| Turn Arrived Push | §3.2.25 | Push "giliran tiba" |
| Delivery Tracking | §3.2.21 | Track status pengiriman |
| Push Subscription | §3.2.25 | Manage FCM token subscription |

---

## 3. API Endpoints (Public)

### 3.1 Subscribe Push Notification

```
POST /api/v1/push-subscriptions
```

**Request:**
```json
{
  "eventId": "uuid",
  "platform": "ANDROID",
  "token": "fcm-device-token"
}
```

**Headers:** `X-Visitor-Token`

### 3.2 Unsubscribe Push Notification

```
DELETE /api/v1/push-subscriptions/{eventId}
```

---

## 4. Data Model (PostgreSQL)

### 4.1 Delivery Tracking

```sql
CREATE TABLE notification.deliveries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  type TEXT NOT NULL,
  channel TEXT NOT NULL,
  recipient TEXT NOT NULL,
  reference_id UUID NOT NULL,
  reference_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'PENDING',
  attempts INT NOT NULL DEFAULT 0,
  last_attempt_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  failed_at TIMESTAMPTZ,
  failure_reason TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_deliveries_reference ON notification.deliveries(reference_type, reference_id);
CREATE INDEX idx_deliveries_status ON notification.deliveries(status) WHERE status = 'PENDING';
```

**Type values:** `ETICKET`, `EXPIRED`, `REVIEW`, `TURN_SOON`, `TURN_ARRIVED`
**Channel values:** `EMAIL`, `PUSH`
**Status values:** `PENDING`, `SENT`, `DELIVERED`, `FAILED`

### 4.2 Push Subscriptions

```sql
CREATE TABLE notification.push_subscriptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id UUID NOT NULL,
  visitor_id UUID NOT NULL,
  platform TEXT NOT NULL,
  token TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (event_id, visitor_id)
);

CREATE INDEX idx_push_subs_event ON notification.push_subscriptions(event_id);
```

**Platform values:** `ANDROID`, `IOS`, `WEB`

---

## 5. Event Consumption

### 5.1 Events Consumed

| Event | Producer | Action |
|-------|----------|--------|
| `booking.paid` | Booking | Send e-ticket email |
| `booking.expired` | Booking | Send expiry email |
| `booking.review` | Booking | Send review email |
| `otp.requested` | User | Decrypt and send OTP email |
| `queue.turn.soon` | Queue | Send push notification |
| `queue.turn.arrived` | Queue | Send push notification |

### 5.2 Consumer Configuration

```
Exchange: rbt.events (topic)
Queue: notification.booking.events
  - booking.paid
  - booking.expired
  - booking.review
Queue: notification.otp (no retry - time-sensitive)
  - otp.requested
Queue: notification.queue.events
  - queue.turn.soon
  - queue.turn.arrived
```

### 5.3 Retry and DLQ

```
notification.booking.events → fail → notification.booking.events.retry.5s → fail → notification.booking.events.dlq
```

**Retry policy:**
- Max 3 attempts
- 5 second delay between retries
- After 3 failures → DLQ

### 5.4 otp.requested Consumer

**Queue:** `notification.otp` (no retry - time-sensitive)

**Action:**
1. Decrypt `emailEnc` and `codeEnc` using shared PII encryption key
2. Render OTP email template with code and expiry
3. Send via SMTP
4. Log delivery (without plaintext code)

**Idempotency:** Skip if `messageId` already processed within 5 minutes (Valkey dedup key)

**On failure:** Log error, do not retry (user will request new code)

### 5.4 Idempotency

Check `deliveries` table before sending:
```sql
SELECT 1 FROM notification.deliveries
WHERE reference_type = $1 AND reference_id = $2 AND type = $3 AND channel = $4
```

If exists, skip (already processed or in progress).

---

## 6. Email Templates

### 6.1 Template Location

```
assets/templates/
├── eticket.txt.tmpl
├── expired.txt.tmpl
└── review.txt.tmpl
```

### 6.2 E-ticket Template

```
Subject: [RBT] E-Tiket Anda untuk {{.EventTitle}}

Halo,

Pembayaran Anda berhasil! Berikut e-tiket Anda:

Event: {{.EventTitle}}
Tanggal: {{.EventDate}}
Venue: {{.VenueName}}

Tiket:
{{range .Tickets}}
- {{.CategoryName}} | {{.PublicCode}} | {{.AttendeeNikMasked}}
{{end}}

Total: Rp {{.TotalAmount}}

Tunjukkan kode tiket atau QR code di pintu masuk.

Terima kasih,
Tim Rusuh Booking Tiket
```

### 6.3 Expired Template

```
Subject: [RBT] Pembayaran Expired - {{.EventTitle}}

Halo,

Maaf, waktu pembayaran Anda untuk event {{.EventTitle}} telah habis.

Booking ID: {{.BookingId}}
Total: Rp {{.Amount}}

Silakan coba lagi jika tiket masih tersedia.

Terima kasih,
Tim Rusuh Booking Tiket
```

### 6.4 Review Template

```
Subject: [RBT] Pembayaran Terlambat - {{.EventTitle}}

Halo,

Kami mendeteksi pembayaran Anda setelah batas waktu.

Booking ID: {{.BookingId}}
Total: Rp {{.Amount}}

Tim kami akan memproses refund dalam 3-5 hari kerja.

Terima kasih,
Tim Rusuh Booking Tiket
```

### 6.5 OTP Login Template

```
Subject: Kode Login Rusuh Booking Tiket

Kode login Anda: {{.Code}}

Kode ini berlaku selama 5 menit.
Jangan bagikan kode ini kepada siapa pun.

Jika Anda tidak meminta kode ini, abaikan email ini.

Terima kasih,
Tim Rusuh Booking Tiket
```

---

## 7. Push Notification

### 7.1 FCM Integration

**Provider:** Firebase Cloud Messaging (FCM)

**Message format:**
```json
{
  "to": "fcm-device-token",
  "notification": {
    "title": "Giliran Anda Tiba!",
    "body": "Segera claim dalam 3 menit"
  },
  "data": {
    "eventId": "uuid",
    "entryId": "uuid",
    "type": "TURN_ARRIVED",
    "claimDeadline": "2026-10-19T10:03:00Z"
  }
}
```

### 7.2 Turn Soon

**Condition:** Position ≤ 10
**Title:** "Giliran Sebentar Lagi"
**Body:** "Bersiap, giliran Anda hampir tiba"

### 7.3 Turn Arrived

**Condition:** Entry status ADMITTED
**Title:** "Giliran Anda Tiba!"
**Body:** "Segera claim dalam 3 menit"

### 7.4 Delivery Check

Before sending push:
1. Check subscription exists
2. Check deadline not passed (for turn.arrived)
3. If deadline passed, skip (entry already UNCLAIMED)

---

## 8. Configuration

```bash
# Application
APP_ENV=development
APP_NAME=notification-service
APP_PORT=8084
LOG_LEVEL=info

# PostgreSQL
DATABASE_URL=postgres://svc_notification:xxx@localhost:5432/rusuh?search_path=notification

# RabbitMQ
RABBITMQ_URL=amqp://guest:guest@localhost:5672/

# SMTP (SES)
SMTP_HOST=email-smtp.ap-southeast-3.amazonaws.com
SMTP_PORT=587
SMTP_USER=AKIAXXXXXXXX
SMTP_PASSWORD=xxxxxx
SMTP_FROM=noreply@rusuh.example

# FCM
FCM_PROJECT_ID=rusuh-booking
FCM_CREDENTIALS_PATH=/secrets/fcm-service-account.json

# Templates
TEMPLATE_PATH=./assets/templates
```

---

## 9. Observability

### 9.1 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rbt_notification_deliveries_total` | Counter | `type`, `channel`, `status` | Delivery attempts |
| `rbt_notification_email_latency` | Histogram | `type` | Email sending latency |
| `rbt_notification_push_latency` | Histogram | `type` | Push sending latency |
| `rbt_notification_consumer_lag` | Gauge | `queue` | Consumer lag |
| `rbt_notification_dlq_size` | Gauge | `queue` | DLQ message count |

### 9.2 Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| NotificationDLQNotEmpty | dlq_size > 0 for 5m | Warning |
| NotificationEmailFailRate | fail_rate > 10% for 10m | Warning |
| NotificationPushFailRate | fail_rate > 20% for 10m | Warning |
| NotificationConsumerLag | lag > 100 for 5m | Warning |

---

## 10. Running Locally

```bash
# From repo root
make infra keys

# Start service
cd services/notification
cp .env.example .env
go run ./cmd migrate up
go run ./cmd worker &  # Consumer only, no API needed for core function
go run ./cmd api &     # For push subscription endpoints

# Test with Mailpit (local SMTP)
# Mailpit UI: http://localhost:8025

curl localhost:8084/health
```

---

## 11. References

- OpenAPI: `docs/contracts/openapi/notification.yaml`
- HLD §4.2.1: Service responsibilities
- SRS §3.2.20-§3.2.21: Notification requirements
- SRS §3.2.25: Turn notification
- FCM Docs: https://firebase.google.com/docs/cloud-messaging
- SES Docs: https://docs.aws.amazon.com/ses/

---

## 12. Test Case Alignment

| Feature | SRS | Test Cases | Level |
|---------|-----|------------|-------|
| E-ticket email | SRS-F-20-01..03 | TC-TKT-01, TC-TKT-02 | Integration + E2E |
| Notification delivery | SRS-F-21-01..03 | TC-MSG-01 | Integration |
| DLQ/Retry | SRS-Q-REL-01 | TC-MSG-01, TC-MSG-02, TC-MSG-04 | Integration |
| Idempotency | SRS-Q-REL-01 | TC-MSG-03 | Integration |
| Turn notification | SRS-F-25-01..13 | TC-QUE-04 (trigger), E2E | Integration |
| OTP email delivery | SRS-F-27-02, SRS-F-27-12 | TC-MSG-05 | Integration |
| OTP invalid encryption | SRS-F-27-12 | TC-MSG-06 | Unit |

### Key Test Scenarios

| TC ID | Scenario | Expected | Validates |
|-------|----------|----------|-----------|
| TC-MSG-01 | SMTP down during send | Retry queue, delivered after recovery | Resilience |
| TC-MSG-02 | DLQ redrive | Message processed, no duplicate | DLQ handling |
| TC-MSG-03 | Duplicate booking.paid | Single email sent | Idempotency |
| TC-MSG-04 | Handler fails transient then succeeds | Retry with x-rbt-attempt=2, then ack | Retry mechanism |
| TC-MSG-05 | OTP email via Notification | Email received within 5s | OTP delivery |
| TC-MSG-06 | Invalid OTP encryption | Rejected, logged as error | Security |
| TC-TKT-01 | E-ticket content | Contains QR, ticket codes | Template |
| TC-TKT-02 | Email delivery | Received in inbox | SMTP integration |

---

## Appendix A: Event Payload Schemas

### A.1 booking.paid

```json
{
  "messageId": "uuid",
  "eventType": "booking.paid",
  "schemaVersion": "v1",
  "occurredAt": "2026-10-19T10:08:00Z",
  "correlationId": "uuid",
  "producer": "booking-service",
  "payload": {
    "bookingId": "uuid",
    "eventId": "uuid",
    "eventTitle": "Konser ABC",
    "eventDate": "2026-11-15T19:00:00Z",
    "venueName": "Gelora Bung Karno",
    "buyerEmail": "encrypted-base64",
    "totalAmount": 3000000,
    "tickets": [
      {
        "ticketId": "uuid",
        "publicCode": "RBT-ABC123",
        "categoryName": "VIP",
        "attendeeNikMasked": "320101****0001",
        "qrPayload": "encrypted-base64"
      }
    ],
    "paidAt": "2026-10-19T10:08:00Z"
  }
}
```

### A.2 booking.expired

```json
{
  "messageId": "uuid",
  "eventType": "booking.expired",
  "schemaVersion": "v1",
  "occurredAt": "2026-10-19T10:15:00Z",
  "correlationId": "uuid",
  "producer": "booking-service",
  "payload": {
    "bookingId": "uuid",
    "eventId": "uuid",
    "eventTitle": "Konser ABC",
    "buyerEmail": "encrypted-base64",
    "amount": 3000000,
    "expiredAt": "2026-10-19T10:15:00Z"
  }
}
```

### A.3 queue.turn.arrived

```json
{
  "messageId": "uuid",
  "eventType": "queue.turn.arrived",
  "schemaVersion": "v1",
  "occurredAt": "2026-10-19T10:00:00Z",
  "correlationId": "uuid",
  "producer": "queue-service",
  "payload": {
    "entryId": "uuid",
    "eventId": "uuid",
    "eventTitle": "Konser ABC",
    "pushToken": "fcm-device-token",
    "platform": "ANDROID",
    "claimDeadline": "2026-10-19T10:03:00Z",
    "position": 0
  }
}
```

### A.4 otp.requested

```json
{
  "messageId": "uuid",
  "eventType": "otp.requested",
  "schemaVersion": "v1",
  "occurredAt": "2026-10-20T13:45:00Z",
  "correlationId": "uuid",
  "producer": "user-service",
  "payload": {
    "emailEnc": "base64(AES-256-GCM encrypted email)",
    "codeEnc": "base64(AES-256-GCM encrypted 6-digit code)",
    "expiresAt": "2026-10-20T13:50:00Z",
    "locale": "id"
  }
}
```

---

## Appendix B: Error Codes

| Code | HTTP | Description | User Action |
|------|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Field tidak valid | Fix input |
| `EVENT_NOT_FOUND` | 404 | Event tidak ada | Check event ID |
| `SUBSCRIPTION_EXISTS` | 409 | Sudah subscribe | Ignore |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Subscription tidak ada | Subscribe ulang |
| `PUSH_TOKEN_INVALID` | 400 | FCM token tidak valid | Re-register token |

---

## Appendix C: Consumer Implementation

### C.1 Idempotent Processing

```go
func (c *Consumer) HandleBookingPaid(ctx context.Context, msg *Event) error {
    payload := msg.Payload.(*BookingPaidPayload)
    
    // Check idempotency - already sent?
    exists, err := c.repo.DeliveryExists(ctx, "ETICKET", "EMAIL", payload.BookingID)
    if err != nil { return err }
    if exists {
        log.Info("already processed, skipping", "bookingId", payload.BookingID)
        return nil // ACK message
    }
    
    // Create delivery record (PENDING)
    delivery := &Delivery{
        Type:          "ETICKET",
        Channel:       "EMAIL",
        Recipient:     c.decrypt(payload.BuyerEmail),
        ReferenceID:   payload.BookingID,
        ReferenceType: "booking",
        Status:        "PENDING",
    }
    if err := c.repo.CreateDelivery(ctx, delivery); err != nil {
        return err // NACK, will retry
    }
    
    // Render template
    body, err := c.templates.Render("eticket", payload)
    if err != nil {
        c.repo.MarkDeliveryFailed(ctx, delivery.ID, err.Error())
        return nil // ACK, permanent failure
    }
    
    // Send email
    if err := c.smtp.Send(ctx, delivery.Recipient, "E-Tiket Anda", body); err != nil {
        delivery.Attempts++
        if delivery.Attempts >= 3 {
            c.repo.MarkDeliveryFailed(ctx, delivery.ID, err.Error())
            return nil // ACK, give up
        }
        return err // NACK, will retry
    }
    
    // Mark delivered
    c.repo.MarkDeliveryDelivered(ctx, delivery.ID)
    return nil
}
```

### C.2 Push with Deadline Check

```go
func (c *Consumer) HandleTurnArrived(ctx context.Context, msg *Event) error {
    payload := msg.Payload.(*TurnArrivedPayload)
    
    // Check if deadline already passed
    if time.Now().After(payload.ClaimDeadline) {
        log.Info("deadline passed, skipping push", "entryId", payload.EntryID)
        return nil // ACK, too late
    }
    
    // Check idempotency
    exists, err := c.repo.DeliveryExists(ctx, "TURN_ARRIVED", "PUSH", payload.EntryID)
    if exists { return nil }
    
    // Get subscription
    sub, err := c.repo.GetSubscription(ctx, payload.EventID, payload.VisitorID)
    if err != nil || sub == nil {
        log.Info("no subscription, skipping", "eventId", payload.EventID)
        return nil // ACK, no subscription
    }
    
    // Send push
    message := &fcm.Message{
        Token: sub.Token,
        Notification: &fcm.Notification{
            Title: "Giliran Anda Tiba!",
            Body:  fmt.Sprintf("Segera claim dalam %d menit", 
                int(time.Until(payload.ClaimDeadline).Minutes())),
        },
        Data: map[string]string{
            "eventId":       payload.EventID,
            "entryId":       payload.EntryID,
            "type":          "TURN_ARRIVED",
            "claimDeadline": payload.ClaimDeadline.Format(time.RFC3339),
        },
    }
    
    if err := c.fcm.Send(ctx, message); err != nil {
        if isFCMTokenInvalid(err) {
            c.repo.DeleteSubscription(ctx, sub.ID)
            return nil // ACK, invalid token
        }
        return err // NACK, will retry
    }
    
    c.repo.CreateDelivery(ctx, &Delivery{
        Type: "TURN_ARRIVED", Channel: "PUSH",
        ReferenceID: payload.EntryID, Status: "DELIVERED",
    })
    return nil
}
```

---

## Appendix D: SMTP Configuration

### D.1 SES Setup

```go
type SMTPConfig struct {
    Host     string // email-smtp.ap-southeast-3.amazonaws.com
    Port     int    // 587 (TLS)
    User     string // SMTP credentials from SES
    Password string
    From     string // noreply@rusuh.example (verified domain)
}

func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
    auth := smtp.PlainAuth("", s.config.User, s.config.Password, s.config.Host)
    
    msg := []byte(fmt.Sprintf(
        "From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
        s.config.From, to, subject, body,
    ))
    
    addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
    return smtp.SendMail(addr, auth, s.config.From, []string{to}, msg)
}
```

### D.2 Sending Limits

| Limit | Value | Notes |
|-------|-------|-------|
| SES sandbox | 200/day | Request production access |
| SES production | 50,000/day | Can request increase |
| Rate | 14/second | Soft limit |
| Bounce rate | < 5% | Or account suspension |
| Complaint rate | < 0.1% | Or account suspension |
