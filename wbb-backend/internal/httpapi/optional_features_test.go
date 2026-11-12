package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/donations"
)

// COMMENT-01, COMMENT-02: signed-in users write, anyone reads, and only the
// author can edit or delete (checked by the server, not the UI).
func TestCommentsCRUDAndAuthorAuthorization(t *testing.T) {
	ts := newTestServer(t)
	authorClient, otherClient, anon := newJarClient(t), newJarClient(t), newJarClient(t)
	_, author, _ := registerAndLogin(t, ts, authorClient, uniqueEmail())
	_, other, _ := registerAndLogin(t, ts, otherClient, uniqueEmail())
	b := createBroadcast(t, ts, author, "댓글 방송")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string) + "/comments"

	if r := do(t, anon, "POST", base, map[string]string{"body": "익명"}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous create: %d", r.status)
	}
	if r := do(t, authorClient, "POST", base, map[string]string{"body": "   "}, author); r.status != http.StatusBadRequest {
		t.Fatalf("blank body: %d", r.status)
	}
	created := do(t, authorClient, "POST", base, map[string]string{"body": "첫 댓글"}, author)
	if created.status != http.StatusCreated {
		t.Fatalf("create: %d %v", created.status, created.body)
	}
	comment := created.body["comment"].(map[string]any)
	cid := comment["id"].(string)
	if comment["author"].(map[string]any)["id"] == "" || comment["body"] != "첫 댓글" {
		t.Fatalf("created comment: %v", comment)
	}
	list := do(t, anon, "GET", base, nil, "")
	if list.status != 200 || len(list.body["items"].([]any)) != 1 {
		t.Fatalf("anonymous list: %d %v", list.status, list.body)
	}

	one := ts.URL + "/api/v1/comments/" + cid
	if r := do(t, otherClient, "PATCH", one, map[string]string{"body": "남의 수정"}, other); r.status != http.StatusForbidden {
		t.Fatalf("other user edit: %d", r.status)
	}
	if r := do(t, otherClient, "DELETE", one, nil, other); r.status != http.StatusForbidden {
		t.Fatalf("other user delete: %d", r.status)
	}
	if r := do(t, anon, "PATCH", one, map[string]string{"body": "x"}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous edit: %d", r.status)
	}
	edited := do(t, authorClient, "PATCH", one, map[string]string{"body": "수정한 댓글"}, author)
	if edited.status != 200 || edited.body["comment"].(map[string]any)["body"] != "수정한 댓글" || edited.body["comment"].(map[string]any)["edited"] != true {
		t.Fatalf("author edit: %d %v", edited.status, edited.body)
	}
	if r := do(t, authorClient, "DELETE", one, nil, author); r.status != http.StatusNoContent {
		t.Fatalf("author delete: %d", r.status)
	}
	if r := do(t, anon, "GET", base, nil, ""); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("deleted comment still listed: %v", r.body)
	}
	if r := do(t, authorClient, "PATCH", one, map[string]string{"body": "x"}, author); r.status != http.StatusNotFound {
		t.Fatalf("edit after delete: %d", r.status)
	}
}

