package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

const supportUser = int64(200)

func newSupportService(t *testing.T) (*Service, *fakeBot) {
	t.Helper()
	svc, bot, _, _ := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	f.byTG[supportUser] = []Subscriber{{TelegramID: supportUser, Username: "bob"}}
	svc.finder = f
	return svc, bot
}

func sentTo(bot *fakeBot, chatID int64) []sentMsg {
	var out []sentMsg
	for _, m := range bot.sent {
		if m.ChatID == chatID {
			out = append(out, m)
		}
	}
	return out
}

func TestSupportUserMessageNotifiesAdmins(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	if err := svc.SupportSendUser(ctx, supportUser, "  need help  "); err != nil {
		t.Fatalf("send user: %v", err)
	}

	msgs, err := svc.store.ListSupportMessages(ctx, supportUser)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("stored messages: %d err %v", len(msgs), err)
	}
	if msgs[0].FromAdmin || msgs[0].Text != "need help" || msgs[0].UserUsername != "bob" {
		t.Fatalf("stored message wrong: %+v", msgs[0])
	}

	adminMsgs := sentTo(bot, 1000)
	if len(adminMsgs) != 1 {
		t.Fatalf("expected one admin notification, got %+v", bot.sent)
	}
	if !strings.Contains(adminMsgs[0].Text, "bob") || !strings.Contains(adminMsgs[0].Text, "need help") {
		t.Fatalf("notification text: %q", adminMsgs[0].Text)
	}
	kb := adminMsgs[0].Keyboard
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "sup:reply:200" {
		t.Fatalf("reply button missing: %+v", kb)
	}

	// Bot-only parity pin: the fan-out is byte-identical to the pre-ticket
	// bot — same text, no ticket number — even though the message now lands
	// in an auto-created open ticket.
	if adminMsgs[0].Text != "🛟 Сообщение в поддержку от bob (TG 200):\n\nneed help" {
		t.Fatalf("fan-out text changed: %q", adminMsgs[0].Text)
	}
	open, err := svc.store.LatestOpenTicketForUser(ctx, supportUser)
	if err != nil || open == nil {
		t.Fatalf("auto-created ticket: %+v err %v", open, err)
	}
	if msgs[0].TicketID != open.ID {
		t.Fatalf("message not attached to ticket: msg %+v ticket %d", msgs[0], open.ID)
	}
}

func TestSupportAdminReplyDeliversToUser(t *testing.T) {
	svc, bot := newSupportService(t)
	ctx := context.Background()

	// Non-admin caller is rejected.
	if err := svc.SupportSendAdmin(ctx, 555, supportUser, "hi"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin reply: want ErrNotAdmin, got %v", err)
	}

	if err := svc.SupportSendAdmin(ctx, 1000, supportUser, "how can I help?"); err != nil {
		t.Fatalf("admin reply: %v", err)
	}
	userMsgs := sentTo(bot, supportUser)
	if len(userMsgs) != 1 || !strings.Contains(userMsgs[0].Text, "how can I help?") {
		t.Fatalf("user delivery: %+v", userMsgs)
	}
	msgs, _ := svc.store.ListSupportMessages(ctx, supportUser)
	if len(msgs) != 1 || !msgs[0].FromAdmin || msgs[0].AuthorTelegramID != 1000 {
		t.Fatalf("stored reply wrong: %+v", msgs)
	}
}

func TestSupportAdminViewsAndClose(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	if err := svc.SupportSendUser(ctx, supportUser, "first"); err != nil {
		t.Fatalf("send user: %v", err)
	}

	// Non-admins cannot list or read conversations.
	if _, err := svc.SupportConversations(ctx, 555); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("conversations non-admin: %v", err)
	}

	convs, err := svc.SupportConversations(ctx, 1000)
	if err != nil || len(convs) != 1 || convs[0].UserID != supportUser || convs[0].Unread != 1 {
		t.Fatalf("conversations: %+v err %v", convs, err)
	}

	// Opening the thread marks user messages read.
	thread, err := svc.SupportThreadAdmin(ctx, 1000, supportUser)
	if err != nil || !thread.Open || len(thread.Messages) != 1 {
		t.Fatalf("thread: %+v err %v", thread, err)
	}
	convs, _ = svc.SupportConversations(ctx, 1000)
	if convs[0].Unread != 0 {
		t.Fatalf("unread should clear after admin reads: %+v", convs)
	}

	// Closing deletes the thread.
	if err := svc.SupportCloseAdmin(ctx, 1000, supportUser); err != nil {
		t.Fatalf("close: %v", err)
	}
	if convs, _ := svc.SupportConversations(ctx, 1000); len(convs) != 0 {
		t.Fatalf("conversations after close: %+v", convs)
	}
}

func TestSupportBotSessionRouting(t *testing.T) {
	svc, _ := newSupportService(t)
	ctx := context.Background()

	// Outside a session, plain text is not captured as support.
	if svc.handleSupportSessionInput(ctx, &tg.Message{Chat: tg.Chat{ID: supportUser}, Text: "hello"}) {
		t.Fatal("should not capture text without an active session")
	}

	svc.StartSupport(ctx, supportUser)
	if !svc.inSupportSession(supportUser) {
		t.Fatal("session should be active after StartSupport")
	}
	if !svc.handleSupportSessionInput(ctx, &tg.Message{Chat: tg.Chat{ID: supportUser}, Text: "my vpn is down"}) {
		t.Fatal("active session should capture the message")
	}
	msgs, _ := svc.store.ListSupportMessages(ctx, supportUser)
	if len(msgs) != 1 || msgs[0].Text != "my vpn is down" {
		t.Fatalf("session message not stored: %+v", msgs)
	}

	// /cancel via HandleText clears the session.
	if !svc.HandleText(ctx, &tg.Message{Chat: tg.Chat{ID: supportUser}, Text: "/cancel"}) {
		t.Fatal("/cancel should be handled while a support session is active")
	}
	if svc.inSupportSession(supportUser) {
		t.Fatal("session should be cleared after /cancel")
	}
}
