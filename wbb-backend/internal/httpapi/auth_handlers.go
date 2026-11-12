package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
)

const refreshCookieName = "wbb_refresh"

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

type userResponse struct {
	User auth.User `json:"user"`
}

type tokenResponse struct {
	AccessToken string    `json:"accessToken"`
	User        auth.User `json:"user"`
}

// handleRegister: POST /api/v1/auth/register → 201 {user}. No auto-login.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if !s.enforceAccountLimit(w, r, "register", strings.ToLower(strings.TrimSpace(req.Email))) {
		return
	}
	user, err := s.auth.Register(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{User: user})
}

// handleLogin: POST /api/v1/auth/login → 200 {accessToken,user} + refresh cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if !s.enforceAccountLimit(w, r, "login", strings.ToLower(strings.TrimSpace(req.Email))) {
		return
	}
	user, session, err := s.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	setRefreshCookie(w, session.RefreshToken, session.ExpiresAt, s.cfg.Env == "prod")
	writeJSON(w, http.StatusOK, tokenResponse{AccessToken: session.AccessToken, User: user})
}

// The evaluation account uses the ordinary credential and session ledger.
// Separate Redis budgets prevent this public, passwordless route from
// consuming the ordinary login budget or being unbounded when it is enabled.
func (s *Server) handleEvaluationLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Evaluation login takes no parameters.")
		return
	}
	account := sha256.Sum256([]byte(s.cfg.EvaluationLoginEmail))
	for _, limit := range []struct {
		kind, key string
		count     int64
	}{
		{"evaluation-ip", clientIP(r), s.cfg.EvaluationLoginIPLimit},
		{"evaluation-account", hex.EncodeToString(account[:]), s.cfg.EvaluationLoginAccountLimit},
	} {
		if limit.count < 1 {
			writeError(w, r, http.StatusServiceUnavailable, "unavailable", "Evaluation login is unavailable.")
			return
		}
		ok, retryAfter, err := checkRateLimit(r.Context(), s.redis, limit.kind, limit.key, limit.count, s.cfg.RateLimitWindow, s.rateLimitClock())
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
	user, session, err := s.auth.Login(r.Context(), s.cfg.EvaluationLoginEmail, s.cfg.EvaluationLoginPassword)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	setRefreshCookie(w, session.RefreshToken, session.ExpiresAt, s.cfg.Env == "prod")
	writeJSON(w, http.StatusOK, tokenResponse{AccessToken: session.AccessToken, User: user})
}

// handleRefresh: POST /api/v1/auth/refresh (HttpOnly cookie) → rotated tokens.
// Per-account limiting uses the stable owner id resolved from the ledger so
// the budget accumulates across rotations; unknown tokens rely on the IP
// limit only.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	token, err := refreshCookie(r)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	owner, err := s.auth.SessionOwner(r.Context(), token)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if owner != "" && !s.enforceAccountLimit(w, r, "refresh", owner) {
		return
	}
	user, session, err := s.auth.Refresh(r.Context(), token)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	setRefreshCookie(w, session.RefreshToken, session.ExpiresAt, s.cfg.Env == "prod")
	writeJSON(w, http.StatusOK, tokenResponse{AccessToken: session.AccessToken, User: user})
}

// handleLogout: POST /api/v1/auth/logout → 204, always idempotent. Both
// presented credentials (refresh cookie and access JWT) are checked
// independently and every live session they reference is revoked, so an
// unknown/stale cookie can never shadow a valid bearer session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var firstErr error
	if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
		if err := s.auth.LogoutByRefreshToken(r.Context(), c.Value); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if access := bearerToken(r); access != "" {
		if err := s.auth.LogoutByAccessToken(r.Context(), access); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	setRefreshCookie(w, "", time.Time{}, s.cfg.Env == "prod")
	if firstErr != nil {
		writeDomainError(w, r, firstErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMe: GET /api/v1/auth/me → 200 {user}.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := authUser(r)
	u, err := s.auth.UserByID(r.Context(), user.ID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userResponse{User: u})
}

func refreshCookie(r *http.Request) (string, error) {
	c, err := r.Cookie(refreshCookieName)
	if err != nil || c.Value == "" {
		return "", auth.ErrUnauthorized
	}
	return c.Value, nil
}

func setRefreshCookie(w http.ResponseWriter, value string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/api/v1/auth",
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// decodeJSON reads a JSON body capped at 64 KiB into dst and writes a 400
// error envelope on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the size limit.")
		case errors.Is(err, io.EOF):
			writeError(w, r, http.StatusBadRequest, "invalid_json", "A JSON request body is required.")
		default:
			writeError(w, r, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		}
		return err
	}
	return nil
}
