package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type PaymentReportFilter struct {
	Since    time.Time
	Status   string
	Provider string
	Query    string
	Limit    int
	Offset   int
}

type PaymentDailyStat struct {
	Date      string
	Confirmed int
	Revenue   int
}

type PaymentProviderStat struct {
	Provider  string
	Confirmed int
	Rejected  int
	Pending   int
	Revenue   int
}

type PaymentAnalytics struct {
	Confirmed     int
	Rejected      int
	Pending       int
	Revenue       int // confirmed subscription payments only
	GiftRevenue   int // paid gifts (issued/redeemed), unfiltered view only
	InviteRevenue int // approved invites, unfiltered view only
	TotalRevenue  int // Revenue + GiftRevenue + InviteRevenue
	Daily         []PaymentDailyStat
	Providers     []PaymentProviderStat
}

type PaymentReport struct {
	Items     []PaymentRequest
	Total     int
	Analytics PaymentAnalytics
}

// ReadPaymentReport returns a filtered history page plus operational aggregates.
// Status and free-text search affect only history; provider and time range also
// bound analytics so the KPI cards stay comparable while browsing the ledger.
func (s *Store) ReadPaymentReport(ctx context.Context, f PaymentReportFilter) (*PaymentReport, error) {
	if f.Limit <= 0 {
		f.Limit = 25
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	report := &PaymentReport{Items: []PaymentRequest{}}
	where, args := paymentHistoryWhere(f)
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM payment_requests"+where, args...).Scan(&report.Total); err != nil {
		return nil, err
	}

	query := `SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
		expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username,
		screenshot_file_id, screenshot_is_document, provider, provider_txn_id
		FROM payment_requests` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanPaymentRequest(rows)
		if err != nil {
			return nil, err
		}
		report.Items = append(report.Items, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	analytics, err := s.readPaymentAnalytics(ctx, f.Since, f.Provider)
	if err != nil {
		return nil, err
	}
	report.Analytics = *analytics
	return report, nil
}

func paymentHistoryWhere(f PaymentReportFilter) (string, []any) {
	clauses := []string{"created_at >= ?"}
	args := []any{formatTime(f.Since)}
	if f.Status != "" && f.Status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.Provider != "" && f.Provider != "all" {
		clauses = append(clauses, "provider = ?")
		args = append(args, f.Provider)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		clauses = append(clauses, `(LOWER(username) LIKE ? OR LOWER(payer_username) LIKE ?
			OR CAST(telegram_id AS TEXT) LIKE ? OR CAST(payer_telegram_id AS TEXT) LIKE ?
			OR CAST(id AS TEXT) LIKE ? OR LOWER(provider_txn_id) LIKE ?)`)
		for range 6 {
			args = append(args, like)
		}
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *Store) readPaymentAnalytics(ctx context.Context, since time.Time, provider string) (*PaymentAnalytics, error) {
	out := &PaymentAnalytics{Daily: []PaymentDailyStat{}, Providers: []PaymentProviderStat{}}
	providerSQL, providerArgs := paymentProviderClause(provider)
	sinceText := formatTime(since)

	resolvedArgs := append([]any{sinceText}, providerArgs...)
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status = 'confirmed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'rejected' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'confirmed' THEN price ELSE 0 END), 0)
		FROM payment_requests WHERE confirmed_at >= ?`+providerSQL, resolvedArgs...).Scan(
		&out.Confirmed, &out.Rejected, &out.Revenue)
	if err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_requests WHERE status = 'pending'`+providerSQL, providerArgs...).Scan(&out.Pending); err != nil {
		return nil, err
	}

	// Gifts and invites have no payment provider, so their revenue only applies
	// to the unfiltered ("all") view. issued_at / resolved_at mirror the
	// confirmed_at window used for payments above, so the day-range selector
	// bounds all three sources consistently.
	if provider == "" || provider == "all" {
		if err := s.db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(price), 0) FROM gift_codes
			WHERE status IN ('issued', 'redeemed') AND issued_at >= ?
		`, sinceText).Scan(&out.GiftRevenue); err != nil {
			return nil, err
		}
		if err := s.db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(price), 0) FROM invite_requests
			WHERE status = 'approved' AND resolved_at >= ?
		`, sinceText).Scan(&out.InviteRevenue); err != nil {
			return nil, err
		}
	}
	out.TotalRevenue = out.Revenue + out.GiftRevenue + out.InviteRevenue

	dailyRows, err := s.db.QueryContext(ctx, `SELECT SUBSTR(confirmed_at, 1, 10), COUNT(*), COALESCE(SUM(price), 0)
		FROM payment_requests WHERE status = 'confirmed' AND confirmed_at >= ?`+providerSQL+`
		GROUP BY SUBSTR(confirmed_at, 1, 10) ORDER BY SUBSTR(confirmed_at, 1, 10)`, resolvedArgs...)
	if err != nil {
		return nil, err
	}
	for dailyRows.Next() {
		var d PaymentDailyStat
		if err := dailyRows.Scan(&d.Date, &d.Confirmed, &d.Revenue); err != nil {
			dailyRows.Close()
			return nil, err
		}
		out.Daily = append(out.Daily, d)
	}
	if err := dailyRows.Close(); err != nil {
		return nil, err
	}

	providerRows, err := s.db.QueryContext(ctx, `SELECT provider,
		COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'rejected' AND confirmed_at >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN price ELSE 0 END), 0)
		FROM payment_requests GROUP BY provider ORDER BY provider`, sinceText, sinceText, sinceText)
	if err != nil {
		return nil, err
	}
	defer providerRows.Close()
	for providerRows.Next() {
		var p PaymentProviderStat
		if err := providerRows.Scan(&p.Provider, &p.Confirmed, &p.Rejected, &p.Pending, &p.Revenue); err != nil {
			return nil, err
		}
		out.Providers = append(out.Providers, p)
	}
	return out, providerRows.Err()
}

func paymentProviderClause(provider string) (string, []any) {
	if provider == "" || provider == "all" {
		return "", nil
	}
	return " AND provider = ?", []any{provider}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPaymentRequest(scanner rowScanner) (*PaymentRequest, error) {
	var (
		r         PaymentRequest
		exp       string
		created   string
		confirmed sql.NullString
	)
	if err := scanner.Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID,
		&r.Months, &r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID,
		&r.PayerUsername, &r.ScreenshotFileID, &r.ScreenshotIsDocument, &r.Provider,
		&r.ProviderTxnID); err != nil {
		return nil, err
	}
	var err error
	if r.ExpireAt, err = parseTime(exp); err != nil {
		return nil, fmt.Errorf("parse payment expiry: %w", err)
	}
	if r.CreatedAt, err = parseTime(created); err != nil {
		return nil, fmt.Errorf("parse payment creation: %w", err)
	}
	if confirmed.Valid {
		resolved, err := parseTime(confirmed.String)
		if err != nil {
			return nil, fmt.Errorf("parse payment resolution: %w", err)
		}
		r.ConfirmedAt = &resolved
	}
	return &r, nil
}
