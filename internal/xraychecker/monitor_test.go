package xraychecker

import (
	"context"
	"strings"
	"testing"
)

type fakeStatus struct {
	statuses []ProxyStatus
	err      error
}

func (f *fakeStatus) Status(context.Context) ([]ProxyStatus, error) {
	return f.statuses, f.err
}

type memSettings struct {
	kv    map[string]string
	muted map[string]bool
}

func newMemSettings() *memSettings {
	return &memSettings{kv: map[string]string{}, muted: map[string]bool{}}
}
func (m *memSettings) GetSetting(_ context.Context, key string) (string, bool, error) {
	v, ok := m.kv[key]
	return v, ok, nil
}
func (m *memSettings) UpsertSetting(_ context.Context, key, value string) error {
	m.kv[key] = value
	return nil
}
func (m *memSettings) ProxyNotifMuted(_ context.Context, key string) (bool, error) {
	return m.muted[key], nil
}

type recordingNotifier struct {
	down        []string
	recovered   []string
	unreachable []string
	reachable   int
}

func (r *recordingNotifier) NotifyProxyDown(_ context.Context, name, _, _ string) {
	r.down = append(r.down, name)
}
func (r *recordingNotifier) NotifyProxyRecovered(_ context.Context, name, _, _ string) {
	r.recovered = append(r.recovered, name)
}
func (r *recordingNotifier) NotifyCheckerUnreachable(_ context.Context, reason string) {
	r.unreachable = append(r.unreachable, reason)
}
func (r *recordingNotifier) NotifyCheckerReachable(_ context.Context) {
	r.reachable++
}

func proxy(name string, up bool) ProxyStatus {
	return ProxyStatus{Name: name, Address: name + ":443", Protocol: "vless", Up: up}
}

func TestMonitorBaselinesThenAlertsOnTransitions(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true), proxy("B", false)}}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	// First check seeds the baseline; an already-down proxy must not alert.
	m.checkOnce(ctx)
	if len(notifier.down) != 0 || len(notifier.recovered) != 0 {
		t.Fatalf("baseline must not alert: down=%v up=%v", notifier.down, notifier.recovered)
	}

	// A goes down -> exactly one down alert.
	src.statuses = []ProxyStatus{proxy("A", false), proxy("B", false)}
	m.checkOnce(ctx)
	m.checkOnce(ctx) // dedup: same state must not re-alert
	if len(notifier.down) != 1 || notifier.down[0] != "A" {
		t.Fatalf("expected one down alert for A, got %v", notifier.down)
	}

	// A recovers, B recovers -> one recovery alert each.
	src.statuses = []ProxyStatus{proxy("A", true), proxy("B", true)}
	m.checkOnce(ctx)
	if len(notifier.recovered) != 2 {
		t.Fatalf("expected two recovery alerts, got %v", notifier.recovered)
	}
}

func TestMonitorSkipsMutedProxy(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true), proxy("B", true)}}
	store := newMemSettings()
	store.muted[stateIdentity(proxy("A", true))] = true // mute A globally
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx) // baseline: both up

	// Both go down: muted A is suppressed, B still alerts.
	src.statuses = []ProxyStatus{proxy("A", false), proxy("B", false)}
	m.checkOnce(ctx)
	if len(notifier.down) != 1 || notifier.down[0] != "B" {
		t.Fatalf("muted A must not alert, only B: %v", notifier.down)
	}

	// A's state still advanced (recorded while muted), so its recovery is a real
	// transition — still suppressed. Only B's recovery alerts.
	src.statuses = []ProxyStatus{proxy("A", true), proxy("B", true)}
	m.checkOnce(ctx)
	if len(notifier.recovered) != 1 || notifier.recovered[0] != "B" {
		t.Fatalf("muted A must not alert on recovery, only B: %v", notifier.recovered)
	}
}

