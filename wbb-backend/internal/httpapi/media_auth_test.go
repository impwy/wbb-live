package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func TestMediaMTXCallbackBindsCreateCredentialToPathAndState(t *testing.T) {
	type credential struct{ user, pass, path string }
	seen := make(chan credential, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		seen <- credential{user, pass, strings.TrimSuffix(r.URL.Path, "/whip")}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", r.URL.Path+"/created")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("v=0\r\nanswer"))
	}))
	defer upstream.Close()
	ts := newTestServer(t, func(c *config.Config) {
		c.MediaWHIPURL = upstream.URL
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
	})
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "인증 callback")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	started := startSession(t, owner, base, access, "screen", "callback-auth-0001")
	sid := started.body["session"].(map[string]any)["id"].(string)
	grant := started.body["publishGrant"].(map[string]any)["token"].(string)
	created := postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, ts.URL, "application/sdp")
	if created.status != 201 {
		t.Fatalf("create=%d", created.status)
	}
	cred := <-seen
	check := func(action, path, user, pass string) int {
		return do(t, &http.Client{}, "POST", ts.URL+"/internal/mediamtx/auth", map[string]any{"user": user, "password": pass, "action": action, "path": path, "protocol": "webrtc"}, "").status
	}
	if got := check("publish", cred.path, cred.user, cred.pass); got != 200 {
		t.Fatalf("valid callback=%d", got)
	}
	if got := check("publish", cred.path+"-other", cred.user, cred.pass); got == 200 {
		t.Fatal("other path authorized")
	}
	if got := check("read", cred.path, cred.user, cred.pass); got == 200 {
		t.Fatal("read action authorized")
	}
	if got := check("publish", cred.path, "any", ""); got == 200 {
		t.Fatal("anonymous publish authorized")
	}
	stop := do(t, owner, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop=%d", stop.status)
	}
	if got := check("publish", cred.path, cred.user, cred.pass); got == 200 {
		t.Fatal("stopped publish authorized")
	}
}
