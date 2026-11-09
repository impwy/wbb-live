package httpapi

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/impwy/wbb-live/wbb-backend/internal/livesessions"
)

var hlsFile = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	sid, gen, err := s.sessions.Playback(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"sessionId": sid, "masterUrl": livesessions.HLSMasterURL(sid, gen)})
}

func (s *Server) handleHLSAuthorize(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.MediaSecret) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-WBB-Media-Secret")), s.cfg.MediaSecret) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	original := r.Header.Get("X-WBB-Original-URI")
	if strings.ContainsAny(original, "\\\x00\r\n") || strings.Contains(strings.ToLower(original), "%2f") || strings.Contains(strings.ToLower(original), "%5c") {
		http.Error(w, "forbidden", 403)
		return
	}
	u, err := url.ParseRequestURI(original)
	if err != nil || u.IsAbs() || u.RawPath != "" {
		http.Error(w, "forbidden", 403)
		return
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "media" || parts[1] != "wbb" || parts[4] != "720p" || !hlsFile.MatchString(parts[5]) || parts[5] == ".." {
		http.Error(w, "forbidden", 403)
		return
	}
	gen, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		http.Error(w, "forbidden", 403)
		return
	}
	allowed, err := s.sessions.AllowHLS(r.Context(), parts[2], gen)
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	if !allowed {
		http.Error(w, "forbidden", 403)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
}
