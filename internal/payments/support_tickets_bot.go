package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// The bot ticket surface mirrors the mini app one: both call the same exported
// Support*Ticket* service methods, so a deployment without WEBAPP_URL loses no
// operation. The pre-ticket typing session (supportSessions) keeps working
// alongside it and always rides the user's newest open ticket; these buttons
// are the way to reach any *other* ticket.

const (
	// ticketBotMaxTickets caps how many tickets one list message renders, so the
	// text and the keyboard stay well inside Telegram's limits.
	ticketBotMaxTickets = 10
	// ticketBotMaxMessages caps how many messages of a thread are rendered; the
	// newest are kept because that is where the conversation continues.
	ticketBotMaxMessages = 15
	// ticketBotSubjectRunes / ticketBotPreviewRunes / ticketBotMessageRunes cap
	// button labels, list previews and single messages. Support text is Russian,
	// so every cut is by rune, never by byte.
	ticketBotSubjectRunes = 28
	ticketBotPreviewRunes = 48
	ticketBotMessageRunes = 400
)

// supportComposeState is the target of the user's next chat message after they
// tapped "new ticket" (ticketID == 0) or a ticket's reply button. The id lives
// here instead of in the callback payload, which Telegram caps at 64 bytes.
type supportComposeState struct {
	ticketID  int64
	createdAt time.Time
}

// supportComposeTTL abandons a compose step the user never answered, matching
// the other conversation states.
const supportComposeTTL = 10 * time.Minute

func (s *Service) setSupportCompose(chatID, ticketID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.supportCompose[chatID] = &supportComposeState{ticketID: ticketID, createdAt: s.now()}
}

// hasSupportCompose reports whether a live (non-expired) compose step is armed.
func (s *Service) hasSupportCompose(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveSupportCompose(chatID) != nil
}

// takeSupportCompose consumes the compose step: it is one-shot, so the next
// plain message goes back to the ordinary routing.
func (s *Service) takeSupportCompose(chatID int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.liveSupportCompose(chatID)
	if st == nil {
		return 0, false
	}
	delete(s.supportCompose, chatID)
	return st.ticketID, true
}

func (s *Service) clearSupportCompose(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.supportCompose, chatID)
}

// liveSupportCompose returns the entry for chatID, evicting it when expired.
// Callers must hold s.mu.
func (s *Service) liveSupportCompose(chatID int64) *supportComposeState {
	st := s.supportCompose[chatID]
	if st == nil {
		return nil
	}
	if s.now().Sub(st.createdAt) > supportComposeTTL {
		delete(s.supportCompose, chatID)
		return nil
	}
	return st
}

