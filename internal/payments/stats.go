package payments

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// statsRevenueWindow bounds the confirmed-payments counter in /stats.
const statsRevenueWindow = 30 * 24 * time.Hour

// sendAdminStats builds and sends the 📊 /stats report: panel user counts from
// the Remnawave API plus payment/gift/invite counters from the local store.
func (s *Service) sendAdminStats(ctx context.Context, chatID int64) {
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		s.logger.Error("stats: list users failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения пользователей из панели."))
		return
	}

	now := s.now()
	var total, active, expiringSoon, expired, linked int
	for i := range subs {
		u := &subs[i]
		total++
		if u.TelegramID != 0 {
			linked++
		}
		pastExpiry := !u.ExpireAt.IsZero() && !u.ExpireAt.After(now)
		switch {
		case u.Status == "ACTIVE" && !pastExpiry:
			active++
			if !u.ExpireAt.IsZero() && u.ExpireAt.Sub(now) <= 7*24*time.Hour {
				expiringSoon++
			}
		case u.Status == "EXPIRED" || pastExpiry:
			expired++
		}
	}

	st, err := s.store.ReadAdminStats(ctx, now.Add(-statsRevenueWindow))
	if err != nil {
		s.logger.Error("stats: read store stats failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка чтения статистики из базы."))
		return
	}

	var b strings.Builder
	b.WriteString(i18n.T("📊 Статистика\n\n"))
	b.WriteString(i18n.T("Пользователи панели:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• всего: %d\n"), total))
	b.WriteString(fmt.Sprintf(i18n.T("• активных: %d\n"), active))
	b.WriteString(fmt.Sprintf(i18n.T("• истекают в ближайшие 7 дней: %d\n"), expiringSoon))
	b.WriteString(fmt.Sprintf(i18n.T("• истекших: %d\n"), expired))
	b.WriteString(fmt.Sprintf(i18n.T("• с привязанным Telegram: %d\n"), linked))
	b.WriteString(i18n.T("\nОплаты:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• подтверждено за 30 дней: %d (на %s)\n"), st.PaymentsConfirmed, s.priceLabel(st.Revenue)))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают подтверждения: %d\n"), st.PaymentsPending))
	b.WriteString(i18n.T("\nПодарочные коды:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают оплаты: %d\n"), st.GiftsByStatus["pending"]))
	b.WriteString(fmt.Sprintf(i18n.T("• выданы (не активированы): %d\n"), st.GiftsByStatus["issued"]))
	b.WriteString(fmt.Sprintf(i18n.T("• активированы: %d\n"), st.GiftsByStatus["activated"]))
	b.WriteString(i18n.T("\nПриглашения:\n"))
	b.WriteString(fmt.Sprintf(i18n.T("• ожидают одобрения: %d"), st.InvitesPending))

	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), kb)
}
