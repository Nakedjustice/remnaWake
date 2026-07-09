package payments

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// ticketSubjectMaxRunes caps a subject derived from the first message.
const ticketSubjectMaxRunes = 60

// WebSupportTicket is one ticket summary for the mini app lists and threads.
// State is derived, never stored: "closed" for closed tickets, otherwise
// "answered" when the last message came from an admin and "awaiting_reply"
// when the user wrote last (or the ticket has no messages yet).
type WebSupportTicket struct {
	ID          int64  `json:"id"`
	Subject     string `json:"subject,omitempty"`
	Status      string `json:"status"`             // open | closed
	StatusLabel string `json:"status_label"`       // localized
	State       string `json:"state"`              // awaiting_reply | answered | closed
	UserID      int64  `json:"user_id,omitempty"`  // admin views only
	Username    string `json:"username,omitempty"` // admin views only
	CreatedAt   string `json:"created_at"`         // DD.MM.YYYY HH:MM
	LastAt      string `json:"last_at,omitempty"`
	LastText    string `json:"last_text,omitempty"`
	Unread      int    `json:"unread,omitempty"`
}

// WebSupportTickets is the user's ticket list plus the total unread admin
// replies (drives the cabinet badge).
type WebSupportTickets struct {
	Tickets     []WebSupportTicket `json:"tickets,omitempty"`
	UnreadTotal int                `json:"unread_total,omitempty"`
}

// WebSupportThread is one ticket with its full message history.
type WebSupportThread struct {
	Ticket   WebSupportTicket    `json:"ticket"`
	Messages []WebSupportMessage `json:"messages,omitempty"`
}

func ticketStatusLabel(status string) string {
	if status == "closed" {
		return i18n.T("Закрыто")
	}
	return i18n.T("Открыто")
}

func ticketState(status string, lastFromAdmin bool) string {
	if status == "closed" {
		return "closed"
	}
	if lastFromAdmin {
		return "answered"
	}
	return "awaiting_reply"
}

// ticketSubjectFromText derives a default subject from the opening message:
// its first line, capped at ticketSubjectMaxRunes runes (the text is Russian —
// never slice bytes).
func ticketSubjectFromText(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	r := []rune(strings.TrimSpace(text))
	if len(r) > ticketSubjectMaxRunes {
		return string(r[:ticketSubjectMaxRunes]) + "…"
	}
	return string(r)
}

func webSupportTicket(tk store.SupportTicket, admin bool) WebSupportTicket {
	out := WebSupportTicket{
		ID:          tk.ID,
		Subject:     tk.Subject,
		Status:      tk.Status,
		StatusLabel: ticketStatusLabel(tk.Status),
		State:       ticketState(tk.Status, tk.LastFromAdmin),
		CreatedAt:   supportTimeLabel(tk.CreatedAt),
		LastAt:      supportTimeLabel(tk.LastAt),
		LastText:    tk.LastText,
		Unread:      tk.Unread,
	}
	if admin {
		out.UserID = tk.UserTelegramID
		out.Username = tk.UserUsername
	}
	return out
}

// userTicket loads a ticket and enforces ownership, concealing foreign tickets
// as missing (same pattern as CheckPlategaPayment).
func (s *Service) userTicket(ctx context.Context, telegramID, ticketID int64) (*store.SupportTicket, error) {
	tk, err := s.store.GetSupportTicket(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("get support ticket: %w", err)
	}
	if tk == nil || tk.UserTelegramID != telegramID {
		return nil, ErrTicketNotFound
	}
	return tk, nil
}

// SupportTicketsUser returns the user's own tickets, open first.
func (s *Service) SupportTicketsUser(ctx context.Context, telegramID int64) (*WebSupportTickets, error) {
	if !s.isEnabled() {
		return nil, ErrPaymentsDisabled
	}
	tickets, err := s.store.ListSupportTicketsForUser(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("list support tickets: %w", err)
	}
	out := &WebSupportTickets{Tickets: make([]WebSupportTicket, 0, len(tickets))}
	for _, tk := range tickets {
		out.Tickets = append(out.Tickets, webSupportTicket(tk, false))
		out.UnreadTotal += tk.Unread
	}
	return out, nil
}

