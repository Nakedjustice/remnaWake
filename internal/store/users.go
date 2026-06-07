package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type NotifiedUser struct {
	RemnawaveID int64
	UUID        string
	Username    string
	TelegramID  int64
	ExpireAt    time.Time
	UpdatedAt   time.Time
}

func (s *Store) UpsertNotifiedUser(ctx context.Context, u NotifiedUser) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notified_users (remnawave_user_id, uuid, username, telegram_id, expire_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(remnawave_user_id) DO UPDATE SET
			uuid = excluded.uuid,
			username = excluded.username,
			telegram_id = excluded.telegram_id,
			expire_at = excluded.expire_at,
			updated_at = excluded.updated_at
	`, u.RemnawaveID, u.UUID, u.Username, u.TelegramID, formatTime(u.ExpireAt), now)
	return err
}

func (s *Store) GetNotifiedUser(ctx context.Context, remnawaveID int64) (*NotifiedUser, error) {
	var (
		u        NotifiedUser
		exp, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT remnawave_user_id, uuid, username, telegram_id, expire_at, updated_at
		FROM notified_users WHERE remnawave_user_id = ?
	`, remnawaveID).Scan(&u.RemnawaveID, &u.UUID, &u.Username, &u.TelegramID, &exp, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.ExpireAt, _ = parseTime(exp)
	u.UpdatedAt, _ = parseTime(upd)
	return &u, nil
}
