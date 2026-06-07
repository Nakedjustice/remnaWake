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
		RemnawaveID: 42, UUID: "u-42", Username: "bob", TelegramID: 555,
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
