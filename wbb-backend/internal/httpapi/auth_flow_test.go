package httpapi_test

// Auth flows tested through the public HTTP seam (ticket 01):
// register/login/refresh/logout/me, rotation, reuse detection, logout
// revocation, family-ledger regression and Redis fail-closed behaviour.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegisterCreatesUserWithoutLogin(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()

	resp := do(t, client, "POST", ts.URL+"/api/v1/auth/register", map[string]any{
		"email":       email,
		"password":    testPassword,
		"displayName": "Alice",
	}, "")
	if resp.status != http.StatusCreated {
		t.Fatalf("status=%d body=%v", resp.status, resp.body)
	}
	user := resp.body["user"].(map[string]any)
	if user["email"] != email || user["displayName"] != "Alice" || user["id"] == "" {
		t.Fatalf("unexpected user: %v", user)
	}
	if user["password"] != nil || user["passwordHash"] != nil {
		t.Fatalf("password material leaked in response: %v", user)
	}
	// No auto-login: no refresh cookie, and /me is unauthorized.
	if c := refreshCookieValue(resp.header); c != "" {
		t.Fatalf("register must not set a refresh cookie")
	}
	if r := meWithToken(t, ts, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("me after register: status=%d", r.status)
	}
}

func TestRegisterDuplicateEmailConflict(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	registerUser(t, ts, client, email)

	resp := do(t, client, "POST", ts.URL+"/api/v1/auth/register", map[string]any{
		"email":       strings.ToUpper(email), // normalized duplicate
		"password":    testPassword,
		"displayName": "Dup",
	}, "")
	if resp.status != http.StatusConflict || resp.errCode() != "email_taken" {
		t.Fatalf("status=%d code=%s body=%v", resp.status, resp.errCode(), resp.body)
	}
}

func TestRegisterValidation(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)

	cases := []map[string]any{
		{"email": "not-an-email", "password": testPassword, "displayName": "A"},
		{"email": "", "password": testPassword, "displayName": "A"},
		{"email": uniqueEmail(), "password": "short", "displayName": "A"},
		{"email": uniqueEmail(), "password": testPassword, "displayName": ""},
		{"email": uniqueEmail(), "password": testPassword, "displayName": strings.Repeat("너무", 30)},
	}
	for i, c := range cases {
		resp := do(t, client, "POST", ts.URL+"/api/v1/auth/register", c, "")
		if resp.status != http.StatusBadRequest || resp.errCode() != "invalid_input" {
			t.Fatalf("case %d: status=%d code=%s body=%v", i, resp.status, resp.errCode(), resp.body)
		}
		if resp.body["error"].(map[string]any)["requestId"] == "" {
			t.Fatalf("case %d: error envelope must carry requestId", i)
		}
	}
}

func TestLoginIssuesTokensAndMe(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	user, access, refresh := registerAndLogin(t, ts, client, email)

	if user["email"] != email {
		t.Fatalf("login user email mismatch: %v", user)
	}

	// Refresh cookie attributes.
	found := false
	for _, c := range loginSetCookies(t, ts, client, email) {
		if strings.HasPrefix(c, "wbb_refresh=") {
			found = true
			if !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "Path=/api/v1/auth") || !strings.Contains(c, "SameSite=Lax") {
				t.Fatalf("refresh cookie attributes wrong: %s", c)
			}
		}
	}
	if !found {
		t.Fatal("login must set the refresh cookie")
	}
	if refresh == "" {
		t.Fatal("refresh cookie value empty")
	}

	// /me with the access token.
	r := meWithToken(t, ts, access)
	if r.status != http.StatusOK || r.body["user"].(map[string]any)["id"] != user["id"] {
		t.Fatalf("me: status=%d body=%v", r.status, r.body)
	}

	// /me without or with a garbage token is unauthorized.
	if r := meWithToken(t, ts, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("me without token: %d", r.status)
	}
	if r := meWithToken(t, ts, "garbage.token.here"); r.status != http.StatusUnauthorized {
		t.Fatalf("me with garbage token: %d", r.status)
	}
}

