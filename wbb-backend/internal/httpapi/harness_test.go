package httpapi_test

// Integration harness: every test exercises the public HTTP seam of a real
// server instance backed by real PostgreSQL and Redis (agreed test boundary).
// Each run gets a fresh database; tests share it but use unique users.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/broadcasts"
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/httpapi"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
)

var (
	testPool      *pgxpool.Pool
	testRedis     redis.UniversalClient
	testRedisAddr string
	testDSN       string
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	adminURL := os.Getenv("WBB_TEST_PG_ADMIN_URL")
	if adminURL == "" {
		adminURL = "postgres://wbb:wbb-dev-password@127.0.0.1:55432/postgres"
	}

	// Redis under test is disposable by construction: either an explicit
	// WBB_TEST_REDIS_ADDR (CI service container, dedicated to this run) or a
	// throwaway docker container started below. Nothing shared is ever flushed.
	redisAddr, stopRedis, err := startTestRedis()
	if err != nil {
		fmt.Fprintf(os.Stderr, "test redis: %v\n", err)
		os.Exit(1)
	}
	defer stopRedis()
	testRedisAddr = redisAddr

	// The admin URL contains credentials; never print it verbatim.
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach test PostgreSQL; run `make deps-up` first: %v\n", redactPGError(err))
		os.Exit(1)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	dbName := "wbb_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		fmt.Fprintf(os.Stderr, "create test database: %v\n", err)
		os.Exit(1)
	}
	testDSN = strings.Replace(adminURL, "/postgres", "/"+dbName, 1)

	testPool, err = pgxpool.New(ctx, testDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect test database: %v\n", err)
		os.Exit(1)
	}
	if err := db.Migrate(ctx, testPool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate test database: %v\n", err)
		os.Exit(1)
	}

	testRedis = redis.NewClient(&redis.Options{Addr: redisAddr})
	for attempt := 0; ; attempt++ {
		if err := testRedis.Ping(ctx).Err(); err == nil {
			break
		} else if attempt >= 50 {
			fmt.Fprintf(os.Stderr, "cannot reach test redis at %s: %v\n", redisAddr, err)
			os.Exit(1)
		}
		time.Sleep(100 * time.Millisecond)
	}

	code := m.Run()

	testPool.Close()
	_, _ = admin.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
	admin.Close()
	_ = testRedis.Close()
	os.Exit(code)
}

// startTestRedis returns an address of a disposable Redis: the explicit
// WBB_TEST_REDIS_ADDR when set (CI), otherwise a fresh docker container on a
// random loopback port that is stopped by the returned cleanup func.
func startTestRedis() (string, func(), error) {
	if addr := os.Getenv("WBB_TEST_REDIS_ADDR"); addr != "" {
		return addr, func() {}, nil
	}
	out, err := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1::6379", "redis:7-alpine").Output()
	if err != nil {
		return "", nil, fmt.Errorf("starting disposable redis (docker required): %w: %s", err, strings.TrimSpace(string(out)))
	}
	containerID := strings.TrimSpace(string(out))
	stop := func() { _ = exec.Command("docker", "stop", containerID).Run() }

	portOut, err := exec.Command("docker", "inspect", "--format",
		`{{(index (index .NetworkSettings.Ports "6379/tcp") 0).HostPort}}`, containerID).Output()
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("inspecting disposable redis port: %w", err)
	}
	addr := "127.0.0.1:" + strings.TrimSpace(string(portOut))
	return addr, stop, nil
}

// redactPGError keeps connection diagnostics without echoing the DSN.
func redactPGError(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "postgres://"); i >= 0 {
		msg = msg[:i] + "<dsn redacted>"
	}
	return errors.New(msg)
}

type testServer struct {
	*httptest.Server
	cfg config.Config
	api *httpapi.Server
}

type serverOpt func(*config.Config)

// withFixedSecret keeps the same JWT secret across server instances so tokens
// survive "restart" tests.
func withFixedSecret(secret []byte) serverOpt {
	return func(c *config.Config) { c.JWTSecret = secret; c.Env = "dev" }
}

