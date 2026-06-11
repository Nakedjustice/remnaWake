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

	if _, err := st.CreateGiftCode(ctx, GiftCode{Code: "AAAA", BuyerTelegramID: 1, Months: 1}); err != nil {
		t.Fatalf("gift: %v", err)
	}
	if _, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 1, NewUsername: "bob", Months: 1, Status: "pending"}); err != nil {
		t.Fatalf("invite: %v", err)
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
	if got.GiftsByStatus["pending"] != 1 {
		t.Fatalf("GiftsByStatus = %v, want pending:1", got.GiftsByStatus)
	}
	if got.InvitesPending != 1 {
		t.Fatalf("InvitesPending = %d, want 1", got.InvitesPending)
	}
}
