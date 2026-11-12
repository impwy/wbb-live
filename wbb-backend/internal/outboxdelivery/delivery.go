// Package outboxdelivery publishes durable session events without delaying API
// or media worker transactions. Kafka delivery is at least once.
package outboxdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"time"
)

const Topic = "wbb.broadcast.events.v1"
const MaxRecordBytes = 512 * 1024

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var ErrClaimLost = errors.New("claim_lost")

type Event struct {
	EventID          string          `json:"event_id"`
	BroadcastID      string          `json:"-"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	Type             string          `json:"type"`
	SchemaVersion    int             `json:"schema_version"`
	OccurredAt       time.Time       `json:"occurred_at"`
	Payload          json.RawMessage `json:"payload"`
	Traceparent      *string         `json:"traceparent"`
	ClaimToken       string          `json:"-"`
	Attempts         int             `json:"-"`
}

type Store interface {
	Claim(context.Context, time.Duration) (*Event, error)
	MarkPublished(context.Context, Event) (bool, error)
	Retry(context.Context, Event, string, time.Duration) (bool, error)
	Quarantine(context.Context, Event, string) (bool, error)
}

// Producer must return nil only after the broker confirms this record.
type Producer interface {
	Publish(context.Context, string, []byte) error
}

type Dispatcher struct {
	Store       Store
	Producer    Producer
	Lease       time.Duration
	MaxBytes    int
	OnErrorCode func(string)
}

func (d *Dispatcher) Step(ctx context.Context) error {
	if d.Store == nil || d.Producer == nil {
		return errors.New("outbox store and producer required")
	}
	lease := d.Lease
	if lease == 0 {
		lease = 45 * time.Second
	}
	e, err := d.Store.Claim(ctx, lease)
	if err != nil || e == nil {
		return err
	}
	max := d.MaxBytes
	if max == 0 {
		max = MaxRecordBytes
	}
	value, code := encode(*e, max)
	if code != "" {
		changed, err := d.Store.Quarantine(ctx, *e, code)
		return mutationResult(changed, err)
	}
	if err = d.Producer.Publish(ctx, e.BroadcastID, value); err != nil {
		// Broker authentication, topic, quota, or connection failures are
		// infrastructure failures, never evidence that this event is poison.
		changed, dbErr := d.Store.Retry(ctx, *e, "broker_unconfirmed", retryDelay(e.Attempts))
		return mutationResult(changed, dbErr)
	}
	changed, err := d.Store.MarkPublished(ctx, *e)
	return mutationResult(changed, err)
}

func mutationResult(changed bool, err error) error {
	if err != nil {
		return err
	}
	if !changed {
		return ErrClaimLost
	}
	return nil
}

func (d *Dispatcher) Run(ctx context.Context) error {
	delay := 200 * time.Millisecond
	for ctx.Err() == nil {
		stepCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := d.Step(stepCtx)
		cancel()
		if err != nil && d.OnErrorCode != nil {
			code := "delivery_store_unavailable"
			if errors.Is(err, ErrClaimLost) {
				code = "claim_lost"
			}
			d.OnErrorCode(code)
		}
		if err == nil {
			delay = 200 * time.Millisecond
		} else if delay < 5*time.Second {
			delay *= 2
		}
		wait := delay + time.Duration(rand.Int63n(int64(delay/2+1)))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
	return nil
}

func encode(e Event, max int) ([]byte, string) {
	if !uuidPattern.MatchString(e.EventID) || !uuidPattern.MatchString(e.BroadcastID) || !uuidPattern.MatchString(e.AggregateID) || e.AggregateVersion < 1 || e.SchemaVersion != 1 || e.OccurredAt.IsZero() {
		return nil, "invalid_envelope"
	}
	switch e.Type {
	case "broadcast.session.starting", "broadcast.session.live", "broadcast.session.stopping", "broadcast.session.ended":
	default:
		return nil, "invalid_type"
	}
	if !json.Valid(e.Payload) || !bytes.HasPrefix(bytes.TrimSpace(e.Payload), []byte("{")) {
		return nil, "invalid_payload"
	}
	value, err := json.Marshal(e)
	if err != nil {
		return nil, "invalid_envelope"
	}
	if len(value)+len(e.BroadcastID) > max {
		return nil, "record_too_large"
	}
	return value, ""
}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	base := time.Second * time.Duration(1<<uint(attempts-1))
	if base > time.Minute {
		base = time.Minute
	}
	return base + time.Duration(rand.Int63n(int64(base/2+1)))
}

func ValidateID(id string) error {
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("invalid event_id")
	}
	return nil
}
