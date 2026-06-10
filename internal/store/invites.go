package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type InviteRequest struct {
	ID                int64
	InviterTelegramID int64
	InviterUsername   string
	NewUsername       string
	Months            int
	Price             int
	Status            string // "pending" | "approved" | "rejected"
	CreatedAt         time.Time
	ResolvedAt        *time.Time
}

func (s *Store) CreateInviteRequest(ctx context.Context, r InviteRequest) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO invite_requests
			(inviter_telegram_id, inviter_username, new_username, months, price, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.InviterTelegramID, r.InviterUsername, r.NewUsername, r.Months, r.Price, "pending", now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetInviteRequest(ctx context.Context, id int64) (*InviteRequest, error) {
	var (
		r          InviteRequest
		created    string
		resolvedAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, inviter_telegram_id, inviter_username, new_username, months, price,
			status, created_at, resolved_at
		FROM invite_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.InviterTelegramID, &r.InviterUsername, &r.NewUsername,
		&r.Months, &r.Price, &r.Status, &created, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = parseTime(created)
	if resolvedAt.Valid {
		if ts, e := parseTime(resolvedAt.String); e == nil {
			r.ResolvedAt = &ts
		}
	}
	return &r, nil
}

// ListInviteRequestsByInviter returns every invite request created by the
// given Telegram user, newest first.
func (s *Store) ListInviteRequestsByInviter(ctx context.Context, inviterTGID int64) ([]InviteRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, inviter_telegram_id, inviter_username, new_username, months, price,
			status, created_at, resolved_at
		FROM invite_requests WHERE inviter_telegram_id = ? ORDER BY id DESC
	`, inviterTGID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InviteRequest
	for rows.Next() {
		var (
			r          InviteRequest
			created    string
			resolvedAt sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.InviterTelegramID, &r.InviterUsername, &r.NewUsername,
			&r.Months, &r.Price, &r.Status, &created, &resolvedAt); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = parseTime(created)
		if resolvedAt.Valid {
			if ts, e := parseTime(resolvedAt.String); e == nil {
				r.ResolvedAt = &ts
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveInviteRequest transitions a pending request to approved or rejected exactly once.
// Returns true only when the row was actually updated.
func (s *Store) ResolveInviteRequest(ctx context.Context, id int64, status string, resolvedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invite_requests SET status = ?, resolved_at = ?
		WHERE id = ? AND status = 'pending'
	`, status, formatTime(resolvedAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
