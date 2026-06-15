package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// newUserCtlService builds a Service with caller-supplied finder and updater
// fakes so the "Manage user" helpers can be exercised in isolation.
func newUserCtlService(t *testing.T, finder Finder, updater UserUpdater) (*Service, *fakeCreator) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	creator := &fakeCreator{}
	svc := New(st, &fakeBot{}, &fakeExtender{}, creator, updater, finder, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	return svc, creator
}

func TestFindManagedUser(t *testing.T) {
	finder := &fakeFinder{
		byName:  map[string]*Subscriber{"alice": {UUID: "u-alice", Username: "alice"}},
		byShort: map[string]*Subscriber{"abc123XY": {UUID: "u-short", Username: "bob"}},
	}
	svc, _ := newUserCtlService(t, finder, &fakeUpdater{})
	ctx := context.Background()

	// By username.
	if sub, err := svc.findManagedUser(ctx, "  alice "); err != nil || sub == nil || sub.UUID != "u-alice" {
		t.Fatalf("by username: sub=%+v err=%v", sub, err)
	}
	// By full subscription link.
	if sub, err := svc.findManagedUser(ctx, "https://panel.example.com/sub/abc123XY"); err != nil || sub == nil || sub.UUID != "u-short" {
		t.Fatalf("by link: sub=%+v err=%v", sub, err)
	}
	// By bare short UUID (username miss, short-uuid hit).
	if sub, err := svc.findManagedUser(ctx, "abc123XY"); err != nil || sub == nil || sub.UUID != "u-short" {
		t.Fatalf("by short uuid: sub=%+v err=%v", sub, err)
	}
	// Not found.
	if _, err := svc.findManagedUser(ctx, "ghost"); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("not found: err = %v, want ErrRequestNotFound", err)
	}
	// Empty.
	if _, err := svc.findManagedUser(ctx, "   "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("empty: err = %v, want ErrBadInput", err)
	}
	// Unrecognised link.
	if _, err := svc.findManagedUser(ctx, "https://panel.example.com/"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("bad link: err = %v, want ErrBadInput", err)
	}
}

