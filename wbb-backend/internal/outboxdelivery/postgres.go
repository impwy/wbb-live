package outboxdelivery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ DB *pgxpool.Pool }

// Claim holds a row lock only for the SQL update, never through a broker call.
func (s PostgresStore) Claim(ctx context.Context, lease time.Duration) (*Event, error) {
	row := s.DB.QueryRow(ctx, `WITH candidate AS (
	 SELECT event_id FROM outbox_events
	 WHERE published_at IS NULL AND quarantined_at IS NULL
	   AND next_attempt_at <= clock_timestamp()
	   AND (claim_until IS NULL OR claim_until <= clock_timestamp())
	 ORDER BY next_attempt_at, occurred_at, event_id
	 FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE outbox_events e SET
	 claim_token=gen_random_uuid(), claim_until=clock_timestamp()+$1::interval,
	 attempts=e.attempts+1
	 FROM candidate c WHERE e.event_id=c.event_id
	 RETURNING e.event_id::text,e.broadcast_id::text,e.aggregate_id::text,
	 e.aggregate_version,e.type,e.schema_version,e.occurred_at,e.payload,e.traceparent,
	 e.claim_token::text,e.attempts`, lease.String())
	e := new(Event)
	err := row.Scan(&e.EventID, &e.BroadcastID, &e.AggregateID, &e.AggregateVersion, &e.Type, &e.SchemaVersion, &e.OccurredAt, &e.Payload, &e.Traceparent, &e.ClaimToken, &e.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s PostgresStore) MarkPublished(ctx context.Context, e Event) (bool, error) {
	r, err := s.DB.Exec(ctx, `UPDATE outbox_events SET published_at=clock_timestamp(),claim_token=NULL,claim_until=NULL,last_error_code=NULL
	 WHERE event_id=$1 AND claim_token=$2 AND published_at IS NULL AND quarantined_at IS NULL AND claim_until>clock_timestamp()`, e.EventID, e.ClaimToken)
	return r.RowsAffected() == 1, err
}
func (s PostgresStore) Retry(ctx context.Context, e Event, code string, delay time.Duration) (bool, error) {
	r, err := s.DB.Exec(ctx, `UPDATE outbox_events SET claim_token=NULL,claim_until=NULL,
	 next_attempt_at=clock_timestamp()+$3::interval,last_error_code=$4
	 WHERE event_id=$1 AND claim_token=$2 AND published_at IS NULL AND quarantined_at IS NULL AND claim_until>clock_timestamp()`, e.EventID, e.ClaimToken, delay.String(), code)
	return r.RowsAffected() == 1, err
}
func (s PostgresStore) Quarantine(ctx context.Context, e Event, code string) (bool, error) {
	r, err := s.DB.Exec(ctx, `UPDATE outbox_events SET quarantined_at=clock_timestamp(),claim_token=NULL,claim_until=NULL,last_error_code=$3
	 WHERE event_id=$1 AND claim_token=$2 AND published_at IS NULL AND quarantined_at IS NULL AND claim_until>clock_timestamp()`, e.EventID, e.ClaimToken, code)
	return r.RowsAffected() == 1, err
}

// Requeue invalidates any old claim. It never changes an immutable event field.
func (s PostgresStore) Requeue(ctx context.Context, id string) (bool, error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	r, err := s.DB.Exec(ctx, `UPDATE outbox_events SET quarantined_at=NULL,claim_token=NULL,claim_until=NULL,
	 next_attempt_at=clock_timestamp(),last_error_code=NULL
	 WHERE event_id=$1 AND quarantined_at IS NOT NULL AND published_at IS NULL`, id)
	return r.RowsAffected() == 1, err
}

type Stats struct {
	Pending          int64
	Quarantined      int64
	OldestAgeSeconds *float64
	Attempts         int64
}

type QuarantinedEvent struct {
	EventID    string    `json:"event_id"`
	ErrorCode  string    `json:"error_code"`
	Attempts   int       `json:"attempts"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (s PostgresStore) Quarantined(ctx context.Context) ([]QuarantinedEvent, error) {
	rows, err := s.DB.Query(ctx, `SELECT event_id::text,COALESCE(last_error_code,'unknown'),attempts,occurred_at
	 FROM outbox_events WHERE quarantined_at IS NOT NULL AND published_at IS NULL
	 ORDER BY quarantined_at DESC,event_id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]QuarantinedEvent, 0)
	for rows.Next() {
		var e QuarantinedEvent
		if err := rows.Scan(&e.EventID, &e.ErrorCode, &e.Attempts, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// QuarantinedByID reaches an older item even when the recent list is full.
func (s PostgresStore) QuarantinedByID(ctx context.Context, id string) (*QuarantinedEvent, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	e := new(QuarantinedEvent)
	err := s.DB.QueryRow(ctx, `SELECT event_id::text,COALESCE(last_error_code,'unknown'),attempts,occurred_at
	 FROM outbox_events WHERE event_id=$1 AND quarantined_at IS NOT NULL AND published_at IS NULL`, id).
		Scan(&e.EventID, &e.ErrorCode, &e.Attempts, &e.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s PostgresStore) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.DB.QueryRow(ctx, `SELECT
	 count(*) FILTER (WHERE published_at IS NULL AND quarantined_at IS NULL),
	 count(*) FILTER (WHERE quarantined_at IS NOT NULL),
	 EXTRACT(EPOCH FROM clock_timestamp()-min(occurred_at) FILTER (WHERE published_at IS NULL AND quarantined_at IS NULL))::float8,
	 COALESCE(sum(attempts),0)
	 FROM outbox_events`).Scan(&st.Pending, &st.Quarantined, &st.OldestAgeSeconds, &st.Attempts)
	return st, err
}
