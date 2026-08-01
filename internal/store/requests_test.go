package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestPaymentRequestRoundTripWithPayer(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "bob", TelegramID: 555,
		Months: 3, Price: 450, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Status: "pending", PayerTelegramID: 777, PayerUsername: "alice",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetPaymentRequest(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.PayerTelegramID != 777 || got.PayerUsername != "alice" {
		t.Fatalf("payer not persisted: %+v", got)
	}
}

func TestPaymentRequestProviderRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "bob", TelegramID: 555,
		Months: 1, Price: 150, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Status: "pending", Provider: "platega",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.GetPaymentRequest(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Provider != "platega" || got.ProviderTxnID != "" {
		t.Fatalf("provider fields = %q/%q, want platega/empty", got.Provider, got.ProviderTxnID)
	}

	if err := st.SetPaymentRequestProviderTxn(ctx, id, "tx-abc"); err != nil {
		t.Fatalf("set txn: %v", err)
	}

	byTxn, err := st.GetPaymentRequestByProviderTxn(ctx, "tx-abc")
	if err != nil || byTxn == nil {
		t.Fatalf("lookup by txn: %v %+v", err, byTxn)
	}
	if byTxn.ID != id || byTxn.ProviderTxnID != "tx-abc" {
		t.Fatalf("unexpected lookup result: %+v", byTxn)
	}

	missing, err := st.GetPaymentRequestByProviderTxn(ctx, "nope")
	if err != nil || missing != nil {
		t.Fatalf("expected nil for unknown txn, got %+v err %v", missing, err)
	}
}

func TestPaymentRequestDefaultsToP2P(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 1, Username: "a", TelegramID: 5,
		Months: 1, Price: 0, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Status: "pending",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := st.GetPaymentRequest(ctx, id)
	if got.Provider != "p2p" {
		t.Fatalf("Provider = %q, want default p2p", got.Provider)
	}
}

func TestEnsurePaymentRequestColumnsAddsToLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Legacy schema: no payer_* columns.
	_, err = db.Exec(`CREATE TABLE payment_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		remnawave_user_id INTEGER NOT NULL,
		uuid TEXT NOT NULL, username TEXT NOT NULL, telegram_id INTEGER NOT NULL,
		months INTEGER NOT NULL, price INTEGER NOT NULL, expire_at TEXT NOT NULL,
		status TEXT NOT NULL, created_at TEXT NOT NULL, confirmed_at TEXT)`)
	if err != nil {
		t.Fatalf("legacy create: %v", err)
	}

	if err := ensurePaymentRequestColumns(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Calling it again must be a no-op (idempotent).
	if err := ensurePaymentRequestColumns(db); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}

	cols := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(payment_requests)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	if !cols["payer_telegram_id"] || !cols["payer_username"] {
		t.Fatalf("columns not added: %v", cols)
	}
	_ = db.Close()
}

func TestListPaymentRequestsByStatus(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	mk := func(username string) int64 {
		id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
			RemnawaveID: 42, Username: username, TelegramID: 999,
			Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return id
	}

	first := mk("alice")
	second := mk("bob")
	third := mk("carol")

	when := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if ok, err := st.ConfirmPaymentRequest(ctx, second, when); err != nil || !ok {
		t.Fatalf("confirm: %v %v", ok, err)
	}

	pending, err := st.ListPaymentRequestsByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 2 || pending[0].ID != first || pending[1].ID != third {
		t.Fatalf("unexpected pending list: %+v", pending)
	}
	if pending[0].Username != "alice" || !pending[0].ExpireAt.Equal(exp) || pending[0].Months != 3 {
		t.Fatalf("unexpected fields: %+v", pending[0])
	}

	confirmed, err := st.ListPaymentRequestsByStatus(ctx, "confirmed")
	if err != nil || len(confirmed) != 1 || confirmed[0].ID != second {
		t.Fatalf("unexpected confirmed list: %+v err %v", confirmed, err)
	}
}

func TestLatestConfirmedPaymentRequestByUser(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	first, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "alice", TelegramID: 999,
		Months: 1, Price: 100, ExpireAt: exp, Plan: "basic", Status: "pending",
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	pending, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "alice", TelegramID: 999,
		Months: 1, Price: 400, ExpireAt: exp, Plan: PlanStandard, Status: "pending",
	})
	if err != nil || pending == 0 {
		t.Fatalf("create pending: id=%d err=%v", pending, err)
	}
	other, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 99, Username: "bob", TelegramID: 555,
		Months: 1, Price: 900, ExpireAt: exp, Plan: "vip", Status: "pending",
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	latest, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "alice", TelegramID: 999,
		Months: 3, Price: 300, ExpireAt: exp, Plan: "basic", Status: "pending",
	})
	if err != nil {
		t.Fatalf("create latest: %v", err)
	}

	if ok, err := st.ConfirmPaymentRequest(ctx, first, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil || !ok {
		t.Fatalf("confirm first: ok=%v err=%v", ok, err)
	}
	if ok, err := st.ConfirmPaymentRequest(ctx, other, time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)); err != nil || !ok {
		t.Fatalf("confirm other: ok=%v err=%v", ok, err)
	}
	if ok, err := st.ConfirmPaymentRequest(ctx, latest, time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)); err != nil || !ok {
		t.Fatalf("confirm latest: ok=%v err=%v", ok, err)
	}

	got, err := st.LatestConfirmedPaymentRequestByUser(ctx, 42)
	if err != nil || got == nil {
		t.Fatalf("latest: %v %+v", err, got)
	}
	if got.ID != latest || got.Plan != "basic" || got.Price != 300 || got.Months != 3 {
		t.Fatalf("unexpected latest request: %+v", got)
	}
	if missing, err := st.LatestConfirmedPaymentRequestByUser(ctx, 12345); err != nil || missing != nil {
		t.Fatalf("missing latest: %+v err=%v", missing, err)
	}
}

func TestRejectPaymentRequest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	id, err := st.CreatePaymentRequest(ctx, PaymentRequest{
		RemnawaveID: 42, Username: "alice", TelegramID: 999,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	when := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	ok, err := st.RejectPaymentRequest(ctx, id, when)
	if err != nil || !ok {
		t.Fatalf("first reject: ok=%v err=%v", ok, err)
	}
	again, err := st.RejectPaymentRequest(ctx, id, when)
	if err != nil || again {
		t.Fatalf("second reject should be no-op: again=%v err=%v", again, err)
	}

	got, _ := st.GetPaymentRequest(ctx, id)
	if got.Status != "rejected" || got.ConfirmedAt == nil {
		t.Fatalf("expected rejected with resolved-at: %+v", got)
	}

	// A rejected request can no longer be confirmed.
	if ok, err := st.ConfirmPaymentRequest(ctx, id, when); err != nil || ok {
		t.Fatalf("confirm after reject must fail: ok=%v err=%v", ok, err)
	}
}
