# Design — User Service

| Field | Value |
|-------|-------|
| **Service** | User Service |
| **Sumber** | SDD-USER-001 v1.1 + implementasi `services/user` |
| **Port** | 8085 |
| **Schema** | `users` (role `svc_user`) |
| **Bahasa/stack** | Go 1.26, net/http, Cobra, pgx v5, valkey-go, EdDSA (`pkg/token`), AES-256-GCM + HMAC (`pkg/crypto`) |

> Desain ini **digroundkan ke kode**. Bagian yang berbeda dari SDD ditandai **(delta Dx)**
> dan dirinci di `requirements.md` → Deltas.

---

## 1. Arsitektur (Hexagonal — Port/Adapter)

```
                         ┌─────────────────────────────────────────┐
  HTTP (net/http)        │          internal/user                   │
  ┌───────────────┐      │  ┌──────────────┐     ┌───────────────┐ │
  │ AuthHandler    │─────▶│  │ inbound ports │     │ application    │ │
  │ (adapter http) │ inbound │ IAuthUsecase  │────▶│  usecase.Auth  │ │
  │ requireAuth    │◀─────│  │ IProfileUsecase│     │  usecase.Profile│ │
  └───────────────┘ IAccessTokenVerifier     │     └──────┬────────┘ │
                         │                     │            │ outbound ports
                         │  ┌──────────────────▼────────────▼──────┐ │
                         │  │ IAccountRepository  IOTPStore         │ │
                         │  │ ISessionRepository  IRateLimiter      │ │
                         │  │ ILoginAudit         IMailer           │ │
                         │  │ ITokenIssuer  ISecretGenerator  IPIIProtector│
                         │  └──────┬───────┬───────┬───────┬───────┘ │
                         └─────────┼───────┼───────┼───────┼─────────┘
                                   ▼       ▼       ▼       ▼
                              postgres  valkey  smtp   security
                              (accounts, (otp,   (SMTP  (PII crypto,
                               sessions, rate-   OTP)   token EdDSA,
                               audit)    limit)         secrets)
```

- **Composition root**: `internal/delivery/user_module.go` mem-wire semua adapter ke usecase.
  Tanpa Postgres/Valkey (unit test) hanya `/health` yang terpasang (`noRoutes`).
- **Aturan lapisan** (SDD §4, steering `structure.md`): domain murni (std-lib), usecase
  bergantung pada `port/`, adapter mengimplementasi port. "Accept interfaces, return structs"
  — `NewAuth`/`NewProfile`/`NewPII`/`NewTokens` mengembalikan tipe konkret.
- Interface selalu berawalan `I` dan hanya di `port/` (atau `pkg/`).

### Port inbound
| Port | Metode |
|------|--------|
| `IAuthUsecase` | `RequestOTP`, `VerifyOTP`, `Refresh`, `Logout` |
| `IProfileUsecase` | `GetProfile`, `UpdateProfile` |
| `IAccessTokenVerifier` | `VerifyAccess(token) → accountID` |

### Port outbound
| Port | Peran | Adapter |
|------|-------|---------|
| `IAccountRepository` | CRUD akun | `postgres` |
| `ISessionRepository` | sesi refresh (Create/FindByTokenHash/**Rotate**/Revoke/RevokeAllForAccount) | `postgres` |
| `ILoginAudit` | tulis `login_audit` | `postgres` |
| `IOTPStore` | Start/Attempt/Clear OTP (TTL) | `valkey` |
| `IRateLimiter` | fixed-window `Allow` | `valkey` |
| `IMailer` | `SendOTP` | `smtp` |
| `ITokenIssuer` | `IssueAccess` | `security` |
| `ISecretGenerator` | `OTPCode`, `RefreshToken` | `security` |
| `IPIIProtector` | Hash/Seal email & NIK, HashOTP, HashRefreshToken | `security` |

---

## 2. Model Data

### 2.1 PostgreSQL — schema `users` (`0001_init.up.sql`)

**`accounts`** — akun opsional, PII terenkripsi.
| Kolom | Tipe | Catatan |
|-------|------|---------|
| `id` | uuid PK | `gen_random_uuid()` |
| `email_hash` | bytea UNIQUE | `HMAC-SHA256(lower(trim(email)))` — pencarian |
| `email_enc` | bytea | AES-256-GCM (`nonce‖ciphertext`) |
| `email_masked` | text | mis. `ar***@gmail.com` |
| `full_name` | text | CHECK 1–100 char atau NULL |
| `nik_hash` | bytea | = `buyer_nik_hash` di Booking/Queue (kunci HMAC sama) |
| `nik_enc` | bytea | AES-256-GCM |
| `nik_masked` | text | mis. `3174********0001` |
| `status` | text | `ACTIVE`/`DISABLED` |
| `created_at`/`updated_at`/`last_login_at` | timestamptz | |

