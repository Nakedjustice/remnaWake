package xraychecker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// stateKey is the settings-table key under which the last-seen up/down state of
// each proxy is persisted, so down/recovery transitions survive restarts and
// are never alerted twice.
const stateKey = "xray_checker_state"

// statusProvider is the slice of *Client the monitor needs.
type statusProvider interface {
	Status(ctx context.Context) ([]ProxyStatus, error)
}

// settingsStore persists the last-seen state across checks and restarts and
// reports which proxies have their notifications muted. It is satisfied by
// *store.Store.
type settingsStore interface {
	GetSetting(ctx context.Context, key string) (value string, found bool, err error)
	UpsertSetting(ctx context.Context, key, value string) error
	// ProxyNotifMuted reports whether alerts for the proxy identified by key
	// (address|name|sub_name) are muted. Muting suppresses the notification only;
	// state tracking continues so unmuting never replays a stale transition.
	ProxyNotifMuted(ctx context.Context, key string) (bool, error)
}

// Notifier receives a callback when a monitored proxy changes between up and
// down. Primitive parameters keep implementers free of an xraychecker import.
type Notifier interface {
	NotifyProxyDown(ctx context.Context, name, protocol, address string)
	NotifyProxyRecovered(ctx context.Context, name, protocol, address string)
}

// Monitor polls a running xray-checker on an interval and alerts on changes.
//
// The first observation per fresh database records the current state as the
// baseline without alerting (so enabling the feature does not fire an alert for
// every already-down proxy). Thereafter each up<->down transition of a
// previously-seen proxy fires exactly one notification.
type Monitor struct {
	client   statusProvider
	store    settingsStore
	notifier Notifier
	interval time.Duration
	logger   *slog.Logger
}

// NewMonitor wires a monitor. A nil logger uses slog.Default; a non-positive
// interval falls back to 2 minutes.
func NewMonitor(client statusProvider, store settingsStore, notifier Notifier, interval time.Duration, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	return &Monitor{client: client, store: store, notifier: notifier, interval: interval, logger: logger}
}

// Run checks immediately, then on every interval tick, until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.checkOnce(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkOnce(ctx)
		}
	}
}

func (m *Monitor) checkOnce(ctx context.Context) {
	statuses, err := m.client.Status(ctx)
	if err != nil {
		m.logger.Warn("xray checker: status fetch failed", "err", err.Error())
		return
	}

	prev, hadPrev, err := m.loadState(ctx)
	if err != nil {
		m.logger.Error("xray checker: load state failed", "err", err.Error())
		return
	}

	cur := make(map[string]bool, len(statuses))
	for _, p := range statuses {
		key := stateIdentity(p)
		cur[key] = p.Up
		if !hadPrev {
			continue // baseline run: record without alerting
		}
		was, seen := prev[key]
		if !seen || was == p.Up {
			continue
		}
		// Admins can mute a noisy server's alerts globally. The transition is
		// still recorded above (cur[key]) and saved below; we only skip the DM.
		// Fail open on a lookup error so a DB hiccup never swallows a real alert.
		if muted, err := m.store.ProxyNotifMuted(ctx, key); err != nil {
			m.logger.Warn("xray checker: mute lookup failed", "name", p.Name, "err", err.Error())
		} else if muted {
			m.logger.Info("xray checker: transition suppressed (muted)", "name", p.Name, "up", p.Up)
			continue
		}
		if p.Up {
			m.logger.Info("xray checker: proxy recovered", "name", p.Name, "address", p.Address)
			m.notifier.NotifyProxyRecovered(ctx, p.Name, p.Protocol, p.Address)
		} else {
			m.logger.Warn("xray checker: proxy down", "name", p.Name, "address", p.Address)
			m.notifier.NotifyProxyDown(ctx, p.Name, p.Protocol, p.Address)
		}
	}

	if err := m.saveState(ctx, cur); err != nil {
		m.logger.Error("xray checker: save state failed", "err", err.Error())
	}
}

// stateIdentity keys a proxy by the labels that distinguish it in xray-checker.
func stateIdentity(p ProxyStatus) string {
	return p.Address + "|" + p.Name + "|" + p.SubName
}

func (m *Monitor) loadState(ctx context.Context) (map[string]bool, bool, error) {
	raw, found, err := m.store.GetSetting(ctx, stateKey)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	state := map[string]bool{}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		// A corrupt/legacy value just re-baselines on the next save.
		m.logger.Warn("xray checker: discarding unreadable state", "err", err.Error())
		return nil, false, nil
	}
	return state, true, nil
}

func (m *Monitor) saveState(ctx context.Context, state map[string]bool) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return m.store.UpsertSetting(ctx, stateKey, string(raw))
}
