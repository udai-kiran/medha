package state

import (
	"context"
	"testing"
)

func TestBackup_ReturnsError(t *testing.T) {
	s := &Store{}
	if err := s.Backup(context.Background(), "/tmp/backup.sql"); err == nil {
		t.Error("expected error from Backup stub")
	}
}

func TestRestore_ReturnsError(t *testing.T) {
	if err := Restore("/tmp/backup.sql", "unused"); err == nil {
		t.Error("expected error from Restore stub")
	}
}
