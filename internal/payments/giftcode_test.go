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
		tgID: {{RemnawaveID: 1, Username: "buyer", TelegramID: tgID,
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
	_ = st.UpsertTariff(ctx, store.PlanStandard, 3, 450)

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
	_ = st.UpsertTariff(ctx, store.PlanStandard, 3, 450)

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

// mgCb builds a callback from chat owner chatID pressing a my-gifts button.
func mgCb(chatID int64, data string) *tg.CallbackQuery {
	return &tg.CallbackQuery{ID: "c", From: tg.User{ID: chatID},
		Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: chatID}}, Data: data}
}

// keyboardData flattens all callback datas of a keyboard.
func keyboardData(kb *tg.InlineKeyboardMarkup) map[string]bool {
	found := map[string]bool{}
	if kb == nil {
		return found
	}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			found[btn.CallbackData] = true
		}
	}
	return found
}

func TestSendMyGiftsShowsStatusMenu(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	buyGift(t, svc, st, 1)
	issuedID := buyGift(t, svc, st, 1)
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", issuedID)))

	bot.sent = nil
	if !svc.SendMyGifts(ctx, 555) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "Выберите статус") {
		t.Fatalf("expected status menu: %+v", bot.sent)
	}
	found := keyboardData(bot.sent[0].Keyboard)
	for _, st := range myGiftsStatuses {
		if !found[fmt.Sprintf("mg:list:%s:0", st.status)] {
			t.Fatalf("menu missing status button %s: %+v", st.status, found)
		}
	}
	// Counts: 1 pending, 1 issued.
	var labels []string
	for _, row := range bot.sent[0].Keyboard.InlineKeyboard {
		labels = append(labels, row[0].Text)
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "Ожидают оплаты (1)") || !strings.Contains(joined, "Выданные (1)") {
		t.Fatalf("menu buttons missing counts: %q", joined)
	}

	// Another user has no gifts at all.
	bot.sent = nil
	svc.SendMyGifts(ctx, 777)
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "не покупали") {
		t.Fatalf("other user must get empty reply: %+v", bot.sent)
	}
}

func TestMyGiftsListPageShowsGiftsAndLinkButtons(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	pendingID := buyGift(t, svc, st, 1)
	issuedID := buyGift(t, svc, st, 1)
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", issuedID)))

	bot.edits = nil
	if !svc.HandleCallback(ctx, mgCb(555, "mg:list:issued:0")) {
		t.Fatal("mg:list should be handled")
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected message edit: %+v", bot.edits)
	}
	edit := bot.edits[0]
	if !strings.Contains(edit.Text, "Выдан, ожидает активации") {
		t.Fatalf("issued page missing status: %q", edit.Text)
	}
	found := keyboardData(edit.Keyboard)
	if !found[fmt.Sprintf("gc_link:%d", issuedID)] {
		t.Fatalf("issued gift must have a link button: %+v", found)
	}
	if found[fmt.Sprintf("gc_link:%d", pendingID)] {
		t.Fatalf("pending gift must not appear on issued page: %+v", found)
	}
	if !found["mg:menu"] {
		t.Fatalf("page must have a back-to-menu button: %+v", found)
	}

	// Single page: no nav row.
	if found["mg:noop"] {
		t.Fatalf("single page must not show nav row: %+v", found)
	}

	// Empty status shows the empty text with a back button.
	bot.edits = nil
	svc.HandleCallback(ctx, mgCb(555, "mg:list:revoked:0"))
	if len(bot.edits) != 1 || !strings.Contains(bot.edits[0].Text, "подарков нет") {
		t.Fatalf("expected empty-status page: %+v", bot.edits)
	}
}

func TestMyGiftsListPagination(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	// 7 pending gifts -> 2 pages (5 + 2).
	for i := 0; i < 7; i++ {
		buyGift(t, svc, st, 1)
	}

	bot.edits = nil
	svc.HandleCallback(ctx, mgCb(555, "mg:list:pending:0"))
	page1 := bot.edits[0]
	if !strings.Contains(page1.Text, "стр. 1/2") {
		t.Fatalf("expected page 1/2 header: %q", page1.Text)
	}
	if n := strings.Count(page1.Text, "заявка от"); n != 5 {
		t.Fatalf("page 1 must show 5 gifts, got %d: %q", n, page1.Text)
	}
	found := keyboardData(page1.Keyboard)
	if !found["mg:list:pending:1"] {
		t.Fatalf("page 1 must have a next button: %+v", found)
	}
	if found["mg:list:pending:-1"] {
		t.Fatalf("page 1 must not have a prev button: %+v", found)
	}

	bot.edits = nil
	svc.HandleCallback(ctx, mgCb(555, "mg:list:pending:1"))
	page2 := bot.edits[0]
	if !strings.Contains(page2.Text, "стр. 2/2") {
		t.Fatalf("expected page 2/2 header: %q", page2.Text)
	}
	if n := strings.Count(page2.Text, "заявка от"); n != 2 {
		t.Fatalf("page 2 must show 2 gifts, got %d: %q", n, page2.Text)
	}
	found = keyboardData(page2.Keyboard)
	if !found["mg:list:pending:0"] {
		t.Fatalf("page 2 must have a prev button: %+v", found)
	}

	// Out-of-range page falls back to the first page.
	bot.edits = nil
	svc.HandleCallback(ctx, mgCb(555, "mg:list:pending:9"))
	if !strings.Contains(bot.edits[0].Text, "стр. 1/2") {
		t.Fatalf("out-of-range page must fall back to page 1: %q", bot.edits[0].Text)
	}
}

