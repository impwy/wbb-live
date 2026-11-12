package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/redis/go-redis/v9"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxAuthUser
	ctxAuthIdentity
)

func requestID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}

// authUser returns the authenticated user placed by requireAuth.
func authUser(r *http.Request) auth.User {
	u, _ := r.Context().Value(ctxAuthUser).(auth.User)
	return u
}

type authIdentity struct{ sessionID, familyID string }

func identity(r *http.Request) authIdentity {
	v, _ := r.Context().Value(ctxAuthIdentity).(authIdentity)
	return v
}

var errOrigin = errors.New("origin not allowed")

// withRequestID assigns a short random request id, exposes it via the
// X-Request-Id header and logs the finished request.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(ctx))
		if r.URL.Path == "/api/v1/health/live" || r.URL.Path == "/api/v1/health/ready" {
			return // keep probe logs quiet
		}
		s.log.Info("http_request",
			"requestId", id,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// recoverPanic converts handler panics into an opaque 500.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "requestId", requestID(r), "panic", rec)
				writeError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withCORS emits CORS headers for explicitly allowed origins only. No
// wildcard origins are ever allowed because refresh cookies need credentials.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(s.cfg.CORSOrigins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth authenticates the Authorization: Bearer access JWT via the auth
// module (Redis cache fail-closed + PostgreSQL ledger as final authority).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
			return
		}
		user, err := s.auth.VerifyRequest(r.Context(), token)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		sid, fid, err := s.auth.AccessIdentity(token)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxAuthUser, user)
		ctx = context.WithValue(ctx, ctxAuthIdentity, authIdentity{sessionID: sid, familyID: fid})
		next(w, r.WithContext(ctx))
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// originAllowed enforces exact Origin validation for browser-driven auth
// POSTs: the full Origin value (scheme + host + port) must match a
// configured allowed origin exactly. Missing Origin, unknown origins and
// scheme mismatches are rejected (docs/design.md §4).
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return slices.Contains(s.cfg.CORSOrigins, origin)
}

func requireOrigin(s *Server, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.originAllowed(r) {
			writeDomainError(w, r, errOrigin)
			return
		}
		next(w, r)
	}
}

// errRateLimited is returned by the fixed-window limiter.
var errRateLimited = errors.New("rate limited")

// rateLimit applies a per-IP fixed window over Redis for sensitive auth
// endpoints. Redis outages fail closed (503) to avoid unbounded attempts.
func (s *Server) rateLimit(kind string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.RateLimitRequests > 0 {
			ok, retryAfter, err := checkRateLimit(r.Context(), s.redis, kind, clientIP(r),
				s.cfg.RateLimitRequests, s.cfg.RateLimitWindow, s.rateLimitClock())
			if err != nil {
				writeError(w, r, http.StatusServiceUnavailable, "session_store_unavailable", "The session store is temporarily unavailable.")
				return
			}
			if !ok {
				w.Header().Set("Retry-After", retryAfter)
				writeDomainError(w, r, errRateLimited)
				return
			}
		}
		next(w, r)
	}
}

// enforceAccountLimit applies the per-account fixed window (in addition to
// the per-IP middleware) for sensitive auth endpoints. accountKey is the
// normalized email for register/login and the token hash for refresh, so
// distributed-IP attempts against one account are also capped. Redis
// outages fail closed.
func (s *Server) enforceAccountLimit(w http.ResponseWriter, r *http.Request, kind, accountKey string) bool {
	if s.cfg.RateLimitAccountRequests <= 0 || accountKey == "" {
		return true
	}
	ok, retryAfter, err := checkRateLimit(r.Context(), s.redis, kind+"-account", accountKey,
		s.cfg.RateLimitAccountRequests, s.cfg.RateLimitWindow, s.rateLimitClock())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "session_store_unavailable", "The session store is temporarily unavailable.")
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", retryAfter)
		writeDomainError(w, r, errRateLimited)
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	// No trusted proxy chain in front of the API in dev; when deployed behind
	// Caddy, replace this with trusted X-Forwarded-For handling.
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func checkRateLimit(ctx context.Context, rdb redis.UniversalClient, kind, ip string, limit int64, window time.Duration, now time.Time) (bool, string, error) {
	nowSec := now.Unix()
	windowSec := int64(window.Seconds())
	idx := nowSec / windowSec
	key := "wbb:rl:" + kind + ":" + ip + ":" + itoa(idx)
	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, "", err
	}
	if count == 1 {
		rdb.Expire(ctx, key, time.Duration((idx+1)*windowSec-nowSec)*time.Second+time.Second)
	}
	if count > limit {
		retry := time.Duration((idx+1)*windowSec-nowSec) * time.Second
		return false, retry.Truncate(time.Second).String(), nil
	}
	return true, "", nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
