package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/autoupdate"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

type fakeActionUpdateChecker struct {
	snapshot      autoupdate.CheckSnapshot
	snapshotErr   error
	checkNowCalls int
	snapshotCalls int
	interval      time.Duration
}

func (f *fakeActionUpdateChecker) CheckNow(context.Context) (autoupdate.CheckResult, error) {
	f.checkNowCalls++
	return autoupdate.CheckResult{Status: autoupdate.CheckStatusUpToDate}, nil
}

func (f *fakeActionUpdateChecker) Interval() time.Duration {
	if f.interval > 0 {
		return f.interval
	}
	return time.Hour
}

func (f *fakeActionUpdateChecker) SetInterval(_ context.Context, interval time.Duration) error {
	f.interval = interval
	return nil
}

func (f *fakeActionUpdateChecker) Snapshot(context.Context) (autoupdate.CheckSnapshot, error) {
	f.snapshotCalls++
	return f.snapshot, f.snapshotErr
}

func actionItemsByID(center *WebAdminActionCenter) map[string]WebAdminActionItem {
	out := make(map[string]WebAdminActionItem, len(center.Items))
	for _, item := range center.Items {
		out[item.ID] = item
	}
	return out
}

func actionSourcesByName(center *WebAdminActionCenter) map[string]WebAdminActionSource {
	out := make(map[string]WebAdminActionSource, len(center.Sources))
	for _, src := range center.Sources {
		out[src.Name] = src
	}
	return out
}

func rejectPaymentForActionCenter(t *testing.T, st *store.Store, now time.Time, provider string, rejectedAt time.Time) {
	t.Helper()
	id, err := st.CreatePaymentRequest(context.Background(), store.PaymentRequest{
		RemnawaveID:     10,
		UUID:            "uuid-" + provider,
		Username:        "user-" + provider,
		TelegramID:      500,
		Months:          1,
		Price:           100,
		ExpireAt:        now.AddDate(0, 1, 0),
		Provider:        provider,
		ProviderTxnID:   provider + "-txn",
		PayerTelegramID: 501,
		PayerUsername:   "payer-" + provider,
	})
	if err != nil {
		t.Fatalf("create rejected payment fixture: %v", err)
	}
	if ok, err := st.RejectPaymentRequest(context.Background(), id, rejectedAt); err != nil || !ok {
		t.Fatalf("reject payment fixture: ok=%v err=%v", ok, err)
	}
}

func TestAdminActionCenterRejectsNonAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.AdminActionCenter(context.Background(), userTG); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("AdminActionCenter non-admin error = %v, want ErrNotAdmin", err)
	}
}

