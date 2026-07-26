package payments

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// supportCb builds a callback query as if the button was pressed in the given
// user's private chat — the only place the ticket keyboards ever live.
func supportCb(fromID int64, data string) *tg.CallbackQuery {
	return &tg.CallbackQuery{
		ID:      "cbid",
		From:    tg.User{ID: fromID},
		Message: &tg.Message{MessageID: 50, Chat: tg.Chat{ID: fromID}},
		Data:    data,
	}
}

// supportTap presses a ticket button without an id (sup:list, sup:new, adm:sup).
func supportTap(t *testing.T, svc *Service, fromID int64, data string) {
	t.Helper()
	if !svc.HandleCallback(context.Background(), supportCb(fromID, data)) {
		t.Fatalf("callback %q was not handled", data)
	}
}

// supportTicketTap presses one of the ticket buttons that carry a ticket id.
func supportTicketTap(t *testing.T, svc *Service, fromID int64, prefix string, ticketID int64) {
	t.Helper()
	supportTap(t, svc, fromID, fmt.Sprintf("%s%d", prefix, ticketID))
}

// supportType feeds a plain chat message through the user text pipeline.
func supportType(svc *Service, chatID int64, text string) bool {
	return svc.HandleText(context.Background(), &tg.Message{Chat: tg.Chat{ID: chatID}, Text: text})
}

// supportTypeAdmin feeds a plain chat message through the admin pipeline.
func supportTypeAdmin(svc *Service, chatID int64, text string) bool {
	return svc.HandleAdminCommand(context.Background(), &tg.Message{Chat: tg.Chat{ID: chatID}, Text: text})
}

func lastSentTo(t *testing.T, bot *fakeBot, chatID int64) sentMsg {
	t.Helper()
	msgs := sentTo(bot, chatID)
	if len(msgs) == 0 {
		t.Fatalf("no message was sent to %d", chatID)
	}
	return msgs[len(msgs)-1]
}

func hasTicketButton(kb *tg.InlineKeyboardMarkup, data string) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == data {
				return true
			}
		}
	}
	return false
}

func TestSupportBotTicketListOpensThread(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "Не работает VPN уже два дня")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SupportReplyTicketAdmin(ctx, 1000, id, "смотрим"); err != nil {
		t.Fatalf("admin reply: %v", err)
	}

	supportTap(t, svc, supportUser, "sup:list")
	list := lastSentTo(t, bot, supportUser)
	if !strings.Contains(list.Text, fmt.Sprintf("№%d", id)) || !strings.Contains(list.Text, "Не работает VPN") {
		t.Fatalf("ticket list text: %q", list.Text)
	}
	if !hasTicketButton(list.Keyboard, fmt.Sprintf("sup:t:%d", id)) || !hasTicketButton(list.Keyboard, "sup:new") {
		t.Fatalf("ticket list keyboard: %+v", list.Keyboard)
	}

	supportTicketTap(t, svc, supportUser, "sup:t:", id)
	thread := lastSentTo(t, bot, supportUser)
	if !strings.Contains(thread.Text, "Не работает VPN") || !strings.Contains(thread.Text, "смотрим") {
		t.Fatalf("thread text: %q", thread.Text)
	}
	for _, want := range []string{fmt.Sprintf("sup:re:%d", id), fmt.Sprintf("sup:cl:%d", id), "sup:list"} {
		if !hasTicketButton(thread.Keyboard, want) {
			t.Fatalf("thread keyboard missing %q: %+v", want, thread.Keyboard)
		}
	}

	// Reading the thread in the bot clears the unread badge the mini app shows.
	tickets, err := svc.SupportTicketsUser(ctx, supportUser)
	if err != nil || tickets.UnreadTotal != 0 {
		t.Fatalf("unread after bot read: %+v err %v", tickets, err)
	}
}

func TestSupportBotEmptyTicketListOffersNewTicket(t *testing.T) {
	svc, bot := newSupportService(t)

	supportTap(t, svc, supportUser, "sup:list")
	msg := lastSentTo(t, bot, supportUser)
	if !hasTicketButton(msg.Keyboard, "sup:new") {
		t.Fatalf("empty list must offer a new ticket: %+v", msg.Keyboard)
	}
}

