package main

import (
	"context"
	"sync"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

// panelUserCacheTTL is how long a full panel user list may be reused. Opening
// the mini app admin panel pulls that list from several endpoints at once (the
// action centre, the stats tiles, the users table, each cohort drill-down), and
// on a large panel each pull is several paginated requests. Fifteen seconds is
// long enough to cover one burst of clicking and short enough that an admin who
// just changed a subscription sees it on the next screen.
const panelUserCacheTTL = 15 * time.Second

// panelUserCache serves the whole-panel user list from a short-lived snapshot.
//
// The mutex is deliberately held across the fetch: concurrent misses then
// collapse into one panel request instead of each caller starting its own, and
// the waiters read the snapshot the winner just stored. A caller can wait as
// long as one panel request takes — which is exactly what it would have paid
// anyway without the cache.
//
// The returned slice is shared between callers and must be treated as read-only.
type panelUserCache struct {
	ttl   time.Duration
	fetch func(context.Context) ([]payments.Subscriber, error)
	now   func() time.Time

	mu        sync.Mutex
	snapshot  []payments.Subscriber
	fetchedAt time.Time
}

func newPanelUserCache(fetch func(context.Context) ([]payments.Subscriber, error), ttl time.Duration) *panelUserCache {
	return &panelUserCache{ttl: ttl, fetch: fetch, now: time.Now}
}

func (c *panelUserCache) list(ctx context.Context) ([]payments.Subscriber, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetchedAt.IsZero() && c.now().Sub(c.fetchedAt) < c.ttl {
		return c.snapshot, nil
	}
	subs, err := c.fetch(ctx)
	if err != nil {
		// A failure is not cached: the next caller retries rather than inheriting
		// a panel outage for the rest of the TTL.
		return nil, err
	}
	c.snapshot = subs
	c.fetchedAt = c.now()
	return subs, nil
}
