package state

import (
	"context"
	"fmt"
	"strings"
)

// ContextRequest controls what the assembled context includes.
type ContextRequest struct {
	Project          string
	SessionID        string
	Query            string // used for semantic memory retrieval
	IncludeShortTerm bool
	IncludeLongTerm  bool
	IncludeReasoning bool
	IncludeSlots     bool
	MaxItems         int
}

// ContextResult is the assembled injection-ready context.
type ContextResult struct {
	Context    string            `json:"context"`
	TokenEst   int               `json:"tokenEstimate"`
	Sources    map[string]int    `json:"sources"` // source type → item count
}

// AssembleContext gathers memories, messages, preferences, facts, slots, and
// recent reasoning traces into a formatted string for LLM injection.
func (s *Store) AssembleContext(ctx context.Context, req ContextRequest) (*ContextResult, error) {
	if req.MaxItems <= 0 {
		req.MaxItems = 10
	}
	var parts []string
	sources := map[string]int{}

	// Long-term memories (semantic search or top by strength).
	if req.IncludeLongTerm {
		mems, err := s.ListMemoriesByTier(ctx, req.Project, TierSemantic, req.MaxItems)
		if err == nil && len(mems) > 0 {
			var sb strings.Builder
			sb.WriteString("## Memories\n")
			for _, m := range mems {
				fmt.Fprintf(&sb, "- **%s**: %s\n", m.Title, truncate(m.Content, 200))
			}
			parts = append(parts, sb.String())
			sources["memories"] = len(mems)
		}

		// Preferences.
		prefs, err := s.SearchPreferences(ctx, req.Project, "", req.Query, req.MaxItems/2)
		if err == nil && len(prefs) > 0 {
			var sb strings.Builder
			sb.WriteString("## Preferences\n")
			for _, p := range prefs {
				fmt.Fprintf(&sb, "- [%s] %s\n", p.Category, p.Preference)
			}
			parts = append(parts, sb.String())
			sources["preferences"] = len(prefs)
		}

		// Facts.
		facts, err := s.SearchFacts(ctx, req.Project, "", "", req.Query, req.MaxItems/2)
		if err == nil && len(facts) > 0 {
			var sb strings.Builder
			sb.WriteString("## Facts\n")
			for _, f := range facts {
				fmt.Fprintf(&sb, "- %s %s %s\n", f.Subject, f.Predicate, f.ObjectVal)
			}
			parts = append(parts, sb.String())
			sources["facts"] = len(facts)
		}
	}

	// Short-term conversation history.
	if req.IncludeShortTerm && req.SessionID != "" {
		msgs, err := s.ListMessages(ctx, "", req.MaxItems)
		if err == nil && len(msgs) > 0 {
			// Fetch the conversation ID first.
			conv, convErr := s.GetConversation(ctx, req.SessionID)
			if convErr == nil {
				msgs, _ = s.ListMessages(ctx, conv.ID, req.MaxItems)
				if len(msgs) > 0 {
					var sb strings.Builder
					sb.WriteString("## Recent Conversation\n")
					for _, m := range msgs {
						fmt.Fprintf(&sb, "**%s**: %s\n", m.Role, truncate(m.Content, 300))
					}
					parts = append(parts, sb.String())
					sources["messages"] = len(msgs)
				}
			}
		}
	}

	// Reasoning traces (similar past tasks).
	if req.IncludeReasoning && req.Query != "" {
		traces, err := s.SearchTraces(ctx, req.Project, req.Query, req.MaxItems/2)
		if err == nil && len(traces) > 0 {
			var sb strings.Builder
			sb.WriteString("## Past Reasoning\n")
			for _, t := range traces {
				outcome := t.Outcome
				if outcome == "" {
					outcome = "in progress"
				}
				fmt.Fprintf(&sb, "- Task: %s → %s\n", t.Task, truncate(outcome, 100))
			}
			parts = append(parts, sb.String())
			sources["traces"] = len(traces)
		}
	}

	// Slots (pinned context blocks).
	if req.IncludeSlots {
		slots, err := s.ListSlots(ctx, req.Project)
		if err == nil && len(slots) > 0 {
			var sb strings.Builder
			sb.WriteString("## Pinned Context\n")
			for _, sl := range slots {
				name, _ := sl["slotName"].(string)
				content, _ := sl["content"].(string)
				if content != "" {
					fmt.Fprintf(&sb, "### %s\n%s\n", name, truncate(content, 500))
				}
			}
			if sb.Len() > 20 {
				parts = append(parts, sb.String())
				sources["slots"] = len(slots)
			}
		}
	}

	combined := strings.Join(parts, "\n")
	return &ContextResult{
		Context:  combined,
		TokenEst: len(strings.Fields(combined)) * 4 / 3, // rough token estimate
		Sources:  sources,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
