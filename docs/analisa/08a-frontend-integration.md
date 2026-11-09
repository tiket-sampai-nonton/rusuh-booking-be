# Frontend Integration Guide — Rusuh Booking Tiket

| Field | Value |
|-------|-------|
| **Document ID** | INT-RBT-001 |
| **Version** | 1.1 |
| **Date** | 2026-11-09 |
| **Status** | Draft |
| **Author(s)** | Tim PT. Tiket Sampai Nonton Sentosa |
| **Audience** | WEB-1, WEB-2, MOB-1 |
| **Source** | HLD v1.4, SRS v1.2, OpenAPI contracts (`docs/contracts/openapi/*.yaml`, normatif) |

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
- Local: `http://localhost:{port}` — Queue: 8081, Booking: 8082, Payment: 8083, Notification: 8084, User: 8085

---

## 2. Queue Flow (Waiting Room)

### 2.1 Flow Overview

```
┌────────┐    ┌──────────────┐    ┌─────────┐    ┌────────┐    ┌─────────┐
│  Join  │───▶│ Wait         │───▶│ Admitted│───▶│ Claim  │───▶│ Booking │
│ (anon) │    │ status doc + │    │         │    │        │    │ Session │
└────────┘    │ me/heartbeat │    └─────────┘    └────────┘    └─────────┘
     │        └──────────────┘
     ▼
 Direct Admission
 (ADMITTED pada respons join bila tidak ada pengantre lebih awal dan slot tersedia)
```

Join anonim (ADR-030): NIK dan email pembeli baru diisi di halaman Booking saat hold (§4.2). Event hanya bisa
di-join setelah admin mengatur antrean (`QUEUE_NOT_CONFIGURED` bila belum, join ketat).

### 2.2 Step 1: Join Queue

**Endpoint:** `POST /api/v1/events/{eventId}/queue-entries`

**Request:** tanpa body. Klien lama yang masih mengirim `buyerNik`/`email`/`consentNik` ditoleransi, nilainya dibuang.

**Headers:**
- Visitor token: cookie HttpOnly `rb_visitor` (web) atau header `X-Visitor-Token` (app). Tidak ada `X-Visitor-ID`.
- `Authorization: Bearer <accessToken>` (opsional, access token User Service): hanya mengidentifikasi akun untuk dedup
  (satu entri aktif per akun); claim NIK token diabaikan.
- `Idempotency-Key`: 16..64 karakter (opsional; replay mengembalikan entri yang sama dengan 200).
- `X-Device-Id` (opsional): id acak per instalasi/profil browser; token yang terbit bersamanya terikat ke HMAC-nya.
- `X-Request-ID` (opsional): diteruskan antar service.

**Responses:**

| Status | Kondisi | Aksi Frontend |
|--------|---------|---------------|
| `201` `WAITING` | Entri baru, harus menunggu | Tampilkan Waiting Room, mulai polling dan heartbeat |
| `201` `ADMITTED` | Langsung masuk (slot tersedia, FR-05) | Panggil claim (§2.4) |
| `200` | Entri aktif sudah ada (token valid, re-attach akun, atau replay `Idempotency-Key`) | Perlakukan seperti `201`; simpan visitor token terbaru |
| `409 ACCOUNT_ALREADY_IN_QUEUE` | Bearer + token entri anonim lain sementara akun sudah punya entri aktif | Tampilkan bahwa akun sudah antre di perangkat lain |
| `409 EVENT_NOT_ON_SALE` | Penjualan belum/tidak dibuka | Tampilkan pesan penjualan belum dibuka |
| `409 QUEUE_NOT_CONFIGURED` | Antrean event belum disiapkan admin (juga untuk id event yang tidak dikenal) | Pesan tenang, jangan coba ulang otomatis |
| `410 EVENT_SOLD_OUT` | Tiket habis | Tampilkan sold out |
| `429 RATE_LIMITED` | Batas lunak per IP | Tunggu sesuai `Retry-After` |
| `503 DEPENDENCY_UNAVAILABLE` | Dependency sementara tidak tersedia | Ulangi dengan `Idempotency-Key` yang sama |

