-- Pembeli yang login (opsional, ADR-020): akun dicatat di audit entry. Guest tetap NULL.
ALTER TABLE entry_audit ADD COLUMN account_id uuid;