func TestSupportBotNewTicketFlow(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	supportTap(t, svc, supportUser, "sup:new")
	if !supportType(svc, supportUser, "Не приходит оплата\nвторой день") {
		t.Fatal("the composed message must be consumed by the ticket flow")
	}

	list, err := svc.SupportTicketsUser(ctx, supportUser)
	if err != nil || len(list.Tickets) != 1 {
		t.Fatalf("tickets: %+v err %v", list, err)
	}
	if list.Tickets[0].Subject != "Не приходит оплата" {
		t.Fatalf("derived subject: %q", list.Tickets[0].Subject)
	}
	thread, err := svc.SupportTicketThreadUser(ctx, supportUser, list.Tickets[0].ID)
	if err != nil || len(thread.Messages) != 1 || thread.Messages[0].Text != "Не приходит оплата\nвторой день" {
		t.Fatalf("thread: %+v err %v", thread, err)
	}

	adminMsgs := sentTo(bot, 1000)
	if len(adminMsgs) != 1 || !strings.Contains(adminMsgs[0].Text, "Не приходит оплата") {
		t.Fatalf("admin fan-out: %+v", adminMsgs)
	}

	// A second plain message is no longer captured: the compose step is one-shot.
	if supportType(svc, supportUser, "ещё текст") {
		t.Fatal("compose state must not survive the message it consumed")
	}
}

func TestSupportBotReplyTargetsChosenTicket(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	first, err := svc.SupportCreateTicket(ctx, supportUser, "Первое", "первое обращение")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := svc.SupportCreateTicket(ctx, supportUser, "Второе", "второе обращение")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	supportTicketTap(t, svc, supportUser, "sup:re:", first)
	if !supportType(svc, supportUser, "дополнение к первому") {
		t.Fatal("reply text must be consumed")
	}

	firstMsgs, _ := svc.store.ListSupportMessagesByTicket(ctx, first)
	secondMsgs, _ := svc.store.ListSupportMessagesByTicket(ctx, second)
	if len(firstMsgs) != 2 || firstMsgs[1].Text != "дополнение к первому" {
		t.Fatalf("first ticket: %+v", firstMsgs)
	}
	if len(secondMsgs) != 1 {
		t.Fatalf("newest ticket must be untouched: %+v", secondMsgs)
	}
}

func TestSupportBotCloseTicket(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "закройте это")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	supportTicketTap(t, svc, supportUser, "sup:cl:", id)
	tk, _ := svc.store.GetSupportTicket(ctx, id)
	if tk == nil || tk.Status != "closed" || tk.ClosedBy != "user" {
		t.Fatalf("ticket after close: %+v", tk)
	}
	var closeNote bool
	for _, m := range sentTo(bot, 1000) {
		if strings.Contains(m.Text, "завершил чат") {
			closeNote = true
		}
	}
	if !closeNote {
		t.Fatalf("admins not told about the close: %+v", sentTo(bot, 1000))
	}

	// Closing twice is reported, not silently repeated.
	before := len(bot.sent)
	supportTicketTap(t, svc, supportUser, "sup:cl:", id)
	if len(bot.sent) == before && len(bot.answers) == 0 {
		t.Fatal("a repeated close must tell the user the ticket is already closed")
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, id); len(msgs) != 1 {
		t.Fatalf("closed ticket grew: %d", len(msgs))
	}

	// A closed ticket refuses a reply from the bot as well.
	supportTicketTap(t, svc, supportUser, "sup:re:", id)
	supportType(svc, supportUser, "ещё сообщение")
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, id); len(msgs) != 1 {
		t.Fatalf("closed ticket accepted a bot reply: %d", len(msgs))
	}
}

// TestSupportBotConcealsForeignTickets pins the concealment contract on the bot
// surface: a foreign ticket id and a nonexistent one must be indistinguishable.
func TestSupportBotConcealsForeignTickets(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "мой тикет")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const stranger = int64(999)

	for _, prefix := range []string{"sup:t:", "sup:re:", "sup:cl:"} {
		bot.sent, bot.answers = nil, nil
		supportTicketTap(t, svc, stranger, prefix, id)
		foreign := fmt.Sprintf("%+v|%+v", sentTo(bot, stranger), bot.answers)

		bot.sent, bot.answers = nil, nil
		supportTicketTap(t, svc, stranger, prefix, id+500)
		missing := fmt.Sprintf("%+v|%+v", sentTo(bot, stranger), bot.answers)

		if foreign != missing {
			t.Fatalf("%s leaks ticket existence:\nforeign=%s\nmissing=%s", prefix, foreign, missing)
		}
	}

	// Nothing the stranger did may touch the ticket, even after typing.
	supportType(svc, stranger, "подслушиваю")
	msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, id)
	if len(msgs) != 1 {
		t.Fatalf("foreign writes landed in the ticket: %+v", msgs)
	}
	tk, _ := svc.store.GetSupportTicket(ctx, id)
	if tk == nil || tk.Status != "open" {
		t.Fatalf("foreign close went through: %+v", tk)
	}
}