**Response Body (201/200)** (amplop `{meta, data}`; field wajib: `entryId`, `seq`, `status`, `joinedAt`, `serverTime`):
```json
{
  "meta": { "code": 201, "requestId": "..." },
  "data": {
    "entryId": "entry-uuid",
    "seq": 12345,
    "status": "WAITING",
    "state": "WAITING",
    "rank": 12,
    "queueToken": "jwt-token",   // deprecated (Q6): jangan dipakai, tidak diterima endpoint mana pun
    "visitorToken": "jwt-token",
    "statusUrl": "/api/v1/queue-status/{eventId}",
    "claimDeadline": null,
    "joinedAt": "2026-10-19T10:00:00Z",
    "serverTime": "2026-10-19T10:00:00Z"
  }
}
```

- `status` adalah status tersimpan (mentah: `WAITING`, `ADMITTED`, `CLAIMED`, `MISSED`, `LEFT`, `SOLD_OUT`, `CLOSED`,
  `COMPLETED`, `ENDED`, `STALE`); `state` adalah status klien (`WAITING`, `STALE`, `ADMITTED`, `CLAIMED`, `ENDED`).
- `rank`: urutan admisi (FIFO ketat atas `seq`); `null` saat `ENDED`.
- `visitorToken` ada di body **hanya untuk mobile**; web menerimanya sebagai cookie HttpOnly `rb_visitor` (Set-Cookie).
  Selalu simpan token terbaru dari setiap respons join: rotasi (re-attach akun, join ulang setelah entri berakhir)
  mencabut token lama (heartbeat/me lama dijawab `401 VISITOR_TOKEN_INVALID`).
- Cookie jar vs `X-Visitor-Token`: join yang membawa cookie `rb_visitor` valid dianggap terautentikasi cookie, jadi
  token baru hanya datang lewat `Set-Cookie` (body kosong). Klien native yang memakai cookie jar bersama header
  `X-Visitor-Token` harus menonaktifkan jar dan memakai header saja, atau mengandalkan cookie dan membaca `Set-Cookie`.
- App native wajib mengirim `X-Client-Type: mobile` pada join agar `visitorToken` ada di body (ADR-030 addendum (f)).
- `queueToken` deprecated (keputusan pemilik Q6): masih diterbitkan satu rilis, tidak diterima endpoint mana pun,
  bukan kredensial; klien mengabaikannya.
- Tidak ada field `id`, `eventId`, atau `config` pada respons join; interval polling datang dari status document (§2.3)
  dan `nextHeartbeatMs` (§2.3a).

### 2.3 Step 2: Polling Status Document (Waiting Room)

**Endpoint:** `GET /api/v1/queue-status/{eventId}` (publik, tanpa identitas)

**Caching:** `Cache-Control: public, max-age=1, stale-while-revalidate=2` (CDN 1 detik) — tidak perlu throttle di client.
`404 NOT_FOUND` bila event tidak ada.

**Response** (`data`; wajib: `eventId`, `saleState`, `soldOut`, `updatedAt`):
```json
{
  "eventId": "event-uuid",
  "saleState": "OPEN",
  "headSeq": 12300,
  "guaranteedUntilSeq": 12450,
  "possibleUntilSeq": 12600,
  "soldOut": false,
  "remainingTickets": 120,
  "admissionRatePerMin": 60,
  "nextPollAfterMs": 3000,
  "poll": { "nearMs": 3000, "midMs": 10000, "farMs": 30000, "midAbove": 100, "farAbove": 1000 },
  "preTurnNotifyPosition": 10,
  "categories": [ { "code": "VIP", "name": "VIP", "state": "AVAILABLE" } ],
  "updatedAt": "2026-10-19T10:00:00Z"
}
```

- `saleState`: `NOT_STARTED`, `OPEN`, `CLOSED`. `categories[].state`: `AVAILABLE`, `LIMITED`, `SOLD_OUT`.
- `headSeq`: seq entri hidup terdepan (0 bila kosong). `guaranteedUntilSeq` / `possibleUntilSeq`: batas seq zona
  terjamin / mungkin (0 = zona kosong).
- Interval polling: pakai `nextPollAfterMs` (dan `poll.nearMs`/`midMs`/`farMs`) dari dokumen, jangan nilai tetap di client.
- Saat `saleState` = `CLOSED` atau `soldOut` = `true`, hentikan polling.