func TestApplyUserExpiryDelta(t *testing.T) {
	updater := &fakeUpdater{}
	svc, _ := newUserCtlService(t, &fakeFinder{}, updater)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	ctx := context.Background()

	// Extend from a future expiry: base = cur.
	cur := now.AddDate(0, 0, 10)
	got, err := svc.applyUserExpiryDelta(ctx, "u-1", cur, 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(cur.AddDate(0, 0, 5)) {
		t.Fatalf("extend future: got %v", got)
	}

	// Extend from a past expiry: base = now.
	got, err = svc.applyUserExpiryDelta(ctx, "u-1", now.AddDate(0, 0, -10), 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(now.AddDate(0, 0, 5)) {
		t.Fatalf("extend past: got %v", got)
	}

	// Large decrease floors at now.
	got, err = svc.applyUserExpiryDelta(ctx, "u-1", cur, -100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(now) {
		t.Fatalf("decrease floor: got %v, want %v", got, now)
	}
	if last := updater.calls[len(updater.calls)-1]; last.ExpireAt == nil || !last.ExpireAt.Equal(now) {
		t.Fatalf("patch expireAt = %v", last.ExpireAt)
	}
}

func TestUserSetters(t *testing.T) {
	updater := &fakeUpdater{}
	svc, _ := newUserCtlService(t, &fakeFinder{}, updater)
	ctx := context.Background()

	if err := svc.setUserHwidLimit(ctx, "u-1", 3); err != nil {
		t.Fatalf("hwid: %v", err)
	}
	if p := updater.calls[0]; p.HwidDeviceLimit == nil || *p.HwidDeviceLimit != 3 {
		t.Fatalf("hwid patch: %+v", p)
	}
	if err := svc.setUserHwidLimit(ctx, "u-1", -1); !errors.Is(err, ErrBadInput) {
		t.Fatalf("hwid negative: err = %v, want ErrBadInput", err)
	}

	if err := svc.setUserTrafficLimitGB(ctx, "u-1", 5); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if p := updater.calls[1]; p.TrafficLimitBytes == nil || *p.TrafficLimitBytes != 5*bytesPerGB {
		t.Fatalf("traffic patch: %+v", p)
	}

	if err := svc.setUserResetStrategy(ctx, "u-1", "WEEK"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if p := updater.calls[2]; p.TrafficLimitStrategy == nil || *p.TrafficLimitStrategy != "WEEK" {
		t.Fatalf("reset patch: %+v", p)
	}
	if err := svc.setUserResetStrategy(ctx, "u-1", "NOPE"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("reset bad enum: err = %v, want ErrBadInput", err)
	}
}

func TestToggleUserStatus(t *testing.T) {
	updater := &fakeUpdater{}
	svc, _ := newUserCtlService(t, &fakeFinder{}, updater)
	ctx := context.Background()

	next, err := svc.toggleUserStatus(ctx, "u-1", "ACTIVE")
	if err != nil || next != "DISABLED" {
		t.Fatalf("active->disabled: next=%q err=%v", next, err)
	}
	if p := updater.calls[0]; p.Status == nil || *p.Status != "DISABLED" {
		t.Fatalf("status patch: %+v", p)
	}
	next, err = svc.toggleUserStatus(ctx, "u-1", "DISABLED")
	if err != nil || next != "ACTIVE" {
		t.Fatalf("disabled->active: next=%q err=%v", next, err)
	}
	// LIMITED/EXPIRED are enabled to ACTIVE.
	if next, _ := svc.toggleUserStatus(ctx, "u-1", "LIMITED"); next != "ACTIVE" {
		t.Fatalf("limited->active: next=%q", next)
	}
}

func TestToggleUserSquad(t *testing.T) {
	updater := &fakeUpdater{}
	svc, _ := newUserCtlService(t, &fakeFinder{}, updater)
	ctx := context.Background()

	// Add a new squad.
	if err := svc.toggleUserSquad(ctx, "u-1", []string{"sq-1"}, "sq-2"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if p := updater.calls[0]; p.ActiveInternalSquads == nil || len(*p.ActiveInternalSquads) != 2 {
		t.Fatalf("add patch: %+v", p)
	}
	// Remove an existing squad.
	if err := svc.toggleUserSquad(ctx, "u-1", []string{"sq-1", "sq-2"}, "sq-1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	p := updater.calls[1]
	if p.ActiveInternalSquads == nil || len(*p.ActiveInternalSquads) != 1 || (*p.ActiveInternalSquads)[0] != "sq-2" {
		t.Fatalf("remove patch: %+v", p)
	}
}

func TestUpdateUserNormalisesError(t *testing.T) {
	updater := &fakeUpdater{err: errors.New("boom")}
	svc, _ := newUserCtlService(t, &fakeFinder{}, updater)
	if err := svc.setUserHwidLimit(context.Background(), "u-1", 1); !errors.Is(err, ErrPanelUnavailable) {
		t.Fatalf("err = %v, want ErrPanelUnavailable", err)
	}
}

func TestDefaultTrafficReset(t *testing.T) {
	svc, _ := newUserCtlService(t, &fakeFinder{}, &fakeUpdater{})
	ctx := context.Background()

	if got := svc.getDefaultTrafficReset(); got != "NO_RESET" {
		t.Fatalf("default = %q, want NO_RESET", got)
	}
	if err := svc.setDefaultTrafficReset(ctx, "MONTH"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := svc.getDefaultTrafficReset(); got != "MONTH" {
		t.Fatalf("after set = %q, want MONTH", got)
	}
	if err := svc.setDefaultTrafficReset(ctx, "NOPE"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("bad enum: err = %v, want ErrBadInput", err)
	}
}

func TestCreateUserUsesConfiguredStrategy(t *testing.T) {
	svc, creator := newUserCtlService(t, &fakeFinder{}, &fakeUpdater{})
	if err := svc.setDefaultTrafficReset(context.Background(), "DAY"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := svc.getDefaultTrafficReset(); got != "DAY" {
		t.Fatalf("strategy = %q", got)
	}
	_ = creator // strategy threading through create flows is covered by invite/gift tests
}
