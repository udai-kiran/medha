package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/models"
	"github.com/udai-kiran/medha/internal/privacy"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/telemetry"
)

// CompressionEnqueuer is the narrow interface Task 12 (queue) will satisfy.
// While Task 12 is pending we use NoOpEnqueuer; observations are stored but
// not compressed yet — the search index in Task 14/15 reads either way.
type CompressionEnqueuer interface {
	EnqueueCompress(ctx context.Context, observationID, sessionID string) error
}

// NoOpEnqueuer is the stub implementation used until Task 12 wires RabbitMQ.
type NoOpEnqueuer struct{}

// EnqueueCompress drops the job on the floor.
func (NoOpEnqueuer) EnqueueCompress(ctx context.Context, observationID, sessionID string) error {
	return nil
}

// ViewerBroadcaster is satisfied by Task 28's WebSocket hub; until then we
// use a no-op so the handler signature is stable.
type ViewerBroadcaster interface {
	BroadcastObservation(ctx context.Context, sessionID, observationID, event string)
}

// NoOpBroadcaster discards broadcast events (placeholder until Task 28).
type NoOpBroadcaster struct{}

// BroadcastObservation does nothing.
func (NoOpBroadcaster) BroadcastObservation(ctx context.Context, sessionID, observationID, event string) {
}

// SessionEndHandler is satisfied by Task 22's consolidation orchestrator.
// Stubbed by default; the observe handler calls it on SessionEnd hooks so
// the end-to-end shape works today even without consolidation.
type SessionEndHandler interface {
	OnSessionEnd(ctx context.Context, sessionID string) error
}

// NoOpSessionEndHandler marks the session ended in state and nothing else.
type NoOpSessionEndHandler struct{ Store *state.Store }

// OnSessionEnd flips the session row to 'completed'.
func (h NoOpSessionEndHandler) OnSessionEnd(ctx context.Context, sessionID string) error {
	if h.Store == nil {
		return nil
	}
	return h.Store.MarkSessionEnded(ctx, sessionID)
}

// ObserveDeps groups the collaborators ObserveHandler needs. Wiring them
// here (rather than reading globals) lets tests inject test doubles and
// later tasks swap NoOp* for real implementations.
type ObserveDeps struct {
	Store           *state.Store
	Deduper         dedup.Deduper
	Enqueuer        CompressionEnqueuer
	Broadcaster     ViewerBroadcaster
	SessionEnd      SessionEndHandler
	MaxImageBytes   int           // 0 → 4 MiB default
	HandlerDeadline time.Duration // 0 → no extra deadline applied
}

// ObserveResponse is the success body of POST /agentmemory/observe.
type ObserveResponse struct {
	ObservationID string `json:"observationId"`
	Compressing   bool   `json:"compressing"`
	Compressed    bool   `json:"compressed"`
	Deduplicated  bool   `json:"deduplicated,omitempty"`
}

// ObserveHandler returns the chi handler. Keeps everything except validation
// in a single function so the hot path stays inlined and easy to benchmark.
func ObserveHandler(deps ObserveDeps) http.HandlerFunc {
	if deps.MaxImageBytes <= 0 {
		deps.MaxImageBytes = 4 * 1024 * 1024
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := telemetry.LoggerFrom(ctx)

		// 1. Parse + validate payload shape.
		var payload models.HookPayload
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&payload); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
			return
		}
		if err := payload.Validate(); err != nil {
			WriteError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}

		// 2. Privacy filter FIRST — before any persistence or enqueue.
		filteredRaw, hadSecrets, err := filterPayload(payload.Data)
		if err != nil {
			log.Error("observe.filter_failed", "err", err)
			WriteError(w, http.StatusBadRequest, "filter_failed", err.Error())
			return
		}

		// 3. Session lifecycle.
		project := payload.Project
		if _, err := deps.Store.EnsureSession(ctx, payload.SessionID, project, payload.CWD); err != nil {
			log.Error("observe.session_ensure", "err", err)
			WriteError(w, http.StatusInternalServerError, "session_failed", "could not ensure session")
			return
		}

		// SessionEnd → route to consolidation handler and return 202.
		if payload.HookType == models.HookSessionEnd {
			if err := deps.SessionEnd.OnSessionEnd(ctx, payload.SessionID); err != nil {
				log.Error("observe.session_end", "err", err)
				// Even if consolidation enqueue fails we already accepted.
			}
			writeJSON(w, http.StatusAccepted, ObserveResponse{Compressing: false})
			return
		}

		// 4. Extract tool fields from the *filtered* payload (never the original).
		toolName, toolInputJSON, toolOutput, userPrompt := extractToolFields(filteredRaw)

		// 5. Dedup check.
		dedupKey, err := dedup.ComputeKey(payload.SessionID, toolName, toolInputJSON)
		if err != nil {
			log.Warn("observe.dedup_key_failed", "err", err)
		} else {
			seen, derr := deps.Deduper.Seen(ctx, payload.SessionID, dedupKey)
			if derr == nil && seen {
				writeJSON(w, http.StatusAccepted, ObserveResponse{Deduplicated: true})
				return
			}
		}

		// 6. Extract inline image (FR-5). modality already derived from raw.
		modality, imageRef := detectModality(filteredRaw, deps.MaxImageBytes)

		obsID := newObservationID()
		row := &state.ObservationRow{
			ID:            obsID,
			SessionID:     payload.SessionID,
			Project:       project,
			HookType:      string(payload.HookType),
			ToolName:      toolName,
			ToolInputJSON: string(toolInputJSON),
			ToolOutput:    toolOutput,
			UserPrompt:    userPrompt,
			RawJSON:       string(filteredRaw),
			Modality:      string(modality),
			ImageRef:      imageRef,
			HasSecrets:    hadSecrets,
			CreatedAt:     payload.Timestamp,
		}
		if row.CreatedAt.IsZero() {
			row.CreatedAt = time.Now().UTC()
		}

		if err := deps.Store.InsertRawObservation(ctx, row); err != nil {
			log.Error("observe.persist", "err", err)
			WriteError(w, http.StatusInternalServerError, "persist_failed", "could not store observation")
			return
		}
		if err := deps.Store.IncrementSessionObservationCount(ctx, payload.SessionID); err != nil {
			log.Warn("observe.increment_count_failed", "err", err)
		}

		// 7. Side effects (best-effort; never block the response on these).
		deps.Broadcaster.BroadcastObservation(ctx, payload.SessionID, obsID, "raw_observation")
		if err := deps.Enqueuer.EnqueueCompress(ctx, obsID, payload.SessionID); err != nil {
			log.Warn("observe.enqueue_compress_failed", "err", err)
		}

		writeJSON(w, http.StatusCreated, ObserveResponse{
			ObservationID: obsID,
			Compressing:   true,
			Compressed:    false,
		})
	}
}

