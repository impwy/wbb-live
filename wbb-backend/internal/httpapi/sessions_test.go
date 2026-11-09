package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func startSession(t *testing.T, client *http.Client, base, access, source, key string) apiResp {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"source": source})
	req, err := http.NewRequest("POST", base+"/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", originOf(base))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Idempotency-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func TestWrongFamilyRenewDoesNotBlockLogoutOutbox(t *testing.T) {
	ts := newTestServer(t)
	owner := newJarClient(t)
	email := uniqueEmail()
	_, ownerAccess, _ := registerAndLogin(t, ts, owner, email)
	otherLogin := newJarClient(t)
	_, otherAccess, _ := loginUser(t, ts, otherLogin, email)
	b := createBroadcast(t, ts, ownerAccess, "로그아웃 갱신 경합")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	started := startSession(t, owner, base, ownerAccess, "screen", "family-race-0001")
	if started.status != 202 {
		t.Fatalf("start: %d", started.status)
	}
	sid := started.body["session"].(map[string]any)["id"].(string)

	// Pause a real logout immediately before the outbox FK checks the
	// broadcast row. At this point logout holds the original session lock.
	_, err := testPool.Exec(t.Context(), `CREATE FUNCTION wbb_test_pause_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.type = 'broadcast.session.stopping' THEN PERFORM pg_sleep(2); END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testPool.Exec(t.Context(), `CREATE TRIGGER wbb_test_pause_outbox BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION wbb_test_pause_outbox()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DROP TRIGGER IF EXISTS wbb_test_pause_outbox ON outbox_events`)
		_, _ = testPool.Exec(context.Background(), `DROP FUNCTION IF EXISTS wbb_test_pause_outbox()`)
	})

	logoutCh := make(chan apiResp, 1)
	go func() { logoutCh <- do(t, owner, "POST", ts.URL+"/api/v1/auth/logout", nil, ownerAccess) }()
	deadline := time.Now().Add(4 * time.Second)
	paused := false
	for time.Now().Before(deadline) {
		err = testPool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep' AND query LIKE '%outbox_events%')`).Scan(&paused)
		if err != nil {
			t.Fatal(err)
		}
		if paused {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !paused {
		t.Fatal("logout did not reach outbox pause")
	}
	renewCh := make(chan apiResp, 1)
	go func() {
		renewCh <- do(t, otherLogin, "POST", base+"/sessions/"+sid+"/publish-grants", nil, otherAccess)
	}()

	// Before the fix a wrong-family request holds broadcasts FOR UPDATE while
	// waiting for the original session; the outbox FK then forms a deadlock.
	locked := false
	probeDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(probeDeadline) {
		var id string
		err := testPool.QueryRow(t.Context(), `SELECT id::text FROM broadcasts WHERE id=$1 FOR KEY SHARE NOWAIT`, b["id"]).Scan(&id)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			locked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	logout := <-logoutCh
	renew := <-renewCh
	if locked || logout.status != 204 || renew.status != 403 {
		t.Fatalf("wrong-family renew blocked logout: rowLocked=%v logout=%d renew=%d", locked, logout.status, renew.status)
	}
}

func TestSessionAuthorityIdempotencyAndActiveDelete(t *testing.T) {
	ts := newTestServer(t)
	owner := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, owner, uniqueEmail())
	b := createBroadcast(t, ts, access, "세션 권한")
	id := b["id"].(string)
	base := ts.URL + "/api/v1/broadcasts/" + id
	other := newJarClient(t)
	_, otherAccess, _ := registerAndLogin(t, ts, other, uniqueEmail())
	if got := startSession(t, owner, base, "", "screen", "authority-0001"); got.status != 401 {
		t.Fatalf("anonymous start: %d", got.status)
	}
	if got := startSession(t, other, base, otherAccess, "screen", "authority-0002"); got.status != 403 {
		t.Fatalf("other owner start: %d", got.status)
	}
	if got := startSession(t, owner, ts.URL+"/api/v1/broadcasts/00000000-0000-4000-8000-000000000000", access, "screen", "authority-0003"); got.status != 404 {
		t.Fatalf("unknown start: %d", got.status)
	}
	first := startSession(t, owner, base, access, "screen", "authority-0004")
	if first.status != 202 {
		t.Fatalf("first start: %d %v", first.status, first.body)
	}
	firstID := first.body["session"].(map[string]any)["id"]
	grant := first.body["publishGrant"].(map[string]any)
	if grant["token"] == "" || !strings.HasPrefix(grant["whipUrl"].(string), "/api/v1/live-sessions/") {
		t.Fatalf("grant shape: %v", grant)
	}
	if got := startSession(t, owner, base, access, "screen", "authority-0004"); got.status != 202 || got.body["session"].(map[string]any)["id"] != firstID || got.body["publishGrant"].(map[string]any)["token"] != grant["token"] {
		t.Fatalf("replay changed session or grant: %d %v", got.status, got.body)
	}
	if got := startSession(t, owner, base, access, "camera", "authority-0004"); got.status != 409 {
		t.Fatalf("changed input: %d", got.status)
	}
	if got := startSession(t, owner, base, access, "screen", "authority-0005"); got.status != 409 {
		t.Fatalf("second active session: %d", got.status)
	}
	if got := startSession(t, owner, base, access, "screen", ""); got.status != 400 {
		t.Fatalf("missing idempotency key: %d", got.status)
	}
	_, secondFamilyAccess, _ := loginUser(t, ts, newJarClient(t), userEmailForSessionTest(t, access, ts))
	if got := startSession(t, owner, base, secondFamilyAccess, "screen", "authority-0004"); got.status != 403 {
		t.Fatalf("different family replay: %d", got.status)
	}
	if got := do(t, owner, "DELETE", base+"?version=1", nil, access); got.status != 409 {
		t.Fatalf("active delete: %d %v", got.status, got.body)
	}
	public := do(t, owner, "GET", base+"/sessions/current", nil, "")
	if public.status != 200 || strings.Contains(public.raw, grant["token"].(string)) || strings.Contains(public.raw, "authFamily") || strings.Contains(public.raw, "jti") {
		t.Fatalf("public leaks grant: %v", public.raw)
	}
	grantURL := base + "/sessions/" + firstID.(string) + "/publish-grants"
	stopURL := base + "/sessions/" + firstID.(string) + "/stop"
	if got := do(t, other, "POST", grantURL, nil, otherAccess); got.status != 403 {
		t.Fatalf("other owner grant: %d", got.status)
	}
	if got := do(t, other, "POST", stopURL, nil, otherAccess); got.status != 403 {
		t.Fatalf("other owner stop: %d", got.status)
	}
	if got := do(t, owner, "POST", grantURL, nil, secondFamilyAccess); got.status != 403 {
		t.Fatalf("other family grant: %d", got.status)
	}
	if got := do(t, owner, "POST", stopURL, nil, secondFamilyAccess); got.status != 202 {
		t.Fatalf("same owner other family stop: %d", got.status)
	}
}

func userEmailForSessionTest(t *testing.T, access string, ts *testServer) string {
	t.Helper()
	me := meWithToken(t, ts, access)
	if me.status != 200 {
		t.Fatalf("me: %d", me.status)
	}
	return me.body["user"].(map[string]any)["email"].(string)
}

func TestSessionGrantExpiryRefreshReuseAndMaintenance(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access, refresh := registerAndLogin(t, ts, client, email)
	b := createBroadcast(t, ts, access, "만료와 인증")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	first := startSession(t, client, base, access, "screen", "expiry-start-0001")
	if first.status != 202 {
		t.Fatalf("start: %d", first.status)
	}
	sid := first.body["session"].(map[string]any)["id"].(string)
	grantURL := base + "/sessions/" + sid + "/publish-grants"
	if got := do(t, client, "POST", grantURL, nil, access); got.status != 409 {
		t.Fatalf("early renew: %d", got.status)
	}
	refreshed := refreshWithToken(t, ts, refresh)
	if refreshed.status != 200 {
		t.Fatalf("refresh: %d", refreshed.status)
	}
	newAccess := refreshed.body["accessToken"].(string)
	if got := startSession(t, client, base, newAccess, "screen", "expiry-start-0001"); got.status != 202 || got.body["publishGrant"].(map[string]any)["token"] != first.body["publishGrant"].(map[string]any)["token"] {
		t.Fatalf("refresh should keep family and grant: %d %v", got.status, got.body)
	}
	issued := time.Now()
	ts.api.SetSessionClock(func() time.Time { return issued.Add(31 * time.Second) })
	renew := do(t, client, "POST", grantURL, nil, newAccess)
	if renew.status != 201 {
		t.Fatalf("renew: %d %v", renew.status, renew.body)
	}
	if renew.body["publishGrant"].(map[string]any)["token"] == first.body["publishGrant"].(map[string]any)["token"] {
		t.Fatal("renew reused original grant")
	}
	startedAt, _ := time.Parse(time.RFC3339Nano, first.body["session"].(map[string]any)["startedAt"].(string))
	renewExpires, _ := time.Parse(time.RFC3339Nano, renew.body["publishGrant"].(map[string]any)["expiresAt"].(string))
	if renewExpires.After(startedAt.Add(60 * time.Second)) {
		t.Fatalf("renew exceeded preparation deadline: %v", renewExpires)
	}
	if got := startSession(t, client, base, newAccess, "screen", "expiry-start-0001"); got.status != 202 || got.body["publishGrant"] != nil || got.body["publishGrantState"] != "unavailable" {
		t.Fatalf("original grant replay after renewal: %d %v", got.status, got.body)
	}
	// The consumed refresh token is reuse. Revocation and STOPPING must be
	// committed together, regardless of the independent maintenance worker.
	reused := refreshWithToken(t, ts, refresh)
	if reused.status != 401 {
		t.Fatalf("reuse: %d", reused.status)
	}
	public := do(t, client, "GET", base+"/sessions/current", nil, "")
	if public.body["session"].(map[string]any)["state"] != "stopping" {
		t.Fatalf("reuse did not stop: %v", public.body)
	}
	if got := do(t, client, "POST", grantURL, nil, newAccess); got.status != 401 {
		t.Fatalf("revoked family grant: %d", got.status)
	}
	var outboxCount int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND type='broadcast.session.stopping'`, sid).Scan(&outboxCount); err != nil || outboxCount != 1 {
		t.Fatalf("stopping outbox: %d %v", outboxCount, err)
	}
	// 04-A has no media create adapter: the background loop can prove absence
	// and finish without any GET side effect or Redis dependency.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got := do(t, client, "GET", base+"/sessions/current", nil, "")
		if got.body["session"].(map[string]any)["state"] == "ended" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("maintenance did not finish STOPPING")
}

func TestSessionPrepareTimeoutSurvivesRestartAndRedisOutage(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	firstServer := newTestServer(t, withFixedSecret(secret))
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, firstServer, client, uniqueEmail())
	b := createBroadcast(t, firstServer, access, "재시작 회차")
	base := firstServer.URL + "/api/v1/broadcasts/" + b["id"].(string)
	start := startSession(t, client, base, access, "screen", "restart-start-0001")
	if start.status != 202 {
		t.Fatalf("start: %d", start.status)
	}
	sid := start.body["session"].(map[string]any)["id"].(string)
	firstToken := start.body["publishGrant"].(map[string]any)["token"].(string)
	preExpiry := newTestServer(t, withFixedSecret(secret))
	preBase := preExpiry.URL + "/api/v1/broadcasts/" + b["id"].(string)
	if replay := startSession(t, client, preBase, access, "screen", "restart-start-0001"); replay.status != 202 || replay.body["publishGrant"].(map[string]any)["token"] != firstToken {
		t.Fatalf("restart extended or changed original grant: %d %v", replay.status, replay.body)
	}
	preExpiry.stopMaintenance()
	preExpiry.Close()
	firstServer.stopMaintenance()
	firstServer.Close()
	restarted := newTestServer(t, withFixedSecret(secret), withBadRedis())
	base = restarted.URL + "/api/v1/broadcasts/" + b["id"].(string)
	current := do(t, client, "GET", base+"/sessions/current", nil, "")
	if current.status != 200 || current.body["session"].(map[string]any)["id"] != sid {
		t.Fatalf("restart lost state: %v", current.body)
	}
	// Redis outage makes protected writes fail closed, but the independent
	// maintenance loop must still expire and finish the prepared session.
	if got := startSession(t, client, base, access, "screen", "restart-start-0001"); got.status != 503 {
		t.Fatalf("protected write during redis outage: %d", got.status)
	}
	restarted.api.SetSessionClock(func() time.Time { return time.Now().Add(61 * time.Second) })
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current = do(t, client, "GET", base+"/sessions/current", nil, "")
		if current.body["session"].(map[string]any)["state"] == "ended" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if current.body["session"].(map[string]any)["state"] != "ended" {
		t.Fatalf("timeout maintenance did not end: %v", current.body)
	}
	if current.body["session"].(map[string]any)["version"] != float64(3) {
		t.Fatalf("missing transitions: %v", current.body)
	}
	var events int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, sid).Scan(&events); err != nil || events != 3 {
		t.Fatalf("outbox transitions: %d %v", events, err)
	}
	good := newTestServer(t, withFixedSecret(secret))
	goodBase := good.URL + "/api/v1/broadcasts/" + b["id"].(string)
	replay := startSession(t, client, goodBase, access, "screen", "restart-start-0001")
	if replay.status != 202 || replay.body["session"].(map[string]any)["id"] != sid || replay.body["publishGrant"] != nil {
		t.Fatalf("ended replay should not renew grant: %d %v", replay.status, replay.body)
	}
	if strings.Contains(replay.raw, firstToken) {
		t.Fatal("ended replay exposed original grant")
	}
}

func TestUnresolvedCreateNeverBecomesEnded(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "정리 보류")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	start := startSession(t, client, base, access, "screen", "pending-start-0001")
	sid := start.body["session"].(map[string]any)["id"].(string)
	var jti string
	if err := testPool.QueryRow(t.Context(), `SELECT jti::text FROM publish_grants WHERE broadcast_session_id=$1`, sid).Scan(&jti); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(t.Context(), `INSERT INTO media_create_operations (broadcast_session_id,media_generation,grant_jti,state) VALUES ($1,1,$2,'pending')`, sid, jti); err != nil {
		t.Fatal(err)
	}
	stop := do(t, client, "POST", base+"/sessions/"+sid+"/stop", nil, access)
	if stop.status != 202 {
		t.Fatalf("stop: %d", stop.status)
	}
	time.Sleep(5500 * time.Millisecond)
	current := do(t, client, "GET", base+"/sessions/current", nil, "")
	if current.body["session"].(map[string]any)["state"] != "stopping" {
		t.Fatalf("unknown create was treated as absent: %v", current.body)
	}
	if _, err := testPool.Exec(t.Context(), `UPDATE media_create_operations SET state='closed',resolved_at=now() WHERE broadcast_session_id=$1`, sid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current = do(t, client, "GET", base+"/sessions/current", nil, "")
		if current.body["session"].(map[string]any)["state"] == "ended" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("closed create did not allow completion")
}

func TestConcurrentStartAndDeleteSerialize(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "시작 삭제 경합")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	var start, del apiResp
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); start = startSession(t, client, base, access, "screen", "race-start-0001") }()
	go func() { defer wg.Done(); del = do(t, client, "DELETE", base+"?version=1", nil, access) }()
	wg.Wait()
	if !((start.status == 202 && del.status == 409) || (start.status == 404 && del.status == 204)) {
		t.Fatalf("start/delete inconsistent: start=%d delete=%d", start.status, del.status)
	}
}

func TestLogoutRevokesFamilyAndStopsPreparationTogether(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "로그아웃 정리")
	base := ts.URL + "/api/v1/broadcasts/" + b["id"].(string)
	start := startSession(t, client, base, access, "screen", "logout-start-0001")
	if start.status != 202 {
		t.Fatalf("start: %d", start.status)
	}
	sid := start.body["session"].(map[string]any)["id"].(string)
	logout := do(t, client, "POST", ts.URL+"/api/v1/auth/logout", nil, access)
	if logout.status != 204 {
		t.Fatalf("logout: %d %v", logout.status, logout.body)
	}
	current := do(t, client, "GET", base+"/sessions/current", nil, "")
	if current.body["session"].(map[string]any)["state"] != "stopping" {
		t.Fatalf("logout left preparation active: %v", current.body)
	}
	if got := startSession(t, client, base, access, "screen", "logout-start-0002"); got.status != 401 {
		t.Fatalf("revoked family started session: %d", got.status)
	}
	var version int64
	if err := testPool.QueryRow(t.Context(), `SELECT aggregate_version FROM outbox_events WHERE aggregate_id=$1 AND type='broadcast.session.stopping'`, sid).Scan(&version); err != nil || version != 2 {
		t.Fatalf("logout outbox version: %d %v", version, err)
	}
}

// The public HTTP seam is backed by the disposable PostgreSQL and Redis
// instances from TestMain. An owner may prepare, while a visitor sees only
// the public state; preparation never means media is live.
func TestPrepareAndCancelSession(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	b := createBroadcast(t, ts, access, "준비 검사")
	id := b["id"].(string)
	base := ts.URL + "/api/v1/broadcasts/" + id
	// Both starts use one logical second, exercising latest-session ordering
	// independently of the externally reported startedAt timestamp.
	fixed := time.Now().UTC().Truncate(time.Second)
	ts.api.SetSessionClock(func() time.Time { return fixed })

	start := startSession(t, client, base, access, "screen", "start-first-0001")
	if start.status != http.StatusAccepted {
		t.Fatalf("start: %d %v", start.status, start.body)
	}
	sess := start.body["session"].(map[string]any)
	if sess["state"] != "starting" || start.body["publishGrant"] == nil {
		t.Fatalf("preparation should be starting with grant: %v", start.body)
	}
	public := do(t, client, "GET", base+"/sessions/current", nil, "")
	if public.status != 200 || public.body["session"].(map[string]any)["state"] != "starting" {
		t.Fatalf("public current: %d %v", public.status, public.body)
	}
	stop := do(t, client, "POST", base+"/sessions/"+sess["id"].(string)+"/stop", nil, access)
	if stop.status != http.StatusAccepted || stop.body["session"].(map[string]any)["state"] != "stopping" {
		t.Fatalf("stop: %d %v", stop.status, stop.body)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		public = do(t, client, "GET", base+"/sessions/current", nil, "")
		if public.body["session"].(map[string]any)["state"] == "ended" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if public.body["session"].(map[string]any)["state"] != "ended" {
		t.Fatal("cancel did not finish")
	}
	again := startSession(t, client, base, access, "screen", "start-second-0002")
	if again.status != 202 || again.body["session"].(map[string]any)["id"] == sess["id"] {
		t.Fatalf("restart: %d %v", again.status, again.body)
	}
	public = do(t, client, "GET", base+"/sessions/current", nil, "")
	if public.body["session"].(map[string]any)["id"] != again.body["session"].(map[string]any)["id"] {
		t.Fatalf("public current returned old session: %v", public.body)
	}
}
