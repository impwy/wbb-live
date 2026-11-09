package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// RecordSessionEvent records the state transition in the same transaction as
// the session row. Delivery to Kafka is a later ticket.
func RecordSessionEvent(ctx context.Context, tx pgx.Tx, broadcastID, sessionID, state string, version int64) error {
	payload, err := json.Marshal(map[string]string{
		"broadcastId": broadcastID, "sessionId": sessionID, "state": state,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events
		(broadcast_id, aggregate_id, aggregate_version, type, payload)
		VALUES ($1, $2, $3, $4, $5)`, broadcastID, sessionID, version,
		"broadcast.session."+state, payload)
	return err
}
