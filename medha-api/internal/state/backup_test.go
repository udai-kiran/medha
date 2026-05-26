package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackup_RoundTrip(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src.db")
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureSession(ctx, "sess-1", "p", "/cwd"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Size() == 0 {
		t.Fatalf("snapshot missing or empty: %v %v", err, info)
	}
	_ = s.Close()

	// Open the snapshot directly and verify the session is there.
	snap, err := Open(ctx, Options{Path: dst})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Close() }()
	sess, err := snap.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("session missing from snapshot: %v", err)
	}
	if sess.Project != "p" {
		t.Errorf("project mismatch: %+v", sess)
	}
}

func TestBackup_RejectsExistingTarget(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src.db")
	ctx := context.Background()
	s, _ := Open(ctx, Options{Path: srcPath})
	defer func() { _ = s.Close() }()

	dst := filepath.Join(t.TempDir(), "snap.db")
	_ = os.WriteFile(dst, []byte{1}, 0o644)
	if err := s.Backup(ctx, dst); err == nil {
		t.Error("expected error when target exists")
	}
}

func TestRestore_ReplacesContents(t *testing.T) {
	ctx := context.Background()

	// Create a source DB with data.
	srcPath := filepath.Join(t.TempDir(), "src.db")
	src, _ := Open(ctx, Options{Path: srcPath})
	_, _ = src.EnsureSession(ctx, "sess-snap", "p", "")
	snapshot := filepath.Join(t.TempDir(), "snap.db")
	_ = src.Backup(ctx, snapshot)
	_ = src.Close()

	// Create a target DB with different data, then close it.
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst, _ := Open(ctx, Options{Path: dstPath})
	_, _ = dst.EnsureSession(ctx, "sess-other", "p", "")
	_ = dst.Close()

	if err := Restore(snapshot, dstPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored, _ := Open(ctx, Options{Path: dstPath})
	defer func() { _ = restored.Close() }()
	if _, err := restored.GetSession(ctx, "sess-snap"); err != nil {
		t.Errorf("expected sess-snap after restore: %v", err)
	}
	if _, err := restored.GetSession(ctx, "sess-other"); err != ErrNotFound {
		t.Errorf("sess-other should be gone after restore, got %v", err)
	}
}
