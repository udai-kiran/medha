package state

import (
	"context"
	"fmt"
)

// Backup is not supported for PostgreSQL via this API.
// Use pg_dump externally:
//
//	pg_dump -U <user> -h <host> <dbname> > backup.sql
func (s *Store) Backup(_ context.Context, dst string) error {
	return fmt.Errorf("Backup: not supported for PostgreSQL; use pg_dump > %s", dst)
}

// Restore is not supported for PostgreSQL via this API.
// Use psql externally:
//
//	psql -U <user> -h <host> <dbname> < backup.sql
func Restore(src, _ string) error {
	return fmt.Errorf("Restore: not supported for PostgreSQL; use psql < %s", src)
}
