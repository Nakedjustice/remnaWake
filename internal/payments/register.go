package payments

import (
	"context"
	"fmt"
	"strings"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

type registerState struct {
	requesterTGID int64
	username      string // empty = still awaiting username input
	uuid          string // resolved panel UUID once a free account is found
	createdAt     time.Time
}

const registerTTL = 10 * time.Minute

func (s *Service) getRegister(chatID int64) *registerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.registers[chatID]
	if r == nil {
		return nil
	}
	if s.now().Sub(r.createdAt) > registerTTL {
		delete(s.registers, chatID)
		return nil
	}
	return r
}

func (s *Service) setRegister(chatID int64, r *registerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registers[chatID] = r
}

func (s *Service) clearRegister(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registers, chatID)
}

// StartRegisterFlow handles /register. Returns true if the message was consumed.
func (s *Service) StartRegisterFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || s.adminID == 0 || s.registrar == nil {
		return false
	}
	s.beginRegisterFlow(ctx, m.Chat.ID)
	return true
}

func (s *Service) beginRegisterFlow(ctx context.Context, chatID int64) {
	s.setRegister(chatID, &registerState{
		requesterTGID: chatID,
		createdAt:     s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		"Введите имя вашего профила (Можно посмотреть в приложении). /cancel — отмена.")
}

// handleMenuRegister starts the register flow from the menu button.
func (s *Service) handleMenuRegister(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginRegisterFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleRegisterUsernameInput processes free-text input during an active register
// flow. Returns true if the message was consumed.
func (s *Service) handleRegisterUsernameInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	r := s.getRegister(chatID)
	if r == nil {
		return false
	}

	text := strings.TrimSpace(m.Text)

	// If a username is already resolved, the user sent text instead of tapping a
	// button — re-show the confirmation.
	if r.username != "" {
		s.showRegisterConfirm(ctx, chatID, r)
		return true
	}

	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID,
			"Введите имя вашего профила или /cancel для отмены.")
		return true
	}

	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов.")
		return true
	}

	sub, err := s.finder.FindByUsername(ctx, text)
	if err != nil {
		s.logger.Error("register: find by username failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if sub == nil {
		_ = s.bot.SendPlain(ctx, chatID,
			"Профиль с таким именем не найден. Попробуйте ещё раз.")
		return true
	}

	// Account already linked: idempotent if it's this user, refuse otherwise.
	if sub.TelegramID != 0 {
		s.clearRegister(chatID)
		if sub.TelegramID == r.requesterTGID {
			_ = s.bot.SendPlain(ctx, chatID,
				"Этот профиль уже привязан к вашему Telegram.")
		} else {
			_ = s.bot.SendPlain(ctx, chatID,
				"Этот профиль уже привязан к другому Telegram. Обратитесь к администратору.")
		}
		return true
	}

	r.username = sub.Username
	r.uuid = sub.UUID
	s.setRegister(chatID, r)
	s.showRegisterConfirm(ctx, chatID, r)
	return true
}

func (s *Service) showRegisterConfirm(ctx context.Context, chatID int64, r *registerState) {
	text := fmt.Sprintf("Привязать ваш Telegram к профилю «%s»?", r.username)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Привязать", CallbackData: "reg_confirm"}},
			{{Text: "Отмена", CallbackData: "reg_cancel"}},
		},
	}
	_ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleRegisterConfirm processes the "Привязать" button press.
func (s *Service) handleRegisterConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	r := s.getRegister(chatID)
	if r == nil || r.username == "" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /register заново.")
		return true
	}

	username := r.username
	uuid := r.uuid
	tgID := r.requesterTGID

	if s.dryRun {
		s.logger.Info("dry-run: would set telegram id", "username", username, "uuid", uuid, "telegram_id", tgID)
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Готово (dry-run).")
		_ = s.bot.SendPlain(ctx, chatID,
			fmt.Sprintf("✅ Готово! Ваш Telegram привязан к профилю «%s» (dry-run).", username))
		return true
	}

	if err := s.registrar.SetTelegramID(ctx, uuid, tgID); err != nil {
		s.logger.Error("register: set telegram id failed", "uuid", uuid, "err", err.Error())
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка привязки. Попробуйте позже.")
		return true
	}

	s.clearRegister(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Привязано!")
	_ = s.bot.SendPlain(ctx, chatID,
		fmt.Sprintf("✅ Готово! Ваш Telegram привязан к профилю «%s».", username))
	return true
}

// handleRegisterCancel processes the "Отмена" button shown during confirmation.
func (s *Service) handleRegisterCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearRegister(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}
