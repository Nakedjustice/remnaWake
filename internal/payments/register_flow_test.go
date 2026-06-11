package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// newRegisterService builds a Service with controllable finder + registrar fakes.
func newRegisterService(t *testing.T) (*Service, *fakeBot, *fakeFinder, *fakeRegistrar) {
	t.Helper()
	svc, bot, _, _ := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	reg := &fakeRegistrar{}
	svc.finder = f
	svc.registrar = reg
	return svc, bot, f, reg
}

func regMsg(chatID int64, text string) *tg.Message {
	return &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}, Text: text}
}

func regConfirmCB(chatID int64, data string) *tg.CallbackQuery {
	return &tg.CallbackQuery{ID: "c", From: tg.User{ID: chatID},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: chatID}}, Data: data}
}

func TestRegisterHappyPathBindsFreeAccount(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}

	if !svc.StartRegisterFlow(ctx, regMsg(200, "/register")) {
		t.Fatal("start should be handled")
	}
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("username should be consumed")
	}
	// Confirmation keyboard shown.
	var confirmShown bool
	for _, m := range bot.sent {
		if m.ChatID == 200 && m.Keyboard != nil &&
			m.Keyboard.InlineKeyboard[0][0].CallbackData == "reg_confirm" {
			confirmShown = true
		}
	}
	if !confirmShown {
		t.Fatalf("confirm keyboard not shown: %+v", bot.sent)
	}

	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 || reg.uuid != "u-42" || reg.telegramID != 200 {
		t.Fatalf("registrar not called correctly: calls=%d uuid=%s tgid=%d", reg.calls, reg.uuid, reg.telegramID)
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared after confirm")
	}
}

func TestRegisterAcceptsSubscriptionLink(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byShort = map[string]*Subscriber{
		"abc123XY": {RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0},
	}

	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "https://sub.example.com/sub/abc123XY")) {
		t.Fatal("link should be consumed")
	}
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "reg_confirm" {
		t.Fatalf("confirm keyboard not shown: %+v", last)
	}
	if !strings.Contains(last.Text, "alice") {
		t.Fatalf("confirmation should name the resolved profile: %q", last.Text)
	}

	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 || reg.uuid != "u-42" || reg.telegramID != 200 {
		t.Fatalf("registrar not called correctly: calls=%d uuid=%s tgid=%d", reg.calls, reg.uuid, reg.telegramID)
	}
}

func TestBareSubscriptionLinkStartsLinking(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byShort = map[string]*Subscriber{
		"abc123XY": {RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0},
	}

	// No /register first: the pasted link alone starts the flow.
	if !svc.HandleText(ctx, regMsg(200, "https://sub.example.com/sub/abc123XY")) {
		t.Fatal("bare link should be consumed")
	}
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "reg_confirm" {
		t.Fatalf("confirm keyboard not shown: %+v", last)
	}

	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 || reg.uuid != "u-42" || reg.telegramID != 200 {
		t.Fatalf("registrar not called correctly: calls=%d uuid=%s tgid=%d", reg.calls, reg.uuid, reg.telegramID)
	}
}

func TestBareSubscriptionLinkUnknownReportsNotFound(t *testing.T) {
	svc, bot, _, reg := newRegisterService(t)
	ctx := context.Background()

	if !svc.HandleText(ctx, regMsg(200, "https://sub.example.com/sub/nosuch1")) {
		t.Fatal("bare link should be consumed even when unknown")
	}
	if reg.calls != 0 {
		t.Fatal("must not write for an unknown link")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("an unknown bare link must not leave a register session behind")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "не найден") {
		t.Fatalf("expected not-found message: %q", last.Text)
	}
}

func TestBareNonLinkTextIsIgnored(t *testing.T) {
	svc, _, _, _ := newRegisterService(t)
	if svc.HandleText(context.Background(), regMsg(200, "hello there")) {
		t.Fatal("plain text outside a flow must not be consumed")
	}
}

