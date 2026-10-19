# Frontend Integration Guide — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | INT-RBT-001 |
| **Version** | 1.0 |
| **Date** | 2026-10-19 |
| **Status** | Draft |
| **Author(s)** | Tim PT. Tiket Sampai Nonton Sentosa |
| **Audience** | WEB-1, WEB-2, MOB-1 |
| **Source** | HLD v1.4, SRS v1.2, OpenAPI contracts |

> Dokumen ini menjelaskan cara frontend (Web dan Mobile) berinteraksi dengan backend services. Untuk kontrak lengkap, lihat `docs/contracts/openapi/`.

---

## 1. Architecture Overview

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────┐
│  Web/Mobile │────▶│  CloudFront+WAF │────▶│     ALB     │
└─────────────┘     └─────────────────┘     └──────┬──────┘
                                                   │
                    ┌──────────────────────────────┼──────────────────────────────┐
                    │                              │                              │
              ┌─────▼─────┐              ┌─────────▼────────┐           ┌─────────▼────────┐
              │  Queue    │              │     Booking      │           │     Payment      │
              │  Service  │              │     Service      │           │     Service      │
              │  :8081    │              │     :8082        │           │     :8083        │
              └───────────┘              └──────────────────┘           └──────────────────┘
```

**Region:** `ap-southeast-3` (Jakarta)

**Base URLs:**
- Production: `https://api.rusuh.example` (ganti saat RFS)
- Local: `http://localhost:{port}` — Queue: 8081, Booking: 8082, Payment: 8083

---

## 2. Queue Flow (Waiting Room)

### 2.1 Flow Overview

```
┌────────┐    ┌────────┐    ┌─────────┐    ┌────────┐    ┌─────────┐
│  Join  │───▶│ Wait   │───▶│ Admitted│───▶│ Claim  │───▶│ Booking │
│        │    │(polling)│    │         │    │        │    │ Session │
└────────┘    └────────┘    └─────────┘    └────────┘    └─────────┘
     │              │
     │              ▼
     │        Status Document
     │        (CDN cached 1s)
     │
     ▼
 Direct Admission
 (jika slot tersedia)
```

### 2.2 Step 1: Join Queue

**Endpoint:** `POST /api/v1/events/{eventId}/queue-entries`

**Request:**
```json
{
  "buyerNik": "3201010101010001",
  "email": "buyer@example.com"
}
```

**Headers:**
- `X-Visitor-ID`: UUID perangkat (wajib, disimpan di localStorage/SecureStorage)
- `Authorization: Bearer <token>` (opsional, untuk login user)
- `Idempotency-Key`: UUID (untuk retry)

**Responses:**

| Status | Kondisi | Aksi Frontend |
|--------|---------|---------------|
| `201 WAITING` | Masuk antrean, harus menunggu | Tampilkan Waiting Room, mulai polling |
| `201 ADMITTED` | Langsung masuk (slot tersedia) | Panggil `/claim`, lanjut ke booking |
| `409 NIK_ALREADY_IN_QUEUE` | NIK sudah punya entry aktif | Tampilkan error, arahkan ke entry yang ada |
| `409 VISITOR_ALREADY_IN_QUEUE` | Perangkat sudah punya entry aktif | Tampilkan error, arahkan ke entry yang ada |
| `409 EMAIL_ALREADY_IN_QUEUE` | Email sudah punya entry aktif | Tampilkan error |
| `409 BUYER_QUOTA_EXHAUSTED` | NIK sudah beli maksimal tiket | Tampilkan error quota habis |
| `410 EVENT_SOLD_OUT` | Tiket habis | Tampilkan sold out |
| `429 RATE_LIMITED` | Terlalu banyak request | Tampilkan retry dengan `Retry-After` header |

**Response Body (201):**
```json
{
  "meta": { "requestId": "...", "serverTime": "2026-10-19T10:00:00Z" },
  "data": {
    "id": "entry-uuid",
    "eventId": "event-uuid",
    "seq": 12345,
    "status": "WAITING",
    "queueToken": "jwt-token",
    "visitorToken": "jwt-token",
    "claimDeadline": null,
    "config": {
      "pollingIntervalMs": 3000,
      "claimWindowSeconds": 180
    }
  }
}
```

**Simpan di client:**
- `queueToken` — untuk operasi claim dan leave
- `visitorToken` — untuk semua request berikutnya (cookie `rb_visitor` atau header `X-Visitor-Token`)
- `seq` — nomor antrean untuk menghitung posisi

### 2.3 Step 2: Polling Status Document (Waiting Room)

**Endpoint:** `GET /api/v1/queue-status/{eventId}`

**Caching:** CDN cached 1 detik — tidak perlu throttle di client