- `UNIQUE INDEX uq_accounts_nik ON accounts(nik_hash) WHERE nik_hash IS NOT NULL` — satu NIK
  hanya di satu akun (anti bypass kuota NIK). CHECK menegakkan trio NIK (hash/enc/masked) konsisten.

**`sessions`** — refresh token (hash, rotasi).
| Kolom | Catatan |
|-------|---------|
| `id` uuid PK | |
| `account_id` uuid FK→accounts ON DELETE CASCADE | |
| `refresh_token_hash` bytea UNIQUE | SHA-256 dari token acak 32 byte |
| `user_agent` text, `created_at`, `expires_at`, `revoked_at`, `replaced_by` uuid→sessions | rotasi: `revoked_at` + `replaced_by` |
| `idx_sessions_account_active` WHERE revoked_at IS NULL | lookup sesi aktif |

**`login_audit`** — jejak login (tanpa email plaintext/kode OTP).
- `id bigint IDENTITY PK`, `account_id` (nullable, ON DELETE SET NULL), `email_hash`,
  `outcome` enum (`OTP_SENT`,`OTP_RATE_LIMITED`,`LOGIN_OK`,`OTP_INVALID`,`OTP_EXPIRED`,
  `OTP_LOCKED`,`REFRESH_OK`,`REFRESH_REUSED`,`LOGOUT`), `client_ip inet`, `created_at`.
- `idx_login_audit_email_time ON (email_hash, created_at DESC)`.

> **Delta D11** vs SDD §4.1: kode menambah `email_masked`, `full_name`, `nik_masked`,
> `status`, `last_login_at`, `sessions.replaced_by`; `login_audit` memakai `outcome` enum
> (bukan `action`/`success`/`failure_reason`/`ip_address`/`user_agent`).

### 2.2 Valkey — OTP & rate limit (adapter `valkey`)

| Key | Peran | TTL |
|-----|-------|-----|
| `otp:{emailHashHex}` | challenge OTP (`codeHash`, `attempts`) — `IOTPStore.Start/Attempt/Clear` | `OTP_TTL` |
| cooldown (di dalam `Start`) | resend cooldown atomik; `started=false` bila masih aktif | `OTP_RESEND_COOLDOWN` |
| `otp:email:{emailHashHex}` | fixed-window per email — `IRateLimiter.Allow` | 1 jam |
| `otp:ip:{clientIP}` | fixed-window per IP | 1 jam |

> Kehilangan data Valkey hanya berarti pengguna meminta kode baru (tidak ada kebenaran bisnis
> hilang). Cooldown ditegakkan **di dalam `OTP.Start`**, bukan sebagai pengecekan limiter
> terpisah (berbeda alur dari SDD §5.1 langkah 1–3).

---

## 3. Alur Kunci

### 3.1 Request OTP (`Auth.RequestOTP`)
```
1. Validasi email (valueobject.NewEmail) → 400 VALIDATION_ERROR bila gagal
2. emailHash = PII.HashEmail(email); key = hex(emailHash)
3. Limiter.Allow("otp:email:"+key, MaxPerEmailHour, 1h) → false ⇒ audit OTP_RATE_LIMITED, 429 RATE_LIMITED
4. Limiter.Allow("otp:ip:"+client.IP, MaxPerIPHour, 1h) → false ⇒ 429 RATE_LIMITED
5. code = Secrets.OTPCode() (6 digit, crypto/rand)
6. started = OTP.Start(key, PII.HashOTP(emailHash, code), OTP_TTL, ResendCooldown)
   └ started=false ⇒ 429 OTP_RESEND_TOO_SOON (+ retryAfterSeconds)
7. Mailer.SendOTP(email, code, TTL); gagal ⇒ OTP.Clear(key) + 503 DEPENDENCY_UNAVAILABLE
8. audit OTP_SENT; 202 {expiresInSeconds, resendAfterSeconds}
```

