package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTrialStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestClaimTrialOnce(t *testing.T) {
	st := newTrialStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	ok, err := st.ClaimTrial(ctx, 777, "alice", now)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	again, err := st.ClaimTrial(ctx, 777, "alice2", now)
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if again {
		t.Fatal("second claim for same Telegram ID must return false")
	}
	// A different Telegram ID can still claim.
	if ok, err := st.ClaimTrial(ctx, 888, "bob", now); err != nil || !ok {
		t.Fatalf("other user claim: ok=%v err=%v", ok, err)
	}
}

func TestReleaseTrialAllowsReclaim(t *testing.T) {
	st := newTrialStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	if ok, _ := st.ClaimTrial(ctx, 777, "alice", now); !ok {
		t.Fatal("first claim should succeed")
	}
	if err := st.ReleaseTrial(ctx, 777); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, err := st.ClaimTrial(ctx, 777, "alice", now); err != nil || !ok {
		t.Fatalf("reclaim after release: ok=%v err=%v", ok, err)
	}
}