// ticketShort trims s to n runes for a button label or a preview line.
func ticketShort(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// parseTicketID reads the ticket id out of a callback payload.
func parseTicketID(data, prefix string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(data, prefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// supportTicketChatID is the chat a ticket keyboard was pressed in. A callback
// can arrive without its message (inaccessible/too old); ticket buttons live in
// the user's private chat, so the presser's id is the same chat.
func supportTicketChatID(cb *tg.CallbackQuery) int64 {
	if cb.Message != nil {
		return cb.Message.Chat.ID
	}
	return cb.From.ID
}

// sendSupportTicketError reports a ticket failure in the bot with the same
// meaning the mini app gives it over HTTP. ErrTicketNotFound deliberately reads
// the same for a foreign ticket and a missing one.
func (s *Service) sendSupportTicketError(ctx context.Context, chatID int64, err error, action string) {
	switch {
	case errors.Is(err, ErrTicketNotFound):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
	case errors.Is(err, ErrTicketClosed):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение уже закрыто."))
	case errors.Is(err, ErrPaymentsDisabled):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Поддержка сейчас недоступна."))
	case errors.Is(err, ErrNotAdmin):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Недостаточно прав."))
	case errors.Is(err, ErrBadInput):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сообщение не может быть пустым."))
	default:
		s.logger.Error("support: "+action+" failed", "err", err.Error(), "chat_id", chatID)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
	}
}

// --- user surface ---

// handleSupportTicketList renders the user's own tickets (sup:list).
func (s *Service) handleSupportTicketList(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	s.sendSupportTicketList(ctx, supportTicketChatID(cb), cb.From.ID)
	return true
}

func (s *Service) sendSupportTicketList(ctx context.Context, chatID, telegramID int64) {
	list, err := s.SupportTicketsUser(ctx, telegramID)
	if err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "list tickets")
		return
	}
	tickets := list.Tickets
	if len(tickets) > ticketBotMaxTickets {
		tickets = tickets[:ticketBotMaxTickets]
	}
	var b strings.Builder
	b.WriteString(i18n.T("🗂 Мои обращения"))
	if len(tickets) == 0 {
		b.WriteString("\n\n" + i18n.T("У вас пока нет обращений. Опишите проблему — мы ответим здесь."))
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(tickets)+1)
	for _, tk := range tickets {
		b.WriteString("\n\n" + ticketHeadline(tk))
		if tk.LastText != "" {
			b.WriteString("\n" + strings.TrimSpace(tk.LastAt+" · "+ticketShort(tk.LastText, ticketBotPreviewRunes)))
		}
		if tk.Unread > 0 {
			b.WriteString(fmt.Sprintf(i18n.T(" · новых: %d"), tk.Unread))
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         ticketButtonLabel(tk),
			CallbackData: fmt.Sprintf("sup:t:%d", tk.ID),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: i18n.T("✍️ Новое обращение"), CallbackData: "sup:new",
	}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// ticketHeadline is the "№12 · Открыто · Тема" line shared by both list views.
func ticketHeadline(tk WebSupportTicket) string {
	line := fmt.Sprintf("№%d · %s", tk.ID, tk.StatusLabel)
	if tk.Subject != "" {
		line += " · " + tk.Subject
	}
	return line
}

func ticketButtonLabel(tk WebSupportTicket) string {
	label := fmt.Sprintf("№%d · %s", tk.ID, tk.StatusLabel)
	if tk.Subject != "" {
		label += " · " + ticketShort(tk.Subject, ticketBotSubjectRunes)
	}
	return label
}

// handleSupportTicketOpen shows one of the user's tickets (sup:t:<id>).
func (s *Service) handleSupportTicketOpen(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	chatID := supportTicketChatID(cb)
	id, ok := parseTicketID(cb.Data, "sup:t:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return true
	}
	thread, err := s.SupportTicketThreadUser(ctx, cb.From.ID, id)
	if err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "open ticket")
		return true
	}
	rows := make([][]tg.InlineKeyboardButton, 0, 2)
	if thread.Ticket.Status != "closed" {
		rows = append(rows, []tg.InlineKeyboardButton{
			{Text: i18n.T("✍️ Ответить"), CallbackData: fmt.Sprintf("sup:re:%d", id)},
			{Text: i18n.T("✖️ Закрыть обращение"), CallbackData: fmt.Sprintf("sup:cl:%d", id)},
		})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← К списку"), CallbackData: "sup:list"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, renderTicketThread(thread, false),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	return true
}

// renderTicketThread prints a ticket header plus its (tail of) history.
func renderTicketThread(thread *WebSupportThread, admin bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(i18n.T("🗂 Обращение №%d · %s"), thread.Ticket.ID, thread.Ticket.StatusLabel))
	if thread.Ticket.Subject != "" {
		b.WriteString("\n" + fmt.Sprintf(i18n.T("Тема: %s"), thread.Ticket.Subject))
	}
	if admin {
		who := fmt.Sprintf("TG %d", thread.Ticket.UserID)
		if thread.Ticket.Username != "" {
			who = fmt.Sprintf("%s (TG %d)", thread.Ticket.Username, thread.Ticket.UserID)
		}
		b.WriteString("\n" + fmt.Sprintf(i18n.T("Пользователь: %s"), who))
	}
	msgs := thread.Messages
	if len(msgs) > ticketBotMaxMessages {
		b.WriteString("\n\n" + fmt.Sprintf(i18n.T("Показаны последние %d сообщений."), ticketBotMaxMessages))
		msgs = msgs[len(msgs)-ticketBotMaxMessages:]
	}
	for _, m := range msgs {
		who := "👤"
		if m.FromAdmin {
			who = "🛟"
		}
		b.WriteString("\n\n" + strings.TrimSpace(who+" "+m.CreatedAt) + "\n" + ticketShort(m.Text, ticketBotMessageRunes))
	}
	return b.String()
}

// handleSupportTicketNew arms the compose step for a brand-new ticket (sup:new).
func (s *Service) handleSupportTicketNew(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	s.setSupportCompose(cb.From.ID, 0)
	_ = s.bot.SendPlain(ctx, supportTicketChatID(cb),
		i18n.T("Опишите проблему одним сообщением (или /cancel для отмены):"))
	return true
}

// handleSupportTicketReplyStart arms the compose step for one ticket
// (sup:re:<id>). Ownership is resolved through userTicket, so a foreign ticket
// never even arms the step and reads exactly like a missing one.
func (s *Service) handleSupportTicketReplyStart(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	chatID := supportTicketChatID(cb)
	id, ok := parseTicketID(cb.Data, "sup:re:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return true
	}
	tk, err := s.userTicket(ctx, cb.From.ID, id)
	if err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "start ticket reply")
		return true
	}
	if tk.Status == "closed" {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение уже закрыто."))
		return true
	}
	s.setSupportCompose(cb.From.ID, tk.ID)
	_ = s.bot.SendPlain(ctx, chatID,
		fmt.Sprintf(i18n.T("Напишите сообщение для обращения №%d (или /cancel для отмены):"), tk.ID))
	return true
}

