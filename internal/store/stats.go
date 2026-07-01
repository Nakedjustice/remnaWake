package store

import (
	"context"
	"time"
)

// AdminStats aggregates the store-side counters shown by the /stats admin
// command; panel-side user counts come from the Remnawave API instead.
type AdminStats struct {
	PaymentsPending   int
	PaymentsConfirmed int // confirmed since the cutoff passed to ReadAdminStats
	Revenue           int // sum of confirmed request prices since the cutoff
	GiftsRevenue      int // sum of issued/redeemed gift prices since the cutoff
	InvitesRevenue    int // sum of approved invite prices since the cutoff
	GiftsByStatus     map[string]int
	InvitesPending    int
}

// ReadAdminStats collects counters for the admin stats report. since bounds
// the confirmed-payments window (e.g. now-30d).
func (s *Store) ReadAdminStats(ctx context.Context, since time.Time) (*AdminStats, error) {
	st := &AdminStats{GiftsByStatus: map[string]int{}}

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM payment_requests WHERE status = 'pending'`,
	).Scan(&st.PaymentsPending)
	if err != nil {
		return nil, err
	}

	// confirmed_at is RFC3339 UTC text, so string comparison is chronological.
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(price), 0) FROM payment_requests
		WHERE status = 'confirmed' AND confirmed_at >= ?
	`, formatTime(since)).Scan(&st.PaymentsConfirmed, &st.Revenue)
	if err != nil {
		return nil, err
	}

	// Paid gifts (issued or redeemed) are revenue too, counted from issued_at so
	// they share the confirmed-payments window above.
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(price), 0) FROM gift_codes
		WHERE status IN ('issued', 'redeemed') AND issued_at >= ?
	`, formatTime(since)).Scan(&st.GiftsRevenue)
	if err != nil {
		return nil, err
	}

	// Approved invites are revenue too, counted from resolved_at (approval time).
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(price), 0) FROM invite_requests
		WHERE status = 'approved' AND resolved_at >= ?
	`, formatTime(since)).Scan(&st.InvitesRevenue)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM gift_codes GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		st.GiftsByStatus[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM invite_requests WHERE status = 'pending'`,
	).Scan(&st.InvitesPending)
	if err != nil {
		return nil, err
	}
	return st, nil
}
