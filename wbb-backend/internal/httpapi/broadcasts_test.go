package httpapi_test

// Broadcast CRUD, ownership, versioning, pagination and restart persistence
// through the public HTTP seam (ticket 01).

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestBroadcastCreateRequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)

	r := do(t, client, "POST", ts.URL+"/api/v1/broadcasts", map[string]any{"title": "무제"}, "")
	if r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous create: %d (want 401)", r.status)
	}
}

func TestBroadcastLifecycle(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	user, access, _ := registerAndLogin(t, ts, client, uniqueEmail())

	// Create.
	created := createBroadcast(t, ts, access, "첫 방송")
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %v", created)
	}
	if created["ownerId"] != user["id"] {
		t.Fatalf("ownerId must be the authenticated user: %v", created)
	}
	owner := created["owner"].(map[string]any)
	if owner["id"] != user["id"] || owner["displayName"] != user["displayName"] {
		t.Fatalf("owner summary wrong: %v", owner)
	}
	if created["version"].(float64) != 1 {
		t.Fatalf("initial version must be 1: %v", created["version"])
	}
	if created["status"] != "ready" {
		t.Fatalf("initial status must be ready: %v", created["status"])
	}
	if created["currentSessionId"] != nil {
		t.Fatalf("currentSessionId must be null before media sessions: %v", created["currentSessionId"])
	}
	if _, hasEmail := owner["email"]; hasEmail {
		t.Fatal("public broadcast object must not expose owner email")
	}

	// Public list contains it.
	list := do(t, client, "GET", ts.URL+"/api/v1/broadcasts", nil, "")
	if list.status != http.StatusOK {
		t.Fatalf("list: %d", list.status)
	}
	if !listContains(list.body["items"].([]any), id) {
		t.Fatalf("created broadcast missing from list: %v", list.body)
	}

	// Public detail.
	detail := do(t, client, "GET", ts.URL+"/api/v1/broadcasts/"+id, nil, "")
	if detail.status != http.StatusOK || detail.body["broadcast"].(map[string]any)["title"] != "첫 방송" {
		t.Fatalf("detail: %d %v", detail.status, detail.body)
	}

	// Update with matching version.
	updated := do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id,
		map[string]any{"version": 1, "title": "제목 바꿈", "description": "설명 바꿈"}, access)
	if updated.status != http.StatusOK {
		t.Fatalf("update: %d body=%v", updated.status, updated.body)
	}
	b := updated.body["broadcast"].(map[string]any)
	if b["title"] != "제목 바꿈" || b["description"] != "설명 바꿈" || b["version"].(float64) != 2 {
		t.Fatalf("update result wrong: %v", b)
	}

	// Delete with matching version, then it disappears from public reads.
	del := do(t, client, "DELETE", ts.URL+"/api/v1/broadcasts/"+id+"?version=2", nil, access)
	if del.status != http.StatusNoContent {
		t.Fatalf("delete: %d body=%v", del.status, del.body)
	}
	if r := do(t, client, "GET", ts.URL+"/api/v1/broadcasts/"+id, nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("get after delete: %d (want 404)", r.status)
	}
	list = do(t, client, "GET", ts.URL+"/api/v1/broadcasts", nil, "")
	if listContains(list.body["items"].([]any), id) {
		t.Fatal("deleted broadcast must be excluded from the list")
	}
}