func TestLoginWrongCredentials(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	registerUser(t, ts, client, email)

	r := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": "wrong-password-123"}, "")
	if r.status != http.StatusUnauthorized || r.errCode() != "invalid_credentials" {
		t.Fatalf("wrong password: status=%d code=%s", r.status, r.errCode())
	}
	r = do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": "ghost@example.com", "password": testPassword}, "")
	if r.status != http.StatusUnauthorized || r.errCode() != "invalid_credentials" {
		t.Fatalf("unknown email: status=%d code=%s", r.status, r.errCode())
	}
}

func TestRefreshRotationAndReuseDestroysFamily(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, _, refresh1 := registerAndLogin(t, ts, client, email)

	// Rotate once.
	r1 := refreshWithToken(t, ts, refresh1)
	if r1.status != http.StatusOK {
		t.Fatalf("refresh: status=%d body=%v", r1.status, r1.body)
	}
	refresh2 := refreshCookieValue(r1.header)
	if refresh2 == "" || refresh2 == refresh1 {
		t.Fatal("refresh cookie must rotate to a new value")
	}
	access2, _ := r1.body["accessToken"].(string)

	// Replaying the consumed token revokes the family…
	r2 := refreshWithToken(t, ts, refresh1)
	if r2.status != http.StatusUnauthorized {
		t.Fatalf("reuse: status=%d body=%v", r2.status, r2.body)
	}

	// …so the newest token is dead too, and its access token no longer works.
	r3 := refreshWithToken(t, ts, refresh2)
	if r3.status != http.StatusUnauthorized {
		t.Fatalf("newest token after family revocation: status=%d body=%v", r3.status, r3.body)
	}
	if r := meWithToken(t, ts, access2); r.status != http.StatusUnauthorized {
		t.Fatalf("access token after family revocation: status=%d", r.status)
	}
}

func TestOldAccessTokenRejectedAfterRotation(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access1, refresh1 := registerAndLogin(t, ts, client, email)

	r := refreshWithToken(t, ts, refresh1)
	if r.status != http.StatusOK {
		t.Fatalf("refresh: %d", r.status)
	}
	access2, _ := r.body["accessToken"].(string)

	if r := meWithToken(t, ts, access1); r.status != http.StatusUnauthorized {
		t.Fatalf("old access token must be rejected after rotation: %d", r.status)
	}
	if r := meWithToken(t, ts, access2); r.status != http.StatusOK {
		t.Fatalf("new access token must work: %d", r.status)
	}
}

func TestLogoutRevokesSessionAndIsIdempotent(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access, refresh := registerAndLogin(t, ts, client, email)

	// Logout with the refresh cookie.
	resp := postLogout(t, ts, refresh, "")
	if resp != http.StatusNoContent {
		t.Fatalf("logout: %d", resp)
	}

	// Access and refresh are both rejected after logout.
	if r := meWithToken(t, ts, access); r.status != http.StatusUnauthorized {
		t.Fatalf("access after logout: %d", r.status)
	}
	if r := refreshWithToken(t, ts, refresh); r.status != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: %d", r.status)
	}
	// Repeated logout stays 204.
	if resp := postLogout(t, ts, "", ""); resp != http.StatusNoContent {
		t.Fatalf("second logout should stay 204: %d", resp)
	}
}