**Zona dan posisi:** bandingkan `seq` milik sendiri dengan `headSeq`/`guaranteedUntilSeq`/`possibleUntilSeq`
(indikator murah, tanpa panggilan per pengguna). Posisi eksak dan zona otoritatif datang dari
`GET /api/v1/queue-entries/me?eventId=` (§2.3a).

### 2.3a Step 2b: Status entri sendiri (me) dan heartbeat

**`GET /api/v1/queue-entries/me?eventId={eventId}`** (visitor token; `eventId` wajib). Dipakai saat dekat giliran dan untuk
mendeteksi `ADMITTED`. Respons `EntryStatus` (wajib: `entryId`, `seq`, `status`, `state`, `rank`, `zone`):

| Field | Arti |
|-------|------|
| `state` | `WAITING`, `STALE`, `ADMITTED`, `CLAIMED`, `ENDED` (STALE = heartbeat senyap) |
| `status` | status tersimpan mentah |
| `rank` | urutan admisi (FIFO ketat atas `seq`); `null` saat `ENDED` |
| `exactPosition` | 1 + jumlah entri hidup (WAITING, bukan STALE) dengan seq lebih kecil; dihitung server |
| `zone` | `GUARANTEED`, `POSSIBLE`, `NOT_ENOUGH`; `null` saat ADMITTED/CLAIMED/ENDED |
| `nextHeartbeatMs` | interval heartbeat berikutnya (±20% jitter, lebih pendek dekat head) |
| `claimDeadline` | batas claim saat `ADMITTED` |
| `reason` | hanya saat `ENDED`: `LEFT`, `STALE_TIMEOUT`, `CLAIM_WINDOW_EXPIRED`, `PAGE_TIMER_EXPIRED`, `JOURNEY_ENDED` |
| `redirect` | tujuan redirect bila ada |

Entri yang sudah berakhir dijawab `200` dengan `state: ENDED` + `reason`; `404 NOT_FOUND` berarti visitor tidak pernah
punya entri untuk event itu. Giliran tiba = `state` `ADMITTED` dari server (jangan hitung di client).

**`POST /api/v1/queue-entries/me/heartbeat?eventId={eventId}`** (visitor token; tanpa body, tanpa data pribadi). Kirim
tiap `nextHeartbeatMs` selama `WAITING`/`STALE`. Senyap lebih dari 90 detik -> `STALE` (dilewati promotor, seq tetap,
hidup lagi pada heartbeat berikutnya); lebih dari 300 detik tanpa admisi -> `ENDED` (`STALE_TIMEOUT`). Respons
`HeartbeatStatus`: `state`, `rank`, `nextHeartbeatMs` (wajib), `entryId`.

| Status | Kondisi | Aksi |
|--------|---------|------|
| `401 VISITOR_TOKEN_INVALID` | Token tidak valid, dirotasi, atau perangkat (`X-Device-Id`) tidak cocok | Join ulang (dengan bearer bila login) |
| `404 ENTRY_NOT_FOUND` (heartbeat) / `404 NOT_FOUND` (me) | Entri berakhir / tidak ada | Panggil `me` untuk `reason`; tawarkan gabung ulang |
| `429 RATE_LIMITED` | Melebihi batas per visitor | Hormati `Retry-After` |

**Keluar antrean:** `DELETE /api/v1/queue-entries/{entryId}?eventId={eventId}` (visitor token) -> `204`; `401
VISITOR_TOKEN_INVALID`, `404 NOT_FOUND`.

### 2.4 Step 3: Claim (Saat Giliran Tiba)

Ketika entry berstatus `ADMITTED` (ditentukan server):

**Endpoint:** `POST /api/v1/queue-entries/{entryId}/claim?eventId={eventId}` (`eventId` wajib)

**Headers:** visitor token (cookie `rb_visitor` atau `X-Visitor-Token`). Tidak memakai `Authorization: Bearer`.

**Response (200):**
```json
{
  "meta": { "code": 200, "requestId": "..." },
  "data": {
    "admissionToken": "jwt-one-time-use",
    "bookingUrl": "https://app.example.com/book/{eventId}"   // deprecated (Q4 ii): navigasi dari admissionToken
  }
}
```