// withBadRedis points the server at an unreachable Redis to simulate an
// outage (fail-closed checks).
func withBadRedis() serverOpt {
	return func(c *config.Config) { c.RedisAddr = "127.0.0.1:1" }
}

func withRateLimit(requests int64) serverOpt {
	return func(c *config.Config) { c.RateLimitRequests = requests; c.RateLimitWindow = time.Minute }
}

func withAccountRateLimit(requests int64) serverOpt {
	return func(c *config.Config) { c.RateLimitAccountRequests = requests; c.RateLimitWindow = time.Minute }
}

func withCORSOrigins(origins ...string) serverOpt {
	return func(c *config.Config) { c.CORSOrigins = origins }
}

func newTestServer(t *testing.T, opts ...serverOpt) *testServer {
	t.Helper()
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	cfg := config.Config{
		Env:               "dev",
		Addr:              "",
		DatabaseURL:       testDSN,
		RedisAddr:         testRedisAddr,
		JWTSecret:         secret,
		AccessTokenTTL:    10 * time.Minute,
		RefreshTokenTTL:   7 * 24 * time.Hour,
		RateLimitRequests: 0, // off by default so tests are deterministic
	}
	for _, o := range opts {
		o(&cfg)
	}

	// The server's own origin is always allowed so "same-origin browser"
	// requests work; tests add extra origins via withCORSOrigins.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverURL := "http://" + listener.Addr().String()
	if !slices.Contains(cfg.CORSOrigins, serverURL) {
		cfg.CORSOrigins = append(cfg.CORSOrigins, serverURL)
	}

	// Servers may use an isolated Redis client (e.g. outage simulation).
	rdb := testRedis
	if cfg.RedisAddr != testRedisAddr {
		rdb = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
		t.Cleanup(func() { _ = rdb.Close() })
	}

	// Reset rate-limit counters so tests with different limits don't share
	// windows (session keys under wbb:sess:* are untouched).
	scanCtx, scanCancel := context.WithTimeout(t.Context(), 5*time.Second)
	iter := testRedis.Scan(scanCtx, 0, "wbb:rl:*", 100).Iterator()
	var rlKeys []string
	for iter.Next(scanCtx) {
		rlKeys = append(rlKeys, iter.Val())
	}
	if len(rlKeys) > 0 {
		testRedis.Del(scanCtx, rlKeys...)
	}
	scanCancel()

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTokenTTL)
	authSvc := auth.NewService(testPool, rdb, tokens, cfg.RefreshTokenTTL)
	bcastSvc := broadcasts.NewService(testPool)
	api := httpapi.NewServer(cfg, authSvc, bcastSvc, rdb, testPool, slog.New(slog.DiscardHandler))
	ts := httptest.NewUnstartedServer(api.Handler())
	ts.Listener = listener
	ts.Start()
	t.Cleanup(ts.Close)
	return &testServer{Server: ts, cfg: cfg, api: api}
}