func TestSupportBotAdminInboxReplyAndClose(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	id, err := svc.SupportCreateTicket(ctx, supportUser, "", "не открывается сайт")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A non-admin gets nothing but the standard refusal.
	supportTap(t, svc, 555, "adm:sup")
	if len(sentTo(bot, 555)) != 0 {
		t.Fatalf("non-admin saw the inbox: %+v", sentTo(bot, 555))
	}

	supportTap(t, svc, 1000, "adm:sup")
	inbox := lastSentTo(t, bot, 1000)
	if !strings.Contains(inbox.Text, fmt.Sprintf("№%d", id)) {
		t.Fatalf("inbox text: %q", inbox.Text)
	}
	if !hasTicketButton(inbox.Keyboard, fmt.Sprintf("adm:sup:t:%d", id)) {
		t.Fatalf("inbox keyboard: %+v", inbox.Keyboard)
	}

	supportTicketTap(t, svc, 1000, "adm:sup:t:", id)
	thread := lastSentTo(t, bot, 1000)
	if !strings.Contains(thread.Text, "не открывается сайт") {
		t.Fatalf("admin thread text: %q", thread.Text)
	}
	for _, want := range []string{fmt.Sprintf("adm:sup:re:%d", id), fmt.Sprintf("adm:sup:cl:%d", id), "adm:sup"} {
		if !hasTicketButton(thread.Keyboard, want) {
			t.Fatalf("admin thread keyboard missing %q: %+v", want, thread.Keyboard)
		}
	}
	if tickets, _ := svc.SupportTicketsAdmin(ctx, 1000); len(tickets) != 1 || tickets[0].Unread != 0 {
		t.Fatalf("reading the thread must clear the admin unread: %+v", tickets)
	}

	supportTicketTap(t, svc, 1000, "adm:sup:re:", id)
	if !supportTypeAdmin(svc, 1000, "уже чиним") {
		t.Fatal("the admin reply text must be consumed")
	}
	userMsgs := sentTo(bot, supportUser)
	if len(userMsgs) == 0 || !strings.Contains(userMsgs[len(userMsgs)-1].Text, "уже чиним") {
		t.Fatalf("reply delivery: %+v", userMsgs)
	}
	stored, _ := svc.store.ListSupportMessagesByTicket(ctx, id)
	if len(stored) != 2 || !stored[1].FromAdmin || stored[1].AuthorTelegramID != 1000 {
		t.Fatalf("stored reply: %+v", stored)
	}

	supportTicketTap(t, svc, 1000, "adm:sup:cl:", id)
	tk, _ := svc.store.GetSupportTicket(ctx, id)
	if tk == nil || tk.Status != "closed" || tk.ClosedBy != "admin" {
		t.Fatalf("ticket after admin close: %+v", tk)
	}
	userMsgs = sentTo(bot, supportUser)
	if !strings.Contains(userMsgs[len(userMsgs)-1].Text, "завершён администратором") {
		t.Fatalf("user close notice: %+v", userMsgs)
	}
}

