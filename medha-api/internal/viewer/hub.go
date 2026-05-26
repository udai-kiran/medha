// Package viewer hosts the real-time WebSocket viewer (Task 28).
// It satisfies api.ViewerBroadcaster so the capture/compression hot paths
// can fan out events without knowing anything about WebSockets.
package viewer

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Event is one item broadcast to viewer clients. Shapes are deliberately
// loose JSON so future event types don't require a schema change.
type Event struct {
	Type      string         `json:"type"`     // observation | memory | session | system
	SessionID string         `json:"sessionId,omitempty"`
	ID        string         `json:"id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// Hub fans events out to all subscribed clients. It is safe for concurrent
// use; emitters call Publish, clients call Subscribe and read from the
// returned channel. Slow clients are dropped rather than blocking the hub.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
	logger  *slog.Logger

	// MaxClients caps the number of concurrent subscribers; 0 = unlimited.
	MaxClients int
	// ClientBuffer controls each subscriber's buffer depth.
	ClientBuffer int
}

// NewHub returns a Hub with sensible defaults.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		clients:      make(map[chan Event]struct{}),
		logger:       logger,
		MaxClients:   256,
		ClientBuffer: 64,
	}
}

// Subscribe registers a new client. Returns the event channel and an
// unsubscribe function that must be called when the client disconnects.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, h.ClientBuffer)
	h.mu.Lock()
	if h.MaxClients > 0 && len(h.clients) >= h.MaxClients {
		h.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Publish sends evt to every subscriber. Drops events to slow clients
// rather than blocking — viewers are diagnostic, not the source of truth.
func (h *Hub) Publish(evt Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- evt:
		default:
			// dropped — viewer too slow
		}
	}
}

// SubscriberCount returns how many clients are currently attached.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// BroadcastObservation satisfies api.ViewerBroadcaster.
func (h *Hub) BroadcastObservation(ctx context.Context, sessionID, observationID, eventType string) {
	if eventType == "" {
		eventType = "raw_observation"
	}
	h.Publish(Event{
		Type:      "observation",
		SessionID: sessionID,
		ID:        observationID,
		Payload:   map[string]any{"event": eventType},
	})
}

// MarshalEvent is a small helper for tests / inspection.
func MarshalEvent(e Event) ([]byte, error) { return json.Marshal(e) }
