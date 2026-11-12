package httpapi_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
)

func TestGoogleDisabledKeepsEmailAuthAvailable(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	providers := do(t, client, "GET", ts.URL+"/api/v1/auth/providers", nil, "")
	if providers.status != 200 || providers.body["googleEnabled"] != false {
		t.Fatalf("providers: %d %v", providers.status, providers.body)
	}
	start := do(t, client, "GET", ts.URL+"/api/v1/auth/google/start", nil, "")
	if start.status != http.StatusServiceUnavailable {
		t.Fatalf("start: %d", start.status)
	}
	email := uniqueEmail()
	registerUser(t, ts, client, email)
}

type googleTransport struct {
	key              *rsa.PrivateKey
	nonce, challenge string
	badClaim         string
	subject, email   string
	exchangeCount    int
	callback         string
}

func (g *googleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := func(status int, v any) *http.Response {
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}
	}
	switch req.URL.String() {
	case "https://oauth2.googleapis.com/token":
		g.exchangeCount++
		_ = req.ParseForm()
		if req.Method != "POST" || req.Form.Get("client_id") != "test-client" || req.Form.Get("client_secret") != "test-secret" || req.Form.Get("grant_type") != "authorization_code" || req.Form.Get("redirect_uri") != g.callback || req.Form.Get("code") != "good-code" || req.Form.Get("code_verifier") == "" {
			return body(400, map[string]string{"error": "invalid_grant"}), nil
		}
		if digestTest(req.Form.Get("code_verifier")) != g.challenge {
			return body(400, map[string]string{"error": "invalid_grant"}), nil
		}
		claims := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "test-client", "sub": g.subject, "email": g.email, "email_verified": true, "name": "Google Tester", "nonce": g.nonce, "exp": time.Now().Add(time.Hour).Unix()}
		switch g.badClaim {
		case "nonce":
			claims["nonce"] = "wrong"
		case "issuer":
			claims["iss"] = "https://evil.example"
		case "audience":
			claims["aud"] = "other"
		case "azp":
			claims["azp"] = "other"
		case "azp-wrong-type":
			claims["azp"] = 123.0 // must NOT be treated as "absent"
		case "azp-empty":
			claims["azp"] = "" // present-but-empty must be rejected
		case "aud-array":
			claims["aud"] = []string{"test-client"} // single-string audience required
		case "expiry":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		case "subject":
			claims["sub"] = ""
		case "email_verified":
			claims["email_verified"] = false
		case "email":
			claims["email"] = "not-an-email"
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key"
		raw, _ := token.SignedString(g.key)
		if g.badClaim == "signature" {
			other, _ := rsa.GenerateKey(rand.Reader, 2048)
			raw, _ = token.SignedString(other)
		}
		return body(200, map[string]string{"id_token": raw}), nil
	case "https://www.googleapis.com/oauth2/v3/certs":
		n := base64.RawURLEncoding.EncodeToString(g.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(g.key.E)).Bytes())
		return body(200, map[string]any{"keys": []any{map[string]string{"kid": "test-key", "kty": "RSA", "alg": "RS256", "n": n, "e": e}}}), nil
	default:
		return body(404, map[string]string{"error": "unexpected_endpoint"}), nil
	}
}
func digestTest(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func googleBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func googleStart(t *testing.T, ts *testServer, c *http.Client, g *googleTransport) (string, string) {
	t.Helper()
	providers := do(t, c, "GET", ts.URL+"/api/v1/auth/providers", nil, "")
	if providers.status != 200 || providers.body["googleEnabled"] != true || providers.body["evaluationEnabled"] != false || len(providers.body) != 2 {
		t.Fatalf("provider response: %d %v", providers.status, providers.body)
	}
	r := do(t, c, "GET", ts.URL+"/api/v1/auth/google/start", nil, "")
	if r.status != 302 {
		t.Fatalf("start %d %v", r.status, r.body)
	}
	loc, _ := url.Parse(r.header.Get("Location"))
	if loc.Scheme != "https" || loc.Host != "accounts.google.com" || loc.Query().Get("redirect_uri") != ts.cfg.GoogleCallbackURI || loc.Query().Get("code_challenge_method") != "S256" || loc.Query().Get("scope") != "openid email profile" {
		t.Fatalf("authorization URL invalid")
	}
	g.nonce = loc.Query().Get("nonce")
	g.challenge = loc.Query().Get("code_challenge")
	g.callback = ts.cfg.GoogleCallbackURI
	return loc.Query().Get("state"), g.nonce
}
func googleCallback(t *testing.T, ts *testServer, c *http.Client, state string) apiResp {
	t.Helper()
	return do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?state="+url.QueryEscape(state)+"&code=good-code", nil, "")
}

func TestGooglePublicFlowAndReplay(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &googleTransport{key: key, subject: "subject-1", email: uniqueEmail()}
	ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
	c := googleBrowser(t)
	state, _ := googleStart(t, ts, c, g)
	wrong := googleBrowser(t)
	bad := googleCallback(t, ts, wrong, state)
	if bad.status != 303 || !strings.Contains(bad.header.Get("Location"), "invalid_request") {
		t.Fatalf("wrong browser %d %s", bad.status, bad.header.Get("Location"))
	}
	done := googleCallback(t, ts, c, state)
	if done.status != 303 || done.header.Get("Location") != "/auth/google/complete" || refreshCookieValue(done.header) == "" {
		t.Fatalf("callback %d %s", done.status, done.header.Get("Location"))
	}
	for _, sensitive := range []string{state, g.nonce, "good-code", "test-secret"} {
		if strings.Contains(done.raw, sensitive) || strings.Contains(done.header.Get("Location"), sensitive) || strings.Contains(ts.logs.String(), sensitive) {
			t.Fatal("callback response or application log exposed OAuth material")
		}
	}
	refresh := do(t, c, "POST", ts.URL+"/api/v1/auth/refresh", nil, "")
	if refresh.status != 200 {
		t.Fatalf("refresh %d %v", refresh.status, refresh.body)
	}
	access := refresh.body["accessToken"].(string)
	me := meWithToken(t, ts, access)
	if me.status != 200 {
		t.Fatalf("me %d", me.status)
	}
	b := createBroadcast(t, ts, access, "Google broadcast")
	if b["ownerId"] != me.body["user"].(map[string]any)["id"] {
		t.Fatalf("owner mismatch")
	}
	replay := googleCallback(t, ts, c, state)
	if !strings.Contains(replay.header.Get("Location"), "invalid_request") || g.exchangeCount != 1 {
		t.Fatalf("replay exchanged: %d", g.exchangeCount)
	}
	logout := do(t, c, "POST", ts.URL+"/api/v1/auth/logout", nil, access)
	if logout.status != 204 {
		t.Fatalf("logout %d", logout.status)
	}
	state, _ = googleStart(t, ts, c, g)
	again := googleCallback(t, ts, c, state)
	if again.header.Get("Location") != "/auth/google/complete" {
		t.Fatalf("repeat %s", again.header.Get("Location"))
	}
}

func TestGoogleIdentityEmailChangeAndCollisions(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &googleTransport{key: key, subject: "stable-subject", email: uniqueEmail()}
	ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
	c := googleBrowser(t)
	state, _ := googleStart(t, ts, c, g)
	first := googleCallback(t, ts, c, state)
	if first.header.Get("Location") != "/auth/google/complete" {
		t.Fatalf("first login: %s", first.header.Get("Location"))
	}
	refreshed := do(t, c, "POST", ts.URL+"/api/v1/auth/refresh", nil, "")
	firstID := refreshed.body["user"].(map[string]any)["id"]
	g.email = uniqueEmail()
	state, _ = googleStart(t, ts, c, g)
	again := googleCallback(t, ts, c, state)
	if again.header.Get("Location") != "/auth/google/complete" {
		t.Fatalf("repeat login: %s", again.header.Get("Location"))
	}
	newCookie := refreshCookieValue(again.header)
	same := refreshWithToken(t, ts, newCookie)
	if same.body["user"].(map[string]any)["id"] != firstID {
		t.Fatal("subject changed user on provider email change")
	}
	localEmail := uniqueEmail()
	registerUser(t, ts, newJarClient(t), localEmail)
	g.subject = "new-subject"
	g.email = localEmail
	state, _ = googleStart(t, ts, c, g)
	conflict := googleCallback(t, ts, c, state)
	if conflict.header.Get("Location") != "/auth/google/complete?error=account_exists" {
		t.Fatalf("local collision: %s", conflict.header.Get("Location"))
	}
	g.email = uniqueEmail()
	state, _ = googleStart(t, ts, c, g)
	created := googleCallback(t, ts, c, state)
	if created.header.Get("Location") != "/auth/google/complete" {
		t.Fatalf("second identity: %s", created.header.Get("Location"))
	}
	g.subject = "different-subject"
	state, _ = googleStart(t, ts, c, g)
	other := googleCallback(t, ts, c, state)
	if other.header.Get("Location") != "/auth/google/complete?error=account_exists" {
		t.Fatalf("other subject collision: %s", other.header.Get("Location"))
	}
	// A Google-only account has NULL password_hash; password login is 401.
	login := do(t, newJarClient(t), "POST", ts.URL+"/api/v1/auth/login", map[string]any{"email": g.email, "password": testPassword}, "")
	if login.status != 401 {
		t.Fatalf("OIDC password login: %d", login.status)
	}
}

func TestGoogleRejectsBadClaimsAndConsumesState(t *testing.T) {
	for _, bad := range []string{"nonce", "issuer", "audience", "azp", "azp-wrong-type", "azp-empty", "aud-array", "signature", "expiry", "subject", "email_verified", "email"} {
		t.Run(bad, func(t *testing.T) {
			key, _ := rsa.GenerateKey(rand.Reader, 2048)
			g := &googleTransport{key: key, subject: "sub-" + bad, email: uniqueEmail(), badClaim: bad}
			ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
			c := googleBrowser(t)
			state, _ := googleStart(t, ts, c, g)
			got := googleCallback(t, ts, c, state)
			if got.header.Get("Location") != "/auth/google/complete?error=unavailable" || refreshCookieValue(got.header) != "" {
				t.Fatalf("accepted bad claim: %s", got.header.Get("Location"))
			}
			again := googleCallback(t, ts, c, state)
			if !strings.Contains(again.header.Get("Location"), "invalid_request") || g.exchangeCount != 1 {
				t.Fatalf("state reused: %d", g.exchangeCount)
			}
		})
	}
}

func TestGoogleMalformedCallbackAndRedisOutage(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &googleTransport{key: key, subject: "subject", email: uniqueEmail()}
	ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
	c := googleBrowser(t)
	state, _ := googleStart(t, ts, c, g)
	if r := do(t, c, "GET", ts.URL+"/api/v1/auth/google/start?next=https://evil.example", nil, ""); r.status != 400 {
		t.Fatalf("external redirect: %d", r.status)
	}
	if r := do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?state="+state+"altered&code=good-code", nil, ""); !strings.Contains(r.header.Get("Location"), "invalid_request") {
		t.Fatal("modified state accepted")
	}
	for _, query := range []string{
		"state=" + state + "&state=" + state + "&code=good-code",
		"state=" + state + "&code=good-code&code=other",
		"state=" + state + "&code=good-code&error=access_denied",
		"state=" + state + "&code=good-code&error=",          // empty error must be rejected (S2)
		"state=" + state + "&code=good-code&code=%ZZ",        // parse error must surface (S2)
		"state=" + state + "&code=good-code&foo=%ZZ",         // parse error in an extra param (S2)
		"state=" + state + "&code=good-code&scope=a&scope=b", // duplicate extra rejected
	} {
		r := do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?"+query, nil, "")
		if r.header.Get("Location") != "/auth/google/complete?error=invalid_request" {
			t.Fatalf("malformed callback accepted: %s", r.header.Get("Location"))
		}
	}
	// Malformed requests do not consume the state; provider description is ignored.
	cancel := do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?state="+state+"&error=access_denied&error_description=secret-provider-text", nil, "")
	if cancel.header.Get("Location") != "/auth/google/complete?error=cancelled" || strings.Contains(cancel.header.Get("Location"), "secret-provider-text") {
		t.Fatalf("cancel: %s", cancel.header.Get("Location"))
	}
	if strings.Contains(cancel.raw, "secret-provider-text") || strings.Contains(ts.logs.String(), "secret-provider-text") || strings.Contains(ts.logs.String(), state) {
		t.Fatal("provider error or callback query leaked")
	}
	outage := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) {
		c.GoogleClientID = "test-client"
		c.GoogleClientSecret = "test-secret"
		c.RedisAddr = "127.0.0.1:1"
		c.RateLimitRequests = 0
	})
	state, _ = googleStart(t, ts, c, g)
	callbackOutage := do(t, c, "GET", outage.URL+"/api/v1/auth/google/callback?state="+state+"&code=good-code", nil, "")
	if callbackOutage.status != 503 || refreshCookieValue(callbackOutage.header) != "" {
		t.Fatalf("redis callback outage: %d", callbackOutage.status)
	}
	if recovered := googleCallback(t, ts, c, state); recovered.header.Get("Location") != "/auth/google/complete" {
		t.Fatal("redis outage consumed healthy state")
	}
	failed := do(t, googleBrowser(t), "GET", outage.URL+"/api/v1/auth/google/start", nil, "")
	if failed.status != 503 || refreshCookieValue(failed.header) != "" {
		t.Fatalf("redis outage: %d", failed.status)
	}
}

