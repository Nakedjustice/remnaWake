package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestSendCabinetUnlinkedOffersRegister(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.finder = &fakeFinder{}

	if !svc.SendCabinet(context.Background(), 555) {
		t.Fatal("expected handled")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(bot.sent))
	}
	msg := bot.sent[0]
	if !strings.Contains(msg.Text, "не привязан") {
		t.Errorf("expected unlinked text, got %q", msg.Text)
	}
	if msg.Keyboard == nil || msg.Keyboard.InlineKeyboard[0][0].CallbackData != "menu:register" {
		t.Errorf("expected register button, got %+v", msg.Keyboard)
	}
}

func TestSendCabinetLinkedShowsProfileAndActions(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	expire := time.Now().Add(10 * 24 * time.Hour)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{
			RemnawaveID:     42,
			Username:        "ivan",
			TelegramID:      555,
			ExpireAt:        expire,
			Status:          "ACTIVE",
			SubscriptionURL: "https://sub.example/abc",
		}},
	}}

	if !svc.SendCabinet(context.Background(), 555) {
		t.Fatal("expected handled")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(bot.sent))
	}
	msg := bot.sent[0]
	for _, want := range []string{"ivan", "✅ Активна", expire.Format("02.01.2006"), "https://sub.example/abc"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("cabinet text missing %q:\n%s", want, msg.Text)
		}
	}

	var callbacks []string
	for _, row := range msg.Keyboard.InlineKeyboard {
		callbacks = append(callbacks, row[0].CallbackData)
	}
	for _, want := range []string{"cab:pay:42", "menu:gift", "menu:mygifts", "menu:invite", "menu:register"} {
		found := false
		for _, cb := range callbacks {
			if cb == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected button %q, got %v", want, callbacks)
		}
	}

	// The snapshot must be refreshed so cab:pay works without a prior notification.
	u, err := st.GetNotifiedUser(context.Background(), 42)
	if err != nil || u == nil {
		t.Fatalf("expected notified user snapshot, got %v, err=%v", u, err)
	}
	if u.RemnawaveID != 42 || u.TelegramID != 555 {
		t.Errorf("snapshot mismatch: %+v", u)
	}
}

func TestSendCabinetWebAppHidesPayButtons(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.SetWebAppURL("https://app.example")
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 42, Username: "ivan", TelegramID: 555, ExpireAt: time.Now().Add(time.Hour), Status: "ACTIVE"}},
	}}

	if !svc.SendCabinet(context.Background(), 555) {
		t.Fatal("expected handled")
	}
	msg := bot.sent[len(bot.sent)-1]

	hasWebApp := false
	var callbacks []string
	for _, row := range msg.Keyboard.InlineKeyboard {
		for _, btn := range row {
			if btn.WebApp != nil {
				hasWebApp = true
			}
			if btn.CallbackData != "" {
				callbacks = append(callbacks, btn.CallbackData)
			}
		}
	}
	if !hasWebApp {
		t.Error("expected mini app button when webapp is enabled")
	}
	// Renewal, gifts and invites live in the mini app now; only profile
	// linking stays as an inline button.
	if len(callbacks) != 1 || callbacks[0] != "menu:register" {
		t.Errorf("expected only menu:register with webapp enabled, got %v", callbacks)
	}
}

func TestSendCabinetShowsGiftAndInviteSummary(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 42, Username: "ivan", TelegramID: 555, ExpireAt: time.Now().Add(time.Hour), Status: "ACTIVE"}},
	}}
	ctx := context.Background()
	if _, err := st.CreateGiftCode(ctx, store.GiftCode{Code: "C1", BuyerTelegramID: 555, BuyerUsername: "ivan", Months: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInviteRequest(ctx, store.InviteRequest{InviterTelegramID: 555, InviterUsername: "ivan", NewUsername: "new_user", Months: 1}); err != nil {
		t.Fatal(err)
	}

	svc.SendCabinet(ctx, 555)
	text := bot.sent[len(bot.sent)-1].Text
	if !strings.Contains(text, "🎁 Подарки: 1") {
		t.Errorf("expected gift summary, got:\n%s", text)
	}
	if !strings.Contains(text, "👥 Приглашения: 1") {
		t.Errorf("expected invite summary, got:\n%s", text)
	}
}

func TestCabinetPayShowsTariffsWithoutTouchingCabinetMessage(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	if err := st.UpsertTariff(ctx, store.PlanStandard, 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNotifiedUser(ctx, store.NotifiedUser{
		RemnawaveID: 42, Username: "ivan", TelegramID: 555, ExpireAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	cb := &tg.CallbackQuery{
		ID:      "cb1",
		From:    tg.User{ID: 555},
		Data:    "cab:pay:42",
		Message: &tg.Message{MessageID: 7, Chat: tg.Chat{ID: 555}},
	}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("expected handled")
	}
	if len(bot.edits) != 0 {
		t.Errorf("cabinet message must keep its keyboard, got edits: %+v", bot.edits)
	}
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil {
		t.Fatal("expected tariff keyboard")
	}
	if got := last.Keyboard.InlineKeyboard[0][0].CallbackData; got != "pick:42:1" {
		t.Errorf("expected pick:42:1, got %q", got)
	}
	cancel := last.Keyboard.InlineKeyboard[len(last.Keyboard.InlineKeyboard)-1][0]
	if cancel.CallbackData != "cab:cancel" {
		t.Errorf("expected cab:cancel button, got %q", cancel.CallbackData)
	}
}
