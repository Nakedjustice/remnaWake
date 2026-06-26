package store

import (
	"context"
	"testing"
	"time"
)

func TestInfraServersCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.ListInfraServers(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty list: got %v err %v", got, err)
	}

	due := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	id, err := st.CreateInfraServer(ctx, InfraServer{
		Name: "edge-1", Hoster: "Hetzner", Country: "DE", CPUCores: 4, RAMMB: 8192,
		Price: 30, Currency: "EUR", PeriodMonths: 3, NextDueAt: due, Notes: "primary",
	})
	if err != nil || id == 0 {
		t.Fatalf("create: id=%d err=%v", id, err)
	}
	// A server without a due date sorts after one that has it.
	if _, err := st.CreateInfraServer(ctx, InfraServer{Name: "spare", Currency: "USD", PeriodMonths: 1, Price: 5}); err != nil {
		t.Fatalf("create spare: %v", err)
	}

	list, err := st.ListInfraServers(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d err %v", len(list), err)
	}
	if list[0].Name != "edge-1" || list[1].Name != "spare" {
		t.Fatalf("order: %+v", list)
	}

	got, err := st.GetInfraServer(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %+v err %v", got, err)
	}
	if got.Hoster != "Hetzner" || got.RAMMB != 8192 || got.Currency != "EUR" ||
		got.PeriodMonths != 3 || !got.NextDueAt.Equal(due) || got.Notes != "primary" {
		t.Fatalf("got: %+v", got)
	}
	if missing, err := st.GetInfraServer(ctx, 9999); err != nil || missing != nil {
		t.Fatalf("missing: %+v err %v", missing, err)
	}

	// Reminder stage round-trips and an update resets it.
	if ok, err := st.SetInfraServerRemindedStage(ctx, id, 2); err != nil || !ok {
		t.Fatalf("set stage: %v %v", ok, err)
	}
	got, _ = st.GetInfraServer(ctx, id)
	if got.RemindedStage != 2 {
		t.Fatalf("stage = %d, want 2", got.RemindedStage)
	}
	got.Price = 33
	next := due.AddDate(0, 3, 0)
	got.NextDueAt = next
	if ok, err := st.UpdateInfraServer(ctx, *got); err != nil || !ok {
		t.Fatalf("update: %v %v", ok, err)
	}
	got, _ = st.GetInfraServer(ctx, id)
	if got.Price != 33 || got.RemindedStage != 0 || !got.NextDueAt.Equal(next) {
		t.Fatalf("after update: %+v", got)
	}

	if ok, err := st.DeleteInfraServer(ctx, id); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, err := st.DeleteInfraServer(ctx, id); err != nil || ok {
		t.Fatalf("delete missing: %v %v", ok, err)
	}
}
