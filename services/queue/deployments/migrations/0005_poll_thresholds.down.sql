
ALTER TABLE event_settings
    DROP CONSTRAINT event_settings_poll_above_order,
    DROP COLUMN poll_far_above,
    DROP COLUMN poll_mid_above;
