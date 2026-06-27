package store

import (
	"context"
	"testing"
)

func TestProxyNotifMuted(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	const key = "1.2.3.4:443|node-a|eu"

	// Default: no row means not muted.
	if muted, err := st.ProxyNotifMuted(ctx, key); err != nil || muted {
		t.Fatalf("default should be unmuted: muted=%v err=%v", muted, err)
	}

	// Mute, then read back.
	if err := st.SetProxyNotifMuted(ctx, key, true); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if muted, err := st.ProxyNotifMuted(ctx, key); err != nil || !muted {
		t.Fatalf("expected muted: muted=%v err=%v", muted, err)
	}

	// Re-muting the same key must stay muted (upsert, not duplicate).
	if err := st.SetProxyNotifMuted(ctx, key, true); err != nil {
		t.Fatalf("re-mute: %v", err)
	}

	// Unmute clears it.
	if err := st.SetProxyNotifMuted(ctx, key, false); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if muted, err := st.ProxyNotifMuted(ctx, key); err != nil || muted {
		t.Fatalf("expected unmuted: muted=%v err=%v", muted, err)
	}
}

func TestMutedProxyKeys(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.MutedProxyKeys(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v err %v", got, err)
	}

	if err := st.SetProxyNotifMuted(ctx, "a", true); err != nil {
		t.Fatalf("mute a: %v", err)
	}
	if err := st.SetProxyNotifMuted(ctx, "b", true); err != nil {
		t.Fatalf("mute b: %v", err)
	}
	if err := st.SetProxyNotifMuted(ctx, "b", false); err != nil {
		t.Fatalf("unmute b: %v", err)
	}

	got, err := st.MutedProxyKeys(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || !got["a"] || got["b"] {
		t.Fatalf("expected only {a}, got %v", got)
	}
}
