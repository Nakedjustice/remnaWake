package payments

import (
	"context"
	"strings"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestSendAdminMenu(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.SendAdminMenu(context.Background(), 1000)

	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(bot.sent))
	}
	m := bot.sent[0]
	if m.ChatID != 1000 {
		t.Fatalf("sent to %d, want adminID 1000", m.ChatID)
	}
	if m.Keyboard == nil || len(m.Keyboard.InlineKeyboard) != 8 {
		t.Fatalf("expected 8 keyboard rows, got %+v", m.Keyboard)
	}
	callbackData := func(row int) string {
		return m.Keyboard.InlineKeyboard[row][0].CallbackData
	}
	for i, want := range []string{"adm:stats", "adm:tariffs", "adm:addtariff", "adm:del_list", "adm:req", "adm:gifts", "adm:setreq", "adm:bcast"} {
		if callbackData(i) != want {
			t.Errorf("row %d: got %q, want %q", i, callbackData(i), want)
		}
	}
}

func TestAdminCommandOpensMenu(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if !svc.HandleAdminCommand(context.Background(), msg(1000, "/admin")) {
		t.Fatal("/admin should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].Keyboard == nil {
		t.Fatalf("expected menu message with keyboard: %+v", bot.sent)
	}
	if !strings.Contains(bot.sent[0].Text, "администратора") {
		t.Fatalf("unexpected menu text: %q", bot.sent[0].Text)
	}
}

func TestAdminMenuIgnoresNonAdmin(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if svc.HandleAdminCommand(context.Background(), msg(2222, "/admin")) {
		t.Fatal("/admin from non-admin should not be handled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("should not reply to non-admin: %+v", bot.sent)
	}
}

// Ensure the test file compiles by referencing tg package usage explicitly.
var _ = tg.InlineKeyboardMarkup{}

func TestAdmMenuCallback(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{
		ID:   "cb1",
		From: tg.User{ID: 1000},
		Data: "adm:menu",
	}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("adm:menu should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].Keyboard == nil {
		t.Fatalf("expected admin menu message: %+v", bot.sent)
	}
}

func TestAdmTariffsCallbackEmpty(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:tariffs"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("adm:tariffs should be handled")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 message: %+v", bot.sent)
	}
	m := bot.sent[0]
	if m.ChatID != 1000 {
		t.Fatalf("sent to wrong chat: %d", m.ChatID)
	}
	// back button present
	if m.Keyboard == nil || m.Keyboard.InlineKeyboard[0][0].CallbackData != "adm:menu" {
		t.Fatalf("expected back button: %+v", m.Keyboard)
	}
}

func TestAdmTariffsCallbackWithTariffs(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, 1, 150)
	_ = st.UpsertTariff(ctx, 3, 450)

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:tariffs"}
	svc.HandleCallback(ctx, cb)

	m := bot.sent[0]
	if !strings.Contains(m.Text, "1 мес.") || !strings.Contains(m.Text, "3 мес.") {
		t.Fatalf("tariff list missing months: %q", m.Text)
	}
}

func TestAdmReqCallbackNotSet(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:req"}
	svc.HandleCallback(context.Background(), cb)

	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не заданы") {
		t.Fatalf("expected not-set notice: %+v", bot.sent)
	}
	if bot.sent[0].Keyboard == nil || bot.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData != "adm:menu" {
		t.Fatalf("expected back button: %+v", bot.sent[0].Keyboard)
	}
}

func TestAdmReqCallbackSet(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.requisites = "Карта 1234"
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:req"}
	svc.HandleCallback(context.Background(), cb)

	if !strings.Contains(bot.sent[0].Text, "Карта 1234") {
		t.Fatalf("expected requisites text: %q", bot.sent[0].Text)
	}
}

func TestAdmDelListCallback(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, 3, 450)

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:del_list"}
	svc.HandleCallback(ctx, cb)

	m := bot.sent[0]
	if m.Keyboard == nil {
		t.Fatal("expected keyboard with tariff buttons")
	}
	// first row = tariff button, last row = back
	first := m.Keyboard.InlineKeyboard[0][0].CallbackData
	if first != "adm:del:3" {
		t.Fatalf("tariff button data = %q, want adm:del:3", first)
	}
	last := m.Keyboard.InlineKeyboard[len(m.Keyboard.InlineKeyboard)-1][0].CallbackData
	if last != "adm:menu" {
		t.Fatalf("back button data = %q", last)
	}
}

