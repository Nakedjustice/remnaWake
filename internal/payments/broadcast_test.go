package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestBroadcastSendsOncePerTelegramID(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.finder = &fakeFinder{all: []Subscriber{
		{Username: "a", TelegramID: 10},
		{Username: "a2", TelegramID: 10}, // second profile, same TG account
		{Username: "nolink", TelegramID: 0},
		{Username: "b", TelegramID: 20, Status: "EXPIRED"}, // status must not matter
	}}

	sent, failed, err := svc.broadcastMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if sent != 2 || failed != 0 {
		t.Fatalf("sent=%d failed=%d, want 2/0", sent, failed)
	}
	if len(bot.sent) != 2 || bot.sent[0].ChatID != 10 || bot.sent[1].ChatID != 20 {
		t.Fatalf("unexpected sends: %+v", bot.sent)
	}
	for _, m := range bot.sent {
		if m.Text != "hello" {
			t.Fatalf("unexpected text: %q", m.Text)
		}
	}
}

func TestBroadcastCountsFailuresAndContinues(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	bot.sendErrs = map[int64]error{10: errors.New("blocked")}
	svc.finder = &fakeFinder{all: []Subscriber{
		{TelegramID: 10},
		{TelegramID: 20},
	}}

	sent, failed, err := svc.broadcastMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if sent != 1 || failed != 1 {
		t.Fatalf("sent=%d failed=%d, want 1/1", sent, failed)
	}
	if len(bot.sent) != 1 || bot.sent[0].ChatID != 20 {
		t.Fatalf("unexpected sends: %+v", bot.sent)
	}
}

func TestBroadcastDryRunSendsNothing(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.dryRun = true
	svc.finder = &fakeFinder{all: []Subscriber{{TelegramID: 10}, {TelegramID: 20}}}

	sent, failed, err := svc.broadcastMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if sent != 2 || failed != 0 {
		t.Fatalf("sent=%d failed=%d, want 2/0", sent, failed)
	}
	if len(bot.sent) != 0 {
		t.Fatalf("dry-run must not send: %+v", bot.sent)
	}
}

func TestAdminBroadcastGuards(t *testing.T) {
	svc, _, _, _ := newTestService(t) // adminID == 1000
	svc.finder = &fakeFinder{all: []Subscriber{{TelegramID: 10}}}

	if _, err := svc.AdminBroadcast(context.Background(), 42, "hi"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin: err=%v, want ErrNotAdmin", err)
	}
	if _, err := svc.AdminBroadcast(context.Background(), 1000, "   "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("empty text: err=%v, want ErrBadInput", err)
	}

	res, err := svc.AdminBroadcast(context.Background(), 1000, "hi")
	if err != nil || !res.Started {
		t.Fatalf("res=%+v err=%v, want started", res, err)
	}
	svc.background.Wait()
}

// TestAdminBroadcastReturnsBeforeSending is the point of the change: the mini
// app admin used to hold an HTTP request open for the whole run (50ms per
// recipient), which no browser waits out on a real panel.
func TestAdminBroadcastReturnsBeforeSending(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	release := make(chan struct{})
	bot.beforeSend = func(int64) { <-release }
	svc.finder = &fakeFinder{all: []Subscriber{{TelegramID: 10}, {TelegramID: 20}}}

	res, err := svc.AdminBroadcast(context.Background(), 1000, "hi")
	if err != nil {
		t.Fatalf("AdminBroadcast: %v", err)
	}
	if !res.Started {
		t.Fatal("want started=true")
	}
	// The call returned while the very first send is still blocked.
	if got := bot.sentCount(); got != 0 {
		t.Fatalf("%d messages already sent, want the call to return first", got)
	}
	close(release)
	svc.background.Wait()
	if got := bot.sentCount(); got != 3 { // 2 recipients + the completion report
		t.Fatalf("sent %d messages, want 2 recipients plus the report", got)
	}
}

