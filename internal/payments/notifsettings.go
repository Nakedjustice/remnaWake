package payments

import (
	"context"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// notifCardKeyboard renders the two independent notification toggles, each
// labelled with its current state.
func (s *Service) notifCardKeyboard(expiryMuted, winbackMuted bool) *tg.InlineKeyboardMarkup {
	expiryLabel := i18n.T("🔔 Напоминания об окончании: вкл")
	if expiryMuted {
		expiryLabel = i18n.T("🔕 Напоминания об окончании: выкл")
	}
	winbackLabel := i18n.T("🔔 Напоминания после окончания: вкл")
	if winbackMuted {
		winbackLabel = i18n.T("🔕 Напоминания после окончания: выкл")
	}
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: expiryLabel, CallbackData: "notif:expiry"}},
		{{Text: winbackLabel, CallbackData: "notif:winback"}},
	}}
}

// sendNotifCard sends the notification-preferences card to chatID.
func (s *Service) sendNotifCard(ctx context.Context, chatID int64) {
	em, wm, err := s.store.GetNotificationPrefs(ctx, chatID)
	if err != nil {
		s.logger.Error("notif: get prefs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("Настройки уведомлений. Нажмите кнопку, чтобы включить или выключить."),
		s.notifCardKeyboard(em, wm))
}

// handleNotifMenu opens the notification-preferences card from the menu button.
func (s *Service) handleNotifMenu(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.sendNotifCard(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleNotifToggle flips one notification preference (kind is
// store.NotificationExpiry or store.NotificationWinback) and re-renders the card
// in place. The chat ID equals the user's Telegram ID, which is what the notify
// job keys mute checks on.
func (s *Service) handleNotifToggle(ctx context.Context, cb *tg.CallbackQuery, kind string) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}
	chatID := cb.Message.Chat.ID
	em, wm, err := s.store.GetNotificationPrefs(ctx, chatID)
	if err != nil {
		s.logger.Error("notif: get prefs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}

	var newVal bool
	switch kind {
	case store.NotificationExpiry:
		newVal = !em
		em = newVal
	case store.NotificationWinback:
		newVal = !wm
		wm = newVal
	default:
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}

	if err := s.store.SetNotificationPref(ctx, chatID, kind, newVal); err != nil {
		s.logger.Error("notif: save pref failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка сохранения настройки."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, s.notifCardKeyboard(em, wm))
	return true
}
