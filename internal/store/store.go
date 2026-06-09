package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS tariffs (
  months     INTEGER PRIMARY KEY,
  price      INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notified_users (
  remnawave_user_id INTEGER PRIMARY KEY,
  uuid              TEXT NOT NULL,
  username          TEXT NOT NULL,
  telegram_id       INTEGER NOT NULL,
  expire_at         TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS payment_requests (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  remnawave_user_id INTEGER NOT NULL,
  uuid              TEXT NOT NULL,
  username          TEXT NOT NULL,
  telegram_id       INTEGER NOT NULL,
  months            INTEGER NOT NULL,
  price             INTEGER NOT NULL,
  expire_at         TEXT NOT NULL,
  status            TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  confirmed_at      TEXT,
  payer_telegram_id INTEGER NOT NULL DEFAULT 0,
  payer_username    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS invite_requests (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  inviter_telegram_id INTEGER NOT NULL,
  inviter_username    TEXT NOT NULL DEFAULT '',
  new_username        TEXT NOT NULL,
  months              INTEGER NOT NULL DEFAULT 1,
  price               INTEGER NOT NULL DEFAULT 0,
  status              TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  resolved_at         TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`

// New opens (creating if needed) the SQLite database at path and applies migrations.
func New(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := ensurePaymentRequestColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate payer columns: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// ensurePaymentRequestColumns adds the payer_* columns to payment_requests when
// an older database created the table without them. Idempotent.
func ensurePaymentRequestColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(payment_requests)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !existing["payer_telegram_id"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN payer_telegram_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !existing["payer_username"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN payer_username TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}
