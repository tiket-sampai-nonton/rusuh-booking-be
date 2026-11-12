ALTER TABLE events ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE ticket_categories ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE admissions ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE bookings ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE booking_items ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE tickets ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE outbox_events ALTER COLUMN id SET DEFAULT gen_random_uuid();
DROP FUNCTION IF EXISTS uuid_generate_v7();
