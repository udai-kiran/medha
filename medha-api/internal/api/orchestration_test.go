package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrchestration_ActionLifecycle(t *testing.T) {
	h, _ := newFullRouter(t)

	// Create.
	w := post(t, h, "/agentmemory/actions", map[string]any{
		"project": "p", "title": "Plan",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d, body=%s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("no id")
	}

	// Get.
	w = get(t, h, "/agentmemory/actions/"+created.ID+"?project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d", w.Code)
	}

	// Patch status.
	req := map[string]any{"status": "completed"}
	body, _ := json.Marshal(req)
	patchReq := httptest.NewRequest(http.MethodPatch, "/agentmemory/actions/"+created.ID+"?project=p", bytes.NewReader(body))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	h.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch = %d body=%s", patchRec.Code, patchRec.Body.String())
	}

	// Frontier should now show no pending actions (the only one is completed).
	w = get(t, h, "/agentmemory/frontier?project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("frontier = %d", w.Code)
	}
}

func TestOrchestration_LeaseConflict(t *testing.T) {
	h, _ := newFullRouter(t)
	_ = post(t, h, "/agentmemory/actions", map[string]any{"id": "act-1", "project": "p", "title": "x"})

	w := post(t, h, "/agentmemory/leases/act-1/acquire", map[string]any{"project": "p", "holderId": "alice", "ttlSecs": 60})
	if w.Code != http.StatusOK {
		t.Fatalf("acquire = %d body=%s", w.Code, w.Body.String())
	}
	w = post(t, h, "/agentmemory/leases/act-1/acquire", map[string]any{"project": "p", "holderId": "bob", "ttlSecs": 60})
	if w.Code != http.StatusConflict {
		t.Fatalf("second acquire status = %d, want 409", w.Code)
	}
}

func TestOrchestration_SignalRoundTrip(t *testing.T) {
	h, _ := newFullRouter(t)
	w := post(t, h, "/agentmemory/signals", map[string]any{
		"project": "p", "from": "alice", "to": "bob",
		"subject": "ping", "body": "hello",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("send = %d body=%s", w.Code, w.Body.String())
	}
	w = get(t, h, "/agentmemory/signals?to=bob&project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var out struct {
		Signals []map[string]any `json:"signals"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Signals) != 1 {
		t.Errorf("expected 1 inbox signal, got %d", len(out.Signals))
	}
}

func TestOrchestration_RoutinePutAndList(t *testing.T) {
	h, _ := newFullRouter(t)
	w := post(t, h, "/agentmemory/routines", map[string]any{
		"project": "p", "name": "build", "steps": []string{"go build ./..."},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("put = %d", w.Code)
	}
	w = get(t, h, "/agentmemory/routines?project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
}

