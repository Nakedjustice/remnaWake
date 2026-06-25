package autoupdate

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Settings keys used to track update state across checks (and restarts).
const (
	settingBaselineDigest = "update_baseline_digest"
	settingNotifiedDigest = "update_notified_digest"
	settingCheckInterval  = "update_check_interval"
)

// digestProvider resolves the current manifest digest of an image ref.
type digestProvider interface {
	Digest(ctx context.Context, ref string) (string, error)
}

// settingsStore is the slice of the SQLite store the checker needs.
type settingsStore interface {
	GetSetting(ctx context.Context, key string) (value string, found bool, err error)
	UpsertSetting(ctx context.Context, key, value string) error
}

// Notifier receives a callback when a newer image digest is available.
type Notifier interface {
	NotifyUpdateAvailable(ctx context.Context, oldDigest, newDigest string)
}

// Checker polls the registry for a new image digest and notifies on change.
//
// On the first observation per process it records the live digest as the
// baseline (the running container is the latest image thanks to
// pull_policy: always), then alerts once each time the remote digest moves away
// from that baseline. After an update + restart the fresh container re-baselines.
type Checker struct {
	fetcher  digestProvider
	store    settingsStore
	notifier Notifier
	image    string
	logger   *slog.Logger

	mu             sync.RWMutex
	interval       time.Duration
	intervalChange chan time.Duration
}

type CheckStatus string

const (
	CheckStatusBaselineInitialized CheckStatus = "baseline_initialized"
	CheckStatusUpToDate            CheckStatus = "up_to_date"
	CheckStatusUpdateAvailable     CheckStatus = "update_available"
	CheckStatusAlreadyNotified     CheckStatus = "already_notified"
)

// CheckResult describes one registry digest check.
type CheckResult struct {
	Status    CheckStatus
	Baseline  string
	Remote    string
	Notified  bool
	FirstRun  bool
	Unchanged bool
}

// NewChecker wires a checker. logger may be nil.
func NewChecker(fetcher digestProvider, store settingsStore, notifier Notifier, image string, interval time.Duration, logger *slog.Logger) *Checker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Checker{
		fetcher:        fetcher,
		store:          store,
		notifier:       notifier,
		image:          image,
		interval:       interval,
		logger:         logger,
		intervalChange: make(chan time.Duration, 1),
	}
}

// Run checks immediately, then on every interval tick, until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	_, _ = c.CheckNow(ctx)
	timer := time.NewTimer(c.Interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = c.CheckNow(ctx)
			timer.Reset(c.Interval())
		case interval := <-c.intervalChange:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		}
	}
}

func (c *Checker) Interval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.interval
}

func (c *Checker) SetInterval(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be positive: %v", interval)
	}
	if err := c.store.UpsertSetting(ctx, settingCheckInterval, interval.String()); err != nil {
		return err
	}
	c.mu.Lock()
	c.interval = interval
	c.mu.Unlock()
	select {
	case c.intervalChange <- interval:
	default:
		select {
		case <-c.intervalChange:
		default:
		}
		c.intervalChange <- interval
	}
	return nil
}

func (c *Checker) LoadPersistedInterval(ctx context.Context) {
	value, found, err := c.store.GetSetting(ctx, settingCheckInterval)
	if err != nil {
		c.logger.Error("autoupdate: read interval failed", "err", err.Error())
		return
	}
	if !found {
		return
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		c.logger.Warn("autoupdate: ignore invalid persisted interval", "value", value)
		return
	}
	c.mu.Lock()
	c.interval = interval
	c.mu.Unlock()
}

// CheckNow performs one digest check immediately. When an update is found, it
// sends the same notification as scheduled checks.
func (c *Checker) CheckNow(ctx context.Context) (CheckResult, error) {
	remote, err := c.fetcher.Digest(ctx, c.image)
	if err != nil {
		c.logger.Warn("autoupdate: digest check failed", "err", err.Error())
		return CheckResult{}, err
	}

	baseline, found, err := c.store.GetSetting(ctx, settingBaselineDigest)
	if err != nil {
		c.logger.Error("autoupdate: read baseline failed", "err", err.Error())
		return CheckResult{}, err
	}
	if !found {
		// First run this process: the running container is already this image.
		if err := c.store.UpsertSetting(ctx, settingBaselineDigest, remote); err != nil {
			c.logger.Error("autoupdate: store baseline failed", "err", err.Error())
			return CheckResult{}, err
		}
		return CheckResult{Status: CheckStatusBaselineInitialized, Baseline: remote, Remote: remote, FirstRun: true}, nil
	}

	if remote == baseline {
		return CheckResult{Status: CheckStatusUpToDate, Baseline: baseline, Remote: remote, Unchanged: true}, nil
	}

	// A newer image exists. Notify at most once per distinct new digest.
	notified, _, err := c.store.GetSetting(ctx, settingNotifiedDigest)
	if err != nil {
		c.logger.Error("autoupdate: read notified digest failed", "err", err.Error())
		return CheckResult{}, err
	}
	if notified == remote {
		return CheckResult{Status: CheckStatusAlreadyNotified, Baseline: baseline, Remote: remote}, nil
	}
	c.logger.Info("autoupdate: new image available", "baseline", baseline, "remote", remote)
	c.notifier.NotifyUpdateAvailable(ctx, baseline, remote)
	if err := c.store.UpsertSetting(ctx, settingNotifiedDigest, remote); err != nil {
		c.logger.Error("autoupdate: store notified digest failed", "err", err.Error())
		return CheckResult{}, err
	}
	return CheckResult{Status: CheckStatusUpdateAvailable, Baseline: baseline, Remote: remote, Notified: true}, nil
}
