package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

func pendingPayment(t *testing.T, st *store.Store, username string) int64 {
	t.Helper()
	id, err := st.CreatePaymentRequest(context.Background(), store.PaymentRequest{
		RemnawaveID: 1, UUID: "u-" + username, Username: username, TelegramID: 777,
		Months: 3, Price: 450, ExpireAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create payment request: %v", err)
	}
	return id
}

func pendingGift(t *testing.T, st *store.Store, code string) int64 {
	t.Helper()
	id, err := st.CreateGiftCode(context.Background(), store.GiftCode{
		Code: code, BuyerTelegramID: 555, BuyerUsername: "buyer", Months: 1, Price: 100,
	})
	if err != nil {
		t.Fatalf("create gift code: %v", err)
	}
	return id
}

func TestPendingSummaryCountsAndDrilldownButtons(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	pendingPayment(t, st, "alice")
	pendingGift(t, st, "g1")
	pendingGift(t, st, "g2")
	if _, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 555, InviterUsername: "buyer", NewUsername: "newbie", Months: 1, Price: 100,
	}); err != nil {
		t.Fatalf("create invite request: %v", err)
	}
	if _, err := st.CreateTrialRequest(ctx, 999, "trialuser", time.Now()); err != nil {
		t.Fatalf("create trial request: %v", err)
	}

	svc.sendPendingSummary(ctx, 1000)

	if len(bot.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(bot.sent))
	}
	m := bot.sent[0]
	for _, want := range []string{
		"Заявки на оплату ждут решения — 1",
		"Заявки на подарки ждут решения — 2",
		"Приглашения ждут одобрения — 1",
		"Пробные периоды ждут одобрения — 1",
	} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("summary text missing %q:\n%s", want, m.Text)
		}
	}
	data := keyboardData(m.Keyboard)
	for _, want := range []string{"adm:pd:pay", "adm:pd:gift", "adm:pd:inv", "adm:pd:tr"} {
		if !data[want] {
			t.Errorf("keyboard missing %q, got %v", want, data)
		}
	}
	// Button labels carry the counts.
	labels := ""
	for _, row := range m.Keyboard.InlineKeyboard {
		for _, b := range row {
			labels += b.Text + "\n"
		}
	}
	if !strings.Contains(labels, "(2)") {
		t.Errorf("gift button label should carry count 2, got:\n%s", labels)
	}
}

func TestPendingSummaryEmptyState(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.sendPendingSummary(context.Background(), 1000)
	if len(bot.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].Text, "Всё спокойно") {
		t.Fatalf("empty state text = %q", bot.sent[0].Text)
	}
	if got := keyboardData(bot.sent[0].Keyboard); len(got) != 0 {
		t.Fatalf("empty state should have no drill-down buttons, got %v", got)
	}
}

func TestPendingCategoryPaymentsCapAndOverflow(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		pendingPayment(t, st, fmt.Sprintf("user%d", i))
	}

	svc.sendPendingCategory(ctx, 1000, "pay")

	if len(bot.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(bot.sent))
	}
	m := bot.sent[0]
	data := keyboardData(m.Keyboard)
	for i := 1; i <= 10; i++ {
		if !data[fmt.Sprintf("ok:%d", i)] || !data[fmt.Sprintf("rej:%d", i)] {
			t.Errorf("missing buttons for request %d: %v", i, data)
		}
	}
	if data["ok:11"] {
		t.Error("list must cap at 10 oldest requests")
	}
	if !strings.Contains(m.Text, "и ещё 2") {
		t.Errorf("overflow note missing:\n%s", m.Text)
	}
	if !strings.Contains(m.Text, "user1") {
		t.Errorf("oldest request missing from text:\n%s", m.Text)
	}
}