// TestSupportBotLegacySessionRidesNewestTicket pins the coexistence rule: the
// pre-ticket typing session keeps working and always lands in the user's newest
// open ticket, while the explicit per-ticket compose step takes precedence.
func TestSupportBotLegacySessionRidesNewestTicket(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	svc.StartSupport(ctx, supportUser)
	prompt := lastSentTo(t, bot, supportUser)
	if !hasTicketButton(prompt.Keyboard, "sup:list") || !hasTicketButton(prompt.Keyboard, "sup:close") {
		t.Fatalf("quick-chat prompt must reach both the ticket list and the old close: %+v", prompt.Keyboard)
	}

	if !supportType(svc, supportUser, "первое сообщение") {
		t.Fatal("the legacy session must capture plain text")
	}
	first, _ := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if first == nil {
		t.Fatal("legacy session did not open a ticket")
	}

	// Opening a second ticket explicitly does not disturb the first one.
	supportTap(t, svc, supportUser, "sup:new")
	if !supportType(svc, supportUser, "вторая тема") {
		t.Fatal("the compose step must win over the live session")
	}
	second, _ := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if second == nil || second.ID == first.ID {
		t.Fatalf("second ticket: %+v (first %d)", second, first.ID)
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, first.ID); len(msgs) != 1 {
		t.Fatalf("first ticket changed: %+v", msgs)
	}

	// The session survived and now rides the newest open ticket.
	if !svc.inSupportSession(supportUser) {
		t.Fatal("the legacy session must survive the ticket detour")
	}
	if !supportType(svc, supportUser, "продолжаю печатать") {
		t.Fatal("the legacy session must still capture text")
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, second.ID); len(msgs) != 2 {
		t.Fatalf("newest ticket after session text: %+v", msgs)
	}

	// /cancel clears the pending compose target together with the session.
	supportTicketTap(t, svc, supportUser, "sup:re:", first.ID)
	if !supportType(svc, supportUser, "/cancel") {
		t.Fatal("/cancel must be handled")
	}
	if supportType(svc, supportUser, "уже никуда") {
		t.Fatal("no support state may survive /cancel")
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, first.ID); len(msgs) != 1 {
		t.Fatalf("text leaked into the first ticket after /cancel: %+v", msgs)
	}
}

// Closing one ticket from the list is not the same as ending the chat: the
// typing session stays live and its next message opens a fresh ticket, exactly
// as it did before tickets were reachable one by one.
func TestSupportBotClosingATicketKeepsTheSessionAlive(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	svc.StartSupport(ctx, supportUser)
	if !supportType(svc, supportUser, "первое") {
		t.Fatal("session must capture text")
	}
	first, _ := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if first == nil {
		t.Fatal("no ticket opened")
	}

	supportTicketTap(t, svc, supportUser, "sup:cl:", first.ID)
	if !svc.inSupportSession(supportUser) {
		t.Fatal("closing one ticket must not end the typing session")
	}
	if !supportType(svc, supportUser, "новая тема") {
		t.Fatal("the session must still capture text after a ticket was closed")
	}
	second, _ := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if second == nil || second.ID == first.ID {
		t.Fatalf("a fresh ticket must be opened: %+v (first %d)", second, first.ID)
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, first.ID); len(msgs) != 1 {
		t.Fatalf("the closed ticket must not grow: %+v", msgs)
	}
}

func TestSupportBotTicketsDryRunSendsNothingToOthers(t *testing.T) {
	svc, bot := newSupportService(t)
	svc.dryRun = true
	ctx := context.Background()

	supportTap(t, svc, supportUser, "sup:new")
	if !supportType(svc, supportUser, "тихое обращение") {
		t.Fatal("compose must be consumed in dry-run too")
	}
	tk, _ := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if tk == nil {
		t.Fatal("dry-run must still create the ticket")
	}
	if len(sentTo(bot, 1000)) != 0 {
		t.Fatalf("dry-run notified the admins: %+v", sentTo(bot, 1000))
	}

	before := len(sentTo(bot, supportUser))
	supportTicketTap(t, svc, 1000, "adm:sup:re:", tk.ID)
	if !supportTypeAdmin(svc, 1000, "тихий ответ") {
		t.Fatal("admin reply must be consumed in dry-run")
	}
	if len(sentTo(bot, supportUser)) != before {
		t.Fatalf("dry-run delivered a reply to the user: %+v", sentTo(bot, supportUser))
	}
	if msgs, _ := svc.store.ListSupportMessagesByTicket(ctx, tk.ID); len(msgs) != 2 {
		t.Fatalf("dry-run must still store the reply: %+v", msgs)
	}

	supportTicketTap(t, svc, 1000, "adm:sup:cl:", tk.ID)
	if len(sentTo(bot, supportUser)) != before {
		t.Fatalf("dry-run announced the close to the user: %+v", sentTo(bot, supportUser))
	}
	if closed, _ := svc.store.GetSupportTicket(ctx, tk.ID); closed == nil || closed.Status != "closed" {
		t.Fatalf("dry-run close must still transition: %+v", closed)
	}
}
