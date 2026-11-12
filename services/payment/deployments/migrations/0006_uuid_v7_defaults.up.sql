-- 0006 (ADR-032): sisa tabel payment memakai UUIDv7 untuk id baru; fungsi uuid_generate_v7() dibuat di 0005.
-- ID v4 yang sudah ada tidak diubah. Aplikasi biasanya mengisi id sendiri, default ini untuk sisipan tanpa id.
ALTER TABLE payment_attempts  ALTER COLUMN id SET DEFAULT uuid_generate_v7();
ALTER TABLE payment_callbacks ALTER COLUMN id SET DEFAULT uuid_generate_v7();
ALTER TABLE refund_cases      ALTER COLUMN id SET DEFAULT uuid_generate_v7();
