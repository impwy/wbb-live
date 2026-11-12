package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func TestMediaReadinessRejectsWrongCompletionAnchor(t *testing.T) {
	completion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contract_version":"wbb-media-completion/1","root":"foreign-root","slot":"slot","epoch":"0123456789abcdef0123456789abcdef","epoch_sequence":1,"sequence":0}`))
	}))
	defer completion.Close()
	ts := newTestServer(t, func(c *config.Config) {
		c.MediaWHIPURL = "http://127.0.0.1:1"
		c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
		c.MediaCompletionURL = completion.URL
		c.MediaCompletionRoot = "expected-root"
		c.MediaCompletionSlot = "slot"
		c.MediaCompletionKey = []byte("test-only-completion-hmac-key-32-bytes")
	})
	got := do(t, &http.Client{}, "GET", ts.URL+"/api/v1/health/ready", nil, "")
	if got.status != 503 {
		t.Fatalf("wrong completion anchor readiness=%d", got.status)
	}
}

func TestMediaReadinessRejectsInvalidLifecycleSequence(t *testing.T) {
	for _, sequence := range []string{`"1"`, "-1", "1.5", "18446744073709551616"} {
		t.Run(sequence, func(t *testing.T) {
			completion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"contract_version":"wbb-media-completion/1","root":"expected-root","slot":"slot","epoch":"0123456789abcdef0123456789abcdef","epoch_sequence":1,"sequence":` + sequence + `}`))
			}))
			defer completion.Close()
			ts := newTestServer(t, func(c *config.Config) {
				c.MediaWHIPURL = "http://127.0.0.1:1"
				c.MediaSecret = []byte("test-only-media-secret-at-least-32-bytes")
				c.MediaCompletionURL = completion.URL
				c.MediaCompletionRoot = "expected-root"
				c.MediaCompletionSlot = "slot"
				c.MediaCompletionKey = []byte("test-only-completion-hmac-key-32-bytes")
			})
			got := do(t, &http.Client{}, "GET", ts.URL+"/api/v1/health/ready", nil, "")
			if got.status != http.StatusServiceUnavailable {
				t.Fatalf("invalid lifecycle sequence readiness=%d", got.status)
			}
		})
	}
}
