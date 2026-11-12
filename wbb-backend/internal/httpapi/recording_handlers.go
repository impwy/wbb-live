package httpapi

import (
	"net/http"
	"strconv"
)

// GET /api/v1/recordings (public): recent replays.
func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.recordings.List(r.Context(), "", limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/broadcasts/{id}/recordings (public).
func (s *Server) handleBroadcastRecordings(w http.ResponseWriter, r *http.Request) {
	items, err := s.recordings.List(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/recordings/{rid} (public).
func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	rec, err := s.recordings.Get(r.Context(), r.PathValue("rid"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recording": rec})
}

// GET /api/v1/recordings/{rid}/media (public): the MP4 with Range support.
func (s *Server) handleRecordingMedia(w http.ResponseWriter, r *http.Request) {
	f, info, err := s.recordings.Open(r.Context(), r.PathValue("rid"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
