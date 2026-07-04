package payments

import (
	"context"
	"fmt"
	"math"
	"time"
)

type WebOperatorAnalytics struct {
	RangeDays         int                          `json:"range_days"`
	GeneratedAt       string                       `json:"generated_at"`
	Revenue           WebRevenueAnalytics          `json:"revenue"`
	Renewals          WebRenewalAnalytics          `json:"renewals"`
	ChurnWinback      WebChurnWinbackAnalytics     `json:"churn_winback"`
	Referrals         WebReferralConversion        `json:"referrals"`
	Gifts             WebGiftAnalytics             `json:"gifts"`
	TrafficExtensions WebTrafficExtensionAnalytics `json:"traffic_extensions"`
	ProviderMix       []WebOperatorPaymentProvider `json:"provider_mix"`
}

type WebRevenueAnalytics struct {
	SubscriptionPayments         int    `json:"subscription_payments"`
	SubscriptionRevenue          int    `json:"subscription_revenue"`
	SubscriptionRevenueLabel     string `json:"subscription_revenue_label"`
	TrafficExtensionPayments     int    `json:"traffic_extension_payments"`
	TrafficExtensionRevenue      int    `json:"traffic_extension_revenue"`
	TrafficExtensionRevenueLabel string `json:"traffic_extension_revenue_label"`
	GiftPurchases                int    `json:"gift_purchases"`
	GiftRevenue                  int    `json:"gift_revenue"`
	GiftRevenueLabel             string `json:"gift_revenue_label"`
	InvitePurchases              int    `json:"invite_purchases"`
	InviteRevenue                int    `json:"invite_revenue"`
	InviteRevenueLabel           string `json:"invite_revenue_label"`
	TotalRevenue                 int    `json:"total_revenue"`
	TotalRevenueLabel            string `json:"total_revenue_label"`
}

type WebRenewalAnalytics struct {
	Payments       int     `json:"payments"`
	UniqueProfiles int     `json:"unique_profiles"`
	RepeatProfiles int     `json:"repeat_profiles"`
	MonthsAdded    int     `json:"months_added"`
	AverageMonths  float64 `json:"average_months"`
}

type WebChurnWinbackAnalytics struct {
	LocalLapsedUsers      int     `json:"local_lapsed_users"`
	WinbackSent           int     `json:"winback_sent"`
	WinbackConverted      int     `json:"winback_converted"`
	WinbackConversionRate float64 `json:"winback_conversion_rate"`
}

type WebReferralConversion struct {
	Created        int     `json:"created"`
	Credited       int     `json:"credited"`
	Pending        int     `json:"pending"`
	ConversionRate float64 `json:"conversion_rate"`
}

type WebGiftAnalytics struct {
	Created      int    `json:"created"`
	Pending      int    `json:"pending"`
	Issued       int    `json:"issued"`
	Redeemed     int    `json:"redeemed"`
	Rejected     int    `json:"rejected"`
	Revoked      int    `json:"revoked"`
	Revenue      int    `json:"revenue"`
	RevenueLabel string `json:"revenue_label"`
}

type WebTrafficExtensionAnalytics struct {
	Confirmed    int    `json:"confirmed"`
	Pending      int    `json:"pending"`
	Active       int    `json:"active"`
	Restored     int    `json:"restored"`
	TrafficGB    int    `json:"traffic_gb"`
	Revenue      int    `json:"revenue"`
	RevenueLabel string `json:"revenue_label"`
}

type WebOperatorPaymentProvider struct {
	Provider     string  `json:"provider"`
	Confirmed    int     `json:"confirmed"`
	Rejected     int     `json:"rejected"`
	Pending      int     `json:"pending"`
	Revenue      int     `json:"revenue"`
	RevenueLabel string  `json:"revenue_label"`
	RevenueShare float64 `json:"revenue_share"`
}

