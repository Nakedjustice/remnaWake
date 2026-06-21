package payments

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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

// RegisterProfile resolves a profile exactly like the bot registration flow
// and links it immediately for the authenticated Mini App user.
func (s *Service) RegisterProfile(ctx context.Context, telegramID int64, query string) (*WebRegistrationResult, error) {
	if s.registrar == nil {
		return nil, ErrPaymentsDisabled
	}
	query = strings.TrimSpace(query)
	var sub *Subscriber
	var err error
	if short, isLink := extractShortUUID(query); isLink {
		if short == "" {
			return nil, ErrInvalidProfileQuery
		}
		sub, err = s.finder.FindByShortUUID(ctx, short)
	} else {
		if !isValidUsername(query) {
			return nil, ErrInvalidProfileQuery
		}
		sub, err = s.finder.FindByUsername(ctx, query)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: resolve profile: %v", ErrPanelUnavailable, err)
	}
	if sub == nil {
		return nil, ErrProfileUnknown
	}
	if sub.TelegramID != 0 {
		if sub.TelegramID == telegramID {
			return &WebRegistrationResult{Username: sub.Username}, nil
		}
		return nil, ErrProfileLinkedElsewhere
	}
	if err := s.linkResolvedProfile(ctx, sub.UUID, telegramID); err != nil {
		return nil, err
	}
	return &WebRegistrationResult{Username: sub.Username}, nil
}

func (s *Service) linkResolvedProfile(ctx context.Context, uuid string, telegramID int64) error {
	if s.dryRun {
		return nil
	}
	if err := s.registrar.SetTelegramID(ctx, uuid, telegramID); err != nil {
		return fmt.Errorf("%w: link profile: %v", ErrPanelUnavailable, err)
	}
	return nil
}

// StartRegisterFlow handles /register. Returns true if the message was consumed.
func (s *Service) StartRegisterFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() || s.registrar == nil {
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
		i18n.T("Отправьте ссылку на вашу подписку или имя профиля (можно посмотреть в приложении). /cancel — отмена."))
}

// handleMenuRegister starts the register flow from the menu button.
func (s *Service) handleMenuRegister(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginRegisterFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleRegisterUsernameInput processes free-text input during an active
// register flow: a subscription link or a profile username. Returns true if
// the message was consumed.
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
			i18n.T("Отправьте ссылку на подписку или имя профиля, либо /cancel для отмены."))
		return true
	}

	if shortUUID, isLink := extractShortUUID(text); isLink {
		if shortUUID == "" {
			_ = s.bot.SendPlain(ctx, chatID,
				i18n.T("Не удалось распознать ссылку. Пришлите ссылку на подписку целиком, как она указана в приложении."))
			return true
		}
		sub, err := s.finder.FindByShortUUID(ctx, shortUUID)
		if err != nil {
			s.logger.Error("register: find by short uuid failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
			return true
		}
		if sub == nil {
			_ = s.bot.SendPlain(ctx, chatID,
				i18n.T("Профиль по этой ссылке не найден. Проверьте ссылку и попробуйте ещё раз."))
			return true
		}
		s.continueRegisterWith(ctx, chatID, r, sub)
		return true
	}

	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов."))
		return true
	}

	sub, err := s.finder.FindByUsername(ctx, text)
	if err != nil {
		s.logger.Error("register: find by username failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if sub == nil {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Профиль с таким именем не найден. Попробуйте ещё раз."))
		return true
	}

	s.continueRegisterWith(ctx, chatID, r, sub)
	return true
}

