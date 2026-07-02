package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestPlansCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.ListPlans(ctx); err != nil || len(got) != 0 {
		t.Fatalf("ListPlans empty = %v, %v", got, err)
	}
	if err := st.UpsertPlan(ctx, Plan{Code: "messenger", Name: "Мессенджер", Tag: "MESSENGER", SquadUUID: "sq-1", TrafficGB: 30, DeviceLimit: 1}); err != nil {
		t.Fatalf("UpsertPlan: %v", err)
	}
	if err := st.UpsertPlan(ctx, Plan{Code: "messenger", Name: "Мессенджер+", Tag: "MESSENGER", SquadUUID: "sq-2", TrafficGB: 50, DeviceLimit: 2}); err != nil {
		t.Fatalf("UpsertPlan update: %v", err)
	}

	got, err := st.GetPlan(ctx, "messenger")
	if err != nil || got == nil {
		t.Fatalf("GetPlan = %v, %v", got, err)
	}
	if got.Name != "Мессенджер+" || got.SquadUUID != "sq-2" || got.TrafficGB != 50 || got.DeviceLimit != 2 || got.Tag != "MESSENGER" {
		t.Fatalf("plan not updated: %+v", got)
	}
	if missing, err := st.GetPlan(ctx, "nope"); err != nil || missing != nil {
		t.Fatalf("GetPlan miss = %v, %v", missing, err)
	}

	list, err := st.ListPlans(ctx)
	if err != nil || len(list) != 1 || list[0].Code != "messenger" {
		t.Fatalf("ListPlans = %v, %v", list, err)
	}
}

func TestDeletePlanCascadesTariffs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.UpsertPlan(ctx, Plan{Code: "messenger", Name: "Мессенджер"}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertTariff(ctx, "messenger", 1, 100)
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)
	_ = st.UpsertTariff(ctx, PlanStandard, 1, 300)

	deleted, err := st.DeletePlan(ctx, "messenger")
	if err != nil || !deleted {
		t.Fatalf("DeletePlan = %v, %v", deleted, err)
	}
	if again, err := st.DeletePlan(ctx, "messenger"); err != nil || again {
		t.Fatalf("DeletePlan repeat = %v, %v", again, err)
	}
	if left, _ := st.ListTariffs(ctx, "messenger"); len(left) != 0 {
		t.Fatalf("messenger tariffs not cascaded: %v", left)
	}
	if std, _ := st.ListTariffs(ctx, PlanStandard); len(std) != 1 {
		t.Fatalf("standard tariffs affected: %v", std)
	}
}

func TestTariffsPerPlanCoexist(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.UpsertTariff(ctx, PlanStandard, 3, 800)
	_ = st.UpsertTariff(ctx, "messenger", 3, 250)

	std, _ := st.GetTariff(ctx, PlanStandard, 3)
	msg, _ := st.GetTariff(ctx, "messenger", 3)
	if std == nil || msg == nil || std.Price != 800 || msg.Price != 250 {
		t.Fatalf("per-plan tariffs wrong: std=%+v msg=%+v", std, msg)
	}

	all, err := st.ListAllTariffs(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAllTariffs = %v, %v", all, err)
	}
	// Ordered by plan then months: messenger < standard.
	if all[0].Plan != "messenger" || all[1].Plan != PlanStandard {
		t.Fatalf("order wrong: %v", all)
	}
}

// TestTariffPlanMigration verifies that a database created before the plan
// dimension is rebuilt in place: old rows land under 'standard' and the new
// composite key allows same-months tariffs in other plans.
func TestTariffPlanMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE tariffs (
		  months INTEGER PRIMARY KEY, price INTEGER NOT NULL,
		  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		INSERT INTO tariffs (months, price, created_at, updated_at)
		VALUES (1, 150, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (3, 450, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := New(path)
	if err != nil {
		t.Fatalf("New old database: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	std, err := st.ListTariffs(ctx, PlanStandard)
	if err != nil || len(std) != 2 || std[0].Months != 1 || std[0].Price != 150 || std[1].Price != 450 {
		t.Fatalf("migrated tariffs = %v, %v", std, err)
	}
	if err := st.UpsertTariff(ctx, "messenger", 3, 250); err != nil {
		t.Fatalf("insert same months in other plan: %v", err)
	}

	// Re-open to prove the migration is idempotent.
	_ = st.Close()
	st2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if all, _ := st2.ListAllTariffs(ctx); len(all) != 3 {
		t.Fatalf("tariffs after reopen = %v", all)
	}
}

func TestPaymentRequestPlanRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 1, UUID: "u-1", Username: "alice", TelegramID: 42,
		Months: 3, Price: 250, Plan: "messenger",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPaymentRequest(ctx, id)
	if err != nil || got == nil || got.Plan != "messenger" {
		t.Fatalf("plan round-trip = %+v, %v", got, err)
	}

	// An empty plan defaults to standard on insert.
	id2, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 2, UUID: "u-2", Username: "bob", TelegramID: 43, Months: 1, Price: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := st.GetPaymentRequest(ctx, id2)
	if got2 == nil || got2.Plan != PlanStandard {
		t.Fatalf("default plan = %+v", got2)
	}
}
