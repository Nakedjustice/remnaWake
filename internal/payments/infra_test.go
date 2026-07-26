package payments

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

type fakeRateFetcher struct {
	rates map[string]float64
	base  string
	err   error
	calls int
}

func (f *fakeRateFetcher) FetchRates(_ context.Context, base string, symbols []string) (map[string]float64, error) {
	f.calls++
	f.base = base
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]float64{}
	for _, s := range symbols {
		if r, ok := f.rates[s]; ok {
			out[s] = r
		}
	}
	return out, nil
}

func TestInfraAdminGuard(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	checks := map[string]error{}
	_, err := svc.AdminListInfraServers(ctx, userTG)
	checks["list"] = err
	checks["save"] = svc.AdminSaveInfraServer(ctx, userTG, WebInfraServerInput{Name: "x", Currency: "USD", PeriodMonths: 1})
	checks["delete"] = svc.AdminDeleteInfraServer(ctx, userTG, 1)
	checks["paid"] = svc.AdminMarkInfraServerPaid(ctx, userTG, 1)
	_, err = svc.AdminListFxRates(ctx, userTG)
	checks["fx"] = err
	checks["manual"] = svc.AdminSetManualFxRate(ctx, userTG, "USD", 90)
	checks["base"] = svc.AdminSetBaseCurrency(ctx, userTG, "USD")
	checks["refresh"] = svc.AdminRefreshFxRates(ctx, userTG)
	for name, err := range checks {
		if !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s: err = %v, want ErrNotAdmin", name, err)
		}
	}
}

func TestInfraServerLifecycleAndConversion(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) }

	// Manual USD rate; base is RUB (derived from the "₽" service currency).
	if err := svc.AdminSetManualFxRate(ctx, adminTG, "USD", 90); err != nil {
		t.Fatalf("manual rate: %v", err)
	}
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
		Name: "edge", Hoster: "Hetzner", Country: "DE", CPUCores: 4, RAMMB: 8192,
		Price: 30, Currency: "usd", PeriodMonths: 3, NextDue: "2026-06-17",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	list, err := svc.AdminListInfraServers(ctx, adminTG)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d err %v", len(list), err)
	}
	srv := list[0]
	if srv.PriceLabel != "$30" || srv.PeriodCostLabel != "$30 / 3 мес." || srv.RAMLabel != "8 ГБ" {
		t.Fatalf("labels: %+v", srv)
	}
	// $30 / 3mo = $10/mo * 90 = 900₽/mo.
	if !srv.Converted || srv.MonthlyBaseLabel != "≈ 900₽/мес" {
		t.Fatalf("converted: %+v", srv)
	}
	if srv.DueStatus != "soon" { // due 17th, now 15th, within the 3-day lead
		t.Fatalf("due status: %q", srv.DueStatus)
	}

	// Mark paid advances the due date by the 3-month period.
	if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, srv.ID); err != nil {
		t.Fatalf("paid: %v", err)
	}
	list, _ = svc.AdminListInfraServers(ctx, adminTG)
	if list[0].NextDue != "17.09.2026" {
		t.Fatalf("advanced due: %q", list[0].NextDue)
	}

	// The cost surfaces in the stats payload.
	stats, err := svc.AdminStatsData(ctx, adminTG)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.InfraServers != 1 || stats.InfraMonthlyCostLabel != "≈ 900₽/мес" || stats.InfraUnconverted != 0 {
		t.Fatalf("stats: %+v", stats)
	}

	// Validation + delete.
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{Name: "", Currency: "USD", PeriodMonths: 1}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("empty name: %v", err)
	}
	// A price typed into the period box, or a mistyped year, is rejected rather
	// than stored and walked decades out by the next "paid" click.
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
		Name: "x", Currency: "USD", PeriodMonths: infraMaxPeriodMonths + 1,
	}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("oversized period: %v", err)
	}
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
		Name: "x", Currency: "USD", PeriodMonths: 1, NextDue: "2054-08-29",
	}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("far-future due date: %v", err)
	}
	if err := svc.AdminDeleteInfraServer(ctx, adminTG, srv.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.AdminDeleteInfraServer(ctx, adminTG, srv.ID); !errors.Is(err, ErrInfraServerUnknown) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestInfraMarkPaidIsIdempotent(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	id, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "edge", Price: 5, Currency: "USD", PeriodMonths: 1,
		NextDueAt: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
	})

	// The first click advances one period; the next ten (a button that did not
	// visibly react, tapped again) must not stack another period each.
	for i := 0; i < 11; i++ {
		if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, id); err != nil {
			t.Fatalf("paid %d: %v", i, err)
		}
	}
	srv, _ := st.GetInfraServer(ctx, id)
	if want := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC); !srv.NextDueAt.Equal(want) {
		t.Fatalf("due = %v, want %v", srv.NextDueAt, want)
	}

	// Same for a server due exactly today: the second click lands on the same
	// date as the first, not a period past it.
	today, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "today", Price: 5, Currency: "USD", PeriodMonths: 1, NextDueAt: now,
	})
	for i := 0; i < 2; i++ {
		if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, today); err != nil {
			t.Fatalf("paid today %d: %v", i, err)
		}
	}
	srv, _ = st.GetInfraServer(ctx, today)
	if want := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC); !srv.NextDueAt.Equal(want) {
		t.Fatalf("today due = %v, want %v", srv.NextDueAt, want)
	}

	// Several periods missed: one click catches up to the next future date.
	overdue, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "old", Price: 5, Currency: "USD", PeriodMonths: 1,
		NextDueAt: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
	})
	if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, overdue); err != nil {
		t.Fatalf("paid overdue: %v", err)
	}
	srv, _ = st.GetInfraServer(ctx, overdue)
	if want := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC); !srv.NextDueAt.Equal(want) {
		t.Fatalf("overdue due = %v, want %v", srv.NextDueAt, want)
	}

	// No due date yet: one period from today.
	fresh, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "new", Price: 5, Currency: "USD", PeriodMonths: 3,
	})
	if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, fresh); err != nil {
		t.Fatalf("paid fresh: %v", err)
	}
	srv, _ = st.GetInfraServer(ctx, fresh)
	if want := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC); !srv.NextDueAt.Equal(want) {
		t.Fatalf("fresh due = %v, want %v", srv.NextDueAt, want)
	}

	// A period stored before the bound existed is clamped instead of trusted.
	legacy, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "legacy", Price: 5, Currency: "USD", PeriodMonths: 336,
		NextDueAt: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
	})
	if err := svc.AdminMarkInfraServerPaid(ctx, adminTG, legacy); err != nil {
		t.Fatalf("paid legacy: %v", err)
	}
	srv, _ = st.GetInfraServer(ctx, legacy)
	if want := time.Date(2031, 7, 29, 0, 0, 0, 0, time.UTC); !srv.NextDueAt.Equal(want) {
		t.Fatalf("legacy due = %v, want %v", srv.NextDueAt, want)
	}
}