**Errors:**
| Status | Kondisi | Aksi |
|--------|---------|------|
| `401 VISITOR_TOKEN_INVALID` | Token visitor dirotasi (generasi lama) | Join ulang |
| `404 NOT_FOUND` | Entry tidak ada, bukan milik visitor ini, atau sudah selesai (LEFT/ENDED/COMPLETED) | Redirect home |
| `409 NOT_ADMITTED` | Entry masih WAITING | Lanjut menunggu |
| `410 TURN_MISSED` | Giliran terlewat | Redirect home dengan reason `TURN_MISSED` |
| `410 EVENT_SOLD_OUT` | Tiket habis | Redirect home dengan reason `SOLD_OUT` |
| `429 RATE_LIMITED` | Terlalu banyak request | Hormati `Retry-After` |

**Admission token:**
- TTL: 60 detik, sekali pakai
- Digunakan untuk `POST /api/v1/booking-sessions` (dikirim di body `{"admissionToken": "..."}`)

**`bookingUrl` (deprecated, keputusan pemilik Q4 ii; dipertahankan satu rilis, klien menavigasi dari `admissionToken`):** halaman booking publik `{BOOKING_PAGE_BASE_URL}/{eventId}`. URL tidak memuat token, entryId, atau data
pembeli; klien membuka halaman itu lalu mengirim `admissionToken` (dari respons claim) ke
`POST /api/v1/booking-sessions`. Tidak ada route baru.

### 2.5 Step 4: Create Booking Session

**Endpoint:** `POST /api/v1/booking-sessions`

**Headers:** visitor token (cookie `rb_visitor` atau `X-Visitor-Token`).

**Body:** `{"admissionToken": "<dari respons claim>"}` (bukan `Authorization: Bearer`).

**Response (201)** (amplop `{meta, data}`; membuat atau memulihkan sesi):
```json
{
  "meta": { "code": 201, "requestId": "..." },
  "data": {
    "sessionId": "session-uuid",
    "eventId": "event-uuid",
    "phase": "SELECTING",
    "reservedUnits": 3,
    "outstandingUnits": 3,
    "maxSelectable": 3,
    "expiresAt": "2026-10-19T10:05:00Z",
    "serverTime": "2026-10-19T10:00:00Z",
    "categories": [
      { "categoryId": "cat-uuid", "code": "VIP", "name": "VIP", "priceMinor": 1500000, "available": 50 }
    ],
    "allowMixedCategories": true,
    "editEnabled": true,
    "maxEdits": 3,
    "selectionWarningSeconds": 60,
    "submitGraceSeconds": 5,
    "booking": null
  }
}
```

- `phase`: `SELECTING`, `EDITABLE`, `LOCKED`, `ENDED`. Harga dalam `priceMinor` (minor unit IDR).
- `outstandingUnits`: unit cadangan yang belum di-hold (0 setelah hold); batas jumlah item pada hold.

**Errors:** `403 ADMISSION_TOKEN_INVALID` (token salah atau dipakai ulang), `410 TURN_MISSED` / `410 SESSION_EXPIRED`,
`401 UNAUTHORIZED` (visitor token tidak valid).

**Pulihkan sesi** (refresh, koneksi putus, back): `GET /api/v1/booking-sessions/current?eventId={eventId}` -> `200`
(sesi, dan `booking` bila sudah hold), `404 NOT_FOUND` bila tidak ada sesi aktif, `429 RATE_LIMITED`.
**Keluar tanpa membeli:** `POST /api/v1/booking-sessions/{sessionId}/cancel` -> `204`.

---

## 3. SSE (Server-Sent Events) untuk Ketersediaan

### 3.1 Connect SSE

**Endpoint:** `GET /api/v1/booking-sessions/{sessionId}/stream`

**Headers:**
- `Accept: text/event-stream`
- visitor token (cookie `rb_visitor` atau `X-Visitor-Token`)

**Events** (satu pasang `availability` + `timer` per tick; heartbeat `SSE_HEARTBEAT_SECONDS`):
```
event: availability
data: {"categories":[{"categoryId":"cat-uuid","available":49}],"serverTime":"2026-10-19T10:05:00Z"}

event: timer
data: {"expiresAt":"2026-10-19T10:10:00Z","serverTime":"2026-10-19T10:05:00Z","phase":"SELECTING"}
```

`503 DEPENDENCY_UNAVAILABLE` = batas koneksi SSE instance tercapai: beralih ke polling (§3.2). Error lain: `401
UNAUTHORIZED`, `403 VISITOR_MISMATCH`, `404 NOT_FOUND`, `400 VALIDATION_ERROR`.

