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
  plan       TEXT NOT NULL DEFAULT 'standard',
  months     INTEGER NOT NULL,
  price      INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (plan, months)
);
CREATE TABLE IF NOT EXISTS traffic_extension_options (
  traffic_gb INTEGER PRIMARY KEY,
  price      INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plans (
  code         TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  tag          TEXT NOT NULL DEFAULT '',
  squad_uuid   TEXT NOT NULL DEFAULT '',
  traffic_gb   INTEGER NOT NULL DEFAULT 0,
  device_limit INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
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
  provider_txn_id    TEXT NOT NULL DEFAULT '',
  plan               TEXT NOT NULL DEFAULT 'standard',
  kind               TEXT NOT NULL DEFAULT 'subscription',
  traffic_gb         INTEGER NOT NULL DEFAULT 0,
  base_traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
  extra_traffic_bytes INTEGER NOT NULL DEFAULT 0,
  extension_expires_at TEXT,
  extension_restored_at TEXT
);
CREATE TABLE IF NOT EXISTS invite_requests (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  inviter_telegram_id INTEGER NOT NULL,
  inviter_username    TEXT NOT NULL DEFAULT '',
  new_username        TEXT NOT NULL,
  months              INTEGER NOT NULL DEFAULT 1,
  price               INTEGER NOT NULL DEFAULT 0,
  plan                TEXT NOT NULL DEFAULT 'standard',
  status              TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  resolved_at         TEXT
);
CREATE TABLE IF NOT EXISTS trial_requests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  telegram_id INTEGER NOT NULL,
  username    TEXT NOT NULL,
  status      TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  resolved_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_trial_requests_status ON trial_requests(status);
CREATE TABLE IF NOT EXISTS gift_codes (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  code                 TEXT NOT NULL UNIQUE,
  buyer_telegram_id    INTEGER NOT NULL,
  buyer_username       TEXT NOT NULL DEFAULT '',
  months               INTEGER NOT NULL,
  price                INTEGER NOT NULL DEFAULT 0,
  plan                 TEXT NOT NULL DEFAULT 'standard',
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
  read_by_admin      INTEGER NOT NULL DEFAULT 0,
  ticket_id          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_support_user ON support_messages(user_telegram_id);
CREATE TABLE IF NOT EXISTS support_tickets (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  user_telegram_id INTEGER NOT NULL,
  user_username    TEXT NOT NULL DEFAULT '',
  subject          TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL DEFAULT 'open',
  created_at       TEXT NOT NULL,
  closed_at        TEXT,
  closed_by        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_support_tickets_user ON support_tickets(user_telegram_id, status);
CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets(status);
CREATE TABLE IF NOT EXISTS referrals (
  referred_telegram_id INTEGER PRIMARY KEY,
  referrer_telegram_id INTEGER NOT NULL,
  status               TEXT NOT NULL DEFAULT 'pending',
  created_at           TEXT NOT NULL,
  credited_at          TEXT
);
CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals(referrer_telegram_id);
CREATE TABLE IF NOT EXISTS infra_servers (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL,
  hoster         TEXT NOT NULL DEFAULT '',
  country        TEXT NOT NULL DEFAULT '',
  cpu_cores      INTEGER NOT NULL DEFAULT 0,
  ram_mb         INTEGER NOT NULL DEFAULT 0,
  price          INTEGER NOT NULL DEFAULT 0,
  currency       TEXT NOT NULL DEFAULT 'USD',
  period_months  INTEGER NOT NULL DEFAULT 1,
  next_due_at    TEXT,
  reminded_stage INTEGER NOT NULL DEFAULT 0,
  notes          TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS fx_rates (
  currency        TEXT PRIMARY KEY,
  auto_rate       REAL,
  auto_updated_at TEXT,
  manual_rate     REAL,
  updated_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS proxy_notif_muted (
  proxy_key  TEXT PRIMARY KEY,
  muted      INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS registration_guards (
  telegram_id     INTEGER PRIMARY KEY,
  username        TEXT NOT NULL DEFAULT '',
  first_name      TEXT NOT NULL DEFAULT '',
  last_name       TEXT NOT NULL DEFAULT '',
  matched_pattern TEXT NOT NULL DEFAULT '',
  blocked         INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  unblocked_at    TEXT
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
		return nil, fmt.Errorf("migrate payment request columns: %w", err)
	}
	if err := ensureTariffPlanColumn(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate tariffs plan column: %w", err)
	}
	if err := ensurePlanColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate plan columns: %w", err)
	}
	if err := ensureTrafficExtensionColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate traffic extension columns: %w", err)
	}
	if err := ensurePaymentRequestIndexes(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate payment request indexes: %w", err)
	}
	if err := ensureSupportTicketSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate support tickets: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// tableColumns returns the set of column names of table.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		existing[name] = true
	}
	return existing, rows.Err()
}

// ensurePaymentRequestColumns adds the payer_* columns to payment_requests when
// an older database created the table without them. Idempotent.
func ensurePaymentRequestColumns(db *sql.DB) error {
	existing, err := tableColumns(db, "payment_requests")
	if err != nil {
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

// ensureTariffPlanColumn rebuilds the tariffs table with the composite
// (plan, months) primary key when an older database still has months as the
// sole key. SQLite cannot alter a primary key in place, so the table is
// copied; existing rows become the 'standard' plan. Idempotent.
func ensureTariffPlanColumn(db *sql.DB) error {
	existing, err := tableColumns(db, "tariffs")
	if err != nil {
		return err
	}
	if existing["plan"] {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		CREATE TABLE tariffs_new (
		  plan       TEXT NOT NULL DEFAULT 'standard',
		  months     INTEGER NOT NULL,
		  price      INTEGER NOT NULL,
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL,
		  PRIMARY KEY (plan, months)
		)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO tariffs_new (plan, months, price, created_at, updated_at)
		SELECT 'standard', months, price, created_at, updated_at FROM tariffs
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE tariffs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE tariffs_new RENAME TO tariffs`); err != nil {
		return err
	}
	return tx.Commit()
}

// ensurePlanColumns adds the plan column to the request tables when an older
// database created them without it. Idempotent.
func ensurePlanColumns(db *sql.DB) error {
	for _, table := range []string{"payment_requests", "invite_requests", "gift_codes"} {
		existing, err := tableColumns(db, table)
		if err != nil {
			return err
		}
		if existing["plan"] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN plan TEXT NOT NULL DEFAULT 'standard'`); err != nil {
			return err
		}
	}
	return nil
}

func ensureTrafficExtensionColumns(db *sql.DB) error {
	existing, err := tableColumns(db, "payment_requests")
	if err != nil {
		return err
	}
	cols := []struct {
		name string
		def  string
	}{
		{"kind", "TEXT NOT NULL DEFAULT 'subscription'"},
		{"traffic_gb", "INTEGER NOT NULL DEFAULT 0"},
		{"base_traffic_limit_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"extra_traffic_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"extension_expires_at", "TEXT"},
		{"extension_restored_at", "TEXT"},
	}
	for _, c := range cols {
		if existing[c.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE payment_requests ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
			return err
		}
	}
	return nil
}

// ensureSupportTicketSchema adds the ticket_id column to support_messages when
// an older database created the table without it, then adopts any pre-ticket
// rows into one open legacy ticket per user. Idempotent: after the first run no
// ticket_id = 0 rows remain, so re-running changes nothing. Nothing is deleted.
func ensureSupportTicketSchema(db *sql.DB) error {
	existing, err := tableColumns(db, "support_messages")
	if err != nil {
		return err
	}
	if !existing["ticket_id"] {
		if _, err := db.Exec(`ALTER TABLE support_messages ADD COLUMN ticket_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// The index lives here, not in the schema const: on a legacy database the
	// table exists without ticket_id until the ALTER above runs, so a schema
	// const index on that column would fail store.New.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_support_messages_ticket ON support_messages(ticket_id)`); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT o.user_telegram_id,
		       COALESCE((SELECT user_username FROM support_messages
		                 WHERE user_telegram_id = o.user_telegram_id AND ticket_id = 0 AND user_username != ''
		                 ORDER BY id DESC LIMIT 1), ''),
		       MIN(o.created_at)
		FROM support_messages o
		WHERE o.ticket_id = 0
		GROUP BY o.user_telegram_id
	`)
	if err != nil {
		return err
	}
	type orphan struct {
		user      int64
		username  string
		createdAt string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.user, &o.username, &o.createdAt); err != nil {
			rows.Close()
			return err
		}
		orphans = append(orphans, o)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(orphans) == 0 {
		return tx.Commit()
	}
	for _, o := range orphans {
		res, err := tx.Exec(`
			INSERT INTO support_tickets (user_telegram_id, user_username, subject, status, created_at)
			VALUES (?, ?, '', 'open', ?)`, o.user, o.username, o.createdAt)
		if err != nil {
			return err
		}
		ticketID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE support_messages SET ticket_id = ? WHERE user_telegram_id = ? AND ticket_id = 0`, ticketID, o.user); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ensurePaymentRequestIndexes(db *sql.DB) error {
	// The two uuid-keyed indexes are dropped rather than kept: Remnawave v3
	// removed the panel uuid, so the column is now written empty and an index
	// over it would only cost write throughput. Dropping an index is reversible
	// — a rollback to a v2 build recreates them from its own migration.
	_, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_payment_requests_created_at ON payment_requests(created_at);
		CREATE INDEX IF NOT EXISTS idx_payment_requests_status_resolved ON payment_requests(status, confirmed_at);
		CREATE INDEX IF NOT EXISTS idx_payment_requests_user_status_resolved ON payment_requests(remnawave_user_id, status, confirmed_at);
		CREATE INDEX IF NOT EXISTS idx_payment_requests_provider ON payment_requests(provider);
		CREATE INDEX IF NOT EXISTS idx_payment_requests_provider_txn ON payment_requests(provider_txn_id);
		CREATE INDEX IF NOT EXISTS idx_payment_requests_traffic_active_user ON payment_requests(remnawave_user_id, kind, status, extension_expires_at, extension_restored_at);
		DROP INDEX IF EXISTS idx_payment_requests_uuid_status_resolved;
		DROP INDEX IF EXISTS idx_payment_requests_traffic_active;
	`)
	return err
}