// TestBroadcastRefusesToRunTwiceAtOnce: returning immediately makes a second
// tap trivial, and every user would get the message twice.
func TestBroadcastRefusesToRunTwiceAtOnce(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	release := make(chan struct{})
	bot.beforeSend = func(int64) { <-release }
	svc.finder = &fakeFinder{all: []Subscriber{{TelegramID: 10}}}

	if _, err := svc.AdminBroadcast(context.Background(), 1000, "hi"); err != nil {
		t.Fatalf("first broadcast: %v", err)
	}
	if _, err := svc.AdminBroadcast(context.Background(), 1000, "hi again"); !errors.Is(err, ErrBroadcastInProgress) {
		t.Fatalf("second broadcast: err=%v, want ErrBroadcastInProgress", err)
	}
	close(release)
	svc.background.Wait()

	// Once it finishes, a new broadcast is allowed again.
	if _, err := svc.AdminBroadcast(context.Background(), 1000, "later"); err != nil {
		t.Fatalf("broadcast after completion: %v", err)
	}
	svc.background.Wait()
}

// TestAdminBroadcastListErrorIsReportedToTheChat: the panel failure can no
// longer come back on the HTTP response, so it must reach the admin some way.
func TestAdminBroadcastListErrorIsReportedToTheChat(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.finder = &fakeFinder{listErr: errors.New("panel down")}

	if _, err := svc.AdminBroadcast(context.Background(), 1000, "hi"); err != nil {
		t.Fatalf("AdminBroadcast: %v", err)
	}
	svc.background.Wait()

	last := bot.sent[len(bot.sent)-1]
	if last.ChatID != 1000 || !strings.Contains(last.Text, "рассылка не выполнена") {
		t.Fatalf("admin was not told the broadcast failed: %+v", last)
	}
}

func TestBroadcastChatFlowPreviewAndCancel(t *testing.T) {
	svc, bot, _, _ := newTestService(t) // adminID == 1000
	ctx := context.Background()

	// Admin presses the menu button.
	start := &tg.CallbackQuery{ID: "c1", From: tg.User{ID: 1000},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 1000}}, Data: "adm:bcast"}
	if !svc.HandleCallback(ctx, start) {
		t.Fatal("adm:bcast should be handled")
	}

	// Next text message becomes the draft and triggers a confirm preview.
	if !svc.HandleAdminCommand(ctx, msg(1000, "Всем привет")) {
		t.Fatal("broadcast text should be consumed")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "Всем привет") || last.Keyboard == nil {
		t.Fatalf("expected preview with confirm keyboard, got %+v", last)
	}
	if last.Keyboard.InlineKeyboard[0][0].CallbackData != "adm:bc_send" {
		t.Fatalf("unexpected keyboard: %+v", last.Keyboard)
	}
	svc.mu.Lock()
	pending := svc.adminInput[1000].pendingBroadcast
	svc.mu.Unlock()
	if pending != "Всем привет" {
		t.Fatalf("pendingBroadcast=%q", pending)
	}

	// Cancel clears the draft and the keyboard.
	cancel := &tg.CallbackQuery{ID: "c2", From: tg.User{ID: 1000},
		Message: &tg.Message{MessageID: 6, Chat: tg.Chat{ID: 1000}}, Data: "adm:bc_cancel"}
	if !svc.HandleCallback(ctx, cancel) {
		t.Fatal("adm:bc_cancel should be handled")
	}
	svc.mu.Lock()
	state := svc.adminInput[1000]
	svc.mu.Unlock()
	if state.pendingBroadcast != "" || state.step != adminInputNone {
		t.Fatalf("state not cleared: %+v", state)
	}
	if len(bot.edits) == 0 {
		t.Fatal("confirm keyboard should be cleared on cancel")
	}
	if got := bot.sent[len(bot.sent)-1].Text; got != "Рассылка отменена." {
		t.Fatalf("unexpected cancel reply: %q", got)
	}
}

func TestBroadcastSendWithoutDraft(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 1000},
		Message: &tg.Message{MessageID: 7, Chat: tg.Chat{ID: 1000}}, Data: "adm:bc_send"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("adm:bc_send should be handled")
	}
	got := bot.sent[len(bot.sent)-1].Text
	if !strings.Contains(got, "Нет текста") {
		t.Fatalf("unexpected reply: %q", got)
	}
}
