package payments

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/autoupdate"
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

func (s *Service) SetUpdateChecker(checker UpdateChecker) {
	s.mu.Lock()
	s.updateChecker = checker
	s.mu.Unlock()
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

func parseUpdateInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty interval")
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, nil
	}
	if len(raw) < 2 {
		return 0, fmt.Errorf("bad interval")
	}
	unit := raw[len(raw)-1]
	n, err := time.ParseDuration(raw[:len(raw)-1] + "h")
	if err == nil && unit == 'd' && n > 0 {
		return n * 24, nil
	}
	return 0, fmt.Errorf("bad interval")
}

func formatUpdateCheckResult(r autoupdate.CheckResult) string {
	switch r.Status {
	case autoupdate.CheckStatusBaselineInitialized:
		return fmt.Sprintf(i18n.T("✅ Проверка выполнена. Текущая версия сохранена как базовая: %s"), shortDigest(r.Remote))
	case autoupdate.CheckStatusUpToDate:
		return fmt.Sprintf(i18n.T("✅ Обновлений нет. Текущая версия: %s"), shortDigest(r.Remote))
	case autoupdate.CheckStatusUpdateAvailable:
		return fmt.Sprintf(i18n.T("🆕 Доступно обновление. Уведомление с кнопками отправлено администраторам.\nТекущая версия: %s\nНовая версия: %s"), shortDigest(r.Baseline), shortDigest(r.Remote))
	case autoupdate.CheckStatusAlreadyNotified:
		return fmt.Sprintf(i18n.T("🆕 Обновление уже найдено ранее. Проверьте сообщение с кнопками установки.\nТекущая версия: %s\nНовая версия: %s"), shortDigest(r.Baseline), shortDigest(r.Remote))
	default:
		return i18n.T("✅ Проверка обновлений выполнена.")
	}
}

func (s *Service) updateCheckerLocked() UpdateChecker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateChecker
}

func (s *Service) sendUpdateSettings(ctx context.Context, chatID int64) {
	checker := s.updateCheckerLocked()
	if checker == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Автопроверка обновлений не настроена. Включите AUTOUPDATE_ENABLED=true."))
		return
	}
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: i18n.T("🔎 Проверить сейчас"), CallbackData: "adm:upd:check"}},
		{{Text: i18n.T("⏱ Изменить интервал"), CallbackData: "adm:upd:interval"}},
		{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
	}}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		fmt.Sprintf(i18n.T("Автопроверка обновлений\n\nТекущий интервал: %s"), checker.Interval().String()), kb)
}

func (s *Service) startUpdateIntervalInput(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputUpdateInterval
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите интервал проверки обновлений: например 15m, 6h или 1d."))
}

func (s *Service) consumeUpdateInterval(ctx context.Context, chatID int64, text string) bool {
	interval, err := parseUpdateInterval(text)
	if err != nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите интервал в минутах, часах или днях. Примеры: 15m, 6h, 1d."))
		return true
	}
	checker := s.updateCheckerLocked()
	if checker == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Автопроверка обновлений не настроена. Включите AUTOUPDATE_ENABLED=true."))
		return true
	}
	if err := checker.SetInterval(ctx, interval); err != nil {
		s.logger.Error("autoupdate: set interval failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения интервала проверки обновлений."))
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Интервал проверки обновлений сохранён: %s"), interval.String()))
	return true
}

func (s *Service) manualUpdateCheck(ctx context.Context, chatID int64) {
	checker := s.updateCheckerLocked()
	if checker == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Автопроверка обновлений не настроена. Включите AUTOUPDATE_ENABLED=true."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Проверяю обновления…"))
	result, err := checker.CheckNow(ctx)
	if err != nil {
		s.logger.Error("autoupdate: manual check failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось проверить обновления. Попробуйте позже."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, formatUpdateCheckResult(result))
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

	s.mu.Lock()
	if s.updateInProgress {
		s.mu.Unlock()
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Обновление уже запущено."))
		if cb.Message != nil {
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
		}
		return true
	}
	s.updateInProgress = true
	trigger := s.updateTrigger
	delay := s.updateDelay
	s.mu.Unlock()

	if err := s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Обновление запущено, бот скоро перезапустится.")); err != nil {
		s.logger.Warn("autoupdate: answer callback failed", "err", err.Error())
	}
	if cb.Message != nil {
		if err := s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil); err != nil {
			s.logger.Warn("autoupdate: clear update buttons failed", "err", err.Error())
		}
	}

	// Watchtower may stop this container before its HTTP response arrives. Delay
	// the request until after this handler returns so the polling loop can send
	// Telegram the advanced update offset and avoid replaying the callback.
	go s.runUpdateTrigger(ctx, cb.From.ID, trigger, delay)
	return true
}

func (s *Service) runUpdateTrigger(ctx context.Context, adminID int64, trigger func(context.Context) error, delay time.Duration) {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			s.finishUpdateTrigger()
			return
		case <-timer.C:
		}
	}

	err := trigger(ctx)
	s.finishUpdateTrigger()
	if err == nil {
		return
	}

	s.logger.Error("autoupdate: trigger failed", "err", err.Error())
	if sendErr := s.bot.SendPlain(ctx, adminID,
		i18n.T("Не удалось запустить обновление. Попробуйте снова позже или обновите вручную:\ndocker compose pull && docker compose up -d")); sendErr != nil {
		s.logger.Error("autoupdate: send trigger failure failed", "admin_id", adminID, "err", sendErr.Error())
	}
}

func (s *Service) finishUpdateTrigger() {
	s.mu.Lock()
	s.updateInProgress = false
	s.mu.Unlock()
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
