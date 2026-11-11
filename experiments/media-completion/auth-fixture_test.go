package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthFixtureExactProtocolActionPath(t *testing.T) {
	key := bytes.Repeat([]byte{0x41}, 32)
	f := &fixture{key: key, allowed: map[string]string{"op-a": "wbb/12345678-1234-1234-1234-123456789abc/1/source"}}
	call := func(a authRequest) int {
		b, _ := json.Marshal(a)
		r := httptest.NewRequest(http.MethodPost, "/internal/mediamtx/auth", bytes.NewReader(b))
		w := httptest.NewRecorder()
		f.handler().ServeHTTP(w, r)
		return w.Code
	}
	session := "12345678-1234-1234-1234-123456789abc"
	source := "wbb/" + session + "/1/source"
	output := "wbb/" + session + "/1/720p"
	worker := authRequest{User: "worker." + session + ".1.7", Password: sign(key, "worker", session, "1", "7"), Action: "read", Protocol: "rtsp", Path: source}
	if got := call(worker); got != 204 {
		t.Fatalf("source read %d", got)
	}
	worker.Action = "publish"
	if got := call(worker); got != 401 {
		t.Fatalf("worker published source %d", got)
	}
	worker.Path = output
	if got := call(worker); got != 204 {
		t.Fatalf("output publish %d", got)
	}
	worker.Protocol = "webrtc"
	if got := call(worker); got != 401 {
		t.Fatalf("worker published WHIP output %d", got)
	}
	worker.Protocol = "rtsp"
	worker.Path = "wbb/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/1/720p"
	if got := call(worker); got != 401 {
		t.Fatalf("worker published another session %d", got)
	}
	worker.Path = "wbb/" + session + "/2/720p"
	if got := call(worker); got != 401 {
		t.Fatalf("worker published another generation %d", got)
	}
	create := authRequest{User: "create.op-a", Password: sign(key, "create", "op-a"), Action: "publish", Protocol: "webrtc", Path: source}
	if got := call(create); got != 204 {
		t.Fatalf("create WHIP source %d", got)
	}
	create.Path = output
	if got := call(create); got != 401 {
		t.Fatalf("create WHIP output %d", got)
	}
	create.Path = source
	create.Protocol = "rtsp"
	if got := call(create); got != 401 {
		t.Fatalf("create RTSP source %d", got)
	}
}
