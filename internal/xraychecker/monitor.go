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

// unreachableThreshold is how many consecutive failed polls turn "the checker
// hiccuped" into "the checker is broken, tell the admins". At the default 2m
// poll interval that is ~6 minutes: long enough to ride out a sidecar restart
// or a container-network blip, short enough to surface a real outage the same
// day it starts.
const unreachableThreshold = 3

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
// down, and when the checker itself becomes unusable. Primitive parameters keep
// implementers free of an xraychecker import.
type Notifier interface {
	NotifyProxyDown(ctx context.Context, name, protocol, address string)
	NotifyProxyRecovered(ctx context.Context, name, protocol, address string)
	// NotifyCheckerUnreachable reports that polling has failed persistently, so
	// proxy health is no longer being monitored at all. reason is the underlying
	// fetch error.
	NotifyCheckerUnreachable(ctx context.Context, reason string)
	// NotifyCheckerReachable reports that polling recovered. Only ever follows a
	// NotifyCheckerUnreachable.
	NotifyCheckerReachable(ctx context.Context)
}

// Monitor polls a running xray-checker on an interval and alerts on changes.
//
// The first observation per fresh database records the current state as the
// baseline without alerting (so enabling the feature does not fire an alert for
// every already-down proxy). Thereafter each up<->down transition of a
// previously-seen proxy fires exactly one notification.
// A failing poll is tracked in memory rather than in the settings table: a
// restart deliberately re-arms the alert, so a fresh process reports a checker
// that is still broken once more instead of inheriting a silenced flag.
type Monitor struct {
	client   statusProvider
	store    settingsStore
	notifier Notifier
	interval time.Duration
	logger   *slog.Logger

	fetchFailures    int  // consecutive failed polls; reset by any success
	unreachableAlert bool // admins have been told, so do not repeat every tick
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
		m.noteFetchFailure(ctx, err)
		return
	}
	m.noteFetchSuccess(ctx)

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

// noteFetchFailure counts a failed poll and alerts admins once the failures stop
// looking transient. Without this the checker can die silently: every tick logs
// a warning and the per-proxy state simply freezes at its last good value.
func (m *Monitor) noteFetchFailure(ctx context.Context, err error) {
	m.fetchFailures++
	if m.fetchFailures < unreachableThreshold || m.unreachableAlert {
		return
	}
	m.unreachableAlert = true
	m.logger.Error("xray checker: unreachable, alerting admins",
		"failures", m.fetchFailures, "err", err.Error())
	m.notifier.NotifyCheckerUnreachable(ctx, err.Error())
}

// noteFetchSuccess clears the failure streak, announcing recovery only to admins
// who were told about the outage in the first place.
func (m *Monitor) noteFetchSuccess(ctx context.Context) {
	m.fetchFailures = 0
	if !m.unreachableAlert {
		return
	}
	m.unreachableAlert = false
	m.logger.Info("xray checker: reachable again")
	m.notifier.NotifyCheckerReachable(ctx)
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
