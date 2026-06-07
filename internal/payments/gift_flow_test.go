package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func newGiftService(t *testing.T) (*Service, *fakeBot, *fakeFinder, *store.Store) {
	t.Helper()
	svc, bot, _, st := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	svc.finder = f
	return svc, bot, f, st
}

func giftMsg(chatID int64, text string) *tg.Message {
	return &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}, Text: text}
}

func TestPayffRejectsNonSubscriber(t *testing.T) {
	svc, bot, _, _ := newGiftService(t)
	if !svc.StartGiftFlow(context.Background(), giftMsg(200, "/payff")) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].ChatID != 200 {
		t.Fatalf("expected one reply to payer: %+v", bot.sent)
	}
	if svc.getGift(200) != nil {
		t.Fatal("non-subscriber must not start a flow")
	}
}

func TestPayffHappyPathByUsername(t *testing.T) {
	svc, bot, f, st := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, UUID: "u-9", Username: "payer", TelegramID: 200, ExpireAt: time.Now()}}
	f.byName["bob"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.StartGiftFlow(ctx, giftMsg(200, "/payff")) {
		t.Fatal("start should be handled")
	}
	if !svc.HandleText(ctx, giftMsg(200, "bob")) {
		t.Fatal("identifier should be consumed")
	}
	var kb *tg.InlineKeyboardMarkup
	for _, m := range bot.sent {
		if m.ChatID == 200 && m.Keyboard != nil {
			kb = m.Keyboard
		}
	}
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "gpick:3" {
		t.Fatalf("tariff keyboard wrong: %+v", kb)
	}

	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "gpick:3"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("gpick should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.UUID != "u-42" || req.Months != 3 || req.Price != 450 {
		t.Fatalf("request wrong: %+v", req)
	}
	if req.PayerTelegramID != 200 || req.PayerUsername != "payer" {
		t.Fatalf("payer not recorded: %+v", req)
	}
	var adminConfirm bool
	for _, m := range bot.sent {
		if m.ChatID == 1000 && m.Keyboard != nil && m.Keyboard.InlineKeyboard[0][0].CallbackData == "ok:1" {
			adminConfirm = true
		}
	}
	if !adminConfirm {
		t.Fatalf("admin not notified: %+v", bot.sent)
	}
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared after pick")
	}
}

func TestPayffByTelegramIDAutodetect(t *testing.T) {
	svc, bot, f, st := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, UUID: "u-9", Username: "payer", TelegramID: 200}}
	f.byTG[555] = []Subscriber{{RemnawaveID: 42, UUID: "u-42", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}}
	_ = st.UpsertTariff(ctx, 1, 150)

	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "555")) {
		t.Fatal("digits should be consumed as TGID")
	}
	g := svc.getGift(200)
	if g == nil || g.target == nil || g.target.Username != "bob" {
		t.Fatalf("target not resolved by TGID: %+v", g)
	}
	_ = bot
}

func TestPayffMultiMatchAsksForUsername(t *testing.T) {
	svc, bot, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	f.byTG[555] = []Subscriber{
		{RemnawaveID: 1, Username: "a", TelegramID: 555},
		{RemnawaveID: 2, Username: "b", TelegramID: 555},
	}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "555")) {
		t.Fatal("should be consumed")
	}
	g := svc.getGift(200)
	if g == nil || g.step != stepAwaitingIdentifier {
		t.Fatalf("should stay awaiting identifier: %+v", g)
	}
	last := bot.sent[len(bot.sent)-1]
	if last.ChatID != 200 {
		t.Fatalf("expected prompt to payer: %+v", last)
	}
}

func TestPayffNotFoundStaysInFlow(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "ghost")) {
		t.Fatal("should be consumed")
	}
	g := svc.getGift(200)
	if g == nil || g.step != stepAwaitingIdentifier {
		t.Fatalf("should stay awaiting identifier: %+v", g)
	}
}

func TestPayffCancelClearsState(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "/cancel")) {
		t.Fatal("/cancel should be consumed while in flow")
	}
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared")
	}
	if svc.HandleText(ctx, giftMsg(300, "/cancel")) {
		t.Fatal("/cancel without a flow must not be consumed")
	}
}

func TestPayffTTLExpiry(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	svc.now = func() time.Time { return base.Add(11 * time.Minute) }
	if svc.getGift(200) != nil {
		t.Fatal("stale session should be dropped on access")
	}
	if svc.HandleText(ctx, giftMsg(200, "bob")) {
		t.Fatal("expired flow must not consume text")
	}
}

func TestSendMenuShowsTariffAndPayffButtons(t *testing.T) {
	svc, bot, _, _ := newGiftService(t) // adminID == 1000
	if !svc.SendMenu(context.Background(), 200) {
		t.Fatal("menu should be sent")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one menu message: %+v", bot.sent)
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
	if !found["menu:tariffs"] || !found["menu:payff"] {
		t.Fatalf("menu buttons wrong: %+v", kb)
	}
}

func TestSendTariffsListsPrices(t *testing.T) {
	svc, bot, _, st := newGiftService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, 1, 150)
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.SendTariffs(ctx, 200) {
		t.Fatal("tariffs should be sent")
	}
	if len(bot.sent) != 1 || bot.sent[0].ChatID != 200 {
		t.Fatalf("expected one reply to user: %+v", bot.sent)
	}
	text := bot.sent[0].Text
	if !strings.Contains(text, "1 мес. — 150₽") || !strings.Contains(text, "3 мес. — 450₽") {
		t.Fatalf("tariff text wrong: %q", text)
	}
}

func TestSendTariffsEmpty(t *testing.T) {
	svc, bot, _, _ := newGiftService(t)
	if !svc.SendTariffs(context.Background(), 200) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не заданы") {
		t.Fatalf("expected 'not set' reply: %+v", bot.sent)
	}
}

func TestMenuTariffsButtonLists(t *testing.T) {
	svc, bot, _, st := newGiftService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, 6, 800)

	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "menu:tariffs"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("menu:tariffs should be handled")
	}
	var listed bool
	for _, m := range bot.sent {
		if m.ChatID == 200 && strings.Contains(m.Text, "6 мес. — 800₽") {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("tariffs not listed: %+v", bot.sent)
	}
}

func TestMenuPayffButtonStartsFlow(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}

	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "menu:payff"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("menu:payff should be handled")
	}
	g := svc.getGift(200)
	if g == nil || g.step != stepAwaitingIdentifier || g.payerName != "payer" {
		t.Fatalf("flow not started: %+v", g)
	}
}

func TestMenuPayffButtonRejectsNonSubscriber(t *testing.T) {
	svc, _, _, _ := newGiftService(t)
	ctx := context.Background()
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "menu:payff"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("menu:payff should be handled")
	}
	if svc.getGift(200) != nil {
		t.Fatal("non-subscriber must not start a flow from the menu")
	}
}

func TestGiftCancelButton(t *testing.T) {
	svc, bot, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "gcancel"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("gcancel should be handled")
	}
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared by gcancel")
	}
	if len(bot.edits) == 0 || bot.edits[len(bot.edits)-1].Keyboard != nil {
		t.Fatalf("expected keyboard cleared: %+v", bot.edits)
	}
}
