package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

const guardUnblockCallbackPrefix = "guard:unblock:"

const (
	maxRegistrationGuardPatterns     = 32
	maxRegistrationGuardPatternRunes = 64
)

var (
	errRegistrationGuardPatternInvalid = errors.New("invalid registration guard pattern")
	errRegistrationGuardPatternLimit   = errors.New("registration guard pattern limit")
	// ErrRegistrationGuardPatternExists and ErrRegistrationGuardPatternNotFound
	// let the Mini App distinguish stale or duplicate edits from server failures.
	ErrRegistrationGuardPatternExists   = errors.New("registration guard pattern already exists")
	ErrRegistrationGuardPatternNotFound = errors.New("registration guard pattern not found")
	ErrRegistrationGuardNotFound        = errors.New("registration guard not found")
)

var suspiciousRegistrationPatterns = []string{
	"support", "сапорт", "саппорт", "поддерж",
	"admin", "админ",
	"verif", "вериф",
	"official", "офиц",
	"moder", "модер",
	"security", "безопас",
	"refund", "возврат",
}

// CheckTelegramRegistration records the first authenticated interaction and
// reports whether the Telegram identity is automatically blocked. It contains
// the shared decision used by both the bot and the Mini App; callers own their
// channel-specific user-facing response. A newly created block notifies admins
// exactly once regardless of the entry point.
func (s *Service) CheckTelegramRegistration(ctx context.Context, user *tg.User) bool {
	created, blocked := s.evaluateTelegramRegistration(ctx, user)
	if blocked && created {
		pattern := s.suspiciousRegistrationPattern(*user)
		s.logger.Warn("registration guard: suspicious user blocked", "telegram_id", user.ID, "pattern", pattern)
		s.notifyAdminsRegistrationBlocked(ctx, *user, pattern)
	}
	return blocked
}

// evaluateTelegramRegistration is the transport-neutral decision and state
// transition. It deliberately contains no Telegram side effects so callers can
// present the correct channel-specific response.
func (s *Service) evaluateTelegramRegistration(ctx context.Context, user *tg.User) (created, blocked bool) {
	if user == nil || user.ID == 0 || s.isAdmin(user.ID) {
		return false, false
	}

	pattern := s.suspiciousRegistrationPattern(*user)
	created, blocked, err := s.store.RecordRegistration(ctx, store.RegistrationGuard{
		TelegramID:     user.ID,
		Username:       user.Username,
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		MatchedPattern: pattern,
		Blocked:        pattern != "",
	})
	if err != nil {
		s.logger.Error("registration guard: record failed", "err", err.Error(), "telegram_id", user.ID)
		return false, false
	}
	return created, blocked
}

// GuardNewTelegramUser adapts CheckTelegramRegistration for the bot's /start
// flow and sends the existing Telegram message to a blocked user.
func (s *Service) GuardNewTelegramUser(ctx context.Context, user *tg.User) bool {
	created, blocked := s.evaluateTelegramRegistration(ctx, user)
	if !blocked {
		return false
	}
	s.clearRegister(user.ID)
	s.setSupportSession(user.ID, false)
	s.sendRegistrationBlocked(ctx, user.ID)
	if created {
		pattern := s.suspiciousRegistrationPattern(*user)
		s.logger.Warn("registration guard: suspicious user blocked", "telegram_id", user.ID, "pattern", pattern)
		s.notifyAdminsRegistrationBlocked(ctx, *user, pattern)
	}
	return true
}

// HandleBlockedRegistrationMessage consumes messages from an automatically
// blocked user, while preserving the existing support-chat escape hatch.
func (s *Service) HandleBlockedRegistrationMessage(ctx context.Context, m *tg.Message) bool {
	if m == nil {
		return false
	}
	blocked, err := s.store.RegistrationBlocked(ctx, m.Chat.ID)
	if err != nil {
		s.logger.Error("registration guard: check failed", "err", err.Error(), "telegram_id", m.Chat.ID)
		return false
	}
	if !blocked {
		return false
	}

	text := strings.TrimSpace(m.Text)
	if text == "/support" {
		return s.StartSupport(ctx, m.Chat.ID)
	}
	if text == "/cancel" && s.inSupportSession(m.Chat.ID) {
		return false
	}
	if s.inSupportSession(m.Chat.ID) && text != "/cancel" {
		return s.handleSupportSessionInput(ctx, m)
	}
	s.sendRegistrationBlocked(ctx, m.Chat.ID)
	return true
}

