package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

func TestAdminPaymentReportMapsHistoryAndGuardsAdmin(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(time.Minute)
	svc.now = func() time.Time { return now }
	id, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 7, UUID: "uuid-7", Username: "alice", TelegramID: 77,
		Months: 3, Price: 450, ExpireAt: now.AddDate(0, 1, 0), Provider: ProviderPlatega,
		PayerTelegramID: 88, PayerUsername: "payer", ProviderTxnID: "txn-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmPaymentRequest(ctx, id, now); err != nil {
		t.Fatal(err)
	}

	filter := WebPaymentFilter{Days: 30, Status: "all", Provider: "all", Page: 1}
	if _, err := svc.AdminPaymentReport(ctx, 2000, filter); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin error = %v", err)
	}
	report, err := svc.AdminPaymentReport(ctx, 1000, filter)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || len(report.Items) != 1 || report.Items[0].ProviderTxnID != "txn-7" {
		t.Fatalf("unexpected history: %+v", report)
	}
	if report.Analytics.RevenueLabel != "450₽" || report.Analytics.ConversionRate != 100 {
		t.Fatalf("unexpected analytics: %+v", report.Analytics)
	}
	if report.TotalPages != 1 || report.PageSize != 25 || report.Items[0].ResolvedAt == "" {
		t.Fatalf("unexpected paging/record: %+v", report)
	}
}

func TestAdminPaymentReportRejectsInvalidFilter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	_, err := svc.AdminPaymentReport(context.Background(), 1000, WebPaymentFilter{
		Days: 14, Status: "all", Provider: "all", Page: 1,
	})
	if !errors.Is(err, ErrBadInput) {
		t.Fatalf("error = %v, want ErrBadInput", err)
	}
}
