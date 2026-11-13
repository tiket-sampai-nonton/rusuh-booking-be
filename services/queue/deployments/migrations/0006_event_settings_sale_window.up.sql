-- 0006 (C8.10, D-C8.10): the sale window Booking reports, written by the status publisher. The workers iterate an event
-- until one day after sales_ends_at; NULL (window not known yet) keeps the event iterated. No default, no backfill.
ALTER TABLE event_settings
    ADD COLUMN sales_starts_at timestamptz,
    ADD COLUMN sales_ends_at   timestamptz;
