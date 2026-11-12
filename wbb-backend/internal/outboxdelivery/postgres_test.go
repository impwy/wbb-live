package outboxdelivery

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

// The real-PostgreSQL seam is opt-in to avoid touching a developer database.
// WBB_TEST_PG_ADMIN_URL must name a disposable-capable server.
func TestPostgresClaimFencingAndExactRequeue(t *testing.T) {
	adminURL := os.Getenv("WBB_TEST_PG_ADMIN_URL")
	if adminURL == "" {
		t.Skip("WBB_TEST_PG_ADMIN_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal("admin connection failed")
	}
	t.Cleanup(admin.Close)
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	name := "wbb_outbox_test_" + hex.EncodeToString(random)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal("create test database failed")
	}
	t.Cleanup(func() {
		cleanupCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s failed: %v", name, err)
		}
	})
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal("invalid admin URL")
	}
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("test database connection failed")
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal("test migration failed", err)
	}
	var bid, eid string
	if err := pool.QueryRow(ctx, `WITH owner AS (INSERT INTO users(email,display_name) VALUES('outbox-test@example.invalid','Outbox Test') RETURNING id)
	 INSERT INTO broadcasts(owner_id,title) SELECT id,'Outbox Test' FROM owner RETURNING id::text`).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
	 VALUES($1,gen_random_uuid(),1,'broadcast.session.starting','{"state":"starting"}') RETURNING event_id::text`, bid).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	s := PostgresStore{DB: pool}
	first, err := s.Claim(ctx, time.Second)
	if err != nil || first == nil || first.EventID != eid {
		t.Fatalf("first claim: found=%v err=%v", first != nil, err)
	}
	none, err := s.Claim(ctx, time.Second)
	if err != nil || none != nil {
		t.Fatalf("claim overlap: found=%v err=%v", none != nil, err)
	}
	time.Sleep(1100 * time.Millisecond)
	second, err := s.Claim(ctx, time.Second)
	if err != nil || second == nil || second.ClaimToken == first.ClaimToken {
		t.Fatalf("reclaim: found=%v err=%v", second != nil, err)
	}
	if ok, err := s.MarkPublished(ctx, *first); err != nil || ok {
		t.Fatalf("stale ACK marked: %v %v", ok, err)
	}
	if ok, err := s.Retry(ctx, *first, "broker_unconfirmed", time.Second); err != nil || ok {
		t.Fatalf("stale retry wrote: %v %v", ok, err)
	}
	if ok, err := s.Quarantine(ctx, *first, "invalid_payload"); err != nil || ok {
		t.Fatalf("stale quarantine wrote: %v %v", ok, err)
	}
	if ok, err := s.Quarantine(ctx, *second, "invalid_payload"); err != nil || !ok {
		t.Fatalf("current quarantine failed: %v %v", ok, err)
	}
	listed, err := s.Quarantined(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].EventID != eid || listed[0].ErrorCode != "invalid_payload" {
		t.Fatalf("quarantine listing mismatch: count=%d", len(listed))
	}
	if ok, err := s.Requeue(ctx, eid); err != nil || !ok {
		t.Fatalf("requeue failed: %v %v", ok, err)
	}
	if ok, err := s.Requeue(ctx, eid); err != nil || ok {
		t.Fatalf("requeue accepted non-quarantined: %v %v", ok, err)
	}
	if ok, err := s.MarkPublished(ctx, *second); err != nil || ok {
		t.Fatalf("old claim after requeue marked: %v %v", ok, err)
	}
	if ok, err := s.Retry(ctx, *second, "broker_unconfirmed", time.Second); err != nil || ok {
		t.Fatalf("old claim after requeue retried: %v %v", ok, err)
	}
	if ok, err := s.Quarantine(ctx, *second, "invalid_payload"); err != nil || ok {
		t.Fatalf("old claim after requeue quarantined: %v %v", ok, err)
	}
	third, err := s.Claim(ctx, time.Second)
	if err != nil || third == nil || third.EventID != eid {
		t.Fatalf("post-requeue claim: found=%v err=%v", third != nil, err)
	}
	if ok, err := s.MarkPublished(ctx, *third); err != nil || !ok {
		t.Fatalf("current ACK failed: %v %v", ok, err)
	}
	if ok, err := s.Requeue(ctx, eid); err != nil || ok {
		t.Fatalf("published requeue accepted: %v %v", ok, err)
	}
	var version int64
	var payload string
	var published bool
	if err := pool.QueryRow(ctx, `SELECT aggregate_version,payload::text,published_at IS NOT NULL FROM outbox_events WHERE event_id=$1`, eid).Scan(&version, &payload, &published); err != nil {
		t.Fatal(err)
	}
	if version != 1 || payload != `{"state": "starting"}` || !published {
		t.Fatalf("immutable event changed: version=%d published=%v", version, published)
	}
	if broker := os.Getenv("WBB_TEST_KAFKA_BROKERS"); broker != "" {
		var kafkaEventID string
		if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
		 VALUES($1,gen_random_uuid(),2,'broadcast.session.ended','{"state":"ended"}') RETURNING event_id::text`, bid).Scan(&kafkaEventID); err != nil {
			t.Fatal(err)
		}
		producer, err := NewKafkaProducer(broker)
		if err != nil {
			t.Fatal(err)
		}
		defer producer.Close()
		if err := (&Dispatcher{Store: s, Producer: producer}).Step(ctx); err != nil {
			t.Fatalf("broker ACK: %v", err)
		}
		consumer, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(Topic), kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()))
		if err != nil {
			t.Fatal(err)
		}
		defer consumer.Close()
		pollCtx, cancelPoll := context.WithTimeout(ctx, 8*time.Second)
		defer cancelPoll()
		found := false
		for !found && pollCtx.Err() == nil {
			fetches := consumer.PollFetches(pollCtx)
			fetches.EachRecord(func(r *kgo.Record) {
				var envelope Event
				if json.Unmarshal(r.Value, &envelope) == nil && envelope.EventID == kafkaEventID {
					if string(r.Key) != bid || envelope.AggregateVersion != 2 || envelope.Type != "broadcast.session.ended" || string(envelope.Payload) != `{"state":"ended"}` {
						t.Errorf("broker record contract mismatch")
					}
					found = true
				}
			})
		}
		if !found {
			t.Fatal("broker record not consumed")
		}
		var acked bool
		if err := pool.QueryRow(ctx, `SELECT published_at IS NOT NULL FROM outbox_events WHERE event_id=$1`, kafkaEventID).Scan(&acked); err != nil || !acked {
			t.Fatalf("broker ACK not marked: %v", err)
		}
		var duplicateID string
		if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
		 VALUES($1,gen_random_uuid(),3,'broadcast.session.ended','{"state":"ended"}') RETURNING event_id::text`, bid).Scan(&duplicateID); err != nil {
			t.Fatal(err)
		}
		failMark := &failFirstMark{PostgresStore: s}
		if err := (&Dispatcher{Store: failMark, Producer: producer, Lease: time.Second}).Step(ctx); err == nil {
			t.Fatal("ACK-before-mark failure not surfaced")
		}
		time.Sleep(1100 * time.Millisecond)
		if err := (&Dispatcher{Store: s, Producer: producer, Lease: time.Second}).Step(ctx); err != nil {
			t.Fatalf("duplicate retry: %v", err)
		}
		seen := 0
		dupCtx, dupCancel := context.WithTimeout(ctx, 8*time.Second)
		defer dupCancel()
		for seen < 2 && dupCtx.Err() == nil {
			fetches := consumer.PollFetches(dupCtx)
			fetches.EachRecord(func(r *kgo.Record) {
				var e Event
				if json.Unmarshal(r.Value, &e) == nil && e.EventID == duplicateID {
					seen++
				}
			})
		}
		if seen != 2 {
			t.Fatalf("ACK-before-mark did not produce duplicate event_id: %d", seen)
		}
		var crashedID string
		if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
		 VALUES($1,gen_random_uuid(),4,'broadcast.session.ended','{"state":"ended"}') RETURNING event_id::text`, bid).Scan(&crashedID); err != nil {
			t.Fatal(err)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestOutboxPublisherChild$", "-test.v")
		child.Env = append(os.Environ(), "WBB_OUTBOX_CHILD=1", "WBB_OUTBOX_CHILD_DATABASE_URL="+u.String(), "WBB_TEST_KAFKA_BROKERS="+broker)
		pipe, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		ackSignal := make(chan bool, 1)
		go func() {
			scanner := bufio.NewScanner(pipe)
			for scanner.Scan() {
				if scanner.Text() == "ACKED" {
					ackSignal <- true
					return
				}
			}
			ackSignal <- false
		}()
		select {
		case ok := <-ackSignal:
			if !ok {
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatal("child did not ACK")
			}
		case <-time.After(10 * time.Second):
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("child ACK timeout")
		}
		if err := child.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
		time.Sleep(1100 * time.Millisecond)
		if err := (&Dispatcher{Store: s, Producer: producer, Lease: time.Second}).Step(ctx); err != nil {
			t.Fatalf("post-crash retry: %v", err)
		}
		seen = 0
		crashCtx, crashCancel := context.WithTimeout(ctx, 8*time.Second)
		defer crashCancel()
		for seen < 2 && crashCtx.Err() == nil {
			consumer.PollFetches(crashCtx).EachRecord(func(r *kgo.Record) {
				var e Event
				if json.Unmarshal(r.Value, &e) == nil && e.EventID == crashedID {
					seen++
				}
			})
		}
		if seen != 2 {
			t.Fatalf("process crash did not duplicate ACKed event_id: %d", seen)
		}
	}
	// Distinct publishers claiming at once must never share a current claim.
	for i := 0; i < 8; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
	 VALUES($1,gen_random_uuid(),$2,'broadcast.session.starting','{"state":"starting"}')`, bid, i+10); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := s.Claim(ctx, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			if e == nil {
				errs <- errors.New("missing claim")
				return
			}
			ids <- e.EventID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	unique := map[string]bool{}
	for id := range ids {
		if unique[id] {
			t.Fatal("duplicate concurrent claim")
		}
		unique[id] = true
	}
	if len(unique) != 8 {
		t.Fatalf("claimed %d of 8", len(unique))
	}
	var retryID string
	if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload)
	 VALUES($1,gen_random_uuid(),99,'broadcast.session.live','{"state":"live"}') RETURNING event_id::text`, bid).Scan(&retryID); err != nil {
		t.Fatal(err)
	}
	retryClaim, err := s.Claim(ctx, time.Second)
	if err != nil || retryClaim == nil || retryClaim.EventID != retryID {
		t.Fatalf("retry claim: %v", err)
	}
	if ok, err := s.Retry(ctx, *retryClaim, "broker_unconfirmed", 300*time.Millisecond); err != nil || !ok {
		t.Fatalf("retry schedule: %v %v", ok, err)
	}
	if found, err := s.Claim(ctx, time.Second); err != nil || found != nil {
		t.Fatalf("claimed before retry deadline: found=%v err=%v", found != nil, err)
	}
	time.Sleep(350 * time.Millisecond)
	reclaimed, err := s.Claim(ctx, time.Second)
	if err != nil || reclaimed == nil || reclaimed.EventID != retryID || reclaimed.ClaimToken == retryClaim.ClaimToken {
		t.Fatalf("retry reclaim: %v", err)
	}
	var oldQuarantineID string
	if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload,quarantined_at,last_error_code)
	 VALUES($1,gen_random_uuid(),200,'broadcast.session.live','{}',clock_timestamp()-interval '10 days','invalid_payload') RETURNING event_id::text`, bid).Scan(&oldQuarantineID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload,quarantined_at,last_error_code)
	 SELECT $1,gen_random_uuid(),200+g,'broadcast.session.live','{}',clock_timestamp(),'invalid_payload'
	 FROM generate_series(1,100) g`, bid); err != nil {
		t.Fatal(err)
	}
	recent, err := s.Quarantined(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 100 {
		t.Fatalf("recent quarantine count=%d", len(recent))
	}
	for _, item := range recent {
		if item.EventID == oldQuarantineID {
			t.Fatal("old item unexpectedly in recent page")
		}
	}
	old, err := s.QuarantinedByID(ctx, oldQuarantineID)
	if err != nil || old == nil || old.EventID != oldQuarantineID || old.ErrorCode != "invalid_payload" {
		t.Fatalf("old quarantine ID lookup: found=%v err=%v", old != nil, err)
	}
}

