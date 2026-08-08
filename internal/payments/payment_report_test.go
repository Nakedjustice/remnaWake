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
		RemnawaveID: 7, Username: "alice", TelegramID: 77,
		Months: 3, Price: 450, ExpireAt: now.AddDate(0, 1, 0), Provider: ProviderPlatega,
		PayerTelegramID: 88, PayerUsername: "payer", ProviderTxnID: "txn-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmPaymentRequest(ctx, id, now); err != nil {
		t.Fatal(err)
	}
	gift, err := st.CreateGiftCode(ctx, store.GiftCode{Code: "GIFT-7", BuyerTelegramID: 1, Months: 1, Price: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueGiftCode(ctx, gift, now); err != nil {
		t.Fatal(err)
	}
	invite, err := st.CreateInviteRequest(ctx, store.InviteRequest{InviterTelegramID: 1, NewUsername: "invitee", Months: 1, Price: 50, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveInviteRequest(ctx, invite, "approved", now); err != nil {
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
	// The history now merges the payment, the gift and the invite.
	if report.Total != 3 || len(report.Items) != 3 {
		t.Fatalf("unexpected history total/items: %+v", report)
	}
	byKind := map[string]WebPaymentRecord{}
	for _, it := range report.Items {
		byKind[it.Kind] = it
	}
	if pay := byKind["payment"]; pay.ProviderTxnID != "txn-7" || pay.Provider != ProviderPlatega || pay.ResolvedAt == "" {
		t.Fatalf("unexpected payment record: %+v", pay)
	}
	if g := byKind["gift"]; g.ProviderTxnID != "GIFT-7" || g.Provider != "" {
		t.Fatalf("unexpected gift record: %+v", g)
	}
	if inv := byKind["invite"]; inv.PayerUsername != "invitee" || inv.Provider != "" {
		t.Fatalf("unexpected invite record: %+v", inv)
	}
	if report.Analytics.RevenueLabel != "450₽" || report.Analytics.ConversionRate != 100 {
		t.Fatalf("unexpected analytics: %+v", report.Analytics)
	}
	if report.Analytics.GiftRevenue != 100 || report.Analytics.InviteRevenue != 50 || report.Analytics.TotalRevenueLabel != "600₽" {
		t.Fatalf("unexpected revenue breakdown: %+v", report.Analytics)
	}
	if report.TotalPages != 1 || report.PageSize != 25 {
		t.Fatalf("unexpected paging: %+v", report)
	}
}

func TestAdminPaymentReportRejectsInvalidFilter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.AdminPaymentReport(ctx, 1000, WebPaymentFilter{
		Days: 14, Status: "all", Provider: "all", Page: 1,
	}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("days: error = %v, want ErrBadInput", err)
	}
	if _, err := svc.AdminPaymentReport(ctx, 1000, WebPaymentFilter{
		Days: 30, Status: "all", Provider: "all", Kind: "bogus", Page: 1,
	}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("kind: error = %v, want ErrBadInput", err)
	}
}
