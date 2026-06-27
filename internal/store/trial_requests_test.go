package store

import (
	"context"
	"testing"
	"time"
)

func TestTrialRequestLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

	id, err := st.CreateTrialRequest(ctx, 777, "alice", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("create returned id 0")
	}

	got, err := st.GetTrialRequest(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get returned nil")
	}
	if got.TelegramID != 777 || got.Username != "alice" || got.Status != "pending" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if got.ResolvedAt != nil {
		t.Fatal("pending request should have no resolved_at")
	}

	pending, err := st.GetPendingTrialRequestByTelegramID(ctx, 777)
	if err != nil || pending == nil || pending.ID != id {
		t.Fatalf("pending lookup: req=%+v err=%v", pending, err)
	}

	list, err := st.ListTrialRequestsByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list pending = %+v", list)
	}

	// Resolve once.
	ok, err := st.ResolveTrialRequest(ctx, id, "approved", now.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	// Resolving again is a no-op.
	if ok, err := st.ResolveTrialRequest(ctx, id, "rejected", now.Add(2*time.Hour)); err != nil || ok {
		t.Fatalf("second resolve must be no-op: ok=%v err=%v", ok, err)
	}

	got, err = st.GetTrialRequest(ctx, id)
	if err != nil {
		t.Fatalf("get after resolve: %v", err)
	}
	if got.Status != "approved" || got.ResolvedAt == nil {
		t.Fatalf("resolved request = %+v", got)
	}

	// No longer pending.
	if pending, err := st.GetPendingTrialRequestByTelegramID(ctx, 777); err != nil || pending != nil {
		t.Fatalf("after resolve pending must be nil: req=%+v err=%v", pending, err)
	}
	if list, err := st.ListTrialRequestsByStatus(ctx, "pending"); err != nil || len(list) != 0 {
		t.Fatalf("after resolve pending list = %+v err=%v", list, err)
	}
}

func TestGetTrialRequestMissing(t *testing.T) {
	st := newTestStore(t)
	got, err := st.GetTrialRequest(context.Background(), 999)
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing request, got %+v", got)
	}
}
