package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// iceServer is the browser RTCIceServer shape.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// handleICEServers: GET /api/v1/media/ice-servers (authenticated publishers).
// Returns short-lived Cloudflare TURN credentials so a publisher can relay
// media when the server's ICE port is unreachable. Without TURN configuration
// or on provider failure it returns an empty list and the browser uses the
// server's direct ICE candidates only.
func (s *Server) handleICEServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	servers := []iceServer{}
	if s.cfg.TURNKeyID != "" {
		got, err := s.fetchTURNCredentials(r.Context())
		if err != nil {
			s.log.Warn("turn credentials unavailable", "error", err)
		} else {
			servers = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"iceServers": servers})
}

func (s *Server) fetchTURNCredentials(ctx context.Context) ([]iceServer, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]int64{"ttl": int64(s.cfg.TURNCredentialTTL / time.Second)})
	url := "https://rtc.live.cloudflare.com/v1/turn/keys/" + s.cfg.TURNKeyID + "/credentials/generate-ice-servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.TURNAPIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.turnClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("turn provider status %d", resp.StatusCode)
	}
	return parseTURNResponse(raw)
}

// parseTURNResponse accepts both the array form ({"iceServers":[...]}) and
// the single-object form ({"iceServers":{...}}), and drops port-53 URLs that
// browsers commonly block.
func parseTURNResponse(raw []byte) ([]iceServer, error) {
	var envelope struct {
		ICEServers json.RawMessage `json:"iceServers"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	type wire struct {
		URLs       any    `json:"urls"`
		Username   string `json:"username"`
		Credential string `json:"credential"`
	}
	var list []wire
	if err := json.Unmarshal(envelope.ICEServers, &list); err != nil {
		var one wire
		if err := json.Unmarshal(envelope.ICEServers, &one); err != nil {
			return nil, fmt.Errorf("unexpected turn response")
		}
		list = []wire{one}
	}
	out := []iceServer{}
	for _, item := range list {
		var urls []string
		switch v := item.URLs.(type) {
		case string:
			urls = []string{v}
		case []any:
			for _, u := range v {
				if s, ok := u.(string); ok {
					urls = append(urls, s)
				}
			}
		}
		kept := urls[:0]
		for _, u := range urls {
			if strings.Contains(u, ":53?") || strings.HasSuffix(u, ":53") {
				continue
			}
			kept = append(kept, u)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, iceServer{URLs: kept, Username: item.Username, Credential: item.Credential})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("turn response had no usable servers")
	}
	return out, nil
}
