// Package mediacompletion speaks the private, versioned MediaMTX operation
// completion contract. It owns HMAC wire encoding and response validation.
package mediacompletion

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const ContractVersion = "wbb-media-completion/1"

type Tuple struct {
	ContractVersion string `json:"contract_version"`
	Root            string `json:"root"`
	Slot            string `json:"slot"`
	Epoch           string `json:"epoch"`
	Sequence        uint64 `json:"epoch_sequence"`
}

type Evidence struct {
	Tuple
	Operation  string `json:"operation"`
	State      string `json:"state"`
	Registered bool   `json:"registered"`
	Revision   uint64 `json:"sequence"`
}

func (e Evidence) Completed() bool { return e.State == "completed" }

type Client struct {
	base       string
	key        []byte
	root, slot string
	http       *http.Client
}

func NewClient(base string, key []byte, root, slot string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || len(key) < 32 || root == "" || slot == "" {
		return nil, errors.New("invalid private completion configuration")
	}
	return &Client{base: strings.TrimSuffix(base, "/"), key: append([]byte(nil), key...), root: root, slot: slot,
		http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 4 * time.Second}}}, nil
}

// Sign uses the fixed MediaMTX byte-length-prefixed HMAC wire format.
func (c *Client) Sign(parts ...string) string {
	h := hmac.New(sha256.New, c.key)
	for _, part := range parts {
		_, _ = h.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func validEpoch(epoch string) bool {
	if len(epoch) != 32 {
		return false
	}
	for _, c := range epoch {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (c *Client) validTuple(t Tuple) bool {
	return t.ContractVersion == ContractVersion && t.Root == c.root && t.Slot == c.slot && validEpoch(t.Epoch) && t.Sequence > 0
}

func (c *Client) request(ctx context.Context, method, path, signature, epoch string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-WBB-Signature", signature)
	if epoch != "" {
		req.Header.Set("X-WBB-Epoch", epoch)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	return body, resp.StatusCode, err
}

func (c *Client) Lifecycle(ctx context.Context) (Tuple, error) {
	body, status, err := c.request(ctx, http.MethodGet, "/v3/wbb/lifecycle", c.Sign("lifecycle", c.root, c.slot, "", ""), "")
	if err != nil {
		return Tuple{}, err
	}
	if status != http.StatusOK {
		return Tuple{}, fmt.Errorf("completion lifecycle HTTP %d", status)
	}
	if err = requireFields(body, "contract_version", "root", "slot", "epoch", "epoch_sequence", "sequence"); err != nil {
		return Tuple{}, err
	}
	var response struct {
		Tuple
		GeneralSequence uint64 `json:"sequence"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return Tuple{}, err
	}
	tuple := response.Tuple
	if !c.validTuple(tuple) {
		return Tuple{}, errors.New("completion lifecycle tuple mismatch")
	}
	return tuple, nil
}

func requireFields(body []byte, fields ...string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	for _, field := range fields {
		if value, ok := raw[field]; !ok || len(value) == 0 || string(value) == "null" {
			return fmt.Errorf("completion field %s missing", field)
		}
	}
	return nil
}

func (c *Client) verifyEvidence(body []byte, expected Tuple, operation string) (Evidence, error) {
	if err := requireFields(body, "contract_version", "root", "slot", "epoch", "epoch_sequence", "operation", "state", "registered", "sequence"); err != nil {
		return Evidence{}, err
	}
	var e Evidence
	if err := json.Unmarshal(body, &e); err != nil {
		return Evidence{}, err
	}
	if !c.validTuple(e.Tuple) || e.Tuple != expected || e.Operation != operation ||
		(e.State != "active" && e.State != "never-registered" && e.State != "closing" && e.State != "completed" && e.State != "unknown_epoch" && e.State != "capacity_unknown") {
		return Evidence{}, errors.New("completion response tuple mismatch")
	}
	return e, nil
}

func (c *Client) Query(ctx context.Context, t Tuple, operation string) (Evidence, error) {
	if !c.validTuple(t) || operation == "" {
		return Evidence{}, errors.New("invalid completion query")
	}
	path := "/v3/wbb/operations/" + url.PathEscape(operation) + "?epoch=" + url.QueryEscape(t.Epoch)
	body, status, err := c.request(ctx, http.MethodGet, path, c.Sign("query", t.Root, t.Slot, t.Epoch, operation), t.Epoch)
	if err != nil {
		return Evidence{}, err
	}
	if status != http.StatusOK {
		return Evidence{}, fmt.Errorf("completion query HTTP %d", status)
	}
	return c.verifyEvidence(body, t, operation)
}

func (c *Client) Close(ctx context.Context, t Tuple, operation string) (Evidence, error) {
	if !c.validTuple(t) || operation == "" {
		return Evidence{}, errors.New("invalid completion close")
	}
	body, status, err := c.request(ctx, http.MethodPost, "/v3/wbb/operations/"+url.PathEscape(operation)+"/close", c.Sign("close", t.Root, t.Slot, t.Epoch, operation), t.Epoch)
	if err != nil {
		return Evidence{}, err
	}
	if status != http.StatusAccepted && status != http.StatusOK && status != http.StatusServiceUnavailable {
		return Evidence{}, fmt.Errorf("completion close HTTP %d", status)
	}
	e, err := c.verifyEvidence(body, t, operation)
	if err != nil {
		return Evidence{}, err
	}
	if status == http.StatusServiceUnavailable && e.State != "capacity_unknown" {
		return Evidence{}, errors.New("completion saturation response mismatch")
	}
	return e, nil
}

func (c *Client) PublishHeaders(h http.Header, t Tuple, operation, path, digest string) error {
	if !c.validTuple(t) || operation == "" || !strings.HasPrefix(path, "wbb/") || !strings.HasSuffix(path, "/source") || strings.Contains(path, "/whip") || len(digest) != 64 {
		return errors.New("invalid completion publish tuple")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return err
	}
	h.Set("X-WBB-Operation", operation)
	h.Set("X-WBB-Root", t.Root)
	h.Set("X-WBB-Slot", t.Slot)
	h.Set("X-WBB-Epoch", t.Epoch)
	h.Set("X-WBB-Signature", c.Sign("publish", t.Root, t.Slot, t.Epoch, operation, path, digest))
	return nil
}
