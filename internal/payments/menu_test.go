package payments

import (
	"context"
	"strings"
	"testing"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func newMenuService(t *testing.T) (*Service, *fakeBot, *fakeFinder, *store.Store) {
	t.Helper()
	svc, bot, _, st := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	svc.finder = f
	return svc, bot, f, st
}

func TestSendMenuShowsTariffAndGiftButtons(t *testing.T) {
	svc, bot, _, _ := newMenuService(t) // adminID == 1000
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
	if !found["menu:tariffs"] || !found["menu:gift"] {
		t.Fatalf("menu buttons wrong: %+v", kb)
	}
	if found["menu:payff"] {
		t.Fatalf("removed payff button must not be shown: %+v", kb)
	}
}

func TestSendTariffsListsPrices(t *testing.T) {
	svc, bot, _, st := newMenuService(t)
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
	svc, bot, _, _ := newMenuService(t)
	if !svc.SendTariffs(context.Background(), 200) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не заданы") {
		t.Fatalf("expected 'not set' reply: %+v", bot.sent)
	}
}

func TestMenuTariffsButtonLists(t *testing.T) {
	svc, bot, _, st := newMenuService(t)
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

func TestCancelWithoutFlowNotConsumed(t *testing.T) {
	svc, _, _, _ := newMenuService(t)
	msg := &tg.Message{MessageID: 1, Chat: tg.Chat{ID: 300}, Text: "/cancel"}
	if svc.HandleText(context.Background(), msg) {
		t.Fatal("/cancel without a flow must not be consumed")
	}
}
