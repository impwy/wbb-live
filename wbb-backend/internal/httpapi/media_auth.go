package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

type mediaAuthRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Path     string `json:"path"`
}

func (s *Server) handleMediaMTXAuth(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.MediaSecret) < 32 {
		http.Error(w, "unauthorized", 401)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var req mediaAuthRequest
	if err = json.Unmarshal(raw, &req); err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if req.Protocol == "hls" {
		s.log.Info("hls auth probe", "action", req.Action, "credential_kind", mediaCredentialKind(req.User))
	}
	if req.Action == "publish" && req.Protocol == "webrtc" && strings.HasPrefix(req.User, "create.") {
		opID := strings.TrimPrefix(req.User, "create.")
		want := mediaCredential(s.cfg.MediaSecret, "create", opID)
		if subtle.ConstantTimeCompare([]byte(req.Password), []byte(want)) == 1 {
			ok, err := s.sessions.AllowMediaCreate(r.Context(), opID, req.Path)
			if err == nil && ok {
				w.WriteHeader(200)
				return
			}
		}
	}
	if strings.HasPrefix(req.User, "worker.") {
		parts := strings.Split(req.User, ".")
		if len(parts) == 3 {
			fence, parseErr := strconv.ParseInt(parts[2], 10, 64)
			want := mediaCredential(s.cfg.MediaSecret, "worker", parts[1]+":"+parts[2])
			if parseErr == nil && subtle.ConstantTimeCompare([]byte(req.Password), []byte(want)) == 1 {
				ok, err := s.sessions.AllowWorkerMedia(r.Context(), parts[1], fence, req.Action, req.Protocol, req.Path)
				if err == nil && ok {
					w.WriteHeader(200)
					return
				}
			}
		}
	}
	if req.User == "control" && req.Action == "api" && subtle.ConstantTimeCompare([]byte(req.Password), []byte(mediaCredential(s.cfg.MediaSecret, "control", "api"))) == 1 {
		w.WriteHeader(200)
		return
	}
	if req.User == "" && req.Password == "" && req.Protocol == "hls" && req.Action == "read" {
		parts := strings.Split(strings.TrimPrefix(req.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "wbb" && parts[3] == "720p" {
			gen, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				ok, err := s.sessions.AllowHLS(r.Context(), parts[1], gen)
				if err == nil && ok {
					w.WriteHeader(200)
					return
				}
			}
		}
	}
	s.log.Log(r.Context(), slog.LevelWarn, "media authentication denied", "action", req.Action, "protocol", req.Protocol, "credential_kind", mediaCredentialKind(req.User))
	http.Error(w, "unauthorized", 401)
}

func mediaCredentialKind(user string) string {
	switch {
	case strings.HasPrefix(user, "worker."):
		return "worker"
	case strings.HasPrefix(user, "create."):
		return "create"
	case user == "":
		return "anonymous"
	default:
		return "other"
	}
}
