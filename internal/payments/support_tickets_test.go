package payments

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSupportSendUserReusesOpenTicket(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	if err := svc.SupportSendUser(ctx, supportUser, "one"); err != nil {
		t.Fatalf("send one: %v", err)
	}
	if err := svc.SupportSendUser(ctx, supportUser, "two"); err != nil {
		t.Fatalf("send two: %v", err)
	}

	tickets, err := svc.store.ListSupportTicketsAdmin(ctx)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("tickets: got %d err %v", len(tickets), err)
	}
	msgs, err := svc.store.ListSupportMessagesByTicket(ctx, tickets[0].ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ticket messages: got %d err %v", len(msgs), err)
	}
}

func TestSupportCloseKeepsHistoryAndNextMessageOpensNewTicket(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	if err := svc.SupportSendUser(ctx, supportUser, "first issue"); err != nil {
		t.Fatalf("send: %v", err)
	}
	first, err := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if err != nil || first == nil {
		t.Fatalf("first ticket: %+v err %v", first, err)
	}

	if err := svc.SupportCloseUser(ctx, supportUser); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := svc.store.GetSupportTicket(ctx, first.ID)
	if err != nil || got == nil || got.Status != "closed" || got.ClosedBy != "user" {
		t.Fatalf("closed ticket: %+v err %v", got, err)
	}
	if msgs, err := svc.store.ListSupportMessagesByTicket(ctx, first.ID); err != nil || len(msgs) != 1 {
		t.Fatalf("history after close: got %d err %v", len(msgs), err)
	}
	// The admins are told the chat ended (existing bot behavior).
	adminMsgs := sentTo(bot, 1000)
	var closeNote bool
	for _, m := range adminMsgs {
		if strings.Contains(m.Text, "завершил чат") {
			closeNote = true
		}
	}
	if !closeNote {
		t.Fatalf("admins not notified of close: %+v", adminMsgs)
	}

	// Closing again with nothing open is a quiet no-op.
	before := len(bot.sent)
	if err := svc.SupportCloseUser(ctx, supportUser); err != nil {
		t.Fatalf("close idle: %v", err)
	}
	if len(bot.sent) != before {
		t.Fatalf("idle close must not notify: %+v", bot.sent[before:])
	}

	// The next message opens a fresh ticket.
	if err := svc.SupportSendUser(ctx, supportUser, "second issue"); err != nil {
		t.Fatalf("send after close: %v", err)
	}
	second, err := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatalf("second ticket: %+v err %v (first %d)", second, err, first.ID)
	}
}

func TestSupportAdminReplyTargetsNewestOpenTicketOrCreates(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	// No open ticket: the admin reply starts an admin-initiated one.
	if err := svc.SupportSendAdmin(ctx, 1000, supportUser, "proactive hello"); err != nil {
		t.Fatalf("admin reply: %v", err)
	}
	tk, err := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if err != nil || tk == nil {
		t.Fatalf("admin-initiated ticket: %+v err %v", tk, err)
	}
	if userMsgs := sentTo(bot, supportUser); len(userMsgs) != 1 || !strings.Contains(userMsgs[0].Text, "proactive hello") {
		t.Fatalf("delivery: %+v", userMsgs)
	}

	// With the ticket open, both sides keep landing in it.
	if err := svc.SupportSendUser(ctx, supportUser, "thanks"); err != nil {
		t.Fatalf("user send: %v", err)
	}
	if err := svc.SupportSendAdmin(ctx, 1000, supportUser, "anytime"); err != nil {
		t.Fatalf("admin send: %v", err)
	}
	tickets, err := svc.store.ListSupportTicketsAdmin(ctx)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("tickets: got %d err %v", len(tickets), err)
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, tk.ID); len(msgs) != 3 {
		t.Fatalf("thread length: %d", len(msgs))
	}
}