// SupportTicketThreadUser returns one of the user's tickets with its history
// and marks the admin replies in it as read.
func (s *Service) SupportTicketThreadUser(ctx context.Context, telegramID, ticketID int64) (*WebSupportThread, error) {
	if !s.isEnabled() {
		return nil, ErrPaymentsDisabled
	}
	tk, err := s.userTicket(ctx, telegramID, ticketID)
	if err != nil {
		return nil, err
	}
	return s.ticketThread(ctx, tk, false)
}

// ticketThread builds the thread payload shared by the user and admin views
// and marks the viewer's side as read.
func (s *Service) ticketThread(ctx context.Context, tk *store.SupportTicket, admin bool) (*WebSupportThread, error) {
	msgs, err := s.store.ListSupportMessagesByTicket(ctx, tk.ID)
	if err != nil {
		return nil, fmt.Errorf("list ticket messages: %w", err)
	}
	if admin {
		if err := s.store.MarkTicketReadByAdmin(ctx, tk.ID); err != nil {
			s.logger.Error("support: mark ticket read by admin failed", "err", err.Error(), "ticket_id", tk.ID)
		}
	} else {
		if err := s.store.MarkTicketReadByUser(ctx, tk.ID); err != nil {
			s.logger.Error("support: mark ticket read by user failed", "err", err.Error(), "ticket_id", tk.ID)
		}
	}
	if n := len(msgs); n > 0 {
		tk.LastFromAdmin = msgs[n-1].FromAdmin
		tk.LastText = msgs[n-1].Text
		tk.LastAt = msgs[n-1].CreatedAt
	}
	return &WebSupportThread{Ticket: webSupportTicket(*tk, admin), Messages: toWebSupportMessages(msgs)}, nil
}

// SupportCreateTicket opens a new ticket with its first message and notifies
// the admins. An empty subject is derived from the message text.
func (s *Service) SupportCreateTicket(ctx context.Context, telegramID int64, subject, text string) (int64, error) {
	if !s.isEnabled() {
		return 0, ErrPaymentsDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, ErrBadInput
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = ticketSubjectFromText(text)
	}
	username := s.supportUsername(ctx, telegramID)
	ticketID, err := s.store.CreateSupportTicket(ctx, telegramID, username, subject)
	if err != nil {
		return 0, fmt.Errorf("create support ticket: %w", err)
	}
	if _, err := s.store.AddSupportMessage(ctx, store.SupportMessage{
		TicketID:       ticketID,
		UserTelegramID: telegramID,
		UserUsername:   username,
		Text:           text,
	}); err != nil {
		return 0, fmt.Errorf("store support message: %w", err)
	}
	s.notifyAdminsSupport(ctx, telegramID, username, text)
	return ticketID, nil
}

// SupportSendUserToTicket appends the user's reply to one of their open
// tickets and notifies the admins. Closed tickets reject the reply.
func (s *Service) SupportSendUserToTicket(ctx context.Context, telegramID, ticketID int64, text string) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrBadInput
	}
	tk, err := s.userTicket(ctx, telegramID, ticketID)
	if err != nil {
		return err
	}
	username := s.supportUsername(ctx, telegramID)
	_, ok, err := s.store.AddSupportMessageToOpenTicket(ctx, store.SupportMessage{
		TicketID:       tk.ID,
		UserTelegramID: telegramID,
		UserUsername:   username,
		Text:           text,
	})
	if err != nil {
		return fmt.Errorf("store support message: %w", err)
	}
	if !ok {
		return ErrTicketClosed
	}
	s.notifyAdminsSupport(ctx, telegramID, username, text)
	return nil
}

// SupportCloseTicketUser closes one of the user's tickets and tells the
// admins. Already-closed tickets report ErrTicketClosed.
func (s *Service) SupportCloseTicketUser(ctx context.Context, telegramID, ticketID int64) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	tk, err := s.userTicket(ctx, telegramID, ticketID)
	if err != nil {
		return err
	}
	closed, err := s.store.CloseSupportTicket(ctx, tk.ID, "user", s.now())
	if err != nil {
		return fmt.Errorf("close support ticket: %w", err)
	}
	if !closed {
		return ErrTicketClosed
	}
	s.notifyAdminsSupportClosed(ctx, telegramID)
	return nil
}

