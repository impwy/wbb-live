package httpapi_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/outboxdelivery"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Public HTTP → atomic PG outbox → actual broker → consumer contract.
// The dedicated local Kafka is opt-in and never started by this test.
func TestOutboxPublicSessionToKafka(t *testing.T) {
	broker := os.Getenv("WBB_TEST_KAFKA_BROKERS")
	if broker == "" {
		t.Skip("WBB_TEST_KAFKA_BROKERS required")
	}
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "Kafka 전달 검사")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	start := startSession(t, client, base, access, "camera", "outbox-kafka-0001")
	if start.status != 202 {
		t.Fatalf("prepare status=%d", start.status)
	}
	sid := start.body["session"].(map[string]any)["id"].(string)
	stop := do(t, client, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop status=%d", stop.status)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got := do(t, client, "GET", base+"/sessions/current", nil, "")
		if got.body["session"].(map[string]any)["state"] == "ended" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var eventIDs []string
	rows, err := testPool.Query(t.Context(), `SELECT event_id::text FROM outbox_events WHERE aggregate_id=$1 ORDER BY aggregate_version`, sid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		eventIDs = append(eventIDs, id)
	}
	rows.Close()
	if len(eventIDs) != 3 {
		t.Fatalf("HTTP transitions produced %d events", len(eventIDs))
	}
	producer, err := outboxdelivery.NewKafkaProducer(broker)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	dispatcher := outboxdelivery.Dispatcher{Store: outboxdelivery.PostgresStore{DB: testPool}, Producer: producer}
	var ownPublished int
	for ownPublished < len(eventIDs) && ctx.Err() == nil {
		if err := dispatcher.Step(ctx); err != nil {
			t.Fatalf("delivery: %v", err)
		}
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND published_at IS NOT NULL`, sid).Scan(&ownPublished); err != nil {
			t.Fatal(err)
		}
	}
	if ownPublished != len(eventIDs) {
		t.Fatalf("own session published %d/%d", ownPublished, len(eventIDs))
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(outboxdelivery.Topic), kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	got := map[string]int64{}
	for len(got) < len(eventIDs) && ctx.Err() == nil {
		fetches := consumer.PollFetches(ctx)
		fetches.EachRecord(func(record *kgo.Record) {
			var e outboxdelivery.Event
			if json.Unmarshal(record.Value, &e) != nil {
				return
			}
			if e.AggregateID != sid {
				return
			}
			if string(record.Key) != b["id"].(string) {
				t.Error("wrong broadcast key")
			}
			got[e.EventID] = e.AggregateVersion
		})
	}
	if len(got) != 3 {
		t.Fatalf("Kafka received %d of 3 HTTP events", len(got))
	}
	for i, id := range eventIDs {
		if got[id] != int64(i+1) {
			t.Fatalf("event version mismatch at %d", i)
		}
	}
	var published int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND published_at IS NOT NULL`, sid).Scan(&published); err != nil || published != 3 {
		t.Fatalf("published rows=%d err=%v", published, err)
	}
}

// Run only while the dedicated Kafka container is stopped; the test process
// never controls existing services. An external orchestrator restarts Kafka
// after the first unconfirmed attempt has been observed.
func TestOutboxPublicSessionDuringKafkaOutage(t *testing.T) {
	if os.Getenv("WBB_TEST_KAFKA_OUTAGE") != "1" {
		t.Skip("explicit outage run only")
	}
	broker := os.Getenv("WBB_TEST_KAFKA_BROKERS")
	if broker == "" {
		t.Fatal("broker address required")
	}
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "Kafka 중단 중 회차 종료")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	start := startSession(t, client, base, access, "camera", "outbox-outage-0001")
	if start.status != 202 {
		t.Fatalf("prepare during outage=%d", start.status)
	}
	sid := start.body["session"].(map[string]any)["id"].(string)
	if stop := do(t, client, "POST", base+"/sessions/"+sid+"/stop", nil, access); stop.status != 202 {
		t.Fatalf("stop during outage=%d", stop.status)
	}
	deadline := time.Now().Add(8 * time.Second)
	ended := false
	for time.Now().Before(deadline) {
		got := do(t, client, "GET", base+"/sessions/current", nil, "")
		if got.body["session"].(map[string]any)["state"] == "ended" {
			ended = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ended {
		t.Fatal("ended did not progress while Kafka down")
	}
	var pending int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND published_at IS NULL`, sid).Scan(&pending); err != nil || pending != 3 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	producer, err := outboxdelivery.NewKafkaProducer(broker)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	firstCtx, firstCancel := context.WithTimeout(t.Context(), 20*time.Second)
	err = (&outboxdelivery.Dispatcher{Store: outboxdelivery.PostgresStore{DB: testPool}, Producer: producer}).Step(firstCtx)
	firstCancel()
	if err != nil {
		t.Fatalf("unconfirmed attempt: %v", err)
	}
	var marked int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND published_at IS NOT NULL`, sid).Scan(&marked); err != nil || marked != 0 {
		t.Fatalf("marked without broker ACK=%d err=%v", marked, err)
	}
	recoveryCtx, recoveryCancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer recoveryCancel()
	d := outboxdelivery.Dispatcher{Store: outboxdelivery.PostgresStore{DB: testPool}, Producer: producer}
	for marked < 3 && recoveryCtx.Err() == nil {
		_ = d.Step(recoveryCtx)
		if err := testPool.QueryRow(recoveryCtx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND published_at IS NOT NULL`, sid).Scan(&marked); err != nil {
			t.Fatal(err)
		}
		if marked < 3 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if marked != 3 {
		t.Fatalf("recovery delivered %d/3", marked)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(outboxdelivery.Topic), kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	seen := map[string]int64{}
	for len(seen) < 3 && recoveryCtx.Err() == nil {
		consumer.PollFetches(recoveryCtx).EachRecord(func(record *kgo.Record) {
			var e outboxdelivery.Event
			if json.Unmarshal(record.Value, &e) != nil || e.AggregateID != sid {
				return
			}
			if string(record.Key) != b["id"].(string) {
				t.Error("recovered record key mismatch")
			}
			seen[e.EventID] = e.AggregateVersion
		})
	}
	if len(seen) != 3 {
		t.Fatalf("consumer received %d/3 recovered events", len(seen))
	}
}