// handleSupportTicketClose closes one of the user's tickets (sup:cl:<id>).
func (s *Service) handleSupportTicketClose(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	chatID := supportTicketChatID(cb)
	id, ok := parseTicketID(cb.Data, "sup:cl:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return true
	}
	if err := s.SupportCloseTicketUser(ctx, cb.From.ID, id); err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "close ticket")
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("✅ Обращение №%d закрыто."), id))
	s.sendSupportTicketList(ctx, chatID, cb.From.ID)
	return true
}

// handleSupportComposeInput routes a plain chat message to the ticket the user
// chose in the list. It runs before the legacy typing session so an explicit
// target always wins over "the newest open ticket". Returns true only when it
// handled the message.
func (s *Service) handleSupportComposeInput(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.hasSupportCompose(m.Chat.ID) {
		return false
	}
	text := strings.TrimSpace(m.Text)
	// Commands keep working while a compose step waits (mirrors admin input).
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	chatID := m.Chat.ID
	ticketID, ok := s.takeSupportCompose(chatID)
	if !ok {
		return false
	}
	if ticketID == 0 {
		id, err := s.SupportCreateTicket(ctx, chatID, "", text)
		if err != nil {
			s.sendSupportTicketError(ctx, chatID, err, "create ticket")
			return true
		}
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("✅ Обращение №%d создано. Мы ответим здесь."), id))
		return true
	}
	if err := s.SupportSendUserToTicket(ctx, chatID, ticketID, text); err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "reply to ticket")
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("✅ Сообщение отправлено в обращение №%d."), ticketID))
	return true
}

// --- admin surface (reached through handleAdminMenu, which enforces isAdmin) ---

