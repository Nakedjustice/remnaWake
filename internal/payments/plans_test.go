package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// TestConfirmStampsCustomPlan verifies that confirming a purchase under a
// custom preset applies the preset's squad, limits and tag together with the
// expiry in a single panel patch.
func TestConfirmStampsCustomPlan(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.now = func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) }

	if err := st.UpsertPlan(ctx, store.Plan{
		Code: "messenger", Name: "Мессенджер", Tag: "MESSENGER",
		SquadUUID: "sq-msg", TrafficGB: 30, DeviceLimit: 1,
	}); err != nil {
		t.Fatal(err)
	}
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 3, Price: 250, ExpireAt: exp, Plan: "messenger",
	})

	if _, _, err := svc.confirmPaymentRequest(ctx, id); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	upd := svc.userUpdater.(*fakeUpdater)
	if len(upd.calls) != 1 || upd.uuids[0] != "uuid-42" {
		t.Fatalf("expected one patch for uuid-42, got %v / %v", upd.uuids, upd.calls)
	}
	p := upd.calls[0]
	if p.ExpireAt == nil || !p.ExpireAt.Equal(exp.AddDate(0, 3, 0)) {
		t.Fatalf("expiry = %v, want %s", p.ExpireAt, exp.AddDate(0, 3, 0))
	}
	if p.TrafficLimitBytes == nil || *p.TrafficLimitBytes != 30*bytesPerGB {
		t.Fatalf("traffic = %v, want 30 GB", p.TrafficLimitBytes)
	}
	if p.HwidDeviceLimit == nil || *p.HwidDeviceLimit != 1 {
		t.Fatalf("device limit = %v, want 1", p.HwidDeviceLimit)
	}
	if p.Tag == nil || *p.Tag != "MESSENGER" {
		t.Fatalf("tag = %v, want MESSENGER", p.Tag)
	}
	if p.ActiveInternalSquads == nil || len(*p.ActiveInternalSquads) != 1 || (*p.ActiveInternalSquads)[0] != "sq-msg" {
		t.Fatalf("squads = %v, want [sq-msg]", p.ActiveInternalSquads)
	}
	if p.TrafficLimitStrategy == nil || *p.TrafficLimitStrategy != "NO_RESET" {
		t.Fatalf("strategy = %v, want NO_RESET", p.TrafficLimitStrategy)
	}
}

// TestConfirmStampsStandardPlan verifies that a standard purchase resets a
// previously limited user: default squad, unlimited traffic/devices, tag
// cleared (pointer to empty string -> JSON null on the wire).
func TestConfirmStampsStandardPlan(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()

	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 1, Price: 300, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if _, _, err := svc.confirmPaymentRequest(ctx, id); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	p := svc.userUpdater.(*fakeUpdater).calls[0]
	if p.TrafficLimitBytes == nil || *p.TrafficLimitBytes != 0 {
		t.Fatalf("traffic = %v, want 0 (unlimited)", p.TrafficLimitBytes)
	}
	if p.HwidDeviceLimit == nil || *p.HwidDeviceLimit != 0 {
		t.Fatalf("device limit = %v, want 0 (unlimited)", p.HwidDeviceLimit)
	}
	if p.Tag == nil || *p.Tag != "" {
		t.Fatalf("tag = %v, want pointer to empty string (clear)", p.Tag)
	}
	// Default squad resolved via the seeded Default-Squad fallback.
	if p.ActiveInternalSquads == nil || (*p.ActiveInternalSquads)[0] != "default-squad-uuid" {
		t.Fatalf("squads = %v, want [default-squad-uuid]", p.ActiveInternalSquads)
	}
}

// TestConfirmDeletedPlanExtendsOnly verifies that a plan deleted between
// purchase and confirm never silently alters limits: only the expiry is
// patched and the confirm still succeeds.
func TestConfirmDeletedPlanExtendsOnly(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()

	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 1, Price: 100, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Plan: "vanished",
	})
	if _, _, err := svc.confirmPaymentRequest(ctx, id); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	p := svc.userUpdater.(*fakeUpdater).calls[0]
	if p.ExpireAt == nil {
		t.Fatal("expiry must still be patched")
	}
	if p.TrafficLimitBytes != nil || p.HwidDeviceLimit != nil || p.Tag != nil || p.ActiveInternalSquads != nil {
		t.Fatalf("deleted plan must not touch limits/tag/squads: %+v", p)
	}
	if req, _ := st.GetPaymentRequest(ctx, id); req.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", req.Status)
	}
}

