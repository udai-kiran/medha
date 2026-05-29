package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// SlotsAPI handles pinned memory slots (G24) and working memory stack (G25).
type SlotsAPI struct{ Store *state.Store }

func (a SlotsAPI) Register(r chi.Router) {
	r.Get("/slots", a.ListSlots)
	r.Post("/slots/{name}", a.SetSlot)
	r.Get("/slots/{name}", a.GetSlot)

	r.Post("/working/push", a.Push)
	r.Post("/working/pop", a.Pop)
	r.Delete("/working", a.Clear)
}

func (a SlotsAPI) ListSlots(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	slots, err := a.Store.ListSlots(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (a SlotsAPI) SetSlot(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	project := r.URL.Query().Get("project")
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.SetSlot(r.Context(), project, name, body.Content); err != nil {
		WriteError(w, http.StatusInternalServerError, "set_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slotName": name, "updated": true})
}

func (a SlotsAPI) GetSlot(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	project := r.URL.Query().Get("project")
	content, err := a.Store.GetSlot(r.Context(), project, name)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "slot not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slotName": name, "content": content})
}

func (a SlotsAPI) Push(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	id, err := a.Store.WorkingPush(r.Context(), body.SessionID, body.Content)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "push_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"workingId": id})
}

func (a SlotsAPI) Pop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		Count     int    `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	items, err := a.Store.WorkingPop(r.Context(), body.SessionID, body.Count)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "pop_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a SlotsAPI) Clear(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if err := a.Store.WorkingClear(r.Context(), sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, "clear_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

// GovernanceAPI handles compliance-safe deletion and extended audit (G23).
type GovernanceAPI struct{ Store *state.Store }

func (a GovernanceAPI) Register(r chi.Router) {
	r.Post("/governance/delete", a.Delete)
	r.Get("/audit/full", a.FullAudit)
}

func (a GovernanceAPI) Delete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MemoryIDs []string `json:"memoryIds"`
		Reason    string   `json:"reason"`
		Actor     string   `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(body.MemoryIDs) == 0 || body.Reason == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "memoryIds and reason are required")
		return
	}
	actor := body.Actor
	if actor == "" {
		actor = "governance"
	}
	deleted := 0
	for _, id := range body.MemoryIDs {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		payload, _ := json.Marshal(map[string]string{"reason": body.Reason, "actor": actor})
		_, _ = a.Store.DB.ExecContext(r.Context(),
			`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
             VALUES ($1, $2, 'governance_delete', 'memory', $3, $4)`,
			now, actor, id, string(payload))
		if err := a.Store.DeleteMemory(r.Context(), id); err == nil {
			deleted++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (a GovernanceAPI) FullAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := q.Get("project")
	action := q.Get("action")
	actor := q.Get("actor")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
        SELECT timestamp, actor, action, target_type, target_id, payload_json
        FROM audit_log
        WHERE ($1 = '' OR payload_json LIKE '%' || $1 || '%')
        AND ($2 = '' OR action = $2)
        AND ($3 = '' OR actor = $3)
        ORDER BY id DESC LIMIT $4
    `, project, action, actor, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var ts, act, atn, ttype, tid, payload string
		if rows.Scan(&ts, &act, &atn, &ttype, &tid, &payload) == nil {
			var pl map[string]any
			_ = json.Unmarshal([]byte(payload), &pl)
			out = append(out, map[string]any{
				"timestamp": ts, "actor": act, "action": atn,
				"targetType": ttype, "targetId": tid, "payload": pl,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": out})
}
