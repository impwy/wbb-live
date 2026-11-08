CREATE TABLE auth_identities (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    issuer text NOT NULL,
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, issuer, subject)
);
CREATE INDEX auth_identities_user_idx ON auth_identities (user_id);