// TestBuyFlowCustomPlanEndToEnd walks the two-step bot flow: pay -> plan
// chooser -> months for that plan -> pick creates a request carrying the plan,
// and the admin notification names the preset.
func TestBuyFlowCustomPlanEndToEnd(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, store.PlanStandard, 1, 300)
	if err := st.UpsertPlan(ctx, store.Plan{Code: "messenger", Name: "Мессенджер", Tag: "MESSENGER", TrafficGB: 30, DeviceLimit: 1}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)

	// pay: with more than one plan shows the plan chooser, standard first.
	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	kb := bot.edits[len(bot.edits)-1].Keyboard
	if kb.InlineKeyboard[0][0].CallbackData != "pln:42:standard" ||
		kb.InlineKeyboard[1][0].CallbackData != "pln:42:messenger" {
		t.Fatalf("plan chooser wrong: %+v", kb.InlineKeyboard)
	}
	if kb.InlineKeyboard[1][0].Text != "Мессенджер" {
		t.Fatalf("plan button label = %q", kb.InlineKeyboard[1][0].Text)
	}

	// Picking the preset swaps in its months grid with plan-tagged callbacks
	// and a back button returning to the chooser.
	if !svc.HandleCallback(ctx, cbq(777, "pln:42:messenger")) {
		t.Fatal("pln should be handled")
	}
	kb = bot.edits[len(bot.edits)-1].Keyboard
	if kb.InlineKeyboard[0][0].CallbackData != "pick:42:3:messenger" {
		t.Fatalf("months keyboard wrong: %+v", kb.InlineKeyboard)
	}
	if back := kb.InlineKeyboard[len(kb.InlineKeyboard)-1][0].CallbackData; back != "pay:42" {
		t.Fatalf("back button = %q, want pay:42", back)
	}

	// Picking months files the request under the preset.
	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3:messenger")) {
		t.Fatal("pick should be handled")
	}
	reqs, _ := st.ListPaymentRequestsByStatus(ctx, "pending")
	if len(reqs) != 1 || reqs[0].Plan != "messenger" || reqs[0].Price != 250 || reqs[0].Months != 3 {
		t.Fatalf("request wrong: %+v", reqs)
	}
	var adminNote string
	for _, m := range bot.sent {
		if m.ChatID == 1000 {
			adminNote = m.Text
		}
	}
	if !strings.Contains(adminNote, "Тариф: Мессенджер") {
		t.Fatalf("admin notification missing plan name:\n%s", adminNote)
	}
}

// TestBuyFlowStandardOnlyUnchanged pins the CLAUDE.md invariant: with no
// custom presets the pay flow renders exactly the pre-plans keyboard (2-part
// pick callbacks, back target intact, no chooser step).
func TestBuyFlowStandardOnlyUnchanged(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, store.PlanStandard, 1, 300)
	_ = st.UpsertTariff(ctx, store.PlanStandard, 3, 800)

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	kb := bot.edits[len(bot.edits)-1].Keyboard
	if kb.InlineKeyboard[0][0].CallbackData != "pick:42:1" ||
		kb.InlineKeyboard[1][0].CallbackData != "pick:42:3" ||
		kb.InlineKeyboard[2][0].CallbackData != "back:42" {
		t.Fatalf("standard keyboard changed: %+v", kb.InlineKeyboard)
	}
}

