package mediaworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type PathState struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
}
type MTXSession struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	State string `json:"state"`
}
type MediaSnapshot struct {
	Paths   map[string]PathState
	Ingress []MTXSession
	RTSP    []MTXSession
}

type MTXClient struct {
	Base, HLSBase, WHIPBase, User, Password string
	HTTP                                    *http.Client
	mu                                      sync.Mutex
	lastPart                                map[string]string
}

var hlsSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DeleteWHIP releases a known MediaMTX resource through its internal endpoint.
// A 404 is idempotent success after an already completed session kick.
func (m *MTXClient) DeleteWHIP(ctx context.Context, resourcePath, source, user, password string) error {
	if m.WHIPBase == "" || !strings.HasPrefix(resourcePath, "/"+source+"/whip/") ||
		strings.ContainsAny(resourcePath, "\\?#%") || strings.Contains(resourcePath, "..") {
		return fmt.Errorf("unsafe WHIP cleanup path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimSuffix(m.WHIPBase, "/")+resourcePath, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, password)
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 && resp.StatusCode != 404 {
		return fmt.Errorf("WHIP cleanup HTTP %d", resp.StatusCode)
	}
	return nil
}

func NewMTXClient(base, hlsBase, user, password string) *MTXClient {
	return &MTXClient{Base: strings.TrimSuffix(base, "/"), HLSBase: strings.TrimSuffix(hlsBase, "/"), User: user, Password: password, lastPart: make(map[string]string),
		HTTP: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 3 * time.Second}}}
}

func (m *MTXClient) request(ctx context.Context, method, rawURL string) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	r.SetBasicAuth(m.User, m.Password)
	return m.HTTP.Do(r)
}