func TestAdmDelTariffCallback(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, 3, 450)

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:del:3"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("adm:del:3 should be handled")
	}
	got, _ := st.GetTariff(ctx, 3)
	if got != nil {
		t.Fatal("tariff should be deleted")
	}
	// confirmation + updated del list sent
	if len(bot.sent) < 2 {
		t.Fatalf("expected confirmation + del list messages: %+v", bot.sent)
	}
	if !strings.Contains(bot.sent[0].Text, "удалён") {
		t.Fatalf("expected deletion confirmation: %q", bot.sent[0].Text)
	}
}

func TestAdmCallbacksIgnoreNonAdmin(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	for _, data := range []string{"adm:menu", "adm:tariffs", "adm:req", "adm:del_list"} {
		bot.sent = nil
		cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 9999}, Data: data}
		if !svc.HandleCallback(context.Background(), cb) {
			t.Fatalf("%q should be handled (rejected)", data)
		}
		for _, m := range bot.sent {
			if m.ChatID == 1000 {
				t.Fatalf("%q: should not send to admin when non-admin presses", data)
			}
		}
	}
}

func TestAdmSetReqCallbackStartsFlow(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:setreq"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("adm:setreq should be handled")
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputRequisites {
		t.Fatalf("step = %v, want adminInputRequisites", step)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "реквизитов") {
		t.Fatalf("expected prompt: %+v", bot.sent)
	}
}

func TestAdmSetReqFlow(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:setreq"})
	bot.sent = nil // clear the prompt message

	if !svc.HandleAdminCommand(ctx, msg(1000, "СБП +79001234567")) {
		t.Fatal("requisites text should be captured")
	}
	if bot.sent[0].Text != "Реквизиты сохранены." {
		t.Fatalf("expected save confirmation: %+v", bot.sent)
	}
	got, found, _ := st.GetSetting(ctx, requisitesKey)
	if !found || got != "СБП +79001234567" {
		t.Fatalf("not persisted: %q", got)
	}
}

func TestAdmAddTariffCallbackStartsFlow(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:addtariff"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("adm:addtariff should be handled")
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputTariffMonths {
		t.Fatalf("step = %v, want adminInputTariffMonths", step)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "месяцев") {
		t.Fatalf("expected months prompt: %+v", bot.sent)
	}
}

func TestAdmAddTariffFullFlow(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	// Start flow
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:addtariff"})
	bot.sent = nil

	// Step 1: months
	if !svc.HandleAdminCommand(ctx, msg(1000, "6")) {
		t.Fatal("months should be captured")
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputTariffPrice {
		t.Fatalf("step after months = %v, want adminInputTariffPrice", step)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "цену") {
		t.Fatalf("expected price prompt: %+v", bot.sent)
	}
	bot.sent = nil

	// Step 2: price
	if !svc.HandleAdminCommand(ctx, msg(1000, "900")) {
		t.Fatal("price should be captured")
	}
	if !strings.Contains(bot.sent[0].Text, "6 мес.") || !strings.Contains(bot.sent[0].Text, "900") {
		t.Fatalf("expected save confirmation: %q", bot.sent[0].Text)
	}

	// Verify persisted
	got, _ := st.GetTariff(ctx, 6)
	if got == nil || got.Price != 900 {
		t.Fatalf("tariff not persisted: %+v", got)
	}

	// State reset
	svc.mu.Lock()
	finalStep := svc.adminInput[1000].step
	svc.mu.Unlock()
	if finalStep != adminInputNone {
		t.Fatalf("state not reset: %v", finalStep)
	}
}

func TestAdmAddTariffInvalidInput(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()

	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:addtariff"})
	bot.sent = nil

	// Invalid months
	if !svc.HandleAdminCommand(ctx, msg(1000, "zero")) {
		t.Fatal("invalid months should still be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "≥ 1") {
		t.Fatalf("expected validation error: %+v", bot.sent)
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputTariffMonths {
		t.Fatalf("step should remain adminInputTariffMonths, got %v", step)
	}
}

func TestAdmInputFlowCancelledByCommand(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:addtariff"})

	// Admin sends a "/" command mid-flow — flow is not consumed, command is dispatched normally.
	if svc.HandleAdminCommand(ctx, msg(1000, "/tariffs")) {
		// /tariffs is handled via cmdListTariffs — that's fine
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	// The "/" command was NOT captured by consumeAdminInput, so step is unchanged.
	if step != adminInputTariffMonths {
		t.Fatalf("step should remain adminInputTariffMonths when a command interrupts, got %v", step)
	}
}
