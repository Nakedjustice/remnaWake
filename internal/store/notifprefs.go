package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetNotificationPrefs returns the per-user mute flags. Both default to false
// (notifications enabled) when no row exists for telegramID.
func (s *Store) GetNotificationPrefs(ctx context.Context, telegramID int64) (expiryMuted, winbackMuted bool, err error) {
	var em, wm int
	err = s.db.QueryRowContext(ctx,
		`SELECT expiry_muted, winback_muted FROM notification_prefs WHERE telegram_id = ?`,
		telegramID).Scan(&em, &wm)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return em == 1, wm == 1, nil
}

// SetNotificationPref upserts a single mute flag for telegramID, leaving the
// other flag unchanged. kind must be NotificationExpiry or NotificationWinback.
func (s *Store) SetNotificationPref(ctx context.Context, telegramID int64, kind string, muted bool) error {
	var col string
	switch kind {
	case NotificationExpiry:
		col = "expiry_muted"
	case NotificationWinback:
		col = "winback_muted"
	default:
		return fmt.Errorf("unknown notification kind: %q", kind)
	}
	val := 0
	if muted {
		val = 1
	}
	// On insert, default the non-target column to 0; on conflict, update only the
	// target column. col is a fixed string (never user input), safe to inline.
	em, wm := 0, 0
	if col == "expiry_muted" {
		em = val
	} else {
		wm = val
	}
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_prefs (telegram_id, expiry_muted, winback_muted, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(telegram_id) DO UPDATE SET `+col+` = ?, updated_at = ?
	`, telegramID, em, wm, now, val, now)
	return err
}

// NotificationMuted reports whether telegramID has muted notifications of the
// given kind. Unknown kinds are treated as not muted.
func (s *Store) NotificationMuted(ctx context.Context, telegramID int64, kind string) (bool, error) {
	expiryMuted, winbackMuted, err := s.GetNotificationPrefs(ctx, telegramID)
	if err != nil {
		return false, err
	}
	switch kind {
	case NotificationExpiry:
		return expiryMuted, nil
	case NotificationWinback:
		return winbackMuted, nil
	default:
		return false, nil
	}
}