// freezeRateClock pins the rate-limit window clock mid-window so fixed-window
// bucket boundaries cannot flake assertions.
func freezeRateClock(ts *testServer) {
	ts.api.SetRateLimitClock(func() time.Time {
		return time.Date(2026, 11, 6, 12, 0, 30, 0, time.UTC)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- HTTP helpers ---------------------------------------------------------

type apiResp struct {
	status int
	header http.Header
	body   map[string]any
}

func (r apiResp) errCode() string {
	if r.body == nil {
		return ""
	}
	if e, ok := r.body["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func do(t *testing.T, client *http.Client, method, url string, payload any, bearer string) apiResp {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Browsers always send Origin on state-changing requests; mirror that so
	// origin validation is exercised on every auth POST.
	if method != http.MethodGet {
		req.Header.Set("Origin", originOf(url))
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	out := apiResp{status: resp.StatusCode, header: resp.Header}
	if len(raw) > 0 && strings.Contains(resp.Header.Get("Content-Type"), "json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			t.Fatalf("%s %s: decode body %q: %v", method, url, string(raw), err)
		}
	}
	return out
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

func newJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 15 * time.Second}
}

// --- data helpers ---------------------------------------------------------

var userSeq int

func uniqueEmail() string {
	userSeq++
	return fmt.Sprintf("user%d-%d@example.com", time.Now().UnixNano()%1_000_000, userSeq)
}

const testPassword = "correct-horse-battery"

// registerUser registers and returns the created user map.
func registerUser(t *testing.T, ts *testServer, client *http.Client, email string) map[string]any {
	t.Helper()
	resp := do(t, client, "POST", ts.URL+"/api/v1/auth/register", map[string]any{
		"email":       email,
		"password":    testPassword,
		"displayName": "User " + email,
	}, "")
	if resp.status != http.StatusCreated {
		t.Fatalf("register %s: status=%d body=%v", email, resp.status, resp.body)
	}
	user, _ := resp.body["user"].(map[string]any)
	if user == nil || user["id"] == "" {
		t.Fatalf("register %s: missing user id: %v", email, resp.body)
	}
	return user
}

// loginUser logs in and returns (user, accessToken, refreshCookieValue).
func loginUser(t *testing.T, ts *testServer, client *http.Client, email string) (map[string]any, string, string) {
	t.Helper()
	resp := do(t, client, "POST", ts.URL+"/api/v1/auth/login", map[string]any{
		"email":    email,
		"password": testPassword,
	}, "")
	if resp.status != http.StatusOK {
		t.Fatalf("login %s: status=%d body=%v", email, resp.status, resp.body)
	}
	access, _ := resp.body["accessToken"].(string)
	if access == "" {
		t.Fatalf("login %s: missing accessToken: %v", email, resp.body)
	}
	refresh := refreshCookieValue(resp.header)
	if refresh == "" {
		t.Fatalf("login %s: missing refresh cookie", email)
	}
	user, _ := resp.body["user"].(map[string]any)
	return user, access, refresh
}

func refreshCookieValue(header http.Header) string {
	for _, c := range header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "wbb_refresh=") {
			v := strings.TrimPrefix(c, "wbb_refresh=")
			if i := strings.Index(v, ";"); i >= 0 {
				v = v[:i]
			}
			return v
		}
	}
	return ""
}

// registerAndLogin is the common arrange step.
func registerAndLogin(t *testing.T, ts *testServer, client *http.Client, email string) (map[string]any, string, string) {
	t.Helper()
	user := registerUser(t, ts, client, email)
	u, access, refresh := loginUser(t, ts, client, email)
	if u["id"] != user["id"] {
		t.Fatalf("login returned different user: %v vs %v", u, user)
	}
	return user, access, refresh
}

// refreshWithToken posts the refresh endpoint with an explicit cookie value
// (no shared jar, so rotation tests control exactly which token is used).
func refreshWithToken(t *testing.T, ts *testServer, refreshToken string) apiResp {
	t.Helper()
	req, err := http.NewRequest("POST", ts.URL+"/api/v1/auth/refresh", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "wbb_refresh", Value: refreshToken})
	req.Header.Set("Origin", ts.URL)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	out := apiResp{status: resp.StatusCode, header: resp.Header}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

// meWithToken calls GET /auth/me with a bearer token.
func meWithToken(t *testing.T, ts *testServer, token string) apiResp {
	t.Helper()
	return do(t, &http.Client{Timeout: 15 * time.Second}, "GET", ts.URL+"/api/v1/auth/me", nil, token)
}

func createBroadcast(t *testing.T, ts *testServer, token, title string) map[string]any {
	t.Helper()
	resp := do(t, &http.Client{Timeout: 15 * time.Second}, "POST", ts.URL+"/api/v1/broadcasts",
		map[string]any{"title": title, "description": title + " 설명"}, token)
	if resp.status != http.StatusCreated {
		t.Fatalf("create broadcast: status=%d body=%v", resp.status, resp.body)
	}
	b, _ := resp.body["broadcast"].(map[string]any)
	return b
}
