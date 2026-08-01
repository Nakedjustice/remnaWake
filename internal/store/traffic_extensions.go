package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type TrafficExtensionOption struct {
	TrafficGB int
	Price     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) UpsertTrafficExtensionOption(ctx context.Context, trafficGB, price int) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO traffic_extension_options (traffic_gb, price, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(traffic_gb) DO UPDATE SET price = excluded.price, updated_at = excluded.updated_at
	`, trafficGB, price, now, now)
	return err
}

func (s *Store) ListTrafficExtensionOptions(ctx context.Context) ([]TrafficExtensionOption, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT traffic_gb, price, created_at, updated_at
		FROM traffic_extension_options ORDER BY traffic_gb ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficExtensionOption
	for rows.Next() {
		var (
			o            TrafficExtensionOption
			created, upd string
		)
		if err := rows.Scan(&o.TrafficGB, &o.Price, &created, &upd); err != nil {
			return nil, err
		}
		o.CreatedAt, _ = parseTime(created)
		o.UpdatedAt, _ = parseTime(upd)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) GetTrafficExtensionOption(ctx context.Context, trafficGB int) (*TrafficExtensionOption, error) {
	var (
		o            TrafficExtensionOption
		created, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT traffic_gb, price, created_at, updated_at
		FROM traffic_extension_options WHERE traffic_gb = ?
	`, trafficGB).Scan(&o.TrafficGB, &o.Price, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.CreatedAt, _ = parseTime(created)
	o.UpdatedAt, _ = parseTime(upd)
	return &o, nil
}

func (s *Store) DeleteTrafficExtensionOption(ctx context.Context, trafficGB int) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM traffic_extension_options WHERE traffic_gb = ?`, trafficGB)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) CreateTrafficExtensionPaymentRequest(ctx context.Context, r PaymentRequest, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM payment_requests
		WHERE remnawave_user_id = ? AND kind = ? AND (
			status = 'pending'
			OR (status = 'confirmed' AND extension_restored_at IS NULL AND extension_expires_at > ?)
		)
	`, r.RemnawaveID, PaymentKindTrafficExtension, formatTime(now)).Scan(&active); err != nil {
		return 0, err
	}
	if active > 0 {
		return 0, ErrTrafficExtensionActive
	}
	provider := r.Provider
	if provider == "" {
		provider = "p2p"
	}
	plan := r.Plan
	if plan == "" {
		plan = PlanStandard
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at,
			 status, created_at, payer_telegram_id, payer_username, screenshot_file_id,
			 screenshot_is_document, provider, provider_txn_id, plan, kind, traffic_gb,
			 base_traffic_limit_bytes, extra_traffic_bytes)
		VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", formatTime(now), r.PayerTelegramID, r.PayerUsername,
		r.ScreenshotFileID, r.ScreenshotIsDocument, provider, r.ProviderTxnID, plan,
		PaymentKindTrafficExtension, r.TrafficGB, r.BaseTrafficLimitBytes, r.ExtraTrafficBytes)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

var ErrTrafficExtensionActive = errors.New("traffic extension already active")

func (s *Store) ConfirmTrafficExtensionRequest(ctx context.Context, id int64, confirmedAt, expiresAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests
		SET status = 'confirmed', confirmed_at = ?, extension_expires_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(confirmedAt), formatTime(expiresAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ActiveTrafficExtensionForUser(ctx context.Context, remnawaveID int64, now time.Time) (*PaymentRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan,
			kind, traffic_gb, base_traffic_limit_bytes, extra_traffic_bytes,
			extension_expires_at, extension_restored_at
		FROM payment_requests
		WHERE remnawave_user_id = ? AND kind = ? AND status = 'confirmed'
			AND extension_restored_at IS NULL AND extension_expires_at > ?
		ORDER BY confirmed_at DESC, id DESC LIMIT 1
	`, remnawaveID, PaymentKindTrafficExtension, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanPaymentRequestRow(rows)
}

func (s *Store) UpdateActiveTrafficExtensionBase(ctx context.Context, remnawaveID int64, now time.Time, baseBytes int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests
		SET base_traffic_limit_bytes = ?
		WHERE remnawave_user_id = ? AND kind = ? AND status = 'confirmed'
			AND extension_restored_at IS NULL AND extension_expires_at > ?
	`, baseBytes, remnawaveID, PaymentKindTrafficExtension, formatTime(now))
	return err
}

func (s *Store) MarkActiveTrafficExtensionsRestored(ctx context.Context, remnawaveID int64, restoredAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests
		SET extension_restored_at = ?
		WHERE remnawave_user_id = ? AND kind = ? AND status = 'confirmed' AND extension_restored_at IS NULL
	`, formatTime(restoredAt), remnawaveID, PaymentKindTrafficExtension)
	return err
}

func (s *Store) ListExpiredTrafficExtensions(ctx context.Context, now time.Time) ([]PaymentRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remnawave_user_id, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
			screenshot_file_id, screenshot_is_document, provider, provider_txn_id, plan,
			kind, traffic_gb, base_traffic_limit_bytes, extra_traffic_bytes,
			extension_expires_at, extension_restored_at
		FROM payment_requests
		WHERE kind = ? AND status = 'confirmed' AND extension_restored_at IS NULL
			AND extension_expires_at <= ?
		ORDER BY extension_expires_at ASC, id ASC
	`, PaymentKindTrafficExtension, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PaymentRequest
	for rows.Next() {
		r, err := scanPaymentRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func scanPaymentRequestRow(rows *sql.Rows) (*PaymentRequest, error) {
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
	return &r, nil
}
