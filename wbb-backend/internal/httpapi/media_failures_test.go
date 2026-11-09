package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func TestWHIPRejectsMalformedClaimsAndRedisOutage(t *testing.T) {
	secret := []byte("fixed-media-test-secret-32-bytes-plus")
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(400) }))
	defer upstream.Close()
	mediaOpt := func(c *config.Config) {
		c.MediaWHIPURL = upstream.URL
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
	}
	ts := newTestServer(t, withFixedSecret(secret), mediaOpt)
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "잘못된 grant")
	started := startSession(t, owner, ts.URL+"/api/v1/broadcasts/"+b["id"].(string), access, "screen", "whip-claims-0001")
	sid := started.body["session"].(map[string]any)["id"].(string)
	grant := started.body["publishGrant"].(map[string]any)["token"].(string)
	parsed, err := jwt.ParseWithClaims(grant, jwt.MapClaims{}, func(*jwt.Token) (any, error) { return secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("issued grant invalid: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	sign := func(c jwt.MapClaims) string {
		t.Helper()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, tc := range []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{"missing issued at", func(c jwt.MapClaims) { delete(c, "iat") }},
		{"invalid broadcast uuid", func(c jwt.MapClaims) { c["bid"] = "not-a-uuid" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"other audience", func(c jwt.MapClaims) { c["aud"] = "wbb-api" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := jwt.MapClaims{}
			for k, v := range claims {
				copy[k] = v
			}
			tc.mutate(copy)
			got := postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", sign(copy), ts.URL, "application/sdp")
			if got.status != 401 {
				t.Fatalf("status=%d", got.status)
			}
		})
	}
	if reached.Load() != 0 {
		t.Fatalf("malformed grant reached upstream %d times", reached.Load())
	}
	bad := newTestServer(t, withFixedSecret(secret), withBadRedis(), mediaOpt)
	got := postWHIP(t, bad.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, bad.URL, "application/sdp")
	if got.status != 503 {
		t.Fatalf("Redis outage WHIP=%d", got.status)
	}
	public := do(t, &http.Client{}, "GET", bad.URL+"/api/v1/broadcasts/"+b["id"].(string)+"/sessions/current", nil, "")
	if public.status != 200 {
		t.Fatalf("Redis outage public current=%d", public.status)
	}
	control := postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, ts.URL, "application/sdp")
	if control.status != 502 || reached.Load() != 1 {
		t.Fatalf("valid grant did not reach upstream: status=%d calls=%d", control.status, reached.Load())
	}
}

func TestWHIPLateCreatedResourceIsNotExposedAfterStop(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	deleteSeen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			close(entered)
			<-release
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("Location", r.URL.Path+"/late")
			w.WriteHeader(201)
			_, _ = w.Write([]byte("v=0\r\nanswer"))
		case http.MethodDelete:
			deleteSeen <- r.URL.Path
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer upstream.Close()
	ts := newTestServer(t, func(c *config.Config) {
		c.MediaWHIPURL = upstream.URL
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
	})
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "늦은 201")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	started := startSession(t, owner, base, access, "screen", "whip-late-0001")
	sid := started.body["session"].(map[string]any)["id"].(string)
	grant := started.body["publishGrant"].(map[string]any)["token"].(string)
	done := make(chan apiResp, 1)
	go func() {
		done <- postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, ts.URL, "application/sdp")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not reached")
	}
	stop := do(t, owner, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop=%d", stop.status)
	}
	close(release)
	var result apiResp
	select {
	case result = <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("late POST did not finish")
	}
	if result.status == 201 {
		t.Fatal("late 201 exposed")
	}
	select {
	case path := <-deleteSeen:
		if !strings.HasSuffix(path, "/source/whip/late") {
			t.Fatalf("wrong cleanup path: %s", path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("late resource DELETE not attempted")
	}
	var state string
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM media_create_operations WHERE broadcast_session_id=$1`, sid).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cleanup_pending" {
		t.Fatalf("operation=%s", state)
	}
	if strings.Contains(result.header.Get("Location"), "late") {
		t.Fatal("late resource leaked")
	}
}

func TestWHIPLostResponseAndClientCancellationRemainUnresolved(t *testing.T) {
	for _, mode := range []string{"lost-response", "cancelled-client"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			bodyRead := make(chan error, 1)
			cancelObserved := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
			releaseTimer := time.AfterFunc(8*time.Second, releaseHandler)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.Copy(io.Discard, r.Body)
				bodyRead <- err
				if err != nil {
					return
				}
				close(entered)
				if mode == "lost-response" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				select {
				case <-r.Context().Done():
					close(cancelObserved)
				case <-release:
				}
			}))
			defer func() {
				releaseTimer.Stop()
				releaseHandler()
				upstream.Close()
			}()
			ts := newTestServer(t, func(c *config.Config) {
				c.MediaWHIPURL = upstream.URL
				c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
			})
			owner := newJarClient(t)
			_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
			b := createBroadcast(t, ts, access, "응답 유실")
			base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
			started := startSession(t, owner, base, access, "screen", "lost-"+mode+"-0001")
			sid := started.body["session"].(map[string]any)["id"].(string)
			grant := started.body["publishGrant"].(map[string]any)["token"].(string)
			endpoint := ts.URL + "/api/v1/live-sessions/" + sid + "/whip"
			if mode == "lost-response" {
				if got := postWHIP(t, endpoint, grant, ts.URL, "application/sdp"); got.status != 504 {
					t.Fatalf("lost response status=%d", got.status)
				}
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("v=0\r\n"))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+grant)
				req.Header.Set("Origin", ts.URL)
				req.Header.Set("Content-Type", "application/sdp")
				result := make(chan error, 1)
				go func() {
					resp, err := (&http.Client{}).Do(req)
					if resp != nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
					result <- err
				}()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("upstream not reached")
				}
				if err := <-bodyRead; err != nil {
					t.Fatalf("upstream request body: %v", err)
				}
				cancel()
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("cancelled client unexpectedly succeeded")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled client hung")
				}
				select {
				case <-cancelObserved:
				case <-time.After(3 * time.Second):
					t.Fatal("upstream request cancellation not observed")
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				var state string
				var path *string
				err := testPool.QueryRow(context.Background(), `SELECT state,upstream_resource_path FROM media_create_operations WHERE broadcast_session_id=$1`, sid).Scan(&state, &path)
				if err == nil && state == "cleanup_pending" && path == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("operation not retained: state=%s path=%v err=%v", state, path, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			stop := do(t, owner, "POST", base+"/sessions/"+sid+"/stop", nil, access)
			if stop.status != 202 {
				t.Fatalf("stop=%d", stop.status)
			}
			var sessionState string
			if err := testPool.QueryRow(context.Background(), `SELECT state FROM broadcast_sessions WHERE id=$1`, sid).Scan(&sessionState); err != nil || sessionState != "stopping" {
				t.Fatalf("unknown POST should keep STOPPING: state=%s err=%v", sessionState, err)
			}
		})
	}
}
