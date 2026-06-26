package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// statsRevenueWindow bounds the confirmed-payments counter in /stats.
const statsRevenueWindow = 30 * 24 * time.Hour

// WebAdminStats is the shared statistics snapshot used by the bot report and
// the Mini App admin panel.
type WebAdminStats struct {
	UsersTotal           int    `json:"users_total"`
	UsersActive          int    `json:"users_active"`
	UsersExpiringSoon    int    `json:"users_expiring_soon"`
	UsersExpired         int    `json:"users_expired"`
	UsersLinked          int    `json:"users_linked"`
	PaymentsConfirmed30d int    `json:"payments_confirmed_30d"`
	PaymentsPending      int    `json:"payments_pending"`
	Revenue              int    `json:"revenue"`
	RevenueLabel         string `json:"revenue_label"`
	GiftsPending         int    `json:"gifts_pending"`
	GiftsIssued          int    `json:"gifts_issued"`
	GiftsActivated       int    `json:"gifts_activated"`
	InvitesPending       int    `json:"invites_pending"`
	// Infrastructure cost, converted into the service currency. InfraUnconverted
	// counts servers whose currency has no available FX rate.
	InfraServers          int     `json:"infra_servers"`
	InfraMonthlyCost      float64 `json:"infra_monthly_cost"`
	InfraMonthlyCostLabel string  `json:"infra_monthly_cost_label"`
	InfraUnconverted      int     `json:"infra_unconverted"`
}

// AdminStatsData returns the admin statistics snapshot for the Mini App and
// bot. The caller identity is always checked because Web API requests reach
// this method directly.
func (s *Service) AdminStatsData(ctx context.Context, telegramID int64) (*WebAdminStats, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: list users: %v", ErrPanelUnavailable, err)
	}

	now := s.now()
	out := &WebAdminStats{UsersTotal: len(subs)}
	for i := range subs {
		u := &subs[i]
		if u.TelegramID != 0 {
			out.UsersLinked++
		}
		pastExpiry := !u.ExpireAt.IsZero() && !u.ExpireAt.After(now)
		switch {
		case u.Status == "ACTIVE" && !pastExpiry:
			out.UsersActive++
			if !u.ExpireAt.IsZero() && u.ExpireAt.Sub(now) <= 7*24*time.Hour {
				out.UsersExpiringSoon++
			}
		case u.Status == "EXPIRED" || pastExpiry:
			out.UsersExpired++
		}
	}

	st, err := s.store.ReadAdminStats(ctx, now.Add(-statsRevenueWindow))
	if err != nil {
		return nil, fmt.Errorf("read store stats: %w", err)
	}
	out.PaymentsConfirmed30d = st.PaymentsConfirmed
	out.PaymentsPending = st.PaymentsPending
	out.Revenue = st.Revenue
	out.RevenueLabel = s.priceLabel(st.Revenue)
	out.GiftsPending = st.GiftsByStatus["pending"]
	out.GiftsIssued = st.GiftsByStatus["issued"]
	out.GiftsActivated = st.GiftsByStatus["activated"]
	out.InvitesPending = st.InvitesPending

	base := s.baseCurrencyISO(ctx)
	servers, err := s.store.ListInfraServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list infra servers: %w", err)
	}
	out.InfraServers = len(servers)
	var monthly float64
	for i := range servers {
		if m, ok := s.serverMonthlyBase(ctx, base, &servers[i]); ok {
			monthly += m
		} else {
			out.InfraUnconverted++
		}
	}
	out.InfraMonthlyCost = monthly
	out.InfraMonthlyCostLabel = s.baseMonthlyLabel(monthly)
	return out, nil
}

// sendAdminStats builds and sends the 📊 /stats report: panel user counts from
// the Remnawave API plus payment/gift/invite counters from the local store.
func (s *Service) sendAdminStats(ctx context.Context, chatID int64) {
	st, err := s.AdminStatsData(ctx, chatID)
	if errors.Is(err, ErrPanelUnavailable) {
		s.logger.Error("stats: list users failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения пользователей из панели."))
		return
	}
	if err != nil {
		s.logger.Error("stats: read store stats failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка чтения статистики из базы."))
		return
	}

	var b strings.Builder
	b.WriteString(i18n.T("📊 Статистика\n\n"))
	b.WriteString(i18n.T("Пользователи панели:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• всего: %d\n"), st.UsersTotal))
	b.WriteString(fmt.Sprintf(i18n.T("• активных: %d\n"), st.UsersActive))
	b.WriteString(fmt.Sprintf(i18n.T("• истекают в ближайшие 7 дней: %d\n"), st.UsersExpiringSoon))
	b.WriteString(fmt.Sprintf(i18n.T("• истекших: %d\n"), st.UsersExpired))
	b.WriteString(fmt.Sprintf(i18n.T("• с привязанным Telegram: %d\n"), st.UsersLinked))
	b.WriteString(i18n.T("\nОплаты:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• подтверждено за 30 дней: %d (на %s)\n"), st.PaymentsConfirmed30d, st.RevenueLabel))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают подтверждения: %d\n"), st.PaymentsPending))
	b.WriteString(i18n.T("\nПодарочные коды:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают оплаты: %d\n"), st.GiftsPending))
	b.WriteString(fmt.Sprintf(i18n.T("• выданы (не активированы): %d\n"), st.GiftsIssued))
	b.WriteString(fmt.Sprintf(i18n.T("• активированы: %d\n"), st.GiftsActivated))
	b.WriteString(i18n.T("\nПриглашения:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают одобрения: %d"), st.InvitesPending))
	b.WriteString(i18n.T("\n\nИнфраструктура:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• серверов: %d\n"), st.InfraServers))
	b.WriteString(fmt.Sprintf(i18n.T("• расходы в месяц: %s"), st.InfraMonthlyCostLabel))
	if st.InfraUnconverted > 0 {
		b.WriteString(fmt.Sprintf(i18n.T("\n• без курса: %d"), st.InfraUnconverted))
	}

	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), kb)
}