func (s *Service) AdminOperatorAnalytics(ctx context.Context, telegramID int64, days int) (*WebOperatorAnalytics, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	if !validAnalyticsDays(days) {
		return nil, ErrBadInput
	}
	now := s.now().UTC()
	report, err := s.store.ReadOperatorAnalytics(ctx, now.Add(-time.Duration(days)*24*time.Hour), now)
	if err != nil {
		return nil, fmt.Errorf("read operator analytics: %w", err)
	}

	out := &WebOperatorAnalytics{
		RangeDays:   days,
		GeneratedAt: now.Format(time.RFC3339),
		Revenue: WebRevenueAnalytics{
			SubscriptionPayments:         report.Revenue.SubscriptionPayments,
			SubscriptionRevenue:          report.Revenue.SubscriptionRevenue,
			SubscriptionRevenueLabel:     s.priceLabel(report.Revenue.SubscriptionRevenue),
			TrafficExtensionPayments:     report.Revenue.TrafficExtensionPayments,
			TrafficExtensionRevenue:      report.Revenue.TrafficExtensionRevenue,
			TrafficExtensionRevenueLabel: s.priceLabel(report.Revenue.TrafficExtensionRevenue),
			GiftPurchases:                report.Revenue.GiftPurchases,
			GiftRevenue:                  report.Revenue.GiftRevenue,
			GiftRevenueLabel:             s.priceLabel(report.Revenue.GiftRevenue),
			InvitePurchases:              report.Revenue.InvitePurchases,
			InviteRevenue:                report.Revenue.InviteRevenue,
			InviteRevenueLabel:           s.priceLabel(report.Revenue.InviteRevenue),
			TotalRevenue:                 report.Revenue.TotalRevenue,
			TotalRevenueLabel:            s.priceLabel(report.Revenue.TotalRevenue),
		},
		Renewals: WebRenewalAnalytics{
			Payments:       report.Renewals.Payments,
			UniqueProfiles: report.Renewals.UniqueProfiles,
			RepeatProfiles: report.Renewals.RepeatProfiles,
			MonthsAdded:    report.Renewals.MonthsAdded,
		},
		ChurnWinback: WebChurnWinbackAnalytics{
			LocalLapsedUsers: report.ChurnWinback.LocalLapsedUsers,
			WinbackSent:      report.ChurnWinback.WinbackSent,
			WinbackConverted: report.ChurnWinback.WinbackConverted,
		},
		Referrals: WebReferralConversion{
			Created:  report.Referrals.Created,
			Credited: report.Referrals.Credited,
			Pending:  report.Referrals.Pending,
		},
		Gifts: WebGiftAnalytics{
			Created:      report.Gifts.Created,
			Pending:      report.Gifts.Pending,
			Issued:       report.Gifts.Issued,
			Redeemed:     report.Gifts.Redeemed,
			Rejected:     report.Gifts.Rejected,
			Revoked:      report.Gifts.Revoked,
			Revenue:      report.Gifts.Revenue,
			RevenueLabel: s.priceLabel(report.Gifts.Revenue),
		},
		TrafficExtensions: WebTrafficExtensionAnalytics{
			Confirmed:    report.TrafficExtensions.Confirmed,
			Pending:      report.TrafficExtensions.Pending,
			Active:       report.TrafficExtensions.Active,
			Restored:     report.TrafficExtensions.Restored,
			TrafficGB:    report.TrafficExtensions.TrafficGB,
			Revenue:      report.TrafficExtensions.Revenue,
			RevenueLabel: s.priceLabel(report.TrafficExtensions.Revenue),
		},
		ProviderMix: make([]WebOperatorPaymentProvider, 0, len(report.ProviderMix)),
	}
	if out.Renewals.Payments > 0 {
		out.Renewals.AverageMonths = oneDecimal(float64(out.Renewals.MonthsAdded) / float64(out.Renewals.Payments))
	}
	if out.ChurnWinback.WinbackSent > 0 {
		out.ChurnWinback.WinbackConversionRate = percent(out.ChurnWinback.WinbackConverted, out.ChurnWinback.WinbackSent)
	}
	if out.Referrals.Created > 0 {
		out.Referrals.ConversionRate = percent(out.Referrals.Credited, out.Referrals.Created)
	}
	totalProviderRevenue := 0
	for _, p := range report.ProviderMix {
		totalProviderRevenue += p.Revenue
	}
	for _, p := range report.ProviderMix {
		item := WebOperatorPaymentProvider{
			Provider:     p.Provider,
			Confirmed:    p.Confirmed,
			Rejected:     p.Rejected,
			Pending:      p.Pending,
			Revenue:      p.Revenue,
			RevenueLabel: s.priceLabel(p.Revenue),
		}
		if totalProviderRevenue > 0 {
			item.RevenueShare = percent(p.Revenue, totalProviderRevenue)
		}
		out.ProviderMix = append(out.ProviderMix, item)
	}
	return out, nil
}

func validAnalyticsDays(days int) bool {
	return days == 7 || days == 30 || days == 90
}

func percent(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return oneDecimal(float64(part) * 100 / float64(total))
}

func oneDecimal(v float64) float64 {
	return math.Round(v*10) / 10
}
