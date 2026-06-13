package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const maxTranscriptDelta = 2 * 1024 * 1024 // 2 MB max per ingest pass

// transcriptLine is a single entry in the Claude Code transcript JSONL.
// The real format (observed from .claude/projects/*.jsonl) has type="assistant"
// with message.content as a JSON array of typed blocks.
type transcriptLine struct {
	Type    string         `json:"type"`
	Message *transcriptMsg `json:"message,omitempty"`
}

type transcriptMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ingestTranscriptRange reads the transcript in [from, to) bytes, extracts
// assistant text blocks, and posts a transcript_delta observation.
// to=0 or to<=from is a no-op. Call with a pre-claimed range to avoid
// duplicate ingestion across concurrent hook processes.
func ingestTranscriptRange(cfg config, base BaseInput, path string, from, to int64) {
	if path == "" || to <= from {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck

	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return
		}
	}

	limit := to - from
	if limit > maxTranscriptDelta {
		limit = maxTranscriptDelta
	}

	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil || len(data) == 0 {
		return
	}

	var texts []string
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" || entry.Message == nil {
			continue
		}
		if text := extractAssistantText(entry.Message.Content); text != "" {
			texts = append(texts, text)
		}
	}

	if len(texts) == 0 {
		return
	}
	combined := strings.Join(texts, "\n\n---\n\n")
	// Cap to 8000 runes so the observation stays within server limits
	if runes := []rune(combined); len(runes) > 8000 {
		combined = string(runes[:8000]) + "…"
	}
	capture(cfg, "transcript_delta", base, base.CWD, map[string]any{
		"text": combined,
	})
}

// extractAssistantText extracts non-empty text from a message content field.
// content is either a plain JSON string or a JSON array of typed blocks;
// thinking and tool_use blocks are skipped.
func extractAssistantText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try plain string (some older entries use this form)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	// Try array of typed blocks
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}