func TestGoogleConcurrentFirstLoginConverges(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	email := uniqueEmail()
	g1 := &googleTransport{key: key, subject: "same-concurrent-subject", email: email}
	g2 := &googleTransport{key: key, subject: g1.subject, email: email}
	opts := func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" }
	ts1 := newTestServerWithGoogle(t, &http.Client{Transport: g1, Timeout: 5 * time.Second}, opts)
	ts2 := newTestServerWithGoogle(t, &http.Client{Transport: g2, Timeout: 5 * time.Second}, opts)
	c1, c2 := googleBrowser(t), googleBrowser(t)
	state1, _ := googleStart(t, ts1, c1, g1)
	state2, _ := googleStart(t, ts2, c2, g2)
	var wg sync.WaitGroup
	wg.Add(2)
	locations := make([]string, 2)
	errs := make([]error, 2)
	go func() {
		defer wg.Done()
		resp, e := c1.Get(ts1.URL + "/api/v1/auth/google/callback?state=" + state1 + "&code=good-code")
		errs[0] = e
		if e == nil {
			locations[0] = resp.Header.Get("Location")
			resp.Body.Close()
		}
	}()
	go func() {
		defer wg.Done()
		resp, e := c2.Get(ts2.URL + "/api/v1/auth/google/callback?state=" + state2 + "&code=good-code")
		errs[1] = e
		if e == nil {
			locations[1] = resp.Header.Get("Location")
			resp.Body.Close()
		}
	}()
	wg.Wait()
	for i := range locations {
		if errs[i] != nil || locations[i] != "/auth/google/complete" {
			t.Fatalf("concurrent callback %d: %s %v", i, locations[i], errs[i])
		}
	}
	r1 := do(t, c1, "POST", ts1.URL+"/api/v1/auth/refresh", nil, "")
	r2 := do(t, c2, "POST", ts2.URL+"/api/v1/auth/refresh", nil, "")
	if r1.status != 200 || r2.status != 200 || r1.body["user"].(map[string]any)["id"] != r2.body["user"].(map[string]any)["id"] {
		t.Fatalf("identity split: %v %v", r1.body, r2.body)
	}
}

