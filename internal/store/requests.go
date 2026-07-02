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
	// ScreenshotFileID is the Telegram file_id of the payment screenshot the
	// user attached; empty when the screenshot requirement was off.
	ScreenshotFileID string
	// ScreenshotIsDocument records whether the receipt arrived as a document
	// (PDF or uncompressed image) rather than a photo — Telegram file_ids are
	// only valid with their own send method, so any later re-send needs this.
	ScreenshotIsDocument bool
	// Provider names the payment provider that owns this request: "p2p"
	// (manual admin confirmation, the default) or "platega" (automatic).
	Provider string
	// ProviderTxnID is the external gateway transaction id (Platega), used to
	// correlate webhook callbacks with this request; empty for p2p.
	ProviderTxnID string
	// Plan is the tariff preset code the buyer picked; its panel settings are
	// stamped onto the user when the request is confirmed.
	Plan string
}

func (s *Store) CreatePaymentRequest(ctx context.Context, r PaymentRequest) (int64, error) {
	now := formatTime(time.Now())
	provider := r.Provider
	if provider == "" {
		provider = "p2p"
	}
	plan := r.Plan
	if plan == "" {
		plan = PlanStandard
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at,
			 status, created_at, payer_telegram_id, payer_username, screenshot_file_id,
			 screenshot_is_document, provider, provider_txn_id, plan)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.UUID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", now, r.PayerTelegramID, r.PayerUsername, r.ScreenshotFileID,
		r.ScreenshotIsDocument, provider, r.ProviderTxnID, plan)
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
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan
		FROM payment_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername,
		&r.ScreenshotFileID, &r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan)
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

// GetPaymentRequestByProviderTxn returns the request carrying the given external
// gateway transaction id, or nil if none matches. Used by the Platega webhook to
// correlate a callback (which carries only a transaction id) with its request.
func (s *Store) GetPaymentRequestByProviderTxn(ctx context.Context, txnID string) (*PaymentRequest, error) {
	if txnID == "" {
		return nil, nil
	}
	var (
		r            PaymentRequest
		exp, created string
		confirmed    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan
		FROM payment_requests WHERE provider_txn_id = ?
	`, txnID).Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername,
		&r.ScreenshotFileID, &r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan)
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

// SetPaymentRequestProviderTxn records the external gateway transaction id on a
// request right after the gateway transaction is created.
func (s *Store) SetPaymentRequestProviderTxn(ctx context.Context, id int64, txnID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests SET provider_txn_id = ? WHERE id = ?
	`, txnID, id)
	return err
}

func (s *Store) SetPaymentRequestScreenshot(ctx context.Context, id int64, fileID string, asDocument bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE payment_requests SET screenshot_file_id = ?, screenshot_is_document = ? WHERE id = ? AND status = 'pending'`, fileID, asDocument, id)
	return err
}

// ListPaymentRequestsByStatus returns all requests with the given status in
// creation order.
func (s *Store) ListPaymentRequestsByStatus(ctx context.Context, status string) ([]PaymentRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan
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
			&r.PayerTelegramID, &r.PayerUsername, &r.ScreenshotFileID,
			&r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan); err != nil {
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

// DeletePendingPaymentRequest removes a request nobody could act on (e.g.
// every admin notification failed right after creation). Only pending rows
// are deleted, so a concurrently confirmed/rejected request is left intact.
func (s *Store) DeletePendingPaymentRequest(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM payment_requests WHERE id = ? AND status = 'pending'
	`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeletePaymentRequest permanently removes a payment request of any status by
// id. Unlike DeletePendingPaymentRequest this is unconditional, so admins can
// purge test/junk records that distort reporting. Returns true if a row was
// deleted, false if no request had that id.
func (s *Store) DeletePaymentRequest(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM payment_requests WHERE id = ?`, id)
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
