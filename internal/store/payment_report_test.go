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
			RemnawaveID: idForTest(username), UUID: "uuid-" + username, Username: username,
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

	report, err := st.ReadPaymentReport(ctx, PaymentReportFilter{Since: now.Add(-30 * 24 * time.Hour), Status: "all", Provider: "all", Limit: 25})
	if err != nil {
		t.Fatalf("ReadPaymentReport: %v", err)
	}
	if report.Total != 3 || len(report.Items) != 3 {
		t.Fatalf("history total/items = %d/%d, want 3/3", report.Total, len(report.Items))
	}
	if report.Items[0].Username != "carol" || report.Items[2].Username != "bob" {
		t.Fatalf("unexpected newest-first order: %+v", report.Items)
	}
	if report.Analytics.Confirmed != 1 || report.Analytics.Rejected != 1 || report.Analytics.Pending != 1 || report.Analytics.Revenue != 100 {
		t.Fatalf("unexpected analytics: %+v", report.Analytics)
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
}

func TestReadPaymentReportPaginationAndIDSearch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var lastID int64
	for i := 0; i < 27; i++ {
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: int64(i + 1), UUID: fmt.Sprintf("uuid-%d", i), Username: fmt.Sprintf("user-%02d", i),
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
