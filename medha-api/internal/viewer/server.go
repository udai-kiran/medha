package viewer

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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

// Handler returns the WebSocket /stream and a small HTML/JS dashboard.
type Handler struct {
	Hub    *Hub
	Logger *slog.Logger
}

// New returns a Handler wired to hub.
func New(hub *Hub, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{Hub: hub, Logger: logger}
}

// ServeHTTP routes by path: GET /stream → WebSocket; everything else → dashboard.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/stream":
		h.serveStream(w, r)
	case "/events":
		// Server-sent events for simple HTML clients that don't want WebSocket.
		h.serveSSE(w, r)
	case "/", "/index.html":
		h.serveDashboard(w, r)
	case "/health":
		_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ok","subscribers":%d}`, h.Hub.SubscriberCount())))
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

//go:embed dashboard.html
var dashboardHTML string

func (h *Handler) serveDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardHTML))
}