// TestLogoutWithBothCredentialsRevokesValidSession: an unknown/stale refresh
// cookie must not shadow a valid Bearer token — both credentials are checked
// independently and every live session they reference is destroyed
// (regression: bearer session previously survived).
func TestLogoutWithBothCredentialsRevokesValidSession(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())

	// Unknown refresh cookie + valid bearer.
	if resp := postLogout(t, ts, "garbage-refresh-token-value", access); resp != http.StatusNoContent {
		t.Fatalf("logout: %d", resp)
	}
	if r := meWithToken(t, ts, access); r.status != http.StatusUnauthorized {
		t.Fatalf("bearer session must be revoked despite unknown cookie: %d", r.status)
	}

	// Both valid: cookie session and bearer session both die.
	_, access2, refresh2 := registerAndLogin(t, ts, client, uniqueEmail())
	if resp := postLogout(t, ts, refresh2, access2); resp != http.StatusNoContent {
		t.Fatalf("logout both: %d", resp)
	}
	if r := meWithToken(t, ts, access2); r.status != http.StatusUnauthorized {
		t.Fatalf("access after both-credentials logout: %d", r.status)
	}
	if r := refreshWithToken(t, ts, refresh2); r.status != http.StatusUnauthorized {
		t.Fatalf("refresh after both-credentials logout: %d", r.status)
	}
}

func TestLogoutWithBearerOnly(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access, _ := registerAndLogin(t, ts, client, email)

	r := do(t, client, "POST", ts.URL+"/api/v1/auth/logout", nil, access)
	if r.status != http.StatusNoContent {
		t.Fatalf("logout via bearer: %d body=%v", r.status, r.body)
	}
	if r := meWithToken(t, ts, access); r.status != http.StatusUnauthorized {
		t.Fatalf("access after bearer logout: %d", r.status)
	}
}

// TestFamilyRevocationSurvivesStaleRedisCache is the regression for the
// review finding: a family revoked in PostgreSQL (e.g. logout whose cache
// delete failed) must stay rejected even while the Redis sid entry is alive.
func TestFamilyRevocationSurvivesStaleRedisCache(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access, _ := registerAndLogin(t, ts, client, email)

	if r := meWithToken(t, ts, access); r.status != http.StatusOK {
		t.Fatalf("precondition me: %d", r.status)
	}

	// Simulate "logout succeeded in PG, Redis DEL failed": revoke the family
	// ledger directly while leaving the Redis active-sid entry untouched.
	_, err := testPool.Exec(t.Context(),
		`UPDATE auth_session_families SET revoked_at = now(), revoked_reason = 'logout'`)
	if err != nil {
		t.Fatalf("revoke family: %v", err)
	}

	if r := meWithToken(t, ts, access); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked family must be rejected despite live cache: status=%d", r.status)
	}
}

// TestConcurrentRefreshAllowsExactlyOneWinner: parallel refreshes with the
// same token must not mint two live sessions. The loser replay must revoke
// the whole family inside the lock, so even the winner's fresh tokens die
// (regression: loser previously returned 401 without destroying the family).
func TestConcurrentRefreshAllowsExactlyOneWinner(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, _, refresh1 := registerAndLogin(t, ts, client, email)

	type result struct {
		status  int
		refresh string
		access  string
	}
	const n = 8
	results := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := refreshWithToken(t, ts, refresh1)
			res := result{status: r.status, refresh: refreshCookieValue(r.header)}
			if s, ok := r.body["accessToken"].(string); ok {
				res.access = s
			}
			results[i] = res
		}(i)
	}
	close(start)
	wg.Wait()

	var winner *result
	wins := 0
	for i := range results {
		if results[i].status == http.StatusOK {
			wins++
			winner = &results[i]
		} else if results[i].status != http.StatusUnauthorized {
			t.Fatalf("unexpected status %d in race", results[i].status)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one refresh must win, got %d", wins)
	}

	// The losers' replays revoke the family: the winner's tokens must be dead.
	if r := refreshWithToken(t, ts, winner.refresh); r.status != http.StatusUnauthorized {
		t.Fatalf("winner refresh after losing replays: %d (want 401, family must be revoked)", r.status)
	}
	if r := meWithToken(t, ts, winner.access); r.status != http.StatusUnauthorized {
		t.Fatalf("winner access after losing replays: %d (want 401)", r.status)
	}
}