// filterPayload applies the privacy filter to the raw payload bytes. Returns
// the filtered JSON, whether any secret pattern fired, and an error if the
// input was not valid JSON.
func filterPayload(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return json.RawMessage("null"), false, nil
	}
	// Sanity-check it parses; reject malformed JSON at the boundary so the
	// privacy filter cannot accidentally let through binary garbage.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, errors.New("data must be valid JSON")
	}
	// Re-encode (compact) so the filtered/unfiltered comparison is meaningful.
	compact, err := json.Marshal(v)
	if err != nil {
		return nil, false, err
	}
	filtered, res := privacy.Filter(string(compact))
	return json.RawMessage(filtered), res.HadSecrets, nil
}

// extractToolFields plucks the well-known tool fields from a filtered payload
// without imposing structure on the rest. Anything missing returns "".
func extractToolFields(raw json.RawMessage) (toolName string, toolInput json.RawMessage, toolOutput, userPrompt string) {
	var probe struct {
		ToolName   string          `json:"tool_name"`
		ToolInput  json.RawMessage `json:"tool_input"`
		ToolOutput any             `json:"tool_output"`
		UserPrompt string          `json:"user_prompt"`
	}
	_ = json.Unmarshal(raw, &probe)
	toolName = probe.ToolName
	toolInput = probe.ToolInput
	userPrompt = probe.UserPrompt
	switch v := probe.ToolOutput.(type) {
	case string:
		toolOutput = v
	case nil:
		toolOutput = ""
	default:
		if b, err := json.Marshal(v); err == nil {
			toolOutput = string(b)
		}
	}
	return
}

var dataImageRE = regexp.MustCompile(`data:image/[a-zA-Z0-9.+\-]+;base64,([A-Za-z0-9+/=]{16,})`)

// detectModality inspects the filtered payload for inline images. If a
// data:image/... URL is present, the modality is set accordingly and the
// blob reference is the truncated marker (Task 28 stores full blobs).
func detectModality(raw json.RawMessage, maxBytes int) (models.Modality, string) {
	if len(raw) > maxBytes {
		// Truncate the search to avoid pathological scans on giant payloads.
		raw = raw[:maxBytes]
	}
	match := dataImageRE.FindIndex(raw)
	if match == nil {
		return models.ModalityText, ""
	}
	// Modality is "mixed" when there is both prose text AND an image; "image"
	// when the payload is essentially just the image. Heuristic: <100 bytes
	// outside the data URL → "image"; otherwise "mixed".
	imgLen := match[1] - match[0]
	if len(raw)-imgLen < 100 {
		return models.ModalityImage, "inline-truncated"
	}
	return models.ModalityMixed, "inline-truncated"
}

func newObservationID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "obs-" + hex.EncodeToString(b[:])
}

// hookTypeIsValidString is a small helper used in benchmarks/tests that
// don't import the models package directly.
func hookTypeIsValidString(s string) bool {
	return models.HookType(strings.ToLower(s)).IsValid()
}
