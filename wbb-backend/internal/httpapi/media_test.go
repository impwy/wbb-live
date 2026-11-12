package httpapi_test

import (
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func postWHIP(t *testing.T, endpoint, token, origin, contentType string) apiResp {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader("v=0\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return apiResp{status: resp.StatusCode, header: resp.Header}
}

func TestWHIPCreateReservesAndReturnsOpaqueResource(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/source/whip") || r.Header.Get("Content-Type") != "application/sdp" {
			t.Errorf("bad upstream request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", r.URL.Path+"/media-resource")
		w.Header().Set("ETag", `"ice-1"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\r\nanswer"))
	}))
	defer upstream.Close()
	ts := newTestServer(t, func(c *config.Config) {
		c.MediaWHIPURL = upstream.URL
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
	})
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "미디어 생성")
	started := startSession(t, owner, ts.URL+"/api/v1/broadcasts/"+b["id"].(string), access, "screen", "whip-create-0001")
	if started.status != http.StatusAccepted {
		t.Fatalf("prepare: %d", started.status)
	}
	sid := started.body["session"].(map[string]any)["id"].(string)
	grant := started.body["publishGrant"].(map[string]any)["token"].(string)
	endpoint := ts.URL + "/api/v1/live-sessions/" + sid + "/whip"
	first := postWHIP(t, endpoint, grant, ts.URL, "application/sdp")
	if first.status != http.StatusCreated || first.header.Get("Content-Type") != "application/sdp" || !strings.HasPrefix(first.header.Get("Location"), "/api/v1/live-sessions/"+sid+"/whip/") || first.header.Get("ETag") != `"ice-1"` {
		t.Fatalf("create: status=%d headers=%v", first.status, first.header)
	}
	second := postWHIP(t, endpoint, grant, ts.URL, "application/sdp")
	if second.status != http.StatusConflict || upstreamCalls.Load() != 1 {
		t.Fatalf("reused grant: status=%d upstream=%d", second.status, upstreamCalls.Load())
	}
	// The public response must not leak the internal endpoint or MediaMTX id.
	if strings.Contains(first.header.Get("Location"), "media-resource") || strings.Contains(first.header.Get("Location"), upstream.URL) {
		t.Fatal("upstream resource leaked")
	}
}

func TestWHIPResourceRequiresOriginalFamilyAfterRefresh(t *testing.T) {
	for _, deleteStatus := range []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound} {
		t.Run(http.StatusText(deleteStatus), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					w.Header().Set("Content-Type", "application/sdp")
					w.Header().Set("Location", r.URL.Path+"/media-resource")
					w.Header().Set("ETag", `"ice-1"`)
					w.WriteHeader(201)
					_, _ = w.Write([]byte("v=0\r\nanswer"))
				case http.MethodPatch:
					if r.Header.Get("If-Match") != `"ice-1"` {
						t.Errorf("lost ETag: %s", r.Header.Get("If-Match"))
					}
					w.WriteHeader(204)
				case http.MethodDelete:
					w.WriteHeader(deleteStatus)
				}
			}))
			defer upstream.Close()
			ts := newTestServer(t, func(c *config.Config) {
				c.MediaWHIPURL = upstream.URL
				c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
			})
			owner := newJarClient(t)
			email := uniqueEmail()
			_, access, refresh := registerAndLogin(t, ts, owner, email)
			otherLogin := newJarClient(t)
			_, otherAccess, _ := loginUser(t, ts, otherLogin, email)
			b := createBroadcast(t, ts, access, "리소스 인가")
			base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
			started := startSession(t, owner, base, access, "screen", "resource-auth-0001")
			sid := started.body["session"].(map[string]any)["id"].(string)
			grant := started.body["publishGrant"].(map[string]any)["token"].(string)
			created := postWHIP(t, ts.URL+"/api/v1/live-sessions/"+sid+"/whip", grant, ts.URL, "application/sdp")
			if created.status != 201 {
				t.Fatalf("create %d", created.status)
			}
			resourceURL := ts.URL + created.header.Get("Location")
			if got := sendWHIPResource(t, http.MethodPatch, resourceURL, otherAccess, ts.URL, `"ice-1"`); got != 403 {
				t.Fatalf("other login PATCH=%d", got)
			}
			if got := sendWHIPResource(t, http.MethodDelete, resourceURL, otherAccess, ts.URL, ""); got != 403 {
				t.Fatalf("other login DELETE=%d", got)
			}
			rotated := refreshWithToken(t, ts, refresh)
			if rotated.status != 200 {
				t.Fatalf("refresh=%d", rotated.status)
			}
			refreshedAccess := rotated.body["accessToken"].(string)
			if got := sendWHIPResource(t, http.MethodPatch, resourceURL, refreshedAccess, ts.URL, `"ice-1"`); got != 204 {
				t.Fatalf("original PATCH=%d", got)
			}
			if got := sendWHIPResource(t, http.MethodDelete, resourceURL, refreshedAccess, ts.URL, ""); got != 202 {
				t.Fatalf("original DELETE=%d", got)
			}
			var state string
			if err := testPool.QueryRow(t.Context(), `SELECT state FROM media_create_operations WHERE broadcast_session_id=$1`, sid).Scan(&state); err != nil || state == "closed" {
				t.Fatalf("DELETE acceptance prematurely closed operation: state=%s err=%v", state, err)
			}
		})
	}
}

func sendWHIPResource(t *testing.T, method, endpoint, access, origin, etag string) int {
	t.Helper()
	var body *strings.Reader
	if method == http.MethodPatch {
		body = strings.NewReader("a=ice-ufrag:test\r\n")
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Origin", origin)
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
		req.Header.Set("If-Match", etag)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestWHIPRejectsUnauthorisedPublication(t *testing.T) {
	ts := newTestServer(t)
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "미디어 인가")
	started := startSession(t, owner, ts.URL+"/api/v1/broadcasts/"+b["id"].(string), access, "screen", "whip-auth-0001")
	if started.status != http.StatusAccepted {
		t.Fatalf("prepare: %d", started.status)
	}
	sid := started.body["session"].(map[string]any)["id"].(string)
	endpoint := ts.URL + "/api/v1/live-sessions/" + sid + "/whip"
	for _, tc := range []struct {
		name, token, origin string
		want                int
	}{
		{"missing grant", "", ts.URL, http.StatusUnauthorized},
		{"access token is not a publish grant", access, ts.URL, http.StatusUnauthorized},
		{"missing origin", started.body["publishGrant"].(map[string]any)["token"].(string), "", http.StatusForbidden},
		{"wrong origin", started.body["publishGrant"].(map[string]any)["token"].(string), "https://wrong.example", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := postWHIP(t, endpoint, tc.token, tc.origin, "application/sdp")
			if got.status != tc.want {
				t.Fatalf("status=%d want=%d", got.status, tc.want)
			}
		})
	}
}
