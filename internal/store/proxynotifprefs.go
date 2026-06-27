package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Proxy-health notifications are muted per server (globally, for all admins),
// keyed by the proxy's canonical identity "address|name|sub_name" — the same key
// xraychecker uses for state tracking. A row exists with muted=1 while a server's
// up/down alerts are silenced.

// SetProxyNotifMuted upserts the mute flag for a single proxy key.
func (s *Store) SetProxyNotifMuted(ctx context.Context, key string, muted bool) error {
	val := 0
	if muted {
		val = 1
	}
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_notif_muted (proxy_key, muted, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(proxy_key) DO UPDATE SET muted = ?, updated_at = ?
	`, key, val, now, val, now)
	return err
}

// ProxyNotifMuted reports whether the given proxy key has its notifications
// muted. A missing row means not muted.
func (s *Store) ProxyNotifMuted(ctx context.Context, key string) (bool, error) {
	var muted int
	err := s.db.QueryRowContext(ctx,
		`SELECT muted FROM proxy_notif_muted WHERE proxy_key = ?`, key).Scan(&muted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return muted == 1, nil
}

// MutedProxyKeys returns the set of proxy keys whose notifications are muted, for
// bulk-marking the proxy-health snapshot without a query per row.
func (s *Store) MutedProxyKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT proxy_key FROM proxy_notif_muted WHERE muted = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}
