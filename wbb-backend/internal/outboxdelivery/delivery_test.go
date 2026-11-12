package outboxdelivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryStore struct {
	event       Event
	claimed     bool
	marked      bool
	retried     bool
	quarantined bool
	lost        bool
}

func (m *memoryStore) Claim(context.Context, time.Duration) (*Event, error) {
	if m.claimed {
		return nil, nil
	}
	m.claimed = true
	e := m.event
	return &e, nil
}
func (m *memoryStore) MarkPublished(context.Context, Event) (bool, error) {
	m.marked = true
	return !m.lost, nil
}
func (m *memoryStore) Retry(context.Context, Event, string, time.Duration) (bool, error) {
	m.retried = true
	return !m.lost, nil
}
func (m *memoryStore) Quarantine(context.Context, Event, string) (bool, error) {
	m.quarantined = true
	return !m.lost, nil
}

type fakeProducer struct{ err error }

func (p fakeProducer) Publish(context.Context, string, []byte) error { return p.err }

func TestOnlyAcknowledgedRecordIsMarked(t *testing.T) {
	for _, tc := range []struct {
		name        string
		producerErr error
		mark, retry bool
	}{
		{"ack", nil, true, false}, {"broker failure", errors.New("broker unavailable"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &memoryStore{event: Event{EventID: "00000000-0000-0000-0000-000000000001", BroadcastID: "00000000-0000-0000-0000-000000000002", AggregateID: "00000000-0000-0000-0000-000000000003", AggregateVersion: 1, Type: "broadcast.session.starting", SchemaVersion: 1, OccurredAt: time.Now(), Payload: []byte(`{"state":"starting"}`), ClaimToken: "00000000-0000-0000-0000-000000000004"}}
			d := Dispatcher{Store: s, Producer: fakeProducer{tc.producerErr}}
			if err := d.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s.marked != tc.mark || s.retried != tc.retry || s.quarantined {
				t.Fatalf("mark=%v retry=%v quarantine=%v", s.marked, s.retried, s.quarantined)
			}
		})
	}
}

func TestMalformedEventQuarantinedWithoutPublishing(t *testing.T) {
	s := &memoryStore{event: Event{Type: "unknown", SchemaVersion: 1, Payload: []byte(`{}`)}}
	if err := (&Dispatcher{Store: s, Producer: fakeProducer{errors.New("must not call")}}).Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.quarantined || s.marked || s.retried {
		t.Fatalf("unexpected result: %+v", s)
	}
}

func TestLostClaimReportedForEveryOutcome(t *testing.T) {
	for _, tc := range []struct {
		name        string
		event       Event
		producerErr error
	}{
		{"ack", validEvent(), nil},
		{"retry", validEvent(), errors.New("broker down")},
		{"quarantine", Event{Type: "bad"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &memoryStore{event: tc.event, lost: true}
			err := (&Dispatcher{Store: s, Producer: fakeProducer{tc.producerErr}}).Step(context.Background())
			if !errors.Is(err, ErrClaimLost) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func validEvent() Event {
	return Event{EventID: "00000000-0000-0000-0000-000000000001", BroadcastID: "00000000-0000-0000-0000-000000000002", AggregateID: "00000000-0000-0000-0000-000000000003", AggregateVersion: 1, Type: "broadcast.session.starting", SchemaVersion: 1, OccurredAt: time.Now(), Payload: []byte(`{"state":"starting"}`), ClaimToken: "00000000-0000-0000-0000-000000000004"}
}
