package store

import (
	"context"
	"testing"
)

func TestNotificationPrefsDefaultUnmuted(t *testing.T) {
	st := newTrialStore(t)
	ctx := context.Background()

	em, wm, err := st.GetNotificationPrefs(ctx, 777)
	if err != nil {
		t.Fatalf("get prefs: %v", err)
	}
	if em || wm {
		t.Fatalf("defaults should be unmuted, got expiry=%v winback=%v", em, wm)
	}
	if muted, _ := st.NotificationMuted(ctx, 777, NotificationExpiry); muted {
		t.Fatal("expiry should default to not muted")
	}
}

func TestSetNotificationPrefIndependent(t *testing.T) {
	st := newTrialStore(t)
	ctx := context.Background()

	if err := st.SetNotificationPref(ctx, 777, NotificationExpiry, true); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	em, wm, _ := st.GetNotificationPrefs(ctx, 777)
	if !em || wm {
		t.Fatalf("after muting expiry: expiry=%v winback=%v", em, wm)
	}

	// Muting winback must not change expiry.
	if err := st.SetNotificationPref(ctx, 777, NotificationWinback, true); err != nil {
		t.Fatalf("set winback: %v", err)
	}
	em, wm, _ = st.GetNotificationPrefs(ctx, 777)
	if !em || !wm {
		t.Fatalf("after muting both: expiry=%v winback=%v", em, wm)
	}

	// Unmuting expiry must leave winback muted.
	if err := st.SetNotificationPref(ctx, 777, NotificationExpiry, false); err != nil {
		t.Fatalf("unset expiry: %v", err)
	}
	em, wm, _ = st.GetNotificationPrefs(ctx, 777)
	if em || !wm {
		t.Fatalf("after unmuting expiry: expiry=%v winback=%v", em, wm)
	}
	if muted, _ := st.NotificationMuted(ctx, 777, NotificationWinback); !muted {
		t.Fatal("winback should be muted")
	}
}

func TestSetNotificationPrefUnknownKind(t *testing.T) {
	st := newTrialStore(t)
	if err := st.SetNotificationPref(context.Background(), 777, "bogus", true); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
