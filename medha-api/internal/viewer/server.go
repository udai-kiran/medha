package viewer

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader allows any origin in M5 — Task 33 tightens this with a Bearer
// auth check + Origin allowlist.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

// SessionSummary is the viewer-level representation of a session row.
type SessionSummary struct {
	ID               string     `json:"id"`
	Project          string     `json:"project"`
	Status           string     `json:"status"`
	ObservationCount int        `json:"observationCount"`
	StartedAt        time.Time  `json:"startedAt"`
	EndedAt          *time.Time `json:"endedAt,omitempty"`
}

// SystemCounts are aggregate store counts for the stats panel.
type SystemCounts struct {
	Sessions     int `json:"sessions"`
	Observations int `json:"observations"`
	Memories     int `json:"memories"`
	Subscribers  int `json:"subscribers"`
}

// MemorySummary is the viewer-level representation of a memory row.
type MemorySummary struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Tier      string    `json:"tier"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Strength  float64   `json:"strength"`
	CreatedAt time.Time `json:"createdAt"`
}

// StatsReader is the minimal interface the viewer's metadata API requires.
// Handler.Stats may be nil, in which case /api/* routes return 503.
type StatsReader interface {
	Projects(ctx context.Context) ([]string, error)
	Sessions(ctx context.Context, project string, limit int) ([]SessionSummary, error)
	Counts(ctx context.Context) (SystemCounts, error)
	Events(ctx context.Context, sessionID string, limit int) ([]Event, error)
	Memories(ctx context.Context, project string, limit int) ([]MemorySummary, error)
}

// Handler returns the WebSocket /stream and a small HTML/JS dashboard.
type Handler struct {
	Hub    *Hub
	Logger *slog.Logger
	// Stats is optional; set it to enable the /api/* metadata endpoints.
	Stats StatsReader
}

// New returns a Handler wired to hub.
func New(hub *Hub, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{Hub: hub, Logger: logger}
}

// ServeHTTP routes by path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/stream":
		h.serveStream(w, r)
	case "/events":
		h.serveSSE(w, r)
	case "/", "/index.html":
		h.serveDashboard(w, r)
	case "/health":
		_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ok","subscribers":%d}`, h.Hub.SubscriberCount())))
	case "/api/projects":
		h.serveProjects(w, r)
	case "/api/sessions":
		h.serveSessions(w, r)
	case "/api/stats":
		h.serveStats(w, r)
	case "/api/events":
		h.serveEvents(w, r)
	case "/api/memories":
		h.serveMemories(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.Logger.Warn("viewer.ws.upgrade_failed", "err", err)
		return
	}
	defer func() { _ = conn.Close() }()

	ch, unsubscribe := h.Hub.Subscribe()
	defer unsubscribe()

	// Send a hello frame so the client sees an immediate signal.
	_ = conn.WriteJSON(Event{Type: "system", Timestamp: time.Now().UTC(),
		Payload: map[string]any{"message": "connected"}})

	// Reader goroutine: discard inbound frames (clients are read-only).
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	// Writer loop.
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case <-closeCh:
			return
		case <-r.Context().Done():
			return
		case <-pingTicker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case evt, ok := <-ch:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		}
	}
}

func (h *Handler) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := h.Hub.Subscribe()
	defer unsubscribe()

	_, _ = fmt.Fprint(w, "event: hello\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(evt)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, b)
			flusher.Flush()
		}
	}
}

func (h *Handler) serveProjects(w http.ResponseWriter, r *http.Request) {
	if h.Stats == nil {
		http.Error(w, `{"error":"stats not configured"}`, http.StatusServiceUnavailable)
		return
	}
	projects, err := h.Stats.Projects(r.Context())
	if err != nil {
		h.Logger.Warn("viewer.api.projects", "err", err)
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []string{}
	}
	writeJSON(w, map[string]any{"projects": projects})
}

func (h *Handler) serveSessions(w http.ResponseWriter, r *http.Request) {
	if h.Stats == nil {
		http.Error(w, `{"error":"stats not configured"}`, http.StatusServiceUnavailable)
		return
	}
	project := r.URL.Query().Get("project")
	limit := 50
	if lq := r.URL.Query().Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	sessions, err := h.Stats.Sessions(r.Context(), project, limit)
	if err != nil {
		h.Logger.Warn("viewer.api.sessions", "err", err)
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []SessionSummary{}
	}
	writeJSON(w, map[string]any{"sessions": sessions})
}

func (h *Handler) serveStats(w http.ResponseWriter, r *http.Request) {
	if h.Stats == nil {
		// Return live subscriber count even without a store.
		writeJSON(w, SystemCounts{Subscribers: h.Hub.SubscriberCount()})
		return
	}
	counts, err := h.Stats.Counts(r.Context())
	if err != nil {
		h.Logger.Warn("viewer.api.stats", "err", err)
		writeJSON(w, SystemCounts{Subscribers: h.Hub.SubscriberCount()})
		return
	}
	writeJSON(w, counts)
}

func (h *Handler) serveEvents(w http.ResponseWriter, r *http.Request) {
	if h.Stats == nil {
		http.Error(w, `{"error":"stats not configured"}`, http.StatusServiceUnavailable)
		return
	}
	sessionID := r.URL.Query().Get("session")
	limit := 200
	if lq := r.URL.Query().Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	events, err := h.Stats.Events(r.Context(), sessionID, limit)
	if err != nil {
		h.Logger.Warn("viewer.api.events", "err", err)
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, map[string]any{"events": events})
}

func (h *Handler) serveMemories(w http.ResponseWriter, r *http.Request) {
	if h.Stats == nil {
		http.Error(w, `{"error":"stats not configured"}`, http.StatusServiceUnavailable)
		return
	}
	project := r.URL.Query().Get("project")
	limit := 100
	if lq := r.URL.Query().Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	mems, err := h.Stats.Memories(r.Context(), project, limit)
	if err != nil {
		h.Logger.Warn("viewer.api.memories", "err", err)
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	if mems == nil {
		mems = []MemorySummary{}
	}
	writeJSON(w, map[string]any{"memories": mems})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

//go:embed dashboard.html
var dashboardHTML string

func (h *Handler) serveDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardHTML))
}