func TestInfraFxAutoPreferredAndUnconverted(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) }

	// EUR server with no rate yet → unconverted.
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
		Name: "eu", Price: 10, Currency: "EUR", PeriodMonths: 1,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	list, _ := svc.AdminListInfraServers(ctx, adminTG)
	if list[0].Converted || list[0].MonthlyBaseLabel != "≈ — (нет курса)" {
		t.Fatalf("expected unconverted: %+v", list[0])
	}
	fx, err := svc.AdminListFxRates(ctx, adminTG)
	if err != nil || fx.BaseCurrency != "RUB" || fx.Unconverted != 1 {
		t.Fatalf("fx overview: %+v err %v", fx, err)
	}

	// Manual fallback makes it convertible.
	if err := svc.AdminSetManualFxRate(ctx, adminTG, "EUR", 100); err != nil {
		t.Fatalf("manual: %v", err)
	}
	// A fetched auto rate then wins over the manual fallback.
	fetcher := &fakeRateFetcher{rates: map[string]float64{"EUR": 105}}
	svc.SetForex(fetcher)
	if err := svc.RefreshInfraFxRates(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fetcher.calls != 1 || fetcher.base != "RUB" {
		t.Fatalf("fetch calls=%d base=%q", fetcher.calls, fetcher.base)
	}
	list, _ = svc.AdminListInfraServers(ctx, adminTG)
	// 10 EUR/mo * 105 = 1050₽.
	if list[0].MonthlyBaseLabel != "≈ 1050₽/мес" {
		t.Fatalf("auto-converted: %+v", list[0])
	}
}