func TestMyGiftsBackToMenuEditsMessage(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	buyGift(t, svc, st, 1)

	svc.HandleCallback(ctx, mgCb(555, "mg:list:pending:0"))
	bot.edits = nil
	if !svc.HandleCallback(ctx, mgCb(555, "mg:menu")) {
		t.Fatal("mg:menu should be handled")
	}
	if len(bot.edits) != 1 || !strings.Contains(bot.edits[0].Text, "Выберите статус") {
		t.Fatalf("expected menu restored in-place: %+v", bot.edits)
	}
	found := keyboardData(bot.edits[0].Keyboard)
	if !found["mg:list:pending:0"] {
		t.Fatalf("menu must have status buttons: %+v", found)
	}
}

func TestGiftCodePickEditsMessageWithConfirmation(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)

	svc.StartGiftCodeFlow(ctx, msg(555, "/gift"))
	bot.edits = nil
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "c", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 555}}, Data: "gc_pick:1"})
	if len(bot.edits) != 1 {
		t.Fatalf("expected tariff message edited: %+v", bot.edits)
	}
	e := bot.edits[0]
	if e.ChatID != 555 || e.MessageID != 5 ||
		!strings.Contains(e.Text, "отправлена администратору") || e.Keyboard != nil {
		t.Fatalf("expected confirmation text without keyboard: %+v", e)
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

	// Level 1: buyer picker lists the buyer with a drill-down button.
	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:gifts")) {
		t.Fatal("adm:gifts should be handled")
	}
	picker := keyboardData(bot.sent[len(bot.sent)-1].Keyboard)
	if !picker["adm:gbuyer:555"] {
		t.Fatalf("expected buyer drill-down button: %+v", picker)
	}

	// Level 2: only the Not used bucket exists for this buyer.
	bot.edits = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:gbuyer:555")) {
		t.Fatal("adm:gbuyer should be handled")
	}
	buckets := keyboardData(bot.edits[len(bot.edits)-1].Keyboard)
	if !buckets["adm:glist:555:issued:0"] {
		t.Fatalf("expected Not used bucket: %+v", buckets)
	}
	if buckets["adm:glist:555:redeemed:0"] {
		t.Fatalf("Used bucket must be hidden when empty: %+v", buckets)
	}

	// Level 3: leaf list exposes the revoke button.
	bot.edits = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:glist:555:issued:0")) {
		t.Fatal("adm:glist should be handled")
	}
	leaf := keyboardData(bot.edits[len(bot.edits)-1].Keyboard)
	if !leaf[fmt.Sprintf("adm:grev:%d", giftID)] {
		t.Fatalf("expected revoke button: %+v", leaf)
	}

	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("adm:grev:%d", giftID))) {
		t.Fatal("adm:grev should be handled")
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "revoked" {
		t.Fatalf("status = %s, want revoked", g.Status)
	}
}

func TestAdminGiftUsedBucket(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedBuyer(svc, 555)
	id := buyGift(t, svc, st, 1)
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("gc_ok:%d", id)))
	g, _ := st.GetGiftCode(ctx, id)
	if ok, err := st.RedeemGiftCode(ctx, g.Code, 888, "carol", time.Now()); err != nil || !ok {
		t.Fatalf("redeem: %v %v", ok, err)
	}

	// Buckets: Used present, Not used hidden.
	bot.edits = nil
	svc.HandleCallback(ctx, cbq(1000, "adm:gbuyer:555"))
	buckets := keyboardData(bot.edits[len(bot.edits)-1].Keyboard)
	if !buckets["adm:glist:555:redeemed:0"] {
		t.Fatalf("expected Used bucket: %+v", buckets)
	}
	if buckets["adm:glist:555:issued:0"] {
		t.Fatalf("Not used bucket must be hidden when empty: %+v", buckets)
	}

	// Used leaf names the redeemer and offers no revoke button.
	bot.edits = nil
	svc.HandleCallback(ctx, cbq(1000, "adm:glist:555:redeemed:0"))
	leaf := bot.edits[len(bot.edits)-1]
	if !strings.Contains(leaf.Text, "carol") {
		t.Fatalf("used leaf should name the redeemer: %q", leaf.Text)
	}
	for k := range keyboardData(leaf.Keyboard) {
		if strings.HasPrefix(k, "adm:grev:") {
			t.Fatalf("used codes must not be revocable: %q", k)
		}
	}
}
