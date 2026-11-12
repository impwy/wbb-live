package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func evaluationOptions(email, password string) serverOpt {
	return func(c *config.Config) {
		c.EvaluationLoginEnabled = true
		c.EvaluationLoginEmail = email
		c.EvaluationLoginPassword = password
		c.EvaluationLoginIPLimit = 10
		c.EvaluationLoginAccountLimit = 10
		c.RateLimitWindow = 1 * time.Minute
	}
}

func TestEvaluationLoginPublicAuthAndOwnership(t *testing.T) {
	client := newJarClient(t)
	disabled := newTestServer(t)
	if got := do(t, client, "GET", disabled.URL+"/api/v1/auth/providers", nil, ""); got.body["evaluationEnabled"] != false {
		t.Fatal("disabled provider was advertised")
	}
	if got := do(t, client, "POST", disabled.URL+"/api/v1/auth/evaluation-login", nil, ""); got.status != http.StatusNotFound {
		t.Fatalf("disabled endpoint=%d", got.status)
	}

	email := uniqueEmail()
	ts := newTestServer(t, evaluationOptions(email, testPassword))
	path := ts.URL + "/api/v1/auth/evaluation-login"
	if got := do(t, client, "GET", ts.URL+"/api/v1/auth/providers", nil, ""); got.body["evaluationEnabled"] != true {
		t.Fatal("enabled provider was hidden")
	}
	if got := do(t, client, "POST", path, nil, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("missing account=%d", got.status)
	}
	registerUser(t, ts, client, email)
	wrong := newTestServer(t, evaluationOptions(email, "wrong-placeholder-password"))
	if got := do(t, newJarClient(t), "POST", wrong.URL+"/api/v1/auth/evaluation-login", nil, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("wrong secret=%d", got.status)
	}
	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://wrong.example")
	badOrigin, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	badOrigin.Body.Close()
	if badOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin=%d", badOrigin.StatusCode)
	}
	missingOrigin, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutOrigin, err := client.Do(missingOrigin)
	if err != nil {
		t.Fatal(err)
	}
	withoutOrigin.Body.Close()
	if withoutOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("missing origin=%d", withoutOrigin.StatusCode)
	}
	if got := do(t, client, "POST", path, map[string]string{"email": "ignored"}, ""); got.status != http.StatusBadRequest {
		t.Fatalf("credential body accepted=%d", got.status)
	}
	result := do(t, client, "POST", path, nil, "")
	if result.status != http.StatusOK || refreshCookieValue(result.header) == "" {
		t.Fatalf("evaluation login failed: status=%d cookie=%t", result.status, refreshCookieValue(result.header) != "")
	}
	access, _ := result.body["accessToken"].(string)
	if access == "" || meWithToken(t, ts, access).status != http.StatusOK {
		t.Fatal("evaluation session was not a normal authenticated session")
	}
	own := createBroadcast(t, ts, access, "evaluation owned")
	otherClient := newJarClient(t)
	_, otherAccess, _ := registerAndLogin(t, ts, otherClient, uniqueEmail())
	other := createBroadcast(t, ts, otherAccess, "other owned")
	if got := do(t, client, "DELETE", ts.URL+"/api/v1/broadcasts/"+other["id"].(string)+"?version=1", nil, access); got.status != http.StatusForbidden {
		t.Fatalf("evaluation user controlled another broadcast: %d", got.status)
	}
	if got := do(t, otherClient, "DELETE", ts.URL+"/api/v1/broadcasts/"+own["id"].(string)+"?version=1", nil, otherAccess); got.status != http.StatusForbidden {
		t.Fatalf("other user controlled evaluation broadcast: %d", got.status)
	}
	if got := do(t, client, "POST", ts.URL+"/api/v1/auth/refresh", nil, ""); got.status != http.StatusOK {
		t.Fatalf("evaluation refresh=%d", got.status)
	}
	if got := do(t, client, "POST", ts.URL+"/api/v1/auth/logout", nil, ""); got.status != http.StatusNoContent {
		t.Fatalf("evaluation logout=%d", got.status)
	}
	if meWithToken(t, ts, access).status != http.StatusUnauthorized {
		t.Fatal("logout left evaluation access active")
	}
}

