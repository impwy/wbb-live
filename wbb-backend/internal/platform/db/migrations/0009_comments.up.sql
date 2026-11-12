CREATE TABLE broadcast_comments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    author_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 500),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz
);

CREATE INDEX broadcast_comments_feed_idx ON broadcast_comments (broadcast_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