func (s *Service) registrationBlocked(ctx context.Context, telegramID int64) bool {
	blocked, err := s.store.RegistrationBlocked(ctx, telegramID)
	if err != nil {
		s.logger.Error("registration guard: check failed", "err", err.Error(), "telegram_id", telegramID)
	}
	return blocked
}

func (s *Service) sendRegistrationBlocked(ctx context.Context, chatID int64) {
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{
		{Text: i18n.T("🛟 Связаться с поддержкой"), CallbackData: "menu:support"},
	}}}
	_, err := s.bot.SendFormattedWithKeyboard(ctx, chatID,
		i18n.T("🛑 <b>Вы не прошли автоматическую антиспам-проверку</b>\n\nДоступ к боту ограничен. Если это ошибка, напишите в поддержку — вас проверят и разблокируют."), kb)
	if err != nil {
		s.logger.Error("registration guard: notify blocked user failed", "err", err.Error(), "telegram_id", chatID)
	}
}

func (s *Service) notifyAdminsRegistrationBlocked(ctx context.Context, user tg.User, pattern string) {
	text := fmt.Sprintf(i18n.T("⚠️ <b>Подозрительная регистрация заблокирована автоматически</b>\n\nПользователь: %s\nTelegram ID: <code>%d</code>\nСработал паттерн: <code>%s</code>\n\nЕсли это живой человек, разблокируйте его кнопкой ниже."),
		escapedTelegramUserLabel(user), user.ID, html.EscapeString(pattern))
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{
		{Text: i18n.T("✅ Разблокировать"), CallbackData: guardUnblockCallbackPrefix + strconv.FormatInt(user.ID, 10)},
	}}}
	for _, adminID := range s.adminIDs {
		if s.dryRun {
			s.logger.Info("dry-run: would notify admin of registration block", "admin_id", adminID, "telegram_id", user.ID)
			continue
		}
		if _, err := s.bot.SendFormattedWithKeyboard(ctx, adminID, text, kb); err != nil {
			s.logger.Error("registration guard: notify admin failed", "err", err.Error(), "admin_id", adminID, "telegram_id", user.ID)
		}
	}
}

func (s *Service) handleRegistrationUnblock(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}
	telegramID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, guardUnblockCallbackPrefix), 10, 64)
	if err != nil || telegramID <= 0 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Некорректный пользователь."))
		return true
	}
	changed, err := s.store.UnblockRegistration(ctx, telegramID)
	if err != nil {
		s.logger.Error("registration guard: unblock failed", "err", err.Error(), "telegram_id", telegramID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось разблокировать. Попробуйте ещё раз."))
		return true
	}
	if !changed {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Пользователь уже разблокирован."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Пользователь разблокирован."))
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	return true
}

func (s *Service) suspiciousRegistrationPattern(user tg.User) string {
	return suspiciousRegistrationPattern(user, s.getBotUsername(), s.customRegistrationGuardPatterns())
}

func suspiciousRegistrationPattern(user tg.User, botUsername string, customPatterns []string) string {
	fields := []string{user.Username, user.FirstName, user.LastName}
	normalized := strings.ToLower(strings.Join(fields, " "))
	for _, pattern := range suspiciousRegistrationPatterns {
		if strings.Contains(normalized, pattern) {
			return pattern
		}
	}
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if botUsername != "" && strings.Contains(normalized, strings.ToLower(botUsername)) {
		return botUsername
	}
	for _, pattern := range customPatterns {
		if strings.Contains(normalized, pattern) {
			return pattern
		}
	}
	return ""
}

func escapedTelegramUserLabel(user tg.User) string {
	parts := make([]string, 0, 2)
	if fullName := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " ")); fullName != "" {
		parts = append(parts, fullName)
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		parts = append(parts, "@"+username)
	}
	if len(parts) == 0 {
		parts = append(parts, "без имени")
	}
	return html.EscapeString(limitTelegramLabel(strings.Join(parts, " · ")))
}

func limitTelegramLabel(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	runes := []rune(b.String())
	if len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return string(runes)
}

