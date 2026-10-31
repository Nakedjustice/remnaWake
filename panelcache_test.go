package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

// countingFetch records how many times the panel was actually asked.
type countingFetch struct {
	mu      sync.Mutex
	calls   int
	release chan struct{} // when non-nil, each fetch blocks until it is closed
	err     error
	subs    []payments.Subscriber
}

func (f *countingFetch) fetch(ctx context.Context) ([]payments.Subscriber, error) {
	f.mu.Lock()
	f.calls++
	release := f.release
	f.mu.Unlock()
	if release != nil {
		<-release
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.subs, nil
}

func (f *countingFetch) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestCache(f *countingFetch, now func() time.Time) *panelUserCache {
	c := newPanelUserCache(f.fetch, 15*time.Second)
	c.now = now
	return c
}

func TestPanelUserCacheServesRepeatsFromTheSnapshot(t *testing.T) {
	f := &countingFetch{subs: []payments.Subscriber{{RemnawaveID: 1}}}
	clock := time.Unix(1700000000, 0)
	c := newTestCache(f, func() time.Time { return clock })

	for i := 0; i < 4; i++ {
		subs, err := c.list(context.Background())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(subs) != 1 {
			t.Fatalf("len(subs) = %d, want 1", len(subs))
		}
	}
	if got := f.count(); got != 1 {
		t.Fatalf("panel fetched %d times, want 1", got)
	}
}

func TestPanelUserCacheRefetchesAfterTTL(t *testing.T) {
	f := &countingFetch{subs: []payments.Subscriber{{RemnawaveID: 1}}}
	clock := time.Unix(1700000000, 0)
	c := newTestCache(f, func() time.Time { return clock })

	if _, err := c.list(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}
	clock = clock.Add(16 * time.Second)
	if _, err := c.list(context.Background()); err != nil {
		t.Fatalf("list after ttl: %v", err)
	}
	if got := f.count(); got != 2 {
		t.Fatalf("panel fetched %d times, want 2", got)
	}
}

// TestPanelUserCacheCollapsesConcurrentMisses is the case that actually hurts:
// opening the admin panel fires the action centre and the stats endpoints at
// once, and both used to pull the whole user list independently.
func TestPanelUserCacheCollapsesConcurrentMisses(t *testing.T) {
	f := &countingFetch{subs: []payments.Subscriber{{RemnawaveID: 1}}, release: make(chan struct{})}
	clock := time.Unix(1700000000, 0)
	c := newTestCache(f, func() time.Time { return clock })

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.list(context.Background()); err != nil {
				t.Errorf("list: %v", err)
			}
		}()
	}
	// Let every caller pile up on the same in-flight fetch, then let it finish.
	time.Sleep(20 * time.Millisecond)
	close(f.release)
	wg.Wait()

	if got := f.count(); got != 1 {
		t.Fatalf("panel fetched %d times for 5 concurrent callers, want 1", got)
	}
}

func TestPanelUserCacheDoesNotCacheFailures(t *testing.T) {
	f := &countingFetch{err: errors.New("panel down")}
	clock := time.Unix(1700000000, 0)
	c := newTestCache(f, func() time.Time { return clock })

	if _, err := c.list(context.Background()); err == nil {
		t.Fatal("want the panel error surfaced")
	}
	f.err = nil
	f.subs = []payments.Subscriber{{RemnawaveID: 7}}
	subs, err := c.list(context.Background())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(subs) != 1 || subs[0].RemnawaveID != 7 {
		t.Fatalf("subs = %+v, want the recovered list", subs)
	}
	if got := f.count(); got != 2 {
		t.Fatalf("panel fetched %d times, want a retry after the failure", got)
	}
}
