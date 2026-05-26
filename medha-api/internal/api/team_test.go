package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTeam_ShareFeedRevoke(t *testing.T) {
	h, _ := newFullRouter(t)

	// Create a memory to share.
	w := post(t, h, "/agentmemory/remember", map[string]any{
		"project": "p", "type": "fact", "title": "Use jose", "content": "JWT.",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("remember = %d body=%s", w.Code, w.Body.String())
	}
	var rem struct {
		MemoryID string `json:"memoryId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rem)

	// Share to a team.
	w = post(t, h, "/agentmemory/team/share", map[string]any{
		"memoryId": rem.MemoryID, "team": "platform", "mode": "read", "actor": "alice",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("share = %d body=%s", w.Code, w.Body.String())
	}

	// Feed.
	w = get(t, h, "/agentmemory/team/feed?team=platform")
	if w.Code != http.StatusOK {
		t.Fatalf("feed = %d", w.Code)
	}
	var feed struct {
		Feed []map[string]any `json:"feed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &feed)
	if len(feed.Feed) != 1 {
		t.Fatalf("feed len = %d", len(feed.Feed))
	}

	// Audit should record at least the share entry.
	w = get(t, h, "/agentmemory/audit")
	if w.Code != http.StatusOK {
		t.Fatalf("audit = %d", w.Code)
	}
	var auditOut struct {
		Audit []map[string]any `json:"audit"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &auditOut)
	sawShare := false
	for _, e := range auditOut.Audit {
		if e["action"] == "share" && e["targetId"] == rem.MemoryID {
			sawShare = true
		}
	}
	if !sawShare {
		t.Errorf("share not in audit log: %+v", auditOut.Audit)
	}

	// Revoke.
	w = post(t, h, "/agentmemory/team/revoke", map[string]any{
		"memoryId": rem.MemoryID, "team": "platform", "actor": "alice",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d body=%s", w.Code, w.Body.String())
	}
	w = get(t, h, "/agentmemory/team/feed?team=platform")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &feed)
	if len(feed.Feed) != 0 {
		t.Errorf("after revoke feed should be empty, got %d", len(feed.Feed))
	}
}

func TestTeam_ShareValidation(t *testing.T) {
	h, _ := newFullRouter(t)
	w := post(t, h, "/agentmemory/team/share", map[string]any{"team": "x"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing memoryId = %d", w.Code)
	}
	w = post(t, h, "/agentmemory/team/share", map[string]any{"memoryId": "mem-1", "team": "x", "mode": "wat"})
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("bad mode = %d", w.Code)
	}
}
