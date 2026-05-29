package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ConversationsAPI handles short-term conversation/message memory (G08).
type ConversationsAPI struct {
	Store *state.Store
}

func (a ConversationsAPI) Register(r chi.Router) {
	r.Post("/messages", a.AddMessage)
	r.Get("/conversation", a.GetConversation)
	r.Delete("/conversation", a.ClearConversation)
	r.Get("/messages/search", a.SearchMessages)
}

func (a ConversationsAPI) AddMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string         `json:"sessionId"`
		Project   string         `json:"project"`
		Role      string         `json:"role"`
		Content   string         `json:"content"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.SessionID == "" || body.Role == "" || body.Content == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "sessionId, role, and content are required")
		return
	}
	msg, err := a.Store.AddMessage(r.Context(), body.SessionID, body.Project, body.Role, body.Content, body.Metadata)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "add_message_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (a ConversationsAPI) GetConversation(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, "missing_param", "sessionId is required")
		return
	}
	conv, err := a.Store.GetConversation(r.Context(), sessionID)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (a ConversationsAPI) ClearConversation(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, "missing_param", "sessionId is required")
		return
	}
	if err := a.Store.ClearConversation(r.Context(), sessionID); err != nil {
		WriteError(w, http.StatusInternalServerError, "clear_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

func (a ConversationsAPI) SearchMessages(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	query := r.URL.Query().Get("query")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := a.Store.SearchMessages(r.Context(), project, query, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}
