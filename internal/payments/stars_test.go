package payments

import (
	"context"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestStarsAmountRoundsUp(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.SetTelegramStars(50) // 50 price units per Star
	cases := []struct{ price, want int }{
		{0, 1},   // never below 1 Star
		{50, 1},  // exact
		{51, 2},  // ceil
		{100, 2}, // exact
		{149, 3}, // ceil
	}
	for _, c := range cases {
		if got := svc.starsAmount(c.price); got != c.want {
			t.Errorf("starsAmount(%d) = %d, want %d", c.price, got, c.want)
		}
	}
}

func TestSetTelegramStarsEnablesProvider(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	// Before configuration, enabling Stars is rejected.
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err == nil {
		t.Fatal("expected error enabling unconfigured stars")
	}
	svc.SetTelegramStars(1)
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}
	if !contains(svc.enabledProviders(), ProviderTelegramStars) {
		t.Fatalf("enabledProviders = %v, want to include telegram_stars", svc.enabledProviders())
	}

	// Persists across a restart, but is filtered out when not re-wired.
	svc2 := newServiceOverStore(t, svc.store)
	if !svc2.isProviderEnabled(ProviderTelegramStars) {
		t.Fatal("reloaded isProviderEnabled(stars) = false, want true")
	}
	if contains(svc2.enabledProviders(), ProviderTelegramStars) {
		t.Fatalf("reloaded enabledProviders = %v, want stars filtered out", svc2.enabledProviders())
	}
}

func TestCannotDisableLastProvider(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	// Default enabled set is {p2p}; disabling it is rejected.
	if err := svc.setProviderEnabled(ctx, ProviderP2P, false); err == nil {
		t.Fatal("expected error disabling the last provider")
	}
}

func TestStartStarsAndPromptSendsInvoice(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: 555}}}
	svc.startStarsAndPrompt(ctx, cb, u, 2, 300, store.PlanStandard)

	if len(bot.invoices) != 1 {
		t.Fatalf("invoices = %d, want 1", len(bot.invoices))
	}
	inv := bot.invoices[0]
	if inv.ChatID != 555 {
		t.Fatalf("invoice chat = %d, want 555", inv.ChatID)
	}
	if len(inv.Prices) != 1 || inv.Prices[0].Amount != 6 { // ceil(300/50)
		t.Fatalf("invoice prices = %+v, want amount 6", inv.Prices)
	}

	// A pending Stars request was stored, and the payload carries its id.
	reqID, err := reqIDFromPayload(inv.Payload)
	if err != nil {
		t.Fatalf("payload %q: %v", inv.Payload, err)
	}
	got, _ := st.GetPaymentRequest(ctx, reqID)
	if got == nil || got.Provider != ProviderTelegramStars || got.Status != "pending" || got.Months != 2 {
		t.Fatalf("unexpected request: %+v", got)
	}
}

func TestStartStarsAndPromptWithdrawsOnSendError(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)
	bot.invoiceErr = context.DeadlineExceeded

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 555}}
	svc.startStarsAndPrompt(ctx, cb, u, 1, 100, store.PlanStandard)

	// The dangling pending request must have been withdrawn (id 1 not present).
	if got, _ := st.GetPaymentRequest(ctx, 1); got != nil {
		t.Fatalf("request must be withdrawn after send failure: %+v", got)
	}
}

func TestPreCheckoutAcksPendingAndRejectsStale(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	reqID, err := svc.startStarsPayment(ctx, u, 1, 100, store.PlanStandard) // 2 stars
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Correct amount on a pending request -> ack ok.
	bot := svc.bot.(*fakeBot)
	q := &tg.PreCheckoutQuery{ID: "q1", InvoicePayload: starsPayload(reqID), TotalAmount: 2}
	if !svc.HandlePreCheckout(ctx, q) {
		t.Fatal("pre-checkout should be handled")
	}
	if len(bot.precheckoutAck) != 1 || !bot.precheckoutAck[0] {
		t.Fatalf("expected ok ack, got %v", bot.precheckoutAck)
	}

	// Amount mismatch -> reject.
	q2 := &tg.PreCheckoutQuery{ID: "q2", InvoicePayload: starsPayload(reqID), TotalAmount: 999}
	svc.HandlePreCheckout(ctx, q2)
	if got := bot.precheckoutAck[len(bot.precheckoutAck)-1]; got {
		t.Fatal("expected reject on amount mismatch")
	}

	// Unknown request -> reject.
	q3 := &tg.PreCheckoutQuery{ID: "q3", InvoicePayload: "req:99999", TotalAmount: 2}
	svc.HandlePreCheckout(ctx, q3)
	if got := bot.precheckoutAck[len(bot.precheckoutAck)-1]; got {
		t.Fatal("expected reject on unknown request")
	}
	_ = st
}

