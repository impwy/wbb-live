package httpapi

import (
	"net/http"
	"strconv"

	"github.com/impwy/wbb-live/wbb-backend/internal/broadcasts"
)

type broadcastListResponse struct {
	Items      []broadcasts.Broadcast `json:"items"`
	NextCursor string                 `json:"nextCursor"`
}

type broadcastResponse struct {
	Broadcast broadcasts.Broadcast `json:"broadcast"`
}

type createBroadcastRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type updateBroadcastRequest struct {
	Version     *int64  `json:"version"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

// handleListBroadcasts: GET /api/v1/broadcasts?status=&cursor=&limit= (public).
func (s *Server) handleListBroadcasts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 {
		limit = 20
	}
	items, next, err := s.broadcasts.List(r.Context(), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, broadcastListResponse{Items: items, NextCursor: next})
}

// handleCreateBroadcast: POST /api/v1/broadcasts (authenticated) → 201.
func (s *Server) handleCreateBroadcast(w http.ResponseWriter, r *http.Request) {
	var req createBroadcastRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := authUser(r)
	b, err := s.broadcasts.Create(r.Context(), user.ID, req.Title, req.Description)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, broadcastResponse{Broadcast: b})
}

// handleGetBroadcast: GET /api/v1/broadcasts/{id} (public).
func (s *Server) handleGetBroadcast(w http.ResponseWriter, r *http.Request) {
	b, err := s.broadcasts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, broadcastResponse{Broadcast: b})
}

// handleUpdateBroadcast: PATCH /api/v1/broadcasts/{id} (owner, version).
func (s *Server) handleUpdateBroadcast(w http.ResponseWriter, r *http.Request) {
	var req updateBroadcastRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Version == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_input", "version: required")
		return
	}
	user := authUser(r)
	b, err := s.broadcasts.Update(r.Context(), user.ID, r.PathValue("id"), *req.Version, req.Title, req.Description)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, broadcastResponse{Broadcast: b})
}

// handleDeleteBroadcast: DELETE /api/v1/broadcasts/{id}?version=N (owner).
// version travels as a query parameter because DELETE bodies are awkward
// across HTTP clients; confirm with Codex if a body variant is preferred.
func (s *Server) handleDeleteBroadcast(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_input", "version: must be an integer query parameter")
		return
	}
	user := authUser(r)
	if err := s.broadcasts.Delete(r.Context(), user.ID, r.PathValue("id"), version); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
