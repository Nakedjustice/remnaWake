# Month-selection Tariffs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users renew for an admin-chosen number of months (each with an informational price) via a two-step «Я оплатил» flow, backed by SQLite-persisted tariffs and payment state, with a fallback to today's single-button / 1-month behavior when no tariffs are set.

**Architecture:** Add two packages — `internal/store` (pure-Go SQLite persistence) and `internal/payments` (domain logic: tariffs, request lifecycle, admin commands, Telegram keyboards). `internal/notify` sheds its payment code and delegates to `payments`; `main.go` wires the store/payments and routes callbacks + admin commands to `payments`.

**Tech Stack:** Go 1.22, `database/sql` + `modernc.org/sqlite` (pure-Go, keeps `CGO_ENABLED=0`), Telegram Bot API (long polling), Docker Compose (distroless nonroot + named volume).

**Spec:** `docs/superpowers/specs/2026-06-07-month-selection-tariffs-design.md`

> **Branch:** Execute on a feature branch (e.g. `feat/month-tariffs`), NOT on `main`. Set it up via superpowers:using-git-worktrees before Task 1.

---

## File Structure

**New:**
- `internal/store/store.go` — `Store`, `New`, `Close`, migrations, time helpers.
- `internal/store/tariffs.go` — `Tariff` type + CRUD.
- `internal/store/users.go` — `NotifiedUser` type + upsert/get.
- `internal/store/requests.go` — `PaymentRequest` type + create/get/confirm.
- `internal/store/store_test.go` — store tests.
- `internal/payments/payments.go` — `Service`, `New`, `BotSender`/`Extender` interfaces, `PaymentButton`, `RememberUser`.
- `internal/payments/callbacks.go` — `HandleCallback` (pay/pick/back/ok), keyboards, message formatting, callback-data helpers.
- `internal/payments/commands.go` — `HandleAdminCommand` (/tariffs, /settariff, /deltariff, /help).
- `internal/payments/payments_test.go` — payments tests with fakes.

**Modified:**
- `internal/config/config.go` — add `DBPath`, `Currency`.
- `internal/config/config_test.go` — defaults test.
- `internal/telegram/bot.go` — add `EditMessageReplyMarkup`.
- `internal/telegram/bot_test.go` — new, tests `EditMessageReplyMarkup`.
- `internal/notify/service.go` — remove payment code; delegate to `payments`.
- `main.go` — create store + payments; route callbacks/commands.
- `go.mod` / `go.sum` — add `modernc.org/sqlite`.
- `Dockerfile`, `docker-compose.yml`, `.env.example`, `install.sh`, `README.md`, `README.ru.md`.

**Type/string conventions (used across tasks):**
- Time columns stored as `t.UTC().Format(time.RFC3339)`.
- Request status: `"pending"` | `"confirmed"`.
- Callback prefixes: `pay:`, `pick:`, `back:`, `ok:`.
- Month label: `fmt.Sprintf("%d мес.", n)`. Price label: `fmt.Sprintf("%d%s", price, currency)`.

---

