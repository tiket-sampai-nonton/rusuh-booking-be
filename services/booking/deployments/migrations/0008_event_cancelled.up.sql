-- 0008: status CANCELLED untuk event yang dibatalkan penyelenggara (SCRUM-56). Status tersimpan (diatur admin) dan
-- mengalahkan status turunan dari jendela penjualan, seperti SOLD_OUT dan CLOSED.
ALTER TABLE booking.events DROP CONSTRAINT IF EXISTS events_status_check;
ALTER TABLE booking.events
    ADD CONSTRAINT events_status_check
    CHECK (status IN ('DRAFT','NOT_STARTED','AVAILABLE','SOLD_OUT','CLOSED','CANCELLED'));
