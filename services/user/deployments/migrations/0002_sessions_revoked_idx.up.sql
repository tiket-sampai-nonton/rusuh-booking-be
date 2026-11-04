-- Indeks untuk RevokeAllForAccount: mencari sesi akun yang baru dicabut (jendela denylist) tanpa memindai semua baris akun.
CREATE INDEX idx_sessions_account_revoked ON sessions(account_id, revoked_at) WHERE revoked_at IS NOT NULL;
