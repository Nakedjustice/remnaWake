package store

import (
	"context"
	"testing"
	"time"
)

func TestReadAdminStats(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	mkReq := func() int64 {
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: 1, UUID: "u", Username: "alice", TelegramID: 1,
			Months: 1, Price: 150, ExpireAt: exp, Status: "pending",
		})
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		return id
	}

	// One pending, one confirmed inside the window, one confirmed before it.
	mkReq()
	recent := mkReq()
	old := mkReq()
	if _, err := st.ConfirmPaymentRequest(ctx, recent, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("confirm recent: %v", err)
	}
	if _, err := st.ConfirmPaymentRequest(ctx, old, now.Add(-60*24*time.Hour)); err != nil {
		t.Fatalf("confirm old: %v", err)
	}

	// A pending gift (no revenue), a gift issued inside the window, a gift
	// redeemed inside the window, and a gift issued before it (excluded).
	if _, err := st.CreateGiftCode(ctx, GiftCode{Code: "AAAA", BuyerTelegramID: 1, Months: 1}); err != nil {
		t.Fatalf("gift: %v", err)
	}
	issuedGift, err := st.CreateGiftCode(ctx, GiftCode{Code: "BBBB", BuyerTelegramID: 1, Months: 1, Price: 200})
	if err != nil {
		t.Fatalf("issued gift: %v", err)
	}
	if _, err := st.IssueGiftCode(ctx, issuedGift, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("issue gift: %v", err)
	}
	redeemedGift, err := st.CreateGiftCode(ctx, GiftCode{Code: "CCCC", BuyerTelegramID: 1, Months: 1, Price: 300})
	if err != nil {
		t.Fatalf("redeemed gift: %v", err)
	}
	if _, err := st.IssueGiftCode(ctx, redeemedGift, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("issue gift for redeem: %v", err)
	}
	if _, err := st.RedeemGiftCode(ctx, "CCCC", 2, "carol", now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("redeem gift: %v", err)
	}
	oldGift, err := st.CreateGiftCode(ctx, GiftCode{Code: "DDDD", BuyerTelegramID: 1, Months: 1, Price: 999})
	if err != nil {
		t.Fatalf("old gift: %v", err)
	}
	if _, err := st.IssueGiftCode(ctx, oldGift, now.Add(-60*24*time.Hour)); err != nil {
		t.Fatalf("issue old gift: %v", err)
	}

	// A pending invite (no revenue), an invite approved inside the window, and
	// one approved before it (excluded).
	if _, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 1, NewUsername: "bob", Months: 1, Status: "pending"}); err != nil {
		t.Fatalf("invite: %v", err)
	}
	approvedInvite, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 1, NewUsername: "dave", Months: 1, Price: 150, Status: "pending"})
	if err != nil {
		t.Fatalf("approved invite: %v", err)
	}
	if _, err := st.ResolveInviteRequest(ctx, approvedInvite, "approved", now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("approve invite: %v", err)
	}
	oldInvite, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 1, NewUsername: "erin", Months: 1, Price: 999, Status: "pending"})
	if err != nil {
		t.Fatalf("old invite: %v", err)
	}
	if _, err := st.ResolveInviteRequest(ctx, oldInvite, "approved", now.Add(-60*24*time.Hour)); err != nil {
		t.Fatalf("approve old invite: %v", err)
	}

	got, err := st.ReadAdminStats(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("ReadAdminStats: %v", err)
	}
	if got.PaymentsPending != 1 {
		t.Fatalf("PaymentsPending = %d, want 1", got.PaymentsPending)
	}
	if got.PaymentsConfirmed != 1 || got.Revenue != 150 {
		t.Fatalf("Confirmed = %d Revenue = %d, want 1/150", got.PaymentsConfirmed, got.Revenue)
	}
	if got.GiftsRevenue != 500 {
		t.Fatalf("GiftsRevenue = %d, want 500 (issued+redeemed inside window)", got.GiftsRevenue)
	}
	if got.InvitesRevenue != 150 {
		t.Fatalf("InvitesRevenue = %d, want 150", got.InvitesRevenue)
	}
	if got.GiftsByStatus["pending"] != 1 {
		t.Fatalf("GiftsByStatus = %v, want pending:1", got.GiftsByStatus)
	}
	if got.InvitesPending != 1 {
		t.Fatalf("InvitesPending = %d, want 1", got.InvitesPending)
	}
}