### 3.2 Fallback Polling

Jika SSE gagal atau tidak tersedia:

**Endpoint:** `GET /api/v1/booking-sessions/{sessionId}` (sisa waktu dan ketersediaan live; `410 SESSION_EXPIRED` bila
sesi habis)

**Polling interval:** 3 detik (nilai klien; kontrak tidak mengirim interval di sesi)

**Auto-reconnect SSE:** Exponential backoff (1s, 2s, 4s, max 30s)

---

## 4. Booking Flow

### 4.1 Validate Attendee NIK

Sebelum hold, cek NIK pemegang tiket satu per satu saat diketik:

**Endpoint:** `POST /api/v1/booking-sessions/{sessionId}/attendee-checks`

**Request:**
```json
{ "nik": "3201010101010001" }
```

**Response (200):**
```json
{
  "meta": { "code": 200, "requestId": "..." },
  "data": { "result": "AVAILABLE", "checksRemaining": 19 }
}
```

`result`: `AVAILABLE`, `ALREADY_REGISTERED`, `CHECK_LIMIT_REACHED`. NIK tidak pernah dikembalikan.

**Errors:**
| Status / Error Code | Kondisi |
|---------------------|---------|
| `422 NIK_INVALID` | Struktur NIK salah (jatah cek tidak berkurang) |
| `429 RATE_LIMITED` | Terlalu banyak request (`Retry-After`) |
| `403 VISITOR_MISMATCH` / `404 NOT_FOUND` / `401 UNAUTHORIZED` | Sesi bukan milik pemanggil / tidak ada / token tidak valid |

### 4.2 Create Booking (Hold)

**Endpoint:** `POST /api/v1/bookings`

**Headers:**
- visitor token (cookie `rb_visitor` atau `X-Visitor-Token`)
- `Idempotency-Key: <16..64 karakter>` (**wajib**, unik per tombol; retry memakai key yang sama)
- `Authorization: Bearer <accessToken>` opsional; hanya mengidentifikasi akun, tidak pernah menyuplai NIK.

**Request** (ADR-030: data pembeli diisi di sini, bukan di join; `buyer` wajib):
```json
{
  "sessionId": "session-uuid",
  "buyer": {
    "nik": "3201010101010001",
    "email": "pembeli@contoh.id",
    "emailConfirmation": "pembeli@contoh.id",
    "consent": true
  },
  "items": [
    { "categoryId": "cat-uuid", "attendee": { "nik": "3201010101010002", "fullName": "Budi Santoso" } },
    { "categoryId": "cat-uuid", "attendee": { "nik": "3201010101010003", "fullName": "Siti Aminah" } }
  ]
}
```

- `items`: 1..3, satu item per tiket (1 qty = 1 NIK pemegang), tidak boleh melebihi `outstandingUnits` sesi.
- `buyer.consent` harus `true`; `emailConfirmation` harus sama dengan `email`. Pengguna login boleh diprefill dari akun
  di klien; server tetap memvalidasi.

**Response (201):** amplop `{meta, data}`; `data` = `Booking`:
```json
{
  "meta": { "code": 201, "requestId": "..." },
  "data": {
    "bookingId": "booking-uuid",
    "publicRef": "RBT-XXXX",
    "orderId": "RBT-XXXX",
    "status": "PENDING_PAYMENT",
    "amountMinor": 3000000,
    "currency": "IDR",
    "ticketQuantity": 2,
    "buyerNikMasked": "3201********0001",
    "paymentExpiresAt": "2026-10-19T10:15:00Z",
    "serverTime": "2026-10-19T10:05:00Z",
    "version": 1,
    "canEdit": true,
    "editsRemaining": 3,
    "items": [ { "itemId": "item-uuid", "categoryId": "cat-uuid", "attendeeNikMasked": "3201********0002", "attendeeName": "Budi Santoso" } ]
  }
}
```

`orderId` sama dengan `publicRef`. `status`: `PENDING_PAYMENT`, `VERIFYING`, `PAID`, `EXPIRED`, `CANCELLED`, `REVIEW`.
Timer pembayaran server-authoritative (`paymentExpiresAt` vs `serverTime`).

