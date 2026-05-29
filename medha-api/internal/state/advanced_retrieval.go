package state

import (
	"context"
	"strings"
	"time"
)

// --- G26: Facets ---

// AddFacet tags a memory with a dimension:value pair.
func (s *Store) AddFacet(ctx context.Context, memoryID, dimension, value string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO memory_facets (memory_id, dimension, value, created_at)
        VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING
    `, memoryID, dimension, value, now)
	return err
}

// QueryFacets returns memory IDs matching ALL provided dimension:value pairs.
func (s *Store) QueryFacets(ctx context.Context, project string, facets map[string][]string, limit int) ([]*MemoryRow, error) {
	if limit <= 0 {
		limit = 20
	}
	if len(facets) == 0 {
		return s.ListMemoriesByTier(ctx, project, "", limit)
	}
	// Build intersection: memory must have ALL dimension:value pairs.
	type kv struct{ d, v string }
	var pairs []kv
	for dim, vals := range facets {
		for _, v := range vals {
			pairs = append(pairs, kv{dim, v})
		}
	}
	// Use EXISTS subqueries for each pair.
	args := []any{project, project}
	conds := []string{}
	for _, p := range pairs {
		n := len(args) + 1
		args = append(args, p.d, p.v)
		conds = append(conds, "EXISTS (SELECT 1 FROM memory_facets f WHERE f.memory_id = m.id AND f.dimension = $"+itoa(n)+" AND f.value = $"+itoa(n+1)+")")
	}
	q := `SELECT id, project, type, tier, title, content,
               concepts_json, files_json, session_ids_json, source_observation_ids,
               strength, is_latest, created_at, updated_at, last_retrieved_at
          FROM memories m
          WHERE ($1 = '' OR project = $2)
          AND ` + strings.Join(conds, " AND ") + `
          ORDER BY strength DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*MemoryRow
	for rows.Next() {
		m, err := scanMemory(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- G27: Lessons ---

// LessonRow is a lesson extracted from a session.
type LessonRow struct {
	ID        string
	Project   string
	SessionID string
	Lesson    string
	Context   string
	Strength  float64
	CreatedAt string
}

// AddLesson stores a lesson extracted from a session.
func (s *Store) AddLesson(ctx context.Context, project, sessionID, lesson, lessonContext string) (*LessonRow, error) {
	id := newID("les")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO lessons (id, project, session_id, lesson, context, strength, created_at)
         VALUES ($1, $2, $3, $4, $5, 1.0, $6)`,
		id, project, sessionID, lesson, lessonContext, now)
	if err != nil {
		return nil, err
	}
	return &LessonRow{ID: id, Project: project, SessionID: sessionID, Lesson: lesson, Context: lessonContext, Strength: 1.0, CreatedAt: now}, nil
}

// SearchLessons searches lessons by topic text.
func (s *Store) SearchLessons(ctx context.Context, project, topic string, limit int) ([]*LessonRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + topic + "%"
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, session_id, lesson, COALESCE(context,''), strength, created_at
        FROM lessons
        WHERE ($1 = '' OR project = $2) AND LOWER(lesson) LIKE LOWER($3)
        ORDER BY strength DESC, created_at DESC LIMIT $4
    `, project, project, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*LessonRow
	for rows.Next() {
		l := &LessonRow{}
		if rows.Scan(&l.ID, &l.Project, &l.SessionID, &l.Lesson, &l.Context, &l.Strength, &l.CreatedAt) == nil {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

// --- G28: Skills ---

// SkillRow is an acquired skill record.
type SkillRow struct {
	ID               string
	Project          string
	SkillName        string
	Level            string // novice | competent | expert
	EvidenceCount    int
	LastDemonstrated string
	CreatedAt        string
}

// UpsertSkill records or increments evidence for a skill.
func (s *Store) UpsertSkill(ctx context.Context, project, skillName string) (*SkillRow, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newID("skill")
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO skills (id, project, skill_name, level, evidence_count, last_demonstrated, created_at)
        VALUES ($1, $2, $3, 'novice', 1, $4, $4)
        ON CONFLICT(project, skill_name) DO UPDATE SET
            evidence_count    = skills.evidence_count + 1,
            last_demonstrated = excluded.last_demonstrated,
            level             = CASE
                WHEN skills.evidence_count + 1 >= 10 THEN 'expert'
                WHEN skills.evidence_count + 1 >= 3  THEN 'competent'
                ELSE 'novice'
            END
    `, id, project, skillName, now)
	if err != nil {
		return nil, err
	}
	// Read back the updated row.
	row := &SkillRow{}
	_ = s.DB.QueryRowContext(ctx,
		`SELECT id, project, skill_name, level, evidence_count, last_demonstrated, created_at
         FROM skills WHERE project = $1 AND skill_name = $2`,
		project, skillName,
	).Scan(&row.ID, &row.Project, &row.SkillName, &row.Level, &row.EvidenceCount, &row.LastDemonstrated, &row.CreatedAt)
	return row, nil
}

// SearchSkills returns skills matching a name prefix.
func (s *Store) SearchSkills(ctx context.Context, project, skillName string, limit int) ([]*SkillRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + skillName + "%"
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, skill_name, level, evidence_count, last_demonstrated, created_at
        FROM skills
        WHERE ($1 = '' OR project = $2) AND LOWER(skill_name) LIKE LOWER($3)
        ORDER BY evidence_count DESC LIMIT $4
    `, project, project, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*SkillRow
	for rows.Next() {
		sk := &SkillRow{}
		if rows.Scan(&sk.ID, &sk.Project, &sk.SkillName, &sk.Level, &sk.EvidenceCount, &sk.LastDemonstrated, &sk.CreatedAt) == nil {
			out = append(out, sk)
		}
	}
	return out, rows.Err()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
