package autoupdate

import (
	"context"
	"log/slog"
	"time"
)

// Settings keys used to track update state across checks (and restarts).
const (
	settingBaselineDigest = "update_baseline_digest"
	settingNotifiedDigest = "update_notified_digest"
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
	interval time.Duration
	logger   *slog.Logger
}

// NewChecker wires a checker. logger may be nil.
func NewChecker(fetcher digestProvider, store settingsStore, notifier Notifier, image string, interval time.Duration, logger *slog.Logger) *Checker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Checker{
		fetcher:  fetcher,
		store:    store,
		notifier: notifier,
		image:    image,
		interval: interval,
		logger:   logger,
	}
}

// Run checks immediately, then on every interval tick, until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	c.checkOnce(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkOnce(ctx)
		}
	}
}

func (c *Checker) checkOnce(ctx context.Context) {
	remote, err := c.fetcher.Digest(ctx, c.image)
	if err != nil {
		c.logger.Warn("autoupdate: digest check failed", "err", err.Error())
		return
	}

	baseline, found, err := c.store.GetSetting(ctx, settingBaselineDigest)
	if err != nil {
		c.logger.Error("autoupdate: read baseline failed", "err", err.Error())
		return
	}
	if !found {
		// First run this process: the running container is already this image.
		if err := c.store.UpsertSetting(ctx, settingBaselineDigest, remote); err != nil {
			c.logger.Error("autoupdate: store baseline failed", "err", err.Error())
		}
		return
	}

	if remote == baseline {
		return // up to date
	}

	// A newer image exists. Notify at most once per distinct new digest.
	notified, _, err := c.store.GetSetting(ctx, settingNotifiedDigest)
	if err != nil {
		c.logger.Error("autoupdate: read notified digest failed", "err", err.Error())
		return
	}
	if notified == remote {
		return
	}
	c.logger.Info("autoupdate: new image available", "baseline", baseline, "remote", remote)
	c.notifier.NotifyUpdateAvailable(ctx, baseline, remote)
	if err := c.store.UpsertSetting(ctx, settingNotifiedDigest, remote); err != nil {
		c.logger.Error("autoupdate: store notified digest failed", "err", err.Error())
	}
}