// TestPayViaCarriesPlan verifies the provider chooser round-trips the preset
// (payvia:<uid>:<months>:<provider>:<plan>).
func TestPayViaCarriesPlan(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	if err := st.UpsertPlan(ctx, store.Plan{Code: "messenger", Name: "Мессенджер"}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)
	svc.SetTelegramStars(50)
	if err := svc.setProviderEnabled(ctx, ProviderTelegramStars, true); err != nil {
		t.Fatalf("enable stars: %v", err)
	}

	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3:messenger")) {
		t.Fatal("pick should be handled")
	}
	kb := bot.edits[len(bot.edits)-1].Keyboard
	var found bool
	for _, row := range kb.InlineKeyboard {
		if row[0].CallbackData == "payvia:42:3:p2p:messenger" {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider buttons missing plan: %+v", kb.InlineKeyboard)
	}

	if !svc.HandleCallback(ctx, cbq(777, "payvia:42:3:p2p:messenger")) {
		t.Fatal("payvia should be handled")
	}
	reqs, _ := st.ListPaymentRequestsByStatus(ctx, "pending")
	if len(reqs) != 1 || reqs[0].Plan != "messenger" || reqs[0].Price != 250 {
		t.Fatalf("request wrong: %+v", reqs)
	}
}

// TestCabinetDataPlans verifies /api/me carries the preset list (standard
// first, each with its own grid) only when custom presets exist.
func TestCabinetDataPlans(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	_ = st.UpsertTariff(ctx, store.PlanStandard, 1, 300)

	// Standard only: no plans block.
	data, err := svc.CabinetData(ctx, 777)
	if err != nil {
		t.Fatal(err)
	}
	if data.Plans != nil {
		t.Fatalf("plans should be omitted without presets: %+v", data.Plans)
	}

	if err := st.UpsertPlan(ctx, store.Plan{Code: "messenger", Name: "Мессенджер", TrafficGB: 30, DeviceLimit: 1}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)

	data, err = svc.CabinetData(ctx, 777)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Plans) != 2 || data.Plans[0].Code != store.PlanStandard || data.Plans[1].Code != "messenger" {
		t.Fatalf("plans = %+v", data.Plans)
	}
	if len(data.Plans[0].Tariffs) != 1 || data.Plans[0].Tariffs[0].Price != 300 {
		t.Fatalf("standard grid = %+v", data.Plans[0].Tariffs)
	}
	if p := data.Plans[1]; p.TrafficGB != 30 || p.DeviceLimit != 1 || len(p.Tariffs) != 1 || p.Tariffs[0].Price != 250 {
		t.Fatalf("messenger plan = %+v", p)
	}
	// The legacy top-level grid stays the standard one (gift flow depends on it).
	if len(data.Tariffs) != 1 || data.Tariffs[0].Price != 300 {
		t.Fatalf("top-level tariffs = %+v", data.Tariffs)
	}
}