**Response:**
```json
{
  "eventId": "event-uuid",
  "servingSeq": 12300,
  "waitingCount": 500,
  "activeSlots": 45,
  "maxActiveSlots": 50,
  "saleStatus": "OPEN",
  "updatedAt": "2026-10-19T10:00:00Z",
  "config": {
    "pollingIntervalMs": 3000
  }
}
```

**Hitung posisi di client:**
```javascript
const myPosition = mySeq - servingSeq;
const isMyTurn = myPosition <= 0;
```

**Zona peluang (indikator UI):**
| Posisi | Zona | Warna UI |
|--------|------|----------|
| ≤ 0 | Giliran tiba | 🟢 Hijau |
| 1–10 | Sangat dekat | 🟡 Kuning |
| 11–50 | Dekat | 🟠 Oranye |
| > 50 | Menunggu | ⚪ Abu-abu |

**Adaptive polling:**
- Gunakan `config.pollingIntervalMs` dari response (default 3 detik)
- Saat `saleStatus` = `CLOSED` atau `SOLD_OUT`, hentikan polling

### 2.4 Step 3: Claim (Saat Giliran Tiba)

Ketika `mySeq <= servingSeq`:

**Endpoint:** `POST /api/v1/queue-entries/{entryId}/claim`

**Headers:**
- `Authorization: Bearer <queueToken>`
- `X-Visitor-Token: <visitorToken>`

**Response (200):**
```json
{
  "meta": { ... },
  "data": {
    "admissionToken": "jwt-one-time-use",
    "expiresAt": "2026-10-19T10:01:00Z"
  }
}
```

**Errors:**
| Status | Kondisi | Aksi |
|--------|---------|------|
| `403 CLAIM_DEADLINE_PASSED` | Giliran hangus (> 3 menit) | Redirect home dengan `TURN_MISSED` |
| `409 ALREADY_CLAIMED` | Sudah claim sebelumnya | Lanjut ke booking session |
| `410 ENTRY_CANCELLED` | Entry dibatalkan | Redirect home |

**Admission token:**
- TTL: 60 detik
- Sekali pakai
- Digunakan untuk `POST /booking-sessions`

### 2.5 Step 4: Create Booking Session

**Endpoint:** `POST /api/v1/booking-sessions`

**Headers:**
- `Authorization: Bearer <admissionToken>`
- `X-Visitor-Token: <visitorToken>`

**Response (201):**
```json
{
  "meta": { ... },
  "data": {
    "id": "session-uuid",
    "eventId": "event-uuid",
    "units": 3,
    "expiresAt": "2026-10-19T10:10:00Z",
    "serverTime": "2026-10-19T10:00:00Z",
    "categories": [
      {
        "id": "cat-uuid",
        "name": "VIP",
        "price": 1500000,
        "available": 50
      }
    ],
    "config": {
      "maxTicketsPerOrder": 3,
      "sseEnabled": true,
      "pollingIntervalMs": 3000
    }
  }
}
```

---

## 3. SSE (Server-Sent Events) untuk Ketersediaan

### 3.1 Connect SSE

**Endpoint:** `GET /api/v1/booking-sessions/{sessionId}/stream`

**Headers:**
- `Accept: text/event-stream`
- `X-Visitor-Token: <visitorToken>`

**Events:**
```
event: availability
data: {"categories":[{"id":"cat-uuid","available":49}]}

event: session
data: {"expiresAt":"2026-10-19T10:10:00Z","serverTime":"2026-10-19T10:05:00Z"}
```

### 3.2 Fallback Polling

Jika SSE gagal atau tidak tersedia:

**Endpoint:** `GET /api/v1/booking-sessions/{sessionId}`

**Polling interval:** 3 detik (dari `config.pollingIntervalMs`)

**Auto-reconnect SSE:** Exponential backoff (1s, 2s, 4s, max 30s)

---

## 4. Booking Flow

### 4.1 Validate Attendee NIK

Sebelum hold, validasi NIK pemegang tiket:

**Endpoint:** `POST /api/v1/booking-sessions/{sessionId}/attendee-checks`

**Request:**
```json
{
  "attendeeNiks": ["3201010101010001", "3201010101010002"]
}
```

**Response (200):**
```json
{
  "meta": { ... },
  "data": {
    "valid": true,
    "attendees": [
      { "nik": "320101****0001", "valid": true },
      { "nik": "320101****0002", "valid": true }
    ]
  }
}
```

**Errors:**
| Error Code | Kondisi |
|------------|---------|
| `NIK_INVALID` | Format NIK salah |
| `NIK_ALREADY_HAS_TICKET` | NIK sudah punya tiket di event ini |
| `ATTENDEE_CHECK_LIMIT_EXCEEDED` | Terlalu banyak cek per sesi |

### 4.2 Create Booking (Hold)