func (s *Service) loadRegistrationGuardPatterns(ctx context.Context) []string {
	value, found, err := s.store.GetSetting(ctx, registrationGuardPatternsKey)
	if err != nil {
		s.logger.Error("registration guard: load custom patterns failed", "err", err.Error())
		return nil
	}
	if !found || value == "" {
		return nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		s.logger.Error("registration guard: decode custom patterns failed", "err", err.Error())
		return nil
	}
	patterns := make([]string, 0, len(raw))
	for _, pattern := range raw {
		normalized, err := normalizeRegistrationGuardPattern(pattern)
		if err != nil || containsRegistrationGuardPattern(patterns, normalized) || isBuiltInRegistrationGuardPattern(normalized) {
			continue
		}
		patterns = append(patterns, normalized)
		if len(patterns) == maxRegistrationGuardPatterns {
			break
		}
	}
	return patterns
}

func (s *Service) customRegistrationGuardPatterns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.registrationGuardPatterns...)
}

func (s *Service) startRegistrationGuardPatternInput(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputRegistrationGuardPattern
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте новый паттерн следующим сообщением. Он будет искаться в имени и нике пользователя без учёта регистра."))
}

func (s *Service) consumeRegistrationGuardPattern(ctx context.Context, chatID int64, text string) bool {
	added, err := s.addRegistrationGuardPattern(ctx, chatID, text)
	switch {
	case errors.Is(err, errRegistrationGuardPatternInvalid):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Паттерн должен содержать от 2 до 64 символов без переносов строк."))
		return true
	case errors.Is(err, errRegistrationGuardPatternLimit):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Можно добавить не более 32 своих паттернов."))
		return true
	case err != nil:
		s.logger.Error("registration guard: add custom pattern failed", "err", err.Error())
		s.mu.Lock()
		delete(s.adminInput, chatID)
		s.mu.Unlock()
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось сохранить паттерн."))
		return true
	case !added:
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Такой паттерн уже есть."))
		return true
	}
	normalized, _ := normalizeRegistrationGuardPattern(text)
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Паттерн сохранён: %s"), normalized))
	return true
}

func (s *Service) addRegistrationGuardPattern(ctx context.Context, adminID int64, raw string) (bool, error) {
	if !s.isAdmin(adminID) {
		return false, ErrNotAdmin
	}
	pattern, err := normalizeRegistrationGuardPattern(raw)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isBuiltInRegistrationGuardPattern(pattern) || containsRegistrationGuardPattern(s.registrationGuardPatterns, pattern) {
		return false, nil
	}
	if len(s.registrationGuardPatterns) >= maxRegistrationGuardPatterns {
		return false, errRegistrationGuardPatternLimit
	}
	updated := append(append([]string(nil), s.registrationGuardPatterns...), pattern)
	encoded, err := json.Marshal(updated)
	if err != nil {
		return false, fmt.Errorf("encode custom registration patterns: %w", err)
	}
	if err := s.store.UpsertSetting(ctx, registrationGuardPatternsKey, string(encoded)); err != nil {
		return false, fmt.Errorf("save custom registration patterns: %w", err)
	}
	s.registrationGuardPatterns = updated
	return true, nil
}

// AdminAddRegistrationGuardPattern saves a custom guard pattern from the Mini
// App. It reuses exactly the same normalization and persistence as the bot
// admin flow.
func (s *Service) AdminAddRegistrationGuardPattern(ctx context.Context, adminID int64, raw string) error {
	added, err := s.addRegistrationGuardPattern(ctx, adminID, raw)
	if errors.Is(err, errRegistrationGuardPatternInvalid) || errors.Is(err, errRegistrationGuardPatternLimit) {
		return ErrBadInput
	}
	if err != nil {
		return err
	}
	if !added {
		return ErrRegistrationGuardPatternExists
	}
	return nil
}