func TestBroadcastOwnershipMatrix(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	ownerUser, ownerAccess, _ := registerAndLogin(t, ts, client, uniqueEmail())
	_, otherAccess, _ := registerAndLogin(t, ts, client, uniqueEmail())

	created := createBroadcast(t, ts, ownerAccess, "내 방송")
	id := created["id"].(string)

	cases := []struct {
		name   string
		method string
		bearer string
		want   int
	}{
		{"anonymous update", "PATCH", "", http.StatusUnauthorized},
		{"other update", "PATCH", otherAccess, http.StatusForbidden},
		{"anonymous delete", "DELETE", "", http.StatusUnauthorized},
		{"other delete", "DELETE", otherAccess, http.StatusForbidden},
	}
	for _, c := range cases {
		var body any
		if c.method == "PATCH" {
			body = map[string]any{"version": 1, "title": "해킹"}
		}
		path := ts.URL + "/api/v1/broadcasts/" + id
		if c.method == "DELETE" {
			path += "?version=1"
		}
		r := do(t, client, c.method, path, body, c.bearer)
		if r.status != c.want {
			t.Fatalf("%s: status=%d want=%d body=%v", c.name, r.status, c.want, r.body)
		}
	}

	// Unknown broadcast: 404 for owner too.
	r := do(t, client, "GET", ts.URL+"/api/v1/broadcasts/00000000-0000-0000-0000-000000000000", nil, "")
	if r.status != http.StatusNotFound {
		t.Fatalf("unknown id: %d", r.status)
	}
	r = do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/00000000-0000-0000-0000-000000000000",
		map[string]any{"version": 1, "title": "x"}, ownerAccess)
	if r.status != http.StatusNotFound {
		t.Fatalf("unknown id update: %d", r.status)
	}

	// ownerId in the payload is ignored: the server decides the owner.
	forged := do(t, client, "POST", ts.URL+"/api/v1/broadcasts",
		map[string]any{"title": "위장", "ownerId": "00000000-0000-0000-0000-000000000000"}, ownerAccess)
	if forged.status != http.StatusCreated {
		t.Fatalf("forged ownerId create: %d", forged.status)
	}
	if forged.body["broadcast"].(map[string]any)["ownerId"] != ownerUser["id"] {
		t.Fatal("server must ignore client-provided ownerId")
	}
}

func TestBroadcastVersionConflict(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	created := createBroadcast(t, ts, access, "버전 테스트")
	id := created["id"].(string)

	r := do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id,
		map[string]any{"version": 1, "title": "첫 수정"}, access)
	if r.status != http.StatusOK {
		t.Fatalf("first update: %d", r.status)
	}

	// Stale version → 409 for update and delete.
	r = do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id,
		map[string]any{"version": 1, "title": "늦은 수정"}, access)
	if r.status != http.StatusConflict || r.errCode() != "version_conflict" {
		t.Fatalf("stale update: %d code=%s", r.status, r.errCode())
	}
	r = do(t, client, "DELETE", ts.URL+"/api/v1/broadcasts/"+id+"?version=1", nil, access)
	if r.status != http.StatusConflict {
		t.Fatalf("stale delete: %d", r.status)
	}

	// Missing version → 400.
	r = do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id, map[string]any{"title": "무버전"}, access)
	if r.status != http.StatusBadRequest {
		t.Fatalf("missing version: %d", r.status)
	}
	r = do(t, client, "DELETE", ts.URL+"/api/v1/broadcasts/"+id, nil, access)
	if r.status != http.StatusBadRequest {
		t.Fatalf("delete without version: %d", r.status)
	}
}

func TestBroadcastValidation(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty title", map[string]any{"title": "", "description": ""}},
		{"title over 100", map[string]any{"title": strings.Repeat("가", 101)}},
		{"description over 2000", map[string]any{"title": "ok", "description": strings.Repeat("d", 2001)}},
	}
	for _, c := range cases {
		r := do(t, client, "POST", ts.URL+"/api/v1/broadcasts", c.body, access)
		if r.status != http.StatusBadRequest || r.errCode() != "invalid_input" {
			t.Fatalf("%s: status=%d code=%s body=%v", c.name, r.status, r.errCode(), r.body)
		}
	}

	// Invalid status filter.
	r := do(t, client, "GET", ts.URL+"/api/v1/broadcasts?status=bogus", nil, "")
	if r.status != http.StatusBadRequest {
		t.Fatalf("bogus status: %d", r.status)
	}
	// Malformed JSON body.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/broadcasts", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed json: %d", resp.StatusCode)
	}
}

