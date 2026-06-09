package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GetSetting returns the value stored under key. found is false when no row
// exists for the key.
func (s *Store) GetSetting(ctx context.Context, key string) (value string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// UpsertSetting inserts or updates the value for key.
func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, now)
	return err
}
