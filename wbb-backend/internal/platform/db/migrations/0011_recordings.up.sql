-- Recorded 720p segments of finished broadcast sessions. file_path is the
-- storage location relative to the recordings root; it is never exposed.
CREATE TABLE recordings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES broadcast_sessions(id),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    file_path text NOT NULL UNIQUE,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    started_at timestamptz NOT NULL,
    duration_seconds integer NOT NULL CHECK (duration_seconds >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX recordings_feed_idx ON recordings (started_at DESC, id DESC);
CREATE INDEX recordings_broadcast_idx ON recordings (broadcast_id, started_at DESC);
