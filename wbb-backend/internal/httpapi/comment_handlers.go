package httpapi

import (
	"net/http"
	"strconv"
)

type commentRequest struct {
	Body string `json:"body"`
}

// GET /api/v1/broadcasts/{id}/comments?limit= (public).
func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.comments.List(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// POST /api/v1/broadcasts/{id}/comments (signed in).
func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	c, err := s.comments.Create(r.Context(), authUser(r).ID, r.PathValue("id"), req.Body)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": c})
}

// PATCH /api/v1/comments/{cid} (author only).
func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	c, err := s.comments.Update(r.Context(), authUser(r).ID, r.PathValue("cid"), req.Body)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": c})
}

// DELETE /api/v1/comments/{cid} (author only).
func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	if err := s.comments.Delete(r.Context(), authUser(r).ID, r.PathValue("cid")); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
