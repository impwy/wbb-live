package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/mediacompletion"
	"github.com/impwy/wbb-live/wbb-backend/internal/mediaworker"
)

func TestStopBeforeDispatchCommitSendsNoUpstreamByte(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBarrier := func() { releaseOnce.Do(func() { close(release) }) }
	completion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v3/wbb/lifecycle" {
			http.NotFound(w, r)
			return
		}
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"contract_version":"wbb-media-completion/1","root":"root-race","slot":"slot-race","epoch":"0123456789abcdef0123456789abcdef","epoch_sequence":1,"sequence":0}`)
	}))
	defer completion.Close()
	defer releaseBarrier()
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(201) }))
	defer upstream.Close()
	ts := newTestServer(t, func(c *config.Config) {
		c.MediaWHIPURL = upstream.URL
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
		c.MediaCompletionURL = completion.URL
		c.MediaCompletionRoot = "root-race"
		c.MediaCompletionSlot = "slot-race"
		c.MediaCompletionKey = []byte("test-only-completion-hmac-key-32-bytes")
	})
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "dispatch race")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	started := startSession(t, owner, base, access, "screen", "dispatch-race-0001")
	sid := started.body["session"].(map[string]any)["id"].(string)
	grant := started.body["publishGrant"].(map[string]any)["token"].(string)
	done := make(chan apiResp, 1)
	go func() {
		done <- postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, ts.URL, "application/sdp")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle was not reached")
	}
	stop := do(t, owner, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop=%d", stop.status)
	}
	releaseBarrier()
	select {
	case result := <-done:
		if result.status != 409 {
			t.Fatalf("late publish=%d", result.status)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("late publish did not finish")
	}
	if upstreamCalls.Load() != 0 {
		t.Fatal("upstream received a byte after stop")
	}
	var startedAt bool
	if err := testPool.QueryRow(t.Context(), `SELECT request_started_at IS NOT NULL FROM media_create_operations WHERE broadcast_session_id=$1`, sid).Scan(&startedAt); err != nil || startedAt {
		t.Fatalf("dispatch wrongly committed: started=%t err=%v", startedAt, err)
	}
	mtx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"itemCount":0,"pageCount":0,"items":[]}`)
	}))
	defer mtx.Close()
	completionClient, err := mediacompletion.NewClient(completion.URL, []byte("test-only-completion-hmac-key-32-bytes"), "root-race", "slot-race")
	if err != nil {
		t.Fatal(err)
	}
	mtxClient := mediaworker.NewMTXClient(mtx.URL, mtx.URL, "control", "test")
	mtxClient.WHIPBase = mtx.URL
	worker := &mediaworker.Worker{DB: testPool, MTX: mtxClient, Completion: completionClient,
		Secret: []byte("test-only-media-secret-at-least-32-bytes"), RTSPHost: "127.0.0.1:1", LockFile: filepath.Join(t.TempDir(), "worker.lock")}
	workerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	deadline := time.Now().Add(5 * time.Second)
	var state, evidence string
	for time.Now().Before(deadline) {
		if err := testPool.QueryRow(t.Context(), `SELECT s.state,COALESCE(o.completion_evidence_kind,'') FROM broadcast_sessions s JOIN media_create_operations o ON o.broadcast_session_id=s.id WHERE s.id=$1`, sid).Scan(&state, &evidence); err != nil {
			t.Fatal(err)
		}
		if state == "ended" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "ended" || evidence != "never_sent" {
		t.Fatalf("worker did not prove never-sent close: state=%s evidence=%s", state, evidence)
	}
	cancel()
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("worker did not stop")
	}
}
