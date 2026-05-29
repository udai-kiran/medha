package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ConversationRow is a conversation record.
type ConversationRow struct {
	ID        string
	SessionID string
	Project   string
	Title     string
	CreatedAt string
	UpdatedAt string
	Messages  []*MessageRow
}

// MessageRow is a single message within a conversation.
type MessageRow struct {
	ID             string
	ConversationID string
	SessionID      string
	Project        string
	Role           string // user | assistant | system
	Content        string
	Metadata       map[string]any
	CreatedAt      string
}

// EnsureConversation upserts a conversation for the given session.
func (s *Store) EnsureConversation(ctx context.Context, sessionID, project string) (*ConversationRow, error) {
	// Return existing conversation for session if present.
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, COALESCE(project,''), COALESCE(title,''), created_at, updated_at
         FROM conversations WHERE session_id = $1 LIMIT 1`, sessionID)
	var c ConversationRow
	if err := row.Scan(&c.ID, &c.SessionID, &c.Project, &c.Title, &c.CreatedAt, &c.UpdatedAt); err == nil {
		return &c, nil
	}
	// Create new.
	c.ID = newID("conv")
	c.SessionID = sessionID
	c.Project = project
	now := time.Now().UTC().Format(time.RFC3339Nano)
	c.CreatedAt = now
	c.UpdatedAt = now
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO conversations (id, session_id, project, created_at, updated_at)
         VALUES ($1, $2, $3, $4, $4)`,
		c.ID, c.SessionID, c.Project, now)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// AddMessage appends a message to the session's conversation.
func (s *Store) AddMessage(ctx context.Context, sessionID, project, role, content string, metadata map[string]any) (*MessageRow, error) {
	if role == "" {
		return nil, errors.New("AddMessage: role required")
	}
	conv, err := s.EnsureConversation(ctx, sessionID, project)
	if err != nil {
		return nil, err
	}
	metaJSON, _ := json.Marshal(metadata)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	msgID := newID("msg")
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, session_id, project, role, content, metadata_json, created_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		msgID, conv.ID, sessionID, project, role, content, string(metaJSON), now)
	if err != nil {
		return nil, err
	}
	// Bump conversation updated_at.
	_, _ = s.DB.ExecContext(ctx,
		`UPDATE conversations SET updated_at = $1 WHERE id = $2`, now, conv.ID)
	return &MessageRow{
		ID: msgID, ConversationID: conv.ID, SessionID: sessionID,
		Project: project, Role: role, Content: content,
		Metadata: metadata, CreatedAt: now,
	}, nil
}

// GetConversation returns a conversation with its messages.
func (s *Store) GetConversation(ctx context.Context, sessionID string) (*ConversationRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, COALESCE(project,''), COALESCE(title,''), created_at, updated_at
         FROM conversations WHERE session_id = $1 LIMIT 1`, sessionID)
	var c ConversationRow
	if err := row.Scan(&c.ID, &c.SessionID, &c.Project, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, ErrNotFound
	}
	msgs, err := s.ListMessages(ctx, c.ID, 200)
	if err != nil {
		return nil, err
	}
	c.Messages = msgs
	return &c, nil
}

// ListMessages returns messages for a conversation ordered chronologically.
func (s *Store) ListMessages(ctx context.Context, conversationID string, limit int) ([]*MessageRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, conversation_id, session_id, COALESCE(project,''), role, content, metadata_json, created_at
         FROM messages WHERE conversation_id = $1
         ORDER BY created_at ASC LIMIT $2`,
		conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*MessageRow
	for rows.Next() {
		m := &MessageRow{}
		var metaRaw string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SessionID, &m.Project,
			&m.Role, &m.Content, &metaRaw, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaRaw), &m.Metadata)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SearchMessages does a simple content LIKE search across messages.
func (s *Store) SearchMessages(ctx context.Context, project, query string, limit int) ([]*MessageRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, conversation_id, session_id, COALESCE(project,''), role, content, metadata_json, created_at
         FROM messages
         WHERE ($1 = '' OR project = $2) AND LOWER(content) LIKE LOWER($3)
         ORDER BY created_at DESC LIMIT $4`,
		project, project, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*MessageRow
	for rows.Next() {
		m := &MessageRow{}
		var metaRaw string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SessionID, &m.Project,
			&m.Role, &m.Content, &metaRaw, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaRaw), &m.Metadata)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClearConversation deletes all messages for a session's conversation.
func (s *Store) ClearConversation(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM messages WHERE session_id = $1`, sessionID)
	return err
}