// notifyAdminsSupportClosed tells every admin the user ended the conversation
// (same text as the pre-ticket bot close).
func (s *Service) notifyAdminsSupportClosed(ctx context.Context, telegramID int64) {
	if s.dryRun {
		s.logger.Info("dry-run: would notify admins of support close", "user_telegram_id", telegramID)
		return
	}
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

// SupportTicketsAdmin returns the admin inbox: open tickets first, then the
// most recently active closed ones.
func (s *Service) SupportTicketsAdmin(ctx context.Context, adminID int64) ([]WebSupportTicket, error) {
	if err := s.adminGuard(adminID); err != nil {
		return nil, err
	}
	tickets, err := s.store.ListSupportTicketsAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("list support tickets: %w", err)
	}
	out := make([]WebSupportTicket, 0, len(tickets))
	for _, tk := range tickets {
		out = append(out, webSupportTicket(tk, true))
	}
	return out, nil
}

// SupportTicketThreadAdmin returns one ticket with history for an admin and
// marks its user messages as read.
func (s *Service) SupportTicketThreadAdmin(ctx context.Context, adminID, ticketID int64) (*WebSupportThread, error) {
	if err := s.adminGuard(adminID); err != nil {
		return nil, err
	}
	tk, err := s.store.GetSupportTicket(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("get support ticket: %w", err)
	}
	if tk == nil {
		return nil, ErrTicketNotFound
	}
	return s.ticketThread(ctx, tk, true)
}

// SupportReplyTicketAdmin appends an admin reply to a specific open ticket and
// delivers it to the user. Closed tickets reject the reply — unlike the bot's
// SupportSendAdmin, this never creates a new ticket.
func (s *Service) SupportReplyTicketAdmin(ctx context.Context, adminID, ticketID int64, text string) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrBadInput
	}
	tk, err := s.store.GetSupportTicket(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("get support ticket: %w", err)
	}
	if tk == nil {
		return ErrTicketNotFound
	}
	_, ok, err := s.store.AddSupportMessageToOpenTicket(ctx, store.SupportMessage{
		TicketID:         tk.ID,
		UserTelegramID:   tk.UserTelegramID,
		FromAdmin:        true,
		AuthorTelegramID: adminID,
		Text:             text,
	})
	if err != nil {
		return fmt.Errorf("store support reply: %w", err)
	}
	if !ok {
		return ErrTicketClosed
	}
	if s.dryRun {
		s.logger.Info("dry-run: would deliver support reply", "user_telegram_id", tk.UserTelegramID, "ticket_id", tk.ID)
		return nil
	}
	_ = s.bot.SendPlain(ctx, tk.UserTelegramID, fmt.Sprintf(i18n.T("🛟 Поддержка: %s"), text))
	return nil
}

// SupportCloseTicketAdmin closes a ticket from the admin side and tells the
// user (same text as the pre-ticket bot close).
func (s *Service) SupportCloseTicketAdmin(ctx context.Context, adminID, ticketID int64) error {
	if err := s.adminGuard(adminID); err != nil {
		return err
	}
	tk, err := s.store.GetSupportTicket(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("get support ticket: %w", err)
	}
	if tk == nil {
		return ErrTicketNotFound
	}
	closed, err := s.store.CloseSupportTicket(ctx, tk.ID, "admin", s.now())
	if err != nil {
		return fmt.Errorf("close support ticket: %w", err)
	}
	if !closed {
		return ErrTicketClosed
	}
	s.setSupportSession(tk.UserTelegramID, false)
	if s.dryRun {
		s.logger.Info("dry-run: would notify user of support close", "user_telegram_id", tk.UserTelegramID, "ticket_id", tk.ID)
		return nil
	}
	_ = s.bot.SendPlain(ctx, tk.UserTelegramID, i18n.T("🛟 Чат с поддержкой завершён администратором."))
	return nil
}
