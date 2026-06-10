package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type GiftCode struct {
	ID                 int64
	Code               string
	BuyerTelegramID    int64
	BuyerUsername      string
	Months             int
	Price              int
	Status             string // "pending" | "issued" | "redeemed" | "rejected" | "revoked"
	RedeemerTelegramID int64
	RedeemedUsername   string
	CreatedAt          time.Time
	IssuedAt           *time.Time
	ResolvedAt         *time.Time
}

func (s *Store) CreateGiftCode(ctx context.Context, g GiftCode) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO gift_codes
			(code, buyer_telegram_id, buyer_username, months, price, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, g.Code, g.BuyerTelegramID, g.BuyerUsername, g.Months, g.Price, "pending", now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetGiftCode(ctx context.Context, id int64) (*GiftCode, error) {
	return s.getGiftCode(ctx, `WHERE id = ?`, id)
}

func (s *Store) GetGiftCodeByCode(ctx context.Context, code string) (*GiftCode, error) {
	return s.getGiftCode(ctx, `WHERE code = ?`, code)
}

func (s *Store) getGiftCode(ctx context.Context, where string, arg any) (*GiftCode, error) {
	var (
		g          GiftCode
		created    string
		issuedAt   sql.NullString
		resolvedAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, code, buyer_telegram_id, buyer_username, months, price, status,
			redeemer_telegram_id, redeemed_username, created_at, issued_at, resolved_at
		FROM gift_codes `+where, arg,
	).Scan(&g.ID, &g.Code, &g.BuyerTelegramID, &g.BuyerUsername, &g.Months, &g.Price,
		&g.Status, &g.RedeemerTelegramID, &g.RedeemedUsername, &created, &issuedAt, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.CreatedAt, _ = parseTime(created)
	if issuedAt.Valid {
		if ts, e := parseTime(issuedAt.String); e == nil {
			g.IssuedAt = &ts
		}
	}
	if resolvedAt.Valid {
		if ts, e := parseTime(resolvedAt.String); e == nil {
			g.ResolvedAt = &ts
		}
	}
	return &g, nil
}

// IssueGiftCode transitions a pending gift to issued exactly once.
// Returns true only when the row was actually updated.
func (s *Store) IssueGiftCode(ctx context.Context, id int64, issuedAt time.Time) (bool, error) {
	return s.transitionGift(ctx, `
		UPDATE gift_codes SET status = 'issued', issued_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(issuedAt), id)
}

// RejectGiftCode transitions a pending gift to rejected exactly once.
func (s *Store) RejectGiftCode(ctx context.Context, id int64, resolvedAt time.Time) (bool, error) {
	return s.transitionGift(ctx, `
		UPDATE gift_codes SET status = 'rejected', resolved_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(resolvedAt), id)
}

// RevokeGiftCode transitions an issued gift to revoked exactly once.
func (s *Store) RevokeGiftCode(ctx context.Context, id int64, resolvedAt time.Time) (bool, error) {
	return s.transitionGift(ctx, `
		UPDATE gift_codes SET status = 'revoked', resolved_at = ?
		WHERE id = ? AND status = 'issued'
	`, formatTime(resolvedAt), id)
}

// RedeemGiftCode atomically claims an issued gift for the redeemer. Returns
// true only when this call performed the issued -> redeemed transition, which
// makes the code single-use even under concurrent redemption attempts.
func (s *Store) RedeemGiftCode(ctx context.Context, code string, redeemerTGID int64, username string, resolvedAt time.Time) (bool, error) {
	return s.transitionGift(ctx, `
		UPDATE gift_codes
		SET status = 'redeemed', redeemer_telegram_id = ?, redeemed_username = ?, resolved_at = ?
		WHERE code = ? AND status = 'issued'
	`, redeemerTGID, username, formatTime(resolvedAt), code)
}

// ReissueGiftCode rolls a redeemed gift back to issued, clearing the redeemer
// fields. Used when the panel call fails after the code was claimed, so the
// code stays usable.
func (s *Store) ReissueGiftCode(ctx context.Context, id int64) (bool, error) {
	return s.transitionGift(ctx, `
		UPDATE gift_codes
		SET status = 'issued', redeemer_telegram_id = 0, redeemed_username = '', resolved_at = NULL
		WHERE id = ? AND status = 'redeemed'
	`, id)
}

func (s *Store) transitionGift(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ListGiftCodesByStatus(ctx context.Context, status string) ([]GiftCode, error) {
	return s.listGiftCodes(ctx, `WHERE status = ? ORDER BY id`, status)
}

// ListGiftCodesByBuyer returns every gift code purchased by the given buyer,
// newest first.
func (s *Store) ListGiftCodesByBuyer(ctx context.Context, buyerTGID int64) ([]GiftCode, error) {
	return s.listGiftCodes(ctx, `WHERE buyer_telegram_id = ? ORDER BY id DESC`, buyerTGID)
}

// ListGiftCodesByBuyerStatus returns one page of the buyer's gift codes in the
// given status, newest first.
func (s *Store) ListGiftCodesByBuyerStatus(ctx context.Context, buyerTGID int64, status string, limit, offset int) ([]GiftCode, error) {
	return s.listGiftCodes(ctx,
		`WHERE buyer_telegram_id = ? AND status = ? ORDER BY id DESC LIMIT ? OFFSET ?`,
		buyerTGID, status, limit, offset)
}

// CountGiftCodesByBuyer returns the buyer's gift counts grouped by status.
// Statuses with no gifts are absent from the map.
func (s *Store) CountGiftCodesByBuyer(ctx context.Context, buyerTGID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM gift_codes
		WHERE buyer_telegram_id = ? GROUP BY status
	`, buyerTGID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

func (s *Store) listGiftCodes(ctx context.Context, where string, args ...any) ([]GiftCode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, buyer_telegram_id, buyer_username, months, price, status,
			redeemer_telegram_id, redeemed_username, created_at, issued_at, resolved_at
		FROM gift_codes `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GiftCode
	for rows.Next() {
		var (
			g          GiftCode
			created    string
			issuedAt   sql.NullString
			resolvedAt sql.NullString
		)
		if err := rows.Scan(&g.ID, &g.Code, &g.BuyerTelegramID, &g.BuyerUsername, &g.Months,
			&g.Price, &g.Status, &g.RedeemerTelegramID, &g.RedeemedUsername,
			&created, &issuedAt, &resolvedAt); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = parseTime(created)
		if issuedAt.Valid {
			if ts, e := parseTime(issuedAt.String); e == nil {
				g.IssuedAt = &ts
			}
		}
		if resolvedAt.Valid {
			if ts, e := parseTime(resolvedAt.String); e == nil {
				g.ResolvedAt = &ts
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
