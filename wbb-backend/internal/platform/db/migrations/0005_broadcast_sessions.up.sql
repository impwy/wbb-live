CREATE TABLE broadcast_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    publisher_auth_family_id uuid NOT NULL REFERENCES auth_session_families(id),
    initial_auth_session_id uuid NOT NULL REFERENCES auth_sessions(id),
    source text NOT NULL CHECK (source IN ('screen', 'camera')),
    state text NOT NULL CHECK (state IN ('starting', 'live', 'stopping', 'ended')),
    version bigint NOT NULL DEFAULT 1,
    media_generation bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz NOT NULL DEFAULT now(),
    prepare_deadline timestamptz NOT NULL,
    ended_at timestamptz,
    end_reason text,
    ingest_connection_id text,
    worker_lease_until timestamptz
);

CREATE UNIQUE INDEX broadcast_sessions_one_active ON broadcast_sessions (broadcast_id)
    WHERE state IN ('starting', 'live', 'stopping');
CREATE INDEX broadcast_sessions_latest ON broadcast_sessions (broadcast_id, created_at DESC, id DESC);
CREATE INDEX broadcast_sessions_family_active ON broadcast_sessions (publisher_auth_family_id)
    WHERE state IN ('starting', 'live', 'stopping');

CREATE TABLE session_start_requests (
    user_id uuid NOT NULL REFERENCES users(id),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    operation text NOT NULL DEFAULT 'start',
    idempotency_key text NOT NULL,
    source text NOT NULL,
    auth_family_id uuid NOT NULL REFERENCES auth_session_families(id),
    broadcast_session_id uuid NOT NULL REFERENCES broadcast_sessions(id),
    original_grant_jti uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, broadcast_id, operation, idempotency_key)
);
CREATE INDEX session_start_requests_expiry ON session_start_requests (expires_at);

CREATE TABLE publish_grants (
    jti uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_session_id uuid NOT NULL REFERENCES broadcast_sessions(id),
    auth_family_id uuid NOT NULL REFERENCES auth_session_families(id),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    consumed_at timestamptz
);
CREATE INDEX publish_grants_session_latest ON publish_grants (broadcast_session_id, issued_at DESC);

-- Reserved before a future WHIP upstream create. 04-A cannot create one;
-- any unresolved row must prevent confirmation that resources are absent.
CREATE TABLE media_create_operations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_session_id uuid NOT NULL REFERENCES broadcast_sessions(id),
    media_generation bigint NOT NULL,
    grant_jti uuid NOT NULL REFERENCES publish_grants(jti),
    state text NOT NULL CHECK (state IN ('pending', 'attached', 'cleanup_pending', 'closed')),
    upstream_connection_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);
CREATE UNIQUE INDEX media_create_operations_one_unresolved ON media_create_operations (broadcast_session_id)
    WHERE state <> 'closed';

CREATE TABLE outbox_events (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    aggregate_id uuid NOT NULL,
    aggregate_version bigint NOT NULL,
    type text NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    payload jsonb NOT NULL,
    traceparent text,
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    UNIQUE (aggregate_id, aggregate_version)
);

CREATE TABLE consumer_inbox (
    consumer_name text NOT NULL,
    event_id uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_name, event_id)
);
