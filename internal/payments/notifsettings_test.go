package payments

import (
	"context"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestNotifTogglePersistsAndRerenders(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 777},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 777}}, Data: "notif:expiry"}

	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("notif:expiry should be handled")
	}
	if em, _, _ := st.GetNotificationPrefs(ctx, 777); !em {
		t.Fatal("expiry should be muted after the first toggle")
	}
	if len(bot.edits) == 0 {
		t.Fatal("expected the card to be re-rendered in place")
	}
	last := bot.edits[len(bot.edits)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "notif:expiry" {
		t.Fatalf("unexpected re-rendered keyboard: %+v", last.Keyboard)
	}

	// A second toggle flips it back; winback is untouched throughout.
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("second notif:expiry should be handled")
	}
	em, wm, _ := st.GetNotificationPrefs(ctx, 777)
	if em {
		t.Fatal("expiry should be unmuted after the second toggle")
	}
	if wm {
		t.Fatal("winback should never have been muted")
	}
}
