package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/livesessions"
)

const maxSDP = 256 << 10

func mediaCredential(secret []byte, kind, id string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(kind + ":" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

func privateMediaClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 10 * time.Second}}
}

func (s *Server) abandonMediaOperation(op livesessions.PublishOperation) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.sessions.AbandonPublish(ctx, op); err != nil {
		s.log.Error("media operation abandonment deferred to worker", "operation", op.ID, "error", err)
	}
}

func (s *Server) discardUpstreamResource(op livesessions.PublishOperation, upstreamPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimSuffix(s.cfg.MediaWHIPURL, "/")+upstreamPath, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth("create."+op.ID, mediaCredential(s.cfg.MediaSecret, "create", op.ID))
	resp, err := s.mediaClient.Do(req)
	if err != nil {
		s.log.Warn("late media resource cleanup deferred to worker", "operation", op.ID, "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 && resp.StatusCode != 404 {
		s.log.Warn("late media resource cleanup deferred to worker", "operation", op.ID, "status", resp.StatusCode)
	}
}

func checkedUpstreamLocation(raw, expectedPrefix, upstreamOrigin string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\\?#\x00\r\n") || strings.Contains(strings.ToLower(raw), "%2f") || strings.Contains(strings.ToLower(raw), "%5c") {
		return "", fmt.Errorf("unsafe Location")
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("path traversal")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.IsAbs() && (u.Scheme != "http" || u.Host != upstreamOrigin) {
		return "", fmt.Errorf("cross origin")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, expectedPrefix+"/") || strings.Contains(u.EscapedPath(), "%") {
		return "", fmt.Errorf("outside resource path")
	}
	return u.Path, nil
}

func (s *Server) handleWHIPPost(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeDomainError(w, r, auth.ErrTokenInvalid)
		return
	}
	claims, err := auth.NewTokenManager(s.cfg.JWTSecret, s.cfg.AccessTokenTTL).VerifyPublish(token)
	if err != nil || claims.SessionID != r.PathValue("sid") {
		writeDomainError(w, r, auth.ErrTokenInvalid)
		return
	}
	if !strings.EqualFold(r.Header.Get("Content-Type"), "application/sdp") {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Expected application/sdp.")
		return
	}
	if s.cfg.MediaWHIPURL == "" || len(s.cfg.MediaSecret) < 32 {
		writeError(w, r, http.StatusServiceUnavailable, "media_unavailable", "Media is not configured.")
		return
	}
	if s.completion == nil {
		writeError(w, r, http.StatusServiceUnavailable, "media_unavailable", "Media completion is not configured.")
		return
	}
	storeCtx, storeCancel := context.WithTimeout(r.Context(), time.Second)
	defer storeCancel()
	if err := s.redis.Ping(storeCtx).Err(); err != nil {
		writeDomainError(w, r, auth.ErrStoreUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSDP+1))
	if err != nil || len(body) == 0 || len(body) > maxSDP {
		writeError(w, r, http.StatusRequestEntityTooLarge, "invalid_sdp", "SDP is empty or too large.")
		return
	}
	op, err := s.sessions.ReservePublish(r.Context(), claims)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	path := fmt.Sprintf("wbb/%s/%d/source", op.SessionID, op.Generation)
	digestBytes := sha256.Sum256(body)
	digest := hex.EncodeToString(digestBytes[:])
	tuple, err := s.completion.Lifecycle(r.Context())
	if err != nil {
		s.abandonMediaOperation(op)
		writeError(w, r, 503, "media_unavailable", "Media completion is unavailable.")
		return
	}
	if err := s.sessions.MarkDispatched(r.Context(), op, tuple, path, digest); err != nil {
		writeDomainError(w, r, err)
		return
	}
	whipPath := "/" + path + "/whip"
	upstreamURL := strings.TrimSuffix(s.cfg.MediaWHIPURL, "/") + whipPath
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, strings.NewReader(string(body)))
	if err != nil {
		s.abandonMediaOperation(op)
		writeError(w, r, 502, "media_error", "Media signaling failed.")
		return
	}
	request.Header.Set("Content-Type", "application/sdp")
	if err := s.completion.PublishHeaders(request.Header, tuple, op.ID, path, digest); err != nil {
		s.abandonMediaOperation(op)
		writeError(w, r, 503, "media_unavailable", "Media completion is unavailable.")
		return
	}
	request.SetBasicAuth("create."+op.ID, mediaCredential(s.cfg.MediaSecret, "create", op.ID))
	response, err := s.mediaClient.Do(request)
	if err != nil {
		s.abandonMediaOperation(op)
		writeError(w, r, 504, "media_timeout", "Media signaling timed out.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		s.abandonMediaOperation(op)
		writeError(w, r, 502, "media_error", "Media signaling failed.")
		return
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxSDP+1))
	if err != nil || len(answer) == 0 || len(answer) > maxSDP || response.Header.Get("Content-Type") != "application/sdp" {
		s.abandonMediaOperation(op)
		writeError(w, r, 502, "media_error", "Invalid media answer.")
		return
	}
	parsed, _ := url.Parse(s.cfg.MediaWHIPURL)
	upstreamPath, err := checkedUpstreamLocation(response.Header.Get("Location"), whipPath, parsed.Host)
	if err != nil {
		s.abandonMediaOperation(op)
		writeError(w, r, 502, "media_error", "Invalid media resource.")
		return
	}
	if err = s.sessions.AttachPublish(r.Context(), op, upstreamPath, response.Header.Get("ETag")); err != nil {
		s.discardUpstreamResource(op, upstreamPath)
		s.abandonMediaOperation(op)
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/api/v1/live-sessions/"+op.SessionID+"/whip/"+op.ID)
	if tag := response.Header.Get("ETag"); tag != "" {
		w.Header().Set("ETag", tag)
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(answer)
}

func (s *Server) handleWHIPResource(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MediaWHIPURL == "" {
		writeError(w, r, 503, "media_unavailable", "Media is not configured.")
		return
	}
	cleanup := r.Method == http.MethodDelete
	res, err := s.sessions.LookupResource(r.Context(), actor(r), r.PathValue("sid"), r.PathValue("resource"), cleanup)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var body []byte
	if r.Method == http.MethodPatch {
		if r.Header.Get("Content-Type") != "application/trickle-ice-sdpfrag" {
			writeError(w, r, 415, "unsupported_media_type", "Expected ICE fragment.")
			return
		}
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil || len(body) == 0 {
			writeError(w, r, 413, "invalid_ice", "ICE fragment is empty or too large.")
			return
		}
		if res.ETag != "" && r.Header.Get("If-Match") == "" {
			writeError(w, r, 428, "etag_required", "ICE ETag required.")
			return
		}
		if res.ETag != "" && r.Header.Get("If-Match") != res.ETag {
			writeError(w, r, 412, "etag_mismatch", "ICE ETag changed.")
			return
		}
	} else {
		// A DELETE is also a user stop request: revoke state before touching the
		// ingress, so concurrent new POST/PATCH requests cannot start later.
		if _, _, err = s.sessions.Stop(r.Context(), actor(r), res.BroadcastID, res.SessionID); err != nil {
			writeDomainError(w, r, err)
			return
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimSuffix(s.cfg.MediaWHIPURL, "/")+res.UpstreamPath, strings.NewReader(string(body)))
	if err != nil {
		writeError(w, r, 502, "media_error", "Media signaling failed.")
		return
	}
	req.SetBasicAuth("create."+res.OperationID, mediaCredential(s.cfg.MediaSecret, "create", res.OperationID))
	if r.Method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
		req.Header.Set("If-Match", r.Header.Get("If-Match"))
	}
	resp, err := s.mediaClient.Do(req)
	if err != nil {
		writeError(w, r, 504, "media_timeout", "Media signaling timed out.")
		return
	}
	defer resp.Body.Close()
	if cleanup {
		if resp.StatusCode != 204 && resp.StatusCode != 200 && resp.StatusCode != 404 {
			writeError(w, r, 502, "media_error", "Media cleanup failed.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(202)
		return
	}
	if resp.StatusCode != 204 {
		writeError(w, r, 502, "media_error", "ICE update failed.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
