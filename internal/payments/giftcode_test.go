package payments

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestGenerateGiftCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		code, err := generateGiftCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(code) != giftCodeLen {
			t.Fatalf("length = %d, want %d", len(code), giftCodeLen)
		}
		for _, r := range code {
			if !strings.ContainsRune(giftCodeAlphabet, r) {
				t.Fatalf("code %q contains %q outside alphabet", code, r)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code after %d iterations: %s", i, code)
		}
		seen[code] = true
	}
}

func seedBuyer(svc *Service, tgID int64) {
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		tgID: {{RemnawaveID: 1, UUID: "u-buyer", Username: "buyer", TelegramID: tgID,
			ExpireAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}}
}

func TestGiftCodeFlowRejectsNonSubscriber(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()

	if !svc.StartGiftCodeFlow(ctx, msg(555, "/gift")) {
		t.Fatal("/gift should be handled")
	}
	if svc.getGiftCode(555) != nil {
		t.Fatal("flow must not start for non-subscriber")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "только подписчикам") {
		t.Fatalf("expected subscriber-only reply: %+v", bot.sent)
	}
}

func TestGiftCodePurchaseHappyPath(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.StartGiftCodeFlow(ctx, msg(555, "/gift")) {
		t.Fatal("/gift should be handled")
	}
	last := bot.sent[len(bot.sent)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "gc_pick:3" {
		t.Fatalf("expected tariff keyboard: %+v", last)
	}

	pick := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:3"}
	if !svc.HandleCallback(ctx, pick) {
		t.Fatal("gc_pick should be handled")
	}

	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")
	if len(gifts) != 1 || gifts[0].Months != 3 || gifts[0].Price != 450 ||
		gifts[0].BuyerTelegramID != 555 || gifts[0].BuyerUsername != "buyer" {
		t.Fatalf("pending gift wrong: %+v", gifts)
	}
	if svc.getGiftCode(555) != nil {
		t.Fatal("purchase state must be cleared after pick")
	}

	var adminGot bool
	for _, m := range bot.sent {
		if m.ChatID == 1000 && m.Keyboard != nil &&
			m.Keyboard.InlineKeyboard[0][0].CallbackData == fmt.Sprintf("gc_ok:%d", gifts[0].ID) {
			adminGot = true
		}
	}
	if !adminGot {
		t.Fatalf("admin not notified with approve button: %+v", bot.sent)
	}
}

func TestGiftCodeApproveSendsDeepLink(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	svc.SetBotUsername("testbot")
	_ = st.UpsertTariff(ctx, 3, 450)

	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:3"})
	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")
	giftID := gifts[0].ID

	// Non-admin cannot approve.
	if !svc.HandleCallback(ctx, cbq(2222, fmt.Sprintf("gc_ok:%d", giftID))) {
		t.Fatal("should be handled (rejected)")
	}
	if g, _ := st.GetGiftCode(ctx, giftID); g.Status != "pending" {
		t.Fatalf("non-admin must not issue: %+v", g)
	}

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", giftID))) {
		t.Fatal("gc_ok should be handled")
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "issued" {
		t.Fatalf("status = %s, want issued", g.Status)
	}

	var buyerMsg string
	for _, m := range bot.sent {
		if m.ChatID == 555 {
			buyerMsg = m.Text
		}
	}
	want := "https://t.me/testbot?start=gift_" + g.Code
	if !strings.Contains(buyerMsg, want) {
		t.Fatalf("buyer message missing deep link %q: %q", want, buyerMsg)
	}

	// Second approve is a no-op.
	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", giftID))) {
		t.Fatal("second gc_ok should be handled")
	}
	if last := bot.answers[len(bot.answers)-1]; !strings.Contains(last, "уже обработана") {
		t.Fatalf("expected already-processed answer, got %q", last)
	}
}

func TestGiftCodeApproveFallsBackWithoutBotUsername(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:1"})
	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")

	bot.sent = nil
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", gifts[0].ID)))
	var buyerMsg string
	for _, m := range bot.sent {
		if m.ChatID == 555 {
			buyerMsg = m.Text
		}
	}
	if !strings.Contains(buyerMsg, "/start gift_"+gifts[0].Code) {
		t.Fatalf("expected raw-code fallback, got %q", buyerMsg)
	}
}