func TestMonitorSkipsOnFetchError(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{err: context.DeadlineExceeded}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx)
	if len(notifier.down) != 0 {
		t.Fatal("fetch error must not alert")
	}
	if _, ok := store.kv[stateKey]; ok {
		t.Fatal("fetch error must not persist a baseline")
	}
}

// A checker that stops answering is the failure mode that hid a two-month
// outage: every poll logged a warning and nobody was told. Admins get exactly
// one DM once the failures look persistent rather than transient.
func TestMonitorAlertsOnceAfterConsecutiveFetchFailures(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true)}}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx) // baseline while healthy

	src.err = context.DeadlineExceeded
	for i := 1; i < unreachableThreshold; i++ {
		m.checkOnce(ctx)
		if len(notifier.unreachable) != 0 {
			t.Fatalf("must not alert before %d consecutive failures (got one at %d): %v",
				unreachableThreshold, i, notifier.unreachable)
		}
	}

	m.checkOnce(ctx) // crosses the threshold
	if len(notifier.unreachable) != 1 {
		t.Fatalf("expected exactly one unreachable alert, got %v", notifier.unreachable)
	}
	if !strings.Contains(notifier.unreachable[0], context.DeadlineExceeded.Error()) {
		t.Fatalf("alert must carry the underlying error, got %q", notifier.unreachable[0])
	}

	// Still broken: the alert must not repeat every poll.
	m.checkOnce(ctx)
	m.checkOnce(ctx)
	if len(notifier.unreachable) != 1 {
		t.Fatalf("unreachable alert must not repeat, got %v", notifier.unreachable)
	}
}

func TestMonitorAnnouncesCheckerRecoveryAndRearms(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true)}}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx)
	src.err = context.DeadlineExceeded
	for i := 0; i < unreachableThreshold; i++ {
		m.checkOnce(ctx)
	}
	if len(notifier.unreachable) != 1 {
		t.Fatalf("setup: expected one unreachable alert, got %v", notifier.unreachable)
	}

	src.err = nil
	m.checkOnce(ctx)
	if notifier.reachable != 1 {
		t.Fatalf("expected one recovery notice, got %d", notifier.reachable)
	}
	m.checkOnce(ctx)
	if notifier.reachable != 1 {
		t.Fatalf("recovery notice must not repeat, got %d", notifier.reachable)
	}

	// The counter reset, so a fresh outage alerts again.
	src.err = context.DeadlineExceeded
	for i := 0; i < unreachableThreshold; i++ {
		m.checkOnce(ctx)
	}
	if len(notifier.unreachable) != 2 {
		t.Fatalf("expected a second unreachable alert after re-arming, got %v", notifier.unreachable)
	}
}

// Blips shorter than the threshold must stay silent in both directions: no
// unreachable alert, and no "recovered" for an outage nobody was told about.
func TestMonitorStaysSilentOnTransientFetchFailure(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true)}}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx)
	src.err = context.DeadlineExceeded
	m.checkOnce(ctx)
	src.err = nil
	m.checkOnce(ctx)

	if len(notifier.unreachable) != 0 || notifier.reachable != 0 {
		t.Fatalf("transient failure must stay silent: unreachable=%v reachable=%d",
			notifier.unreachable, notifier.reachable)
	}
}

func TestMonitorIgnoresNewlyAppearedProxies(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true)}}
	store := newMemSettings()
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx) // baseline: A up
	// A brand-new proxy that is already down should be recorded, not alerted.
	src.statuses = []ProxyStatus{proxy("A", true), proxy("C", false)}
	m.checkOnce(ctx)
	if len(notifier.down) != 0 {
		t.Fatalf("newly appeared proxy must not alert: %v", notifier.down)
	}
	// But once known, a subsequent transition does alert.
	src.statuses = []ProxyStatus{proxy("A", true), proxy("C", true)}
	m.checkOnce(ctx)
	if len(notifier.recovered) != 1 || notifier.recovered[0] != "C" {
		t.Fatalf("expected recovery alert for C, got %v", notifier.recovered)
	}
}
