package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newGiftStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGiftCodeRoundTrip(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()

	id, err := st.CreateGiftCode(ctx, GiftCode{
		Code: "ABCDEFGH23456789", BuyerTelegramID: 111, BuyerUsername: "alice",
		Months: 3, Price: 450,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.GetGiftCode(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Status != "pending" || got.Code != "ABCDEFGH23456789" || got.Months != 3 ||
		got.Price != 450 || got.BuyerTelegramID != 111 || got.BuyerUsername != "alice" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	byCode, err := st.GetGiftCodeByCode(ctx, "ABCDEFGH23456789")
	if err != nil || byCode == nil || byCode.ID != id {
		t.Fatalf("get by code: %v %+v", err, byCode)
	}

	missing, err := st.GetGiftCodeByCode(ctx, "NOPE")
	if err != nil || missing != nil {
		t.Fatalf("missing code should be nil,nil: %v %+v", err, missing)
	}
}

func TestGiftCodeUniqueCode(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()

	if _, err := st.CreateGiftCode(ctx, GiftCode{Code: "SAME", BuyerTelegramID: 1, Months: 1}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateGiftCode(ctx, GiftCode{Code: "SAME", BuyerTelegramID: 2, Months: 1}); err == nil {
		t.Fatal("duplicate code must fail")
	}
}

func TestGiftCodeLifecycle(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()
	now := time.Now()

	id, err := st.CreateGiftCode(ctx, GiftCode{Code: "LIFE", BuyerTelegramID: 1, Months: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Cannot redeem or revoke while pending.
	if ok, _ := st.RedeemGiftCode(ctx, "LIFE", 2, "bob", now); ok {
		t.Fatal("redeem of pending code must fail")
	}
	if ok, _ := st.RevokeGiftCode(ctx, id, now); ok {
		t.Fatal("revoke of pending code must fail")
	}

	if ok, err := st.IssueGiftCode(ctx, id, now); err != nil || !ok {
		t.Fatalf("issue: %v %v", ok, err)
	}
	if ok, _ := st.IssueGiftCode(ctx, id, now); ok {
		t.Fatal("second issue must be a no-op")
	}
	if ok, _ := st.RejectGiftCode(ctx, id, now); ok {
		t.Fatal("reject after issue must fail")
	}

	if ok, err := st.RedeemGiftCode(ctx, "LIFE", 2, "bob", now); err != nil || !ok {
		t.Fatalf("redeem: %v %v", ok, err)
	}
	if ok, _ := st.RedeemGiftCode(ctx, "LIFE", 3, "eve", now); ok {
		t.Fatal("second redeem must fail")
	}

	got, _ := st.GetGiftCode(ctx, id)
	if got.Status != "redeemed" || got.RedeemerTelegramID != 2 || got.RedeemedUsername != "bob" ||
		got.IssuedAt == nil || got.ResolvedAt == nil {
		t.Fatalf("redeemed state mismatch: %+v", got)
	}

	// Rollback restores redeemability.
	if ok, err := st.ReissueGiftCode(ctx, id); err != nil || !ok {
		t.Fatalf("reissue: %v %v", ok, err)
	}
	got, _ = st.GetGiftCode(ctx, id)
	if got.Status != "issued" || got.RedeemerTelegramID != 0 || got.RedeemedUsername != "" || got.ResolvedAt != nil {
		t.Fatalf("reissued state mismatch: %+v", got)
	}
	if ok, _ := st.RedeemGiftCode(ctx, "LIFE", 4, "carol", now); !ok {
		t.Fatal("redeem after reissue must succeed")
	}
}

func TestGiftCodeRejectAndRevoke(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()
	now := time.Now()

	rejID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "REJ", BuyerTelegramID: 1, Months: 1})
	if ok, err := st.RejectGiftCode(ctx, rejID, now); err != nil || !ok {
		t.Fatalf("reject: %v %v", ok, err)
	}
	if ok, _ := st.IssueGiftCode(ctx, rejID, now); ok {
		t.Fatal("issue after reject must fail")
	}

	revID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "REV", BuyerTelegramID: 1, Months: 1})
	_, _ = st.IssueGiftCode(ctx, revID, now)
	if ok, err := st.RevokeGiftCode(ctx, revID, now); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if ok, _ := st.RedeemGiftCode(ctx, "REV", 2, "bob", now); ok {
		t.Fatal("redeem of revoked code must fail")
	}
}

func TestListGiftCodesByStatus(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()
	now := time.Now()

	a, _ := st.CreateGiftCode(ctx, GiftCode{Code: "A1", BuyerTelegramID: 1, Months: 1})
	b, _ := st.CreateGiftCode(ctx, GiftCode{Code: "B2", BuyerTelegramID: 1, Months: 2})
	_, _ = st.CreateGiftCode(ctx, GiftCode{Code: "C3", BuyerTelegramID: 1, Months: 3})
	_, _ = st.IssueGiftCode(ctx, a, now)
	_, _ = st.IssueGiftCode(ctx, b, now)

	issued, err := st.ListGiftCodesByStatus(ctx, "issued")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(issued) != 2 || issued[0].Code != "A1" || issued[1].Code != "B2" {
		t.Fatalf("issued list mismatch: %+v", issued)
	}
	pending, _ := st.ListGiftCodesByStatus(ctx, "pending")
	if len(pending) != 1 || pending[0].Code != "C3" {
		t.Fatalf("pending list mismatch: %+v", pending)
	}
}

func TestListGiftCodesByBuyer(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()

	first, _ := st.CreateGiftCode(ctx, GiftCode{Code: "M1", BuyerTelegramID: 111, Months: 1})
	second, _ := st.CreateGiftCode(ctx, GiftCode{Code: "M2", BuyerTelegramID: 111, Months: 3})
	_, _ = st.CreateGiftCode(ctx, GiftCode{Code: "OTHER", BuyerTelegramID: 222, Months: 1})

	mine, err := st.ListGiftCodesByBuyer(ctx, 111)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 2 || mine[0].ID != second || mine[1].ID != first {
		t.Fatalf("buyer list must be newest first: %+v", mine)
	}

	none, err := st.ListGiftCodesByBuyer(ctx, 999)
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown buyer must get empty list: %v %+v", err, none)
	}
}
