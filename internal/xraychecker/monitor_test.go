package xraychecker

import (
	"context"
	"testing"
)

type fakeStatus struct {
	statuses []ProxyStatus
	err      error
}

func (f *fakeStatus) Status(context.Context) ([]ProxyStatus, error) {
	return f.statuses, f.err
}

type memSettings map[string]string

func (m memSettings) GetSetting(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memSettings) UpsertSetting(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

type recordingNotifier struct {
	down      []string
	recovered []string
}

func (r *recordingNotifier) NotifyProxyDown(_ context.Context, name, _, _ string) {
	r.down = append(r.down, name)
}
func (r *recordingNotifier) NotifyProxyRecovered(_ context.Context, name, _, _ string) {
	r.recovered = append(r.recovered, name)
}

func proxy(name string, up bool) ProxyStatus {
	return ProxyStatus{Name: name, Address: name + ":443", Protocol: "vless", Up: up}
}

func TestMonitorBaselinesThenAlertsOnTransitions(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true), proxy("B", false)}}
	store := memSettings{}
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

func TestMonitorSkipsOnFetchError(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{err: context.DeadlineExceeded}
	store := memSettings{}
	notifier := &recordingNotifier{}
	m := NewMonitor(src, store, notifier, 1, nil)

	m.checkOnce(ctx)
	if len(notifier.down) != 0 {
		t.Fatal("fetch error must not alert")
	}
	if _, ok := store[stateKey]; ok {
		t.Fatal("fetch error must not persist a baseline")
	}
}

func TestMonitorIgnoresNewlyAppearedProxies(t *testing.T) {
	ctx := context.Background()
	src := &fakeStatus{statuses: []ProxyStatus{proxy("A", true)}}
	store := memSettings{}
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