// AdminDeleteRegistrationGuardPattern removes one custom pattern by value.
// Values are normalized before matching so the web and bot inputs behave the
// same way without relying on a mutable list index.
func (s *Service) AdminDeleteRegistrationGuardPattern(ctx context.Context, adminID int64, raw string) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	pattern, err := normalizeRegistrationGuardPattern(raw)
	if err != nil {
		return ErrBadInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, candidate := range s.registrationGuardPatterns {
		if candidate == pattern {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrRegistrationGuardPatternNotFound
	}
	updated := append([]string(nil), s.registrationGuardPatterns[:index]...)
	updated = append(updated, s.registrationGuardPatterns[index+1:]...)
	encoded, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("encode custom registration patterns: %w", err)
	}
	if err := s.store.UpsertSetting(ctx, registrationGuardPatternsKey, string(encoded)); err != nil {
		return fmt.Errorf("save custom registration patterns: %w", err)
	}
	s.registrationGuardPatterns = updated
	return nil
}

// AdminUnblockRegistration removes an active automatic block for a Mini App
// administrator. A missing or already-resolved guard is reported explicitly so
// a stale browser view cannot look like a successful state transition.
func (s *Service) AdminUnblockRegistration(ctx context.Context, adminID, telegramID int64) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	if telegramID <= 0 {
		return ErrBadInput
	}
	changed, err := s.store.UnblockRegistration(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("unblock registration guard: %w", err)
	}
	if !changed {
		return ErrRegistrationGuardNotFound
	}
	return nil
}

func (s *Service) sendRegistrationGuardPatternSettings(ctx context.Context, chatID int64) {
	patterns := s.customRegistrationGuardPatterns()
	var text strings.Builder
	text.WriteString(i18n.T("🛡 Антискам-паттерны"))
	text.WriteString(i18n.T("\n\nВстроенные: support, саппорт, поддержка, admin, админ, verif, official, moder, security, refund и имя бота."))
	if len(patterns) == 0 {
		text.WriteString(i18n.T("\n\nСвои паттерны не заданы."))
	} else {
		text.WriteString(i18n.T("\n\nСвои паттерны:\n"))
		for _, pattern := range patterns {
			text.WriteString("• " + pattern + "\n")
		}
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(patterns)+2)
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("➕ Добавить паттерн"), CallbackData: "adm:guard:add"}})
	for i, pattern := range patterns {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text: i18n.T("❌ Удалить: ") + pattern, CallbackData: fmt.Sprintf("adm:guard:del:%d", i),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(text.String(), "\n"), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) handleRegistrationGuardPatternDelete(ctx context.Context, chatID int64, data string) {
	index, err := strconv.Atoi(strings.TrimPrefix(data, "adm:guard:del:"))
	if err != nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Паттерн не найден."))
		return
	}
	deleted, err := s.deleteRegistrationGuardPattern(ctx, chatID, index)
	if err != nil {
		s.logger.Error("registration guard: delete custom pattern failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось сохранить паттерн."))
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Паттерн не найден."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Паттерн удалён."))
	s.sendRegistrationGuardPatternSettings(ctx, chatID)
}

func (s *Service) deleteRegistrationGuardPattern(ctx context.Context, adminID int64, index int) (bool, error) {
	if !s.isAdmin(adminID) {
		return false, ErrNotAdmin
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.registrationGuardPatterns) {
		return false, nil
	}
	updated := append([]string(nil), s.registrationGuardPatterns[:index]...)
	updated = append(updated, s.registrationGuardPatterns[index+1:]...)
	encoded, err := json.Marshal(updated)
	if err != nil {
		return false, fmt.Errorf("encode custom registration patterns: %w", err)
	}
	if err := s.store.UpsertSetting(ctx, registrationGuardPatternsKey, string(encoded)); err != nil {
		return false, fmt.Errorf("save custom registration patterns: %w", err)
	}
	s.registrationGuardPatterns = updated
	return true, nil
}

func normalizeRegistrationGuardPattern(raw string) (string, error) {
	pattern := strings.ToLower(strings.TrimSpace(raw))
	runes := []rune(pattern)
	if len(runes) < 2 || len(runes) > maxRegistrationGuardPatternRunes {
		return "", errRegistrationGuardPatternInvalid
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return "", errRegistrationGuardPatternInvalid
		}
	}
	return pattern, nil
}

func isBuiltInRegistrationGuardPattern(pattern string) bool {
	for _, builtin := range suspiciousRegistrationPatterns {
		if pattern == builtin {
			return true
		}
	}
	return false
}

func containsRegistrationGuardPattern(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if pattern == target {
			return true
		}
	}
	return false
}