// sendAdminTicketInbox lists every open ticket plus the recently closed ones.
func (s *Service) sendAdminTicketInbox(ctx context.Context, chatID int64) {
	tickets, err := s.SupportTicketsAdmin(ctx, chatID)
	if err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "list admin tickets")
		return
	}
	if len(tickets) > ticketBotMaxTickets {
		tickets = tickets[:ticketBotMaxTickets]
	}
	var b strings.Builder
	b.WriteString(i18n.T("🛟 Обращения пользователей"))
	if len(tickets) == 0 {
		b.WriteString("\n\n" + i18n.T("Обращений пока нет."))
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(tickets)+1)
	for _, tk := range tickets {
		who := fmt.Sprintf("TG %d", tk.UserID)
		if tk.Username != "" {
			who = tk.Username
		}
		b.WriteString("\n\n" + ticketHeadline(tk) + "\n" + who)
		if tk.LastText != "" {
			b.WriteString(" · " + ticketShort(tk.LastText, ticketBotPreviewRunes))
		}
		if tk.Unread > 0 {
			b.WriteString(fmt.Sprintf(i18n.T(" · новых: %d"), tk.Unread))
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf("№%d · %s", tk.ID, ticketShort(who, ticketBotSubjectRunes)),
			CallbackData: fmt.Sprintf("adm:sup:t:%d", tk.ID),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, b.String(), &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// sendAdminTicketThread shows one ticket with its history (adm:sup:t:<id>).
func (s *Service) sendAdminTicketThread(ctx context.Context, chatID int64, data string) {
	id, ok := parseTicketID(data, "adm:sup:t:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return
	}
	thread, err := s.SupportTicketThreadAdmin(ctx, chatID, id)
	if err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "open admin ticket")
		return
	}
	rows := make([][]tg.InlineKeyboardButton, 0, 2)
	if thread.Ticket.Status != "closed" {
		rows = append(rows, []tg.InlineKeyboardButton{
			{Text: i18n.T("✍️ Ответить"), CallbackData: fmt.Sprintf("adm:sup:re:%d", id)},
			{Text: i18n.T("✖️ Закрыть обращение"), CallbackData: fmt.Sprintf("adm:sup:cl:%d", id)},
		})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← К обращениям"), CallbackData: "adm:sup"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, renderTicketThread(thread, true),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// startAdminTicketReply arms the free-text step for one ticket
// (adm:sup:re:<id>); the target is kept in adminInputState, not in the payload.
func (s *Service) startAdminTicketReply(ctx context.Context, chatID int64, data string) {
	id, ok := parseTicketID(data, "adm:sup:re:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return
	}
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputSupportTicketReply
	state.supportTicket = id
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID,
		fmt.Sprintf(i18n.T("Введите ответ по обращению №%d следующим сообщением (или /cancel для отмены):"), id))
}

// consumeSupportTicketReply delivers the admin's typed reply into the ticket.
func (s *Service) consumeSupportTicketReply(ctx context.Context, chatID int64, text string) bool {
	s.mu.Lock()
	ticketID := s.adminInput[chatID].supportTicket
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if ticketID == 0 {
		return true
	}
	if err := s.SupportReplyTicketAdmin(ctx, chatID, ticketID, text); err != nil {
		switch {
		case errors.Is(err, ErrTicketNotFound), errors.Is(err, ErrTicketClosed), errors.Is(err, ErrBadInput):
			s.sendSupportTicketError(ctx, chatID, err, "admin ticket reply")
		default:
			s.logger.Error("support: admin ticket reply failed", "err", err.Error(), "admin_id", chatID, "ticket_id", ticketID)
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось отправить ответ."))
		}
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("✅ Ответ отправлен."))
	return true
}

// handleAdminTicketClose closes a ticket from the inbox (adm:sup:cl:<id>).
func (s *Service) handleAdminTicketClose(ctx context.Context, chatID int64, data string) {
	id, ok := parseTicketID(data, "adm:sup:cl:")
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Обращение не найдено."))
		return
	}
	if err := s.SupportCloseTicketAdmin(ctx, chatID, id); err != nil {
		s.sendSupportTicketError(ctx, chatID, err, "close admin ticket")
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("✅ Обращение №%d закрыто."), id))
	s.sendAdminTicketInbox(ctx, chatID)
}