**Errors:**
| Status | errorCode | Aksi |
|--------|-----------|------|
| `400` | `VALIDATION_ERROR` | Perbaiki field |
| `409` | `ATTENDEE_NIK_ALREADY_REGISTERED`, `ATTENDEE_NIK_DUPLICATE_IN_ORDER`, `IDEMPOTENCY_KEY_REUSED`, `EVENT_CANCELLED` | Tampilkan pesan sesuai kode |
| `409` | `CATEGORY_UNAVAILABLE: <id>[,<id>]` | Tandai kategori habis (id setelah awalan `: `), baca ulang ketersediaan |
| `410` | `SESSION_EXPIRED` | Redirect home `SESSION_EXPIRED` |
| `422` | `QUANTITY_EXCEEDS_LIMIT`, `QUANTITY_INVALID`, `MIXED_CATEGORIES_NOT_ALLOWED`, `NIK_INVALID`, `EMAIL_MISMATCH`, `CONSENT_REQUIRED`, `BUYER_NIK_LIMIT_REACHED` | `BUYER_NIK_LIMIT_REACHED`: "NIK ini sudah mencapai batas, gunakan NIK lain"; sesi dan timer tetap |
| `429` | `BUYER_NIK_THROTTLED` | Tunggu `Retry-After` |
| `503` | `DEPENDENCY_UNAVAILABLE` | Ulangi dengan `Idempotency-Key` yang sama |
| `401` | `UNAUTHORIZED` | Visitor token tidak valid |

**Endpoint terkait:** `PUT /api/v1/bookings/{bookingId}/items` (ubah pesanan sebelum bayar, kirim `version`, `Idempotency-Key`
wajib), `GET /api/v1/bookings/{bookingId}`, `POST /api/v1/bookings/{bookingId}/cancel`,
`GET /api/v1/bookings/{bookingId}/tickets` (setelah `PAID`), `GET /api/v1/me/bookings` (login).

---

## 5. Token Summary

| Token | Disimpan di | TTL | Kegunaan |
|-------|-------------|-----|----------|
| `visitorToken` | Web: cookie HttpOnly `rb_visitor`; app: header `X-Visitor-Token` (secure storage) | 24 jam (`TOKEN_VISITOR_TTL`) | Identitas perangkat untuk antrean, claim, sesi, hold |
| `queueToken` (deprecated, Q6) | — | 1 jam (`TOKEN_QUEUE_TTL`) | Dikembalikan pada join satu rilis lagi, tetapi tidak dipakai endpoint mana pun (lihat Catatan kontrak vs kode) |
| `admissionToken` | Memory (sekali pakai) | 60 detik | Body `POST /booking-sessions` |
| `accessToken` (login) | Memory | 15 menit | `Authorization: Bearer`; opsional pada join/hold untuk mengidentifikasi akun |
| `refreshToken` (login) | Body respons `TokenResponse` (disimpan klien) | 30 hari (720 jam) | Rotasi lewat `POST /api/v1/auth/refresh`; token lama yang dipakai ulang mencabut semua sesi |

---

## 6. Error Handling

### 6.1 Standard Error Response

Semua service memakai amplop `{meta, data}`; pada error `data` = `null` dan kode ada di awalan `meta.message`
(format `"KODE: pesan"`). Field `errorCode` dan `details` tidak lagi dikirim. `requestId` pada error dikirim di header
`X-Request-ID`.

```json
{
  "data": null,
  "meta": {
    "code": 409,
    "message": "ACCOUNT_ALREADY_IN_QUEUE: akun sudah antre di perangkat lain"
  }
}
```

Klien membaca kode dari `meta.message` sebelum `": "`.

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
| `GET /api/v1/events` | 2 detik | path + `timeframe`, `city`, `genres`, `startDate`, `endDate`, `status`, `sort` |
| `GET /api/v1/events/facets` | 2 detik | path + filter yang sama kecuali `sort` |
| `GET /api/v1/events/{id}` | 2 detik | path |
| `GET /api/v1/queue-status/{eventId}` | 1 detik (+ `stale-while-revalidate=2`) | path only |
| Semua POST/PUT/DELETE dan endpoint per-visitor (`me`, sesi, booking) | No cache | — |

---

## 8. Mobile-Specific

### 8.1 Device ID dan visitor token

