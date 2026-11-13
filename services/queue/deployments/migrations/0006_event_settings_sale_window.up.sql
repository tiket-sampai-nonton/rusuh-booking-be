-- 0006 (C8.10, D-C8.10): the sale window Booking reports, written by the status publisher. The workers iterate an event
-- until one day after sales_ends_at; NULL (window not known yet) keeps the event iterated. No default, no backfill.
ALTER TABLE event_settings
    ADD COLUMN sales_starts_at timestamptz,
    ADD COLUMN sales_ends_at   timestamptz;
-- Operator note (R40): setting sales_ends_at back to NULL re-activates the event, so the workers iterate it again. Use it
-- to extend a sale past its cut-off. The status publisher writes Booking's window again only when that window changes or
-- after a restart, so after a NULL the window stays as set until one of those happens.
