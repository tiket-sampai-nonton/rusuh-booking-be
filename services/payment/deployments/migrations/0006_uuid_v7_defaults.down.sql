ALTER TABLE payment_attempts  ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payment_callbacks ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE refund_cases      ALTER COLUMN id SET DEFAULT gen_random_uuid();
