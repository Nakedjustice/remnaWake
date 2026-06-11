package store

import (
	"context"
	"testing"
	"time"
)

func TestTryMarkNotificationSentClaimsOnce(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	ok, err := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 7, exp)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	again, err := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 7, exp)
	if err != nil || again {
		t.Fatalf("second claim should be refused: ok=%v err=%v", again, err)
	}
}

func TestUnmarkNotificationSentReArms(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if ok, err := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 3, exp); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := st.UnmarkNotificationSent(ctx, 42, NotificationExpiry, 3, exp); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if ok, err := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 3, exp); err != nil || !ok {
		t.Fatalf("re-claim after unmark: ok=%v err=%v", ok, err)
	}
}

func TestNotificationKeyDimensionsAreIndependent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if ok, _ := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 7, exp); !ok {
		t.Fatal("base claim refused")
	}
	// Different milestone, kind, user, or expiry each get their own claim.
	if ok, _ := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 3, exp); !ok {
		t.Fatal("different milestone refused")
	}
	if ok, _ := st.TryMarkNotificationSent(ctx, 42, NotificationWinback, 7, exp); !ok {
		t.Fatal("different kind refused")
	}
	if ok, _ := st.TryMarkNotificationSent(ctx, 43, NotificationExpiry, 7, exp); !ok {
		t.Fatal("different user refused")
	}
	// Renewal: a new expiry re-arms the same user/kind/milestone.
	renewed := exp.AddDate(0, 1, 0)
	if ok, _ := st.TryMarkNotificationSent(ctx, 42, NotificationExpiry, 7, renewed); !ok {
		t.Fatal("renewed expiry refused")
	}
}
