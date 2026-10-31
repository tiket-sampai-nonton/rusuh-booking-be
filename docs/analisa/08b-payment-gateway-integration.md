# Payment Gateway Integration Guide — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | INT-RBT-002 |
| **Version** | 1.0 |
| **Date** | 2026-10-19 |
| **Status** | Draft |
| **Author(s)** | Tim PT. Tiket Sampai Nonton Sentosa |
| **Audience** | WEB-2, BE-2, MOB-1 |
| **Source** | HLD v1.4, SRS v1.2, OpenAPI Payment |

> Dokumen ini menjelaskan integrasi dengan Midtrans sebagai payment gateway. Payment Service bertindak sebagai abstraksi — frontend tidak langsung berinteraksi dengan Midtrans.

> **Pembaruan 2026-10-31 (ADR-024):** integrasi memakai **Midtrans Snap**, bukan Core API. Backend membuat token Snap (`POST /snap/v1/transactions`) dan mengembalikan `redirectUrl`; FE membukanya (redirect/WebView) lalu **selalu memverifikasi** `GET /payments/{bookingId}/status`. Tidak ada nomor VA/QR/deeplink dari backend. Bentuk respons yang berlaku ada di §3.2 dan `docs/contracts/openapi/payment.yaml`; contoh `instructions` di bagian lain dokumen ini sudah tidak berlaku.

---

## 1. Architecture Overview

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────┐     ┌──────────────┐
│  Frontend   │────▶│  Payment Service │────▶│   Midtrans  │────▶│   Webhook    │
│  (Web/App)  │◀────│     :8083        │◀────│   Gateway   │     │   Callback   │
└─────────────┘     └─────────────────┘     └─────────────┘     └──────────────┘
                            │                                           │
                            │         ┌────────────────────────────────┘
                            ▼         ▼
                    ┌─────────────────────────┐
                    │    Booking Service      │
                    │    (settlement/expiry)  │
                    └─────────────────────────┘
```

**Region:** `ap-southeast-3` (Jakarta)

**Environments:**
| Environment | Midtrans Mode | Webhook URL |
|-------------|---------------|-------------|
| Local (simulator) | `PAYMENT_PROVIDER=simulator` | N/A |
| Local (Midtrans) | `PAYMENT_PROVIDER=midtrans` | via cloudflared tunnel |
| Staging/Demo | Midtrans Sandbox | `https://api.staging.rusuh.example/api/v1/webhooks/midtrans` |
| Production | Midtrans Production | `https://api.rusuh.example/api/v1/webhooks/midtrans` |

---

## 2. Payment Flow

### 2.1 Flow Overview

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│  Booking │───▶│  Payment │───▶│  Select  │───▶│  Charge  │───▶│  Webhook │
│  HELD    │    │   Page   │    │  Method  │    │ Midtrans │    │ Callback │
└──────────┘    └──────────┘    └──────────┘    └──────────┘    └──────────┘
                                                      │                │
                                                      ▼                ▼
                                              ┌──────────────┐  ┌──────────────┐
                                              │   Pending    │  │  Settlement  │
                                              │   Payment    │  │   Applied    │
                                              └──────────────┘  └──────────────┘