// TestRefreshRaceUnderFamilyLock deterministically forces the review race:
// two refreshes both pass the pre-checks (token alive, cache matches) and
// then serialize on the family row lock. The loser must revoke the family
// inside its transaction, so the winner's freshly minted tokens die too.
func TestRefreshRaceUnderFamilyLock(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, _, refresh1 := registerAndLogin(t, ts, client, email)

	// Arrange: hold the family row lock from the test so both HTTP refreshes
	// queue on it after passing their pre-checks.
	sum := sha256.Sum256([]byte(refresh1))
	tokenHash := hex.EncodeToString(sum[:])
	var familyID string
	if err := testPool.QueryRow(t.Context(),
		`SELECT refresh_family_id::text FROM auth_sessions WHERE refresh_token_hash = $1`, tokenHash,
	).Scan(&familyID); err != nil {
		t.Fatalf("find family: %v", err)
	}
	lockTx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	defer lockTx.Rollback(t.Context())
	if _, err := lockTx.Exec(t.Context(),
		`SELECT revoked_at FROM auth_session_families WHERE id = $1 FOR UPDATE`, familyID); err != nil {
		t.Fatalf("lock family: %v", err)
	}

	type outcome struct {
		status  int
		refresh string
		access  string
	}
	done := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r := refreshWithToken(t, ts, refresh1)
			o := outcome{status: r.status, refresh: refreshCookieValue(r.header)}
			if s, ok := r.body["accessToken"].(string); ok {
				o.access = s
			}
			done <- o
		}()
	}
	// Both requests are now parked on the family lock (pre-checks passed).
	time.Sleep(500 * time.Millisecond)
	if err := lockTx.Commit(t.Context()); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	var winner *outcome
	wins := 0
	for i := 0; i < 2; i++ {
		o := <-done
		switch o.status {
		case http.StatusOK:
			wins++
			winner = &o
		case http.StatusUnauthorized:
			// loser
		default:
			t.Fatalf("unexpected status %d", o.status)
		}
	}
	if wins != 1 || winner == nil {
		t.Fatalf("exactly one refresh must win, got %d", wins)
	}

	// The loser revoked the family under the lock: winner's tokens are dead.
	if r := refreshWithToken(t, ts, winner.refresh); r.status != http.StatusUnauthorized {
		t.Fatalf("winner refresh must be dead after losing replay: %d", r.status)
	}
	if r := meWithToken(t, ts, winner.access); r.status != http.StatusUnauthorized {
		t.Fatalf("winner access must be dead after losing replay: %d", r.status)
	}
}

// TestRedisOutageFailsClosedProtectedButNotPublic: with Redis unreachable,
// protected APIs answer 503 while anonymous reads keep working.
func TestRedisOutageFailsClosedProtectedButNotPublic(t *testing.T) {
	good := newTestServer(t)
	client := newJarClient(t)
	email := uniqueEmail()
	_, access, _ := registerAndLogin(t, good, client, email)

	secret := good.cfg.JWTSecret
	bad := newTestServer(t, withFixedSecret(secret), withBadRedis())

	if r := meWithToken(t, bad, access); r.status != http.StatusServiceUnavailable {
		t.Fatalf("me during redis outage: %d (want 503)", r.status)
	}
	r := do(t, client, "GET", bad.URL+"/api/v1/broadcasts", nil, "")
	if r.status != http.StatusOK {
		t.Fatalf("anonymous list during redis outage: %d (want 200)", r.status)
	}
}

func TestRateLimitOnLogin(t *testing.T) {
	ts := newTestServer(t, withRateLimit(3))
	client := newJarClient(t)
	email := uniqueEmail()
	registerUser(t, ts, client, email) // register kind is separate

	for i := 1; i <= 3; i++ {
		r := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, "")
		if r.status != http.StatusOK {
			t.Fatalf("login %d: %d", i, r.status)
		}
	}
	r := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, "")
	if r.status != http.StatusTooManyRequests || r.errCode() != "rate_limited" {
		t.Fatalf("4th login: status=%d code=%s", r.status, r.errCode())
	}
	if r.header.Get("Retry-After") == "" {
		t.Fatal("429 must include Retry-After")
	}
}

