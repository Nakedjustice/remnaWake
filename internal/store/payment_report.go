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
	Kind     string // "" / "all" / "payment" / "gift" / "invite"
	Query    string
	Limit    int
	Offset   int
}

// PaymentHistoryItem is one row of the unified admin history: a subscription
// payment, a gift code, or an invite request normalized to a common shape.
// Kind selects which source the row came from; Username/TelegramID is the
// primary party (payment recipient / gift buyer / inviter) and
// CounterpartyName/CounterpartyTelegramID is the other party (payer / gift
// redeemer / invited username). Provider and Reference only apply to payments
// and gifts (gift Reference is the code); invites leave them empty.
type PaymentHistoryItem struct {
	Kind                   string
	ID                     int64
	Username               string
	TelegramID             int64
	CounterpartyName       string
	CounterpartyTelegramID int64
	Months                 int
	Price                  int
	Status                 string
	Provider               string
	Reference              string
	CreatedAt              time.Time
	ResolvedAt             *time.Time
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
	Items     []PaymentHistoryItem
	Total     int
	Analytics PaymentAnalytics
}

// paymentHistoryUnion normalizes the three transaction sources into one column
// set so they can be filtered, ordered and paginated together. Every branch
// emits the same columns in the same order; the outer WHERE in
// paymentHistoryWhere references only these aliases.
const paymentHistoryUnion = `
	SELECT 'payment' AS kind, id, username, telegram_id,
		payer_username AS counterparty_name, payer_telegram_id AS counterparty_telegram_id,
		months, price, status, provider, provider_txn_id AS reference,
		created_at, confirmed_at AS resolved_at
	FROM payment_requests
	UNION ALL
	SELECT 'gift' AS kind, id, buyer_username AS username, buyer_telegram_id AS telegram_id,
		redeemed_username AS counterparty_name, redeemer_telegram_id AS counterparty_telegram_id,
		months, price, status, '' AS provider, code AS reference,
		created_at, COALESCE(resolved_at, issued_at) AS resolved_at
	FROM gift_codes
	UNION ALL
	SELECT 'invite' AS kind, id, inviter_username AS username, inviter_telegram_id AS telegram_id,
		new_username AS counterparty_name, 0 AS counterparty_telegram_id,
		months, price, status, '' AS provider, '' AS reference,
		created_at, resolved_at
	FROM invite_requests`

// ReadPaymentReport returns a filtered history page plus operational aggregates.
// The history page now merges subscription payments, gift codes and invites;
// status, kind, provider and free-text search bound only that list. Analytics
// stay payments-only (plus the separate gift/invite revenue lines), bounded by
// provider and time range, so the KPI cards remain comparable while browsing.
func (s *Store) ReadPaymentReport(ctx context.Context, f PaymentReportFilter) (*PaymentReport, error) {
	if f.Limit <= 0 {
		f.Limit = 25
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	report := &PaymentReport{Items: []PaymentHistoryItem{}}
	where, args := paymentHistoryWhere(f)
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+paymentHistoryUnion+") AS h"+where, args...).Scan(&report.Total); err != nil {
		return nil, err
	}

	query := `SELECT kind, id, username, telegram_id, counterparty_name, counterparty_telegram_id,
		months, price, status, provider, reference, created_at, resolved_at
		FROM (` + paymentHistoryUnion + `) AS h` + where + ` ORDER BY created_at DESC, kind DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanPaymentHistoryItem(rows)
		if err != nil {
			return nil, err
		}
		report.Items = append(report.Items, *it)
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
	if f.Kind != "" && f.Kind != "all" {
		clauses = append(clauses, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Provider != "" && f.Provider != "all" {
		clauses = append(clauses, "provider = ?")
		args = append(args, f.Provider)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		clauses = append(clauses, `(LOWER(username) LIKE ? OR LOWER(counterparty_name) LIKE ?
			OR CAST(telegram_id AS TEXT) LIKE ? OR CAST(counterparty_telegram_id AS TEXT) LIKE ?
			OR CAST(id AS TEXT) LIKE ? OR LOWER(reference) LIKE ?)`)
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

func scanPaymentHistoryItem(scanner rowScanner) (*PaymentHistoryItem, error) {
	var (
		it       PaymentHistoryItem
		created  string
		resolved sql.NullString
	)
	if err := scanner.Scan(&it.Kind, &it.ID, &it.Username, &it.TelegramID,
		&it.CounterpartyName, &it.CounterpartyTelegramID, &it.Months, &it.Price,
		&it.Status, &it.Provider, &it.Reference, &created, &resolved); err != nil {
		return nil, err
	}
	var err error
	if it.CreatedAt, err = parseTime(created); err != nil {
		return nil, fmt.Errorf("parse history creation: %w", err)
	}
	if resolved.Valid && resolved.String != "" {
		ts, err := parseTime(resolved.String)
		if err != nil {
			return nil, fmt.Errorf("parse history resolution: %w", err)
		}
		it.ResolvedAt = &ts
	}
	return &it, nil
}