type failFirstMark struct {
	PostgresStore
	failed bool
}

func TestOutboxPublisherChild(t *testing.T) {
	if os.Getenv("WBB_OUTBOX_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, os.Getenv("WBB_OUTBOX_CHILD_DATABASE_URL"))
	if err != nil {
		t.Fatal("child database unavailable")
	}
	defer pool.Close()
	e, err := (PostgresStore{DB: pool}).Claim(ctx, time.Second)
	if err != nil || e == nil {
		t.Fatal("child claim failed")
	}
	producer, err := NewKafkaProducer(os.Getenv("WBB_TEST_KAFKA_BROKERS"))
	if err != nil {
		t.Fatal("child producer setup failed")
	}
	defer producer.Close()
	value, code := encode(*e, MaxRecordBytes)
	if code != "" {
		t.Fatal("child event invalid")
	}
	if err := producer.Publish(ctx, e.BroadcastID, value); err != nil {
		t.Fatal("child broker ACK failed")
	}
	fmt.Fprintln(os.Stdout, "ACKED")
	<-ctx.Done()
}

func (s *failFirstMark) MarkPublished(ctx context.Context, e Event) (bool, error) {
	if !s.failed {
		s.failed = true
		return false, errors.New("injected database mark failure")
	}
	return s.PostgresStore.MarkPublished(ctx, e)
}
