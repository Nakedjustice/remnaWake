package payments

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// SendMenu replies with the user menu: a short command list plus, when the
// payment flow is enabled, buttons to view tariffs and start the gift and
// invite flows.
func (s *Service) SendMenu(ctx context.Context, chatID int64) bool {
	text := i18n.T("Меню\n\n" +
		"/me — личный кабинет\n" +
		"/tariff — посмотреть тарифы\n" +
		"/gift — подарить подписку\n" +
		"/mygifts — мои подарочные подписки\n" +
		"/invite — пригласить нового пользователя\n" +
		"/register — привязать свой Telegram к профилю\n" +
		"/cancel — отменить текущее действие")
	if !s.isEnabled() {
		_ = s.bot.SendPlain(ctx, chatID, text)
		return true
	}
	referralOn, _, _ := s.referralConfig()
	referralRow := []tg.InlineKeyboardButton{{Text: i18n.T("🤝 Пригласить друга"), CallbackData: "menu:referral"}}
	var kb *tg.InlineKeyboardMarkup
	if url := s.getWebAppURL(); url != "" {
		// Cabinet, tariffs, gifts and invites live in the mini app, so their
		// inline buttons would duplicate it — keep only profile linking, which
		// stays a chat flow.
		rows := [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("🖥 Открыть мини-приложение"), WebApp: &tg.WebAppInfo{URL: url}}},
			{{Text: i18n.T("🔗 Привязать аккаунт"), CallbackData: "menu:register"}},
		}
		if referralOn {
			rows = append(rows, referralRow)
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("💬 Поддержка"), CallbackData: "menu:support"}})
		kb = &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
	} else {
		var rows [][]tg.InlineKeyboardButton
		// The free-trial button leads the menu when enabled, since it targets new
		// users who have nothing else to do yet.
		if enabled, _ := s.trialConfig(); enabled {
			rows = append(rows, []tg.InlineKeyboardButton{
				{Text: i18n.T("🎁 Попробовать бесплатно"), CallbackData: "menu:trial"},
			})
		}
		rows = append(rows,
			[]tg.InlineKeyboardButton{{Text: i18n.T("👤 Личный кабинет"), CallbackData: "menu:cabinet"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("💵 Тарифы"), CallbackData: "menu:tariffs"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("🎁 Подарить подписку"), CallbackData: "menu:gift"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("📦 Мои подарки"), CallbackData: "menu:mygifts"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("👤 Пригласить пользователя"), CallbackData: "menu:invite"}},
		)
		if referralOn {
			rows = append(rows, referralRow)
		}
		rows = append(rows,
			[]tg.InlineKeyboardButton{{Text: i18n.T("🔗 Привязать аккаунт"), CallbackData: "menu:register"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("💬 Поддержка"), CallbackData: "menu:support"}},
			[]tg.InlineKeyboardButton{{Text: i18n.T("🔔 Уведомления"), CallbackData: "notif:menu"}},
		)
		kb = &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
	return true
}

// SendTariffs lists the current tariffs to any user. Returns true (handled).
func (s *Service) SendTariffs(ctx context.Context, chatID int64) bool {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("tariffs: list failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Тарифы пока не заданы."))
		return true
	}
	var b strings.Builder
	b.WriteString(i18n.T("Тарифы:\n"))
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf(i18n.T("%d мес. — %s\n"), t.Months, s.priceLabel(t.Price)))
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
// (invite, redeem, register), plus /cancel and bare subscription links.
// Returns true only when it handled the message.
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
		hasPayPhoto := s.getPayPhoto(chatID) != nil
		hasTrial := s.getTrial(chatID) != nil
		hasSupport := s.inSupportSession(chatID)
		if !hasInvite && !hasRegister && !hasGiftCode && !hasRedeem && !hasPayPhoto && !hasTrial && !hasSupport {
			return false
		}
		s.clearInvite(chatID)
		s.clearRegister(chatID)
		s.clearGiftCode(chatID)
		s.clearRedeem(chatID)
		s.clearPayPhoto(chatID)
		s.clearTrial(chatID)
		s.setSupportSession(chatID, false)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отменено."))
		return true
	}

	// A live support session captures all plain text until /cancel or close.
	if s.handleSupportSessionInput(ctx, m) {
		return true
	}

	if s.handleTrialUsernameInput(ctx, m) {
		return true
	}
	if s.handleInviteUsernameInput(ctx, m) {
		return true
	}
	if s.handleRedeemUsernameInput(ctx, m) {
		return true
	}
	if s.handleRegisterUsernameInput(ctx, m) {
		return true
	}
	// The receipt nudge runs after the input handlers so an awaiting-receipt
	// state (which the mini app can set remotely) never swallows a reply meant
	// for another in-flight flow.
	if s.remindPayPhoto(ctx, chatID) {
		return true
	}
	// A pasted subscription link starts the linking flow even without /register.
	return s.handleBareSubscriptionLink(ctx, m)
}
