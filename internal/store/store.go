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
  confirmed_at      TEXT
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
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }
