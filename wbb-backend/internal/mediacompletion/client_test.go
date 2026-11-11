package mediacompletion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testEpoch = "0123456789abcdef0123456789abcdef"

func TestLifecyclePinsContractAndDeploymentIdentity(t *testing.T) {
	const signed = "a30f10af62cded7bd5055686c8c579c215bc109d76c5f9455e543d5f3ea4fe41"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v3/wbb/lifecycle" || (requests == 1 && r.Header.Get("X-WBB-Signature") != signed) {
			t.Errorf("unsigned lifecycle request")
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"contract_version": ContractVersion, "root": "root", "slot": "slot", "epoch": testEpoch, "epoch_sequence": 3, "sequence": 0})
	}))
	defer server.Close()
	c, err := NewClient(server.URL, []byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}, "root", "slot")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Lifecycle(context.Background())
	if err != nil || got.Epoch != testEpoch || got.Sequence != 3 {
		t.Fatalf("lifecycle=%+v err=%v", got, err)
	}
	foreign, err := NewClient(server.URL, []byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}, "root", "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Lifecycle(context.Background()); err == nil {
		t.Fatal("foreign slot accepted")
	}
}

func TestQueryRequiresExactCompletedTuple(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/wbb/operations/11111111-1111-4111-8111-111111111111" || r.URL.Query().Get("epoch") != testEpoch || r.Header.Get("X-WBB-Epoch") != testEpoch || len(r.Header.Get("X-WBB-Signature")) != 64 {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"contract_version": ContractVersion, "root": "root", "slot": "slot", "epoch": testEpoch, "epoch_sequence": 3, "operation": "11111111-1111-4111-8111-111111111111", "state": "completed", "registered": true, "sequence": 6})
	}))
	defer server.Close()
	c, err := NewClient(server.URL, make([]byte, 32), "root", "slot")
	if err != nil {
		t.Fatal(err)
	}
	tuple := Tuple{ContractVersion: ContractVersion, Root: "root", Slot: "slot", Epoch: testEpoch, Sequence: 3}
	evidence, err := c.Query(context.Background(), tuple, "11111111-1111-4111-8111-111111111111")
	if err != nil || !evidence.Completed() {
		t.Fatalf("query=%+v err=%v", evidence, err)
	}
	tuple.Sequence = 4
	if _, err := c.Query(context.Background(), tuple, "11111111-1111-4111-8111-111111111111"); err == nil {
		t.Fatal("wrong epoch sequence accepted")
	}
}

func TestHistoricalCompletionDoesNotRequireRegisteredFlag(t *testing.T) {
	op := "11111111-1111-4111-8111-111111111111"
	tuple := Tuple{ContractVersion: ContractVersion, Root: "root", Slot: "slot", Epoch: testEpoch, Sequence: 3}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/wbb/operations/"+op+"/close" || r.Header.Get("X-WBB-Epoch") != testEpoch {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"contract_version": ContractVersion, "root": "root", "slot": "slot", "epoch": testEpoch, "epoch_sequence": 3, "operation": op, "state": "completed", "registered": false, "sequence": 0})
	}))
	defer server.Close()
	c, err := NewClient(server.URL, make([]byte, 32), "root", "slot")
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := c.Close(context.Background(), tuple, op)
	if err != nil || !evidence.Completed() || evidence.Registered {
		t.Fatalf("historical proof=%+v err=%v", evidence, err)
	}
}

func TestMissingEvidenceFieldFailsClosed(t *testing.T) {
	op := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"contract_version": ContractVersion, "root": "root", "slot": "slot", "epoch": testEpoch, "epoch_sequence": 3, "operation": op, "state": "completed", "sequence": 6})
	}))
	defer server.Close()
	c, err := NewClient(server.URL, make([]byte, 32), "root", "slot")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Query(context.Background(), Tuple{ContractVersion: ContractVersion, Root: "root", Slot: "slot", Epoch: testEpoch, Sequence: 3}, op)
	if err == nil {
		t.Fatal("completed response without registered was trusted")
	}
}

func TestCloseAndQueryRejectMismatchedOrUnavailableManagement(t *testing.T) {
	const op = "11111111-1111-4111-8111-111111111111"
	tuple := Tuple{ContractVersion: ContractVersion, Root: "root", Slot: "slot", Epoch: testEpoch, Sequence: 3}
	for _, method := range []string{"close", "query"} {
		t.Run(method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"contract_version": ContractVersion, "root": "root", "slot": "slot", "epoch": testEpoch,
					"epoch_sequence": 4, "operation": op, "state": "completed", "registered": false, "sequence": 5})
			}))
			c, err := NewClient(server.URL, make([]byte, 32), "root", "slot")
			if err != nil {
				t.Fatal(err)
			}
			call := func() error {
				if method == "close" {
					_, err := c.Close(context.Background(), tuple, op)
					return err
				}
				_, err := c.Query(context.Background(), tuple, op)
				return err
			}
			if err := call(); err == nil {
				t.Fatal("wrong epoch sequence was accepted")
			}
			server.Close()
			if err := call(); err == nil {
				t.Fatal("management outage was accepted as completion")
			}
		})
	}
}
