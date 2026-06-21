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
  payer_username    TEXT NOT NULL DEFAULT '',
  screenshot_file_id TEXT NOT NULL DEFAULT '',
  screenshot_is_document INTEGER NOT NULL DEFAULT 0,
  provider           TEXT NOT NULL DEFAULT 'p2p',
  provider_txn_id    TEXT NOT NULL DEFAULT ''
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
CREATE TABLE IF NOT EXISTS gift_codes (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  code                 TEXT NOT NULL UNIQUE,
  buyer_telegram_id    INTEGER NOT NULL,
  buyer_username       TEXT NOT NULL DEFAULT '',
  months               INTEGER NOT NULL,
  price                INTEGER NOT NULL DEFAULT 0,
  status               TEXT NOT NULL,
  redeemer_telegram_id INTEGER NOT NULL DEFAULT 0,
  redeemed_username    TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL,
  issued_at            TEXT,
  resolved_at          TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sent_notifications (
  remnawave_user_id INTEGER NOT NULL,
  kind              TEXT NOT NULL,
  milestone         INTEGER NOT NULL,
  expire_at         TEXT NOT NULL,
  sent_at           TEXT NOT NULL,
  PRIMARY KEY (remnawave_user_id, kind, milestone, expire_at)
);
CREATE TABLE IF NOT EXISTS trial_claims (
  telegram_id INTEGER PRIMARY KEY,
  username    TEXT NOT NULL,
  claimed_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notification_prefs (
  telegram_id   INTEGER PRIMARY KEY,
  expiry_muted  INTEGER NOT NULL DEFAULT 0,
  winback_muted INTEGER NOT NULL DEFAULT 0,
  updated_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS support_messages (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  user_telegram_id   INTEGER NOT NULL,
  user_username      TEXT NOT NULL DEFAULT '',
  from_admin         INTEGER NOT NULL,
  author_telegram_id INTEGER NOT NULL DEFAULT 0,
  text               TEXT NOT NULL,
  created_at         TEXT NOT NULL,
  read_by_user       INTEGER NOT NULL DEFAULT 0,
  read_by_admin      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_support_user ON support_messages(user_telegram_id);
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
	if !existing["screenshot_file_id"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN screenshot_file_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !existing["screenshot_is_document"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN screenshot_is_document INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !existing["provider"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN provider TEXT NOT NULL DEFAULT 'p2p'`); err != nil {
			return err
		}
	}
	if !existing["provider_txn_id"] {
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN provider_txn_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}
