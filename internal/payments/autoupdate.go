package payments

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// SetUpdateTrigger wires the action run when an admin taps "Install now" on an
// update notification (typically a Watchtower HTTP-API call). Left nil, the
// install button falls back to manual upgrade instructions. Called once at
// startup, before callback polling begins.
func (s *Service) SetUpdateTrigger(fn func(context.Context) error) {
	s.updateTrigger = fn
}

// shortDigest trims an image digest ("sha256:abcd...") to a readable prefix.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func updateKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: i18n.T("🔄 Установить сейчас"), CallbackData: "upd:install"}},
		{{Text: i18n.T("Позже"), CallbackData: "upd:dismiss"}},
	}}
}

// NotifyUpdateAvailable DMs every admin that a newer bot image is available,
// with buttons to install now or dismiss. Implements autoupdate.Notifier.
func (s *Service) NotifyUpdateAvailable(ctx context.Context, oldDigest, newDigest string) {
	text := fmt.Sprintf(i18n.T("🆕 Доступно обновление бота.\n\nТекущая версия: %s\nНовая версия: %s"),
		shortDigest(oldDigest), shortDigest(newDigest))
	kb := updateKeyboard()
	for _, adminID := range s.adminIDs {
		if _, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb); err != nil {
			s.logger.Error("autoupdate: notify admin failed", "admin_id", adminID, "err", err.Error())
		}
	}
}

// handleUpdateInstall applies the available update when an admin taps the
// button. Degrades to manual instructions when no trigger is wired in, and
// never touches the trigger under DRY_RUN.
func (s *Service) handleUpdateInstall(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}
	if s.updateTrigger == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
		if cb.Message != nil {
			_ = s.bot.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID,
				i18n.T("Автоустановка не настроена. Обновите вручную:\ndocker compose pull && docker compose up -d"), nil)
		}
		return true
	}
	if s.dryRun {
		s.logger.Info("autoupdate: install skipped (dry run)")
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Обновление запущено, бот скоро перезапустится."))
		return true
	}
	if err := s.updateTrigger(ctx); err != nil {
		s.logger.Error("autoupdate: trigger failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось запустить обновление."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Обновление запущено, бот скоро перезапустится."))
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	return true
}

// handleUpdateDismiss clears the update notification's buttons.
func (s *Service) handleUpdateDismiss(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	return true
}
