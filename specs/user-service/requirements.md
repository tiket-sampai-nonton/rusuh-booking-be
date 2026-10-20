# Requirements — User Service

| Field | Value |
|-------|-------|
| **Service** | User Service |
| **Spec dari** | SDD-USER-001 v1.2 + implementasi `services/user` |
| **Port** | 8085 |
| **Schema** | `users` (role `svc_user`) |
| **Owner** | BE-1 |
| **SRS** | §3.2.27 (SRS-F-27-xx) |
| **Test Plan** | TC-USR-xx (`docs/test/06-TP-RBT-001.md`) |

## Pendahuluan

User Service mengelola **akun opsional** dengan login OTP email tanpa password. Login
tidak pernah menjadi syarat membeli tiket — pembeli boleh tetap guest. Dokumen ini adalah
spec Kiro yang **digroundkan ke kode nyata** di `services/user`, bukan hanya ke SDD. Di mana
SDD dan kode berbeda, kebenaran mengikuti kode dan perbedaannya dicatat di bagian
[Deltas](#deltas-sdd-vs-kode).

Prinsip (PRD/HLD): login opsional, OTP email (tanpa password), *privacy by design*
(email & NIK dienkripsi AES-256-GCM + hash HMAC untuk pencarian; refresh token di-hash;
kode OTP hanya tersimpan sebagai hash di Valkey dengan TTL).
OTP email dikirim via Notification Service (event `otp.requested` dengan enkripsi AES-256-GCM).

Arsitektur: hexagonal (port/adapter). Usecase `Auth` dan `Profile` bergantung pada port
`outbound` (`IAccountRepository`, `ISessionRepository`, `ILoginAudit`, `IOTPStore`,
`IRateLimiter`, `IOTPPublisher`, `ITokenIssuer`, `ISecretGenerator`, `IPIIProtector`), adapter
HTTP memanggil port `inbound` (`IAuthUsecase`, `IProfileUsecase`, `IAccessTokenVerifier`).

---

## Requirement 1 — Request OTP

**User story:** Sebagai pembeli, saya ingin meminta kode OTP ke email saya, sehingga saya
bisa login tanpa password.

SRS-F-27-02 · TC-USR-02, TC-USR-04

### Acceptance Criteria (EARS)

1. WHEN klien `POST /api/v1/auth/otp/request` dengan email valid, THE SYSTEM SHALL
   membuat kode 6 digit acak kriptografis, menyimpan **hash**-nya di Valkey dengan TTL
   `OTP_TTL`, dan membalas `202 Accepted` berisi `expiresInSeconds` dan `resendAfterSeconds`.
2. WHEN email terdaftar maupun belum terdaftar, THE SYSTEM SHALL membalas respons `202`
   yang identik (anti-enumerasi akun). *(TC-USR-02)*
3. IF cooldown resend untuk email masih aktif (dicek atomik saat menyimpan OTP), THEN THE
   SYSTEM SHALL menolak dengan `429 OTP_RESEND_TOO_SOON` dan menyertakan
   `retryAfterSeconds`. *(TC-USR-04)*
4. IF permintaan per-email melebihi `OTP_MAX_PER_EMAIL_HOUR` dalam 1 jam, THE SYSTEM SHALL
   menolak dengan `429 RATE_LIMITED` dan mencatat audit `OTP_RATE_LIMITED`.
5. IF permintaan per-IP melebihi `OTP_MAX_PER_IP_HOUR` dalam 1 jam, THE SYSTEM SHALL
   menolak dengan `429 RATE_LIMITED`.
6. WHEN email gagal divalidasi (format/panjang), THE SYSTEM SHALL menolak dengan
   `400 VALIDATION_ERROR` dan menyebut field `email`.
7. IF publish event `otp.requested` gagal, THE SYSTEM SHALL menghapus OTP yang baru disimpan dan
   membalas `503 DEPENDENCY_UNAVAILABLE` (`ErrPublishFailed`).
8. THE SYSTEM SHALL mencatat audit `OTP_SENT` saat berhasil. Kode OTP dan email dienkripsi
   (AES-256-GCM) saat dipublish ke broker dan tidak pernah muncul plaintext di log. *(TC-USR-11)*

---

## Requirement 2 — Verify OTP & Login

**User story:** Sebagai pembeli, saya ingin memverifikasi kode OTP untuk masuk, sehingga
akun saya dibuat otomatis pada login pertama dan saya menerima token.

SRS-F-27-03..05 · TC-USR-03, TC-USR-05

### Acceptance Criteria (EARS)

1. WHEN klien `POST /api/v1/auth/otp/verify` dengan email dan kode 6 digit yang benar, THE
   SYSTEM SHALL menghapus OTP, menemukan-atau-membuat akun berdasarkan `email_hash`,
   membuka sesi (refresh token), menerbitkan access token, dan membalas `200` berisi
   `tokenType`, `accessToken`, `accessTokenExpiresAt`, `refreshToken`,
   `refreshTokenExpiresAt`, dan objek `account`.
2. WHEN akun belum ada, THE SYSTEM SHALL membuat akun baru (email terenkripsi + hash +
   masked) dan mencatat audit `LOGIN_OK`.
3. IF OTP tidak ada di Valkey (kedaluwarsa atau belum diminta), THEN THE SYSTEM SHALL
   menolak dengan `OTP_EXPIRED` dan mencatat audit `OTP_EXPIRED`.
4. IF jumlah percobaan melebihi `OTP_MAX_ATTEMPTS`, THEN THE SYSTEM SHALL menghapus OTP,
   menolak dengan `OTP_LOCKED`, dan mencatat audit `OTP_LOCKED`. *(TC-USR-03)*
5. IF kode salah (dibanding dengan `subtle.ConstantTimeCompare`), THEN THE SYSTEM SHALL
   menolak dengan `OTP_INVALID` menyertakan `attemptsLeft`, dan mencatat audit `OTP_INVALID`.
6. IF akun berstatus `DISABLED`, THEN THE SYSTEM SHALL menolak dengan `403 ACCOUNT_DISABLED`.
7. WHEN login sukses, THE SYSTEM SHALL memperbarui `last_login_at` akun.
8. THE SYSTEM SHALL membandingkan kode via hash OTP yang diikat ke `email_hash`, sehingga
   kode untuk satu email tidak berlaku untuk email lain.

---

## Requirement 3 — Refresh Token (rotasi + deteksi replay)

**User story:** Sebagai pembeli yang sudah login, saya ingin memperpanjang sesi dengan
refresh token, sehingga saya tidak perlu login ulang tiap 15 menit.

SRS-F-27-06 · TC-USR-06

### Acceptance Criteria (EARS)

1. WHEN klien `POST /api/v1/auth/refresh` dengan `refreshToken` yang valid dan belum
   dicabut/kedaluwarsa, THE SYSTEM SHALL merotasi sesi (membuat sesi baru, mencabut yang
   lama secara atomik), menerbitkan access token baru, dan membalas `200` dengan pasangan
   token baru.
2. IF `refreshToken` kosong atau tidak ditemukan, THEN THE SYSTEM SHALL menolak dengan
   `SESSION_INVALID`.
3. IF refresh token yang **sudah dicabut** dipakai ulang, THEN THE SYSTEM SHALL mencabut
   **seluruh sesi akun** (deteksi pencurian), mencatat audit `REFRESH_REUSED`, dan menolak
   dengan `SESSION_INVALID`. *(TC-USR-06)*
4. IF sesi sudah kedaluwarsa, THEN THE SYSTEM SHALL menolak dengan `SESSION_INVALID`.
5. IF akun pada sesi tidak ditemukan, THEN THE SYSTEM SHALL menolak dengan `SESSION_INVALID`;
   IF akun `DISABLED`, THEN `ACCOUNT_DISABLED`.
6. WHEN rotasi berhasil, THE SYSTEM SHALL mencatat audit `REFRESH_OK`.

---

## Requirement 4 — Logout

**User story:** Sebagai pembeli, saya ingin logout agar refresh token saya dicabut.

SRS-F-27-07 · TC-USR-07

### Acceptance Criteria (EARS)

1. WHEN klien `POST /api/v1/auth/logout` dengan `refreshToken`, THE SYSTEM SHALL mencabut
   sesi terkait dan membalas `204 No Content`.
2. IF `refreshToken` kosong, tidak dikenal, atau sudah dicabut, THEN THE SYSTEM SHALL tetap
   membalas sukses (operasi idempoten).
3. WHEN sesi dicabut melalui logout, THE SYSTEM SHALL mencatat audit `LOGOUT`.

---

## Requirement 5 — Lihat Profil

**User story:** Sebagai pembeli yang sudah login, saya ingin melihat profil saya.

SRS-F-27-08 · TC-USR-08

### Acceptance Criteria (EARS)

1. WHEN klien `GET /api/v1/me` dengan Bearer access token valid, THE SYSTEM SHALL membalas
   `200` berisi `accountId`, `emailMasked`, `fullName` (opsional), `nikMasked` (opsional),
   dan `hasNik`.
2. THE SYSTEM SHALL mengirim email dan NIK **hanya dalam bentuk tersamar** (masked), tidak
   pernah plaintext. *(TC-USR-12)*
3. IF Bearer token tidak ada/invalid/kedaluwarsa, THEN THE SYSTEM SHALL menolak dengan
   `401 UNAUTHORIZED` dan header `WWW-Authenticate: Bearer`.
4. IF akun tidak ditemukan, THEN THE SYSTEM SHALL memetakan ke `401 UNAUTHORIZED`.

---

## Requirement 6 — Update Profil (nama & link NIK)

**User story:** Sebagai pembeli yang sudah login, saya ingin menautkan NIK dan mengisi nama
agar tersimpan di akun.

SRS-F-27-08..09 · TC-USR-08

### Acceptance Criteria (EARS)

1. WHEN klien `PATCH /api/v1/me` dengan Bearer token valid dan field `fullName` dan/atau
   `nik`, THE SYSTEM SHALL memperbarui hanya field yang dikirim dan membalas `200` dengan
   profil terbaru.
2. IF `fullName` di luar 1–100 karakter, THEN THE SYSTEM SHALL menolak dengan
   `400 VALIDATION_ERROR` field `fullName`.
3. IF `nik` tidak 16 digit numerik, THEN THE SYSTEM SHALL menolak dengan
   `400 VALIDATION_ERROR` field `nik`.
4. WHEN NIK valid dikirim, THE SYSTEM SHALL menyimpan NIK terenkripsi (`nik_enc`) + hash
   (`nik_hash`, kunci HMAC sama dengan Queue/Booking) + masked (`nik_masked`).
5. IF NIK sudah tertaut ke akun lain (unik per NIK), THEN THE SYSTEM SHALL menolak dengan
   `409 NIK_ALREADY_LINKED`.
6. IF tidak ada field yang dikirim, THEN THE SYSTEM SHALL menolak dengan
   `400 VALIDATION_ERROR` ("tidak ada field yang diubah").

---

## Requirement 7 — Token untuk service lain (stateless validation)

**User story:** Sebagai Queue/Booking Service, saya ingin memvalidasi access token tanpa
memanggil User Service, sehingga validasi cepat dan stateless.

SRS-F-27-10 · TC-USR-09, TC-USR-09a

### Acceptance Criteria (EARS)

1. THE SYSTEM SHALL menerbitkan access token JWT EdDSA (Ed25519) dengan klaim `iss`, `sub`
   (account UUID), `aud`, `jti`, `scope="buyer"`, `iat`, `exp`; TTL `TOKEN_ACCESS_TTL`.
2. THE SYSTEM SHALL membagikan public key (via kid) ke Queue dan Booking agar mereka
   memverifikasi token yang sama tanpa HTTP call ke User Service.
3. WHEN memverifikasi token, THE SYSTEM SHALL mengecek tanda tangan, issuer, audience, dan
   `exp` (dengan leeway 5 detik) lalu mengembalikan `sub` sebagai accountID.
4. **Catatan (delta):** token **tidak** memuat klaim `email`/`nik_hash`/`has_nik` seperti
   SDD §6.1. Lihat [Deltas](#deltas-sdd-vs-kode) D1 — implikasi untuk TC-USR-09/09a.

---

## Requirement 8 — Health & Event posture

**User story:** Sebagai platform (ALB/container), saya ingin probe kesehatan service.

SRS-F-27-xx · (D-23)

### Acceptance Criteria (EARS)

1. WHEN klien `GET /health`, THE SYSTEM SHALL mengembalikan `200 UP` saat Postgres & Valkey
   sehat, atau `503 DOWN` bila ada dependency bermasalah.
2. THE SYSTEM SHALL **tidak** mempublikasikan maupun mengonsumsi event broker apa pun (User
   Service stateless dari perspektif event).
3. WHEN dependency (Postgres/Valkey) belum ter-wire (mis. unit test), THE SYSTEM SHALL hanya
   memasang route `/health`.

---

## Requirement 9 — Build & Verify

**User story:** Sebagai pengembang, saya ingin membangun dan memverifikasi service secara
deterministik.

### Acceptance Criteria (EARS) — Build (run)

1. WHEN `go run ./cmd migrate up` dijalankan, THE SYSTEM SHALL menerapkan migrasi
   `0001_init` (schema `users`: `accounts`, `sessions`, `login_audit`).
2. WHEN `go run ./cmd api` dijalankan dengan konfigurasi valid, THE SYSTEM SHALL start di
   port 8085 dan melayani `/health` + route `/api/v1/*`.
3. IF konfigurasi tidak valid (TOKEN_*, OTP_*, PII_*, RABBITMQ_* di luar rentang), THEN THE
   SYSTEM SHALL gagal start dengan error validasi yang jelas.

### Acceptance Criteria (EARS) — Verify

4. WHEN `make build` dijalankan, THE SYSTEM SHALL meng-compile modul `services/user` tanpa
   error (termasuk isolation build `GOWORK=off`).
5. WHEN `make test` dijalankan, THE SYSTEM SHALL menjalankan unit test `-race` (usecase,
   security, mapper, config, handler) dan lulus.
6. WHEN `make test-integration` dijalankan, THE SYSTEM SHALL menjalankan integration test
   adapter (build tag `integration`) terhadap PostgreSQL & Valkey nyata dan lulus.
7. WHEN `make lint` dijalankan, THE SYSTEM SHALL lulus golangci-lint termasuk depguard
   (lapisan hexagonal dihormati).
8. THE SYSTEM SHALL menjaga coverage logika ≥ 80% (`make cover`), dengan `cmd/`,
   `internal/delivery/`, `internal/config/`, `_mock/` dikecualikan.

---

## Deltas (SDD vs Kode)

Setiap delta di bawah **diverifikasi dengan membaca kode**, bukan ditebak. Format:
*SDD menyatakan → Kode sebenarnya → sumber*.

| ID | SDD menyatakan | Kode sebenarnya | Sumber kode |
|----|----------------|-----------------|-------------|
| **D1** | Access token membawa klaim `email`, `nik_hash`, `has_nik` + `aud` array `["queue-service","booking-service"]` (§6.1, App C) | `pkg/token.Claims` **sudah punya** field `Email`, `NIKHash`, `HasNIK`. **NAMUN** `Tokens.IssueAccess` hanya menandatangani `iss, sub, aud(string), jti, scope="buyer", iat, exp` — **tidak mempopulasi** klaim identitas. `aud` adalah satu string (`TOKEN_AUDIENCE=rbt`). **Status: struct ready, issuance not wired.** | `pkg/token/token.go` (`Claims`); `infrastructure/security/security.go` (`IssueAccess`) |
| **D2** | Rute OTP `POST /api/v1/auth/otp-request` dan `otp-verify` (tanda hubung) | Rute `POST /api/v1/auth/otp/request` dan `otp/verify` (garis miring) | `infrastructure/http/auth_handler.go` (`Register`) |
| **D3** | Refresh token lewat **cookie HttpOnly `rb_refresh`**; verify "Set cookie", refresh "Set new cookie", logout "Clear cookie" | **Tidak ada cookie sama sekali.** Refresh token dikirim/diterima sebagai JSON body `refreshToken` (request) dan `refreshToken`/`refreshTokenExpiresAt` (response). | `auth_handler.go`; `domain/dto/auth.go` (`RefreshRequest`, `TokenResponse`) |
| **D4** | Profil: `GET /api/v1/users/me`, link NIK via `POST /api/v1/users/me/nik` | `GET /api/v1/me` dan `PATCH /api/v1/me` (nama + NIK digabung dalam satu PATCH) | `auth_handler.go` (`Register`); `profile.go` (`UpdateProfile`) |
| **D5** | `OTP_INVALID`=400, `OTP_EXPIRED`=400, `OTP_LOCKED`=403 (App B) | `OTP_INVALID`=**401**, `OTP_EXPIRED`=**401**, `OTP_LOCKED`=**429** | `port/inbound/errors.go` |
| **D6** | Verify response: `accountId`, `isNewAccount`, `accessToken`, `expiresAt` + cookie | Response: `tokenType`, `accessToken`, `accessTokenExpiresAt`, `refreshToken`, `refreshTokenExpiresAt`, `account{...}`. **Tidak ada** `isNewAccount`. | `domain/dto/auth.go` (`TokenResponse`) |
| **D7** | Profil response: `email` (plaintext), `nikLinked`, `createdAt` | Hanya `emailMasked`, `fullName`, `nikMasked`, `hasNik`, `accountId` — email **tidak pernah** plaintext | `dto/auth.go` (`ProfileResponse`); `mapper/account_mapper.go` |
| **D8** | Env: `DATABASE_URL`, `VALKEY_URL`, `USER_TOKEN_PRIVATE_KEY_PATH`/`_PUBLIC_KEY_PATH`, `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL`, `PII_ENCRYPTION_KEY_PATH`, `OTP_LENGTH`, `OTP_COOLDOWN`, `OTP_HOURLY_LIMIT_*` | `POSTGRES_*`(+`POSTGRES_SCHEMA`), `VALKEY_SERVER`(JSON), `TOKEN_ISSUER/AUDIENCE/KEY_ID/PRIVATE_KEY_FILE/ACCESS_TTL/REFRESH_TTL`, `PII_ENC_KEY`/`PII_HASH_KEY`/`OTP_HASH_KEY` (hex, bukan path), `OTP_TTL/RESEND_COOLDOWN/MAX_ATTEMPTS/MAX_PER_EMAIL_HOUR/MAX_PER_IP_HOUR`. Tidak ada `OTP_LENGTH` (hardcoded 6 digit). | `.env.example`; `internal/config/user.go` |
| **D9** | OTP hashing dengan `argon2.IDKey` + salt (App A.3) | OTP di-hash dengan **HMAC** (`crypto.Hasher`) diikat ke `email_hash` via `OTP_HASH_KEY`, bukan argon2 | `security.go` (`HashOTP`); `config/user.go` (`OTPKeyHex`) |
| **D10** | Refresh token hash = `SHA256(token)` (App C.2) | Benar: `HashRefreshToken` = `SHA-256` dari token acak 32 byte base64url. (Tidak ada delta — dikonfirmasi) | `security.go` (`HashRefreshToken`) |
| **D11** | Tabel `accounts` punya `email_enc`, `email_hash`, `nik_enc`, `nik_hash`, `created_at`, `updated_at` | Kode/migrasi menambahkan `email_masked`, `full_name` (CHECK 1–100), `nik_masked`, `status` (`ACTIVE`/`DISABLED`), `last_login_at`; `sessions` menambahkan `replaced_by`; `login_audit` memakai `outcome` enum + `id bigint IDENTITY` (SDD menamai kolom `action`,`success`,`failure_reason`,`ip_address`,`user_agent`) | `deployments/migrations/0001_init.up.sql` |
| **D12** | Error table tidak memuat `ACCOUNT_DISABLED` (403) dan `DEPENDENCY_UNAVAILABLE` (503) untuk publish | Keduanya ada di kode (`ErrAccountDisabled`, `ErrPublishFailed`) | `port/inbound/errors.go`; `auth.go` |
| **D13** | Order history: tidak ada di User Service (via Booking Service) | Dikonfirmasi — tidak ada endpoint order history di kode. (Sesuai SDD §3.7) | `auth_handler.go` (`Register`) |

### Implikasi Delta terhadap Test Plan

- **D1** mempengaruhi **TC-USR-09 / TC-USR-09a**: skenario SDD "nikHash diambil dari token"
  tidak dapat dipenuhi karena token tidak memuat `nik_hash`/`has_nik`. Queue/Booking harus
  memakai `sub` (accountID) + melakukan lookup/hashing `buyerNik` dari body, atau SDD perlu
  direvisi. Ini keputusan desain yang harus diselaraskan sebelum TC-USR-09 dieksekusi.
- **D5** mempengaruhi assertion status code pada TC-USR-03/05 (harap 401/429, bukan 400/403).
- **D3/D4/D6** mempengaruhi TC-USR-06/07/08 (klien memakai JSON body, bukan cookie; rute `/me`).


---

## Implementation Priority

### P0 — Token Claims (D1) — BLOCKING TC-USR-09/09a

**Decision:** Option A — implement the SDD design. Add `email`, `nik_hash`, `has_nik` to the access token so Queue/Booking can validate NIK offline without calling User Service.

**Why:** PRD v5.4 (US-092), SRS v1.3 (SRS-F-27-10), HLD v1.6, SDD-USER §6.1, SDD-QUEUE §3.1 all specify this design. The docs are aligned; only the code needs to catch up.

**Acceptance Criteria:**

1. `pkg/token.Claims` struct has fields `Email string`, `NIKHash string` (hex-encoded), `HasNIK bool` with appropriate JSON tags.
2. `ITokenIssuer.IssueAccess` signature accepts `(accountID, email string, nikHash []byte, hasNIK bool, now time.Time)`.
3. `IAccountRepository` has method `GetNIKHashByID(ctx, id) ([]byte, error)` to fetch `nik_hash` for token issuance.
4. `security.Tokens.IssueAccess` populates the new claims from the passed parameters.
5. `usecase.Auth.openSession` fetches email (from verify flow) and nikHash (from repository) and passes them to `IssueAccess`.
6. Unit tests verify: (a) token with NIK carries all three claims; (b) token without NIK has `HasNIK=false` and empty `NIKHash`; (c) email is always present.
7. `make build && make test && make lint` pass.
8. Mocks regenerated (`make mock`).

**Downstream impact:** After this is implemented, Queue and Booking can:
- Extract `has_nik` from token
- If `true`: use `nik_hash` directly (ignore `body.buyerNik`)
- If `false`: require `body.buyerNik`, HMAC it with the shared key

**Test unblock:** TC-USR-09, TC-USR-09a become runnable.

---

## P0 Implementation — Token Claims Enhancement ✅ COMPLETE

### Problem Statement

The current access token implementation only includes basic claims (`iss`, `sub`, `aud`, `jti`, `scope`, `iat`, `exp`). Per SDD-USER §6.1, SDD-QUEUE §3.1, and SRS-F-27-10, the token MUST include `email`, `nik_hash`, and `has_nik` so Queue and Booking can validate buyer identity offline without calling User Service.

### Status: COMPLETE (2026-10-20)

| Item | Status | Notes |
|------|--------|-------|
| `pkg/token.Claims` struct | ✅ | Has `Email`, `NIKHash` (`omitempty`), `HasNIK` |
| `IAccountRepository.GetNIKHashByID` | ✅ | `(ctx, id string) ([]byte, error)` |
| `IAccountRepository.GetEmailEncByID` | ✅ | Returns encrypted email bytes |
| `IPIIProtector.UnsealEmail` | ✅ | Decrypts email_enc |
| `ITokenIssuer.IssueAccess` signature | ✅ | `(accountID, email string, nikHash []byte, now)` |
| `Tokens.IssueAccess` implementation | ✅ | Populates Email, HasNIK, NIKHash (hex) |
| `Auth.openSession` wiring | ✅ | Accepts email, fetches nikHash |
| Email in Refresh flow | ✅ | Fetches via `GetEmailEncByID` + `UnsealEmail` |

### Implementation Summary

**Files modified:**
- `port/outbound/repositories.go` — Added `GetNIKHashByID`, `GetEmailEncByID`
- `port/outbound/services.go` — Updated `IssueAccess` signature; added `UnsealEmail`
- `infrastructure/postgres/repositories.go` — Implemented new methods
- `infrastructure/security/security.go` — Updated `IssueAccess`; added `UnsealEmail`
- `application/usecase/auth.go` — Wired email + nikHash flows
- Tests and mocks updated

**Design choices:**
- Repository returns encrypted email bytes (`GetEmailEncByID`), not plaintext — keeps decryption in security domain
- VerifyOTP uses email from validated request; Refresh fetches via `GetEmailEncByID` + `UnsealEmail`
- `openSession` accepts email parameter, fetches nikHash internally

**Verification:**
```bash
make build  # ✅ all 5 services
make test   # ✅ all tests pass
```

### Downstream Usage (Queue/Booking)

```go
claims, _ := verifier.VerifyAccess(bearerToken)

if claims.HasNIK {
    // Use NIK hash from token
    buyerNikHash, _ := hex.DecodeString(claims.NIKHash)
} else {
    // Require body.buyerNik
    if req.BuyerNik == "" {
        return 422, "NIK_REQUIRED"
    }
    buyerNikHash = pii.HashNIK(req.BuyerNik)
}
```

---

_End of User Service Requirements_

### Migration Impact

**None.** This change only affects token issuance logic — no database migrations required. The `nik_hash` column already exists in `accounts` table.

### Rollout Strategy

1. Deploy User Service with new token claims
2. Queue/Booking can immediately use `has_nik`/`nik_hash` from tokens
3. Old tokens (without claims) will fail validation → users re-login automatically (access TTL is 15m)

### TC Unblock

After implementation:
- **TC-USR-09**: Login user with NIK → token has `has_nik=true`, `nik_hash` populated
- **TC-USR-09a**: Login user without NIK → token has `has_nik=false`, `nik_hash` empty
- **Queue join flow**: Extract `has_nik` from token, use `nik_hash` if true, else require `body.buyerNik`
