package autoupdate

import (
	"context"
	"testing"
	"time"
)

type fakeFetcher struct {
	digest string
	err    error
}

func (f *fakeFetcher) Digest(ctx context.Context, ref string) (string, error) {
	return f.digest, f.err
}

type memSettings map[string]string

func (m memSettings) GetSetting(ctx context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memSettings) UpsertSetting(ctx context.Context, key, value string) error {
	m[key] = value
	return nil
}

type recordingNotifier struct {
	calls [][2]string // {old, new}
}

func (r *recordingNotifier) NotifyUpdateAvailable(ctx context.Context, oldDigest, newDigest string) {
	r.calls = append(r.calls, [2]string{oldDigest, newDigest})
}

func TestCheckerBaselinesThenNotifiesOnce(t *testing.T) {
	ctx := context.Background()
	fetch := &fakeFetcher{digest: "sha256:aaa"}
	store := memSettings{}
	notifier := &recordingNotifier{}
	c := NewChecker(fetch, store, notifier, "img:main", 1, nil)

	// First check: records baseline, no notification.
	_, _ = c.CheckNow(ctx)
	if len(notifier.calls) != 0 {
		t.Fatalf("first check must not notify: %+v", notifier.calls)
	}
	if store[settingBaselineDigest] != "sha256:aaa" {
		t.Fatalf("baseline = %q, want sha256:aaa", store[settingBaselineDigest])
	}

	// Unchanged: still no notification.
	_, _ = c.CheckNow(ctx)
	if len(notifier.calls) != 0 {
		t.Fatalf("unchanged digest must not notify: %+v", notifier.calls)
	}

	// New digest: notify exactly once.
	fetch.digest = "sha256:bbb"
	_, _ = c.CheckNow(ctx)
	_, _ = c.CheckNow(ctx) // dedup: same new digest must not re-notify
	if len(notifier.calls) != 1 {
		t.Fatalf("expected exactly one notification, got %+v", notifier.calls)
	}
	if notifier.calls[0] != [2]string{"sha256:aaa", "sha256:bbb"} {
		t.Fatalf("notification args = %+v, want {aaa bbb}", notifier.calls[0])
	}

	// Yet another digest: notify again.
	fetch.digest = "sha256:ccc"
	_, _ = c.CheckNow(ctx)
	if len(notifier.calls) != 2 {
		t.Fatalf("expected a second notification for a new digest, got %+v", notifier.calls)
	}
}

func TestCheckerSkipsOnFetchError(t *testing.T) {
	ctx := context.Background()
	fetch := &fakeFetcher{err: context.DeadlineExceeded}
	store := memSettings{}
	notifier := &recordingNotifier{}
	c := NewChecker(fetch, store, notifier, "img:main", 1, nil)

	_, _ = c.CheckNow(ctx)
	if len(notifier.calls) != 0 {
		t.Fatal("fetch error must not notify")
	}
	if _, ok := store[settingBaselineDigest]; ok {
		t.Fatal("fetch error must not set a baseline")
	}
}

func TestCheckerIntervalPersistsAndLoads(t *testing.T) {
	ctx := context.Background()
	store := memSettings{}
	c := NewChecker(&fakeFetcher{digest: "sha256:aaa"}, store, &recordingNotifier{}, "img:main", time.Hour, nil)

	if err := c.SetInterval(ctx, 30*time.Minute); err != nil {
		t.Fatalf("SetInterval: %v", err)
	}
	if c.Interval() != 30*time.Minute {
		t.Fatalf("Interval = %v, want 30m", c.Interval())
	}
	if store[settingCheckInterval] != "30m0s" {
		t.Fatalf("persisted interval = %q, want 30m0s", store[settingCheckInterval])
	}

	loaded := NewChecker(&fakeFetcher{digest: "sha256:aaa"}, store, &recordingNotifier{}, "img:main", time.Hour, nil)
	loaded.LoadPersistedInterval(ctx)
	if loaded.Interval() != 30*time.Minute {
		t.Fatalf("loaded Interval = %v, want 30m", loaded.Interval())
	}
}

func TestCheckerSetIntervalRejectsNonPositive(t *testing.T) {
	c := NewChecker(&fakeFetcher{digest: "sha256:aaa"}, memSettings{}, &recordingNotifier{}, "img:main", time.Hour, nil)
	if err := c.SetInterval(context.Background(), 0); err == nil {
		t.Fatal("SetInterval(0) must fail")
	}
}
