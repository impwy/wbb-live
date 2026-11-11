// This authorization callback belongs only to the isolated 07-A1 experiment.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type authRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Path     string `json:"path"`
}

type allowRequest struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
}

type fixture struct {
	mu      sync.Mutex
	key     []byte
	allowed map[string]string
}

func sign(key []byte, parts ...string) string {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func equal(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func pathParts(path string) (session, gen, kind string, ok bool) {
	p := strings.Split(path, "/")
	if len(p) != 4 || p[0] != "wbb" || len(p[1]) != 36 || p[2] == "" || (p[3] != "source" && p[3] != "720p") {
		return "", "", "", false
	}
	if _, err := strconv.ParseUint(p[2], 10, 64); err != nil {
		return "", "", "", false
	}
	return p[1], p[2], p[3], true
}

func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /fixture/allow", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req allowRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad input", 400)
			return
		}
		_, _, kind, ok := pathParts(req.Path)
		if !ok || kind != "source" || req.Operation == "" || !equal(r.Header.Get("X-WBB-Signature"), sign(f.key, "fixture-allow", req.Operation, req.Path)) {
			http.Error(w, "unauthorized", 401)
			return
		}
		f.mu.Lock()
		f.allowed[req.Operation] = req.Path
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /fixture/revoke", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req allowRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad input", 400)
			return
		}
		if !equal(r.Header.Get("X-WBB-Signature"), sign(f.key, "fixture-revoke", req.Operation, req.Path)) {
			http.Error(w, "unauthorized", 401)
			return
		}
		f.mu.Lock()
		if f.allowed[req.Operation] == req.Path {
			delete(f.allowed, req.Operation)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /internal/mediamtx/auth", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req authRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "unauthorized", 401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.authorize(req) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unauthorized", 401)
	})
	return mux
}

func (f *fixture) authorize(req authRequest) bool {
	if req.Action == "api" && req.Protocol == "" && req.Path == "" && req.User == "control" && equal(req.Password, sign(f.key, "control")) {
		return true
	}
	if req.Protocol == "webrtc" && req.Action == "publish" && strings.HasPrefix(req.User, "create.") {
		id := strings.TrimPrefix(req.User, "create.")
		return req.Path == f.allowed[id] && req.Path != "" && equal(req.Password, sign(f.key, "create", id))
	}
	session, gen, kind, valid := pathParts(req.Path)
	if !valid {
		return false
	}
	source := "wbb/" + session + "/" + gen + "/source"
	registered := false
	for _, p := range f.allowed {
		if p == source {
			registered = true
			break
		}
	}
	if !registered {
		return false
	}
	if req.Protocol == "hls" && req.Action == "read" && req.User == "" && req.Password == "" {
		return kind == "720p"
	}
	if req.Protocol != "rtsp" {
		return false
	}
	parts := strings.Split(req.User, ".")
	if len(parts) != 4 || parts[0] != "worker" || parts[1] != session || parts[2] != gen {
		return false
	}
	if _, err := strconv.ParseUint(parts[3], 10, 64); err != nil {
		return false
	}
	if !equal(req.Password, sign(f.key, "worker", session, gen, parts[3])) {
		return false
	}
	return (req.Action == "read" && kind == "source") || (req.Action == "publish" && kind == "720p")
}

func main() {
	key, err := hex.DecodeString(os.Getenv("WBB_FIXTURE_KEY_HEX"))
	if err != nil || len(key) < 32 {
		fmt.Fprintln(os.Stderr, "fixture key unavailable")
		os.Exit(1)
	}
	addr := os.Getenv("WBB_FIXTURE_ADDRESS")
	if addr == "" {
		fmt.Fprintln(os.Stderr, "fixture address unavailable")
		os.Exit(1)
	}
	f := &fixture{key: key, allowed: make(map[string]string)}
	s := &http.Server{Addr: addr, Handler: f.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	if err := s.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fixture server stopped:", err)
		os.Exit(1)
	}
}
