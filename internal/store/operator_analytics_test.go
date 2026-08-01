package store

import (
	"context"
	"testing"
	"time"
)

func TestReadOperatorAnalyticsEmpty(t *testing.T) {
	st := newTestStore(t)
	got, err := st.ReadOperatorAnalytics(context.Background(), time.Now().Add(-30*24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ReadOperatorAnalytics: %v", err)
	}
	if got.Revenue.TotalRevenue != 0 || got.Renewals.Payments != 0 || got.ChurnWinback.LocalLapsedUsers != 0 || len(got.ProviderMix) != 0 {
		t.Fatalf("empty analytics = %+v", got)
	}
}

func TestReadOperatorAnalyticsAggregatesLocalWorkflowHistory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-30 * 24 * time.Hour)
	exp := now.AddDate(0, 1, 0)

	createPayment := func(remnawaveID int64, username, provider, kind string, months, price, trafficGB int, createdAt time.Time) int64 {
		t.Helper()
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: remnawaveID, Username: username, TelegramID: 1000 + remnawaveID,
			Months: months, Price: price, ExpireAt: exp, Provider: provider, Kind: kind, TrafficGB: trafficGB,
		})
		if err != nil {
			t.Fatalf("create payment: %v", err)
		}
		if _, err := st.db.ExecContext(ctx, `UPDATE payment_requests SET created_at = ? WHERE id = ?`, formatTime(createdAt), id); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
		return id
	}

	old := createPayment(7, "alice", "p2p", PaymentKindSubscription, 1, 100, 0, since.Add(-10*24*time.Hour))
	if _, err := st.ConfirmPaymentRequest(ctx, old, since.Add(-9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	sub := createPayment(7, "alice", "p2p", PaymentKindSubscription, 3, 450, 0, now.Add(-4*24*time.Hour))
	if _, err := st.ConfirmPaymentRequest(ctx, sub, now.Add(-3*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	traffic := createPayment(8, "bob", "platega", PaymentKindTrafficExtension, 0, 200, 25, now.Add(-2*24*time.Hour))
	if _, err := st.ConfirmTrafficExtensionRequest(ctx, traffic, now.Add(-2*24*time.Hour), now.AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	pendingTraffic := createPayment(9, "carol", "telegram_stars", PaymentKindTrafficExtension, 0, 300, 50, now.Add(-time.Hour))
	_ = pendingTraffic
	rejected := createPayment(10, "dave", "telegram_stars", PaymentKindSubscription, 1, 150, 0, now.Add(-time.Hour))
	if _, err := st.RejectPaymentRequest(ctx, rejected, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	gift, err := st.CreateGiftCode(ctx, GiftCode{Code: "GIFT-A", BuyerTelegramID: 1, Months: 1, Price: 90})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueGiftCode(ctx, gift, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE gift_codes SET created_at = ? WHERE id = ?`, formatTime(now.Add(-25*time.Hour)), gift); err != nil {
		t.Fatalf("set gift created_at: %v", err)
	}
	redeemed, err := st.CreateGiftCode(ctx, GiftCode{Code: "GIFT-R", BuyerTelegramID: 2, Months: 1, Price: 110})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueGiftCode(ctx, redeemed, now.Add(-20*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RedeemGiftCode(ctx, "GIFT-R", 3, "redeemer", now.Add(-19*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pendingGift, err := st.CreateGiftCode(ctx, GiftCode{Code: "GIFT-P", BuyerTelegramID: 4, Months: 1, Price: 70})
	if err != nil || pendingGift == 0 {
		t.Fatalf("pending gift: %d %v", pendingGift, err)
	}

	invite, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 5, NewUsername: "newbie", Months: 1, Price: 120, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveInviteRequest(ctx, invite, "approved", now.Add(-18*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if ok, err := st.CreateReferral(ctx, 777, 100, now.Add(-5*24*time.Hour)); err != nil || !ok {
		t.Fatalf("create credited referral: %v %v", ok, err)
	}
	if ok, err := st.CreditReferral(ctx, 777, now.Add(-4*24*time.Hour)); err != nil || !ok {
		t.Fatalf("credit referral: %v %v", ok, err)
	}
	if ok, err := st.CreateReferral(ctx, 778, 100, now.Add(-2*24*time.Hour)); err != nil || !ok {
		t.Fatalf("create pending referral: %v %v", ok, err)
	}

	if err := st.UpsertNotifiedUser(ctx, NotifiedUser{RemnawaveID: 7, Username: "alice", ExpireAt: now.Add(-10 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNotifiedUser(ctx, NotifiedUser{RemnawaveID: 11, Username: "lapsed", ExpireAt: now.Add(-5 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO sent_notifications (remnawave_user_id, kind, milestone, expire_at, sent_at)
		VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)
	`, int64(7), NotificationWinback, 1, formatTime(now.Add(-10*24*time.Hour)), formatTime(now.Add(-6*24*time.Hour)),
		int64(11), NotificationWinback, 1, formatTime(now.Add(-5*24*time.Hour)), formatTime(now.Add(-4*24*time.Hour))); err != nil {
		t.Fatalf("insert winback notifications: %v", err)
	}

	got, err := st.ReadOperatorAnalytics(ctx, since, now)
	if err != nil {
		t.Fatalf("ReadOperatorAnalytics: %v", err)
	}
	if got.Revenue.SubscriptionRevenue != 450 || got.Revenue.TrafficExtensionRevenue != 200 ||
		got.Revenue.GiftRevenue != 200 || got.Revenue.InviteRevenue != 120 || got.Revenue.TotalRevenue != 970 {
		t.Fatalf("revenue = %+v", got.Revenue)
	}
	if got.Renewals.Payments != 1 || got.Renewals.UniqueProfiles != 1 || got.Renewals.RepeatProfiles != 1 || got.Renewals.MonthsAdded != 3 {
		t.Fatalf("renewals = %+v", got.Renewals)
	}
	if got.ChurnWinback.WinbackSent != 2 || got.ChurnWinback.WinbackConverted != 1 || got.ChurnWinback.LocalLapsedUsers != 1 {
		t.Fatalf("churn/winback = %+v", got.ChurnWinback)
	}
	if got.Referrals.Created != 2 || got.Referrals.Credited != 1 || got.Referrals.Pending != 1 {
		t.Fatalf("referrals = %+v", got.Referrals)
	}
	if got.Gifts.Created != 3 || got.Gifts.Pending != 1 || got.Gifts.Issued != 1 || got.Gifts.Redeemed != 1 || got.Gifts.Revenue != 200 {
		t.Fatalf("gifts = %+v", got.Gifts)
	}
	if got.TrafficExtensions.Confirmed != 1 || got.TrafficExtensions.Pending != 1 || got.TrafficExtensions.Active != 1 ||
		got.TrafficExtensions.TrafficGB != 25 || got.TrafficExtensions.Revenue != 200 {
		t.Fatalf("traffic = %+v", got.TrafficExtensions)
	}
	providers := map[string]ProviderMixStat{}
	for _, p := range got.ProviderMix {
		providers[p.Provider] = p
	}
	if providers["p2p"].Revenue != 450 || providers["platega"].Revenue != 200 || providers["telegram_stars"].Pending != 1 || providers["telegram_stars"].Rejected != 1 {
		t.Fatalf("providers = %+v", got.ProviderMix)
	}
}
