package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReadPaymentReportAnalyticsAndFilters(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	exp := now.AddDate(0, 1, 0)

	create := func(username, payer, provider, txn string, price int, created time.Time) int64 {
		t.Helper()
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: idForTest(username), Username: username,
			TelegramID: 1000 + idForTest(username), Months: 1, Price: price, ExpireAt: exp,
			PayerTelegramID: 2000 + idForTest(username), PayerUsername: payer,
			Provider: provider, ProviderTxnID: txn,
		})
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		if _, err := st.db.ExecContext(ctx, `UPDATE payment_requests SET created_at = ? WHERE id = ?`, formatTime(created), id); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
		return id
	}

	p2p := create("alice", "payer-a", "p2p", "", 100, now.Add(-3*24*time.Hour))
	platega := create("bob", "payer-b", "platega", "plate-22", 200, now.Add(-4*24*time.Hour))
	create("carol", "payer-c", "telegram_stars", "stars-33", 300, now.Add(-24*time.Hour))
	old := create("old-user", "old-payer", "p2p", "", 900, now.Add(-60*24*time.Hour))
	if _, err := st.ConfirmPaymentRequest(ctx, p2p, now.Add(-2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RejectPaymentRequest(ctx, platega, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmPaymentRequest(ctx, old, now.Add(-59*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// A paid gift and an approved invite inside the window: they now appear in
	// the unified history (interleaved by created_at) and contribute to total
	// revenue, but never to the payment_requests ledger, providers, or daily
	// bars. created_at is pinned so the newest-first order below is deterministic.
	gift, err := st.CreateGiftCode(ctx, GiftCode{Code: "GIFT-1", BuyerTelegramID: 1, Months: 1, Price: 500})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueGiftCode(ctx, gift, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE gift_codes SET created_at = ? WHERE id = ?`, formatTime(now.Add(-36*time.Hour)), gift); err != nil {
		t.Fatalf("set gift created_at: %v", err)
	}
	invite, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 1, NewUsername: "invitee", Months: 1, Price: 700, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveInviteRequest(ctx, invite, "approved", now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE invite_requests SET created_at = ? WHERE id = ?`, formatTime(now.Add(-60*time.Hour)), invite); err != nil {
		t.Fatalf("set invite created_at: %v", err)
	}

	report, err := st.ReadPaymentReport(ctx, PaymentReportFilter{Since: now.Add(-30 * 24 * time.Hour), Status: "all", Provider: "all", Limit: 25})
	if err != nil {
		t.Fatalf("ReadPaymentReport: %v", err)
	}
	if report.Total != 5 || len(report.Items) != 5 {
		t.Fatalf("history total/items = %d/%d, want 5/5", report.Total, len(report.Items))
	}
	if report.Items[0].Username != "carol" || report.Items[4].Username != "bob" {
		t.Fatalf("unexpected newest-first order: %+v", report.Items)
	}
	if report.Items[1].Kind != "gift" || report.Items[1].Reference != "GIFT-1" {
		t.Fatalf("gift not in history as expected: %+v", report.Items[1])
	}
	if report.Items[2].Kind != "invite" || report.Items[2].CounterpartyName != "invitee" {
		t.Fatalf("invite not in history as expected: %+v", report.Items[2])
	}
	if report.Analytics.Confirmed != 1 || report.Analytics.Rejected != 1 || report.Analytics.Pending != 1 || report.Analytics.Revenue != 100 {
		t.Fatalf("unexpected analytics: %+v", report.Analytics)
	}
	if report.Analytics.GiftRevenue != 500 || report.Analytics.InviteRevenue != 700 || report.Analytics.TotalRevenue != 1300 {
		t.Fatalf("unexpected total revenue breakdown: %+v", report.Analytics)
	}
	if len(report.Analytics.Daily) != 1 || report.Analytics.Daily[0].Revenue != 100 {
		t.Fatalf("unexpected daily stats: %+v", report.Analytics.Daily)
	}
	if len(report.Analytics.Providers) != 3 {
		t.Fatalf("provider stats = %+v", report.Analytics.Providers)
	}

	filtered, err := st.ReadPaymentReport(ctx, PaymentReportFilter{
		Since: now.Add(-30 * 24 * time.Hour), Status: "pending", Provider: "telegram_stars", Query: "STARS-33", Limit: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Username != "carol" {
		t.Fatalf("unexpected filtered history: %+v", filtered)
	}
	if filtered.Analytics.Pending != 1 || filtered.Analytics.Confirmed != 0 {
		t.Fatalf("provider filter did not affect analytics: %+v", filtered.Analytics)
	}
	if filtered.Analytics.GiftRevenue != 0 || filtered.Analytics.InviteRevenue != 0 || filtered.Analytics.TotalRevenue != 0 {
		t.Fatalf("provider filter should exclude gift/invite revenue: %+v", filtered.Analytics)
	}
}

func TestReadPaymentReportKindFilter(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-30 * 24 * time.Hour)

	if _, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 1, Username: "payer-user", TelegramID: 11,
		Months: 1, Price: 100, ExpireAt: now.AddDate(0, 1, 0), Provider: "p2p",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGiftCode(ctx, GiftCode{Code: "GIFT-K", BuyerTelegramID: 22, BuyerUsername: "buyer", Months: 1, Price: 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInviteRequest(ctx, InviteRequest{InviterTelegramID: 33, InviterUsername: "inviter", NewUsername: "newbie", Months: 1, Price: 300, Status: "pending"}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		kind string
		want int
	}{{"all", 3}, {"payment", 1}, {"gift", 1}, {"invite", 1}} {
		report, err := st.ReadPaymentReport(ctx, PaymentReportFilter{Since: since, Status: "all", Provider: "all", Kind: c.kind, Limit: 25})
		if err != nil {
			t.Fatalf("kind %q: %v", c.kind, err)
		}
		if report.Total != c.want || len(report.Items) != c.want {
			t.Fatalf("kind %q: total/items = %d/%d, want %d", c.kind, report.Total, len(report.Items), c.want)
		}
		if c.kind != "all" && report.Items[0].Kind != c.kind {
			t.Fatalf("kind %q: item kind = %q", c.kind, report.Items[0].Kind)
		}
	}
}

func TestReadPaymentReportPaginationAndIDSearch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var lastID int64
	for i := 0; i < 27; i++ {
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: int64(i + 1), Username: fmt.Sprintf("user-%02d", i),
			TelegramID: int64(5000 + i), Months: 1, Price: 100, ExpireAt: now.AddDate(0, 1, 0), Provider: "p2p",
		})
		if err != nil {
			t.Fatal(err)
		}
		lastID = id
	}
	report, err := st.ReadPaymentReport(ctx, PaymentReportFilter{Since: now.Add(-time.Hour), Status: "all", Provider: "all", Limit: 25, Offset: 25})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 27 || len(report.Items) != 2 {
		t.Fatalf("pagination total/items = %d/%d", report.Total, len(report.Items))
	}
	searched, err := st.ReadPaymentReport(ctx, PaymentReportFilter{
		Since: now.Add(-time.Hour), Status: "all", Provider: "all", Query: fmt.Sprint(lastID), Limit: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if searched.Total != 1 || searched.Items[0].ID != lastID {
		t.Fatalf("ID search = %+v", searched.Items)
	}
}

func idForTest(s string) int64 {
	var out int64
	for _, r := range s {
		out += int64(r)
	}
	return out
}
