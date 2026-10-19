-- User Service (ADR-020): akun opsional dengan login OTP email tanpa password.
-- Guest checkout tetap jalan tanpa akun. Email dan NIK disimpan terenkripsi (AES-256-GCM)
-- dengan hash HMAC-SHA256 untuk pencarian; tampilan memakai versi tersamar.
-- Kode OTP tidak disimpan di sini: hash OTP dan hitungan percobaan ada di Valkey dengan TTL.

CREATE TABLE accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email_hash bytea NOT NULL UNIQUE,              -- HMAC-SHA256(lower(trim(email)))
    email_enc bytea NOT NULL,                      -- AES-256-GCM (nonce || ciphertext)
    email_masked text NOT NULL,                    -- mis. ar***@gmail.com
    full_name text CHECK (full_name IS NULL OR char_length(full_name) BETWEEN 1 AND 100),
    nik_hash bytea,                                -- sama dengan buyer_nik_hash di Booking/Queue
    nik_enc bytea,
    nik_masked text,                               -- mis. 3174********0001
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CHECK ((nik_hash IS NULL) = (nik_enc IS NULL) AND (nik_hash IS NULL) = (nik_masked IS NULL))
);
-- Satu NIK hanya boleh tersimpan di satu akun, agar kuota NIK pembeli tidak diakali lewat banyak akun.
CREATE UNIQUE INDEX uq_accounts_nik ON accounts(nik_hash) WHERE nik_hash IS NOT NULL;

-- Refresh token disimpan sebagai hash; logout dan rotasi mengisi revoked_at.
CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    refresh_token_hash bytea NOT NULL UNIQUE,      -- SHA-256 dari token acak 32 byte
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    replaced_by uuid REFERENCES sessions(id),
    CHECK (expires_at > created_at)
);
CREATE INDEX idx_sessions_account_active ON sessions(account_id) WHERE revoked_at IS NULL;

-- Jejak login untuk audit dan deteksi brute force (tanpa email plaintext, tanpa kode OTP).
CREATE TABLE login_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
    email_hash bytea NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('OTP_SENT','OTP_RATE_LIMITED','LOGIN_OK','OTP_INVALID','OTP_EXPIRED','OTP_LOCKED','REFRESH_OK','REFRESH_REUSED','LOGOUT')),
    client_ip inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_login_audit_email_time ON login_audit(email_hash, created_at DESC);
