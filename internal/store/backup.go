package store

import (
	"context"
	"fmt"
)

// BackupTo writes a consistent, compacted snapshot of the database to dest
// using VACUUM INTO. The snapshot is a self-contained SQLite file (no WAL
// sidecar), safe to take while the service is running. dest must not exist.
func (s *Store) BackupTo(ctx context.Context, dest string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}
