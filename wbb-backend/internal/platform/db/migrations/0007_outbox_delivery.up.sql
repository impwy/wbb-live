ALTER TABLE outbox_events
    ADD COLUMN claim_token uuid,
    ADD COLUMN claim_until timestamptz,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN quarantined_at timestamptz,
    ADD COLUMN last_error_code text;

CREATE INDEX outbox_events_delivery_pending
    ON outbox_events (next_attempt_at, occurred_at, event_id)
    WHERE published_at IS NULL AND quarantined_at IS NULL;