// DONATION-01..03: the server fixes target and amount in the order, verifies
// the gateway approval against it, credits once despite repeated approval
// notices, and reports per-broadcast history and total.
func TestDonationsVerifyApprovalAndCreditOnce(t *testing.T) {
	ts := newTestServer(t)
	ownerClient, donorClient, otherClient, anon := newJarClient(t), newJarClient(t), newJarClient(t), newJarClient(t)
	owner, ownerAccess, _ := registerAndLogin(t, ts, ownerClient, uniqueEmail())
	_, donor, _ := registerAndLogin(t, ts, donorClient, uniqueEmail())
	_, other, _ := registerAndLogin(t, ts, otherClient, uniqueEmail())
	b := createBroadcast(t, ts, ownerAccess, "후원 방송")
	bid := b["id"].(string)
	api := ts.URL + "/api/v1"

	if r := do(t, anon, "GET", api+"/payments/config", nil, ""); r.body["enabled"] != true {
		t.Fatalf("payments config: %v", r.body)
	}
	if r := do(t, anon, "POST", api+"/broadcasts/"+bid+"/donations", map[string]any{"amount": 5000}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous order: %d", r.status)
	}
	if r := do(t, donorClient, "POST", api+"/broadcasts/"+bid+"/donations", map[string]any{"amount": 500}, donor); r.status != http.StatusBadRequest {
		t.Fatalf("below minimum: %d", r.status)
	}
	created := do(t, donorClient, "POST", api+"/broadcasts/"+bid+"/donations", map[string]any{"amount": 5000, "message": "응원해요"}, donor)
	if created.status != http.StatusCreated {
		t.Fatalf("order: %d %v", created.status, created.body)
	}
	order := created.body["order"].(map[string]any)
	orderID := order["orderId"].(string)
	if order["amount"].(float64) != 5000 || order["recipient"] != owner["displayName"] {
		t.Fatalf("order target/amount: %v (owner %v)", order, owner["displayName"])
	}

	// Only the donor may pay or confirm this order.
	if r := do(t, otherClient, "POST", api+"/payments/test-checkout", map[string]any{"orderId": orderID, "approve": true}, other); r.status != http.StatusForbidden {
		t.Fatalf("other user checkout: %d", r.status)
	}
	checkout := do(t, donorClient, "POST", api+"/payments/test-checkout", map[string]any{"orderId": orderID, "approve": true}, donor)
	if checkout.status != 200 || checkout.body["amount"].(float64) != 5000 {
		t.Fatalf("checkout: %d %v", checkout.status, checkout.body)
	}
	key := checkout.body["paymentKey"].(string)

	confirm := func(c *http.Client, token string, amount int, paymentKey string) apiResp {
		return do(t, c, "POST", api+"/donations/confirm", map[string]any{"paymentKey": paymentKey, "orderId": orderID, "amount": amount}, token)
	}
	if r := confirm(donorClient, donor, 100, key); r.status != http.StatusConflict {
		t.Fatalf("forged amount accepted: %d", r.status)
	}
	if r := confirm(otherClient, other, 5000, key); r.status != http.StatusForbidden {
		t.Fatalf("other user confirm: %d", r.status)
	}
	if r := confirm(donorClient, donor, 5000, "test_pk_forged"); r.status == http.StatusOK {
		t.Fatal("unknown payment key confirmed")
	}
	first := confirm(donorClient, donor, 5000, key)
	if first.status != 200 || first.body["credited"] != true {
		t.Fatalf("first confirm: %d %v", first.status, first.body)
	}
	// Repeated approval notices: confirm again and the gateway webhook.
	if r := confirm(donorClient, donor, 5000, key); r.status != 200 || r.body["credited"] != false {
		t.Fatalf("repeated confirm: %d %v", r.status, r.body)
	}
	if r := do(t, anon, "POST", api+"/payments/webhook", map[string]any{"data": map[string]any{"paymentKey": key}}, ""); r.status != 200 || r.body["credited"] != false {
		t.Fatalf("repeated webhook: %d %v", r.status, r.body)
	}

	// A declined payment fails its order and is never credited.
	declined := do(t, donorClient, "POST", api+"/broadcasts/"+bid+"/donations", map[string]any{"amount": 3000}, donor).body["order"].(map[string]any)
	if r := do(t, donorClient, "POST", api+"/payments/test-checkout", map[string]any{"orderId": declined["orderId"], "approve": false}, donor); r.status != http.StatusPaymentRequired {
		t.Fatalf("declined checkout: %d", r.status)
	}

	summary := do(t, anon, "GET", api+"/broadcasts/"+bid+"/donations", nil, "")
	if summary.status != 200 || summary.body["count"].(float64) != 1 || summary.body["total"].(float64) != 5000 {
		t.Fatalf("summary: %d %v", summary.status, summary.body)
	}
	item := summary.body["items"].([]any)[0].(map[string]any)
	if item["amount"].(float64) != 5000 || item["message"] != "응원해요" {
		t.Fatalf("history item: %v", item)
	}
	var stored int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM donations WHERE broadcast_id=$1 AND status='confirmed' AND recipient_id::text=$2`, bid, owner["id"]).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored confirmed donations to owner: %d %v", stored, err)
	}
}

// VOD-01..03: a recording of an ended session is registered with its
// location, size, start and duration, survives a server restart, and is
// listed, detailed and served with Range support.
func TestRecordingsRegisterListAndServe(t *testing.T) {
	dir := t.TempDir()
	withRecordings := func(c *config.Config) { c.RecordingsDir = dir }
	ts := newTestServer(t, withRecordings)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "녹화 방송")
	bid := b["id"].(string)
	base := ts.URL + "/api/v1/broadcasts/" + bid

	start := startSession(t, client, base, access, "camera", "record-session-0001")
	if start.status != http.StatusAccepted {
		t.Fatalf("start: %d %v", start.status, start.body)
	}
	sid := start.body["session"].(map[string]any)["id"].(string)

	// The transcoder writes <root>/wbb/<session>/<generation>/720p/<UTC start>.mp4.
	started := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	file := filepath.Join(dir, "wbb", sid, "1", "720p", started.Format("2006-01-02_15-04-05")+"-000000.mp4")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("fmp4-bytes-", 400))
	if err := os.WriteFile(file, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// While the session is not ended the file is not registered yet.
	if err := ts.api.ScanRecordings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := do(t, client, "GET", base+"/recordings", nil, ""); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("recording of an active session listed: %v", r.body)
	}
	stop := do(t, client, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != http.StatusAccepted && stop.status != http.StatusOK {
		t.Fatalf("stop: %d", stop.status)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if do(t, client, "GET", base+"/sessions/current", nil, "").body["session"].(map[string]any)["state"] == "ended" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := ts.api.ScanRecordings(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Stored location and metadata (VOD-03).
	var rel string
	var size int64
	var startedAt time.Time
	if err := testPool.QueryRow(t.Context(), `SELECT file_path,size_bytes,started_at FROM recordings WHERE session_id=$1`, sid).Scan(&rel, &size, &startedAt); err != nil {
		t.Fatalf("recording row: %v", err)
	}
	if rel != filepath.ToSlash(strings.TrimPrefix(file, dir+string(filepath.Separator))) || size != int64(len(payload)) || !startedAt.Equal(started) {
		t.Fatalf("stored recording: %s %d %s", rel, size, startedAt)
	}

	// A restarted server (same DB, new process state) still lists it.
	restarted := newTestServer(t, withRecordings)
	anon := newJarClient(t)
	list := do(t, anon, "GET", restarted.URL+"/api/v1/recordings", nil, "")
	var rid string
	for _, it := range list.body["items"].([]any) {
		if r := it.(map[string]any); r["sessionId"] == sid {
			rid = r["id"].(string)
			if r["broadcastTitle"] != "녹화 방송" || r["sizeBytes"].(float64) != float64(len(payload)) {
				t.Fatalf("listed recording: %v", r)
			}
		}
	}
	if rid == "" {
		t.Fatalf("recording missing after restart: %v", list.body)
	}
	if r := do(t, anon, "GET", restarted.URL+"/api/v1/recordings/"+rid, nil, ""); r.status != 200 {
		t.Fatalf("detail: %d", r.status)
	}
	req, _ := http.NewRequest("GET", restarted.URL+"/api/v1/recordings/"+rid+"/media", nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Type") != "video/mp4" || string(body) != string(payload[:10]) {
		t.Fatalf("media range: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if r := do(t, anon, "GET", restarted.URL+"/api/v1/recordings/00000000-0000-0000-0000-000000000000/media", nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("unknown recording: %d", r.status)
	}
}

// Live chat: anyone reads the SSE stream; signed-in users send.
func TestLiveChatStreamAndSend(t *testing.T) {
	ts := newTestServer(t)
	client, anon := newJarClient(t), newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "채팅 방송")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string) + "/chat"

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if r := do(t, anon, "POST", base, map[string]string{"text": "익명"}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous send: %d", r.status)
	}
	if r := do(t, client, "POST", base, map[string]string{"text": "안녕하세요"}, access); r.status != http.StatusCreated {
		t.Fatalf("send: %d %v", r.status, r.body)
	}
	if r := do(t, client, "POST", base, map[string]string{"text": "도배"}, access); r.status != http.StatusTooManyRequests {
		t.Fatalf("flood not limited: %d", r.status)
	}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before message: %v", err)
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "안녕하세요") {
			return
		}
	}
}

// DONATION-02/03 with the Toss Payments provider: a fake Toss API stands in
// for api.tosspayments.com. The server sends the stored order to Toss and
// credits only when Toss reports DONE with the same order and amount.
func TestDonationsWithTossProvider(t *testing.T) {
	var confirms int
	var tamper bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); !ok || user != "test_sk_fake" {
			http.Error(w, `{"code":"UNAUTHORIZED_KEY"}`, http.StatusUnauthorized)
			return
		}
		var req struct {
			PaymentKey string `json:"paymentKey"`
			OrderID    string `json:"orderId"`
			Amount     int    `json:"amount"`
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payments/confirm":
			_ = json.NewDecoder(r.Body).Decode(&req)
			confirms++
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payments/"):
			req.PaymentKey = strings.TrimPrefix(r.URL.Path, "/v1/payments/")
			req.OrderID, req.Amount = lastOrder, lastAmount
		default:
			http.NotFound(w, r)
			return
		}
		lastOrder, lastAmount = req.OrderID, req.Amount
		amount := req.Amount
		if tamper {
			amount++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"paymentKey": req.PaymentKey, "orderId": req.OrderID, "status": "DONE", "totalAmount": amount})
	}))
	t.Cleanup(fake.Close)

	ts := newTestServer(t)
	ts.api.SetPaymentProvider(&donations.Toss{SecretKey: "test_sk_fake", BaseURL: fake.URL, Client: fake.Client()})
	ownerClient, donorClient := newJarClient(t), newJarClient(t)
	_, ownerAccess, _ := registerAndLogin(t, ts, ownerClient, uniqueEmail())
	_, donor, _ := registerAndLogin(t, ts, donorClient, uniqueEmail())
	bid := createBroadcast(t, ts, ownerAccess, "토스 후원")["id"].(string)
	api := ts.URL + "/api/v1"
	order := func(amount int) string {
		r := do(t, donorClient, "POST", api+"/broadcasts/"+bid+"/donations", map[string]any{"amount": amount}, donor)
		if r.status != http.StatusCreated {
			t.Fatalf("order: %d %v", r.status, r.body)
		}
		return r.body["order"].(map[string]any)["orderId"].(string)
	}
	confirm := func(orderID, key string, amount int) apiResp {
		return do(t, donorClient, "POST", api+"/donations/confirm", map[string]any{"paymentKey": key, "orderId": orderID, "amount": amount}, donor)
	}

	first := order(10000)
	if r := confirm(first, "toss_pk_1", 10000); r.status != 200 || r.body["credited"] != true {
		t.Fatalf("toss confirm: %d %v", r.status, r.body)
	}
	if r := confirm(first, "toss_pk_1", 10000); r.body["credited"] != false || confirms != 1 {
		t.Fatalf("repeat confirm reached Toss again or credited: %v confirms=%d", r.body, confirms)
	}
	if r := do(t, newJarClient(t), "POST", api+"/payments/webhook", map[string]any{"data": map[string]any{"paymentKey": "toss_pk_1"}}, ""); r.body["credited"] != false {
		t.Fatalf("webhook re-credited: %v", r.body)
	}
	// Toss reporting a different amount than the order is never credited.
	tamper = true
	second := order(20000)
	if r := confirm(second, "toss_pk_2", 20000); r.status != http.StatusConflict {
		t.Fatalf("tampered Toss amount accepted: %d %v", r.status, r.body)
	}
	if r := do(t, newJarClient(t), "GET", api+"/broadcasts/"+bid+"/donations", nil, ""); r.body["count"].(float64) != 1 || r.body["total"].(float64) != 10000 {
		t.Fatalf("toss summary: %v", r.body)
	}
}

var (
	lastOrder  string
	lastAmount int
)