// handleBareSubscriptionLink links an account when the user simply pastes their
// subscription link into the chat with no flow active — no /register required.
// Returns true if the message was consumed.
func (s *Service) handleBareSubscriptionLink(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() || s.registrar == nil {
		return false
	}
	shortUUID, isLink := extractShortUUID(strings.TrimSpace(m.Text))
	if !isLink {
		return false
	}
	chatID := m.Chat.ID
	if shortUUID == "" {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Не удалось распознать ссылку. Пришлите ссылку на подписку целиком, как она указана в приложении."))
		return true
	}
	sub, err := s.finder.FindByShortUUID(ctx, shortUUID)
	if err != nil {
		s.logger.Error("register: find by short uuid failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if sub == nil {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Профиль по этой ссылке не найден. Проверьте ссылку и попробуйте ещё раз."))
		return true
	}
	r := &registerState{requesterTGID: chatID, createdAt: s.now()}
	s.setRegister(chatID, r)
	s.continueRegisterWith(ctx, chatID, r, sub)
	return true
}

// continueRegisterWith applies the already-linked checks to a resolved
// subscriber and shows the confirmation keyboard.
func (s *Service) continueRegisterWith(ctx context.Context, chatID int64, r *registerState, sub *Subscriber) {
	// Account already linked: idempotent if it's this user, refuse otherwise.
	if sub.TelegramID != 0 {
		s.clearRegister(chatID)
		if sub.TelegramID == r.requesterTGID {
			_ = s.bot.SendPlain(ctx, chatID,
				i18n.T("Этот профиль уже привязан к вашему Telegram."))
		} else {
			_ = s.bot.SendPlain(ctx, chatID,
				i18n.T("Этот профиль уже привязан к другому Telegram. Обратитесь к администратору."))
		}
		return
	}

	r.username = sub.Username
	r.uuid = sub.UUID
	s.setRegister(chatID, r)
	s.showRegisterConfirm(ctx, chatID, r)
}

// extractShortUUID reports whether text looks like an http(s) link and, if so,
// returns the short UUID terminating it (the last path segment of a Remnawave
// subscription link). An empty short UUID with isLink=true means the text is a
// link but no plausible short UUID could be extracted.
func extractShortUUID(text string) (shortUUID string, isLink bool) {
	lower := strings.ToLower(text)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", false
	}
	u, err := url.Parse(text)
	if err != nil {
		return "", true
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	last := segments[len(segments)-1]
	if !isValidShortUUID(last) {
		return "", true
	}
	return last, true
}

// isValidShortUUID accepts the nanoid-style identifiers Remnawave puts at the
// end of subscription links: 4-64 chars, letters/digits/«-»/«_».
func isValidShortUUID(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func (s *Service) showRegisterConfirm(ctx context.Context, chatID int64, r *registerState) {
	text := fmt.Sprintf(i18n.T("Привязать ваш Telegram к профилю «%s»?"), r.username)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("Привязать"), CallbackData: "reg_confirm"}},
			{{Text: i18n.T("Отмена"), CallbackData: "reg_cancel"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleRegisterConfirm processes the "Привязать" button press.
func (s *Service) handleRegisterConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}
	chatID := cb.Message.Chat.ID
	r := s.getRegister(chatID)
	if r == nil || r.username == "" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Сессия истекла. Запустите /register заново."))
		return true
	}

	username := r.username
	uuid := r.uuid
	tgID := r.requesterTGID

	if s.dryRun {
		s.logger.Info("dry-run: would set telegram id", "username", username, "uuid", uuid, "telegram_id", tgID)
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Готово (dry-run)."))
		_ = s.bot.SendPlain(ctx, chatID,
			fmt.Sprintf(i18n.T("✅ Готово! Ваш Telegram привязан к профилю «%s» (dry-run)."), username))
		return true
	}

	if err := s.linkResolvedProfile(ctx, uuid, tgID); err != nil {
		s.logger.Error("register: set telegram id failed", "uuid", uuid, "err", err.Error())
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка привязки. Попробуйте позже."))
		return true
	}

	s.clearRegister(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Привязано!"))
	_ = s.bot.SendPlain(ctx, chatID,
		fmt.Sprintf(i18n.T("✅ Готово! Ваш Telegram привязан к профилю «%s»."), username))
	return true
}

// handleRegisterCancel processes the "Отмена" button shown during confirmation.
func (s *Service) handleRegisterCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearRegister(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Отменено."))
	return true
}
