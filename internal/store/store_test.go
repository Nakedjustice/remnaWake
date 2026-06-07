package store

import (
	"context"
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

func TestTariffsCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.ListTariffs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty list: got %v err %v", got, err)
	}
	if err := st.UpsertTariff(ctx, 3, 450); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertTariff(ctx, 1, 150); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertTariff(ctx, 3, 500); err != nil { // update existing
		t.Fatalf("upsert update: %v", err)
	}

	list, err := st.ListTariffs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Months != 1 || list[1].Months != 3 || list[1].Price != 500 {
		t.Fatalf("unexpected list: %+v", list)
	}

	got, err := st.GetTariff(ctx, 3)
	if err != nil || got == nil || got.Price != 500 {
		t.Fatalf("get: %+v err %v", got, err)
	}
	if missing, err := st.GetTariff(ctx, 99); err != nil || missing != nil {
		t.Fatalf("get missing: %+v err %v", missing, err)
	}

	deleted, err := st.DeleteTariff(ctx, 3)
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if again, err := st.DeleteTariff(ctx, 3); err != nil || again {
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