func (m *MTXClient) listAll(ctx context.Context, path string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	count, pages := 0, 0
	for page := 0; ; page++ {
		rawURL := m.Base + path
		if page > 0 {
			rawURL += fmt.Sprintf("?page=%d", page)
		}
		resp, err := m.request(ctx, "GET", rawURL)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("MediaMTX %s: HTTP %d", path, resp.StatusCode)
		}
		var body struct {
			ItemCount int               `json:"itemCount"`
			PageCount int               `json:"pageCount"`
			Items     []json.RawMessage `json:"items"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if page == 0 {
			count, pages = body.ItemCount, body.PageCount
			if count == 0 && pages == 0 && len(body.Items) == 0 {
				return []json.RawMessage{}, nil
			}
			if pages < 1 || pages > 100 || count < 0 || count > 10000 {
				return nil, fmt.Errorf("incomplete MediaMTX pagination")
			}
		}
		if body.ItemCount != count || body.PageCount != pages || page >= pages {
			return nil, fmt.Errorf("changed MediaMTX pagination")
		}
		all = append(all, body.Items...)
		if page+1 == pages {
			break
		}
	}
	if len(all) != count {
		return nil, fmt.Errorf("incomplete MediaMTX snapshot: %d of %d", len(all), count)
	}
	return all, nil
}

func (m *MTXClient) Snapshot(ctx context.Context) (MediaSnapshot, error) {
	p, err := m.listAll(ctx, "/v3/paths/list")
	if err != nil {
		return MediaSnapshot{}, err
	}
	w, err := m.listAll(ctx, "/v3/webrtcsessions/list")
	if err != nil {
		return MediaSnapshot{}, err
	}
	rs, err := m.listAll(ctx, "/v3/rtspsessions/list")
	if err != nil {
		return MediaSnapshot{}, err
	}
	out := MediaSnapshot{Paths: make(map[string]PathState)}
	for _, raw := range p {
		var it PathState
		if err := json.Unmarshal(raw, &it); err != nil {
			return MediaSnapshot{}, err
		}
		out.Paths[it.Name] = it
	}
	for _, raw := range w {
		var it MTXSession
		if err := json.Unmarshal(raw, &it); err != nil {
			return MediaSnapshot{}, err
		}
		out.Ingress = append(out.Ingress, it)
	}
	for _, raw := range rs {
		var it MTXSession
		if err := json.Unmarshal(raw, &it); err != nil {
			return MediaSnapshot{}, err
		}
		out.RTSP = append(out.RTSP, it)
	}
	return out, nil
}

func (m *MTXClient) Kick(ctx context.Context, kind, id string) error {
	if kind != "webrtcsessions" && kind != "rtspsessions" {
		return fmt.Errorf("unsupported session kind")
	}
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return fmt.Errorf("unsafe session id")
	}
	resp, err := m.request(ctx, "POST", m.Base+"/v3/"+kind+"/kick/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return fmt.Errorf("kick %s: HTTP %d", kind, resp.StatusCode)
	}
	return nil
}

// PartReady probes an actual LL-HLS media playlist and a referenced part.
// A master 200 alone cannot change STARTING to LIVE.
func (m *MTXClient) PartReady(ctx context.Context, output, user, password string) (bool, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return false, err
	}
	client := *m.HTTP
	client.Jar = jar
	base, err := url.Parse(m.HLSBase + "/" + output + "/")
	if err != nil {
		return false, err
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != base.Scheme || req.URL.Host != base.Host || !strings.HasPrefix(req.URL.Path, base.Path) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	selectedSession := ""
	read := func(ref string, limit int64) ([]byte, int, error) {
		if !safeHLSRef(ref, selectedSession) {
			return nil, 0, fmt.Errorf("unsafe HLS object URI")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", base.String()+ref, nil)
		if err != nil {
			return nil, 0, err
		}
		req.SetBasicAuth(user, password)
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, resp.StatusCode, nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
		return raw, resp.StatusCode, err
	}
	raw, status, err := read("index.m3u8", 128<<10)
	if err != nil {
		return false, err
	}
	if status == 404 {
		return false, nil
	}
	if status != 200 {
		return false, fmt.Errorf("HLS master: HTTP %d", status)
	}
	if !strings.Contains(string(raw), "#EXT-X-PART:") {
		child := hlsVariant(string(raw))
		if child == "" {
			return false, nil
		}
		childURL, _ := url.Parse(child)
		selectedSession = childURL.Query().Get("session")
		raw, status, err = read(child, 128<<10)
		if err != nil {
			return false, err
		}
		if status == 404 {
			return false, nil
		}
		if status != 200 {
			return false, fmt.Errorf("HLS variant: HTTP %d", status)
		}
	}
	var init, part, mediaSequence string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:") {
			mediaSequence = strings.TrimSpace(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			init = hlsURI(line)
		}
		if strings.HasPrefix(line, "#EXT-X-PART:") {
			part = hlsURI(line)
		}
	}
	if init == "" || part == "" {
		return false, nil
	}
	partRef, err := url.Parse(part)
	if err != nil {
		return false, err
	}
	// HLS session queries can rotate between probes while the underlying
	// media remains unchanged. Progress is sequence plus part identity.
	partIdentity := mediaSequence + ":" + partRef.Path
	for _, name := range []string{init, part} {
		object, status, err := read(name, 1)
		if err != nil {
			return false, err
		}
		if status != 200 || len(object) == 0 {
			return false, nil
		}
	}
	m.mu.Lock()
	old := m.lastPart[output]
	m.lastPart[output] = partIdentity
	m.mu.Unlock()
	return old != "" && old != partIdentity, nil
}

func hlsVariant(raw string) string {
	variantNext := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if variantNext && line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
		variantNext = strings.HasPrefix(line, "#EXT-X-STREAM-INF:")
	}
	return ""
}

func safeHLSRef(ref, expectedSession string) bool {
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || u.Path == "" ||
		strings.ContainsAny(u.Path, "/\\:") || strings.Contains(u.Path, "..") || strings.Contains(u.EscapedPath(), "%") {
		return false
	}
	if u.RawQuery == "" {
		return true
	}
	q, err := url.ParseQuery(u.RawQuery)
	return err == nil && len(q) == 1 && len(q["session"]) == 1 && hlsSessionID.MatchString(q.Get("session")) &&
		(expectedSession == "" || q.Get("session") == expectedSession)
}

func hlsURI(line string) string {
	marker := "URI=\""
	i := strings.Index(line, marker)
	if i < 0 {
		return ""
	}
	rest := line[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
