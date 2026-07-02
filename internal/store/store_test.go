package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNewCreatesSchema(t *testing.T) {
	st := newTestStore(t)
	for _, table := range []string{"tariffs", "notified_users", "payment_requests"} {
		if _, err := st.db.Exec("SELECT 1 FROM " + table + " WHERE 1=0"); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestNewMigratesPaymentRequestIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE payment_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT, remnawave_user_id INTEGER NOT NULL,
		uuid TEXT NOT NULL, username TEXT NOT NULL, telegram_id INTEGER NOT NULL,
		months INTEGER NOT NULL, price INTEGER NOT NULL, expire_at TEXT NOT NULL,
		status TEXT NOT NULL, created_at TEXT NOT NULL, confirmed_at TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := New(path)
	if err != nil {
		t.Fatalf("New old database: %v", err)
	}
	defer st.Close()
	rows, err := st.db.Query(`PRAGMA index_list(payment_requests)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexes := map[string]bool{}
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		indexes[name] = true
	}
	for _, name := range []string{"idx_payment_requests_created_at", "idx_payment_requests_status_resolved", "idx_payment_requests_provider", "idx_payment_requests_provider_txn"} {
		if !indexes[name] {
			t.Errorf("missing migrated index %s", name)
		}
	}
}

func TestTariffsCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.ListTariffs(ctx, PlanStandard); err != nil || len(got) != 0 {
		t.Fatalf("empty list: got %v err %v", got, err)
	}
	if err := st.UpsertTariff(ctx, PlanStandard, 3, 450); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertTariff(ctx, PlanStandard, 1, 150); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertTariff(ctx, PlanStandard, 3, 500); err != nil { // update existing
		t.Fatalf("upsert update: %v", err)
	}

	list, err := st.ListTariffs(ctx, PlanStandard)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Months != 1 || list[1].Months != 3 || list[1].Price != 500 {
		t.Fatalf("unexpected list: %+v", list)
	}

	got, err := st.GetTariff(ctx, PlanStandard, 3)
	if err != nil || got == nil || got.Price != 500 {
		t.Fatalf("get: %+v err %v", got, err)
	}
	if missing, err := st.GetTariff(ctx, PlanStandard, 99); err != nil || missing != nil {
		t.Fatalf("get missing: %+v err %v", missing, err)
	}

	deleted, err := st.DeleteTariff(ctx, PlanStandard, 3)
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if again, err := st.DeleteTariff(ctx, PlanStandard, 3); err != nil || again {
		t.Fatalf("delete missing: %v %v", again, err)
	}
}

func TestNotifiedUsersUpsert(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	u := NotifiedUser{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 999, ExpireAt: exp}
	if err := st.UpsertNotifiedUser(ctx, u); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	u.Username = "alice2"
	if err := st.UpsertNotifiedUser(ctx, u); err != nil {
		t.Fatalf("upsert2: %v", err)
	}

	got, err := st.GetNotifiedUser(ctx, 42)
	if err != nil || got == nil {
		t.Fatalf("get: %+v err %v", got, err)
	}
	if got.Username != "alice2" || got.UUID != "uuid-42" || got.TelegramID != 999 || !got.ExpireAt.Equal(exp) {
		t.Fatalf("unexpected: %+v", got)
	}
	if missing, err := st.GetNotifiedUser(ctx, 7); err != nil || missing != nil {
		t.Fatalf("missing: %+v err %v", missing, err)
	}
}

func TestPaymentRequestsLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 999,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})
	if err != nil || id == 0 {
		t.Fatalf("create: id=%d err=%v", id, err)
	}

	got, err := st.GetPaymentRequest(ctx, id)
	if err != nil || got == nil || got.Months != 3 || got.Status != "pending" || !got.ExpireAt.Equal(exp) {
		t.Fatalf("get: %+v err %v", got, err)
	}

	when := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	ok, err := st.ConfirmPaymentRequest(ctx, id, when)
	if err != nil || !ok {
		t.Fatalf("first confirm: ok=%v err=%v", ok, err)
	}
	again, err := st.ConfirmPaymentRequest(ctx, id, when)
	if err != nil || again {
		t.Fatalf("second confirm should be no-op: again=%v err=%v", again, err)
	}

	got, _ = st.GetPaymentRequest(ctx, id)
	if got.Status != "confirmed" || got.ConfirmedAt == nil {
		t.Fatalf("expected confirmed: %+v", got)
	}
	if missing, err := st.GetPaymentRequest(ctx, 9999); err != nil || missing != nil {
		t.Fatalf("missing: %+v err %v", missing, err)
	}
}
