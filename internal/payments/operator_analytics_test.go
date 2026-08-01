package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

func TestAdminOperatorAnalyticsMapsLocalStoreAndGuardsAdmin(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	id, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 7, Username: "alice", TelegramID: 77,
		Months: 2, Price: 300, ExpireAt: now.AddDate(0, 1, 0), Provider: ProviderP2P,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmPaymentRequest(ctx, id, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateReferral(ctx, 77, 1000, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.AdminOperatorAnalytics(ctx, 2000, 30); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin error = %v", err)
	}
	got, err := svc.AdminOperatorAnalytics(ctx, 1000, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got.RangeDays != 30 || got.GeneratedAt != now.Format(time.RFC3339) {
		t.Fatalf("range/generated = %d/%q", got.RangeDays, got.GeneratedAt)
	}
	if got.Revenue.SubscriptionRevenue != 300 || got.Revenue.SubscriptionRevenueLabel != "300₽" || got.Revenue.TotalRevenueLabel != "300₽" {
		t.Fatalf("revenue = %+v", got.Revenue)
	}
	if got.Renewals.Payments != 1 || got.Renewals.AverageMonths != 2 {
		t.Fatalf("renewals = %+v", got.Renewals)
	}
	if got.Referrals.Created != 1 || got.Referrals.ConversionRate != 0 {
		t.Fatalf("referrals = %+v", got.Referrals)
	}
	if len(got.ProviderMix) != 1 || got.ProviderMix[0].Provider != ProviderP2P || got.ProviderMix[0].RevenueShare != 100 {
		t.Fatalf("provider mix = %+v", got.ProviderMix)
	}
	if ext.calls != 0 {
		t.Fatalf("analytics must not mutate Remnawave, extender calls=%d", ext.calls)
	}
}

func TestAdminOperatorAnalyticsRejectsInvalidDays(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.AdminOperatorAnalytics(context.Background(), 1000, 14); !errors.Is(err, ErrBadInput) {
		t.Fatalf("error = %v, want ErrBadInput", err)
	}
}