```

### 2.2 Supported Payment Methods

| Method | Code | Flow Type | TTL |
|--------|------|-----------|-----|
| GoPay | `gopay` | Redirect/QR | 15 menit |
| ShopeePay | `shopeepay` | Redirect/QR | 15 menit |
| QRIS | `qris` | QR Code | 15 menit |
| Bank Transfer BCA | `bank_transfer_bca` | VA Number | TTL Booking |
| Bank Transfer BNI | `bank_transfer_bni` | VA Number | TTL Booking |
| Bank Transfer Mandiri | `bank_transfer_mandiri` | VA Number | TTL Booking |
| Bank Transfer Permata | `bank_transfer_permata` | VA Number | TTL Booking |

---

## 3. Frontend Integration

### 3.1 Step 1: Get Payment Page

Setelah booking `HELD`, redirect ke payment page:

**Endpoint:** `GET /api/v1/payments/{bookingId}/page`

**Headers:**
- `X-Visitor-Token: <visitorToken>` atau cookie `rb_visitor`

**Response (200):**
```json
{
  "meta": { ... },
  "data": {
    "bookingId": "booking-uuid",
    "amount": 3000000,
    "currency": "IDR",
    "expiresAt": "2026-10-19T10:15:00Z",
    "serverTime": "2026-10-19T10:05:00Z",
    "status": "AWAITING_PAYMENT",
    "items": [
      {
        "categoryName": "VIP",
        "attendeeNikMasked": "320101****0001",
        "price": 1500000
      }
    ],
    "availableMethods": [
      { "code": "gopay", "name": "GoPay", "type": "ewallet" },
      { "code": "qris", "name": "QRIS", "type": "qr" },
      { "code": "bank_transfer_bca", "name": "BCA Virtual Account", "type": "va" }
    ],
    "currentAttempt": null
  }
}
```

### 3.2 Step 2: Lock Booking (Tekan "Bayar")

Saat user memilih metode dan tekan "Bayar":

**Endpoint:** `POST /api/v1/payments/{bookingId}/attempts`

**Headers:**
- `X-Visitor-Token: <visitorToken>`
- `Idempotency-Key: <uuid>`

**Request:** `method` adalah *bucket* yang membatasi channel di halaman Snap (`VA_BCA`→`bca_va`, `VA_BNI`→`bni_va`, `VA_BRI`→`bri_va`, `VA_PERMATA`→`permata_va`, `QRIS`→`qris`, `GOPAY`→`gopay`, `SNAP`→semua channel).
```json
{ "method": "VA_BCA" }
```

**Response (201):** jumlah dan `order_id` diambil backend dari Booking, tidak dari klien. Token Snap tidak dikirim ke klien secara terpisah.
```json
{
  "meta": { "code": 201 },
  "data": {
    "attemptNo": 1,
    "method": "VA_BCA",
    "status": "PENDING",
    "bookingVersion": 4,
    "expiresAt": "2026-10-19T10:20:00Z",
    "redirectUrl": "https://app.sandbox.midtrans.com/snap/v4/redirection/<token>",
    "successReturnUrl": "https://<fe>/payment/success",
    "failedReturnUrl": "https://<fe>/payment/failed"
  }
}
```

Catatan perilaku:
- Kedaluwarsa transaksi Snap dihitung dari sisa waktu booking, dibulatkan **ke bawah** ke menit (minimum 1 menit); `expiry.unit` = `minute` (belum diverifikasi di Sandbox, task PAY-2).
- Order Snap baru muncul di Core API setelah pembayar memilih channel. Sebelum itu `GET /v2/{order_id}/status` menjawab 404 dan backend memperlakukannya sebagai belum dibayar.
- Token Snap yang sudah dibuat tidak bisa dibatalkan lewat API. Bila attempt diganti, pembayaran yang masih masuk lewat token lama akan dicatat sebagai pembayaran setelah pembatalan (kasus refund `SETTLED_AFTER_CANCEL`).
- Webhook, signature (`SHA512(order_id+status_code+gross_amount+serverKey)`), idempotensi, dan status Core API tidak berubah.

### 3.3 Step 3: Handle Payment Result

**Untuk redirect methods (GoPay, ShopeePay):**
- User diarahkan ke app/web Midtrans
- Setelah selesai, redirect kembali ke `finish_url`
- Frontend poll status

**Untuk QR methods (QRIS):**
- Tampilkan QR code
- Poll status setiap 3 detik

**Untuk VA methods:**
- Tampilkan nomor VA
- User transfer manual
- Poll status atau tunggu email

### 3.4 Step 4: Poll Payment Status

**Endpoint:** `GET /api/v1/payments/{bookingId}/status`

**Response:** `bookingStatus` adalah status Booking (otoritatif); `attempt.status` adalah status attempt terakhir (`CREATED|PENDING|UNKNOWN|SUCCEEDED|FAILED|CANCELLED|EXPIRED|REFUND_REQUIRED|REFUNDED`).
```json
{
  "meta": { "code": 200 },
  "data": {
    "bookingId": "booking-uuid", "bookingStatus": "PAID", "orderId": "RB-7F3A9C21",
    "paymentExpiresAt": "2026-10-19T10:20:00Z", "serverTime": "2026-10-19T10:08:05Z",
    "attempt": { "attemptNo": 1, "method": "VA_BCA", "status": "SUCCEEDED", "bookingVersion": 4 }
  }
}
```

**Status values:**
| Status | Kondisi | Aksi Frontend |
|--------|---------|---------------|
| `AWAITING_PAYMENT` | Belum ada attempt | Tampilkan pilihan metode |
| `PENDING` | Menunggu pembayaran | Poll; pembayar sedang/akan membayar di halaman Snap |
| `PAID` | Berhasil | Redirect ke halaman tiket |
| `EXPIRED` | TTL habis | Redirect home dengan `PAYMENT_EXPIRED` |
| `FAILED` | Gagal di gateway | Tampilkan error, allow retry method |
| `CANCELLED` | Dibatalkan | Redirect home |

---

## 4. Mobile WebView Integration

### 4.1 Get Page Token

Mobile app tidak bisa share cookie dengan WebView, gunakan token:

**Endpoint:** `POST /api/v1/payments/{bookingId}/page-tokens`

**Headers:**
- `X-Visitor-Token: <visitorToken>`

**Response:**
```json
{
  "meta": { ... },
  "data": {
    "token": "one-time-token",
    "expiresAt": "2026-10-19T10:06:00Z",
    "pageUrl": "https://api.rusuh.example/pay/{bookingId}?token=..."
  }
}
```

### 4.2 Open WebView

```javascript
// React Native example
const response = await api.post(`/payments/${bookingId}/page-tokens`);
const { pageUrl } = response.data.data;

