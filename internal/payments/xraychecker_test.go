package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeChecker struct {
	statuses []ProxyStatus
	err      error
}

func (f *fakeChecker) Status(context.Context) ([]ProxyStatus, error) {
	return f.statuses, f.err
}

func TestXrayCheckerAdminMenuButtonConditional(t *testing.T) {
	svc, bot, _, _ := newTestService(t)

	svc.SendAdminMenu(context.Background(), 1000)
	if keyboardData(bot.sent[len(bot.sent)-1].Keyboard)["adm:checker"] {
		t.Fatal("checker button must be absent when not configured")
	}

	bot.sent = nil
	svc.SetXrayChecker(&fakeChecker{})
	svc.SendAdminMenu(context.Background(), 1000)
	if !keyboardData(bot.sent[len(bot.sent)-1].Keyboard)["adm:checker"] {
		t.Fatal("checker button must appear once configured")
	}
}

func TestNotifyProxyDownDMsAllAdmins(t *testing.T) {
	svc, bot, _, _ := newTestServiceTwoAdmins(t)
	svc.NotifyProxyDown(context.Background(), "Alpha", "vless", "a.example.com:443")

	got := map[int64]bool{}
	for _, m := range bot.sent {
		got[m.ChatID] = true
		if !strings.Contains(m.Text, "Alpha") {
			t.Fatalf("alert should name the proxy: %q", m.Text)
		}
	}
	if !got[1000] || !got[2000] {
		t.Fatalf("expected a DM to both admins: %+v", bot.sent)
	}
}

func TestProxyAlertSuppressedInDryRun(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.dryRun = true
	svc.NotifyProxyRecovered(context.Background(), "Alpha", "vless", "a:443")
	if len(bot.sent) != 0 {
		t.Fatalf("dry run must not DM admins: %+v", bot.sent)
	}
}

func TestAdminProxyHealthGuardsAndAggregates(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.SetXrayChecker(&fakeChecker{statuses: []ProxyStatus{
		{Name: "A", Up: true, LatencyMs: 10}, {Name: "B", Up: false},
	}})

	if _, err := svc.AdminProxyHealth(context.Background(), 2222); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin error = %v, want ErrNotAdmin", err)
	}
	h, err := svc.AdminProxyHealth(context.Background(), 1000)
	if err != nil {
		t.Fatalf("AdminProxyHealth: %v", err)
	}
	if !h.Configured || h.Up != 1 || h.Down != 1 || len(h.Proxies) != 2 {
		t.Fatalf("unexpected snapshot: %+v", h)
	}
}

func TestAdminProxyHealthUnconfigured(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	h, err := svc.AdminProxyHealth(context.Background(), 1000)
	if err != nil {
		t.Fatalf("AdminProxyHealth: %v", err)
	}
	if h.Configured {
		t.Fatalf("expected unconfigured snapshot: %+v", h)
	}
}

func TestProxyHealthCallbackRendersReport(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.SetXrayChecker(&fakeChecker{statuses: []ProxyStatus{
		{Name: "Alpha", Up: true, LatencyMs: 42}, {Name: "Bravo", Up: false},
	}})

	if !svc.HandleCallback(context.Background(), cbq(1000, "adm:checker")) {
		t.Fatal("adm:checker should be handled")
	}
	m := bot.sent[len(bot.sent)-1]
	if !strings.Contains(m.Text, "Alpha") || !strings.Contains(m.Text, "Bravo") {
		t.Fatalf("report missing proxies: %q", m.Text)
	}
	found := keyboardData(m.Keyboard)
	if !found["adm:menu"] || !found["adm:checker"] {
		t.Fatalf("expected refresh + back buttons: %+v", found)
	}
}

func TestProxyHealthCallbackHandlesError(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.SetXrayChecker(&fakeChecker{err: errors.New("unreachable")})
	if !svc.HandleCallback(context.Background(), cbq(1000, "adm:checker")) {
		t.Fatal("adm:checker should be handled")
	}
	if len(bot.sent) == 0 || !strings.Contains(bot.sent[len(bot.sent)-1].Text, "Не удалось") {
		t.Fatalf("expected failure notice: %+v", bot.sent)
	}
}