func TestEvaluationLoginIndependentLimitsAndStoreFailure(t *testing.T) {
	email := uniqueEmail()
	seed := newTestServer(t)
	registerUser(t, seed, newJarClient(t), email)
	ip := newTestServer(t, func(c *config.Config) {
		evaluationOptions(email, testPassword)(c)
		c.EvaluationLoginIPLimit = 1
	})
	account := newTestServer(t, func(c *config.Config) {
		evaluationOptions(email, testPassword)(c)
		c.EvaluationLoginAccountLimit = 1
	})
	fixedWindow := func() time.Time { return time.Date(2026, 11, 11, 12, 0, 5, 0, time.UTC) }
	ip.api.SetRateLimitClock(fixedWindow)
	account.api.SetRateLimitClock(fixedWindow)
	first := do(t, newJarClient(t), "POST", ip.URL+"/api/v1/auth/evaluation-login", nil, "")
	if first.status != 200 {
		t.Fatalf("first IP attempt=%d", first.status)
	}
	// X-Forwarded-For is not trusted. Behind Caddy this bucket represents
	// the proxy address until a trusted-proxy design is implemented.
	req, err := http.NewRequest(http.MethodPost, ip.URL+"/api/v1/auth/evaluation-login", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", ip.URL)
	req.Header.Set("X-Forwarded-For", "203.0.113.40")
	resp, err := newJarClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("forwarded IP bypassed proxy-address bucket: %d", resp.StatusCode)
	}

	if got := do(t, newJarClient(t), "POST", account.URL+"/api/v1/auth/evaluation-login", nil, ""); got.status != 429 {
		// The first server already consumed this account's independent budget.
		t.Fatalf("account limit not shared across requests: %d", got.status)
	}
	bad := newTestServer(t, withBadRedis(), evaluationOptions(email, testPassword))
	if got := do(t, newJarClient(t), "POST", bad.URL+"/api/v1/auth/evaluation-login", nil, ""); got.status != 503 {
		t.Fatalf("Redis outage issued evaluation session: %d", got.status)
	}
}

// One-click test accounts: the browser sees only id and label; signing in
// issues an ordinary session for that account, and ownership still applies.
func TestQuickLoginTestAccounts(t *testing.T) {
	seed := newTestServer(t)
	streamerEmail, viewerEmail := uniqueEmail(), uniqueEmail()
	registerUser(t, seed, newJarClient(t), streamerEmail)
	registerUser(t, seed, newJarClient(t), viewerEmail)
	id := func(email string) string { return strings.SplitN(email, "@", 2)[0] }
	ts := newTestServer(t, func(c *config.Config) {
		c.QuickLogins = []config.QuickLogin{
			{ID: id(streamerEmail), Label: "방송자1", Email: streamerEmail, Password: testPassword},
			{ID: id(viewerEmail), Label: "시청자1", Email: viewerEmail, Password: testPassword},
		}
		c.EvaluationLoginIPLimit = 20
		c.EvaluationLoginAccountLimit = 20
		c.RateLimitWindow = time.Minute
	})
	providers := do(t, newJarClient(t), "GET", ts.URL+"/api/v1/auth/providers", nil, "")
	list := providers.body["quickLogins"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["label"] != "방송자1" || len(list[0].(map[string]any)) != 2 {
		t.Fatalf("providers must expose only id and label: %v", list)
	}
	if strings.Contains(providers.raw, testPassword) || strings.Contains(providers.raw, streamerEmail) {
		t.Fatal("providers leaked credentials")
	}

	streamer := newJarClient(t)
	login := do(t, streamer, "POST", ts.URL+"/api/v1/auth/quick-login/"+id(streamerEmail), nil, "")
	if login.status != 200 || refreshCookieValue(login.header) == "" || login.body["user"].(map[string]any)["email"] != streamerEmail {
		t.Fatalf("quick login: %d %v", login.status, login.body)
	}
	streamerAccess := login.body["accessToken"].(string)
	viewer := newJarClient(t)
	viewerAccess := do(t, viewer, "POST", ts.URL+"/api/v1/auth/quick-login/"+id(viewerEmail), nil, "").body["accessToken"].(string)
	b := createBroadcast(t, ts, streamerAccess, "빠른 로그인 방송")
	if r := do(t, viewer, "DELETE", ts.URL+"/api/v1/broadcasts/"+b["id"].(string)+"?version=1", nil, viewerAccess); r.status != http.StatusForbidden {
		t.Fatalf("viewer controlled streamer broadcast: %d", r.status)
	}
	if r := do(t, newJarClient(t), "POST", ts.URL+"/api/v1/auth/quick-login/nobody", nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("unknown account: %d", r.status)
	}
	if r := do(t, newJarClient(t), "POST", ts.URL+"/api/v1/auth/quick-login/"+id(viewerEmail), map[string]string{"email": "x"}, ""); r.status != http.StatusBadRequest {
		t.Fatalf("credential body accepted: %d", r.status)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/quick-login/"+id(viewerEmail), nil)
	req.Header.Set("Origin", "https://wrong.example")
	resp, err := newJarClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin: %d", resp.StatusCode)
	}
	if r := do(t, newJarClient(t), "POST", seed.URL+"/api/v1/auth/quick-login/"+id(viewerEmail), nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("disabled quick login route: %d", r.status)
	}
}