### 3.2 Verify OTP & Login (`Auth.VerifyOTP`)
```
1. Validasi email + code (len 6, numeric)
2. (ch, found) = OTP.Attempt(key)  // increment attempts
   └ !found ⇒ audit OTP_EXPIRED, 401 OTP_EXPIRED
3. ch.Attempts > MaxAttempts ⇒ OTP.Clear, audit OTP_LOCKED, 429 OTP_LOCKED
4. subtle.ConstantTimeCompare(ch.CodeHash, PII.HashOTP(emailHash, code)) != 1
   ⇒ audit OTP_INVALID, 401 OTP_INVALID (+ attemptsLeft)
5. OTP.Clear(key)
6. acc = Accounts.FindByEmailHash → bila tidak ada: SealEmail + Accounts.Create (email_enc/hash/masked)
7. !acc.Active() ⇒ 403 ACCOUNT_DISABLED
8. Accounts.TouchLogin(now)
9. openSession(acc, client, now, rotateFrom="")  // lihat 3.4
10. audit LOGIN_OK; 200 TokenResponse
```

### 3.3 Token issuance (`security.Tokens.IssueAccess`) — **delta D1**
```
exp = now + TOKEN_ACCESS_TTL
jti = Secrets.RefreshToken()[:22]      // 22 char base64url
sign Claims{ iss=TOKEN_ISSUER, sub=accountID, aud=TOKEN_AUDIENCE(string),
             jti, scope="buyer", iat=now, exp }
alg = EdDSA (Ed25519), typ=JWT, kid=TOKEN_KEY_ID
```
Klaim yang **sebenarnya** ditandatangani: `iss, sub, aud, jti, scope, iat, exp`.
**Tidak ada** `email`, `nik_hash`, `has_nik` (SDD §6.1/App C keliru). `aud` satu string
(`rbt`), bukan array. Verifikasi (`VerifyAccess`) mengecek signature/issuer/audience/exp
(leeway 5s) dan mengembalikan `sub`.

### 3.4 Session issuance & rotasi (`Auth.openSession` + `Auth.Refresh`)
```
openSession(acc, client, now, rotateFrom):
  raw = Secrets.RefreshToken()                 // 32 byte, base64url
  next = NewSession{ RefreshTokenHash=HashRefreshToken(raw), UA=trunc(256), ExpiresAt=now+RefreshTTL }
  rotateFrom=="" ? Sessions.Create(next) : Sessions.Rotate(rotateFrom, next, now)
    └ Rotate: cabut old + simpan new atomik; ErrRevoked ⇒ 401 SESSION_INVALID
  access, exp = Tokens.IssueAccess(acc.ID, now)
  return TokenResponse{ Bearer, access, accessExp, raw(refresh), refreshExp, account }

Refresh(req):
  req.RefreshToken=="" ⇒ 401 SESSION_INVALID
  sess = Sessions.FindByTokenHash(HashRefreshToken(req.RefreshToken)); !found ⇒ 401 SESSION_INVALID
  sess.Revoked() ⇒ RevokeAllForAccount(accountID) + audit REFRESH_REUSED + 401 SESSION_INVALID  // deteksi replay
  sess.Expired(now) ⇒ 401 SESSION_INVALID
  acc = Accounts.GetByID; NotFound ⇒ SESSION_INVALID; !Active ⇒ ACCOUNT_DISABLED
  openSession(acc, client, now, rotateFrom=sess.ID); audit REFRESH_OK
```
Refresh token **dikirim di JSON body** `refreshToken`, bukan cookie `rb_refresh` **(delta D3)**.

### 3.5 Logout (`Auth.Logout`)
Idempoten: token kosong/tak dikenal/sudah dicabut ⇒ tetap sukses; selain itu `Sessions.Revoke`
+ audit `LOGOUT`; HTTP `204`.

### 3.6 Profil (`Profile.GetProfile` / `UpdateProfile`)
`GET /api/v1/me` → `ToProfileResponse` (hanya masked). `PATCH /api/v1/me` → validasi
`fullName` (1–100) dan/atau `nik` (16 numerik), seal+hash+mask NIK, `Accounts.UpdateProfile`;
`ErrNIKTaken`→`409 NIK_ALREADY_LINKED`, `ErrNotFound`→`401 UNAUTHORIZED`, tanpa field→`400`.

---

## 4. Transport / Routing (`infrastructure/http` → `Register`)

| Method | Path | Handler | Auth |
|--------|------|---------|------|
| POST | `/api/v1/auth/otp/request` | `requestOTP` | — |
| POST | `/api/v1/auth/otp/verify` | `verifyOTP` | — |
| POST | `/api/v1/auth/refresh` | `refresh` | body refreshToken |
| POST | `/api/v1/auth/logout` | `logout` | body refreshToken |
| GET | `/api/v1/me` | `getMe` | Bearer (`requireAuth`) |
| PATCH | `/api/v1/me` | `patchMe` | Bearer (`requireAuth`) |
| GET | `/health` | health handler | — |