// TestCreateRenewRequestWithPlan verifies the mini app renew accepts a preset
// and rejects unknown plans/durations.
func TestCreateRenewRequestWithPlan(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		777: {{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
			ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	if err := st.UpsertPlan(ctx, store.Plan{Code: "messenger", Name: "Мессенджер"}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)

	if _, err := svc.CreateRenewRequest(ctx, 777, 42, 3, "", "messenger"); err != nil {
		t.Fatalf("renew with plan: %v", err)
	}
	reqs, _ := st.ListPaymentRequestsByStatus(ctx, "pending")
	if len(reqs) != 1 || reqs[0].Plan != "messenger" || reqs[0].Price != 250 {
		t.Fatalf("request = %+v", reqs)
	}

	if _, err := svc.CreateRenewRequest(ctx, 777, 42, 6, "", "messenger"); !errors.Is(err, ErrTariffUnknown) {
		t.Fatalf("missing months must return ErrTariffUnknown, got %v", err)
	}
	if _, err := svc.CreateRenewRequest(ctx, 777, 42, 3, "", "nope"); !errors.Is(err, ErrPlanUnknown) {
		t.Fatalf("unknown plan must return ErrPlanUnknown, got %v", err)
	}
	// The legacy no-tariffs fallback stays standard-only: a custom plan without
	// a grid cannot fall back to months=1/price=0.
	_, _ = st.DeleteTariff(ctx, "messenger", 3)
	if _, err := svc.CreateRenewRequest(ctx, 777, 42, 1, "", "messenger"); !errors.Is(err, ErrTariffUnknown) {
		t.Fatalf("empty custom grid must return ErrTariffUnknown, got %v", err)
	}
}

// TestAdminSetPlanValidatesAndPersists covers the preset editor rules: code
// regex / reserved 'standard', tag uppercasing + panel regex, squad UUID
// checked against the panel squad list, and delete cascading (via the store).
func TestAdminSetPlanValidatesAndPersists(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()

	bad := []WebAdminPlan{
		{Code: "standard", Name: "X"},                       // reserved
		{Code: "Bad Code!", Name: "X"},                      // regex
		{Code: "waytoolongplancode17", Name: "X"},           // > 16 chars
		{Code: "msg", Name: ""},                             // empty name
		{Code: "msg", Name: "X", Tag: "bad tag"},            // tag regex
		{Code: "msg", Name: "X", TrafficGB: -1},             // negative
		{Code: "msg", Name: "X", DeviceLimit: -2},           // negative
		{Code: "msg", Name: "X", SquadUUID: "no-such-uuid"}, // unknown squad
	}
	for i, in := range bad {
		if err := svc.AdminSetPlan(ctx, adminTG, in); !errors.Is(err, ErrBadInput) {
			t.Errorf("case %d (%+v): err = %v, want ErrBadInput", i, in, err)
		}
	}

	// Valid: tag is uppercased, the seeded Default-Squad UUID is accepted.
	err := svc.AdminSetPlan(ctx, adminTG, WebAdminPlan{
		Code: "Messenger", Name: "Мессенджер", Tag: "messenger",
		SquadUUID: "default-squad-uuid", TrafficGB: 30, DeviceLimit: 1,
	})
	if err != nil {
		t.Fatalf("set plan: %v", err)
	}
	p, _ := st.GetPlan(ctx, "messenger")
	if p == nil || p.Tag != "MESSENGER" || p.SquadUUID != "default-squad-uuid" || p.TrafficGB != 30 {
		t.Fatalf("persisted plan = %+v", p)
	}

	// It appears in the admin panel payload with its grid.
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)
	panel, err := svc.AdminPanelData(ctx, adminTG)
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	if len(panel.Plans) != 1 || panel.Plans[0].Code != "messenger" || len(panel.Plans[0].Tariffs) != 1 {
		t.Fatalf("panel plans = %+v", panel.Plans)
	}

	// Tariff CRUD targets the plan; an unknown plan is rejected.
	if err := svc.AdminSetTariff(ctx, adminTG, "messenger", 6, 400); err != nil {
		t.Fatalf("set plan tariff: %v", err)
	}
	if err := svc.AdminSetTariff(ctx, adminTG, "nope", 6, 400); !errors.Is(err, ErrPlanUnknown) {
		t.Fatalf("unknown plan tariff: err = %v, want ErrPlanUnknown", err)
	}
	if err := svc.AdminDeleteTariff(ctx, adminTG, "messenger", 6); err != nil {
		t.Fatalf("delete plan tariff: %v", err)
	}

	// Delete removes the plan and its grid; standard is not deletable.
	if err := svc.AdminDeletePlan(ctx, adminTG, "standard"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("delete standard: err = %v, want ErrBadInput", err)
	}
	if err := svc.AdminDeletePlan(ctx, adminTG, "messenger"); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	if err := svc.AdminDeletePlan(ctx, adminTG, "messenger"); !errors.Is(err, ErrPlanUnknown) {
		t.Fatalf("delete missing: err = %v, want ErrPlanUnknown", err)
	}
	if left, _ := st.ListTariffs(ctx, "messenger"); len(left) != 0 {
		t.Fatalf("plan grid not cascaded: %+v", left)
	}
}

// TestConfirmDryRunMakesNoPanelCalls verifies the confirm path honors DRY_RUN.
func TestConfirmDryRunMakesNoPanelCalls(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	upd := &fakeUpdater{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, upd, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", true /*dryRun*/, logger)

	ctx := context.Background()
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 1, Price: 300, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if _, _, err := svc.confirmPaymentRequest(ctx, id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(upd.calls) != 0 {
		t.Fatalf("dry-run must not call the panel: %+v", upd.calls)
	}
	if req, _ := st.GetPaymentRequest(ctx, id); req.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", req.Status)
	}
}