// TestAuthPostOriginValidation: register, login, refresh and logout compare
// the full Origin exactly against the configured list. Missing Origin,
// unknown origins and scheme mismatches are rejected (regression: Origin was
// optional and Host-only matching was accepted before).
func TestAuthPostOriginValidation(t *testing.T) {
	ts := newTestServer(t)
	email := uniqueEmail()

	// Missing Origin on all four auth POSTs → 403.
	if r := postRegisterNoOrigin(t, ts, email); r != http.StatusForbidden {
		t.Fatalf("register without Origin: %d (want 403)", r)
	}
	if r := postLoginOrigin(t, ts, email, ""); r != http.StatusForbidden {
		t.Fatalf("login without Origin: %d (want 403)", r)
	}
	if r := postRefreshOrigin(t, ts, "x", ""); r != http.StatusForbidden {
		t.Fatalf("refresh without Origin: %d (want 403)", r)
	}
	if r := postLogoutOrigin(t, ts, "", ""); r != http.StatusForbidden {
		t.Fatalf("logout without Origin: %d (want 403)", r)
	}

	// Unknown and scheme-mismatched origins are rejected even when the host
	// matches.
	hostOnly := strings.TrimPrefix(ts.URL, "http://")
	for _, origin := range []string{
		"https://" + hostOnly, // same host, different scheme
		"http://" + hostOnly + ".evil.example",
		"https://evil.example",
	} {
		if r := postLoginOrigin(t, ts, email, origin); r != http.StatusForbidden {
			t.Fatalf("login with Origin %q: %d (want 403)", origin, r)
		}
	}

	// The configured same-origin value works end to end (do() sends the
	// same-origin Origin on POSTs).
	if r := postRegisterNoOriginSet(t, ts, email, ts.URL); r != http.StatusCreated {
		t.Fatalf("register with exact origin: %d", r)
	}
	client := newJarClient(t)
	if r := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, ""); r.status != http.StatusOK {
		t.Fatalf("login with exact origin: %d body=%v", r.status, r.body)
	}
}

// --- explicit-origin request helpers ---------------------------------------

func postLoginOrigin(t *testing.T, ts *testServer, email, origin string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/login",
		strings.NewReader(`{"email":"`+email+`","password":"wrong-password-1"}`))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func postRegisterNoOrigin(t *testing.T, ts *testServer, email string) int {
	return postRegisterNoOriginSet(t, ts, email, "")
}

func postRegisterNoOriginSet(t *testing.T, ts *testServer, email, origin string) int {
	t.Helper()
	body := fmt.Sprintf(`{"email":"%s","password":"%s","displayName":"O"}`, email, testPassword)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func postRefreshOrigin(t *testing.T, ts *testServer, refreshToken, origin string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/refresh", nil)
	if refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "wbb_refresh", Value: refreshToken})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// postLogout sends logout with explicit credentials and Origin (always set:
// logout requires it after the fix).
func postLogout(t *testing.T, ts *testServer, refreshToken, bearer string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/logout", nil)
	if refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "wbb_refresh", Value: refreshToken})
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Origin", ts.URL)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func postLogoutOrigin(t *testing.T, ts *testServer, refreshToken, origin string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/logout", nil)
	if refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "wbb_refresh", Value: refreshToken})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func loginSetCookies(t *testing.T, ts *testServer, client *http.Client, email string) []string {
	t.Helper()
	resp := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, "")
	if resp.status != http.StatusOK {
		t.Fatalf("login: %d", resp.status)
	}
	return resp.header.Values("Set-Cookie")
}