## Task 1: Config — DB path and currency

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestLoadDefaultsDBPathAndCurrency(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("DB_PATH", "")
	t.Setenv("CURRENCY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "/data/bot.db" {
		t.Fatalf("DBPath = %q, want /data/bot.db", cfg.DBPath)
	}
	if cfg.Currency != "₽" {
		t.Fatalf("Currency = %q, want ₽", cfg.Currency)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadDefaultsDBPathAndCurrency`
Expected: FAIL — `cfg.DBPath` / `cfg.Currency` undefined.

- [ ] **Step 3: Add the fields and loading**

In `internal/config/config.go`, add to the `Config` struct (after `RunOnStart bool`):

```go
	DBPath     string
	Currency   string
```

In `Load()`, inside the `cfg := &Config{...}` literal, add after `RunOnStart: ...,`:

```go
		DBPath:     getenv("DB_PATH", "/data/bot.db"),
		Currency:   getenv("CURRENCY", "₽"),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add DB_PATH and CURRENCY settings"
```

---

## Task 2: Add SQLite dependency and store skeleton

**Files:**
- Create: `internal/store/store.go`
- Modify: `go.mod`, `go.sum`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@latest`
Expected: `go.mod` gains `require modernc.org/sqlite vX.Y.Z`.

- [ ] **Step 2: Write the failing test**

Create `internal/store/store_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
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
	// Querying each table must succeed if migrations ran.
	for _, table := range []string{"tariffs", "notified_users", "payment_requests"} {
		if _, err := st.db.Exec("SELECT 1 FROM " + table + " WHERE 1=0"); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 4: Implement the store skeleton**

Create `internal/store/store.go`:

```go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/store/
git commit -m "feat(store): add SQLite store with schema migrations"
```

---

## Task 3: Store — tariffs CRUD

**Files:**
- Create: `internal/store/tariffs.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

First add `"context"` to the import block at the top of `internal/store/store_test.go`. Then append:

```go
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
```

(If `context` is already imported in the test file, do not duplicate the import.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestTariffsCRUD`
Expected: FAIL — `ListTariffs` undefined.

- [ ] **Step 3: Implement tariffs**

Create `internal/store/tariffs.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Tariff struct {
	Months    int
	Price     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) UpsertTariff(ctx context.Context, months, price int) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tariffs (months, price, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(months) DO UPDATE SET price = excluded.price, updated_at = excluded.updated_at
	`, months, price, now, now)
	return err
}

func (s *Store) ListTariffs(ctx context.Context) ([]Tariff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT months, price, created_at, updated_at FROM tariffs ORDER BY months ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Tariff
	for rows.Next() {
		var (
			t              Tariff
			created, upd   string
		)
		if err := rows.Scan(&t.Months, &t.Price, &created, &upd); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = parseTime(created)
		t.UpdatedAt, _ = parseTime(upd)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTariff(ctx context.Context, months int) (*Tariff, error) {
	var (
		t            Tariff
		created, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT months, price, created_at, updated_at FROM tariffs WHERE months = ?
	`, months).Scan(&t.Months, &t.Price, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = parseTime(created)
	t.UpdatedAt, _ = parseTime(upd)
	return &t, nil
}

func (s *Store) DeleteTariff(ctx context.Context, months int) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tariffs WHERE months = ?`, months)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): tariff CRUD"
```

---

## Task 4: Store — notified users

**Files:**
- Create: `internal/store/users.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Ensure `"time"` is in the import block at the top of `internal/store/store_test.go` (added now if not already present). Then append:

```go
func TestNotifiedUsersUpsert(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	u := NotifiedUser{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 999, ExpireAt: exp}
	if err := st.UpsertNotifiedUser(ctx, u); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// update
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestNotifiedUsersUpsert`
Expected: FAIL — `NotifiedUser` undefined.

- [ ] **Step 3: Implement users**

Create `internal/store/users.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type NotifiedUser struct {
	RemnawaveID int64
	UUID        string
	Username    string
	TelegramID  int64
	ExpireAt    time.Time
	UpdatedAt   time.Time
}

func (s *Store) UpsertNotifiedUser(ctx context.Context, u NotifiedUser) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notified_users (remnawave_user_id, uuid, username, telegram_id, expire_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(remnawave_user_id) DO UPDATE SET
			uuid = excluded.uuid,
			username = excluded.username,
			telegram_id = excluded.telegram_id,
			expire_at = excluded.expire_at,
			updated_at = excluded.updated_at
	`, u.RemnawaveID, u.UUID, u.Username, u.TelegramID, formatTime(u.ExpireAt), now)
	return err
}

func (s *Store) GetNotifiedUser(ctx context.Context, remnawaveID int64) (*NotifiedUser, error) {
	var (
		u            NotifiedUser
		exp, upd     string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT remnawave_user_id, uuid, username, telegram_id, expire_at, updated_at
		FROM notified_users WHERE remnawave_user_id = ?
	`, remnawaveID).Scan(&u.RemnawaveID, &u.UUID, &u.Username, &u.TelegramID, &exp, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.ExpireAt, _ = parseTime(exp)
	u.UpdatedAt, _ = parseTime(upd)
	return &u, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): notified-user snapshot persistence"
```

---

## Task 5: Store — payment requests + idempotent confirm

**Files:**
- Create: `internal/store/requests.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
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
	// idempotent: second confirm is a no-op
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPaymentRequestsLifecycle`
Expected: FAIL — `CreatePaymentRequest` undefined.

- [ ] **Step 3: Implement requests**

Create `internal/store/requests.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PaymentRequest struct {
	ID          int64
	RemnawaveID int64
	UUID        string
	Username    string
	TelegramID  int64
	Months      int
	Price       int
	ExpireAt    time.Time
	Status      string // "pending" | "confirmed"
	CreatedAt   time.Time
	ConfirmedAt *time.Time
}

func (s *Store) CreatePaymentRequest(ctx context.Context, r PaymentRequest) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.UUID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetPaymentRequest(ctx context.Context, id int64) (*PaymentRequest, error) {
	var (
		r           PaymentRequest
		exp, created string
		confirmed   sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at
		FROM payment_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ExpireAt, _ = parseTime(exp)
	r.CreatedAt, _ = parseTime(created)
	if confirmed.Valid {
		if ts, e := parseTime(confirmed.String); e == nil {
			r.ConfirmedAt = &ts
		}
	}
	return &r, nil
}

// ConfirmPaymentRequest transitions pending->confirmed exactly once.
// Returns true only on the transition; false if already confirmed or not found.
func (s *Store) ConfirmPaymentRequest(ctx context.Context, id int64, confirmedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests SET status = 'confirmed', confirmed_at = ?
		WHERE id = ? AND status = 'pending'
	`, formatTime(confirmedAt), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): payment-request lifecycle with idempotent confirm"
```

---

## Task 6: Telegram bot — EditMessageReplyMarkup

**Files:**
- Modify: `internal/telegram/bot.go`
- Test: `internal/telegram/bot_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/telegram/bot_test.go`:

```go
package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEditMessageReplyMarkupClearsKeyboard(t *testing.T) {
	var gotPath string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL // same-package test: point at the stub

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "x", CallbackData: "y"}}}}
	if err := b.EditMessageReplyMarkup(context.Background(), 555, 777, kb); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if gotPath != "/editMessageReplyMarkup" {
		t.Fatalf("path = %q", gotPath)
	}
	if body["chat_id"].(float64) != 555 || body["message_id"].(float64) != 777 {
		t.Fatalf("ids wrong: %v", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram/`
Expected: FAIL — `EditMessageReplyMarkup` undefined.

- [ ] **Step 3: Implement the method**

In `internal/telegram/bot.go`, add a request type near the other request structs:

```go
type editMessageReplyMarkupRequest struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}
```

And add the method (after `AnswerCallbackQuery`):

```go
func (b *Bot) EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, keyboard *InlineKeyboardMarkup) error {
	payload := editMessageReplyMarkupRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		ReplyMarkup: keyboard,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/editMessageReplyMarkup", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram edit reply markup: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram edit reply markup failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram edit reply markup not ok: %s", ar.Description)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/
git commit -m "feat(telegram): EditMessageReplyMarkup"
```

---

## Task 7: payments — service, interfaces, button, RememberUser

**Files:**
- Create: `internal/payments/payments.go`
- Test: `internal/payments/payments_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/payments/payments_test.go`:

```go
package payments

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// --- fakes ---

type sentMsg struct {
	ChatID   int64
	Text     string
	Keyboard *tg.InlineKeyboardMarkup
}
type editCall struct {
	ChatID, MessageID int64
	Keyboard          *tg.InlineKeyboardMarkup
}
type fakeBot struct {
	sent     []sentMsg
	answers  []string
	edits    []editCall
}

func (f *fakeBot) SendPlain(_ context.Context, chatID int64, text string) error {
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text})
	return nil
}
func (f *fakeBot) SendPlainWithKeyboard(_ context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) error {
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text, Keyboard: kb})
	return nil
}
func (f *fakeBot) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeBot) EditMessageReplyMarkup(_ context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error {
	f.edits = append(f.edits, editCall{ChatID: chatID, MessageID: messageID, Keyboard: kb})
	return nil
}

type fakeExtender struct {
	uuid    string
	expire  time.Time
	calls   int
}

func (f *fakeExtender) ExtendSubscriptionByUUID(_ context.Context, uuid string, newExpireAt time.Time) error {
	f.uuid = uuid
	f.expire = newExpireAt
	f.calls++
	return nil
}

func newTestService(t *testing.T) (*Service, *fakeBot, *fakeExtender, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	ext := &fakeExtender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, ext, 1000 /*adminID*/, "₽", false /*dryRun*/, logger)
	return svc, bot, ext, st
}

func TestPaymentButtonNilWithoutAdmin(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	svc := New(st, &fakeBot{}, &fakeExtender{}, 0, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.PaymentButton(42) != nil {
		t.Fatal("expected nil button when adminID == 0")
	}
}

func TestPaymentButtonHasPayCallback(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	kb := svc.PaymentButton(42)
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "pay:42" {
		t.Fatalf("unexpected button: %+v", kb)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/payments/`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 3: Implement the service core**

Create `internal/payments/payments.go`:

```go
package payments

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// BotSender is the subset of *telegram.Bot that payments needs.
type BotSender interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) error
	AnswerCallbackQuery(ctx context.Context, id, text string) error
	EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error
}

// Extender is the subset of *remnawave.Client that payments needs.
type Extender interface {
	ExtendSubscriptionByUUID(ctx context.Context, uuid string, newExpireAt time.Time) error
}

type Service struct {
	store    *store.Store
	bot      BotSender
	extender Extender
	adminID  int64
	currency string
	dryRun   bool
	logger   *slog.Logger
	now      func() time.Time
}

func New(st *store.Store, bot BotSender, ext Extender, adminID int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	return &Service{
		store:    st,
		bot:      bot,
		extender: ext,
		adminID:  adminID,
		currency: currency,
		dryRun:   dryRun,
		logger:   logger,
		now:      time.Now,
	}
}

// PaymentButton returns the single «Я оплатил» keyboard, or nil when the
// payment flow is disabled (no admin configured).
func (s *Service) PaymentButton(userID int64) *tg.InlineKeyboardMarkup {
	if s.adminID == 0 {
		return nil
	}
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Я оплатил", CallbackData: fmt.Sprintf("pay:%d", userID)}},
		},
	}
}

// RememberUser persists the user snapshot captured when a notification is sent.
func (s *Service) RememberUser(ctx context.Context, u store.NotifiedUser) error {
	return s.store.UpsertNotifiedUser(ctx, u)
}

func (s *Service) priceLabel(price int) string {
	return fmt.Sprintf("%d%s", price, s.currency)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/payments/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/payments/
git commit -m "feat(payments): service core, BotSender/Extender interfaces, payment button"
```

---

## Task 8: payments — admin commands

**Files:**
- Create: `internal/payments/commands.go`
- Test: `internal/payments/payments_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/payments/payments_test.go`:

```go
func msg(chatID int64, text string) *tg.Message {
	return &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}, Text: text}
}

func TestAdminCommandsIgnoreNonAdmin(t *testing.T) {
	svc, bot, _, _ := newTestService(t) // adminID == 1000
	if svc.HandleAdminCommand(context.Background(), msg(2222, "/settariff 3 450")) {
		t.Fatal("non-admin command should not be handled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("should not reply to non-admin: %+v", bot.sent)
	}
}

func TestAdminSetListDeleteTariff(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	if !svc.HandleAdminCommand(ctx, msg(1000, "/settariff 3 450")) {
		t.Fatal("settariff should be handled")
	}
	got, _ := st.GetTariff(ctx, 3)
	if got == nil || got.Price != 450 {
		t.Fatalf("tariff not stored: %+v", got)
	}

	if !svc.HandleAdminCommand(ctx, msg(1000, "/tariffs")) {
		t.Fatal("tariffs should be handled")
	}
	if !svc.HandleAdminCommand(ctx, msg(1000, "/deltariff 3")) {
		t.Fatal("deltariff should be handled")
	}
	if again, _ := st.GetTariff(ctx, 3); again != nil {
		t.Fatalf("tariff not deleted: %+v", again)
	}
	if len(bot.sent) < 3 {
		t.Fatalf("expected replies for each command: %+v", bot.sent)
	}
}

func TestAdminSetTariffValidation(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if !svc.HandleAdminCommand(context.Background(), msg(1000, "/settariff three lots")) {
		t.Fatal("should be handled (with a usage reply)")
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected a usage reply on invalid input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/payments/ -run TestAdmin`
Expected: FAIL — `HandleAdminCommand` undefined.

- [ ] **Step 3: Implement commands**

Create `internal/payments/commands.go`:

```go
package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const adminUsage = "Команды тарифов:\n" +
	"/tariffs — список тарифов\n" +
	"/settariff <месяцев> <цена> — добавить/обновить\n" +
	"/deltariff <месяцев> — удалить\n" +
	"/help — эта справка"

// HandleAdminCommand processes a tariff admin command. Returns true if the
// message was a recognized admin command (handled), false otherwise.
func (s *Service) HandleAdminCommand(ctx context.Context, m *tg.Message) bool {
	if m == nil || s.adminID == 0 || m.Chat.ID != s.adminID {
		return false
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/tariffs":
		s.cmdListTariffs(ctx)
		return true
	case "/help":
		_ = s.bot.SendPlain(ctx, s.adminID, adminUsage)
		return true
	case "/settariff":
		s.cmdSetTariff(ctx, fields)
		return true
	case "/deltariff":
		s.cmdDelTariff(ctx, fields)
		return true
	default:
		return false
	}
}

func (s *Service) cmdListTariffs(ctx context.Context) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка чтения тарифов.")
		return
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Тарифы не заданы. Добавьте: /settariff <месяцев> <цена>")
		return
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, s.adminID, strings.TrimRight(b.String(), "\n"))
}

func (s *Service) cmdSetTariff(ctx context.Context, fields []string) {
	if len(fields) != 3 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Использование: /settariff <месяцев> <цена>")
		return
	}
	months, err1 := strconv.Atoi(fields[1])
	price, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || months < 1 || price < 0 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Месяцев — целое ≥ 1, цена — целое ≥ 0. Пример: /settariff 3 450")
		return
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка сохранения тарифа.")
		return
	}
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
}

func (s *Service) cmdDelTariff(ctx context.Context, fields []string) {
	if len(fields) != 2 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Использование: /deltariff <месяцев>")
		return
	}
	months, err := strconv.Atoi(fields[1])
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, s.adminID, "Месяцев — целое ≥ 1. Пример: /deltariff 3")
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, s.adminID, "Ошибка удаления тарифа.")
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф на %d мес. не найден.", months))
		return
	}
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("Тариф на %d мес. удалён.", months))
}
```

Add the import of the telegram package to `commands.go` (used by `*tg.Message`): change the import block to:

```go
import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/payments/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/payments/
git commit -m "feat(payments): admin tariff commands"
```

---

## Task 9: payments — user flow callbacks (pay/pick/back) + fallback

**Files:**
- Create: `internal/payments/callbacks.go`
- Test: `internal/payments/payments_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/payments/payments_test.go`:

```go
func cbq(fromID int64, data string) *tg.CallbackQuery {
	return &tg.CallbackQuery{
		ID:      "cbid",
		From:    tg.User{ID: fromID},
		Message: &tg.Message{MessageID: 50, Chat: tg.Chat{ID: 777}},
		Data:    data,
	}
}

func rememberAlice(t *testing.T, st *store.Store) {
	t.Helper()
	err := st.UpsertNotifiedUser(context.Background(), store.NotifiedUser{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
}

func TestPayShowsTariffButtons(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 1, 150)
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	if len(bot.edits) != 1 {
		t.Fatalf("expected keyboard edit, got %+v", bot.edits)
	}
	kb := bot.edits[0].Keyboard
	// 2 tariff rows + 1 back row
	if kb == nil || len(kb.InlineKeyboard) != 3 {
		t.Fatalf("unexpected keyboard: %+v", kb)
	}
	if kb.InlineKeyboard[0][0].CallbackData != "pick:42:1" {
		t.Fatalf("first tariff cb: %q", kb.InlineKeyboard[0][0].CallbackData)
	}
	if kb.InlineKeyboard[2][0].CallbackData != "back:42" {
		t.Fatalf("back cb: %q", kb.InlineKeyboard[2][0].CallbackData)
	}
}

func TestPayFallbackNoTariffs(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	// fallback: a pending request is created and the admin is notified
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.Months != 1 || req.Status != "pending" {
		t.Fatalf("fallback request not created: %+v", req)
	}
	var adminGotConfirm bool
	for _, m := range bot.sent {
		if m.ChatID == 1000 && m.Keyboard != nil &&
			m.Keyboard.InlineKeyboard[0][0].CallbackData == "ok:1" {
			adminGotConfirm = true
		}
	}
	if !adminGotConfirm {
		t.Fatalf("admin not notified with confirm button: %+v", bot.sent)
	}
}

func TestPickCreatesRequestAndNotifiesAdmin(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3")) {
		t.Fatal("pick should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.Months != 3 || req.Price != 450 || req.UUID != "uuid-42" {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected keyboard cleared, got %+v", bot.edits)
	}
}

func TestBackRestoresPayButton(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if !svc.HandleCallback(context.Background(), cbq(777, "back:42")) {
		t.Fatal("back should be handled")
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard.InlineKeyboard[0][0].CallbackData != "pay:42" {
		t.Fatalf("expected pay button restored: %+v", bot.edits)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/payments/ -run 'TestPay|TestPick|TestBack'`
Expected: FAIL — `HandleCallback` undefined.

- [ ] **Step 3: Implement the callback dispatcher and user-flow handlers**

Create `internal/payments/callbacks.go`:

```go
package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// HandleCallback dispatches an inline-button callback. Returns true if handled.
func (s *Service) HandleCallback(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb == nil {
		return false
	}
	switch {
	case strings.HasPrefix(cb.Data, "pay:"):
		return s.handlePay(ctx, cb)
	case strings.HasPrefix(cb.Data, "pick:"):
		return s.handlePick(ctx, cb)
	case strings.HasPrefix(cb.Data, "back:"):
		return s.handleBack(ctx, cb)
	// NOTE: the "ok:" (admin confirm) case is added in Task 10.
	default:
		return false
	}
}

func (s *Service) handlePay(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "pay:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	if len(tariffs) == 0 {
		// Fallback: behave like the old single-button flow (1 month).
		s.createRequestAndNotify(ctx, cb, userID, 1, 0)
		return true
	}

	// Show tariff options on the same message.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.tariffKeyboard(userID, tariffs))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Выберите количество месяцев.")
	return true
}

func (s *Service) handlePick(ctx context.Context, cb *tg.CallbackQuery) bool {
	parts := strings.Split(strings.TrimPrefix(cb.Data, "pick:"), ":")
	if len(parts) != 2 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}

	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот тариф больше недоступен.")
		return true
	}

	s.createRequestAndNotify(ctx, cb, userID, months, tariff.Price)
	return true
}

func (s *Service) handleBack(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "back:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.PaymentButton(userID))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	return true
}

// createRequestAndNotify looks up the remembered user, writes a pending request,
// clears the user's keyboard, and DMs the admin a confirm button.
func (s *Service) createRequestAndNotify(ctx context.Context, cb *tg.CallbackQuery, userID int64, months, price int) {
	u, err := s.store.GetNotifiedUser(ctx, userID)
	if err != nil {
		s.logger.Error("get notified user failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return
	}
	if u == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось найти данные. Дождитесь следующего уведомления.")
		return
	}

	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending",
	})
	if err != nil {
		s.logger.Error("create payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return
	}

	// Clear the user's keyboard and acknowledge.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	// Notify the admin with details + confirm button.
	text := s.formatAdminRequest(u, months, price)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Подтвердить оплату", CallbackData: fmt.Sprintf("ok:%d", reqID)}},
		},
	}
	if err := s.bot.SendPlainWithKeyboard(ctx, s.adminID, text, kb); err != nil {
		s.logger.Error("notify admin failed", "err", err.Error())
	}
}

func (s *Service) tariffKeyboard(userID int64, tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("pick:%d:%d", userID, t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: "← Назад", CallbackData: fmt.Sprintf("back:%d", userID),
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) formatAdminRequest(u *store.NotifiedUser, months, price int) string {
	var b strings.Builder
	b.WriteString("💳 Заявка на оплату\n\n")
	b.WriteString("Клиент: " + u.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", u.RemnawaveID))
	b.WriteString("UUID: " + u.UUID + "\n")
	b.WriteString(fmt.Sprintf("Telegram ID: %d\n", u.TelegramID))
	b.WriteString("Подписка до: " + u.ExpireAt.Format("02.01.2006") + "\n")
	if price > 0 {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес. — %s", months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес.", months))
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/payments/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/payments/
git commit -m "feat(payments): two-step user flow with no-tariff fallback"
```

---

## Task 10: payments — admin confirm (extend + idempotent + dry-run)

**Files:**
- Modify: `internal/payments/callbacks.go` (add `handleConfirm`)
- Test: `internal/payments/payments_test.go`

- [ ] **Step 1: Write the failing test**

Add `"fmt"` to the import block at the top of `internal/payments/payments_test.go` (used by the new tests). Then append:

```go
func TestConfirmRejectsNonAdmin(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 3, Price: 450, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Status: "pending",
	})
	if !svc.HandleCallback(ctx, cbq(2222 /*not admin*/, fmt.Sprintf("ok:%d", id))) {
		t.Fatal("should be handled (rejected)")
	}
	if ext.calls != 0 {
		t.Fatal("must not extend for non-admin")
	}
}

func TestConfirmExtendsByChosenMonths(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	// Fix the clock so max(now, expiry) is deterministic.
	svc.now = func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) }
	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // future -> base is expiry
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})

	if !svc.HandleCallback(ctx, cbq(1000 /*admin*/, fmt.Sprintf("ok:%d", id))) {
		t.Fatal("confirm should be handled")
	}
	if ext.calls != 1 || ext.uuid != "uuid-42" {
		t.Fatalf("extend not called correctly: calls=%d uuid=%s", ext.calls, ext.uuid)
	}
	want := exp.AddDate(0, 3, 0)
	if !ext.expire.Equal(want) {
		t.Fatalf("new expiry = %s, want %s", ext.expire, want)
	}

	// idempotent: a second confirm does not extend again
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("ok:%d", id)))
	if ext.calls != 1 {
		t.Fatalf("second confirm must be a no-op, calls=%d", ext.calls)
	}
}

func TestConfirmExtendsFromNowWhenExpired(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // already expired
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 1, Price: 150, ExpireAt: past, Status: "pending",
	})
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("ok:%d", id)))
	want := now.AddDate(0, 1, 0)
	if !ext.expire.Equal(want) {
		t.Fatalf("new expiry = %s, want %s (from now)", ext.expire, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/payments/ -run TestConfirm`
Expected: FAIL — `handleConfirm` undefined (compile error).

- [ ] **Step 3: Add the `ok:` dispatch case, then implement handleConfirm**

First, in `internal/payments/callbacks.go`, add the confirm case to `HandleCallback`'s switch, just before `default:`:

```go
	case strings.HasPrefix(cb.Data, "ok:"):
		return s.handleConfirm(ctx, cb)
```

Then append the handler to the same file:

```go
func (s *Service) handleConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if s.adminID == 0 || cb.From.ID != s.adminID {
		s.logger.Warn("unauthorized confirm attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав для подтверждения оплаты.")
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("get payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if req == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if req.Status == "confirmed" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подписка уже была продлена.")
		return true
	}

	base := req.ExpireAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, req.Months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would extend", "uuid", req.UUID, "months", req.Months, "new_expire", newExpireAt.Format("2006-01-02"))
		_, _ = s.store.ConfirmPaymentRequest(ctx, reqID, s.now())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подписка продлена (dry-run).")
		return true
	}

	if err := s.extender.ExtendSubscriptionByUUID(ctx, req.UUID, newExpireAt); err != nil {
		s.logger.Error("extend subscription failed", "uuid", req.UUID, "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка продления подписки. Проверьте логи.")
		return true
	}

	if _, err := s.store.ConfirmPaymentRequest(ctx, reqID, s.now()); err != nil {
		s.logger.Error("mark confirmed failed", "err", err.Error())
	}

	// Remove the admin's confirm button to prevent re-taps.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Подписка продлена!")
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("✅ Подписка для %s продлена на %d мес. до %s",
		req.Username, req.Months, newExpireAt.Format("02.01.2006")))
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/payments/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/payments/
git commit -m "feat(payments): admin-authorized idempotent confirm + extend"
```

---

## Task 11: Wire payments into notify.Service

**Files:**
- Modify: `internal/notify/service.go`

This removes all payment code from `notify` and delegates the button + remembering to `payments`. `daysUntil` and the notification logic stay.

- [ ] **Step 1: Replace the Service struct, constructor, and notify block**

In `internal/notify/service.go`:

1. Update the import block to add `payments` and `store`, and drop the now-unused `strconv`/`strings`/`sync` once the payment code is removed:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)
```

2. Replace the `Service` struct and `paymentUser` type with:

```go
type Service struct {
	rw     *remnawave.Client
	tg     *tgbot.Bot
	pay    *payments.Service
	logger *slog.Logger
	dryRun bool
	now    func() time.Time
}
```

3. Replace `NewService` with:

```go
func NewService(rw *remnawave.Client, tg *tgbot.Bot, pay *payments.Service, logger *slog.Logger, dryRun bool) *Service {
	return &Service{
		rw:     rw,
		tg:     tg,
		pay:    pay,
		logger: logger,
		dryRun: dryRun,
		now:    time.Now,
	}
}
```

4. In `Run`, replace the per-user keyboard/remember block. Find the section that builds `text`, `keyboard`, and the `chatID`, and replace from the `text := fmt.Sprintf(...)` line through the send so it reads:

```go
		text := fmt.Sprintf("⏰ Подписка истекает через %d %s. Для продления оплатите подписку.",
			days, pluralDays(days))

		chatID := *u.TelegramID
		// Persist a snapshot so the «Я оплатил» flow works (even after a restart).
		if err := s.pay.RememberUser(ctx, store.NotifiedUser{
			RemnawaveID: u.ID,
			UUID:        u.UUID,
			Username:    u.Username,
			TelegramID:  chatID,
			ExpireAt:    u.ExpireAt,
		}); err != nil {
			s.logger.Error("remember user failed", "err", err.Error(), "user_id", u.ID)
		}
		keyboard := s.pay.PaymentButton(u.ID)

		logEntry := logger.With(
			"user_id", u.ID,
			"uuid", u.UUID,
			"username", u.Username,
			"chat_id", chatID,
			"expire_at", u.ExpireAt.Format(time.RFC3339),
			"days_left", days,
		)

		if s.dryRun {
			logEntry.Info("dry-run: would send", "text", text)
			notified++
			continue
		}

		if err := s.tg.SendWithKeyboard(ctx, chatID, text, keyboard); err != nil {
			logEntry.Error("telegram send failed", "err", err.Error())
			failed++
			continue
		}
		logEntry.Info("notification sent", "text", text)
		notified++
```

5. Delete every payment-only declaration now living in `service.go`: `triggerDays` stays; remove `paymentCallbackPrefix`, `confirmPaymentCallbackPrefix`, `HandleCallback`, `handleConfirmPayment`, `markConfirmed`, `handleUserPayment`, `paymentKeyboard`, `confirmPaymentKeyboard`, `rememberPaymentUser`, `lookupPaymentUser`, `paymentCallbackData`, `parsePaymentCallbackData`, `confirmPaymentCallbackData`, `parseConfirmPaymentCallbackData`, `formatPaymentNotification`, `formatTelegramUser`, and the `paidUsersMu`/`paidUsers`/`confirmedUUIDs` fields (already removed in step 2). Keep `shouldNotify`, `daysUntil`, `pluralDays`.

- [ ] **Step 2: Verify it compiles (callers will still break — that's Task 12)**

Run: `go build ./internal/notify/`
Expected: PASS (notify package compiles on its own).

- [ ] **Step 3: Run notify tests**

Run: `go test ./internal/notify/`
Expected: PASS — `TestDaysUntilNormalizesTimezones` still green (function unchanged).

- [ ] **Step 4: Commit**

```bash
git add internal/notify/
git commit -m "refactor(notify): delegate payment flow to payments package"
```

---

## Task 12: Wire store + payments into main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update construction and the poll loop**

In `main.go`:

1. Add imports `"github.com/Nakedjustice/remnaWake/internal/payments"` and `"github.com/Nakedjustice/remnaWake/internal/store"`.

2. After the remnawave client is created and before `bot := tgbot.NewBot(...)` stays, replace the service-construction block:

```go
	bot := tgbot.NewBot(cfg.Telegram.BotToken, cfg.Telegram.ParseMode, cfg.HTTP.Timeout)

	db, err := store.New(cfg.DBPath)
	if err != nil {
		logger.Error("store init failed", "err", err.Error(), "db_path", cfg.DBPath)
		os.Exit(1)
	}
	defer db.Close()

	pay := payments.New(db, bot, rwClient, cfg.Telegram.AdminID, cfg.Currency, cfg.DryRun, logger)
	svc := notify.NewService(rwClient, bot, pay, logger, cfg.DryRun)
```

(Remove the old `svc := notify.NewService(rwClient, bot, logger, cfg.DryRun, cfg.Telegram.AdminID)` line.)

3. Update the goroutine guard and `pollTelegramCallbacks` call to pass `pay` instead of `svc`:

```go
	if cfg.Telegram.AdminID != 0 {
		go pollTelegramCallbacks(rootCtx, bot, pay, logger)
	}
```

4. Replace `pollTelegramCallbacks`'s signature and body delegation:

```go
func pollTelegramCallbacks(ctx context.Context, bot *tgbot.Bot, pay *payments.Service, logger *slog.Logger) {
```

Inside the update loop, replace the callback and message handling with:

```go
			if u.CallbackQuery != nil {
				if pay.HandleCallback(ctx, u.CallbackQuery) {
					continue
				}
				logger.Debug("ignored telegram callback", "data", u.CallbackQuery.Data)
				continue
			}
			if u.Message != nil && u.Message.Text != "" {
				if strings.TrimSpace(u.Message.Text) == "/start" {
					logger.Info("received /start command", "chat_id", u.Message.Chat.ID)
					if err := bot.SendWelcome(ctx, u.Message.Chat.ID); err != nil {
						logger.Error("send welcome message failed", "err", err.Error(), "chat_id", u.Message.Chat.ID)
					}
					continue
				}
				if pay.HandleAdminCommand(ctx, u.Message) {
					continue
				}
			}
```

(Keep the existing `strings` import; it is still used for `TrimSpace`.)

- [ ] **Step 2: Build the whole module**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS across `config`, `store`, `payments`, `telegram`, `notify`, `remnawave`.

- [ ] **Step 4: Vet**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat(main): wire SQLite store and payments service"
```

---

## Task 13: Docker, compose, env, installer, docs

**Files:**
- Modify: `Dockerfile`, `docker-compose.yml`, `.env.example`, `install.sh`, `README.md`, `README.ru.md`

- [ ] **Step 1: Dockerfile — create a nonroot-owned /data**

In `Dockerfile`, in the build stage after `RUN go build ...` add:

```dockerfile
RUN mkdir -p /data
```

In the runtime stage, after `COPY --from=build /out/bot /app/bot` add:

```dockerfile
COPY --from=build --chown=65532:65532 /data /data
```

- [ ] **Step 2: docker-compose.yml — named volume + DB path**

Replace `docker-compose.yml` with:

```yaml
services:
  bot:
    build: .
    image: remnawave-notify-bot:latest
    container_name: remnawave-notify-bot
    restart: unless-stopped
    env_file:
      - ./.env
    environment:
      DB_PATH: /data/bot.db
    volumes:
      - botdata:/data
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

volumes:
  botdata:
```

- [ ] **Step 3: .env.example — add settings**

Append to `.env.example`:

```bash
DB_PATH=/data/bot.db
CURRENCY=₽
```

- [ ] **Step 4: install.sh — prompt for currency in the advanced section**

In `install.sh`, inside the advanced-options `if` block, add after the `RUN_ON_START` prompt:

```bash
  ask CURRENCY            "Currency label shown next to prices"           "₽"     v_nonempty
```

And add `CURRENCY` to the generated `.env` heredoc (after the `RUN_ON_START=...` line):

```bash
CURRENCY=$CURRENCY
```

Also set its default before the advanced block, next to the other defaults:

```bash
CURRENCY="₽"
```

(Leave `DB_PATH` out of the prompt — it is fixed to `/data/bot.db` by docker-compose; the config default covers local runs.)

- [ ] **Step 5: README — document tariffs**

Add to both `README.md` and `README.ru.md`, in the payment-flow section, a short note that the admin manages tariffs via `/tariffs`, `/settariff <months> <price>`, `/deltariff <months>`, `/help`, that prices are informational, and that data persists in SQLite at `DB_PATH` (default `/data/bot.db`, stored in the `botdata` Docker volume). Add `DB_PATH` and `CURRENCY` rows to the config tables.

English config-table rows:

```markdown
| `DB_PATH`              | no       | `/data/bot.db`   | SQLite database file path                                    |
| `CURRENCY`             | no       | `₽`              | Currency label shown next to tariff prices                   |
```

Russian config-table rows:

```markdown
| `DB_PATH`                | нет         | `/data/bot.db`    | Путь к файлу базы данных SQLite                        |
| `CURRENCY`               | нет         | `₽`               | Обозначение валюты рядом с ценами тарифов              |
```

- [ ] **Step 6: Validate compose and build**

Run: `docker compose config`
Expected: prints the merged config with the `botdata` volume, no errors.

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile docker-compose.yml .env.example install.sh README.md README.ru.md
git commit -m "feat(deploy): persist SQLite via named volume; document tariffs"
```

---

## Task 14: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS, no vet output.

- [ ] **Step 2: Confirm no stale references**

Run: `grep -rn "paidUsers\|confirmedUUIDs\|handleUserPayment" internal/notify/`
Expected: no matches (all payment code moved out of notify).

- [ ] **Step 3: Confirm gofmt cleanliness**

Run: `gofmt -l internal/ main.go`
Expected: no output (all files formatted).

- [ ] **Step 4: Commit any formatting fixes if needed**

```bash
gofmt -w internal/ main.go
git add -A
git commit -m "chore: gofmt" || true
```

---

## Definition of Done

- `go build ./...`, `go vet ./...`, `go test ./...` all pass.
- Admin can `/settariff`, `/tariffs`, `/deltariff`, `/help`; tariffs persist across restart.
- User taps «Я оплатил» → sees month/price buttons (or fallback 1-month request when no tariffs) → picks → admin gets a confirm button → confirm extends by the chosen months from `max(now, expiry)`, once (idempotent), admin-only.
- SQLite DB persists in the `botdata` Docker volume at `/data/bot.db`.
- READMEs and `.env.example` document `DB_PATH`, `CURRENCY`, and the tariff commands.
```
