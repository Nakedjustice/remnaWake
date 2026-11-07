package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TrialRequest is a free-trial activation awaiting admin approval. It exists
// only when the trial requires approval and the requester arrived without a
// referral; the one-time trial_claims row is reserved at request time so a
// rejection is final and a re-request collapses to "already used".
type TrialRequest struct {
	ID         int64
	TelegramID int64
	Username   string
	Status     string // "pending" | "approved" | "rejected"
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

func (s *Store) CreateTrialRequest(ctx context.Context, telegramID int64, username string, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO trial_requests (telegram_id, username, status, created_at)
		VALUES (?, ?, 'pending', ?)
	`, telegramID, username, formatTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// trialRequestColumns is the trial_requests column list in scanTrialRequest order.
const trialRequestColumns = `id, telegram_id, username, status, created_at, resolved_at`

// scanTrialRequest reads one row in trialRequestColumns order.
func scanTrialRequest(sc rowScanner) (TrialRequest, error) {
	var (
		r          TrialRequest
		created    string
		resolvedAt sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.TelegramID, &r.Username, &r.Status, &created, &resolvedAt); err != nil {
		return TrialRequest{}, err
	}
	r.CreatedAt, _ = parseTime(created)
	if resolvedAt.Valid {
		if ts, e := parseTime(resolvedAt.String); e == nil {
			r.ResolvedAt = &ts
		}
	}
	return r, nil
}

func (s *Store) getTrialRequest(row *sql.Row) (*TrialRequest, error) {
	r, err := scanTrialRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetTrialRequest(ctx context.Context, id int64) (*TrialRequest, error) {
	return s.getTrialRequest(s.db.QueryRowContext(ctx,
		`SELECT `+trialRequestColumns+` FROM trial_requests WHERE id = ?`, id))
}

// GetPendingTrialRequestByTelegramID returns the user's pending trial request,
// or nil when none is awaiting a decision. Used to give a friendly "already
// under review" message on a duplicate submit.
func (s *Store) GetPendingTrialRequestByTelegramID(ctx context.Context, telegramID int64) (*TrialRequest, error) {
	return s.getTrialRequest(s.db.QueryRowContext(ctx, `
		SELECT `+trialRequestColumns+`
		FROM trial_requests WHERE telegram_id = ? AND status = 'pending'
		ORDER BY id DESC LIMIT 1
	`, telegramID))
}

// ListTrialRequestsByStatus returns every trial request in the given status,
// newest first.
func (s *Store) ListTrialRequestsByStatus(ctx context.Context, status string) ([]TrialRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+trialRequestColumns+` FROM trial_requests WHERE status = ? ORDER BY id DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanList(rows, scanTrialRequest)
}

// ResolveTrialRequest transitions a pending request to approved or rejected
// exactly once. Returns true only when the row was actually updated.
func (s *Store) ResolveTrialRequest(ctx context.Context, id int64, status string, resolvedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE trial_requests SET status = ?, resolved_at = ?
		WHERE id = ? AND status = 'pending'
	`, status, formatTime(resolvedAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RejectInvalidTrialRequest rejects a legacy pending request and releases its
// one-time trial claim in the same transaction. It is intentionally separate
// from ordinary rejection, which keeps the claim and is final.
//
// The claim is matched on username as well as telegram_id: the row released must
// be the one this request reserved. A claim recorded under a different name
// belongs to a trial the user has already been granted, and dropping it would
// hand out a second free trial.
func (s *Store) RejectInvalidTrialRequest(ctx context.Context, id, telegramID int64, username string, resolvedAt time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE trial_requests SET status = 'rejected', resolved_at = ?
		WHERE id = ? AND telegram_id = ? AND status = 'pending'
	`, formatTime(resolvedAt), id, telegramID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM trial_claims WHERE telegram_id = ? AND username = ?`, telegramID, username); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
