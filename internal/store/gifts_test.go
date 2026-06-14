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

func TestDeleteResolvedGiftCodes(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()
	now := time.Now()

	pendID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "PEND", BuyerTelegramID: 1, Months: 1})

	issID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "ISS", BuyerTelegramID: 1, Months: 1})
	_, _ = st.IssueGiftCode(ctx, issID, now)

	redID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "RED", BuyerTelegramID: 1, Months: 1})
	_, _ = st.IssueGiftCode(ctx, redID, now)
	_, _ = st.RedeemGiftCode(ctx, "RED", 2, "bob", now)

	rejID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "REJ", BuyerTelegramID: 1, Months: 1})
	_, _ = st.RejectGiftCode(ctx, rejID, now)

	revID, _ := st.CreateGiftCode(ctx, GiftCode{Code: "REV", BuyerTelegramID: 1, Months: 1})
	_, _ = st.IssueGiftCode(ctx, revID, now)
	_, _ = st.RevokeGiftCode(ctx, revID, now)

	n, err := st.DeleteResolvedGiftCodes(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted rows, got %d", n)
	}

	for _, id := range []int64{rejID, revID} {
		if g, err := st.GetGiftCode(ctx, id); err != nil || g != nil {
			t.Fatalf("gift %d must be deleted: %v %+v", id, err, g)
		}
	}
	for _, id := range []int64{pendID, issID, redID} {
		if g, err := st.GetGiftCode(ctx, id); err != nil || g == nil {
			t.Fatalf("gift %d must survive cleanup: %v %+v", id, err, g)
		}
	}

	// Nothing left to delete on the next run.
	if n, err := st.DeleteResolvedGiftCodes(ctx); err != nil || n != 0 {
		t.Fatalf("second run must delete nothing: %v %d", err, n)
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

func TestListGiftBuyers(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()
	now := time.Now()

	// Buyer 111 (alice): one issued + one redeemed.
	a1, _ := st.CreateGiftCode(ctx, GiftCode{Code: "A1", BuyerTelegramID: 111, BuyerUsername: "alice", Months: 1})
	a2, _ := st.CreateGiftCode(ctx, GiftCode{Code: "A2", BuyerTelegramID: 111, BuyerUsername: "alice", Months: 2})
	_, _ = st.IssueGiftCode(ctx, a1, now)
	_, _ = st.IssueGiftCode(ctx, a2, now)
	_, _ = st.RedeemGiftCode(ctx, "A2", 999, "carol", now)

	// Buyer 222 (bob): one issued only.
	b1, _ := st.CreateGiftCode(ctx, GiftCode{Code: "B1", BuyerTelegramID: 222, BuyerUsername: "bob", Months: 1})
	_, _ = st.IssueGiftCode(ctx, b1, now)

	// Buyer 333 (dan): only pending and revoked — must not appear.
	_, _ = st.CreateGiftCode(ctx, GiftCode{Code: "D1", BuyerTelegramID: 333, BuyerUsername: "dan", Months: 1})
	d2, _ := st.CreateGiftCode(ctx, GiftCode{Code: "D2", BuyerTelegramID: 333, BuyerUsername: "dan", Months: 1})
	_, _ = st.IssueGiftCode(ctx, d2, now)
	_, _ = st.RevokeGiftCode(ctx, d2, now)

	buyers, err := st.ListGiftBuyers(ctx)
	if err != nil {
		t.Fatalf("list buyers: %v", err)
	}
	if len(buyers) != 2 {
		t.Fatalf("expected 2 buyers, got %d: %+v", len(buyers), buyers)
	}
	// Newest activity first: bob's last gift (b1) has a higher id than alice's, so bob leads.
	if buyers[0].BuyerTelegramID != 222 || buyers[0].BuyerUsername != "bob" ||
		buyers[0].NotUsed != 1 || buyers[0].Used != 0 {
		t.Fatalf("buyer[0] mismatch: %+v", buyers[0])
	}
	if buyers[1].BuyerTelegramID != 111 || buyers[1].BuyerUsername != "alice" ||
		buyers[1].NotUsed != 1 || buyers[1].Used != 1 {
		t.Fatalf("buyer[1] mismatch: %+v", buyers[1])
	}
}

func TestGiftCodesByBuyerStatusPagingAndCounts(t *testing.T) {
	st := newGiftStore(t)
	ctx := context.Background()

	codes := []string{"P1", "P2", "P3", "I1", "I2", "X1"}
	ids := map[string]int64{}
	for _, c := range codes {
		buyer := int64(111)
		if c == "X1" {
			buyer = 222
		}
		id, err := st.CreateGiftCode(ctx, GiftCode{Code: c, BuyerTelegramID: buyer, Months: 1})
		if err != nil {
			t.Fatalf("create %s: %v", c, err)
		}
		ids[c] = id
	}
	now := time.Now()
	for _, c := range []string{"I1", "I2"} {
		if ok, err := st.IssueGiftCode(ctx, ids[c], now); err != nil || !ok {
			t.Fatalf("issue %s: %v %v", c, ok, err)
		}
	}

	counts, err := st.CountGiftCodesByBuyer(ctx, 111)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts["pending"] != 3 || counts["issued"] != 2 || len(counts) != 2 {
		t.Fatalf("counts mismatch: %+v", counts)
	}

	// Newest first, limit/offset.
	page, err := st.ListGiftCodesByBuyerStatus(ctx, 111, "pending", 2, 0)
	if err != nil || len(page) != 2 || page[0].Code != "P3" || page[1].Code != "P2" {
		t.Fatalf("page 1 mismatch: %v %+v", err, page)
	}
	page, err = st.ListGiftCodesByBuyerStatus(ctx, 111, "pending", 2, 2)
	if err != nil || len(page) != 1 || page[0].Code != "P1" {
		t.Fatalf("page 2 mismatch: %v %+v", err, page)
	}

	// Another buyer's gifts stay invisible.
	page, err = st.ListGiftCodesByBuyerStatus(ctx, 222, "pending", 10, 0)
	if err != nil || len(page) != 1 || page[0].Code != "X1" {
		t.Fatalf("other buyer mismatch: %v %+v", err, page)
	}
	if c, err := st.CountGiftCodesByBuyer(ctx, 999); err != nil || len(c) != 0 {
		t.Fatalf("unknown buyer must have empty counts: %v %+v", err, c)
	}
}
