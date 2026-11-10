
ALTER TABLE event_settings
    ADD COLUMN poll_far_above bigint NOT NULL DEFAULT 500 CHECK (poll_far_above >= 1),
    ADD COLUMN poll_mid_above bigint NOT NULL DEFAULT 100 CHECK (poll_mid_above >= 1),
    ADD CONSTRAINT event_settings_poll_above_order CHECK (poll_mid_above < poll_far_above);