Generate satu kali per instalasi dan simpan di SecureStorage; kirim sebagai `X-Device-Id` (§ "Antrean, liveness dan
posisi"). Visitor token dari body respons join disimpan di SecureStorage dan dikirim sebagai `X-Visitor-Token`.
```javascript
const deviceId = crypto.randomUUID();
await SecureStore.setItemAsync('deviceId', deviceId);
```

### 8.2 Push Notification

Subscribe untuk notifikasi giliran (Notification Service, port lokal 8084; auth `X-Visitor-Token`):

**Endpoint:** `POST /api/v1/push-subscriptions` -> `204` (token disimpan terenkripsi, dihapus saat penjualan berakhir)

**Request:**
```json
{
  "eventId": "event-uuid",
  "platform": "ANDROID",
  "pushToken": "fcm-device-token"
}
```

`platform`: `ANDROID` atau `IOS`. Hapus: `DELETE /api/v1/push-subscriptions/{eventId}` -> `204`.

**Push events:** `TURN_SOON` (giliran sebentar lagi) dan `TURN_ARRIVED` (giliran tiba, segera claim); push hanya bonus.

### 8.3 Background Handling

- App di background tidak menerima polling/SSE
- Andalkan push notification untuk giliran
- Saat app kembali ke foreground: panggil `me`, lanjutkan heartbeat, reconnect SSE

---

## 9. Checklist Implementasi

- [ ] Generate dan persist `deviceId` (`X-Device-Id`)
- [ ] Join tanpa body, dengan retry dan `Idempotency-Key`; simpan visitor token terbaru
- [ ] Polling status document dengan `nextPollAfterMs` dari server
- [ ] Heartbeat tiap `nextHeartbeatMs` selama `WAITING`/`STALE`
- [ ] Tampilkan posisi eksak dari `EntryStatus.exactPosition` (server)
- [ ] Auto-claim saat `EntryStatus.state === 'ADMITTED'`
- [ ] Hold dengan blok `buyer` (NIK, email dua kali, consent) dan `Idempotency-Key`
- [ ] SSE connection dengan auto-reconnect
- [ ] Fallback polling jika SSE gagal
- [ ] Handle semua error codes dengan UI yang sesuai (baca kode dari `meta.message`)
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


## Perubahan yang dibutuhkan web dan app (ADR-030, tanpa edit repo frontend)

Antrean (Queue):
1. Hapus form NIK/email/consent dari langkah join; panggil `POST .../queue-entries` tanpa body (kirim visitor token dan
   bearer bila ada, serta `Idempotency-Key` untuk retry).
2. Tangani `200` (entri sudah ada) sama dengan `201`; simpan visitor token baru bila ada di respons (re-attach akun).
3. Tangani `409 ACCOUNT_ALREADY_IN_QUEUE`: tampilkan bahwa akun sudah antre di perangkat lain.
3a. Tangani `409 QUEUE_NOT_CONFIGURED`: antrean untuk event ini belum dibuka oleh penyelenggara. Tampilkan pesan tenang
    (mis. "Antrean belum dibuka, silakan kembali sebentar lagi"), **jangan** coba ulang otomatis dan jangan tampilkan
    kode error ke pembeli; tombol manual "Coba lagi" boleh. Kode ini juga dijawab untuk id event yang tidak dikenal.
4. Hapus pemeriksaan bot di klien (Turnstile, proof-of-work, Play Integrity/App Attest): tidak dibangun.
5. Tangani 429 dengan `Retry-After` pada join (batas lunak per IP).

Halaman Booking (urutan seperti tiket pesawat):
1. Bagian pembeli dahulu: NIK pembeli, email dua kali, consent. Pengguna login: prefill dari akun lalu konfirmasi.
2. Daftar kategori memakai ketersediaan hidup; setiap +1 qty menambah satu form pemegang (NIK + nama); 1 qty = 1 NIK.
3. Batas qty = `outstandingUnits` pada session view (sampai 3), bukan 3 per kategori.
4. Tombol lanjut mengirim `POST /api/v1/bookings` dengan blok `buyer` dan `items`; timer 5 menit booking mulai di sini.
5. `422 BUYER_NIK_LIMIT_REACHED`: tampilkan "NIK ini sudah mencapai batas, gunakan NIK lain"; sesi dan timer tetap; kirim ulang
   dengan NIK lain dan Idempotency-Key yang sama. `429 BUYER_NIK_THROTTLED`: tunggu `Retry-After`.
6. `409 CATEGORY_UNAVAILABLE: <id>[,<id>]`: tandai kategori itu habis (baca id setelah awalan `: `), baca ulang
   ketersediaan, biarkan pembeli memilih kategori lain; unit cadangan tetap ada.

Antrean, liveness dan posisi (ADR-030):
1. Kirim `X-Device-Id` (id acak per instalasi/profil browser, dibuat sekali) di join, me, heartbeat, claim dan leave.
   Token yang terbit bersama header ini hanya berlaku dengan header yang sama (tanpa header atau nilai lain:
   `401 VISITOR_TOKEN_INVALID`; pada join dianggap visitor baru).
   Pengiriman token: web memakai cookie HttpOnly `rb_visitor` saja (body tidak pernah berisi token, walau header
   `X-Client-Type: mobile` dikirim); app native wajib mengirim `X-Client-Type: mobile` pada join untuk menerima
   `visitorToken` di body, lalu mengirimnya sebagai header `X-Visitor-Token` (bukan `Authorization: Bearer`).
2. Selalu simpan visitor token terbaru dari setiap respons join (generasi token dirotasi saat re-attach akun atau join
   ulang); `401 VISITOR_TOKEN_INVALID` di heartbeat/me/claim/leave berarti token tidak valid atau lama: join ulang
   (dengan bearer bila login). Kunci penanganan pada `errorCode` `VISITOR_TOKEN_INVALID`, bukan pada status 401
   saja (401 access token User = `UNAUTHORIZED`, ditangani lewat refresh; `VISITOR_TOKEN_INVALID` tidak pernah memicu
   refresh access token). `404 NOT_FOUND` di me/claim/leave berarti
   visitor belum pernah bergabung ke event itu atau entri bukan miliknya/tidak ada: jangan refresh token, bergabunglah.
3. Kirim `POST /api/v1/queue-entries/me/heartbeat?eventId=` tiap `nextHeartbeatMs` (dari heartbeat/me) selama
   WAITING/STALE. `404 ENTRY_NOT_FOUND` = entri berakhir: panggil me untuk `reason` dan tawarkan gabung ulang.
4. Posisi: bandingkan `seq` milik sendiri dengan `guaranteedUntilSeq`/`possibleUntilSeq`/`headSeq` di status
   document; `zone` dari me otoritatif. (Draw dibuang — ADR-030 Amandemen 2026-11-05; semua join FIFO ketat atas seq,
   tidak ada field `*Rank`.)
5. (Dihapus — draw dibuang; tidak ada status `DRAWING`/`drawing:true` dan tidak ada field rank null.)
6. `me` untuk entri berakhir menjawab 200 `state: ENDED` + `reason` (LEFT, STALE_TIMEOUT, CLAIM_WINDOW_EXPIRED,
   PAGE_TIMER_EXPIRED, JOURNEY_ENDED); tampilkan pesan sesuai reason.
7. Deteksi "giliran sebentar lagi" dari status document yang di-cache CDN (`seq` sendiri vs `headSeq`); push hanya bonus.

## Catatan kontrak vs kode (butir 1-2 selesai; butir 3-4 diputuskan pemilik)

1. (Selesai) Mobile vs web pada join: `X-Client-Type: mobile` kini tercantum di `queue.yaml`; body berisi token hanya untuk
   klien mobile tanpa cookie `rb_visitor` valid, cookie selalu di-set (ADR-030 addendum, Batch 2).
2. (Selesai) `X-Device-Id` berlaku di join, me, heartbeat, claim dan leave (diverifikasi oleh `VisitorAuthenticator`),
   tercantum di `queue.yaml` untuk kelimanya, dan diizinkan di CORS lokal.
3. (Diputuskan: deprecated, Q6) `queueToken` ada di skema `QueueEntry` dan dikembalikan join, tetapi tidak ada endpoint
   yang menerimanya (claim dan leave memakai visitor token). Ditandai `deprecated`, dihapus setelah satu rilis.
4. (Diputuskan: dicadangkan, Q5) Kode error `ENTRY_MOVED` (409) terdaftar dan dipetakan di Queue tetapi belum
   dihasilkan jalur mana pun; desain masa depan (SRS-F-01-10). Dicatat di deskripsi operasi `queue.yaml`, klien tidak
   boleh membangun alur di atasnya.