func TestRefreshFxMultipleServerCurrencies(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) }

	for _, c := range []string{"USD", "EUR"} {
		if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
			Name: "srv-" + c, Price: 10, Currency: c, PeriodMonths: 1,
		}); err != nil {
			t.Fatalf("save %s: %v", c, err)
		}
	}
	svc.SetForex(&fakeRateFetcher{rates: map[string]float64{"USD": 80, "EUR": 90}})
	if err := svc.RefreshInfraFxRates(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	for _, c := range []string{"USD", "EUR"} {
		r, _ := st.GetFxRate(ctx, c)
		if r == nil || !r.HasAuto || r.AutoRate <= 0 {
			t.Fatalf("%s auto rate not refreshed: %+v", c, r)
		}
	}
}

func TestRefreshFxStoredRateWithoutServer(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) }

	// One server in USD; EUR has only an admin-entered manual rate (its server
	// was removed, say) so it still shows as a pair in the FX card.
	if err := svc.AdminSaveInfraServer(ctx, adminTG, WebInfraServerInput{
		Name: "us", Price: 10, Currency: "USD", PeriodMonths: 1,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.AdminSetManualFxRate(ctx, adminTG, "EUR", 100); err != nil {
		t.Fatalf("manual: %v", err)
	}

	svc.SetForex(&fakeRateFetcher{rates: map[string]float64{"USD": 80, "EUR": 90}})
	if err := svc.RefreshInfraFxRates(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Both pairs are shown in the FX card, so "Refresh rates" must update both.
	eur, _ := st.GetFxRate(ctx, "EUR")
	if eur == nil || !eur.HasAuto || eur.AutoRate != 90 {
		t.Fatalf("EUR auto rate not refreshed: %+v", eur)
	}
}

func TestInfraPaymentReminders(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	id, err := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "edge", Hoster: "Hetzner", Price: 5, Currency: "USD", PeriodMonths: 1,
		NextDueAt: time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC), // due in 2 days
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	svc.RunInfraPaymentReminders(ctx)
	if got := sentTo(bot, adminTG); len(got) != 1 {
		t.Fatalf("pre-due reminders = %d, want 1", len(got))
	}
	// Re-running the same day must not repeat the pre-due nudge.
	svc.RunInfraPaymentReminders(ctx)
	if got := sentTo(bot, adminTG); len(got) != 1 {
		t.Fatalf("after rerun = %d, want 1 (no repeat)", len(got))
	}
	if srv, _ := st.GetInfraServer(ctx, id); srv.RemindedStage != 1 {
		t.Fatalf("stage = %d, want 1", srv.RemindedStage)
	}

	// Once overdue, a second reminder is sent (stage 2), then no more.
	now = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	svc.RunInfraPaymentReminders(ctx)
	svc.RunInfraPaymentReminders(ctx)
	got := sentTo(bot, adminTG)
	if len(got) != 2 {
		t.Fatalf("overdue reminders = %d, want 2 total", len(got))
	}
	if srv, _ := st.GetInfraServer(ctx, id); srv.RemindedStage != 2 {
		t.Fatalf("stage = %d, want 2", srv.RemindedStage)
	}
	last := got[len(got)-1]
	if last.Keyboard == nil || last.Keyboard.InlineKeyboard[0][0].CallbackData != "adm:infrapaid:"+strconv.FormatInt(id, 10) {
		t.Fatalf("missing paid button: %+v", last.Keyboard)
	}
}

func TestHandleInfraPaidCallback(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) }
	id, _ := st.CreateInfraServer(ctx, store.InfraServer{
		Name: "edge", Price: 5, Currency: "USD", PeriodMonths: 1,
		NextDueAt: time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
	})

	cb := &tg.CallbackQuery{
		ID: "cb1", From: tg.User{ID: adminTG}, Data: "adm:infrapaid:" + strconv.FormatInt(id, 10),
		Message: &tg.Message{MessageID: 7, Chat: tg.Chat{ID: adminTG}},
	}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("callback not handled")
	}
	srv, _ := st.GetInfraServer(ctx, id)
	if !srv.NextDueAt.Equal(time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("due not advanced: %v", srv.NextDueAt)
	}

	// A non-admin tap changes nothing.
	cb2 := &tg.CallbackQuery{ID: "cb2", From: tg.User{ID: userTG}, Data: "adm:infrapaid:" + strconv.FormatInt(id, 10)}
	if !svc.HandleCallback(ctx, cb2) {
		t.Fatal("callback not handled")
	}
	srv2, _ := st.GetInfraServer(ctx, id)
	if !srv2.NextDueAt.Equal(srv.NextDueAt) {
		t.Fatalf("non-admin changed due date: %v", srv2.NextDueAt)
	}
}
