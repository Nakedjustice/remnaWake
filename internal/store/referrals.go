package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Referral records that referrerTelegramID brought in referredTelegramID via a
// referral link. The referred user is the primary key, so a user can be
// referred only once (the first link wins). status transitions pending ->
// credited exactly once when the referred user makes their first payment.
type Referral struct {
	ReferredTelegramID int64
	ReferrerTelegramID int64
	Status             string // "pending" | "credited"
	CreatedAt          time.Time
	CreditedAt         *time.Time
}

// CreateReferral records a pending referral. It is a no-op (returns false) when
// the referred user already has a referral, so the first referrer wins and a
// later link click cannot reassign or overwrite it.
func (s *Store) CreateReferral(ctx context.Context, referredTGID, referrerTGID int64, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO referrals (referred_telegram_id, referrer_telegram_id, status, created_at)
		VALUES (?, ?, 'pending', ?)
		ON CONFLICT(referred_telegram_id) DO NOTHING
	`, referredTGID, referrerTGID, formatTime(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// GetReferral returns the referral recorded for the given referred user, or nil
// when none exists.
func (s *Store) GetReferral(ctx context.Context, referredTGID int64) (*Referral, error) {
	var (
		r          Referral
		created    string
		creditedAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT referred_telegram_id, referrer_telegram_id, status, created_at, credited_at
		FROM referrals WHERE referred_telegram_id = ?
	`, referredTGID).Scan(&r.ReferredTelegramID, &r.ReferrerTelegramID, &r.Status, &created, &creditedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = parseTime(created)
	if creditedAt.Valid {
		if ts, e := parseTime(creditedAt.String); e == nil {
			r.CreditedAt = &ts
		}
	}
	return &r, nil
}

// CreditReferral transitions a pending referral to credited exactly once.
// Returns true only when the row was actually updated, so a concurrent or
// repeated payout (across the bot and mini app) credits the referrer once.
func (s *Store) CreditReferral(ctx context.Context, referredTGID int64, creditedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE referrals SET status = 'credited', credited_at = ?
		WHERE referred_telegram_id = ? AND status = 'pending'
	`, formatTime(creditedAt), referredTGID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ReferralStats returns how many users the given referrer has brought in
// (invited) and how many of those have already converted (credited).
func (s *Store) ReferralStats(ctx context.Context, referrerTGID int64) (invited, credited int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(CASE WHEN status = 'credited' THEN 1 END)
		FROM referrals WHERE referrer_telegram_id = ?
	`, referrerTGID).Scan(&invited, &credited)
	return invited, credited, err
}