func TestGoogleCallbackAcceptsRealGoogleExtras(t *testing.T) {
	// Real Google callbacks carry scope/authuser/prompt/hd/session_state and
	// may carry other unrecognized parameters; per Google's code-model
	// guidance they are IGNORED (never used for auth/redirect/linking
	// decisions, never logged) while malformed security inputs stay rejected.
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &googleTransport{key: key, subject: "subject-extras", email: uniqueEmail()}
	ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
	c := googleBrowser(t)
	state, _ := googleStart(t, ts, c, g)
	extra := "&scope=" + url.QueryEscape("openid email profile") + "&authuser=0&prompt=consent&hd=example.com&session_state=abc123&next=https://evil.example&unrecognized=whatever"
	done := do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?state="+url.QueryEscape(state)+"&code=good-code"+extra, nil, "")
	if done.status != 303 || done.header.Get("Location") != "/auth/google/complete" || refreshCookieValue(done.header) == "" {
		t.Fatalf("real-form callback rejected: %d %s", done.status, done.header.Get("Location"))
	}
	if strings.Contains(done.header.Get("Location"), "evil.example") || strings.Contains(done.raw, "evil.example") || strings.Contains(ts.logs.String(), "evil.example") || strings.Contains(ts.logs.String(), "unrecognized") {
		t.Fatal("extra callback parameter influenced redirect/response or was logged")
	}
	refresh := do(t, c, "POST", ts.URL+"/api/v1/auth/refresh", nil, "")
	if refresh.status != 200 {
		t.Fatalf("refresh after extras callback: %d", refresh.status)
	}
}

