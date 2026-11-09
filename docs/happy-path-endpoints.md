# Happy Path dan Edge Case Endpoint: Guest, User Login, dan Admin

Panduan uji manual lintas service. Sumber: kontrak di `docs/contracts/openapi/*.yaml` dan route yang terdaftar di kode.

**Status: diuji nyata dengan curl pada 2026-11-09**, diuji ulang setelah perbaikan T1–T8 pada `feature/rework` @ `c63bbcf` terhadap 5 service (api + worker,
migrasi `migrate up`) yang berjalan di host. Hasil per skenario ada di bagian [Hasil uji curl](#hasil-uji-curl-2026-11-09);
temuan yang tidak sesuai kontrak ada di [Temuan](#temuan-perilaku-tidak-sesuai-kontrak). Kolom **Uji** di setiap tabel:
✅ lulus, ❌ tidak sesuai kontrak (lihat temuan), ➖ tidak diuji.

Skrip uji (dapat diulang): `.agents/tasks/local/run/start.sh` (build + migrasi + jalankan semua) dan
`.agents/tasks/local/run/curl/run_all.sh` (semua skenario; `fast` untuk melewati uji timer).

## Port lokal

| Service | API | Worker (health) |
|---|---|---|
| queue | 8081 | 8091 |
| booking | 8082 | 8092 |
| payment | 8083 | 8093 |
| notification | 8084 | 8094 |
| user | 8085 | — |
| Mailpit (OTP dan e-tiket) | 8025 | |
| Gateway Midtrans tiruan (lokal, tanpa kunci sandbox) | 18089 | |

Infra yang dipakai: PostgreSQL `localhost:5432` (database `berebut`, schema per service), Valkey cluster `7000-7005`,
RabbitMQ `5672`, Mailpit `1025/8025`.

Penyesuaian lokal di `start.sh` karena semua pembeli simulasi berasal dari satu IP (`127.0.0.1`):
`RATE_LIMIT_JOIN_PER_IP=300` (queue), `RATE_LIMIT_PER_IP=0` (booking, payment), `OTP_MAX_PER_IP_HOUR=1000` (user).
Payment memakai `MIDTRANS_BASE_URL`/`MIDTRANS_SNAP_BASE_URL=http://localhost:18089` (gateway tiruan
`.agents/tasks/local/run/fake_midtrans.py`) karena kunci sandbox tidak tersedia.

## Perbedaan dua actor

Antrean anonim untuk semua pembeli (ADR-030). NIK dan email diisi di Booking saat hold, bukan saat join antrean.

| | Guest | User login |
|---|---|---|
| Header `Authorization: Bearer` | tidak ada | ada (access token dari User Service) |
| Identitas antrean | visitor token saja | visitor token + akun (dedup per akun) |
| Re-attach antrean dari perangkat lain | tidak bisa | bisa (200, entri sama, visitor token baru) ✅ |
| `GET/PATCH /api/v1/me` | tidak bisa | bisa ✅ |
| `GET /api/v1/me/bookings` | tidak bisa (401) ✅ | bisa ✅ |
| NIK dan email di hold | diisi manual | boleh diprefill di klien; server tetap validasi |

Identitas visitor:
- Web: cookie HttpOnly `rb_visitor` (selalu diset oleh join bila token terbit). Body tidak pernah berisi token untuk
  pemegang cookie valid, walau header `X-Client-Type: mobile` dikirim (diperbaiki 2026-11-09).
- App: header `X-Visitor-Token`. Token dikirim di body join hanya bila request membawa **`X-Client-Type: mobile`** DAN
  tidak membawa cookie `rb_visitor` valid; app native wajib mengirim header itu pada join.
- `X-Device-Id` (opsional, id acak per instalasi): bila dikirim saat join, token terikat ke perangkat itu dan **wajib**
  dikirim lagi di setiap panggilan visitor (join, me, heartbeat, claim, leave); CORS lokal mengizinkannya. Visitor token
  sebagai `Authorization: Bearer` tidak diterima (401). Klien menangani 401 menurut `errorCode`:
  `VISITOR_TOKEN_INVALID` = join ulang (bukan refresh access token).
- Klien native dengan cookie jar: lihat catatan cookie jar vs `X-Visitor-Token` di `docs/analisa/08a-frontend-integration.md` (§ join).

---

## Actor A: Guest

### A1. Katalog (publik, tanpa auth)

```
GET :8082/api/v1/events?city=jakarta&genres=POP      filter kota (tanpa beda huruf besar/kecil) dan genre
GET :8082/api/v1/events/facets
GET :8082/api/v1/events/{eventId}                    -> catat categoryId; minPriceMinor, description (Slate)
```
Uji: ✅ semua.

### A2. Waiting room

```
POST :8081/api/v1/events/{eventId}/queue-entries
     Header: X-Client-Type: mobile (app), X-Device-Id (opsional), Idempotency-Key (opsional, 16-64)
     Body: tidak dibaca
     -> 201 WAITING atau langsung ADMITTED; simpan rb_visitor / X-Visitor-Token
     (`queueToken` di respons deprecated: abaikan, tidak diterima endpoint mana pun)

GET  :8081/api/v1/queue-status/{eventId}                  status publik (dokumen dipublikasikan tiap ~1 dtk)
GET  :8081/api/v1/queue-entries/me?eventId={eventId}      posisi eksak (butuh visitor token)
POST :8081/api/v1/queue-entries/me/heartbeat?eventId={eventId}   sinyal hidup
POST :8081/api/v1/queue-entries/{entryId}/claim?eventId={eventId}
     -> {admissionToken, bookingUrl (deprecated)} saat giliran tiba; navigasi dari `admissionToken`;
        auth = visitor token + `?eventId=`
```
Uji: ✅ semua. Event harus sudah dikonfigurasi antreannya oleh admin (lihat C2), kalau tidak join menjawab
`409 QUEUE_NOT_CONFIGURED`. `queue-status` bisa `404` sesaat sebelum dokumen pertama dipublikasikan.

### A3. Sesi booking

```
POST :8082/api/v1/booking-sessions
     {"admissionToken":"<token>"}                      -> 201, catat sessionId
GET  :8082/api/v1/booking-sessions/current?eventId={eventId}   pulihkan sesi (eventId wajib)
GET  :8082/api/v1/booking-sessions/{sessionId}         sisa waktu + ketersediaan
GET  :8082/api/v1/booking-sessions/{sessionId}/stream  SSE: event `timer` dan `availability`
POST :8082/api/v1/booking-sessions/{sessionId}/attendee-checks
     {"nik":"<16 digit>"}                              -> {result, checksRemaining}
POST :8082/api/v1/booking-sessions/{sessionId}/cancel  keluar tanpa membeli -> 204
```
Uji: ✅ semua, termasuk dampak cancel ke antrean (✅ T5: entri antrean `ENDED`, slot bebas, pembeli berikutnya diadmit). Cancel pada sesi yang sudah punya hold -> 409 `BOOKING_LOCKED`.

### A4. Hold

```
POST :8082/api/v1/bookings
     Header: Idempotency-Key (wajib, 16-64 karakter)
     {
       "sessionId": "<uuid>",
       "buyer": {
         "nik": "<16 digit>",
         "email": "buyer@example.com",
         "emailConfirmation": "buyer@example.com",
         "consent": true
       },
       "items": [
         {"categoryId": "<uuid>",
          "attendee": {"nik": "<16 digit>", "fullName": "Nama Pemegang"}}
       ]
     }                                                -> 201 PENDING_PAYMENT, catat bookingId

GET  :8082/api/v1/bookings/{bookingId}
PUT  :8082/api/v1/bookings/{bookingId}/items          ubah pesanan sebelum bayar
     Header: Idempotency-Key (wajib)
     {"version":1,
      "items":[{"categoryId":"<uuid>",
                "attendee":{"nik":"<16 digit>","fullName":"Nama Pemegang"}}]}
POST :8082/api/v1/bookings/{bookingId}/cancel         batal sebelum bayar -> 200 CANCELLED
```

`items` 1 sampai 3, satu item per tiket, maksimal 3 tiket per NIK pembeli per event, dan tidak boleh melebihi unit
cadangan sesi (`reservedUnits`). Uji: ✅ semua; varian `{"keepFromItemId":"<uuid>"}` untuk mempertahankan pemegang lama ✅ (T7 diperbaiki 2026-11-09;
uji unit + integrasi, belum dijalankan ulang lewat curl). Kirim `keepFromItemId` saja (tanpa `nik`/`fullName`).

### A5. Payment

```
GET  :8083/api/v1/payments/{bookingId}/page           view model halaman bayar (visitor token)
POST :8083/api/v1/payments/{bookingId}/page-tokens    token sekali pakai (mobile WebView) -> 201
POST :8083/api/v1/payment-page-sessions               {"pageToken":"..."} -> 204 + cookie rb_pay
GET  :8083/api/v1/payments/{bookingId}/page           dengan cookie rb_pay saja
POST :8083/api/v1/payments/{bookingId}/attempts
     Header: Idempotency-Key (16-64 karakter)
     {"method":"QRIS"}                                -> 201, redirectUrl Snap
     method aktif (PAYMENT_METHODS_ENABLED): VA_BCA | VA_BNI | VA_BRI | QRIS | GOPAY (SNAP = semua channel)
GET  :8083/api/v1/payments/{bookingId}/status
```
Uji: ✅ semua.

### A6. Gateway menyelesaikan pembayaran

Simulator lokal (✅ T8 diperbaiki 2026-11-09; uji unit + integrasi, belum dijalankan ulang lewat curl), hanya `APP_ENV=local|ci`:
```
POST :8083/api/v1/local-simulator/payments/{attemptId}/scenario   (visitor token pemilik attempt)
     {"scenario":"SETTLE|SETTLE_LATE|EXPIRE|DENY|TIMEOUT|DUPLICATE_WEBHOOK|OUT_OF_ORDER"}  -> 202 {"simulated":true,"scenario":"..."}
```
Menyuntikkan notifikasi Midtrans bertanda tangan ke webhook asli; di `dev|staging|prod` rute tidak ada (404 sama dengan path tak dikenal).
Yang dipakai saat uji: notifikasi HTTP bertanda tangan persis seperti Midtrans ke webhook Payment (lihat E1), dikirim
oleh gateway tiruan:
```
POST :18089/_sim/{orderId}/{settlement|pending|deny|expire|cancel}[?dup=1]
```
Uji: ✅ settlement → booking `PAID`.

### A7. Setelah bayar

```
GET :8082/api/v1/bookings/{bookingId}/tickets         e-tiket, hanya setelah PAID
```
Uji: ✅ tiket terbit (satu per item), email e-tiket sampai di Mailpit, entri antrean menjadi `COMPLETED`.

---

## Actor B: User login

Semua langkah A1 sampai A7 sama, dengan tambahan header `Authorization: Bearer <accessToken>` pada setiap panggilan
(kecuali katalog yang memang publik). Uji: ✅ (pembayaran VA_BCA sampai PAID dan tiket).

### B1. Login OTP

```
POST :8085/api/v1/auth/otp/request
     {"email":"buyer@example.com"}                    -> 202 (kode OTP lihat di Mailpit :8025)

POST :8085/api/v1/auth/otp/verify
     {"email":"buyer@example.com","code":"123456"}    -> 200 accessToken + refreshToken
                                                         (akun dibuat saat login pertama)
```

### B2. Profil dan sesi

```
GET   :8085/api/v1/me                                 (Bearer)
PATCH :8085/api/v1/me                                 (Bearer)
      {"fullName":"Nama Baru","nik":"<16 digit>"}     minimal satu field (body kosong -> 400)

POST  :8085/api/v1/auth/refresh
      {"refreshToken":"<token>"}                      refresh token dirotasi
POST  :8085/api/v1/auth/logout                        (Bearer)
      {"refreshToken":"<token>"}                      -> 204, idempoten
```

### B3. Khusus login

```
GET :8082/api/v1/me/bookings                          (Bearer) riwayat pesanan
```
Uji B1–B3: ✅ semua.

---

## C. Admin (internal, tidak lewat edge publik)

Bearer = access token User Service milik email di `ADMIN_EMAILS` (Booking dan Queue). Non-admin dan tanpa token -> 401.

### C1. Event (Booking, `internal-booking.yaml`)

```
POST  :8082/internal/v1/admin/events                  buat event (DRAFT) + kategori; city, genres, description
GET   :8082/internal/v1/admin/events/{eventId}        termasuk draf
PATCH :8082/internal/v1/admin/events/{eventId}        TTL, edit, city, genres (aturan terkunci setelah sale buka)
POST  :8082/internal/v1/admin/events/{eventId}/publish
POST  :8082/internal/v1/admin/events/{eventId}/unpublish
POST  :8082/internal/v1/admin/events/{eventId}/cancel          idempoten
POST  :8082/internal/v1/admin/events/{eventId}/images?kind=image|banner|thumbnail   multipart field `file`
```

### C2. Pengaturan antrean (Queue, `queue.yaml`)

```
GET :8081/internal/v1/admin/events/{eventId}/queue-settings    404 QUEUE_NOT_CONFIGURED bila belum ada
PUT :8081/internal/v1/admin/events/{eventId}/queue-settings
    {"activeSlotCapacity":5}                          201 dibuat / 200 diubah; hanya setelah event terbit
```

Urutan: buat event -> publish -> PUT queue-settings -> penjualan buka.

| Kasus | Hasil | Uji |
|---|---|---|
| Non-admin / tanpa token | 401 | ✅ |
| Draf tidak tampil di katalog publik | 404 | ✅ |
| `city` dirapikan, `genres` dikanonkan (`indie pop` -> `INDIE_POP`) | | ✅ |
| `paymentTtlSeconds` < 120 | 400 | ✅ |
| `selectionTtlSeconds` 60 dengan peringatan bawaan 60 dtk | 400 (`events_check3`: peringatan harus < TTL) | ✅ |
| Upload bukan gambar | 400 | ✅ |
| PUT queue-settings pada draf | 404 | ✅ |
| Aturan lintas field (`pollNearMs` > `pollMidMs`) | 400 | ✅ |
| Setiap perubahan pengaturan antrean tercatat di `queue.event_settings_audit` | | ✅ |
| Unpublish | hilang dari katalog publik | ✅ |
| Cancel | tetap bisa dibaca, status `CANCELLED`, keluar dari daftar | ✅ |
| Event tidak ada / bukan UUID | 404 / 400 | ✅ |

---

## D. Notifikasi push (`notification.yaml`)

```
POST   :8084/api/v1/push-subscriptions                (visitor token)
       {"eventId":"<uuid>","platform":"ANDROID|IOS","pushToken":"<token FCM/APNs>"}   -> 204
DELETE :8084/api/v1/push-subscriptions/{eventId}      -> 204
```
Uji: ✅ simpan, hapus, tanpa token 401, platform salah 400.

## E. Webhook dan health

### E1. Webhook Midtrans (`payment.yaml`)

```
POST :8083/api/v1/webhooks/midtrans
     signature_key = SHA512(order_id + status_code + gross_amount + MIDTRANS_SERVER_KEY)
```

### E2. Health

```
GET :{8081..8085, 8091..8094}/health                  200 UP / 503 DOWN
```
Uji: ✅ semua 9 proses.

---

## Edge case

### Antrean dan sesi

| Kasus | Hasil (kontrak) | Uji |
|---|---|---|
| Join dua kali dengan visitor token yang sama | 200, entri yang sama | ✅ |
| Join dengan Idempotency-Key yang sama | 200, replay entri yang sama | ✅ |
| Login punya entri aktif, join dari perangkat baru tanpa token | 200 (re-attach), visitor token baru. **Khusus login** | ✅ |
| Login + token entri anonim lain, akun sudah punya entri aktif | 409 `ACCOUNT_ALREADY_IN_QUEUE`. **Khusus login** | ✅ |
| Event belum buka penjualan (atau sudah lewat `salesEndsAt`) | 409 `EVENT_NOT_ON_SALE` | ✅ T1 (diperbaiki 2026-11-09) |
| Tiket habis | 410 `EVENT_SOLD_OUT` (setelah `booking.sold_out` sampai ke Queue, ≤ 5 dtk lewat outbox) | ✅ T2 (diperbaiki 2026-11-09) |
| Jendela penjualan belum diketahui dan Booking tidak terjangkau | 503 `DEPENDENCY_UNAVAILABLE` | ➖ (unit test) |
| Batas lunak per IP terlampaui | 429 `RATE_LIMITED` + `Retry-After` | ✅ |
| Visitor token salah, kedaluwarsa, atau perangkat tidak cocok (me, heartbeat) | 401 `VISITOR_TOKEN_INVALID` | ✅ |
| Sama, pada claim dan leave (sebelum entri dicari; sama untuk entri sendiri, orang lain, atau tidak ada) | 401 `VISITOR_TOKEN_INVALID` | ✅ T3 (diperbaiki 2026-11-09) |
| Visitor belum pernah bergabung ke event itu (me, claim, leave) | 404 `NOT_FOUND` | ✅ T4 (diperbaiki 2026-11-09) |
| Token valid, entri milik orang lain vs tidak ada (claim, leave) | 404 `NOT_FOUND`, respons identik | ✅ |
| Claim saat masih WAITING | 409 `NOT_ADMITTED` | ✅ |
| Claim setelah jendela klaim (180 dtk) lewat | 410 `TURN_MISSED` | ✅ |
| Admission token salah, dipakai ulang, atau dipakai visitor lain | 403 `ADMISSION_TOKEN_INVALID` | ✅ |
| Sesi booking habis (TTL Booking, dihitung dari aktivasi sesi) | 410 `SESSION_EXPIRED` | ✅ T6 (diperbaiki 2026-11-09) |
| Dependency sementara tidak tersedia (Payment mati saat cancel) | 503 `DEPENDENCY_UNAVAILABLE`, stok tetap ditahan; ulang berhasil | ✅ |
| Keluar antrean saat WAITING/ADMITTED | `DELETE :8081/api/v1/queue-entries/{entryId}?eventId=` -> 204, `LEFT` | ✅ |
| Keluar antrean saat CLAIMED | 409 `ENTRY_CLAIMED`; entri tetap CLAIMED, batalkan lewat `booking-sessions/{id}/cancel` (keputusan pemilik 2026-11-09) | ✅ T5 (diperbaiki 2026-11-09) |
| Keluar sesi tanpa membeli | `POST :8082/api/v1/booking-sessions/{sessionId}/cancel` -> 204, unit dilepas, entri antrean `ENDED`, slot bebas | ✅ unit, ✅ T5 slot antrean |

### Hold

| Kasus | Hasil | Uji |
|---|---|---|
| NIK pembeli sudah mencapai batas 3 tiket | 422 `BUYER_NIK_LIMIT_REACHED` (boleh coba NIK lain, key yang sama) | ✅ |
| Terlalu banyak percobaan NIK pembeli (setelah 10 penolakan) | 429 `BUYER_NIK_THROTTLED` + `Retry-After` | ✅ |
| NIK pemegang sudah terdaftar di event ini | 409 `ATTENDEE_NIK_ALREADY_REGISTERED` | ✅ |
| NIK pemegang duplikat dalam satu order | 409 `ATTENDEE_NIK_DUPLICATE_IN_ORDER` | ✅ |
| Kategori campur saat `allowMixedCategories=false` | 422 `MIXED_CATEGORIES_NOT_ALLOWED` | ✅ |
| Kategori habis | 409 `CATEGORY_UNAVAILABLE` | ✅ |
| Lebih dari 3 item / melebihi unit cadangan sesi | 422 `QUANTITY_EXCEEDS_LIMIT` / `QUANTITY_INVALID` | ✅ |
| NIK tidak valid (bukan 16 digit atau struktur salah) | 422 `NIK_INVALID` | ✅ |
| `email` tidak sama dengan `emailConfirmation` | 422 `EMAIL_MISMATCH` | ✅ |
| `consent` bukan true | 422 `CONSENT_REQUIRED` | ✅ |
| Idempotency-Key sama, body sama | 201, booking yang sama (replay) | ✅ |
| Idempotency-Key sama, body berbeda | 409 `IDEMPOTENCY_KEY_REUSED` | ✅ |
| Event dibatalkan | 409 `EVENT_CANCELLED` | ✅ |
| Batal booking sebelum bayar | `POST :8082/api/v1/bookings/{bookingId}/cancel` -> 200, stok kembali | ✅ |
| Batal setelah attempt pembayaran dibuat | 409 `BOOKING_LOCKED` | ✅ |
| Booking milik visitor lain | 404/403 | ✅ |
| Timer pembayaran habis tanpa bayar | Payment membatalkan order di gateway, booking `EXPIRED` setelah masa tenggang, stok kembali | ✅ |

### Payment

| Kasus | Hasil | Uji |
|---|---|---|
| Booking tidak bisa dibayar (sudah PAID) | 409 `BOOKING_NOT_PAYABLE` (attempt dan page token) | ✅ |
| Idempotency-Key sama, body sama / berbeda | 201 replay / 409 `IDEMPOTENCY_KEY_REUSED` | ✅ |
| Metode tidak aktif (`VA_PERMATA`) | 422 `PAYMENT_METHOD_UNAVAILABLE` | ✅ |
| Page token dipakai ulang | 403 `PAYMENT_PAGE_TOKEN_INVALID` | ✅ |
| Tanpa visitor token | 401 | ✅ |
| Webhook signature salah | 401, state tidak berubah | ✅ |
| Webhook nominal salah / order tidak dikenal | 200 (diakui), state tidak berubah | ✅ |

Skenario gateway (lewat gateway tiruan + webhook bertanda tangan):

| Skenario | Hasil | Uji |
|---|---|---|
| settlement | attempt `SUCCEEDED`, booking `PAID`, tiket + email | ✅ |
| webhook ganda | idempoten: tiket terbit sekali | ✅ |
| tidak berurutan (pending setelah settlement) | tetap `PAID` | ✅ |
| deny | attempt `FAILED`, booking tetap bisa dibayar; attempt kedua berhasil | ✅ |
| expire, lalu settlement terlambat | attempt `EXPIRED`, lalu `SUCCEEDED`/`PAID` (uang adalah sumber kebenaran) | ✅ |
| timeout gateway | — | ➖ (gateway tiruan belum mensimulasikan) |

### Autentikasi (khusus login)

| Kasus | Hasil | Uji |
|---|---|---|
| OTP salah | 401 `OTP_INVALID` (`attemptsLeft`) | ✅ |
| OTP salah 5 kali | 429 `OTP_LOCKED` | ✅ |
| Minta OTP lagi sebelum cooldown 60 dtk | 429 `OTP_RESEND_TOO_SOON` | ✅ |
| Lebih dari 5 OTP per email per jam | 429 | ✅ |
| Lebih dari `OTP_MAX_PER_IP_HOUR` per IP (bawaan 20) | 429 | ✅ |
| Email tidak valid | 400 | ✅ |
| Refresh token dirotasi | 200, token baru | ✅ |
| Refresh token lama dipakai ulang | 401, seluruh keluarga sesi dicabut (refresh baru juga ditolak) | ✅ |
| Access token setelah logout | 401 | ✅ |

---

## Hasil uji curl (2026-11-09)

| Suite | Isi | Lulus | Gagal |
|---|---|---|---|
| guest | A1–A7 lengkap sampai tiket + email | 45 | 1 (T7) |
| user | B1–B3, journey login, re-attach, `ACCOUNT_ALREADY_IN_QUEUE`, refresh/logout | 31 | 0 |
| admin | C1, C2 | 33 | 2 (T1) |
| edge queue | antrean dan sesi | 22 | 7 (T2, T3, T4, T5) |
| edge hold | hold dan cancel | 30 | 0 |
| edge payment | payment, webhook, skenario gateway | 29 | 1 (T8) |
| misc | health, push, OTP | 20 | 0 |
| slow | TTL sesi, jendela klaim, TTL pembayaran, limit OTP per email, slot setelah cancel sesi | 6 | 3 (T5, T6) |
| ratelimit | batas join per IP | 3 | 0 |
| dependency | Payment mati saat cancel | 5 | 0 |
| **Total** | | **224** | **14** |

**Uji ulang setelah semua perbaikan (2026-11-09, `feature/rework` @ `c63bbcf`):**

| Suite | Lulus | Gagal |
|---|---|---|
| guest | 46 | 0 |
| user | 31 | 0 |
| admin | 35 | 0 |
| edge queue | 62 | 0 |
| edge hold | 30 | 0 |
| edge payment (termasuk simulator lokal T8) | 33 | 0 |
| misc | 20 | 0 |
| slow (timer nyata) | 9 | 0 |
| ratelimit | 3 | 0 |
| dependency | 5 | 0 |
| **Total** | **274** | **0** |

Catatan uji ulang: dua cek `slow` pertama kali gagal karena skrip uji (interval polling 15 dtk; permintaan OTP ke-6
jatuh di jendela jam baru — limiter OTP memakai jendela jam tetap), lalu lulus setelah skrip diperbaiki. Cakupan ini
adalah skenario di dokumen ini, bukan seluruh 237 respons kontrak (lihat rencana Batch 4).

Invarian yang teramati terjaga: tidak ada oversell (kontensi VIP, `held+sold ≤ capacity`), kuota 3 tiket per NIK
pembeli, pembayaran idempoten (webhook ganda), timer pembayaran server-authoritative, stok ditahan saat Payment tidak
terjangkau.

Rerun 2026-11-09 pada cabang perbaikan (build worktree, `run_all.sh`): admin 35/0, edge hold 30/0, edge payment 29/1 (T8),
edge queue 36/3 (T3, T4; termasuk 5 cek baru sold-out → `SOLD_OUT`), guest 45/1 (T7), misc 20/0, slow 9/0, ratelimit 3/0,
user 31/0, dependency 5/0 (dijalankan ulang terpisah: di `run_all` ia berbagi jendela join per IP yang baru dihabiskan ratelimit).

Rerun 2026-11-09 pada cabang `fix/queue-visitor-token` (hanya Queue api dari worktree; sisanya biner lama): edge queue
58 lulus / 4 gagal. T3 dan T4 kini lulus. Cek baru: web join tanpa token di body + cookie HttpOnly, mobile palsu + cookie
tetap tanpa token di body, native mendapat token di body, claim entri orang lain vs tidak ada identik 404, claim token
sampah 401, dan preflight `X-Device-Id` 204 di Queue. 4 kegagalan = preflight di Booking/Payment/Notification/User
yang masih biner lama (belum dibangun ulang); instans worktree keempat service itu dijalankan terpisah di port
sementara dan menjawab 204 dengan `X-Device-Id` pada `Access-Control-Allow-Headers`. Stack milik pemilik dipulihkan
(Queue api biner semula).

## Temuan (perilaku tidak sesuai kontrak)

Semua direproduksi dengan curl dan dicek ke kode. T1, T2, T5, T6 diperbaiki 2026-11-09 (`docs/evidence/2026-11-09-queue-booking-flow-fixes.md`); T3, T4 diperbaiki 2026-11-09 (`docs/evidence/2026-11-09-queue-visitor-token.md`); T7, T8 diperbaiki 2026-11-09 (`docs/evidence/2026-11-09-booking-payment-contract.md`).

| # | Temuan | Bukti | Dampak |
|---|---|---|---|
| T1 | Join diterima sebelum `salesStartsAt` | 201 pada event `NOT_STARTED`; `ErrNotOnSale` tidak pernah dikembalikan | Melanggar SRS-F-04-01 **Diperbaiki 2026-11-09.** |
| T2 | Join diterima setelah tiket habis | 201 `WAITING` (tetap WAITING), padahal sale hash Queue `soldOut=1`; `join.lua` tidak memeriksa sale | Pembeli menunggu event yang sudah habis **Diperbaiki 2026-11-09**; lanjutan: saat Queue memproses `booking.sold_out`, semua entri WAITING/STALE event itu diakhiri aktif dengan status `SOLD_OUT` (`me`: state `ENDED`, status `SOLD_OUT`, reason `SOLD_OUT`; dokumen status `soldOut=true`), ADMITTED/CLAIMED tidak disentuh, batch 500 dan idempoten. |
| T3 | Claim dan leave menjawab 404 untuk token salah/perangkat lain | me/heartbeat 401, claim/leave 404 | Klien tidak bisa membedakan "join ulang" dari "entri hilang" **Diperbaiki 2026-11-09.** |
| T4 | `me` untuk event yang tidak pernah di-join menjawab 401, kontrak 404 | `checkTokenGen` per event | Kode error berbeda dari kontrak **Diperbaiki 2026-11-09.** |
| T5 | Slot antrean bocor setelah sesi booking dibatalkan | `booking-sessions/{id}/cancel` melepas unit tetapi tidak menerbitkan `booking.journey.ended`; entri tetap `CLAIMED`, pembeli berikutnya tetap WAITING > 390 dtk; `DELETE` entri CLAIMED menjawab 204 tetapi diabaikan (`leave.lua`, QSH-OQ-3) | Kapasitas antrean berkurang permanen per pembatalan **Diperbaiki 2026-11-09.** |
| T6 | TTL Booking (`selection_ttl_seconds`) tidak diterapkan | Saat sesi aktif, `expires_at` tetap = reservasi + jendela klaim (180 dtk); hold setelah TTL 90 dtk tetap 201 | Lama sesi = jendela klaim, bukan TTL Booking 5 menit **Diperbaiki 2026-11-09.** |
| T7 | `keepFromItemId` pada `PUT /bookings/{id}/items` tidak didukung | 400 "fullName harus 2..100"; field tidak dibaca di kode | Klien harus mengirim ulang NIK pemegang **Diperbaiki 2026-11-09.** |
| T8 | Endpoint `local-simulator` di kontrak tidak ada di kode | 404 di `APP_ENV=local` | Uji lokal butuh gateway tiruan **Diperbaiki 2026-11-09.** |

Catatan kecil: rentang `selection_ttl_seconds` 60–1800 di SDD §6 praktis mulai 61 karena peringatan bawaan 60 dtk
harus lebih kecil dari TTL.

## Cakupan kontrak

Pass matriks kontrak (2026-11-09): setiap respons yang tertulis di `docs/contracts/openapi/*.yaml` (metode × path × status × errorCode, termasuk `/health`) dipanggil dengan HTTP nyata ke stack lokal dan dibandingkan dengan kontrak. Dari **249 baris**: **234 sesuai** (status + errorCode), **10 dijalankan tetapi berbeda**, **4 tidak bisa dipicu lokal** (alasan tercatat), **1 tidak berlaku** (teks kontrak sendiri menegasikannya). Tidak ada baris yang belum dijalankan.

Temuan terpenting (semua dicek ke kode, belum diperbaiki): permintaan tanpa token visitor lolos di `GET /booking-sessions/{id}`, `PUT /bookings/{id}/items` (booking bisa diubah tanpa autentikasi) dan `POST .../attendee-checks` (tanpa pengecekan pemilik), `POST /bookings` tanpa token menjawab 500, serta `DELETE /push-subscriptions/{id}` dengan id non-UUID menjawab 500. Sisanya baris kontrak yang tidak pernah bisa muncul (mis. 410 pada `POST /booking-sessions`, 409 pada PATCH event, 429 pada claim).

Laporan lengkap, matriks per baris, dan daftar respons tak terdokumentasi: [`.agents/tasks/fix/contract-matrix/report.md`](../.agents/tasks/fix/contract-matrix/report.md), `matrix.tsv`, `undocumented.tsv`. Skrip: `.agents/tasks/local/run/curl/t_matrix.sh` (`matrix/`). Bukti: `docs/evidence/2026-11-09-contract-matrix.md`.

## Urutan uji yang menutup semua cabang

1. Guest: A1 sampai A7 dengan settlement (happy path).
2. Login: B1, lalu A1 sampai A7 dengan Bearer, lalu B3.
3. Admin: C1 dan C2 (event baru selalu butuh publish + PUT queue-settings sebelum join).
4. Ulangi A4 sampai A6 dengan setiap kasus di tabel edge dan setiap skenario gateway.
5. Uji re-attach dan `ACCOUNT_ALREADY_IN_QUEUE` hanya dengan actor login.
6. Uji refresh token reuse dan logout.
7. Uji timer (TTL sesi, jendela klaim, TTL pembayaran) dan batas laju terakhir, karena butuh waktu tunggu dan
   menghabiskan kuota per IP.
