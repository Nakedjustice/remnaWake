package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// issueGift seeds an issued gift code bought by buyerTGID and returns it.
func issueGift(t *testing.T, st *store.Store, buyerTGID int64, months int) *store.GiftCode {
	t.Helper()
	ctx := context.Background()
	code, err := generateGiftCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	id, err := st.CreateGiftCode(ctx, store.GiftCode{
		Code: code, BuyerTelegramID: buyerTGID, BuyerUsername: "buyer", Months: months,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ok, err := st.IssueGiftCode(ctx, id, time.Now()); err != nil || !ok {
		t.Fatalf("issue: %v %v", ok, err)
	}
	g, _ := st.GetGiftCode(ctx, id)
	return g
}

// confirmRedeem presses the "Активировать" button on the single-profile
// confirmation prompt.
func confirmRedeem(t *testing.T, svc *Service, chatID int64) {
	t.Helper()
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: chatID},
		Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: chatID}}, Data: "gc_use:0"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("gc_use:0 should be handled")
	}
}

func TestRedeemUnknownCode(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.StartGiftRedemption(context.Background(), 700, "WRONGFORMAT")
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не найден") {
		t.Fatalf("expected not-found reply: %+v", bot.sent)
	}
}

func TestRedeemPendingCode(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	code, _ := generateGiftCode()
	_, _ = st.CreateGiftCode(ctx, store.GiftCode{Code: code, BuyerTelegramID: 1, Months: 1})

	svc.StartGiftRedemption(ctx, 700, code)
	if !strings.Contains(bot.sent[0].Text, "ещё не подтверждена") {
		t.Fatalf("expected pending reply: %+v", bot.sent)
	}
}

func TestRedeemExtendsExistingProfile(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	exp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) // future -> base is expiry
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		700: {{RemnawaveID: 7, UUID: "u-700", Username: "bob", TelegramID: 700, ExpireAt: exp}},
	}}
	g := issueGift(t, st, 555, 3)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	if ext.calls != 0 {
		t.Fatal("must not extend before confirmation")
	}
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "gc_use:0" {
		t.Fatalf("expected confirmation keyboard: %+v", last)
	}
	confirmRedeem(t, svc, 700)

	if ext.calls != 1 || ext.uuid != "u-700" {
		t.Fatalf("extend not called: calls=%d uuid=%s", ext.calls, ext.uuid)
	}
	if want := exp.AddDate(0, 3, 0); !ext.expire.Equal(want) {
		t.Fatalf("new expiry = %s, want %s", ext.expire, want)
	}
	got, _ := st.GetGiftCode(ctx, g.ID)
	if got.Status != "redeemed" || got.RedeemerTelegramID != 700 || got.RedeemedUsername != "bob" {
		t.Fatalf("not redeemed: %+v", got)
	}

	var redeemerTold, buyerTold bool
	for _, m := range bot.sent {
		if m.ChatID == 700 && strings.Contains(m.Text, "активирован") {
			redeemerTold = true
		}
		if m.ChatID == 555 && strings.Contains(m.Text, "активирован") {
			buyerTold = true
		}
	}
	if !redeemerTold || !buyerTold {
		t.Fatalf("missing confirmations (redeemer=%v buyer=%v): %+v", redeemerTold, buyerTold, bot.sent)
	}

	// Second redemption attempt is blocked.
	bot.sent = nil
	svc.StartGiftRedemption(ctx, 701, g.Code)
	if ext.calls != 1 {
		t.Fatalf("second redeem must not extend, calls=%d", ext.calls)
	}
	if !strings.Contains(bot.sent[0].Text, "уже был активирован") {
		t.Fatalf("expected already-redeemed reply: %+v", bot.sent)
	}
}

func TestRedeemMultiProfilePick(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	expA := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	expB := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		700: {
			{RemnawaveID: 7, UUID: "u-a", Username: "bob_a", TelegramID: 700, ExpireAt: expA},
			{RemnawaveID: 8, UUID: "u-b", Username: "bob_b", TelegramID: 700, ExpireAt: expB},
		},
	}}
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[1][0].CallbackData != "gc_use:1" {
		t.Fatalf("expected profile keyboard: %+v", last)
	}
	if ext.calls != 0 {
		t.Fatal("must not extend before choice")
	}

	pick := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 700},
		Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: 700}}, Data: "gc_use:1"}
	if !svc.HandleCallback(ctx, pick) {
		t.Fatal("gc_use should be handled")
	}
	if ext.calls != 1 || ext.uuid != "u-b" {
		t.Fatalf("expected chosen profile extended: calls=%d uuid=%s", ext.calls, ext.uuid)
	}
}

