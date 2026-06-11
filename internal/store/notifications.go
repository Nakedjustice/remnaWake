package store

import (
	"context"
	"time"
)

// Notification kinds recorded in sent_notifications.
const (
	NotificationExpiry  = "expiry"  // pre-expiry reminder; milestone = days before expiry
	NotificationWinback = "winback" // post-expiry win-back; milestone = days after expiry
)

// TryMarkNotificationSent atomically claims the right to send one notification,
// identified by (user, kind, milestone, expireAt). It returns true when the
// caller owns the send and false when it was already claimed, so a restarted
// run cannot deliver the same notice twice. Including expireAt in the key
// re-arms every milestone automatically once the subscription is renewed.
func (s *Store) TryMarkNotificationSent(ctx context.Context, remnawaveID int64, kind string, milestone int, expireAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sent_notifications (remnawave_user_id, kind, milestone, expire_at, sent_at)
		VALUES (?, ?, ?, ?, ?)
	`, remnawaveID, kind, milestone, formatTime(expireAt), formatTime(time.Now()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UnmarkNotificationSent releases a claim made by TryMarkNotificationSent so a
// later run retries the send (used when the Telegram delivery failed).
func (s *Store) UnmarkNotificationSent(ctx context.Context, remnawaveID int64, kind string, milestone int, expireAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM sent_notifications
		WHERE remnawave_user_id = ? AND kind = ? AND milestone = ? AND expire_at = ?
	`, remnawaveID, kind, milestone, formatTime(expireAt))
	return err
}
