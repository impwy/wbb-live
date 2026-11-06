CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    password_hash text,
    display_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Email is stored normalized (lower-cased by the application).
CREATE UNIQUE INDEX users_email_key ON users (email);
