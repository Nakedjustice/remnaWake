package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newReferralStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestReferralCreateIsFirstReferrerWins(t *testing.T) {
	st := newReferralStore(t)
	ctx := context.Background()
	now := time.Now()

	ok, err := st.CreateReferral(ctx, 100, 200, now)
	if err != nil || !ok {
		t.Fatalf("first create: ok=%v err=%v", ok, err)
	}

	// A second link click for the same referred user must not overwrite.
	ok, err = st.CreateReferral(ctx, 100, 999, now)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if ok {
		t.Fatalf("second create should report no insert")
	}

	got, err := st.GetReferral(ctx, 100)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.ReferrerTelegramID != 200 {
		t.Fatalf("referrer overwritten: got %d, want 200", got.ReferrerTelegramID)
	}
	if got.Status != "pending" {
		t.Fatalf("status: got %q, want pending", got.Status)
	}
}

func TestReferralCreditOnce(t *testing.T) {
	st := newReferralStore(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := st.CreateReferral(ctx, 100, 200, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	ok, err := st.CreditReferral(ctx, 100, now)
	if err != nil || !ok {
		t.Fatalf("first credit: ok=%v err=%v", ok, err)
	}

	// A repeat payout (e.g. a second confirmed payment, or bot + mini app racing)
	// must not credit again.
	ok, err = st.CreditReferral(ctx, 100, now)
	if err != nil {
		t.Fatalf("second credit: %v", err)
	}
	if ok {
		t.Fatalf("second credit should report no update")
	}

	got, err := st.GetReferral(ctx, 100)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Status != "credited" || got.CreditedAt == nil {
		t.Fatalf("not credited: %+v", got)
	}
}

func TestReferralCreditMissingRow(t *testing.T) {
	st := newReferralStore(t)
	ok, err := st.CreditReferral(context.Background(), 404, time.Now())
	if err != nil {
		t.Fatalf("credit missing: %v", err)
	}
	if ok {
		t.Fatalf("crediting a missing referral should report no update")
	}
}

func TestReferralStats(t *testing.T) {
	st := newReferralStore(t)
	ctx := context.Background()
	now := time.Now()

	// referrer 200 brings in three users; one converts.
	for _, ref := range []int64{1, 2, 3} {
		if _, err := st.CreateReferral(ctx, ref, 200, now); err != nil {
			t.Fatalf("create %d: %v", ref, err)
		}
	}
	if _, err := st.CreditReferral(ctx, 2, now); err != nil {
		t.Fatalf("credit: %v", err)
	}
	// An unrelated referrer's referral must not leak into the counts.
	if _, err := st.CreateReferral(ctx, 4, 999, now); err != nil {
		t.Fatalf("create other: %v", err)
	}

	invited, credited, err := st.ReferralStats(ctx, 200)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if invited != 3 || credited != 1 {
		t.Fatalf("stats: invited=%d credited=%d, want 3/1", invited, credited)
	}
}
