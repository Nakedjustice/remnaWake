package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RegistrationGuard records the first bot registration seen for a Telegram
// user. A record is immutable apart from its block state so a false positive
// that an admin unblocks is never automatically blocked again.
type RegistrationGuard struct {
	TelegramID     int64
	Username       string
	FirstName      string
	LastName       string
	MatchedPattern string
	Blocked        bool
	CreatedAt      time.Time
}

// RecordRegistration atomically records a first registration. It returns
// created=false for later starts and reports the currently stored block state.
func (s *Store) RecordRegistration(ctx context.Context, guard RegistrationGuard) (created, blocked bool, err error) {
	if guard.TelegramID == 0 {
		return false, false, fmt.Errorf("registration guard: telegram id is required")
	}
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO registration_guards
			(telegram_id, username, first_name, last_name, matched_pattern, blocked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(telegram_id) DO NOTHING
	`, guard.TelegramID, guard.Username, guard.FirstName, guard.LastName, guard.MatchedPattern,
		boolToInt(guard.Blocked), now, now)
	if err != nil {
		return false, false, fmt.Errorf("record registration guard: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, false, fmt.Errorf("record registration guard rows: %w", err)
	}
	if n > 0 {
		return true, guard.Blocked, nil
	}
	blocked, err = s.RegistrationBlocked(ctx, guard.TelegramID)
	return false, blocked, err
}

// RegistrationBlocked reports whether the Telegram user has an active
// registration block. Unknown users are not blocked.
func (s *Store) RegistrationBlocked(ctx context.Context, telegramID int64) (bool, error) {
	var blocked int
	err := s.db.QueryRowContext(ctx, `SELECT blocked FROM registration_guards WHERE telegram_id = ?`, telegramID).Scan(&blocked)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get registration guard: %w", err)
	}
	return blocked != 0, nil
}

// ListBlockedRegistrationGuards returns the currently active automatic blocks,
// newest first. Resolved guards remain stored for audit purposes but are not
// included in the admin review queue.
func (s *Store) ListBlockedRegistrationGuards(ctx context.Context) ([]RegistrationGuard, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT telegram_id, username, first_name, last_name, matched_pattern, blocked, created_at
		FROM registration_guards
		WHERE blocked = 1
		ORDER BY created_at DESC, telegram_id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list blocked registration guards: %w", err)
	}
	defer rows.Close()
	var guards []RegistrationGuard
	for rows.Next() {
		var (
			guard   RegistrationGuard
			blocked int
			created string
		)
		if err := rows.Scan(&guard.TelegramID, &guard.Username, &guard.FirstName, &guard.LastName,
			&guard.MatchedPattern, &blocked, &created); err != nil {
			return nil, fmt.Errorf("scan blocked registration guard: %w", err)
		}
		guard.Blocked = blocked != 0
		guard.CreatedAt, _ = parseTime(created)
		guards = append(guards, guard)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blocked registration guards: %w", err)
	}
	return guards, nil
}

// UnblockRegistration conditionally removes an automatic registration block.
// changed=false means the user was already unblocked or unknown.
func (s *Store) UnblockRegistration(ctx context.Context, telegramID int64) (changed bool, err error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		UPDATE registration_guards
		SET blocked = 0, updated_at = ?, unblocked_at = ?
		WHERE telegram_id = ? AND blocked = 1
	`, now, now, telegramID)
	if err != nil {
		return false, fmt.Errorf("unblock registration guard: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("unblock registration guard rows: %w", err)
	}
	return n > 0, nil
}
