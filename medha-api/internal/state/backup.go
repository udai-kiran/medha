package state

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Backup writes a snapshot of the SQLite database to the given path. Uses
// SQLite's VACUUM INTO so the snapshot is a clean, transactionally consistent
// copy without holding a long read lock.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if s == nil || s.DB == nil {
		return errors.New("Backup: store not open")
	}
	if dst == "" {
		return errors.New("Backup: dst required")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("Backup: mkdir: %w", err)
	}
	// VACUUM INTO requires the target file not to exist.
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("Backup: target %q already exists", dst)
	}
	_, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", dst)
	if err != nil {
		return fmt.Errorf("Backup: vacuum into: %w", err)
	}
	return nil
}

// Restore replaces the open DB's contents with the snapshot at `src`. The
// store should be closed by the caller first; this function does a raw file
// swap, which is reliable for SQLite WAL files because the new file carries
// its own WAL/SHM state on next open.
//
// Returns an error if `src` doesn't exist or the swap fails.
func Restore(src, dst string) error {
	if src == "" || dst == "" {
		return errors.New("Restore: src and dst required")
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("Restore: open src: %w", err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".restore.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("Restore: create tmp: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("Restore: copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("Restore: close: %w", err)
	}
	// Remove WAL/SHM sidecars so the next open creates fresh ones from the
	// restored snapshot.
	for _, sfx := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dst + sfx)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("Restore: rename: %w", err)
	}
	return nil
}