func TestGoogleStateExpiryExchangeFailureAndConcurrentReplay(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &googleTransport{key: key, subject: "state-test", email: uniqueEmail()}
	ts := newTestServerWithGoogle(t, &http.Client{Transport: g, Timeout: 5 * time.Second}, func(c *config.Config) { c.GoogleClientID = "test-client"; c.GoogleClientSecret = "test-secret" })
	c := googleBrowser(t)
	state, _ := googleStart(t, ts, c, g)
	if err := testRedis.Del(t.Context(), "wbb:google:state:"+digestTest(state)).Err(); err != nil {
		t.Fatal(err)
	}
	expired := googleCallback(t, ts, c, state)
	if !strings.Contains(expired.header.Get("Location"), "invalid_request") {
		t.Fatal("expired state accepted")
	}
	state, _ = googleStart(t, ts, c, g)
	badCode := do(t, c, "GET", ts.URL+"/api/v1/auth/google/callback?state="+state+"&code=bad-code", nil, "")
	if badCode.header.Get("Location") != "/auth/google/complete?error=unavailable" {
		t.Fatalf("exchange failure: %s", badCode.header.Get("Location"))
	}
	if replay := googleCallback(t, ts, c, state); !strings.Contains(replay.header.Get("Location"), "invalid_request") {
		t.Fatal("failed exchange state reused")
	}
	state, _ = googleStart(t, ts, c, g)
	var wg sync.WaitGroup
	wg.Add(2)
	locations := make([]string, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			resp, e := c.Get(ts.URL + "/api/v1/auth/google/callback?state=" + state + "&code=good-code")
			if e == nil {
				locations[i] = resp.Header.Get("Location")
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	good, bad := 0, 0
	for _, loc := range locations {
		if loc == "/auth/google/complete" {
			good++
		} else if loc == "/auth/google/complete?error=invalid_request" {
			bad++
		}
	}
	if good != 1 || bad != 1 {
		t.Fatalf("concurrent callback outcomes: %v", locations)
	}
}
