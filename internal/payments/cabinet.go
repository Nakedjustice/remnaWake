package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// SendCabinet shows the personal cabinet: linked profiles with status, expiry
// and subscription link, a gifts/invites summary, and action buttons. For
// unlinked users it offers to start the register flow. Returns true (handled).
func (s *Service) SendCabinet(ctx context.Context, chatID int64) bool {
	if !s.isEnabled() {
		return false
	}

	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("cabinet: find by telegram id failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}

	if len(subs) == 0 {
		s.sendCabinetUnlinked(ctx, chatID)
		return true
	}

	now := s.now()
	var b strings.Builder
	b.WriteString(i18n.T("👤 Личный кабинет\n"))
	for i := range subs {
		sub := &subs[i]
		b.WriteString(i18n.T("\n📡 Профиль: ") + sub.Username + "\n")
		b.WriteString(i18n.T("Статус: ") + subStatusLabel(sub.Status) + "\n")
		if !sub.ExpireAt.IsZero() {
			b.WriteString(i18n.T("Действует до: ") + sub.ExpireAt.Format("02.01.2006"))
			if sub.ExpireAt.After(now) {
				d := daysLeft(now, sub.ExpireAt)
				b.WriteString(fmt.Sprintf(i18n.T(" (осталось %d %s)"), d, pluralRu(d, i18n.T("день"), i18n.T("дня"), i18n.T("дней"))))
			}
			b.WriteString("\n")
		}
		if sub.SubscriptionURL != "" {
			b.WriteString(i18n.T("🔗 Ссылка на подписку:\n") + sub.SubscriptionURL + "\n")
		}

		// Refresh the snapshot so the payment flow started from the cabinet
		// works even if this user was never notified.
		if err := s.store.UpsertNotifiedUser(ctx, store.NotifiedUser{
			RemnawaveID: sub.RemnawaveID,
			UUID:        sub.UUID,
			Username:    sub.Username,
			TelegramID:  chatID,
			ExpireAt:    sub.ExpireAt,
		}); err != nil {
			s.logger.Error("cabinet: remember user failed", "err", err.Error(), "user_id", sub.RemnawaveID)
		}
	}

	if line := s.giftsSummaryLine(ctx, chatID); line != "" {
		b.WriteString("\n" + line + "\n")
	}
	if line := s.invitesSummaryLine(ctx, chatID); line != "" {
		b.WriteString(line + "\n")
	}

	rows := make([][]tg.InlineKeyboardButton, 0, len(subs)+5)
	if url := s.getWebAppURL(); url != "" {
		// Renewal, gifts and invites live in the mini app, so their inline
		// buttons would duplicate it — keep only the mini app entry and
		// profile linking, which stays a chat flow.
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:   i18n.T("🖥 Открыть мини-приложение"),
			WebApp: &tg.WebAppInfo{URL: url},
		}})
	} else {
		for i := range subs {
			label := i18n.T("💳 Оплатить / продлить")
			if len(subs) > 1 {
				label = fmt.Sprintf(i18n.T("💳 Продлить «%s»"), subs[i].Username)
			}
			rows = append(rows, []tg.InlineKeyboardButton{{
				Text:         label,
				CallbackData: fmt.Sprintf("cab:pay:%d", subs[i].RemnawaveID),
			}})
		}
		rows = append(rows,
			[]tg.InlineKeyboardButton{{Text: i18n.T("🎁 Подарить подписку"), CallbackData: "menu:gift"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("📦 Мои подарки"), CallbackData: "menu:mygifts"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("👥 Пригласить пользователя"), CallbackData: "menu:invite"}},
		)
	}
	rows = append(rows,
		[]tg.InlineKeyboardButton{{Text: i18n.T("🔁 Привязать другой профиль"), CallbackData: "menu:register"}},
	)

	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(b.String(), "\n"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	return true
}

