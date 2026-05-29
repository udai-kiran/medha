package state

import (
	"context"
	"fmt"
	"time"
)

// DiagnosticReport is the result of a system health check.
type DiagnosticReport struct {
	Healthy    bool              `json:"healthy"`
	CheckedAt  string            `json:"checkedAt"`
	Issues     []DiagnosticIssue `json:"issues"`
	Stats      DiagnosticStats   `json:"stats"`
}

// DiagnosticIssue describes a single health problem.
type DiagnosticIssue struct {
	Component   string `json:"component"`
	Severity    string `json:"severity"` // warning | error
	Message     string `json:"message"`
	Fixable     bool   `json:"fixable"`
}

// DiagnosticStats are basic counts for the health dashboard.
type DiagnosticStats struct {
	Sessions         int `json:"sessions"`
	Observations     int `json:"observations"`
	CompressedObs    int `json:"compressedObservations"`
	UncompressedObs  int `json:"uncompressedObservations"`
	Memories         int `json:"memories"`
	BM25Docs         int `json:"bm25Docs"`
	VectorDocs       int `json:"vectorDocs"`
	StuckSessions    int `json:"stuckSessions"`
}

// Diagnose runs all health checks and returns a report.
func (s *Store) Diagnose(ctx context.Context) (*DiagnosticReport, error) {
	r := &DiagnosticReport{
		Healthy:   true,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// DB connectivity.
	if err := s.DB.PingContext(ctx); err != nil {
		r.Healthy = false
		r.Issues = append(r.Issues, DiagnosticIssue{
			Component: "database", Severity: "error",
			Message: fmt.Sprintf("ping failed: %v", err), Fixable: false,
		})
		return r, nil // No point checking further.
	}

	// Counts.
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&r.Stats.Sessions)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&r.Stats.Observations)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE compressed = 1`).Scan(&r.Stats.CompressedObs)
	r.Stats.UncompressedObs = r.Stats.Observations - r.Stats.CompressedObs
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&r.Stats.Memories)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM bm25_docs`).Scan(&r.Stats.BM25Docs)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vector_docs`).Scan(&r.Stats.VectorDocs)

	// Index consistency: BM25 doc count should roughly match compressed observations.
	if r.Stats.CompressedObs > 0 && r.Stats.BM25Docs == 0 {
		r.Healthy = false
		r.Issues = append(r.Issues, DiagnosticIssue{
			Component: "bm25_index", Severity: "warning",
			Message: fmt.Sprintf("%d compressed observations but 0 BM25 docs — index may need rebuild", r.Stats.CompressedObs),
			Fixable: true,
		})
	}

	// Stuck sessions: active sessions older than 48h.
	cutoff := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE status = 'active' AND started_at < $1`, cutoff,
	).Scan(&r.Stats.StuckSessions)
	if r.Stats.StuckSessions > 0 {
		r.Issues = append(r.Issues, DiagnosticIssue{
			Component: "sessions", Severity: "warning",
			Message: fmt.Sprintf("%d sessions still 'active' after 48h", r.Stats.StuckSessions),
			Fixable: true,
		})
	}

	// Orphaned observations: observations whose session_id doesn't exist.
	var orphaned int
	_ = s.DB.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM observations o
        WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = o.session_id)
    `).Scan(&orphaned)
	if orphaned > 0 {
		r.Issues = append(r.Issues, DiagnosticIssue{
			Component: "observations", Severity: "warning",
			Message: fmt.Sprintf("%d observations with no parent session", orphaned),
			Fixable: true,
		})
	}

	if len(r.Issues) > 0 {
		// Only set unhealthy for errors, not warnings.
		for _, iss := range r.Issues {
			if iss.Severity == "error" {
				r.Healthy = false
				break
			}
		}
	}
	return r, nil
}

// HealResult describes what the heal operation fixed.
type HealResult struct {
	Fixed   []string `json:"fixed"`
	Skipped []string `json:"skipped"`
}

// Heal attempts to auto-fix known issue patterns found by Diagnose.
func (s *Store) Heal(ctx context.Context) (*HealResult, error) {
	result := &HealResult{}

	// Fix: mark stuck sessions as abandoned.
	cutoff := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET status = 'abandoned', ended_at = $1
         WHERE status = 'active' AND started_at < $2`,
		time.Now().UTC().Format(time.RFC3339Nano), cutoff)
	if err == nil {
		n, _ := res.RowsAffected()
		if n > 0 {
			result.Fixed = append(result.Fixed, fmt.Sprintf("marked %d stuck sessions as abandoned", n))
		}
	}

	// Fix: delete orphaned observations (no parent session).
	res2, err := s.DB.ExecContext(ctx, `
        DELETE FROM observations
        WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = observations.session_id)
    `)
	if err == nil {
		n, _ := res2.RowsAffected()
		if n > 0 {
			result.Fixed = append(result.Fixed, fmt.Sprintf("removed %d orphaned observations", n))
		}
	}

	// Fix: clean expired dedup window entries.
	expiry := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	res3, err := s.DB.ExecContext(ctx,
		`DELETE FROM dedup_window WHERE seen_at < $1`, expiry)
	if err == nil {
		n, _ := res3.RowsAffected()
		if n > 0 {
			result.Fixed = append(result.Fixed, fmt.Sprintf("pruned %d expired dedup entries", n))
		}
	}

	if len(result.Fixed) == 0 {
		result.Fixed = []string{}
	}
	return result, nil
}
