package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdminStatsDataCountsPanelUsers(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.finder = &fakeFinder{all: []Subscriber{
		{Status: "ACTIVE", ExpireAt: now.Add(3 * 24 * time.Hour), TelegramID: 11},
		{Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour)},
		{Status: "EXPIRED", ExpireAt: now.Add(-24 * time.Hour), TelegramID: 12},
		{Status: "ACTIVE", ExpireAt: now.Add(-24 * time.Hour)},
	}}

	got, err := svc.AdminStatsData(context.Background(), 1000)
	if err != nil {
		t.Fatalf("AdminStatsData: %v", err)
	}
	if got.UsersTotal != 4 || got.UsersActive != 2 || got.UsersExpiringSoon != 1 || got.UsersExpired != 2 || got.UsersLinked != 2 {
		t.Fatalf("unexpected user counters: %+v", got)
	}
	if got.PaymentsConfirmed30d != 0 || got.PaymentsPending != 0 || got.Revenue != 0 || got.RevenueLabel != "0₽" {
		t.Fatalf("unexpected empty payment counters: %+v", got)
	}
}

func TestAdminStatsDataRejectsNonAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.AdminStatsData(context.Background(), 2222); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("error = %v, want ErrNotAdmin", err)
	}
}
