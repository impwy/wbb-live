package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func TestPlaybackAndEveryHLSObjectStopAtSessionEnd(t *testing.T) {
	secret := "test-only-media-secret-at-least-32-bytes"
	ts := newTestServer(t, func(c *config.Config) { c.MediaWHIPURL = "http://127.0.0.1:1"; c.MediaSecret = []byte(secret) })
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "익명 시청")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	started := startSession(t, owner, base, access, "screen", "playback-0001")
	sid := started.body["session"].(map[string]any)["id"].(string)
	if got := do(t, &http.Client{}, "GET", base+"/playback", nil, ""); got.status != 409 {
		t.Fatalf("starting playback=%d", got.status)
	}
	_, err := testPool.Exec(t.Context(), `UPDATE broadcast_sessions SET state='live',version=version+1 WHERE id=$1`, sid)
	if err != nil {
		t.Fatal(err)
	}
	got := do(t, &http.Client{}, "GET", base+"/playback", nil, "")
	if got.status != 200 || got.body["sessionId"] != sid {
		t.Fatalf("live playback=%d %v", got.status, got.body)
	}
	master := got.body["masterUrl"].(string)
	if master != "/media/wbb/"+sid+"/1/720p/index.m3u8" {
		t.Fatalf("master=%s", master)
	}
	for _, object := range []string{"index.m3u8", "stream.m3u8", "init.mp4", "part.mp4", "segment.mp4"} {
		uri := strings.TrimSuffix(master, "index.m3u8") + object + "?_HLS_msn=1"
		if status := hlsGate(t, ts.URL, secret, uri); status != 200 {
			t.Fatalf("%s gate=%d", object, status)
		}
	}
	if status := hlsGate(t, ts.URL, "", master); status == 200 {
		t.Fatal("spoofed direct gate authorized")
	}
	stop := do(t, owner, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop=%d", stop.status)
	}
	for _, object := range []string{"index.m3u8", "init.mp4", "part.mp4", "segment.mp4"} {
		uri := strings.TrimSuffix(master, "index.m3u8") + object
		if status := hlsGate(t, ts.URL, secret, uri); status == 200 {
			t.Fatalf("stopped %s authorized", object)
		}
	}
}

func hlsGate(t *testing.T, base, secret, originalURI string) int {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/internal/hls-authorize", nil)
	if err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		req.Header.Set("X-WBB-Media-Secret", secret)
	}
	req.Header.Set("X-WBB-Original-URI", originalURI)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
