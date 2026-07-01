package payments

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

const webPaymentPageSize = 25

type WebPaymentFilter struct {
	Days     int
	Status   string
	Provider string
	Query    string
	Page     int
}

type WebPaymentRecord struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	TelegramID      int64  `json:"telegram_id"`
	PayerUsername   string `json:"payer_username,omitempty"`
	PayerTelegramID int64  `json:"payer_telegram_id,omitempty"`
	Months          int    `json:"months"`
	Price           int    `json:"price"`
	PriceLabel      string `json:"price_label"`
	Status          string `json:"status"`
	Provider        string `json:"provider"`
	ProviderTxnID   string `json:"provider_txn_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	ResolvedAt      string `json:"resolved_at,omitempty"`
}

type WebPaymentDaily struct {
	Date         string `json:"date"`
	Confirmed    int    `json:"confirmed"`
	Revenue      int    `json:"revenue"`
	RevenueLabel string `json:"revenue_label"`
}

type WebPaymentProvider struct {
	Provider     string `json:"provider"`
	Confirmed    int    `json:"confirmed"`
	Rejected     int    `json:"rejected"`
	Pending      int    `json:"pending"`
	Revenue      int    `json:"revenue"`
	RevenueLabel string `json:"revenue_label"`
}

type WebPaymentAnalytics struct {
	Confirmed          int                  `json:"confirmed"`
	Rejected           int                  `json:"rejected"`
	Pending            int                  `json:"pending"`
	Revenue            int                  `json:"revenue"` // subscription payments only
	RevenueLabel       string               `json:"revenue_label"`
	GiftRevenue        int                  `json:"gift_revenue"`
	GiftRevenueLabel   string               `json:"gift_revenue_label"`
	InviteRevenue      int                  `json:"invite_revenue"`
	InviteRevenueLabel string               `json:"invite_revenue_label"`
	TotalRevenue       int                  `json:"total_revenue"` // payments + gifts + invites
	TotalRevenueLabel  string               `json:"total_revenue_label"`
	ConversionRate     float64              `json:"conversion_rate"`
	Daily              []WebPaymentDaily    `json:"daily"`
	Providers          []WebPaymentProvider `json:"providers"`
}

type WebPaymentReport struct {
	Days       int                 `json:"days"`
	Items      []WebPaymentRecord  `json:"items"`
	Total      int                 `json:"total"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	TotalPages int                 `json:"total_pages"`
	Analytics  WebPaymentAnalytics `json:"analytics"`
}

// AdminPaymentReport returns local payment history without calling Remnawave,
// keeping operational reporting available during panel outages.
func (s *Service) AdminPaymentReport(ctx context.Context, telegramID int64, f WebPaymentFilter) (*WebPaymentReport, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	if !validPaymentReportFilter(f) {
		return nil, ErrBadInput
	}

	since := s.now().UTC().Add(-time.Duration(f.Days) * 24 * time.Hour)
	report, err := s.store.ReadPaymentReport(ctx, store.PaymentReportFilter{
		Since: since, Status: f.Status, Provider: f.Provider, Query: strings.TrimSpace(f.Query),
		Limit: webPaymentPageSize, Offset: (f.Page - 1) * webPaymentPageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("read payment report: %w", err)
	}

	out := &WebPaymentReport{
		Days: f.Days, Items: make([]WebPaymentRecord, 0, len(report.Items)), Total: report.Total,
		Page: f.Page, PageSize: webPaymentPageSize,
		Analytics: WebPaymentAnalytics{
			Confirmed: report.Analytics.Confirmed, Rejected: report.Analytics.Rejected,
			Pending: report.Analytics.Pending, Revenue: report.Analytics.Revenue,
			RevenueLabel:       s.priceLabel(report.Analytics.Revenue),
			GiftRevenue:        report.Analytics.GiftRevenue,
			GiftRevenueLabel:   s.priceLabel(report.Analytics.GiftRevenue),
			InviteRevenue:      report.Analytics.InviteRevenue,
			InviteRevenueLabel: s.priceLabel(report.Analytics.InviteRevenue),
			TotalRevenue:       report.Analytics.TotalRevenue,
			TotalRevenueLabel:  s.priceLabel(report.Analytics.TotalRevenue),
			Daily:              make([]WebPaymentDaily, 0, len(report.Analytics.Daily)),
			Providers:          make([]WebPaymentProvider, 0, len(report.Analytics.Providers)),
		},
	}
	if out.Total > 0 {
		out.TotalPages = (out.Total + webPaymentPageSize - 1) / webPaymentPageSize
	}
	resolved := report.Analytics.Confirmed + report.Analytics.Rejected
	if resolved > 0 {
		out.Analytics.ConversionRate = math.Round(float64(report.Analytics.Confirmed)*1000/float64(resolved)) / 10
	}
	for _, r := range report.Items {
		item := WebPaymentRecord{
			ID: r.ID, Username: r.Username, TelegramID: r.TelegramID,
			PayerUsername: r.PayerUsername, PayerTelegramID: r.PayerTelegramID,
			Months: r.Months, Price: r.Price, PriceLabel: s.priceLabel(r.Price),
			Status: r.Status, Provider: r.Provider, ProviderTxnID: r.ProviderTxnID,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.ConfirmedAt != nil {
			item.ResolvedAt = r.ConfirmedAt.UTC().Format(time.RFC3339)
		}
		out.Items = append(out.Items, item)
	}
	for _, d := range report.Analytics.Daily {
		out.Analytics.Daily = append(out.Analytics.Daily, WebPaymentDaily{
			Date: d.Date, Confirmed: d.Confirmed, Revenue: d.Revenue, RevenueLabel: s.priceLabel(d.Revenue),
		})
	}
	for _, p := range report.Analytics.Providers {
		out.Analytics.Providers = append(out.Analytics.Providers, WebPaymentProvider{
			Provider: p.Provider, Confirmed: p.Confirmed, Rejected: p.Rejected,
			Pending: p.Pending, Revenue: p.Revenue, RevenueLabel: s.priceLabel(p.Revenue),
		})
	}
	return out, nil
}

func validPaymentReportFilter(f WebPaymentFilter) bool {
	if f.Days != 7 && f.Days != 30 && f.Days != 90 {
		return false
	}
	if f.Page < 1 || len(strings.TrimSpace(f.Query)) > 100 {
		return false
	}
	switch f.Status {
	case "all", "pending", "confirmed", "rejected":
	default:
		return false
	}
	switch f.Provider {
	case "all", ProviderP2P, ProviderPlatega, ProviderTelegramStars:
	default:
		return false
	}
	return true
}
