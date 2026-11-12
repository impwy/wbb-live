ALTER TABLE media_create_operations
    ADD COLUMN upstream_resource_path text,
    ADD COLUMN upstream_etag text,
    ADD COLUMN request_started_at timestamptz,
    ADD COLUMN abandoned_at timestamptz;

CREATE UNIQUE INDEX media_create_operations_resource ON media_create_operations (upstream_resource_path)
    WHERE upstream_resource_path IS NOT NULL;

ALTER TABLE broadcast_sessions ADD COLUMN media_fencing_token bigint NOT NULL DEFAULT 0;
ALTER TABLE broadcast_sessions ADD COLUMN first_ingress_at timestamptz;
ALTER TABLE broadcast_sessions ADD COLUMN ingress_lost_at timestamptz;

CREATE TABLE media_worker_runs (
    broadcast_session_id uuid PRIMARY KEY REFERENCES broadcast_sessions(id),
    media_generation bigint NOT NULL,
    fencing_token bigint NOT NULL,
    worker_incarnation uuid NOT NULL,
    run_id uuid NOT NULL UNIQUE,
    pid integer,
    process_start_ticks bigint,
    lock_inherited boolean NOT NULL DEFAULT false,
    lease_until timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('starting','running','stopping','exited')),
    updated_at timestamptz NOT NULL DEFAULT now()
);
