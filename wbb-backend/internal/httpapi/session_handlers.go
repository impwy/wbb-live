package httpapi

import (
	"net/http"

	"github.com/impwy/wbb-live/wbb-backend/internal/livesessions"
)

type prepareRequest struct {
	Source string `json:"source"`
}

func actor(r *http.Request) livesessions.Actor {
	u := authUser(r)
	i := identity(r)
	return livesessions.Actor{UserID: u.ID, FamilyID: i.familyID, AuthSessionID: i.sessionID}
}

func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	var req prepareRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	result, err := s.sessions.Start(r.Context(), actor(r), r.PathValue("id"), req.Source, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleCurrentSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessions.Current(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	sess, ended, err := s.sessions.Stop(r.Context(), actor(r), r.PathValue("id"), r.PathValue("sid"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusAccepted
	if ended {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"session": sess})
}

func (s *Server) handleRenewGrant(w http.ResponseWriter, r *http.Request) {
	grant, err := s.sessions.RenewGrant(r.Context(), actor(r), r.PathValue("id"), r.PathValue("sid"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"publishGrant": grant})
}