- Rute pakai **garis miring** `otp/request` (bukan `otp-request`) **(delta D2)**, path profil
  `/api/v1/me` + PATCH gabung nama+NIK **(delta D4)**.
- **Tidak ada penanganan cookie** di handler mana pun — refresh token selalu JSON **(delta D3)**.
- Semua response auth/profil menyetel `Cache-Control: no-store`.
- `requireAuth` mengambil `Authorization: Bearer`, `verifier.VerifyAccess`, menaruh accountID di
  context; gagal ⇒ `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer`.
- IP klien dari `X-Forwarded-For` saat `trustProxy` (di belakang ALB; `APP_ENV != local`).

---

## 5. Keamanan & Privasi

- **PII terenkripsi** (`pkg/crypto.Sealer`, AES-256-GCM) untuk `email_enc`, `nik_enc`; **hash
  HMAC** (`crypto.Hasher`) untuk `email_hash`, `nik_hash` (pencarian). Entity domain hanya
  memegang bentuk **masked** — PII plaintext tidak pernah mengalir keluar usecase.
- **`nik_hash` berbagi kunci HMAC** (`PII_HASH_KEY`) dengan Queue/Booking agar cocok lintas service.
- **Kode OTP** hanya sebagai **HMAC hash diikat ke email_hash** di Valkey (`OTP_HASH_KEY`),
  TTL pendek, tidak pernah ke log/broker **(delta D9 vs SDD argon2)**. Perbandingan constant-time.
- **Refresh token**: 32 byte acak base64url, disimpan sebagai **SHA-256**; rotasi tiap refresh;
  pemakaian ulang token tercabut ⇒ cabut **semua sesi akun** (deteksi pencurian).
- **Access token** EdDSA (Ed25519), TTL 15m; public key dibagikan via kid untuk validasi stateless.
- **Anti-enumerasi**: Request OTP selalu `202` identik untuk email ada/tidak.
- **Rate limit**: cooldown resend + fixed-window per email & per IP.
- 3 kunci PII (`PII_ENC_KEY`, `PII_HASH_KEY`, `OTP_HASH_KEY`) **wajib berbeda** (divalidasi config).
- Secret via env/SSM; `.env` tidak di-commit; kredensial compose hanya lokal.

---

## 6. Peta Error (kode → HTTP, `port/inbound/errors.go`)

| Code | HTTP | Kondisi |
|------|------|---------|
| `VALIDATION_ERROR` | 400 | email/code/nik/fullName invalid; tidak ada field update |
| `OTP_RESEND_TOO_SOON` | 429 | cooldown resend aktif (+`retryAfterSeconds`) |
| `RATE_LIMITED` | 429 | limit per-email/per-IP per jam |
| `OTP_INVALID` | **401** | kode salah (+`attemptsLeft`) — *delta D5 (SDD 400)* |
| `OTP_EXPIRED` | **401** | OTP tidak ada/kedaluwarsa — *delta D5 (SDD 400)* |
| `OTP_LOCKED` | **429** | percobaan > MaxAttempts — *delta D5 (SDD 403)* |
| `SESSION_INVALID` | 401 | refresh token kosong/tak ada/dicabut/kedaluwarsa |
| `ACCOUNT_DISABLED` | 403 | akun `DISABLED` — *tidak di SDD (D12)* |
| `UNAUTHORIZED` | 401 | Bearer access token invalid/kedaluwarsa |
| `NIK_ALREADY_LINKED` | 409 | NIK sudah tertaut akun lain |
| `DEPENDENCY_UNAVAILABLE` | 503 | SMTP gagal kirim OTP — *tidak di SDD (D12)* |

Pemetaan ke envelope lewat `pkg/httpx.WriteError` + `pkg/apperror`.

---

## 7. Konfigurasi (`internal/config`, `.env.example`) — **delta D8**