func TestBroadcastPagination(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())

	const mine = 25
	myIDs := map[string]bool{}
	for i := 0; i < mine; i++ {
		b := createBroadcast(t, ts, access, fmt.Sprintf("방송 %02d", i))
		myIDs[b["id"].(string)] = true
	}

	// Default page size is 20 and a cursor appears while pages remain.
	page1 := do(t, client, "GET", ts.URL+"/api/v1/broadcasts", nil, "")
	items := page1.body["items"].([]any)
	if len(items) != 20 {
		t.Fatalf("default page size: %d", len(items))
	}
	cursor, _ := page1.body["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("nextCursor required when more pages exist")
	}

	// Walk every page: no duplicates, and all of this test's broadcasts show up.
	seen := map[string]bool{}
	dup := func(id string) bool { _, ok := seen[id]; return ok }
	for _, it := range items {
		id := it.(map[string]any)["id"].(string)
		if dup(id) {
			t.Fatalf("duplicate id in page 1: %s", id)
		}
		seen[id] = true
	}
	for cursor != "" {
		page := do(t, client, "GET", ts.URL+"/api/v1/broadcasts?cursor="+cursor, nil, "")
		if page.status != http.StatusOK {
			t.Fatalf("cursor page: %d", page.status)
		}
		for _, it := range page.body["items"].([]any) {
			id := it.(map[string]any)["id"].(string)
			if dup(id) {
				t.Fatalf("duplicate id across pages: %s", id)
			}
			seen[id] = true
		}
		cursor, _ = page.body["nextCursor"].(string)
	}
	for id := range myIDs {
		if !seen[id] {
			t.Fatalf("my broadcast %s never appeared in pagination", id)
		}
	}

	// limit is capped at 50.
	r := do(t, client, "GET", ts.URL+"/api/v1/broadcasts?limit=500", nil, "")
	if r.status != http.StatusOK {
		t.Fatalf("capped limit: %d", r.status)
	}

	// live filter is honestly empty until media sessions exist.
	r = do(t, client, "GET", ts.URL+"/api/v1/broadcasts?status=live", nil, "")
	if r.status != http.StatusOK || len(r.body["items"].([]any)) != 0 {
		t.Fatalf("live filter: %d %v", r.status, r.body["items"])
	}
}

// TestPersistenceAcrossServerRestart recreates the API server against the
// same database and signing key: broadcast data, ownership and the active
// session must survive (BCAST-04).
func TestPersistenceAcrossServerRestart(t *testing.T) {
	secret := make([]byte, 32)
	ts1 := newTestServer(t, withFixedSecret(secret))
	client := newJarClient(t)
	user, access, _ := registerAndLogin(t, ts1, client, uniqueEmail())
	created := createBroadcast(t, ts1, access, "재시작 생존")
	id := created["id"].(string)

	// "Restart": tear down server 1, boot server 2 on the same DB+Redis+key.
	ts1.Close()
	ts2 := newTestServer(t, withFixedSecret(secret))

	// Broadcast data and ownership survive.
	r := do(t, client, "GET", ts2.URL+"/api/v1/broadcasts/"+id, nil, "")
	if r.status != http.StatusOK {
		t.Fatalf("broadcast after restart: %d", r.status)
	}
	b := r.body["broadcast"].(map[string]any)
	if b["title"] != "재시작 생존" || b["ownerId"] != user["id"] {
		t.Fatalf("broadcast fields after restart: %v", b)
	}

	// The session survives too: PostgreSQL ledger + Redis cache are intact.
	if r := meWithToken(t, ts2, access); r.status != http.StatusOK {
		t.Fatalf("me after restart: %d", r.status)
	}
	list := do(t, client, "GET", ts2.URL+"/api/v1/broadcasts", nil, "")
	if !listContains(list.body["items"].([]any), id) {
		t.Fatal("list must include the broadcast after restart")
	}
}

// TestUpdateNormalizesBeforeStoring: titles are trimmed once and the trimmed
// value is both validated and stored (regression: padding used to survive).
func TestUpdateNormalizesBeforeStoring(t *testing.T) {
	ts := newTestServer(t)
	client := newJarClient(t)
	_, access, _ := registerAndLogin(t, ts, client, uniqueEmail())
	created := createBroadcast(t, ts, access, "원제목")
	id := created["id"].(string)

	// 100 runes + surrounding spaces: valid after trim, stored trimmed.
	padded := "  " + strings.Repeat("방", 100) + "  "
	r := do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id,
		map[string]any{"version": 1, "title": padded}, access)
	if r.status != http.StatusOK {
		t.Fatalf("padded title: %d body=%v", r.status, r.body)
	}
	title := r.body["broadcast"].(map[string]any)["title"].(string)
	if len([]rune(title)) != 100 || strings.TrimSpace(title) != title {
		t.Fatalf("stored title must be the trimmed 100-rune value, got %d runes", len([]rune(title)))
	}

	// 101 runes even after trim: rejected.
	r = do(t, client, "PATCH", ts.URL+"/api/v1/broadcasts/"+id,
		map[string]any{"version": 2, "title": strings.Repeat("가", 101)}, access)
	if r.status != http.StatusBadRequest {
		t.Fatalf("101-rune title: %d (want 400)", r.status)
	}
}

func listContains(items []any, id string) bool {
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["id"] == id {
			return true
		}
	}
	return false
}