func TestExtractShortUUID(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		isLink bool
	}{
		{"https://sub.example.com/sub/abc123XY", "abc123XY", true},
		{"https://sub.example.com/abc123XY", "abc123XY", true},
		{"https://sub.example.com/sub/abc123XY/", "abc123XY", true},
		{"HTTPS://sub.example.com/sub/abc123XY?x=1#frag", "abc123XY", true},
		{"http://example.com/prefix/deep/Short_-1", "Short_-1", true},
		{"https://sub.example.com/", "", true},    // no usable segment
		{"https://sub.example.com/a b", "", true}, // bad segment
		{"alice", "", false},
		{"/register", "", false},
		{"ftp://example.com/abc123XY", "", false},
	}
	for _, c := range cases {
		got, isLink := extractShortUUID(c.in)
		if got != c.want || isLink != c.isLink {
			t.Errorf("extractShortUUID(%q) = (%q, %v), want (%q, %v)", c.in, got, isLink, c.want, c.isLink)
		}
	}
}

func TestRegisterNotFoundStaysInFlow(t *testing.T) {
	svc, bot, _, reg := newRegisterService(t)
	ctx := context.Background()
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "ghost")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("must not write for an unknown username")
	}
	if svc.getRegister(200) == nil {
		t.Fatal("should stay in flow to allow retry")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "не найден") {
		t.Fatalf("expected not-found message: %q", last.Text)
	}
}

func TestRegisterAlreadyLinkedToSelfIsIdempotent(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 200}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("no write needed when already linked to self")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "уже привязан к вашему") {
		t.Fatalf("expected idempotent message: %q", last.Text)
	}
}

func TestRegisterAlreadyLinkedToOtherIsRefused(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 999}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("must not overwrite someone else's link")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared on refusal")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "другому Telegram") {
		t.Fatalf("expected refusal message: %q", last.Text)
	}
}

func TestRegisterDryRunSkipsWrite(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	svc.dryRun = true
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 0 {
		t.Fatalf("dry-run must not write, calls=%d", reg.calls)
	}
	var ok bool
	for _, m := range bot.sent {
		if m.ChatID == 200 && strings.Contains(m.Text, "dry-run") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected dry-run confirmation: %+v", bot.sent)
	}
}

func TestRegisterWriteErrorReported(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	reg.err = errors.New("boom")
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 {
		t.Fatalf("registrar should have been attempted once, calls=%d", reg.calls)
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared after a failed write")
	}
	_ = bot
}

func TestRegisterCancelButtonClearsState(t *testing.T) {
	svc, bot, f, _ := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_cancel")) {
		t.Fatal("reg_cancel should be handled")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared by reg_cancel")
	}
	if len(bot.edits) == 0 || bot.edits[len(bot.edits)-1].Keyboard != nil {
		t.Fatalf("expected keyboard cleared: %+v", bot.edits)
	}
}

func TestRegisterCancelCommandClearsState(t *testing.T) {
	svc, _, _, _ := newRegisterService(t)
	ctx := context.Background()
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "/cancel")) {
		t.Fatal("/cancel should be consumed while in register flow")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared")
	}
}

func TestRegisterTTLExpiry(t *testing.T) {
	svc, _, f, _ := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.now = func() time.Time { return base.Add(11 * time.Minute) }
	if svc.getRegister(200) != nil {
		t.Fatal("stale session should be dropped on access")
	}
	if svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("expired flow must not consume text")
	}
}

func TestSendMenuShowsRegisterButton(t *testing.T) {
	svc, bot, _, _ := newRegisterService(t)
	if !svc.SendMenu(context.Background(), 200) {
		t.Fatal("menu should be sent")
	}
	kb := bot.sent[0].Keyboard
	if kb == nil {
		t.Fatalf("expected keyboard: %+v", bot.sent[0])
	}
	found := map[string]bool{}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			found[btn.CallbackData] = true
		}
	}
	if !found["menu:register"] {
		t.Fatalf("register menu button missing: %+v", kb)
	}
}

func TestMenuRegisterButtonStartsFlow(t *testing.T) {
	svc, _, _, _ := newRegisterService(t)
	ctx := context.Background()
	cb := regConfirmCB(200, "menu:register")
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("menu:register should be handled")
	}
	if svc.getRegister(200) == nil {
		t.Fatal("register flow should start from the menu button")
	}
}