func TestSupportCreateTicketAndListUser(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "Не работает VPN уже два дня, помогите разобраться")
	if err != nil || id == 0 {
		t.Fatalf("create: id=%d err=%v", id, err)
	}

	list, err := svc.SupportTicketsUser(ctx, supportUser)
	if err != nil || len(list.Tickets) != 1 {
		t.Fatalf("user tickets: %+v err %v", list, err)
	}
	got := list.Tickets[0]
	if got.ID != id || got.Status != "open" || got.State != "awaiting_reply" {
		t.Fatalf("ticket summary: %+v", got)
	}
	if got.Subject == "" || !strings.HasPrefix(got.Subject, "Не работает VPN") {
		t.Fatalf("derived subject: %q", got.Subject)
	}

	// Explicit subject wins over derivation.
	id2, err := svc.SupportCreateTicket(ctx, supportUser, "Оплата", "не прошла оплата")
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	list, _ = svc.SupportTicketsUser(ctx, supportUser)
	for _, tk := range list.Tickets {
		if tk.ID == id2 && tk.Subject != "Оплата" {
			t.Fatalf("explicit subject: %+v", tk)
		}
	}

	// An admin reply flips the state and bumps the unread counters.
	if err := svc.SupportReplyTicketAdmin(ctx, 1000, id, "смотрим"); err != nil {
		t.Fatalf("admin reply: %v", err)
	}
	list, _ = svc.SupportTicketsUser(ctx, supportUser)
	var replied *WebSupportTicket
	for i := range list.Tickets {
		if list.Tickets[i].ID == id {
			replied = &list.Tickets[i]
		}
	}
	if replied == nil || replied.State != "answered" || replied.Unread != 1 {
		t.Fatalf("after reply: %+v", replied)
	}
	if list.UnreadTotal != 1 {
		t.Fatalf("unread total: %d", list.UnreadTotal)
	}

	// Reading the thread returns the messages and clears the unread.
	thread, err := svc.SupportTicketThreadUser(ctx, supportUser, id)
	if err != nil || len(thread.Messages) != 2 || thread.Ticket.ID != id {
		t.Fatalf("thread: %+v err %v", thread, err)
	}
	if thread.Ticket.State != "answered" {
		t.Fatalf("thread state: %+v", thread.Ticket)
	}
	list, _ = svc.SupportTicketsUser(ctx, supportUser)
	if list.UnreadTotal != 0 {
		t.Fatalf("unread after read: %d", list.UnreadTotal)
	}

	// Empty text is rejected.
	if _, err := svc.SupportCreateTicket(ctx, supportUser, "subj", "   "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("empty text: %v", err)
	}
}

func TestSupportTicketThreadUserConcealsForeign(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "help me")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const stranger = int64(999)
	if _, err := svc.SupportTicketThreadUser(ctx, stranger, id); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("foreign thread: %v", err)
	}
	if _, err := svc.SupportTicketThreadUser(ctx, supportUser, id+50); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("missing thread: %v", err)
	}
	if err := svc.SupportSendUserToTicket(ctx, stranger, id, "sneaky"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("foreign send: %v", err)
	}
	if err := svc.SupportCloseTicketUser(ctx, stranger, id); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("foreign close: %v", err)
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, id); len(msgs) != 1 {
		t.Fatalf("ticket must be untouched: %d messages", len(msgs))
	}
}

func TestSupportReplyClosedTicketRejected(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "help")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SupportCloseTicketUser(ctx, supportUser, id); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := svc.SupportSendUserToTicket(ctx, supportUser, id, "more"); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("user reply to closed: %v", err)
	}
	if err := svc.SupportReplyTicketAdmin(ctx, 1000, id, "reply"); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("admin reply to closed: %v", err)
	}
	if err := svc.SupportCloseTicketUser(ctx, supportUser, id); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("double close: %v", err)
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, id); len(msgs) != 1 {
		t.Fatalf("closed ticket grew: %d messages", len(msgs))
	}
}

