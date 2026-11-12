-- One row per payment order. The recipient is fixed from the broadcast owner
-- when the order is created; confirmation flips pending -> confirmed exactly
-- once (repeated approval notices find it already confirmed).
CREATE TABLE donations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL UNIQUE,
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id),
    recipient_id uuid NOT NULL REFERENCES users(id),
    donor_id uuid NOT NULL REFERENCES users(id),
    amount integer NOT NULL CHECK (amount BETWEEN 1000 AND 1000000),
    message text NOT NULL DEFAULT '' CHECK (char_length(message) <= 200),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    payment_key text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    confirmed_at timestamptz
);

CREATE INDEX donations_broadcast_confirmed_idx ON donations (broadcast_id, confirmed_at DESC)
    WHERE status = 'confirmed';