func TestSuccessfulPaymentConfirmsAndIsIdempotent(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	reqID, err := svc.startStarsPayment(ctx, u, 1, 100, store.PlanStandard)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	m := &tg.Message{Chat: tg.Chat{ID: 555}, SuccessfulPayment: &tg.SuccessfulPayment{
		Currency: "XTR", TotalAmount: 2, InvoicePayload: starsPayload(reqID),
		TelegramPaymentChargeID: "charge-1"}}
	if !svc.HandleSuccessfulPayment(ctx, m) {
		t.Fatal("successful payment should be handled")
	}
	if ext.calls != 1 {
		t.Fatalf("extend calls = %d, want 1", ext.calls)
	}
	got, _ := st.GetPaymentRequest(ctx, reqID)
	if got.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", got.Status)
	}
	if got.ProviderTxnID != "charge-1" {
		t.Fatalf("provider txn id = %q, want charge-1", got.ProviderTxnID)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected a confirmation message to the user")
	}

	// A duplicate successful_payment for the same request must not extend again.
	if !svc.HandleSuccessfulPayment(ctx, m) {
		t.Fatal("duplicate should still be handled (no-op)")
	}
	if ext.calls != 1 {
		t.Fatalf("extend calls after duplicate = %d, want 1", ext.calls)
	}
}

func TestEnabledProvidersPrecedenceAndOrder(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	svc.SetPlatega(&fakePlatega{}, 2, "RUB", "https://t.me")
	svc.SetTelegramStars(1)

	if err := svc.setProviderEnabled(ctx, ProviderPlatega, true); err != nil {
		t.Fatalf("enable platega: %v", err)
	}
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}
	// All three enabled, returned in canonical order p2p, platega, telegram_stars.
	got := svc.enabledProviders()
	want := []string{ProviderP2P, ProviderPlatega, ProviderTelegramStars}
	if len(got) != len(want) {
		t.Fatalf("enabledProviders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enabledProviders = %v, want %v", got, want)
		}
	}

	// Disabling p2p leaves the two automatic providers.
	if err := svc.setProviderEnabled(ctx, ProviderP2P, false); err != nil {
		t.Fatalf("disable p2p: %v", err)
	}
	if contains(svc.enabledProviders(), ProviderP2P) {
		t.Fatalf("p2p should be disabled: %v", svc.enabledProviders())
	}
}

func TestHandlePayViaRoutesToStars(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)
	svc.SetPlatega(&fakePlatega{nextID: "tx-1"}, 2, "RUB", "https://t.me")
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}
	if err := svc.setProviderEnabled(ctx, ProviderPlatega, true); err != nil {
		t.Fatalf("enable platega: %v", err)
	}

	// A remembered user and a tariff are needed for the payvia route.
	u := store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	if err := st.UpsertNotifiedUser(ctx, u); err != nil {
		t.Fatalf("remember user: %v", err)
	}
	if err := st.UpsertTariff(ctx, store.PlanStandard, 2, 300); err != nil {
		t.Fatalf("tariff: %v", err)
	}

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: 555}},
		Data:    "payvia:7:2:" + ProviderTelegramStars}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("payvia callback should be handled")
	}
	if len(bot.invoices) != 1 {
		t.Fatalf("expected a Stars invoice, got %d", len(bot.invoices))
	}
}

func TestCreateRenewRequestStarsReturnsInvoiceLink(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}
	// Stars-only: disable p2p so it is the sole provider.
	if err := svc.setProviderEnabled(ctx, ProviderP2P, false); err != nil {
		t.Fatalf("disable p2p: %v", err)
	}

	// A linked subscriber via the finder and a tariff.
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
			ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	if err := st.UpsertTariff(ctx, store.PlanStandard, 1, 100); err != nil {
		t.Fatalf("tariff: %v", err)
	}

	res, err := svc.CreateRenewRequest(ctx, 555, 7, 1, "", "", false)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if res == nil || res.Status != ProviderTelegramStars || res.InvoiceURL == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestCreateRenewRequestChoosesProvider(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.SetTelegramStars(50)
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
			ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	if err := st.UpsertTariff(ctx, store.PlanStandard, 1, 100); err != nil {
		t.Fatalf("tariff: %v", err)
	}

	// p2p + stars enabled, no provider chosen -> chooser.
	res, err := svc.CreateRenewRequest(ctx, 555, 7, 1, "", "", false)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if res == nil || res.Status != "choose_provider" || len(res.Providers) != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}