func (s *Service) sendCabinetUnlinked(ctx context.Context, chatID int64) {
	text := i18n.T("👤 Личный кабинет\n\n") +
		i18n.T("Ваш Telegram пока не привязан к профилю подписки, поэтому здесь пусто.\n") +
		i18n.T("Привяжите аккаунт — и тут появятся статус подписки, дата окончания и ссылка.")
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("🔗 Привязать аккаунт"), CallbackData: "menu:register"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// giftsSummaryLine builds the one-line gift summary, or "" when the user has
// no gifts (or the lookup failed — the cabinet still renders without it).
func (s *Service) giftsSummaryLine(ctx context.Context, chatID int64) string {
	gifts, err := s.store.ListGiftCodesByBuyer(ctx, chatID)
	if err != nil {
		s.logger.Error("cabinet: list gifts failed", "err", err.Error())
		return ""
	}
	if len(gifts) == 0 {
		return ""
	}
	var pending, issued int
	for _, g := range gifts {
		switch g.Status {
		case "pending":
			pending++
		case "issued":
			issued++
		}
	}
	line := fmt.Sprintf(i18n.T("🎁 Подарки: %d"), len(gifts))
	var parts []string
	if pending > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T("%d ждёт оплаты"), pending))
	}
	if issued > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T("%d ждёт активации"), issued))
	}
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	return line
}

// invitesSummaryLine builds the one-line invite summary, or "" when the user
// has no invite requests.
func (s *Service) invitesSummaryLine(ctx context.Context, chatID int64) string {
	invites, err := s.store.ListInviteRequestsByInviter(ctx, chatID)
	if err != nil {
		s.logger.Error("cabinet: list invites failed", "err", err.Error())
		return ""
	}
	if len(invites) == 0 {
		return ""
	}
	var pending int
	for _, r := range invites {
		if r.Status == "pending" {
			pending++
		}
	}
	line := fmt.Sprintf(i18n.T("👥 Приглашения: %d"), len(invites))
	if pending > 0 {
		line += fmt.Sprintf(i18n.T(" (%d на рассмотрении)"), pending)
	}
	return line
}

// handleMenuCabinet opens the cabinet from an inline menu button.
func (s *Service) handleMenuCabinet(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.SendCabinet(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleCabinetPay starts the payment flow from the cabinet: shows the
// requisites and sends a fresh message with the tariff keyboard, so the
// cabinet message keeps its buttons.
func (s *Service) handleCabinetPay(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "cab:pay:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать профиль."))
		return true
	}
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}
	chatID := cb.Message.Chat.ID

	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req != "" {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Реквизиты для оплаты:\n\n")+req)
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("cabinet: list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if len(tariffs) == 0 {
		// No tariffs configured: behave like the legacy single-option flow.
		s.createRequestAndNotify(ctx, cb, userID, 1, 0)
		return true
	}

	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf(i18n.T("%d мес. — %s"), t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("pick:%d:%d", userID, t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("Отмена"), CallbackData: "cab:cancel"}})

	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("💳 Продление подписки. Выберите период, после оплаты заявка уйдёт администратору:"),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	return true
}

// handleCabinetCancel dismisses the tariff keyboard spawned from the cabinet.
func (s *Service) handleCabinetCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Отменено."))
	return true
}

func subStatusLabel(status string) string {
	switch status {
	case "ACTIVE":
		return i18n.T("✅ Активна")
	case "EXPIRED":
		return i18n.T("⛔ Истекла")
	case "DISABLED":
		return i18n.T("🚫 Отключена")
	case "LIMITED":
		return i18n.T("⚠️ Превышен лимит трафика")
	}
	if status == "" {
		return "—"
	}
	return status
}

// daysLeft reports the whole days remaining until exp, rounding any partial day
// up so the /me cabinet and the Mini App agree with the Remnawave panel (which
// shows e.g. "8 days" while 7 days and some hours remain) and with the expiry
// reminder. Returns 0 once exp is reached.
func daysLeft(now, exp time.Time) int {
	diff := exp.Sub(now)
	if diff <= 0 {
		return 0
	}
	days := int(diff / (24 * time.Hour))
	if diff%(24*time.Hour) != 0 {
		days++
	}
	return days
}

func pluralRu(n int, one, few, many string) string {
	last := n % 100
	if last >= 11 && last <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}
