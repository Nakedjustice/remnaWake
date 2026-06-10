package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PaymentRequest struct {
	ID              int64
	RemnawaveID     int64
	UUID            string
	Username        string
	TelegramID      int64
	Months          int
	Price           int
	ExpireAt        time.Time
	Status          string // "pending" | "confirmed" | "rejected"
	CreatedAt       time.Time
	ConfirmedAt     *time.Time
	PayerTelegramID int64
	PayerUsername   string
}

func (s *Store) CreatePaymentRequest(ctx context.Context, r PaymentRequest) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at,
			 status, created_at, payer_telegram_id, payer_username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.UUID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", now, r.PayerTelegramID, r.PayerUsername)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetPaymentRequest(ctx context.Context, id int64) (*PaymentRequest, error) {
	var (
		r            PaymentRequest
		exp, created string
		confirmed    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username
		FROM payment_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ExpireAt, _ = parseTime(exp)
	r.CreatedAt, _ = parseTime(created)
	if confirmed.Valid {
		if ts, e := parseTime(confirmed.String); e == nil {
			r.ConfirmedAt = &ts
		}
	}
	return &r, nil
}

// ListPaymentRequestsByStatus returns all requests with the given status in
// creation order.
func (s *Store) ListPaymentRequestsByStatus(ctx context.Context, status string) ([]PaymentRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username
		FROM payment_requests WHERE status = ? ORDER BY id
	`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PaymentRequest
	for rows.Next() {
		var (
			r            PaymentRequest
			exp, created string
			confirmed    sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID,
			&r.Months, &r.Price, &exp, &r.Status, &created, &confirmed,
			&r.PayerTelegramID, &r.PayerUsername); err != nil {
			return nil, err
		}
		r.ExpireAt, _ = parseTime(exp)
		r.CreatedAt, _ = parseTime(created)
		if confirmed.Valid {
			if ts, e := parseTime(confirmed.String); e == nil {
				r.ConfirmedAt = &ts
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ConfirmPaymentRequest transitions pending->confirmed exactly once.
// Returns true only on the transition; false if already confirmed or not found.
func (s *Store) ConfirmPaymentRequest(ctx context.Context, id int64, confirmedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests SET status = 'confirmed', confirmed_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(confirmedAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RejectPaymentRequest transitions pending->rejected exactly once, storing the
// resolution time in confirmed_at (it doubles as resolved-at).
// Returns true only on the transition; false if already resolved or not found.
func (s *Store) RejectPaymentRequest(ctx context.Context, id int64, rejectedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests SET status = 'rejected', confirmed_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(rejectedAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
