package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ContextAPI handles POST /context — unified context assembly for LLM injection (G34).
type ContextAPI struct{ Store *state.Store }

func (a ContextAPI) Register(r chi.Router) {
	r.Post("/context", a.Assemble)
}

func (a ContextAPI) Assemble(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project          string `json:"project"`
		SessionID        string `json:"sessionId"`
		Query            string `json:"query"`
		IncludeShortTerm *bool  `json:"includeShortTerm"`
		IncludeLongTerm  *bool  `json:"includeLongTerm"`
		IncludeReasoning *bool  `json:"includeReasoning"`
		IncludeSlots     *bool  `json:"includeSlots"`
		MaxItems         int    `json:"maxItems"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	boolDefault := func(p *bool, def bool) bool {
		if p == nil {
			return def
		}
		return *p
	}

	req := state.ContextRequest{
		Project:          body.Project,
		SessionID:        body.SessionID,
		Query:            body.Query,
		IncludeShortTerm: boolDefault(body.IncludeShortTerm, true),
		IncludeLongTerm:  boolDefault(body.IncludeLongTerm, true),
		IncludeReasoning: boolDefault(body.IncludeReasoning, false),
		IncludeSlots:     boolDefault(body.IncludeSlots, true),
		MaxItems:         body.MaxItems,
	}

	result, err := a.Store.AssembleContext(r.Context(), req)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "assembly_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