navigation.navigate('PaymentWebView', { uri: pageUrl });
```

### 4.3 WebView Event Handling

Listen untuk redirect dari payment page:

```javascript
// Intercept redirects
onNavigationStateChange={(navState) => {
  const url = new URL(navState.url);
  
  if (url.pathname.includes('/payment/success')) {
    navigation.replace('TicketScreen', { bookingId });
  } else if (url.pathname.includes('/payment/failed')) {
    navigation.replace('PaymentFailed', { bookingId });
  } else if (url.pathname.includes('/payment/expired')) {
    navigation.replace('Home', { reason: 'PAYMENT_EXPIRED' });
  }
}}
```

---

## 5. Webhook (Backend Only)

### 5.1 Midtrans Callback

**Endpoint:** `POST /api/v1/webhooks/midtrans`

**Headers:**
- Signature dari Midtrans

**Request body:** Midtrans notification format

**Processing:**
1. Verify signature dengan server key
2. Check idempotency (fingerprint callback)
3. Update payment attempt status
4. Jika `settlement` → publish `payment.succeeded` event
5. Booking Service apply settlement, issue tickets

### 5.2 Signature Verification

```
SHA512(order_id + status_code + gross_amount + server_key)
```

---

## 6. Error Handling

### 6.1 Payment Errors

| Error Code | Kondisi | Aksi |
|------------|---------|------|
| `BOOKING_NOT_PAYABLE` | Booking bukan status HELD | Redirect ke status yang sesuai |
| `BOOKING_EXPIRED` | TTL booking habis | Redirect home |
| `PAYMENT_METHOD_UNAVAILABLE` | Metode tidak tersedia | Tampilkan metode lain |
| `GATEWAY_ERROR` | Midtrans error | Tampilkan retry, log untuk ops |
| `ATTEMPT_IN_PROGRESS` | Sudah ada attempt pending | Tampilkan instruksi attempt yang ada |

### 6.2 Retry Strategy

- User bisa ganti metode pembayaran selama TTL belum habis
- Attempt lama otomatis expired
- Maksimal 1 attempt aktif per waktu

---

## 7. Edit Order Before Payment

### 7.1 Kembali ke Edit

Selama belum tekan "Bayar", user bisa kembali edit:

**Endpoint:** `PUT /api/v1/bookings/{bookingId}`

**Request:**
```json
{
  "items": [
    { "categoryId": "cat-uuid", "attendeeNik": "3201010101010003" }
  ]
}
```

**Constraint:**
- Total quantity tidak boleh berubah
- Hanya bisa edit sebelum `payment_started_at` terisi
- Maksimal `cfg_max_edits` (default: 3)

### 7.2 Setelah "Bayar"

Setelah tekan "Bayar":
- Booking terkunci (`payment_started_at` terisi)
- Tidak bisa edit
- Harus selesaikan atau tunggu expired

---

## 8. Timer Synchronization

### 8.1 Server Time

Selalu gunakan `serverTime` dari response untuk sinkronisasi:

```javascript
const serverNow = new Date(response.meta.serverTime);
const expiresAt = new Date(response.data.expiresAt);
const remainingMs = expiresAt - serverNow;

// Countdown timer
setInterval(() => {
  remainingMs -= 1000;
  if (remainingMs <= 0) {
    handleExpired();
  }
  updateDisplay(remainingMs);
}, 1000);
```

### 8.2 TTL Values

| TTL | Default | Configurable |
|-----|---------|--------------|
| Booking TTL | 10 menit | Per event |
| Payment TTL | 15 menit | Per method |
| Claim window | 3 menit | Global |

---

## 9. Local Development

### 9.1 Simulator Mode

Untuk testing tanpa Midtrans:

```bash
# .env
PAYMENT_PROVIDER=simulator
```

Simulator auto-settles setelah 5 detik.

### 9.2 Midtrans Sandbox

Untuk testing dengan Midtrans Sandbox:

```bash
# .env
PAYMENT_PROVIDER=midtrans
MIDTRANS_SERVER_KEY=SB-Mid-server-xxx
MIDTRANS_CLIENT_KEY=SB-Mid-client-xxx
MIDTRANS_ENVIRONMENT=sandbox

# Tunnel untuk webhook
cloudflared tunnel --url http://localhost:8083
```

Update Midtrans dashboard dengan tunnel URL untuk webhook.

### 9.3 Test Cards/Accounts

Lihat [Midtrans Sandbox Testing](https://docs.midtrans.com/docs/testing-payment-on-sandbox)

---

## 10. Checklist Implementasi

- [ ] Payment page dengan countdown timer
- [ ] Display available methods dari API
- [ ] Buka `redirectUrl` Snap (redirect/WebView), lalu verifikasi status ke backend di `successReturnUrl`/`failedReturnUrl`
- [ ] Poll status dengan interval 3 detik
- [ ] Handle semua status transitions
- [ ] Mobile WebView dengan page token
- [ ] Intercept WebView redirects
- [ ] Error handling dengan retry option
- [ ] Timer sync dengan serverTime
- [ ] Edit order sebelum "Bayar"

---

## References

- OpenAPI Payment: `docs/contracts/openapi/payment.yaml`
- HLD §4.4.4: Payment and Settlement sequence
- SRS §3.2.14–§3.2.19: Payment capabilities
- ADR-019: Event sourcing for Payment
- Midtrans Docs: https://docs.midtrans.com/