func TestPendingCategoryGiftInviteTrial(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	pendingGift(t, st, "g1")
	if _, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 555, InviterUsername: "buyer", NewUsername: "newbie", Months: 2, Price: 300,
	}); err != nil {
		t.Fatalf("create invite request: %v", err)
	}
	if _, err := st.CreateTrialRequest(ctx, 999, "trialuser", time.Now()); err != nil {
		t.Fatalf("create trial request: %v", err)
	}

	for _, tc := range []struct {
		cat, okData, rejData, wantText string
	}{
		{"gift", "gc_ok:1", "gc_rej:1", "buyer"},
		{"inv", "inv_ok:1", "inv_rej:1", "newbie"},
		{"tr", "tr_ok:1", "tr_rej:1", "trialuser"},
	} {
		bot.sent = nil
		svc.sendPendingCategory(ctx, 1000, tc.cat)
		if len(bot.sent) != 1 {
			t.Fatalf("%s: sent = %d, want 1", tc.cat, len(bot.sent))
		}
		m := bot.sent[0]
		data := keyboardData(m.Keyboard)
		if !data[tc.okData] || !data[tc.rejData] {
			t.Errorf("%s: missing %s/%s buttons: %v", tc.cat, tc.okData, tc.rejData, data)
		}
		if !strings.Contains(m.Text, tc.wantText) {
			t.Errorf("%s: text missing %q:\n%s", tc.cat, tc.wantText, m.Text)
		}
	}

	bot.sent = nil
	svc.sendPendingCategory(ctx, 1000, "pay")
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "Нет ожидающих заявок") {
		t.Fatalf("empty category message = %+v", bot.sent)
	}
}

func TestPendingCategoryListRecoversOrphanedConfirm(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	pendingPayment(t, st, "alice")
	if _, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 2, UUID: "u-bob", Username: "bob", TelegramID: 778,
		Kind: store.PaymentKindTrafficExtension, TrafficGB: 50, Price: 200, ExpireAt: time.Now(),
	}); err != nil {
		t.Fatalf("create traffic request: %v", err)
	}

	// The list is rendered from the store alone — no in-memory refs needed,
	// which is exactly the after-restart situation.
	svc.sendPendingCategory(ctx, 1000, "pay")
	if len(bot.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].Text, "50 ГБ") {
		t.Errorf("traffic extension line missing GB:\n%s", bot.sent[0].Text)
	}

	if !svc.HandleCallback(ctx, cbq(1000, "ok:1")) {
		t.Fatal("ok: callback should be handled")
	}
	if ext.calls != 1 {
		t.Fatalf("extension calls = %d, want 1", ext.calls)
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req.Status != "confirmed" {
		t.Fatalf("request status = %q, want confirmed", req.Status)
	}
}

func TestPendingCommandCallbackAndMenuWiring(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	pendingPayment(t, st, "alice")

	if svc.HandleAdminCommand(ctx, msg(2222, "/pending")) {
		t.Fatal("non-admin /pending must not be handled")
	}
	if !svc.HandleAdminCommand(ctx, msg(1000, "/pending")) {
		t.Fatal("/pending should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "Центр действий") {
		t.Fatalf("/pending reply = %+v", bot.sent)
	}

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:pending")) {
		t.Fatal("adm:pending should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "Центр действий") {
		t.Fatalf("adm:pending reply = %+v", bot.sent)
	}

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:pd:pay")) {
		t.Fatal("adm:pd:pay should be handled")
	}
	if len(bot.sent) != 1 || !keyboardData(bot.sent[0].Keyboard)["ok:1"] {
		t.Fatalf("adm:pd:pay reply = %+v", bot.sent)
	}

	bot.sent = nil
	svc.SendAdminMenu(ctx, 1000)
	if len(bot.sent) != 1 || !keyboardData(bot.sent[0].Keyboard)["adm:pending"] {
		t.Fatal("admin menu should carry the adm:pending button")
	}
}

func TestPendingSummaryReportsDegradedSources(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.finder = &fakeFinder{listErr: errors.New("panel down")}
	svc.sendPendingSummary(context.Background(), 1000)
	if len(bot.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].Text, "remnawave") {
		t.Fatalf("degraded source note missing:\n%s", bot.sent[0].Text)
	}
}
