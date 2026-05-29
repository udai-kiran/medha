package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// DiagnoseAPI exposes GET /diagnose and POST /heal.
type DiagnoseAPI struct {
	Store *state.Store
}

func (a DiagnoseAPI) Register(r chi.Router) {
	r.Get("/diagnose", a.Diagnose)
	r.Post("/heal", a.Heal)
}

func (a DiagnoseAPI) Diagnose(w http.ResponseWriter, r *http.Request) {
	report, err := a.Store.Diagnose(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "diagnose_failed", err.Error())
		return
	}
	status := http.StatusOK
	if !report.Healthy {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}

func (a DiagnoseAPI) Heal(w http.ResponseWriter, r *http.Request) {
	result, err := a.Store.Heal(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "heal_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
