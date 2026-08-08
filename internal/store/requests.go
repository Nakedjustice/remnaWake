package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PaymentRequest struct {
	ID              int64
	RemnawaveID     int64
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
	// Kind selects the product being bought: "subscription" (default) or
	// "traffic_extension".
	Kind                  string
	TrafficGB             int
	BaseTrafficLimitBytes int64
	ExtraTrafficBytes     int64
	ExtensionExpiresAt    *time.Time
	ExtensionRestoredAt   *time.Time
}

const (
	PaymentKindSubscription     = "subscription"
	PaymentKindTrafficExtension = "traffic_extension"
)

type nullTimeScanner struct {
	dst **time.Time
}

func newNullTimeScanner(dst **time.Time) *nullTimeScanner {
	return &nullTimeScanner{dst: dst}
}

func (s *nullTimeScanner) Scan(value any) error {
	var ns sql.NullString
	if err := ns.Scan(value); err != nil {
		return err
	}
	if !ns.Valid || ns.String == "" {
		*s.dst = nil
		return nil
	}
	ts, err := parseTime(ns.String)
	if err != nil {
		return fmt.Errorf("parse time: %w", err)
	}
	*s.dst = &ts
	return nil
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
	kind := r.Kind
	if kind == "" {
		kind = PaymentKindSubscription
	}
	var expiresAt, restoredAt any
	if r.ExtensionExpiresAt != nil {
		expiresAt = formatTime(*r.ExtensionExpiresAt)
	}
	if r.ExtensionRestoredAt != nil {
		restoredAt = formatTime(*r.ExtensionRestoredAt)
	}
	// The uuid column is a v2 leftover: Remnawave v3 removed the panel uuid, so
	// remnawave_user_id is the only panel identity now. The column is kept (and
	// written empty) because it is NOT NULL on every existing install and
	// dropping it would rule out rolling back to a v2 build.
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at,
			 status, created_at, payer_telegram_id, payer_username, screenshot_file_id,
			 screenshot_is_document, provider, provider_txn_id, plan, kind, traffic_gb,
			 base_traffic_limit_bytes, extra_traffic_bytes, extension_expires_at, extension_restored_at)
		VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", now, r.PayerTelegramID, r.PayerUsername, r.ScreenshotFileID,
		r.ScreenshotIsDocument, provider, r.ProviderTxnID, plan, kind, r.TrafficGB,
		r.BaseTrafficLimitBytes, r.ExtraTrafficBytes, expiresAt, restoredAt)
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
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan,
			kind, traffic_gb, base_traffic_limit_bytes, extra_traffic_bytes,
			extension_expires_at, extension_restored_at
		FROM payment_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.RemnawaveID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername,
		&r.ScreenshotFileID, &r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan,
		&r.Kind, &r.TrafficGB, &r.BaseTrafficLimitBytes, &r.ExtraTrafficBytes,
		newNullTimeScanner(&r.ExtensionExpiresAt), newNullTimeScanner(&r.ExtensionRestoredAt))
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
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan,
			kind, traffic_gb, base_traffic_limit_bytes, extra_traffic_bytes,
			extension_expires_at, extension_restored_at
		FROM payment_requests WHERE provider_txn_id = ?
	`, txnID).Scan(&r.ID, &r.RemnawaveID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername,
		&r.ScreenshotFileID, &r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan,
		&r.Kind, &r.TrafficGB, &r.BaseTrafficLimitBytes, &r.ExtraTrafficBytes,
		newNullTimeScanner(&r.ExtensionExpiresAt), newNullTimeScanner(&r.ExtensionRestoredAt))
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

// LatestConfirmedPaymentRequestByUser returns the most recent confirmed
// payment for a panel user. It is used as the local source of truth for the
// currently paid plan when prorating an upgrade.
func (s *Store) LatestConfirmedPaymentRequestByUser(ctx context.Context, remnawaveID int64) (*PaymentRequest, error) {
	if remnawaveID == 0 {
		return nil, nil
	}
	var (
		r         PaymentRequest
		exp       string
		created   string
		confirmed sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan
		FROM payment_requests
		WHERE remnawave_user_id = ? AND status = 'confirmed'
		ORDER BY confirmed_at DESC, id DESC
		LIMIT 1
	`, remnawaveID).Scan(&r.ID, &r.RemnawaveID, &r.Username, &r.TelegramID, &r.Months,
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
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan,
			kind, traffic_gb, base_traffic_limit_bytes, extra_traffic_bytes,
			extension_expires_at, extension_restored_at
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
		if err := rows.Scan(&r.ID, &r.RemnawaveID, &r.Username, &r.TelegramID,
			&r.Months, &r.Price, &exp, &r.Status, &created, &confirmed,
			&r.PayerTelegramID, &r.PayerUsername, &r.ScreenshotFileID,
			&r.ScreenshotIsDocument, &r.Provider, &r.ProviderTxnID, &r.Plan,
			&r.Kind, &r.TrafficGB, &r.BaseTrafficLimitBytes, &r.ExtraTrafficBytes,
			newNullTimeScanner(&r.ExtensionExpiresAt), newNullTimeScanner(&r.ExtensionRestoredAt)); err != nil {
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
