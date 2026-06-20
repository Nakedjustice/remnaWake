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

// supportSendDelay paces the per-admin fan-out of a support message, matching
// the broadcast pacing so a burst of admins stays under Telegram's limits.
const supportSendDelay = 50 * time.Millisecond

// WebSupportMessage is one line of a support conversation for the mini app.
type WebSupportMessage struct {
	FromAdmin bool   `json:"from_admin"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"` // DD.MM.YYYY HH:MM
}

// WebSupport is the full thread payload returned to the user (own thread) or to
// an admin (a single conversation).
type WebSupport struct {
	Open     bool                `json:"open"`
	Messages []WebSupportMessage `json:"messages,omitempty"`
}

// WebSupportConversation is one entry in the admin support conversation list.
type WebSupportConversation struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username,omitempty"`
	LastText string `json:"last_text,omitempty"`
	LastAt   string `json:"last_at"` // DD.MM.YYYY HH:MM
	Unread   int    `json:"unread"`
}

// supportTimeLabel formats a timestamp for the mini app conversation views.
func supportTimeLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("02.01.2006 15:04")
}

// supportUsername returns a best-effort display name for a user's support
// conversation, looked up from the panel by Telegram ID. Empty on miss/error.
func (s *Service) supportUsername(ctx context.Context, telegramID int64) string {
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil || len(subs) == 0 {
		return ""
	}
	return subs[0].Username
}

func (s *Service) inSupportSession(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.supportSessions[chatID]
}

func (s *Service) setSupportSession(chatID int64, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.supportSessions[chatID] = true
	} else {
		delete(s.supportSessions, chatID)
	}
}

// supportCloseKeyboard is the inline "close chat" button shown to the user.
func supportCloseKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✖️ Завершить чат"), CallbackData: "sup:close"},
		}},
	}
}

// StartSupport opens the support typing session in the bot: subsequent plain
// text from the user is forwarded to the admins until /cancel or close.
func (s *Service) StartSupport(ctx context.Context, chatID int64) bool {
	if !s.isEnabled() {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Поддержка сейчас недоступна."))
		return true
	}
	s.setSupportSession(chatID, true)
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("💬 Вы открыли чат с поддержкой. Напишите ваше сообщение — оно будет передано администратору. Отправьте /cancel или нажмите кнопку ниже, чтобы завершить чат."),
		supportCloseKeyboard())
	return true
}

// handleSupportSessionInput forwards a user's free text to support when the bot
// support session is active. Returns true only when it handled the message.
func (s *Service) handleSupportSessionInput(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.inSupportSession(m.Chat.ID) {
		return false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return false
	}
	if err := s.SupportSendUser(ctx, m.Chat.ID, text); err != nil {
		s.logger.Error("support: send user message failed", "err", err.Error(), "telegram_id", m.Chat.ID)
		_ = s.bot.SendPlain(ctx, m.Chat.ID, i18n.T("Не удалось отправить сообщение, попробуйте позже."))
	}
	return true
}

// SupportSendUser stores a user's support message and fans it out to every
// admin with an inline reply button. Used by both the bot session and the mini
// app. Send failures to individual admins are logged but not fatal.
func (s *Service) SupportSendUser(ctx context.Context, telegramID int64, text string) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrBadInput
	}
	username := s.supportUsername(ctx, telegramID)
	if _, err := s.store.AddSupportMessage(ctx, store.SupportMessage{
		UserTelegramID: telegramID,
		UserUsername:   username,
		Text:           text,
	}); err != nil {
		return fmt.Errorf("store support message: %w", err)
	}
	s.notifyAdminsSupport(ctx, telegramID, username, text)
	return nil
}

// notifyAdminsSupport sends a support message to every admin with a reply
// button targeting the user's conversation.
func (s *Service) notifyAdminsSupport(ctx context.Context, userTelegramID int64, username, text string) {
	who := fmt.Sprintf("TG %d", userTelegramID)
	if username != "" {
		who = fmt.Sprintf("%s (TG %d)", username, userTelegramID)
	}
	body := fmt.Sprintf(i18n.T("🛟 Сообщение в поддержку от %s:\n\n%s"), who, text)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✍️ Ответить"), CallbackData: fmt.Sprintf("sup:reply:%d", userTelegramID)},
		}},
	}
	for i, adminID := range s.adminIDs {
		if s.dryRun {
			s.logger.Info("dry-run: would notify admin of support message", "admin_id", adminID, "user_telegram_id", userTelegramID)
			continue
		}
		if i > 0 {
			time.Sleep(supportSendDelay)
		}
		if _, err := s.bot.SendPlainWithKeyboard(ctx, adminID, body, kb); err != nil {
			s.logger.Error("support: notify admin failed", "admin_id", adminID, "err", err.Error())
		}
	}
}

// SupportSendAdmin stores an admin reply and delivers it to the user. The
// caller must already be an admin (enforced by the bot session / web guard).
func (s *Service) SupportSendAdmin(ctx context.Context, adminID, targetUserID int64, text string) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrBadInput
	}
	if _, err := s.store.AddSupportMessage(ctx, store.SupportMessage{
		UserTelegramID:   targetUserID,
		FromAdmin:        true,
		AuthorTelegramID: adminID,
		Text:             text,
	}); err != nil {
		return fmt.Errorf("store support reply: %w", err)
	}
	if !s.dryRun {
		_ = s.bot.SendPlain(ctx, targetUserID, fmt.Sprintf(i18n.T("🛟 Поддержка: %s"), text))
	} else {
		s.logger.Info("dry-run: would deliver support reply", "user_telegram_id", targetUserID)
	}
	return nil
}

