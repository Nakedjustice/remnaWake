package store

import (
	"context"
	"time"
)

// OperatorAnalytics is a local-only admin report derived from SQLite workflow
// history. It intentionally does not snapshot or replace Remnawave user state.
type OperatorAnalytics struct {
	Revenue           RevenueAnalytics
	Renewals          RenewalAnalytics
	ChurnWinback      ChurnWinbackAnalytics
	Referrals         ReferralConversionAnalytics
	Gifts             GiftAnalytics
	TrafficExtensions TrafficExtensionAnalytics
	ProviderMix       []ProviderMixStat
}

type RevenueAnalytics struct {
	SubscriptionPayments     int
	SubscriptionRevenue      int
	TrafficExtensionPayments int
	TrafficExtensionRevenue  int
	GiftPurchases            int
	GiftRevenue              int
	InvitePurchases          int
	InviteRevenue            int
	TotalRevenue             int
}

type RenewalAnalytics struct {
	Payments       int
	UniqueProfiles int
	RepeatProfiles int
	MonthsAdded    int
}

type ChurnWinbackAnalytics struct {
	LocalLapsedUsers int
	WinbackSent      int
	WinbackConverted int
}

type ReferralConversionAnalytics struct {
	Created  int
	Credited int
	Pending  int
}

type GiftAnalytics struct {
	Created  int
	Pending  int
	Issued   int
	Redeemed int
	Rejected int
	Revoked  int
	Revenue  int
}

type TrafficExtensionAnalytics struct {
	Confirmed int
	Pending   int
	Active    int
	Restored  int
	TrafficGB int
	Revenue   int
}

type ProviderMixStat struct {
	Provider  string
	Confirmed int
	Rejected  int
	Pending   int
	Revenue   int
}

func (s *Store) ReadOperatorAnalytics(ctx context.Context, since, now time.Time) (*OperatorAnalytics, error) {
	if now.IsZero() {
		now = time.Now()
	}
	out := &OperatorAnalytics{ProviderMix: []ProviderMixStat{}}
	sinceText := formatTime(since)
	nowText := formatTime(now)

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = ? THEN price ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = ? THEN price ELSE 0 END), 0)
		FROM payment_requests
		WHERE status = 'confirmed' AND confirmed_at >= ?
	`, PaymentKindSubscription, PaymentKindSubscription, PaymentKindTrafficExtension, PaymentKindTrafficExtension, sinceText).Scan(
		&out.Revenue.SubscriptionPayments,
		&out.Revenue.SubscriptionRevenue,
		&out.Revenue.TrafficExtensionPayments,
		&out.Revenue.TrafficExtensionRevenue,
	); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(price), 0)
		FROM gift_codes
		WHERE status IN ('issued', 'redeemed') AND issued_at >= ?
	`, sinceText).Scan(&out.Revenue.GiftPurchases, &out.Revenue.GiftRevenue); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(price), 0)
		FROM invite_requests
		WHERE status = 'approved' AND resolved_at >= ?
	`, sinceText).Scan(&out.Revenue.InvitePurchases, &out.Revenue.InviteRevenue); err != nil {
		return nil, err
	}
	out.Revenue.TotalRevenue = out.Revenue.SubscriptionRevenue + out.Revenue.TrafficExtensionRevenue + out.Revenue.GiftRevenue + out.Revenue.InviteRevenue

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT remnawave_user_id), COALESCE(SUM(months), 0)
		FROM payment_requests
		WHERE kind = ? AND status = 'confirmed' AND confirmed_at >= ?
	`, PaymentKindSubscription, sinceText).Scan(&out.Renewals.Payments, &out.Renewals.UniqueProfiles, &out.Renewals.MonthsAdded); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT p.remnawave_user_id
			FROM payment_requests p
			WHERE p.kind = ? AND p.status = 'confirmed' AND p.confirmed_at >= ?
				AND EXISTS (
					SELECT 1 FROM payment_requests prev
					WHERE prev.remnawave_user_id = p.remnawave_user_id
						AND prev.kind = ? AND prev.status = 'confirmed'
						AND prev.confirmed_at < ?
				)
			GROUP BY p.remnawave_user_id
		)
	`, PaymentKindSubscription, sinceText, PaymentKindSubscription, sinceText).Scan(&out.Renewals.RepeatProfiles); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sent_notifications
		WHERE kind = ? AND sent_at >= ?
	`, NotificationWinback, sinceText).Scan(&out.ChurnWinback.WinbackSent); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT sn.remnawave_user_id)
		FROM sent_notifications sn
		WHERE sn.kind = ? AND sn.sent_at >= ?
			AND EXISTS (
				SELECT 1 FROM payment_requests pr
				WHERE pr.remnawave_user_id = sn.remnawave_user_id
					AND pr.kind = ? AND pr.status = 'confirmed'
					AND pr.confirmed_at > sn.sent_at
			)
	`, NotificationWinback, sinceText, PaymentKindSubscription).Scan(&out.ChurnWinback.WinbackConverted); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM notified_users nu
		WHERE nu.expire_at < ?
			AND NOT EXISTS (
				SELECT 1 FROM payment_requests pr
				WHERE pr.remnawave_user_id = nu.remnawave_user_id
					AND pr.kind = ? AND pr.status = 'confirmed'
					AND pr.confirmed_at > nu.expire_at
			)
	`, nowText, PaymentKindSubscription).Scan(&out.ChurnWinback.LocalLapsedUsers); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'credited' THEN 1 ELSE 0 END), 0)
		FROM referrals
		WHERE created_at >= ?
	`, sinceText).Scan(&out.Referrals.Created, &out.Referrals.Credited); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM referrals WHERE status = 'pending'`).Scan(&out.Referrals.Pending); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'issued' AND issued_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'redeemed' AND resolved_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'rejected' AND resolved_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'revoked' AND resolved_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status IN ('issued', 'redeemed') AND issued_at >= ? THEN price ELSE 0 END), 0)
		FROM gift_codes
	`, sinceText, sinceText, sinceText, sinceText, sinceText, sinceText).Scan(
		&out.Gifts.Created,
		&out.Gifts.Pending,
		&out.Gifts.Issued,
		&out.Gifts.Redeemed,
		&out.Gifts.Rejected,
		&out.Gifts.Revoked,
		&out.Gifts.Revenue,
	); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN price ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN traffic_gb ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND extension_restored_at IS NULL AND extension_expires_at > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND extension_restored_at >= ? THEN 1 ELSE 0 END), 0)
		FROM payment_requests
		WHERE kind = ?
	`, sinceText, sinceText, sinceText, nowText, sinceText, PaymentKindTrafficExtension).Scan(
		&out.TrafficExtensions.Confirmed,
		&out.TrafficExtensions.Revenue,
		&out.TrafficExtensions.TrafficGB,
		&out.TrafficExtensions.Pending,
		&out.TrafficExtensions.Active,
		&out.TrafficExtensions.Restored,
	); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider,
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'rejected' AND confirmed_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' AND confirmed_at >= ? THEN price ELSE 0 END), 0)
		FROM payment_requests
		GROUP BY provider
		ORDER BY provider
	`, sinceText, sinceText, sinceText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ProviderMixStat
		if err := rows.Scan(&p.Provider, &p.Confirmed, &p.Rejected, &p.Pending, &p.Revenue); err != nil {
			return nil, err
		}
		out.ProviderMix = append(out.ProviderMix, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
