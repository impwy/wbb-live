package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/broadcasts"
	"github.com/impwy/wbb-live/wbb-backend/internal/livesessions"
)

// apiError is the public error envelope (docs/design.md §5):
// {"error":{"code","message","requestId"}}. It never contains SQL, stack
// traces or token material.
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID(r),
	}})
}

// writeDomainError maps module errors onto HTTP status codes. Unknown errors
// become an opaque 500 so internals never leak.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	var invalidInput *auth.InvalidInputError
	switch {
	case errors.As(err, &invalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_input", invalidInput.Error())
		return
	}
	var bInvalidInput *broadcasts.InvalidInputError
	switch {
	case errors.As(err, &bInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_input", bInvalidInput.Error())
		return
	}

	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, r, http.StatusConflict, "email_taken", "This email is already registered.")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
	case errors.Is(err, auth.ErrUnauthorized), errors.Is(err, auth.ErrTokenInvalid):
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
	case errors.Is(err, auth.ErrSessionRevoked):
		writeError(w, r, http.StatusUnauthorized, "session_revoked", "The session was revoked. Please sign in again.")
	case errors.Is(err, auth.ErrStoreUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "session_store_unavailable", "The session store is temporarily unavailable.")
	case errors.Is(err, broadcasts.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "Broadcast not found.")
	case errors.Is(err, broadcasts.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "Only the broadcast owner can do this.")
	case errors.Is(err, broadcasts.ErrVersionConflict):
		writeError(w, r, http.StatusConflict, "version_conflict", "The broadcast was changed by someone else. Reload and try again.")
	case errors.Is(err, broadcasts.ErrActiveSession):
		writeError(w, r, http.StatusConflict, "active_session", "Stop the active session before deleting the broadcast.")
	case errors.Is(err, livesessions.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "Broadcast or session not found.")
	case errors.Is(err, livesessions.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "Only the broadcast owner or original login can do this.")
	case errors.Is(err, livesessions.ErrConflict), errors.Is(err, livesessions.ErrGrantUnavailable):
		writeError(w, r, http.StatusConflict, "session_conflict", "The broadcast session is not available for this operation.")
	case errors.Is(err, livesessions.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_input", "Invalid source or Idempotency-Key.")
	case errors.Is(err, errOrigin):
		writeError(w, r, http.StatusForbidden, "invalid_origin", "Request origin is not allowed.")
	case errors.Is(err, errRateLimited):
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
	}
}