func TestRedeemCreatesNewProfile(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	creator := &fakeCreator{}
	registrar := &fakeRegistrar{}
	svc.creator = creator
	svc.registrar = registrar
	// Redeemer has no profiles; "taken" is an occupied username.
	svc.finder = &fakeFinder{
		byTG:   map[int64][]Subscriber{},
		byName: map[string]*Subscriber{"taken": {Username: "taken"}},
	}
	g := issueGift(t, st, 555, 2)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	if !strings.Contains(bot.sent[len(bot.sent)-1].Text, "имя пользователя") {
		t.Fatalf("expected username prompt: %+v", bot.sent)
	}

	// Invalid then taken names re-prompt without consuming the code.
	if !svc.HandleText(ctx, msg(700, "x")) {
		t.Fatal("invalid name should be consumed")
	}
	if !svc.HandleText(ctx, msg(700, "taken")) {
		t.Fatal("taken name should be consumed")
	}
	if !strings.Contains(bot.sent[len(bot.sent)-1].Text, "занято") {
		t.Fatalf("expected taken-name reply: %+v", bot.sent)
	}
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "issued" {
		t.Fatalf("code must stay issued during prompts: %+v", got)
	}

	if !svc.HandleText(ctx, msg(700, "newbie")) {
		t.Fatal("valid name should be consumed")
	}
	if len(creator.created) != 1 || creator.created[0] != "newbie" {
		t.Fatalf("create not called: %+v", creator.created)
	}
	// No squad selected → the Default-Squad fallback from the lister applies.
	if len(creator.squads) != 1 || len(creator.squads[0]) != 1 || creator.squads[0][0] != "default-squad-uuid" {
		t.Fatalf("default squad not passed to create: %+v", creator.squads)
	}
	if registrar.calls != 1 || registrar.uuid != "fake-uuid" || registrar.telegramID != 700 {
		t.Fatalf("registrar not called correctly: %+v", registrar)
	}
	if ext.calls != 0 {
		t.Fatal("extend must not be called on create path")
	}
	got, _ := st.GetGiftCode(ctx, g.ID)
	if got.Status != "redeemed" || got.RedeemedUsername != "newbie" {
		t.Fatalf("not redeemed: %+v", got)
	}
	if svc.getRedeem(700) != nil {
		t.Fatal("redeem state must be cleared")
	}
}

func TestRedeemSelfSuppressesBuyerNotification(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 7, UUID: "u-555", Username: "buyer", TelegramID: 555,
			ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 555, g.Code)
	confirmRedeem(t, svc, 555)
	for _, m := range bot.sent {
		if strings.Contains(m.Text, "Ваш подарочный код") {
			t.Fatalf("buyer notification must be suppressed on self-redeem: %+v", bot.sent)
		}
	}
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "redeemed" {
		t.Fatalf("self-redeem must work: %+v", got)
	}
}

func TestRedeemDryRunMarksRedeemedWithoutPanelCalls(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	svc.dryRun = true
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		700: {{RemnawaveID: 7, UUID: "u-700", Username: "bob", TelegramID: 700,
			ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	confirmRedeem(t, svc, 700)
	if ext.calls != 0 {
		t.Fatal("dry-run must not call the panel")
	}
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "redeemed" {
		t.Fatalf("dry-run must still mark redeemed: %+v", got)
	}
	var dryRunTold bool
	for _, m := range bot.sent {
		if m.ChatID == 700 && strings.Contains(m.Text, "dry-run") {
			dryRunTold = true
		}
	}
	if !dryRunTold {
		t.Fatalf("expected dry-run confirmation: %+v", bot.sent)
	}
}

func TestRedeemSingleProfileCancelKeepsCodeIssued(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		700: {{RemnawaveID: 7, UUID: "u-700", Username: "bob", TelegramID: 700,
			ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	cancel := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 700},
		Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: 700}}, Data: "gc_redeem_cancel"}
	if !svc.HandleCallback(ctx, cancel) {
		t.Fatal("gc_redeem_cancel should be handled")
	}
	if ext.calls != 0 {
		t.Fatal("cancel must not extend")
	}
	if svc.getRedeem(700) != nil {
		t.Fatal("redeem state must be cleared")
	}
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "issued" {
		t.Fatalf("code must stay issued after cancel: %+v", got)
	}

	// The link still works: opening it again shows the confirmation anew.
	bot.sent = nil
	svc.StartGiftRedemption(ctx, 700, g.Code)
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "gc_use:0" {
		t.Fatalf("expected confirmation keyboard again: %+v", last)
	}
}

func TestRedeemCancelKeepsCodeIssued(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{}}
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	if svc.getRedeem(700) == nil {
		t.Fatal("redeem state expected")
	}
	if !svc.HandleText(ctx, msg(700, "/cancel")) {
		t.Fatal("/cancel should be consumed")
	}
	if svc.getRedeem(700) != nil {
		t.Fatal("redeem state must be cleared")
	}
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "issued" {
		t.Fatalf("code must stay issued after cancel: %+v", got)
	}
	_ = bot
}
