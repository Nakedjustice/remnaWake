package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTrafficExtensionOptionCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.UpsertTrafficExtensionOption(ctx, 50, 300); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.GetTrafficExtensionOption(ctx, 50)
	if err != nil || got == nil || got.Price != 300 {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := st.UpsertTrafficExtensionOption(ctx, 50, 350); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, err := st.ListTrafficExtensionOptions(ctx)
	if err != nil || len(list) != 1 || list[0].Price != 350 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if ok, err := st.DeleteTrafficExtensionOption(ctx, 50); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
}

func TestTrafficExtensionRequestBlocksPendingAndActive(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	req := PaymentRequest{RemnawaveID: 1, UUID: "u", Username: "alice", TelegramID: 7, Price: 100, ExpireAt: now.AddDate(0, 1, 0), TrafficGB: 20, BaseTrafficLimitBytes: 100, ExtraTrafficBytes: 20}
	id, err := st.CreateTrafficExtensionPaymentRequest(ctx, req, now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateTrafficExtensionPaymentRequest(ctx, req, now); !errors.Is(err, ErrTrafficExtensionActive) {
		t.Fatalf("second create err = %v", err)
	}
	if ok, err := st.ConfirmTrafficExtensionRequest(ctx, id, now, now.AddDate(0, 0, 30)); err != nil || !ok {
		t.Fatalf("confirm = %v, %v", ok, err)
	}
	if _, err := st.CreateTrafficExtensionPaymentRequest(ctx, req, now.AddDate(0, 0, 1)); !errors.Is(err, ErrTrafficExtensionActive) {
		t.Fatalf("active create err = %v", err)
	}
	if err := st.MarkActiveTrafficExtensionsRestored(ctx, "u", now.AddDate(0, 0, 31)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := st.CreateTrafficExtensionPaymentRequest(ctx, req, now.AddDate(0, 0, 31)); err != nil {
		t.Fatalf("create after restore: %v", err)
	}
}
