package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
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

// UpsertSettings updates a related group of settings in one transaction.
func (s *Store) UpsertSettings(ctx context.Context, values map[string]string) error {
	return s.UpdateSettings(ctx, values, nil)
}

// UpdateSettings atomically upserts values and removes deleteKeys. It is used
// when a configuration change must invalidate related persisted state in the
// same transaction.
func (s *Store) UpdateSettings(ctx context.Context, values map[string]string, deleteKeys []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	now := formatTime(time.Now())
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, values[key], now); err != nil {
			return err
		}
	}
	deleteKeys = append([]string(nil), deleteKeys...)
	sort.Strings(deleteKeys)
	for _, key := range deleteKeys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}