**Endpoint:** `POST /api/v1/bookings`

**Headers:**
- `X-Visitor-Token: <visitorToken>`
- `Idempotency-Key: <uuid>`

**Request:**
```json
{
  "sessionId": "session-uuid",
  "items": [
    {
      "categoryId": "cat-uuid",
      "attendeeNik": "3201010101010001"
    },
    {
      "categoryId": "cat-uuid",
      "attendeeNik": "3201010101010002"
    }
  ]
}
```

**Response (201):**
```json
{
  "meta": { ... },
  "data": {
    "id": "booking-uuid",
    "status": "HELD",
    "amount": 3000000,
    "expiresAt": "2026-10-19T10:15:00Z",
    "items": [ ... ]
  }
}
```

---

## 5. Token Summary

| Token | Disimpan di | TTL | Kegunaan |
|-------|-------------|-----|----------|
| `visitorToken` | Cookie `rb_visitor` atau header `X-Visitor-Token` | 24 jam | Identitas perangkat |
| `queueToken` | Memory/State | Sesi antrean | Operasi claim/leave |
| `admissionToken` | Memory (sekali pakai) | 60 detik | Buat booking session |
| `accessToken` (login) | Memory | 15 menit | User authenticated requests |
| `refreshToken` (login) | HttpOnly cookie | 30 hari | Refresh access token |

---

## 6. Error Handling

### 6.1 Standard Error Response

```json
{
  "meta": {
    "requestId": "req-uuid",
    "serverTime": "2026-10-19T10:00:00Z"
  },
  "error": {
    "code": "NIK_ALREADY_IN_QUEUE",
    "message": "NIK sudah terdaftar di antrean",
    "details": {
      "existingEntryId": "entry-uuid"
    }
  }
}
```

### 6.2 Redirect Reasons

Saat redirect ke home, sertakan reason di query param:

| Reason | Kondisi | Banner Text |
|--------|---------|-------------|
| `SOLD_OUT` | Tiket habis | "Maaf, tiket sudah habis" |
| `TURN_MISSED` | Giliran hangus | "Giliran Anda telah berlalu" |
| `SESSION_EXPIRED` | TTL booking habis | "Waktu pemilihan habis" |
| `SESSION_EXPIRED_OFFLINE` | Offline saat TTL habis | "Koneksi terputus, sesi berakhir" |
| `SALE_CLOSED` | Penjualan ditutup | "Penjualan telah ditutup" |

---

## 7. CDN Cache Behavior

| Endpoint | Cache TTL | Cache Key |
|----------|-----------|-----------|
| `GET /api/v1/events` | 2 detik | path + `page`, `size`, `timeframe`, `city`, `status` |
| `GET /api/v1/events/{id}` | 2 detik | path |
| `GET /api/v1/queue-status/{eventId}` | 1 detik | path only |
| Semua POST/PUT/DELETE | No cache | — |

---

## 8. Mobile-Specific

### 8.1 Visitor ID

Generate dan simpan di SecureStorage saat pertama kali app dibuka:
```javascript
const visitorId = crypto.randomUUID();
await SecureStore.setItemAsync('visitorId', visitorId);
```

### 8.2 Push Notification

Subscribe untuk notifikasi giliran:

**Endpoint:** `POST /api/v1/push-subscriptions`

**Request:**
```json
{
  "eventId": "event-uuid",
  "platform": "ANDROID",
  "token": "fcm-device-token"
}
```

**Push events:**
- `TURN_SOON` — giliran sebentar lagi
- `TURN_ARRIVED` — giliran tiba, segera claim

### 8.3 Background Handling

- App di background tidak menerima polling/SSE
- Andalkan push notification untuk giliran
- Saat app kembali ke foreground: cek status entry, reconnect SSE

---

## 9. Checklist Implementasi

- [ ] Generate dan persist `visitorId`
- [ ] Implementasi join dengan retry dan idempotency key
- [ ] Polling status document dengan interval dari server
- [ ] Hitung posisi dari `seq - servingSeq`
- [ ] Auto-claim saat `servingSeq >= mySeq`
- [ ] SSE connection dengan auto-reconnect
- [ ] Fallback polling jika SSE gagal
- [ ] Handle semua error codes dengan UI yang sesuai
- [ ] Redirect dengan reason parameter
- [ ] Timer countdown sinkron dengan `serverTime`
- [ ] Push notification subscription (mobile)

---

## References

- OpenAPI Queue: `docs/contracts/openapi/queue.yaml`
- OpenAPI Booking: `docs/contracts/openapi/booking.yaml`
- HLD §4.4 Runtime View: Sequence diagrams
- SRS §3.2.25: Near-Real-Time Notification
- ADR-003: Waiting room via polling Status Document
