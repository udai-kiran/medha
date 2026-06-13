package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ObservationPayload is POSTed to /v1/agentmemory/observe.
type ObservationPayload struct {
	HookType  string `json:"hookType"`
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Data      any    `json:"data"`
}

// RecallRequest is POSTed to /v1/agentmemory/recall-summary.
type RecallRequest struct {
	Query   string `json:"query"`
	Project string `json:"project"`
	Limit   int    `json:"limit"`
}

// RecallResponse is the response from recall-summary.
type RecallResponse struct {
	Summary string `json:"summary"`
	Results []struct {
		Relevance float64 `json:"relevance"`
	} `json:"results"`
}

// maxInjectionChars caps additionalContext to avoid flooding the LLM context.
const maxInjectionChars = 1500

var observeClient = &http.Client{Timeout: 5 * time.Second}
var recallClient = &http.Client{Timeout: 20 * time.Second}

// observeAsync enqueues an observation in a goroutine tracked by asyncWG.
// The goroutine completes the HTTP call before the binary exits (see main drain).
func observeAsync(cfg config, p ObservationPayload) {
	asyncWG.Add(1)
	go func() {
		defer asyncWG.Done()
		postObserve(cfg, p)
	}()
}

func postObserve(cfg config, p ObservationPayload) {
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", cfg.URL+"/v1/agentmemory/observe", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Secret)
	}
	resp, err := observeClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// recallSummary calls recall-summary synchronously (20 s timeout).
// Returns ("", nil) when no context is relevant.
func recallSummary(cfg config, query, project string) (string, error) {
	return recallSummaryWith(recallClient, cfg, query, project)
}

// recallSummaryShort calls recall-summary with a short timeout (for SessionStart
// where blocking the session startup is especially undesirable).
func recallSummaryShort(cfg config, query, project string) (string, error) {
	return recallSummaryWith(&http.Client{Timeout: 8 * time.Second}, cfg, query, project)
}

func recallSummaryWith(client *http.Client, cfg config, query, project string) (string, error) {
	body, _ := json.Marshal(RecallRequest{Query: query, Project: project, Limit: 5})
	req, err := http.NewRequest("POST", cfg.URL+"/v1/agentmemory/recall-summary", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var rr RecallResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", err
	}

	maxRel := 0.0
	for _, r := range rr.Results {
		if r.Relevance > maxRel {
			maxRel = r.Relevance
		}
	}
	if maxRel < 0.01 {
		return "", nil
	}
	// Cap to injection budget
	s := rr.Summary
	if runes := []rune(s); len(runes) > maxInjectionChars {
		s = string(runes[:maxInjectionChars]) + "…"
	}
	return s, nil
}

// significantTokenCount counts non-stopword tokens of length ≥ 2.
func significantTokenCount(s string) int {
	stopwords := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "can": true,
		"check": true, "did": true, "do": true, "does": true, "for": true,
		"hello": true, "hey": true, "hi": true, "in": true, "is": true,
		"it": true, "me": true, "no": true, "now": true, "of": true,
		"ok": true, "okay": true, "on": true, "or": true, "please": true,
		"that": true, "the": true, "this": true, "to": true, "was": true,
		"were": true, "with": true, "yes": true, "you": true,
	}
	count := 0
	for _, tok := range tokenize(s) {
		if len(tok) >= 2 && !stopwords[tok] {
			count++
		}
	}
	return count
}

func tokenize(s string) []string {
	var words []string
	var buf strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			buf.WriteRune(r)
		} else if buf.Len() > 0 {
			words = append(words, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		words = append(words, buf.String())
	}
	return words
}

// truncate returns the first n runes of s, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
