# Requirements Document — User Service

| Field | Value |
|-------|-------|
| **Spec** | user-service |
| **Service** | `services/user` |
| **Port** | 8085 |
| **Schema / Role** | `users` / `svc_user` |
| **Owner** | BE-1 |
| **Sumber (urut prioritas)** | **kode `services/user`** (terimplementasi, evidence `docs/evidence/2026-10-19-boilerplate-v1.5.md`) → migrasi `0001_init` → kontrak `docs/contracts/openapi/user.yaml` → SRS §3.2.27 (SRS-F-27-xx), SRS-Q-SEC-10/11 → ADR-020 → `docs/sdd/SDD-USER-001.md` v1.2 |
| **Test Plan** | TC-USR-01..12, TC-MSG-05/06 |

## Introduction

User Service mengelola **akun opsional** dengan login OTP email tanpa password; login tidak pernah
menjadi syarat membeli (pembeli tetap boleh guest). Prinsip *privacy by design*: email & NIK dienkripsi
AES-256-GCM + hash HMAC untuk pencarian; refresh token disimpan sebagai hash; kode OTP hanya sebagai
hash di Valkey dengan TTL.

Spec ini **digroundkan ke kode nyata**. Di mana SDD dan kode berbeda, perilaku kode yang sudah
terverifikasi dicatat di [Deltas](#deltas-sdd-vs-kode); di mana kode belum mengikuti keputusan
arsitektur (ADR-020), requirement ditandai **[target]**.

### Status Implementasi (audit kode 2026-10-21)

| Area | Status | Bukti |
|------|--------|-------|
| OTP request/verify, refresh rotasi + deteksi replay, logout, profil GET/PATCH, audit login, rate limit, PII crypto | ✅ | `application/usecase/{auth,profile}.go`, `infrastructure/{postgres,valkey,security,http}` |
| Klaim identitas di access token (`email`, `nik_hash`, `has_nik`) — dulu "P0 / D1" | ✅ | `infrastructure/security/security.go` `IssueAccess(accountID, email, nikHash, now)`; `pkg/token.Claims` |
| Pengiriman OTP lewat **event `otp.requested`** ke Notification (ADR-020, SDD-USER v1.2) | ❌ **target** — kode masih mengirim langsung via SMTP (`IMailer`, `infrastructure/smtp/mailer.go`) | `delivery/user_module.go`, `usecase/auth.go` |
| Validasi struktur NIK setara `pkg/nik` (SDD-RBT §4.9) | ⚠️ parsial — VO `NewNIK` memeriksa provinsi 11–94, tanggal 1–31/bulan 1–12, urut ≠ 0000; tidak memeriksa kab/kec `00`, tanggal kalender, daftar provinsi | `domain/valueobject/nik.go` |
| Breaker pada panggilan SMTP (SRS-F-19-03) | ❌ | tidak ada `pkg/resilience` di `smtp` adapter (OQ-9) |
| Metrik `rbt_user_*` | ❌ | — |

## Glossary

- **OTP challenge** — `otp:{emailHashHex}` di Valkey: hash kode (HMAC terikat `email_hash`), jumlah percobaan, TTL `OTP_TTL`.
- **Session** — baris `sessions` per refresh token (hash SHA-256); dirotasi setiap refresh (`revoked_at` + `replaced_by`).
- **Replay** — refresh token yang sudah dicabut dipakai lagi → semua sesi akun dicabut.
- **Access token** — JWT EdDSA (`iss`, `sub`, `aud`, `jti`, `scope=buyer`, `iat`, `exp`, `email`, `nik_hash?`, `has_nik?`), TTL `TOKEN_ACCESS_TTL`, diverifikasi Queue/Booking secara offline.

---

## Requirement 1 — Request OTP

**User story:** Sebagai pembeli, saya ingin meminta kode OTP ke email saya, sehingga saya bisa login tanpa password.

SRS-F-27-02, SRS-F-27-04, SRS-F-27-12 · TC-USR-02, TC-USR-04, TC-USR-11, TC-MSG-05

### Acceptance Criteria (EARS)

1. WHEN klien `POST /api/v1/auth/otp/request` dengan email valid, THE SYSTEM SHALL membuat kode 6 digit acak kriptografis, menyimpan **hash**-nya di Valkey dengan TTL `OTP_TTL`, dan membalas `202 Accepted` berisi `expiresInSeconds` dan `resendAfterSeconds`. ✅
2. WHEN email terdaftar maupun belum, THE SYSTEM SHALL membalas `202` identik (anti-enumerasi). ✅ *(TC-USR-02)*
3. IF cooldown resend masih aktif (dicek atomik saat menyimpan OTP), THEN `429 OTP_RESEND_TOO_SOON` + `retryAfterSeconds`. ✅ *(TC-USR-04)*
4. IF permintaan per-email melebihi `OTP_MAX_PER_EMAIL_HOUR`/jam, THEN `429 RATE_LIMITED` + audit `OTP_RATE_LIMITED`. ✅
5. IF permintaan per-IP melebihi `OTP_MAX_PER_IP_HOUR`/jam, THEN `429 RATE_LIMITED`. ✅
6. IF email tidak valid, THEN `400 VALIDATION_ERROR` field `email`. ✅
7. WHEN kode dibuat, THE SYSTEM SHALL mempublikasikan event `otp.requested.v1 { emailEnc, codeEnc, expiresAt, locale? }` (email & kode AES-256-GCM dengan `PII_ENC_KEY`) ke `rusuh.events` dengan publisher confirm; IF publish gagal, THEN OTP dihapus dan `503 DEPENDENCY_UNAVAILABLE`. **[target — ADR-020; schema belum ada: OQ-4]** *Perilaku saat ini [kode]: kirim SMTP langsung; gagal → OTP dihapus + `503 DEPENDENCY_UNAVAILABLE` (`ErrMailUnavailable`).*
8. THE SYSTEM SHALL mencatat audit `OTP_SENT` saat berhasil; kode OTP dan email plaintext tidak pernah muncul di log, payload broker, atau audit. ✅ untuk log/audit; payload broker **[target]** *(TC-USR-11)*

## Requirement 2 — Verify OTP & Login

**User story:** Sebagai pembeli, saya ingin memverifikasi kode OTP untuk masuk, sehingga akun dibuat otomatis pada login pertama dan saya menerima token.

SRS-F-27-03, SRS-F-27-05 · TC-USR-03, TC-USR-05

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/auth/otp/verify` dengan email dan kode benar, THE SYSTEM SHALL menghapus OTP, menemukan-atau-membuat akun berdasarkan `email_hash`, membuka sesi, menerbitkan access token, dan membalas `200 { tokenType, accessToken, accessTokenExpiresAt, refreshToken, refreshTokenExpiresAt, account }`. ✅
2. WHEN akun belum ada, THE SYSTEM SHALL membuat akun (email terenkripsi + hash + masked) dan mencatat audit `LOGIN_OK`. ✅
3. IF OTP tidak ada (kedaluwarsa/belum diminta), THEN `401 OTP_EXPIRED` + audit `OTP_EXPIRED`. ✅
4. IF percobaan melebihi `OTP_MAX_ATTEMPTS`, THEN OTP dihapus, `429 OTP_LOCKED` + audit `OTP_LOCKED`. ✅ *(TC-USR-03)*
5. IF kode salah (`subtle.ConstantTimeCompare`), THEN `401 OTP_INVALID` + `attemptsLeft` + audit `OTP_INVALID`. ✅
6. IF akun `DISABLED`, THEN `403 ACCOUNT_DISABLED`. ✅
7. WHEN login sukses, THE SYSTEM SHALL memperbarui `last_login_at`. ✅
8. THE kode SHALL terikat ke `email_hash` sehingga kode untuk satu email tidak berlaku untuk email lain. ✅

## Requirement 3 — Refresh Token (rotasi + deteksi replay)

**User story:** Sebagai pembeli yang sudah login, saya ingin memperpanjang sesi dengan refresh token, sehingga tidak perlu login ulang tiap 15 menit.

SRS-F-27-06 · TC-USR-06

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/auth/refresh` dengan `refreshToken` valid, THE SYSTEM SHALL merotasi sesi secara atomik, menerbitkan access token baru (dengan klaim identitas terkini), dan membalas `200` pasangan token baru. ✅
2. IF `refreshToken` kosong atau tidak ditemukan, THEN `401 SESSION_INVALID`. ✅
3. IF refresh token yang sudah dicabut dipakai ulang, THEN THE SYSTEM SHALL mencabut **seluruh sesi akun**, audit `REFRESH_REUSED`, dan `401 SESSION_INVALID`. ✅ *(TC-USR-06)*
4. IF sesi kedaluwarsa, THEN `401 SESSION_INVALID`. ✅
5. IF akun tidak ditemukan, THEN `401 SESSION_INVALID`; IF akun `DISABLED`, THEN `403 ACCOUNT_DISABLED`. ✅
6. WHEN rotasi berhasil, THE SYSTEM SHALL mencatat audit `REFRESH_OK`. ✅

## Requirement 4 — Logout

**User story:** Sebagai pembeli, saya ingin logout agar refresh token saya dicabut.

SRS-F-27-07 · TC-USR-07

### Acceptance Criteria (EARS)

1. WHEN `POST /api/v1/auth/logout` dengan `refreshToken`, THE SYSTEM SHALL mencabut sesi dan membalas `204`. ✅
2. IF `refreshToken` kosong/tidak dikenal/sudah dicabut, THEN THE SYSTEM SHALL tetap sukses (idempoten). ✅
3. WHEN sesi dicabut, THE SYSTEM SHALL mencatat audit `LOGOUT`. ✅

## Requirement 5 — Lihat Profil

**User story:** Sebagai pembeli yang sudah login, saya ingin melihat profil saya.

SRS-F-27-09 · TC-USR-08, TC-USR-12

### Acceptance Criteria (EARS)

1. WHEN `GET /api/v1/me` dengan Bearer access token valid, THE SYSTEM SHALL membalas `200 { accountId, emailMasked, fullName?, nikMasked?, hasNik }`. ✅
2. THE SYSTEM SHALL mengirim email dan NIK **hanya** tersamar. ✅ *(TC-USR-12)*
3. IF Bearer token tidak ada/invalid/kedaluwarsa, THEN `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer`. ✅
4. IF akun tidak ditemukan, THEN `401 UNAUTHORIZED`. ✅

## Requirement 6 — Update Profil (nama & NIK)

**User story:** Sebagai pembeli yang sudah login, saya ingin menyimpan nama dan satu NIK pembeli di akun.

SRS-F-27-08, SRS-F-27-09, SRS-F-11-01 · TC-USR-08

### Acceptance Criteria (EARS)

1. WHEN `PATCH /api/v1/me` dengan Bearer token dan `fullName` dan/atau `nik`, THE SYSTEM SHALL memperbarui hanya field yang dikirim dan membalas `200` profil terbaru. ✅
2. IF `fullName` di luar 1–100 karakter, THEN `400 VALIDATION_ERROR` field `fullName`. ✅
3. IF `nik` gagal validasi struktur, THEN `400 VALIDATION_ERROR` field `nik`. ✅ *(VO saat ini lebih longgar dari `pkg/nik` — target: aturan sama dengan Queue/Booking, platform R1. Kode error `400` di sini vs `422 NIK_INVALID` di Queue/Booking mengikuti `user.yaml`.)*
4. WHEN NIK valid, THE SYSTEM SHALL menyimpan `nik_enc` (AES-256-GCM), `nik_hash` (HMAC, kunci sama dengan Queue/Booking), `nik_masked`. ✅
5. IF NIK sudah tertaut ke akun lain, THEN `409 NIK_ALREADY_LINKED`. ✅
6. IF tidak ada field yang dikirim, THEN `400 VALIDATION_ERROR`. ✅

## Requirement 7 — Token untuk service lain (validasi stateless)

**User story:** Sebagai Queue/Booking Service, saya ingin memvalidasi access token tanpa memanggil User Service, sehingga identitas pembeli login dapat dipakai saat join dan riwayat pesanan.

SRS-F-27-10, SRS-F-27-10a, SRS-F-27-11 · TC-USR-09, TC-USR-09a, TC-USR-10

### Acceptance Criteria (EARS)

1. THE SYSTEM SHALL menerbitkan access token JWT EdDSA (Ed25519) dengan klaim `iss`, `sub` (account UUID), `aud`, `jti`, `scope="buyer"`, `iat`, `exp`, serta `email`, dan — bila akun punya NIK — `nik_hash` (hex) dan `has_nik=true`; TTL `TOKEN_ACCESS_TTL`. ✅
2. WHEN akun tidak punya NIK, THE token SHALL tidak memuat `nik_hash` dan `has_nik` (keduanya `omitempty`; verifikator memperlakukan tidak ada = `false`). ✅
3. THE SYSTEM SHALL membagikan kunci publik (per `kid`) ke Queue dan Booking agar mereka memverifikasi tanpa HTTP call. ✅ (kunci) / distribusi ke service lain **[target, platform R6]**
4. WHEN memverifikasi token, THE verifikator SHALL mengecek signature, issuer, audience, dan `exp` (leeway 5 s). ✅
5. WHEN profil mengubah NIK, klaim baru SHALL berlaku pada access token berikutnya (refresh); token lama tetap berlaku sampai `exp` (≤ `TOKEN_ACCESS_TTL`). ✅ *(konsekuensi desain stateless)*

## Requirement 8 — Health & Postur Event

**User story:** Sebagai platform, saya ingin probe kesehatan dan perilaku event yang jelas.

D-23, ADR-020

### Acceptance Criteria (EARS)

1. WHEN `GET /health`, THE SYSTEM SHALL `200 UP` bila PostgreSQL & Valkey sehat, `503 DOWN` bila tidak. ✅ *(Setelah R1.7, RabbitMQ ikut diperiksa.)*
2. THE SYSTEM SHALL **hanya** mempublikasikan `otp.requested.v1` **[target]** dan tidak mengonsumsi event apa pun. *(Spec lama menyatakan "tidak mempublikasikan event apa pun" — bertentangan dengan R1.7/ADR-020; diperbaiki (K10).)*
3. WHEN dependency belum ter-wire (unit test), THE SYSTEM SHALL hanya memasang `/health`. ✅

## Requirement 9 — Build & Verify

**User story:** Sebagai pengembang, saya ingin membangun dan memverifikasi service secara deterministik.

### Acceptance Criteria (EARS)

1. WHEN `go run ./cmd migrate up`, THE SYSTEM SHALL menerapkan `0001_init` (schema `users`: `accounts`, `sessions`, `login_audit`). ✅
2. WHEN `go run ./cmd api` dengan konfigurasi valid, THE SYSTEM SHALL start di port 8085. ✅
3. IF konfigurasi tidak valid (`TOKEN_*`, `OTP_*`, `PII_*` di luar rentang/sama), THEN gagal start dengan error jelas. ✅
4. WHEN `make build` / `make isolation`, THE modul SHALL ter-compile (termasuk `GOWORK=off`). ✅
5. WHEN `make test`, unit test `-race` SHALL lulus. ✅
6. WHEN `make test-integration`, test adapter terhadap PostgreSQL & Valkey nyata SHALL lulus. ✅ *(kecuali integration test `GetNIKHashByID`/`GetEmailEncByID` yang belum ada — user tasks 1.2)*
7. WHEN `make lint`, golangci-lint + depguard SHALL lulus. *(spec lama mencatat 3 temuan lint pre-existing — verifikasi ulang, user tasks 1.3)*
8. THE coverage logika SHALL ≥ 80% (`make cover`). ✅ *(evidence v1.5: 94,4%)*

---

## Deltas (SDD vs Kode)

Setiap delta diverifikasi dengan membaca kode. Format: *SDD menyatakan → kode sebenarnya*.

| ID | SDD menyatakan | Kode sebenarnya | Sumber kode |
|----|----------------|-----------------|-------------|
| D1 | Token membawa `email`, `nik_hash`, `has_nik`; `aud` array | **Terselesaikan**: klaim identitas sudah diisi; `aud` tetap satu string (`TOKEN_AUDIENCE=rbt`); `nik_hash`/`has_nik` `omitempty` | `pkg/token/token.go`, `security.go` |
| D2 | `POST /auth/otp-request`, `otp-verify` | `POST /api/v1/auth/otp/request`, `otp/verify` | `auth_handler.go` |
| D3 | Refresh token di cookie HttpOnly `rb_refresh` | JSON body `refreshToken` (request & response), tanpa cookie | `auth_handler.go`, `dto/auth.go` |
| D4 | `GET /users/me`, `POST /users/me/nik` | `GET`/`PATCH /api/v1/me` | `auth_handler.go`, `profile.go` |
| D5 | `OTP_INVALID`/`OTP_EXPIRED`=400, `OTP_LOCKED`=403 | `401`, `401`, `429` | `port/inbound/errors.go` |
| D6 | Verify: `accountId`, `isNewAccount`, `expiresAt` + cookie | `tokenType`, `accessToken`, `accessTokenExpiresAt`, `refreshToken`, `refreshTokenExpiresAt`, `account{…}` | `dto/auth.go` |
| D7 | Profil: `email` plaintext, `nikLinked`, `createdAt` | `emailMasked`, `fullName`, `nikMasked`, `hasNik`, `accountId` | `dto/auth.go`, `account_mapper.go` |
| D8 | Env `DATABASE_URL`, `USER_TOKEN_*`, path kunci PII, `OTP_LENGTH`… | D-24: `POSTGRES_*`, `VALKEY_SERVER`, `TOKEN_ISSUER/AUDIENCE/KEY_ID/PRIVATE_KEY_FILE/ACCESS_TTL/REFRESH_TTL`, `PII_ENC_KEY`/`PII_HASH_KEY`/`OTP_HASH_KEY` (hex), `OTP_*`, `SMTP_*`; OTP 6 digit hardcoded | `.env.example`, `config/user.go` |
| D9 | OTP di-hash argon2 + salt | HMAC terikat `email_hash` (`OTP_HASH_KEY`) | `security.go` |
| D10 | Refresh hash `SHA256(token)` | Sama (tanpa delta) | `security.go` |
| D11 | Kolom `accounts`/`login_audit` versi SDD | + `email_masked`, `full_name`, `nik_masked`, `status`, `last_login_at`, `sessions.replaced_by`; `login_audit.outcome` enum | migrasi `0001_init` |
| D12 | Error `ACCOUNT_DISABLED`, `DEPENDENCY_UNAVAILABLE` tidak ada | Ada: `ErrAccountDisabled` (403), `ErrMailUnavailable` (503) — spec lama menyebut `ErrPublishFailed` (belum ada; akan muncul bersama R1.7) | `port/inbound/errors.go` |
| D13 | Riwayat pesanan di User Service ("proxy ke Booking" di HLD) | Tidak ada di User; `GET /api/v1/me/bookings` milik Booking (OQ-10) | `auth_handler.go` |
| D14 | SDD-USER v1.2: SMTP adapter dihapus, diganti publisher `otp.requested` | **Belum**: `infrastructure/smtp/mailer.go` masih dipakai | `user_module.go` |

### Implikasi Delta terhadap Test Plan
- **D5** — assertion status TC-USR-03/05: 401/429 (bukan 400/403).
- **D3/D4/D6** — TC-USR-06/07/08 memakai JSON body dan rute `/api/v1/me`.
- **D14** — TC-MSG-05/06 dan TC-USR-11 (bagian broker) baru dapat dijalankan setelah user tasks 3.

## Traceability

| Req | SRS | TC | Status |
|-----|-----|----|--------|
| 1 Request OTP | F-27-02/04/12 | TC-USR-02/04/11, TC-MSG-05 | ✅ kecuali 1.7–1.8 (event) |
| 2 Verify | F-27-03/05 | TC-USR-03/05 | ✅ |
| 3 Refresh | F-27-06 | TC-USR-06 | ✅ |
| 4 Logout | F-27-07 | TC-USR-07 | ✅ |
| 5 Profil | F-27-09 | TC-USR-08/12 | ✅ |
| 6 Update profil | F-27-08/09, F-11-01 | TC-USR-08 | ✅ (NIK parsial) |
| 7 Token | F-27-10/10a/11 | TC-USR-09/09a/10 | ✅ (distribusi kunci target) |
| 8 Health & event | D-23, ADR-020 | — | ✅ / target |
| 9 Build & verify | — | CI | ✅ |