// TestSupportConcurrentCloseAndReply races a user reply against a close: the
// reply must either land before the close or be rejected with ErrTicketClosed —
// never stored into a closed ticket, never lost while reporting success.
func TestSupportConcurrentCloseAndReply(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	for range 10 {
		id, err := svc.SupportCreateTicket(ctx, supportUser, "", "race")
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		var wg sync.WaitGroup
		var replyErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			replyErr = svc.SupportSendUserToTicket(ctx, supportUser, id, "racing reply")
		}()
		go func() {
			defer wg.Done()
			_ = svc.SupportCloseTicketUser(ctx, supportUser, id)
		}()
		wg.Wait()

		msgs, err := svc.store.ListSupportMessagesByTicket(ctx, id)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		switch {
		case replyErr == nil && len(msgs) != 2:
			t.Fatalf("reply reported success but %d messages stored", len(msgs))
		case errors.Is(replyErr, ErrTicketClosed) && len(msgs) != 1:
			t.Fatalf("reply rejected but %d messages stored", len(msgs))
		case replyErr != nil && !errors.Is(replyErr, ErrTicketClosed):
			t.Fatalf("unexpected reply error: %v", replyErr)
		}
	}
}

func TestSupportCloseTicketAdminNotifiesUser(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "help")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := len(sentTo(bot, supportUser))
	if err := svc.SupportCloseTicketAdmin(ctx, 1000, id); err != nil {
		t.Fatalf("admin close: %v", err)
	}
	tk, _ := svc.store.GetSupportTicket(ctx, id)
	if tk == nil || tk.Status != "closed" || tk.ClosedBy != "admin" {
		t.Fatalf("ticket after admin close: %+v", tk)
	}
	userMsgs := sentTo(bot, supportUser)
	if len(userMsgs) != before+1 || !strings.Contains(userMsgs[len(userMsgs)-1].Text, "завершён администратором") {
		t.Fatalf("user close notice: %+v", userMsgs)
	}
	if err := svc.SupportCloseTicketAdmin(ctx, 1000, id); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("double admin close: %v", err)
	}
	if _, err := svc.store.GetSupportTicket(ctx, id+77); err != nil {
		t.Fatalf("sanity: %v", err)
	}
	if err := svc.SupportCloseTicketAdmin(ctx, 1000, id+77); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("close missing: %v", err)
	}
}

func TestSupportTicketAdminGuard(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "help")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const outsider = int64(555)
	if _, err := svc.SupportTicketsAdmin(ctx, outsider); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("tickets: %v", err)
	}
	if _, err := svc.SupportTicketThreadAdmin(ctx, outsider, id); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("thread: %v", err)
	}
	if err := svc.SupportReplyTicketAdmin(ctx, outsider, id, "hi"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("reply: %v", err)
	}
	if err := svc.SupportCloseTicketAdmin(ctx, outsider, id); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("close: %v", err)
	}

	// The admin inbox view works and marks the thread read.
	tickets, err := svc.SupportTicketsAdmin(ctx, 1000)
	if err != nil || len(tickets) != 1 || tickets[0].UserID != supportUser || tickets[0].Unread != 1 {
		t.Fatalf("admin tickets: %+v err %v", tickets, err)
	}
	thread, err := svc.SupportTicketThreadAdmin(ctx, 1000, id)
	if err != nil || len(thread.Messages) != 1 || thread.Ticket.UserID != supportUser {
		t.Fatalf("admin thread: %+v err %v", thread, err)
	}
	tickets, _ = svc.SupportTicketsAdmin(ctx, 1000)
	if tickets[0].Unread != 0 {
		t.Fatalf("unread after admin read: %+v", tickets[0])
	}
}

func TestSupportCloseDryRunSendsNothing(t *testing.T) {
	svc, bot := newSupportService(t)
	svc.dryRun = true
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "help")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SupportReplyTicketAdmin(ctx, 1000, id, "dry reply"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := svc.SupportCloseTicketAdmin(ctx, 1000, id); err != nil {
		t.Fatalf("close: %v", err)
	}
	tk, _ := svc.store.GetSupportTicket(ctx, id)
	if tk == nil || tk.Status != "closed" {
		t.Fatalf("dry-run close must still transition: %+v", tk)
	}
	if len(bot.sent) != 0 {
		t.Fatalf("dry-run sent messages: %+v", bot.sent)
	}
}
