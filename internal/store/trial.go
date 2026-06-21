package store

import (
	"context"
	"time"
)

// ClaimTrial atomically records that telegramID has claimed the one-time free
// trial. It returns true when the claim was newly recorded, false when this
// Telegram ID already claimed a trial. Race-safe: concurrent claims collapse to
// a single winner via the primary-key conflict, the same idempotency pattern as
// RedeemGiftCode.
func (s *Store) ClaimTrial(ctx context.Context, telegramID int64, username string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO trial_claims (telegram_id, username, claimed_at)
		VALUES (?, ?, ?)
		ON CONFLICT(telegram_id) DO NOTHING
	`, telegramID, username, formatTime(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ReleaseTrial removes a trial claim. Used to roll back when profile creation
// fails after the claim, so a transient panel error doesn't burn the user's one
// and only trial.
func (s *Store) ReleaseTrial(ctx context.Context, telegramID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM trial_claims WHERE telegram_id = ?`, telegramID)
	return err
}