func TestGiftCodeReject(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:1"})
	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_rej:%d", gifts[0].ID))) {
		t.Fatal("gc_rej should be handled")
	}
	g, _ := st.GetGiftCode(ctx, gifts[0].ID)
	if g.Status != "rejected" {
		t.Fatalf("status = %s, want rejected", g.Status)
	}
	var buyerTold bool
	for _, m := range bot.sent {
		if m.ChatID == 555 && strings.Contains(m.Text, "отклонена") {
			buyerTold = true
		}
	}
	if !buyerTold {
		t.Fatalf("buyer not told about rejection: %+v", bot.sent)
	}
}

// buyGift runs the purchase flow for buyer 555 and returns the gift ID.
func buyGift(t *testing.T, svc *Service, st *store.Store, months int) int64 {
	t.Helper()
	ctx := context.Background()
	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: fmt.Sprintf("gc_pick:%d", months)})
	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")
	return gifts[len(gifts)-1].ID
}

func TestSendMyGiftsEmpty(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if !svc.SendMyGifts(context.Background(), 555) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не покупали") {
		t.Fatalf("expected empty-list reply: %+v", bot.sent)
	}
}

func TestSendMyGiftsShowsStatusesAndLinkButton(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	pendingID := buyGift(t, svc, st, 1)
	issuedID := buyGift(t, svc, st, 1)
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", issuedID)))

	bot.sent = nil
	if !svc.SendMyGifts(ctx, 555) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected one list message: %+v", bot.sent)
	}
	text := bot.sent[0].Text
	if !strings.Contains(text, "Ожидает подтверждения оплаты") ||
		!strings.Contains(text, "Выдан, ожидает активации") {
		t.Fatalf("statuses missing from list: %q", text)
	}

	kb := bot.sent[0].Keyboard
	if kb == nil {
		t.Fatalf("expected resend keyboard: %+v", bot.sent[0])
	}
	found := map[string]bool{}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			found[btn.CallbackData] = true
		}
	}
	if !found[fmt.Sprintf("gc_link:%d", issuedID)] {
		t.Fatalf("issued gift must have a link button: %+v", kb)
	}
	if found[fmt.Sprintf("gc_link:%d", pendingID)] {
		t.Fatalf("pending gift must not have a link button: %+v", kb)
	}

	// Another user sees nothing of buyer 555's gifts.
	bot.sent = nil
	svc.SendMyGifts(ctx, 777)
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не покупали") {
		t.Fatalf("other user must get empty list: %+v", bot.sent)
	}
}

func TestGiftCodeResendLink(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	svc.SetBotUsername("testbot")

	giftID := buyGift(t, svc, st, 3)

	// Pending gift: link not available yet.
	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(555, fmt.Sprintf("gc_link:%d", giftID))) {
		t.Fatal("gc_link should be handled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("pending gift must not be resent: %+v", bot.sent)
	}

	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", giftID)))
	g, _ := st.GetGiftCode(ctx, giftID)

	// Someone other than the buyer must be refused.
	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(777, fmt.Sprintf("gc_link:%d", giftID))) {
		t.Fatal("gc_link should be handled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("non-buyer must not receive the link: %+v", bot.sent)
	}

	// Buyer gets the deep link again.
	if !svc.HandleCallback(ctx, cbq(555, fmt.Sprintf("gc_link:%d", giftID))) {
		t.Fatal("gc_link should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].ChatID != 555 ||
		!strings.Contains(bot.sent[0].Text, "https://t.me/testbot?start=gift_"+g.Code) {
		t.Fatalf("buyer must get the deep link again: %+v", bot.sent)
	}
}

func TestAdminGiftListAndRevoke(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:1"})
	gifts, _ := st.ListGiftCodesByStatus(ctx, "pending")
	giftID := gifts[0].ID
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", giftID)))

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:gifts")) {
		t.Fatal("adm:gifts should be handled")
	}
	list := bot.sent[len(bot.sent)-1]
	if list.Keyboard == nil ||
		list.Keyboard.InlineKeyboard[0][0].CallbackData != fmt.Sprintf("adm:grev:%d", giftID) {
		t.Fatalf("expected revoke button: %+v", list)
	}

	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("adm:grev:%d", giftID))) {
		t.Fatal("adm:grev should be handled")
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "revoked" {
		t.Fatalf("status = %s, want revoked", g.Status)
	}
}