| Grup | Variabel | Validasi |
|------|----------|----------|
| App | `APP_ENV`, `APP_NAME`, `APP_PORT=8085`, `LOG_LEVEL`, `LOG_FORMAT`, `HTTP_SHUTDOWN_TIMEOUT`, `HEALTH_TIMEOUT` | |
| Postgres | `POSTGRES_HOST/PORT/USER/PASSWORD/DATABASE/SSLMODE/SCHEMA/MAX_CONNS` | schema `users` |
| Valkey | `VALKEY_SERVER` (JSON array), `VALKEY_USERNAME/PASSWORD/TLS/DB/PREFIX_ENV` | |
| Token | `TOKEN_ISSUER`, `TOKEN_AUDIENCE`, `TOKEN_KEY_ID`, `TOKEN_PRIVATE_KEY_FILE` (PEM PKCS#8 Ed25519), `TOKEN_ACCESS_TTL` (1m–1h), `TOKEN_REFRESH_TTL` (1h–90d) | rentang ketat |
| OTP | `OTP_TTL` (1m–15m), `OTP_RESEND_COOLDOWN` (≥10s, <TTL), `OTP_MAX_ATTEMPTS` (3–10), `OTP_MAX_PER_EMAIL_HOUR` (≥1), `OTP_MAX_PER_IP_HOUR` (≥per-email) | — |
| PII | `PII_ENC_KEY`, `PII_HASH_KEY`, `OTP_HASH_KEY` (hex 32 byte, **harus berbeda**) | `crypto.ParseKey` |
| SMTP | `SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TIMEOUT` | host/port/from/timeout valid |

Catatan: nama var berbeda dari SDD §8 (SDD pakai `DATABASE_URL`, `USER_TOKEN_*`,
`ACCESS_TOKEN_TTL`, path untuk PII, `OTP_LENGTH`/`OTP_COOLDOWN`/`OTP_HOURLY_LIMIT_*`). Kunci
PII adalah **hex inline**, bukan path file. Panjang OTP hardcoded 6 digit (tak ada `OTP_LENGTH`).

---

## 8. Observability

- **Health**: satu `GET /health` (200 UP / 503 DOWN) untuk ALB & HEALTHCHECK container; checker
  memverifikasi Postgres & Valkey (`infrastructure/healthcheck`).
- **Audit** `login_audit` sebagai jejak keamanan: `OTP_SENT`, `OTP_RATE_LIMITED`, `LOGIN_OK`,
  `OTP_INVALID`, `OTP_EXPIRED`, `OTP_LOCKED`, `REFRESH_OK`, `REFRESH_REUSED`, `LOGOUT` — basis
  alert brute-force / fail-rate (SDD §9.2) via query atas tabel ini.
- **Log**: `slog` terstruktur; PII & kode OTP tidak pernah di-log (hanya `error`/`outcome`).
- Metrik SDD §9.1 (`rbt_user_*`) belum terpasang di kode saat ini — target observability, bukan
  perilaku terverifikasi.

---

## 9. Strategi Testing

| Level | Cakupan | Lokasi |
|-------|---------|--------|
| Unit (testify, table-driven, mock `_mock/`) | usecase `Auth`/`Profile` (semua cabang error), `security` (PII/token/secrets), `mapper`, `config` validasi, handler auth/health, valueobject | `*_test.go` di tiap paket |
| Integration (build tag `integration`) | adapter `postgres` (accounts/sessions/audit, rotasi atomik, unik NIK) & `valkey` (OTP store, rate limiter) terhadap infra nyata | `*_integration_test.go` |
| Coverage | ≥80% logika (`make cover`); `cmd/`, `delivery/`, `config/`, `_mock/` dikecualikan | CI `case-coverage` |

Skenario kunci: wrong-OTP 5x→`OTP_LOCKED`; resend <cooldown→`OTP_RESEND_TOO_SOON`; replay
refresh→semua sesi tercabut; verify double-send idempoten; cek kolom DB terenkripsi/ter-hash;
cari OTP di log→tidak ditemukan.

> **Catatan verifikasi TC**: TC-USR-09/09a (join pakai nikHash dari token) **tidak dapat lulus
> apa adanya** karena token tak memuat `nik_hash`/`has_nik` **(delta D1)** — perlu penyelarasan
> SDD/Queue sebelum TC dieksekusi.

---

## 10. Build & Verify (ringkas)

- **Build/run**: `go run ./cmd migrate up` (migrasi tertanam `embed`), `go run ./cmd api`
  (port 8085), verb Cobra `api`/`migrate`/`healthcheck`.
- **Verify**: `make build` (+ isolation `GOWORK=off`), `make test` (`-race`),
  `make test-integration`, `make lint` (golangci-lint + depguard lapisan), `make cover` (≥80%).
- Hanya satu image per service, satu verb per container (`user api`, `user migrate up`).