// SupportHistoryUser returns the user's own conversation and marks the admin
// replies in it as read.
func (s *Service) SupportHistoryUser(ctx context.Context, telegramID int64) (*WebSupport, error) {
	if !s.isEnabled() {
		return nil, ErrPaymentsDisabled
	}
	msgs, err := s.store.ListSupportMessages(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("list support messages: %w", err)
	}
	if err := s.store.MarkSupportReadByUser(ctx, telegramID); err != nil {
		s.logger.Error("support: mark read by user failed", "err", err.Error(), "telegram_id", telegramID)
	}
	return &WebSupport{Open: len(msgs) > 0, Messages: toWebSupportMessages(msgs)}, nil
}

// SupportConversations returns the admin support inbox.
func (s *Service) SupportConversations(ctx context.Context, adminID int64) ([]WebSupportConversation, error) {
	if err := s.adminGuard(adminID); err != nil {
		return nil, err
	}
	convs, err := s.store.ListSupportConversations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list support conversations: %w", err)
	}
	out := make([]WebSupportConversation, 0, len(convs))
	for _, c := range convs {
		out = append(out, WebSupportConversation{
			UserID:   c.UserTelegramID,
			Username: c.UserUsername,
			LastText: c.LastText,
			LastAt:   supportTimeLabel(c.LastAt),
			Unread:   c.Unread,
		})
	}
	return out, nil
}

// SupportThreadAdmin returns one conversation for an admin and marks its user
// messages as read.
func (s *Service) SupportThreadAdmin(ctx context.Context, adminID, targetUserID int64) (*WebSupport, error) {
	if err := s.adminGuard(adminID); err != nil {
		return nil, err
	}
	msgs, err := s.store.ListSupportMessages(ctx, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("list support messages: %w", err)
	}
	if err := s.store.MarkSupportReadByAdmin(ctx, targetUserID); err != nil {
		s.logger.Error("support: mark read by admin failed", "err", err.Error(), "user_telegram_id", targetUserID)
	}
	return &WebSupport{Open: len(msgs) > 0, Messages: toWebSupportMessages(msgs)}, nil
}

// SupportCloseUser closes (deletes) the user's own conversation and notifies
// the admins it was ended.
func (s *Service) SupportCloseUser(ctx context.Context, telegramID int64) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	s.setSupportSession(telegramID, false)
	closed, err := s.store.CloseSupportConversation(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("close support conversation: %w", err)
	}
	if closed && !s.dryRun {
		username := s.supportUsername(ctx, telegramID)
		who := fmt.Sprintf("TG %d", telegramID)
		if username != "" {
			who = fmt.Sprintf("%s (TG %d)", username, telegramID)
		}
		note := fmt.Sprintf(i18n.T("🛟 Пользователь %s завершил чат с поддержкой."), who)
		for _, adminID := range s.adminIDs {
			_ = s.bot.SendPlain(ctx, adminID, note)
		}
	}
	return nil
}

// SupportCloseAdmin closes a conversation from the admin side and tells the
// user the chat was closed.
func (s *Service) SupportCloseAdmin(ctx context.Context, adminID, targetUserID int64) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	s.setSupportSession(targetUserID, false)
	closed, err := s.store.CloseSupportConversation(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("close support conversation: %w", err)
	}
	if closed && !s.dryRun {
		_ = s.bot.SendPlain(ctx, targetUserID, i18n.T("🛟 Чат с поддержкой завершён администратором."))
	}
	return nil
}

// handleMenuSupport opens the support session from the bot menu button.
func (s *Service) handleMenuSupport(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.StartSupport(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleSupportClose handles the user's "close chat" inline button.
func (s *Service) handleSupportClose(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message == nil {
		return true
	}
	chatID := cb.Message.Chat.ID
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	if err := s.SupportCloseUser(ctx, chatID); err != nil {
		s.logger.Error("support: close by user failed", "err", err.Error(), "telegram_id", chatID)
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("💬 Чат с поддержкой завершён."))
	return true
}

// handleSupportReplyStart handles an admin tapping "Reply" on a support
// notification: it arms the admin-input state so the next message is delivered
// to that user.
func (s *Service) handleSupportReplyStart(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message == nil || !s.isAdmin(cb.Message.Chat.ID) {
		return true
	}
	target, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "sup:reply:"), 10, 64)
	if err != nil {
		return true
	}
	chatID := cb.Message.Chat.ID
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputSupportReply
	state.supportTarget = target
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите ответ пользователю следующим сообщением (или /cancel для отмены):"))
	return true
}

// consumeSupportReply delivers an admin's typed reply to the targeted user.
func (s *Service) consumeSupportReply(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	target := s.adminInput[chatID].supportTarget
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if target == 0 {
		return true
	}
	if err := s.SupportSendAdmin(ctx, chatID, target, text); err != nil {
		s.logger.Error("support: admin reply failed", "err", err.Error(), "admin_id", chatID)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось отправить ответ."))
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("✅ Ответ отправлен."))
	return true
}

func toWebSupportMessages(msgs []store.SupportMessage) []WebSupportMessage {
	out := make([]WebSupportMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, WebSupportMessage{
			FromAdmin: m.FromAdmin,
			Text:      m.Text,
			CreatedAt: supportTimeLabel(m.CreatedAt),
		})
	}
	return out
}
