package mediaworker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSnapshotReadsEveryPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "paths/list") && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`{"itemCount":2,"pageCount":2,"items":[{"name":"wbb/second","ready":true}]}`))
		case strings.Contains(r.URL.Path, "paths/list"):
			_, _ = w.Write([]byte(`{"itemCount":2,"pageCount":2,"items":[{"name":"wbb/first","ready":true}]}`))
		default:
			_, _ = w.Write([]byte(`{"itemCount":0,"pageCount":1,"items":[]}`))
		}
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "test", "test")
	s, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Paths["wbb/first"].Ready || !s.Paths["wbb/second"].Ready {
		t.Fatalf("incomplete paths: %+v", s.Paths)
	}
}

func TestSnapshotAcceptsMediaMTXEmptyPageCountZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"itemCount":0,"pageCount":0,"items":[]}`))
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "test", "test")
	s, err := m.Snapshot(context.Background())
	if err != nil || len(s.Paths) != 0 || len(s.Ingress) != 0 {
		t.Fatalf("empty snapshot=%+v err=%v", s, err)
	}
}

func TestPartReadyRequiresRealPartAndProgress(t *testing.T) {
	var mu sync.Mutex
	part := "part1.mp4"
	partAvailable := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "index.m3u8"):
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2000000\nvideo1_stream.m3u8?session=11111111-1111-4111-8111-111111111111\n"))
		case strings.HasSuffix(r.URL.Path, "video1_stream.m3u8"):
			if r.URL.Query().Get("session") != "11111111-1111-4111-8111-111111111111" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`#EXTM3U
#EXT-X-MAP:URI="init.mp4"
#EXT-X-PART:DURATION=0.2,URI="` + part + `"
`))
		case strings.HasSuffix(r.URL.Path, "init.mp4"):
			_, _ = w.Write([]byte("init"))
		case strings.HasSuffix(r.URL.Path, "part1.mp4") && !partAvailable:
			w.WriteHeader(404)
		default:
			_, _ = w.Write([]byte("part"))
		}
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "test", "test")
	ready, err := m.PartReady(context.Background(), "wbb/session/1/720p", "test", "test")
	if err != nil || ready {
		t.Fatalf("init 200 / actual part 404: ready=%v err=%v", ready, err)
	}
	mu.Lock()
	partAvailable = true
	mu.Unlock()
	ready, err = m.PartReady(context.Background(), "wbb/session/1/720p", "test", "test")
	if err != nil || ready {
		t.Fatalf("same part cannot establish freshness: ready=%v err=%v", ready, err)
	}
	mu.Lock()
	part = "part2.mp4"
	mu.Unlock()
	ready, err = m.PartReady(context.Background(), "wbb/session/1/720p", "test", "test")
	if err != nil || !ready {
		t.Fatalf("new real part should be ready: ready=%v err=%v", ready, err)
	}
}

func TestPartReadyDoesNotCountHLSSessionQueryAsMediaProgress(t *testing.T) {
	var mu sync.Mutex
	session := "11111111-1111-4111-8111-111111111111"
	part := "part1.mp4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "index.m3u8"):
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nvideo1_stream.m3u8?session=" + session + "\n"))
		case strings.HasSuffix(r.URL.Path, "video1_stream.m3u8"):
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:12\n#EXT-X-MAP:URI=\"init.mp4?session=" + session + "\"\n#EXT-X-PART:DURATION=0.2,URI=\"" + part + "?session=" + session + "\"\n"))
		default:
			_, _ = w.Write([]byte("media"))
		}
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "test", "test")
	probe := func(want bool) {
		t.Helper()
		ready, err := m.PartReady(context.Background(), "wbb/session/1/720p", "test", "test")
		if err != nil || ready != want {
			t.Fatalf("ready=%v want=%v err=%v", ready, want, err)
		}
	}
	probe(false)
	mu.Lock()
	session = "22222222-2222-4222-8222-222222222222"
	mu.Unlock()
	probe(false)
	mu.Lock()
	part = "part2.mp4"
	mu.Unlock()
	probe(true)
}

func TestPartReadyRejectsExternalAndOtherSessionReferences(t *testing.T) {
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
	}))
	defer external.Close()
	for _, variant := range []string{external.URL + "/steal", "../other/stream.m3u8", "/wbb/other/1/720p/stream.m3u8", "stream%2fm3u8"} {
		t.Run(variant, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n" + variant + "\n"))
			}))
			defer server.Close()
			m := NewMTXClient(server.URL, server.URL, "test", "test")
			ready, err := m.PartReady(context.Background(), "wbb/session/1/720p", "test", "test")
			if ready || err == nil {
				t.Fatalf("unsafe variant accepted: ready=%v err=%v", ready, err)
			}
		})
	}
	if externalCalls.Load() != 0 {
		t.Fatal("internal Basic credentials reached external origin")
	}
}

func TestDeleteWHIPUsesOriginalCredentialAndSessionPath(t *testing.T) {
	const source = "wbb/11111111-1111-4111-8111-111111111111/1/source"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		user, pass, _ := r.BasicAuth()
		if r.Method != http.MethodDelete || r.URL.Path != "/"+source+"/whip/resource" || user != "create.op" || pass != "test-pass" {
			t.Errorf("wrong WHIP cleanup request: %s %s user=%s", r.Method, r.URL.Path, user)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "control", "unused")
	m.WHIPBase = server.URL
	if err := m.DeleteWHIP(context.Background(), "/"+source+"/whip/resource", source, "create.op", "test-pass"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteWHIP(context.Background(), "/wbb/other/1/source/whip/resource", source, "create.op", "test-pass"); err == nil {
		t.Fatal("cross-session WHIP cleanup allowed")
	}
	if calls.Load() != 1 {
		t.Fatalf("WHIP cleanup calls=%d", calls.Load())
	}
}

func TestKickAcceptsAlreadyGoneSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()
	m := NewMTXClient(server.URL, server.URL, "control", "test")
	if err := m.Kick(context.Background(), "rtspsessions", "gone"); err != nil {
		t.Fatalf("already removed session: %v", err)
	}
}
