package payments

import (
	"context"
	"fmt"
	"strings"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// SendMenu replies with the user menu: a short command list plus, when the
// payment flow is enabled, buttons to view tariffs and start the gift and
// invite flows.
func (s *Service) SendMenu(ctx context.Context, chatID int64) bool {
	text := "Меню\n\n" +
		"/tariff — посмотреть тарифы\n" +
		"/gift — подарить подписку\n" +
		"/mygifts — мои подарочные подписки\n" +
		"/invite — пригласить нового пользователя\n" +
		"/register — привязать свой Telegram к профилю\n" +
		"/cancel — отменить текущее действие"
	if !s.isEnabled() {
		_ = s.bot.SendPlain(ctx, chatID, text)
		return true
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "💵 Тарифы", CallbackData: "menu:tariffs"}},
			{{Text: "🎁 Подарить подписку", CallbackData: "menu:gift"}},
			{{Text: "📦 Мои подарки", CallbackData: "menu:mygifts"}},
			{{Text: "👤 Пригласить пользователя", CallbackData: "menu:invite"}},
			{{Text: "🔗 Привязать аккаунт", CallbackData: "menu:register"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
	return true
}

// SendTariffs lists the current tariffs to any user. Returns true (handled).
func (s *Service) SendTariffs(ctx context.Context, chatID int64) bool {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("tariffs: list failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Тарифы пока не заданы.")
		return true
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, chatID, strings.TrimRight(b.String(), "\n"))
	return true
}

// handleMenuTariffs lists tariffs from the menu button.
func (s *Service) handleMenuTariffs(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.SendTariffs(ctx, cb.Message.Chat.ID)
	}
	return true
}

// HandleText consumes a free-text message when the chat is mid-flow
// (invite, redeem, register), plus /cancel. Returns true only when it
// handled the message.
func (s *Service) HandleText(ctx context.Context, m *tg.Message) bool {
	if m == nil {
		return false
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if text == "/cancel" {
		hasInvite := s.getInvite(chatID) != nil
		hasRegister := s.getRegister(chatID) != nil
		hasGiftCode := s.getGiftCode(chatID) != nil
		hasRedeem := s.getRedeem(chatID) != nil
		if !hasInvite && !hasRegister && !hasGiftCode && !hasRedeem {
			return false
		}
		s.clearInvite(chatID)
		s.clearRegister(chatID)
		s.clearGiftCode(chatID)
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Отменено.")
		return true
	}

	if s.handleInviteUsernameInput(ctx, m) {
		return true
	}
	if s.handleRedeemUsernameInput(ctx, m) {
		return true
	}
	return s.handleRegisterUsernameInput(ctx, m)
}