func TestAdminActionCenterAggregatesAttentionItems(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	svc.now = func() time.Time { return now }

	if _, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 1, UUID: "uuid-pay", Username: "alice", TelegramID: 101,
		Months: 1, Price: 100, ExpireAt: now.AddDate(0, 1, 0), Provider: ProviderP2P,
	}); err != nil {
		t.Fatalf("create payment request: %v", err)
	}
	if _, err := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "RW-GIFT", BuyerTelegramID: 201, BuyerUsername: "buyer", Months: 1, Price: 100,
	}); err != nil {
		t.Fatalf("create gift request: %v", err)
	}
	if _, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 301, InviterUsername: "owner", NewUsername: "newbie", Months: 1, Price: 100,
	}); err != nil {
		t.Fatalf("create invite request: %v", err)
	}
	if _, err := st.CreateTrialRequest(ctx, 401, "trial", now); err != nil {
		t.Fatalf("create trial request: %v", err)
	}
	supportID, err := st.CreateSupportTicket(ctx, 450, "support", "help")
	if err != nil {
		t.Fatalf("create support ticket: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, store.SupportMessage{TicketID: supportID, UserTelegramID: 450, UserUsername: "support", Text: "hello"}); err != nil {
		t.Fatalf("create unread support message: %v", err)
	}
	if _, _, err := st.RecordRegistration(ctx, store.RegistrationGuard{TelegramID: 451, Username: "blocked", MatchedPattern: "support", Blocked: true}); err != nil {
		t.Fatalf("create blocked registration: %v", err)
	}
	rejectPaymentForActionCenter(t, st, now, ProviderPlatega, now.Add(-24*time.Hour))
	rejectPaymentForActionCenter(t, st, now, ProviderTelegramStars, now.Add(-2*24*time.Hour))
	rejectPaymentForActionCenter(t, st, now, ProviderP2P, now.Add(-24*time.Hour))
	rejectPaymentForActionCenter(t, st, now, ProviderTelegramStars, now.Add(-8*24*time.Hour))

	svc.finder = &fakeFinder{all: []Subscriber{
		{UUID: "expiring", Username: "expiring", Status: "ACTIVE", ExpireAt: now.Add(6 * 24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 100},
		{UUID: "low", Username: "low", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 900},
		{UUID: "unlimited", Username: "unlimited", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 0, UsedTrafficBytes: 999},
		{UUID: "disabled", Username: "disabled", Status: "DISABLED", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 950},
	}}
	svc.SetXrayChecker(&fakeChecker{statuses: []ProxyStatus{
		{Name: "Alpha", Up: false},
		{Name: "Bravo", Up: true},
	}})
	if _, err := st.CreateInfraServer(ctx, store.InfraServer{Name: "overdue", Price: 10, Currency: "USD", PeriodMonths: 1, NextDueAt: now.Add(-24 * time.Hour)}); err != nil {
		t.Fatalf("create overdue infra server: %v", err)
	}
	if _, err := st.CreateInfraServer(ctx, store.InfraServer{Name: "soon", Price: 10, Currency: "USD", PeriodMonths: 1, NextDueAt: now.Add(48 * time.Hour)}); err != nil {
		t.Fatalf("create soon infra server: %v", err)
	}
	if _, err := st.CreateInfraServer(ctx, store.InfraServer{Name: "ok", Price: 10, Currency: "USD", PeriodMonths: 1, NextDueAt: now.Add(30 * 24 * time.Hour)}); err != nil {
		t.Fatalf("create ok infra server: %v", err)
	}
	checker := &fakeActionUpdateChecker{snapshot: autoupdate.CheckSnapshot{Status: autoupdate.CheckStatusUpdateAvailable, Baseline: "sha256:old", Remote: "sha256:new"}}
	svc.SetUpdateChecker(checker)

	center, err := svc.AdminActionCenter(ctx, adminTG)
	if err != nil {
		t.Fatalf("AdminActionCenter: %v", err)
	}
	items := actionItemsByID(center)
	for id, want := range map[string]int{
		"pending_payments":      1,
		"pending_gifts":         1,
		"pending_invites":       1,
		"pending_trials":        1,
		"unread_support":        1,
		"blocked_registrations": 1,
		"provider_rejections":   2,
		"expiring_users":        1,
		"low_traffic_users":     1,
		"proxy_down":            1,
		"infra_overdue":         1,
		"infra_due_soon":        1,
		"update_available":      1,
	} {
		if got := items[id].Count; got != want {
			t.Fatalf("%s count = %d, want %d; items=%+v", id, got, want, center.Items)
		}
	}
	if center.Summary.Total != 13 || center.Summary.Affected != 14 || center.Summary.Critical != 2 || center.Summary.Warning != 11 {
		t.Fatalf("unexpected summary: %+v", center.Summary)
	}
	if center.Summary.NeedsAction != 9 || center.Summary.Incidents != 2 || center.Summary.Opportunities != 2 {
		t.Fatalf("unexpected group summary: %+v", center.Summary)
	}
	for id, want := range map[string]string{
		"pending_payments": actionGroupNeedsAction,
		"proxy_down":       actionGroupIncident,
		"expiring_users":   actionGroupOpportunity,
	} {
		if got := items[id].Group; got != want {
			t.Fatalf("%s group = %q, want %q", id, got, want)
		}
	}
	if items["pending_payments"].OldestAt == "" || items["unread_support"].OldestAt == "" || items["blocked_registrations"].OldestAt == "" {
		t.Fatalf("missing oldest timestamps: %+v", items)
	}
	if items["infra_overdue"].DueAt == "" || items["infra_due_soon"].DueAt == "" {
		t.Fatalf("missing infrastructure deadlines: %+v", items)
	}
	if got := items["low_traffic_users"].Filter["cohort"]; got != "low_traffic" {
		t.Fatalf("low-traffic filter = %q, want low_traffic", got)
	}
	if got := items["provider_rejections"].Filter["days"]; got != "7" {
		t.Fatalf("provider filter days = %q, want 7", got)
	}
	sources := actionSourcesByName(center)
	for _, name := range []string{"local_store", "remnawave", "xray_checker", "infra", "autoupdate"} {
		if sources[name].Status != actionSourceOK {
			t.Fatalf("source %s = %+v, want ok", name, sources[name])
		}
	}
	if checker.checkNowCalls != 0 || checker.snapshotCalls != 1 {
		t.Fatalf("action center must read update snapshot only: checkNow=%d snapshot=%d", checker.checkNowCalls, checker.snapshotCalls)
	}
}

func TestActionSeverityAtAgeThreshold(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		oldest    time.Time
		threshold time.Duration
		want      string
	}{
		{name: "payment just below", oldest: now.Add(-2*time.Hour + time.Second), threshold: 2 * time.Hour, want: actionSeverityWarning},
		{name: "payment exact", oldest: now.Add(-2 * time.Hour), threshold: 2 * time.Hour, want: actionSeverityCritical},
		{name: "administrative just below", oldest: now.Add(-24*time.Hour + time.Second), threshold: 24 * time.Hour, want: actionSeverityWarning},
		{name: "administrative exact", oldest: now.Add(-24 * time.Hour), threshold: 24 * time.Hour, want: actionSeverityCritical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionSeverityForAge(now, tc.oldest, tc.threshold); got != tc.want {
				t.Fatalf("severity = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdminActionCenterEscalatesSupportButNotRegistrationBlock(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	createdAt := time.Now().UTC()
	svc.now = func() time.Time { return createdAt.Add(25 * time.Hour) }
	svc.finder = &fakeFinder{all: []Subscriber{}}

	ticketID, err := st.CreateSupportTicket(ctx, 901, "waiting", "help")
	if err != nil {
		t.Fatalf("create support ticket: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, store.SupportMessage{TicketID: ticketID, UserTelegramID: 901, Text: "still waiting"}); err != nil {
		t.Fatalf("add support message: %v", err)
	}
	if _, _, err := st.RecordRegistration(ctx, store.RegistrationGuard{TelegramID: 902, Username: "blocked", Blocked: true}); err != nil {
		t.Fatalf("record registration block: %v", err)
	}

	center, err := svc.AdminActionCenter(ctx, adminTG)
	if err != nil {
		t.Fatalf("AdminActionCenter: %v", err)
	}
	items := actionItemsByID(center)
	if got := items["unread_support"].Severity; got != actionSeverityCritical {
		t.Fatalf("aged support severity = %q, want critical", got)
	}
	if got := items["blocked_registrations"].Severity; got != actionSeverityWarning {
		t.Fatalf("aged registration block severity = %q, want warning", got)
	}
}

func TestAdminActionCenterFailsWhenLocalStoreIsUnavailable(t *testing.T) {
	svc, _, _, st := newTestService(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := svc.AdminActionCenter(context.Background(), adminTG); err == nil {
		t.Fatal("AdminActionCenter succeeded with a closed local store")
	}
}

func TestAdminActionCenterDegradesExternalSources(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	if _, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 1, UUID: "uuid-pay", Username: "alice", TelegramID: 101,
		Months: 1, Price: 100, ExpireAt: now.AddDate(0, 1, 0), Provider: ProviderP2P,
	}); err != nil {
		t.Fatalf("create local payment request: %v", err)
	}
	svc.finder = &fakeFinder{listErr: errors.New("panel down")}
	svc.SetXrayChecker(&fakeChecker{err: errors.New("checker down")})
	svc.SetUpdateChecker(&fakeActionUpdateChecker{snapshotErr: errors.New("settings down")})

	center, err := svc.AdminActionCenter(ctx, adminTG)
	if err != nil {
		t.Fatalf("AdminActionCenter: %v", err)
	}
	if got := actionItemsByID(center)["pending_payments"].Count; got != 1 {
		t.Fatalf("local pending item hidden after source degradation: got %d", got)
	}
	sources := actionSourcesByName(center)
	for _, name := range []string{"remnawave", "xray_checker", "autoupdate"} {
		if sources[name].Status != actionSourceDegraded || sources[name].Error == "" {
			t.Fatalf("source %s = %+v, want degraded with error", name, sources[name])
		}
	}
	items := actionItemsByID(center)
	for _, id := range []string{"remnawave_unavailable", "xray_checker_unavailable", "autoupdate_unavailable"} {
		if items[id].Group != actionGroupIncident || items[id].Count != 1 {
			t.Fatalf("degraded source item %s = %+v", id, items[id])
		}
	}
	if items["remnawave_unavailable"].Detail == "panel down" || items["xray_checker_unavailable"].Detail == "checker down" {
		t.Fatalf("source errors leaked into user-facing details: %+v", items)
	}
}

func TestAdminListUsersByCohortLowTraffic(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	svc.finder = &fakeFinder{all: []Subscriber{
		{UUID: "low", Username: "low", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 900},
		{UUID: "eleven", Username: "eleven", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 899},
		{UUID: "unlimited", Username: "unlimited", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 0, UsedTrafficBytes: 999},
		{UUID: "expired", Username: "expired", Status: "EXPIRED", ExpireAt: now.Add(-24 * time.Hour), TrafficLimitBytes: 1000, UsedTrafficBytes: 950},
	}}

	users, err := svc.AdminListUsersByCohort(context.Background(), adminTG, "low_traffic")
	if err != nil {
		t.Fatalf("AdminListUsersByCohort(low_traffic): %v", err)
	}
	if len(users) != 1 || users[0].Username != "low" {
		t.Fatalf("low_traffic users = %+v, want only low", users)
	}
}
