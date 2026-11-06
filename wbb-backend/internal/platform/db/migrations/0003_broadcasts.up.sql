CREATE TABLE broadcasts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- Soft-deleted broadcasts are excluded from public reads.
CREATE INDEX broadcasts_owner_active_idx ON broadcasts (owner_id) WHERE deleted_at IS NULL;
CREATE INDEX broadcasts_feed_idx ON broadcasts (created_at DESC, id DESC) WHERE deleted_at IS NULL;
