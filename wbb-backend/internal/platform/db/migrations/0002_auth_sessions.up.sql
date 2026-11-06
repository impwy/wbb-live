-- The family ledger is the authoritative revocation unit. Rotation, logout
-- and reuse detection all serialize on the family row (SELECT ... FOR UPDATE
-- or UPDATE), so a revocation can never miss a concurrently created session.
CREATE TABLE auth_session_families (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    revoked_reason text
);

CREATE INDEX auth_session_families_user_idx ON auth_session_families (user_id);

CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    refresh_family_id uuid NOT NULL REFERENCES auth_session_families(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash text NOT NULL,
    rotated_from_id uuid REFERENCES auth_sessions(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_reason text
);

-- Only token hashes are stored, never refresh token plaintext.
CREATE UNIQUE INDEX auth_sessions_token_hash_key ON auth_sessions (refresh_token_hash);
CREATE INDEX auth_sessions_family_idx ON auth_sessions (refresh_family_id);
CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id);
