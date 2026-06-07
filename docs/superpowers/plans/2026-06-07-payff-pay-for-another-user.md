# Pay for another user (`/payff`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an existing subscriber gift a renewal to another user (by Remnawave username or Telegram ID) through a `/payff` conversation, reusing the tariff catalogue and the admin pending→confirmed confirm flow.

**Architecture:** Two new read-only Remnawave lookups (`by-username`, `by-telegram-id`) feed a `Finder` interface (payments-local `Subscriber` type, adapter wired in `main.go`). The `payments.Service` gains an in-memory, mutex-guarded conversation state map (`map[chatID]*giftState`) with a 10-minute TTL. New handlers (`StartGiftFlow`, `HandleText`, `gpick:`/`gcancel` callbacks) drive a two-step flow that creates a `payment_requests` row carrying both target and payer fields; the existing `ok:{reqID}` admin-confirm handler extends the target. `payment_requests` gains nullable payer columns via an additive, idempotent migration.

**Tech Stack:** Go 1.25, `database/sql` + `modernc.org/sqlite`, Telegram Bot API (long polling, inline keyboards), `net/http/httptest` for tests.

**Reference spec:** `docs/superpowers/specs/2026-06-07-payff-pay-for-another-user-design.md`

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/remnawave/types.go` | Lookup response envelopes | Modify: add `userResponse`, `usersByTgResponse` |
| `internal/remnawave/client.go` | Single-user lookups | Modify: add `GetUserByUsername`, `GetUserByTelegramID` |
| `internal/remnawave/client_test.go` | Lookup tests | Modify: add 4 tests |
| `internal/store/store.go` | Schema + migration | Modify: add payer columns + `ensurePaymentRequestColumns` |
| `internal/store/requests.go` | Payment-request CRUD | Modify: `PaymentRequest` fields + INSERT/SELECT |
| `internal/store/requests_test.go` | Round-trip + migration tests | Create |
| `internal/payments/payments.go` | `Finder`, `Subscriber`, state fields, `New` sig | Modify |
| `internal/payments/gift.go` | Gift conversation handlers | Create |
| `internal/payments/callbacks.go` | Callback dispatch | Modify: add `gpick:`/`gcancel` cases |
| `internal/payments/gift_test.go` | Gift flow tests | Create |
| `internal/payments/payments_test.go` | Update `New` call sites + add `fakeFinder` | Modify |
| `main.go` | Finder adapter + message routing | Modify |
| `README.md`, `README.ru.md` | Document `/payff` | Modify |

---

## Task 1: Remnawave single-user lookups

**Files:**
- Modify: `internal/remnawave/types.go`
- Modify: `internal/remnawave/client.go`
- Test: `internal/remnawave/client_test.go`

- [ ] **Step 1: Add response-envelope types**

Append to `internal/remnawave/types.go`:

```go
// userResponse wraps the single-user lookup (GET /api/users/by-username/{username}).
type userResponse struct {
	Response User `json:"response"`
}

// usersByTgResponse wraps the by-Telegram-ID lookup, which returns an array
// because one Telegram ID may map to multiple subscriptions.
type usersByTgResponse struct {
	Response []User `json:"response"`
}
```

- [ ] **Step 2: Write failing tests**

Append to `internal/remnawave/client_test.go`:

```go
func TestGetUserByUsername(t *testing.T) {
	const token = "tok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/api/users/by-username/alice%20b" {
			t.Fatalf("path = %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-1","id":7,"username":"alice b","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":555}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, token, time.Second)
	u, err := c.GetUserByUsername(context.Background(), "alice b")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u == nil || u.UUID != "u-1" || u.ID != 7 || u.TelegramID == nil || *u.TelegramID != 555 {
		t.Fatalf("user wrong: %+v", u)
	}
}

func TestGetUserByUsernameNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	u, err := c.GetUserByUsername(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u != nil {
		t.Fatalf("want nil, got %+v", u)
	}
}

func TestGetUserByTelegramID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/by-telegram-id/123" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":[{"uuid":"u-a","id":1,"username":"a","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":123},{"uuid":"u-b","id":2,"username":"b","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":123}]}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	us, err := c.GetUserByTelegramID(context.Background(), 123)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(us) != 2 || us[0].Username != "a" || us[1].Username != "b" {
		t.Fatalf("users wrong: %+v", us)
	}
}

func TestGetUserByTelegramIDNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	us, err := c.GetUserByTelegramID(context.Background(), 999)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(us) != 0 {
		t.Fatalf("want empty, got %+v", us)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/remnawave/ -run 'ByUsername|ByTelegram' -v`
Expected: FAIL — `c.GetUserByUsername`/`c.GetUserByTelegramID` undefined.

- [ ] **Step 4: Implement the lookups**

Append to `internal/remnawave/client.go` (the file already imports `net/url`, `io`, `encoding/json`, `fmt`, `net/http`, and `textutil`):

```go
func (c *Client) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-username/%s", c.baseURL, url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("get user by username: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user by username: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload userResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode user: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return &payload.Response, nil
}

func (c *Client) GetUserByTelegramID(ctx context.Context, telegramID int64) ([]User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-telegram-id/%d", c.baseURL, telegramID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user by telegram id: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("get user by telegram id: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user by telegram id: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload usersByTgResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode users: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return payload.Response, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/remnawave/ -v`
Expected: PASS (all, including the new four).

- [ ] **Step 6: Commit**

```bash
git add internal/remnawave/types.go internal/remnawave/client.go internal/remnawave/client_test.go
git commit -m "feat(remnawave): add by-username and by-telegram-id lookups"
```

---

## Task 2: Store payer columns + idempotent migration

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/requests.go`
- Test: `internal/store/requests_test.go` (create)

- [ ] **Step 1: Write failing tests**

Create `internal/store/requests_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run 'PaymentRequest|Payer|Legacy' -v`
Expected: FAIL — `PayerTelegramID`/`PayerUsername` fields and `ensurePaymentRequestColumns` undefined.

- [ ] **Step 3: Update the schema and add the migration**

In `internal/store/store.go`, change the `payment_requests` block of the `schema` const so the final two lines become:

```go
  status            TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  confirmed_at      TEXT,
  payer_telegram_id INTEGER NOT NULL DEFAULT 0,
  payer_username    TEXT NOT NULL DEFAULT ''
);
```

Then, in `New`, after the `db.Exec(schema)` block and before `return &Store{db: db}, nil`, add the migration call:

```go
	if err := ensurePaymentRequestColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate payer columns: %w", err)
	}
```

And add this function at the end of `internal/store/store.go`:

```go
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
```

- [ ] **Step 4: Add the struct fields and wire CRUD**

In `internal/store/requests.go`, add two fields to the `PaymentRequest` struct (after `ConfirmedAt *time.Time`):

```go
	PayerTelegramID int64
	PayerUsername   string
```

Replace the `CreatePaymentRequest` INSERT so it includes the payer columns:

```go
func (s *Store) CreatePaymentRequest(ctx context.Context, r PaymentRequest) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests
			(remnawave_user_id, uuid, username, telegram_id, months, price, expire_at,
			 status, created_at, payer_telegram_id, payer_username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.RemnawaveID, r.UUID, r.Username, r.TelegramID, r.Months, r.Price,
		formatTime(r.ExpireAt), "pending", now, r.PayerTelegramID, r.PayerUsername)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
```

Replace `GetPaymentRequest`'s SELECT + Scan to read the payer columns:

```go
func (s *Store) GetPaymentRequest(ctx context.Context, id int64) (*PaymentRequest, error) {
	var (
		r            PaymentRequest
		exp, created string
		confirmed    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, remnawave_user_id, uuid, username, telegram_id, months, price,
			expire_at, status, created_at, confirmed_at, payer_telegram_id, payer_username
		FROM payment_requests WHERE id = ?
	`, id).Scan(&r.ID, &r.RemnawaveID, &r.UUID, &r.Username, &r.TelegramID, &r.Months,
		&r.Price, &exp, &r.Status, &created, &confirmed, &r.PayerTelegramID, &r.PayerUsername)
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (round-trip + legacy migration).

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/requests.go internal/store/requests_test.go
git commit -m "feat(store): add payer columns to payment_requests with idempotent migration"
```

---

## Task 3: Payments `Finder`/`Subscriber` + state scaffolding + `New` signature

This task changes the `payments.New` signature, so it also updates the two test call sites and the `main.go` construction in the same commit to keep the build green. No gift behavior yet — that is Task 4.

**Files:**
- Modify: `internal/payments/payments.go`
- Modify: `internal/payments/payments_test.go`
- Modify: `main.go`
- Test: `internal/payments/gift_test.go` (create — one small unit test for `isAllDigits`)

- [ ] **Step 1: Add `Subscriber`, `Finder`, state fields, and update `New`**

In `internal/payments/payments.go`, add `"sync"` to the imports (keep existing `context`, `fmt`, `log/slog`, `time`, `store`, `tg`). Do **not** add `"strings"` here — `payments.go` does not use it (the gift handlers that use `strings` live in `gift.go`, which has its own import block). Add the new types after the `Extender` interface:

```go
// Subscriber is the minimal user view the gift flow needs, kept payments-local
// so this package stays decoupled from the remnawave package.
type Subscriber struct {
	RemnawaveID int64
	UUID        string
	Username    string
	TelegramID  int64
	ExpireAt    time.Time
}

// Finder resolves a target subscriber by Telegram ID (may match several) or by
// username (at most one).
type Finder interface {
	FindByTelegramID(ctx context.Context, telegramID int64) ([]Subscriber, error)
	FindByUsername(ctx context.Context, username string) (*Subscriber, error)
}

type giftStep int

const (
	stepAwaitingIdentifier giftStep = iota
	stepAwaitingTariff
)

const giftTTL = 10 * time.Minute

type giftState struct {
	step      giftStep
	payerName string
	payerTGID int64
	target    *Subscriber
	createdAt time.Time
}
```

Add the new fields to the `Service` struct (after `now func() time.Time`):

```go
	finder Finder
	mu     sync.Mutex
	gifts  map[int64]*giftState
```

Change `New` to accept `finder` and initialize the map:

```go
func New(st *store.Store, bot BotSender, ext Extender, finder Finder, adminID int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	return &Service{
		store:    st,
		bot:      bot,
		extender: ext,
		finder:   finder,
		adminID:  adminID,
		currency: currency,
		dryRun:   dryRun,
		logger:   logger,
		now:      time.Now,
		gifts:    make(map[int64]*giftState),
	}
}
```

- [ ] **Step 2: Update the test call sites + add `fakeFinder`**

In `internal/payments/payments_test.go`:

Add the fake after `fakeExtender`:

```go
type fakeFinder struct {
	byTG   map[int64][]Subscriber
	byName map[string]*Subscriber
}

func (f *fakeFinder) FindByTelegramID(_ context.Context, id int64) ([]Subscriber, error) {
	return f.byTG[id], nil
}
func (f *fakeFinder) FindByUsername(_ context.Context, name string) (*Subscriber, error) {
	return f.byName[name], nil
}
```

Update `newTestService` to construct and pass a `fakeFinder` (return arity unchanged so existing tests are untouched):

```go
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
	svc := New(st, bot, ext, &fakeFinder{}, 1000 /*adminID*/, "₽", false /*dryRun*/, logger)
	return svc, bot, ext, st
}
```

Update the direct `New` call in `TestPaymentButtonNilWithoutAdmin` to add the finder arg:

```go
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeFinder{}, 0, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
```

- [ ] **Step 3: Add `isAllDigits` + its test**

Create `internal/payments/gift.go` with just the helper for now:

```go
package payments

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

Create `internal/payments/gift_test.go`:

```go
package payments

import "testing"

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{
		"":       false,
		"123":    true,
		"007":    true,
		"12a":    false,
		"alice":  false,
		" 12":    false,
		"-12":    false,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Fatalf("isAllDigits(%q) = %v, want %v", in, got, want)
		}
	}
}
```

- [ ] **Step 4: Update `main.go` construction with a Finder adapter**

In `main.go`, add `"context"` is already imported. Update the `payments.New` call:

```go
	pay := payments.New(db, bot, rwClient, rwFinder{rwClient}, cfg.Telegram.AdminID, cfg.Currency, cfg.DryRun, logger)
```

Add the adapter and converter at the bottom of `main.go`:

```go
// rwFinder adapts *remnawave.Client to payments.Finder, converting User -> Subscriber.
type rwFinder struct{ c *remnawave.Client }

func (f rwFinder) FindByTelegramID(ctx context.Context, telegramID int64) ([]payments.Subscriber, error) {
	us, err := f.c.GetUserByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	out := make([]payments.Subscriber, 0, len(us))
	for i := range us {
		out = append(out, toSubscriber(us[i]))
	}
	return out, nil
}

func (f rwFinder) FindByUsername(ctx context.Context, username string) (*payments.Subscriber, error) {
	u, err := f.c.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	s := toSubscriber(*u)
	return &s, nil
}

func toSubscriber(u remnawave.User) payments.Subscriber {
	var tgID int64
	if u.TelegramID != nil {
		tgID = *u.TelegramID
	}
	return payments.Subscriber{
		RemnawaveID: u.ID,
		UUID:        u.UUID,
		Username:    u.Username,
		TelegramID:  tgID,
		ExpireAt:    u.ExpireAt,
	}
}
```

- [ ] **Step 5: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS — everything compiles with the new `New` signature; `TestIsAllDigits` passes.

- [ ] **Step 6: Commit**

```bash
git add internal/payments/payments.go internal/payments/gift.go internal/payments/gift_test.go internal/payments/payments_test.go main.go
git commit -m "feat(payments): add Finder/Subscriber and gift-state scaffolding"
```

---

## Task 4: Gift conversation handlers

**Files:**
- Modify: `internal/payments/gift.go`
- Modify: `internal/payments/callbacks.go`
- Test: `internal/payments/gift_flow_test.go` (create — a **separate** file from `gift_test.go` to avoid a duplicate `testing` import)

- [ ] **Step 1: Write failing tests**

Create `internal/payments/gift_flow_test.go` (a new file; `gift_test.go` keeps only `TestIsAllDigits`). The helpers `fakeBot`, `fakeFinder`, and `newTestService` come from `payments_test.go` in the same package:

```go
package payments

import (
	"context"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func newGiftService(t *testing.T) (*Service, *fakeBot, *fakeFinder, *store.Store) {
	t.Helper()
	svc, bot, _, st := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	svc.finder = f
	return svc, bot, f, st
}

func giftMsg(chatID int64, text string) *tg.Message {
	return &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}, Text: text}
}

func TestPayffRejectsNonSubscriber(t *testing.T) {
	svc, bot, _, _ := newGiftService(t)
	if !svc.StartGiftFlow(context.Background(), giftMsg(200, "/payff")) {
		t.Fatal("should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].ChatID != 200 {
		t.Fatalf("expected one reply to payer: %+v", bot.sent)
	}
	// No conversation state should remain.
	if svc.getGift(200) != nil {
		t.Fatal("non-subscriber must not start a flow")
	}
}

func TestPayffHappyPathByUsername(t *testing.T) {
	svc, bot, f, st := newGiftService(t)
	ctx := context.Background()
	// Payer 200 is a subscriber.
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, UUID: "u-9", Username: "payer", TelegramID: 200, ExpireAt: time.Now()}}
	// Target "bob" exists.
	f.byName["bob"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.StartGiftFlow(ctx, giftMsg(200, "/payff")) {
		t.Fatal("start should be handled")
	}
	if !svc.HandleText(ctx, giftMsg(200, "bob")) {
		t.Fatal("identifier should be consumed")
	}
	// A tariff keyboard with a gpick button was sent to the payer.
	var kb *tg.InlineKeyboardMarkup
	for _, m := range bot.sent {
		if m.ChatID == 200 && m.Keyboard != nil {
			kb = m.Keyboard
		}
	}
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "gpick:3" {
		t.Fatalf("tariff keyboard wrong: %+v", kb)
	}

	// Tap the 3-month button.
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "gpick:3"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("gpick should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.UUID != "u-42" || req.Months != 3 || req.Price != 450 {
		t.Fatalf("request wrong: %+v", req)
	}
	if req.PayerTelegramID != 200 || req.PayerUsername != "payer" {
		t.Fatalf("payer not recorded: %+v", req)
	}
	// Admin got a confirm button.
	var adminConfirm bool
	for _, m := range bot.sent {
		if m.ChatID == 1000 && m.Keyboard != nil && m.Keyboard.InlineKeyboard[0][0].CallbackData == "ok:1" {
			adminConfirm = true
		}
	}
	if !adminConfirm {
		t.Fatalf("admin not notified: %+v", bot.sent)
	}
	// State cleared.
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared after pick")
	}
}

func TestPayffByTelegramIDAutodetect(t *testing.T) {
	svc, bot, f, st := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, UUID: "u-9", Username: "payer", TelegramID: 200}}
	f.byTG[555] = []Subscriber{{RemnawaveID: 42, UUID: "u-42", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}}
	_ = st.UpsertTariff(ctx, 1, 150)

	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "555")) {
		t.Fatal("digits should be consumed as TGID")
	}
	g := svc.getGift(200)
	if g == nil || g.target == nil || g.target.Username != "bob" {
		t.Fatalf("target not resolved by TGID: %+v", g)
	}
	_ = bot
}

func TestPayffMultiMatchAsksForUsername(t *testing.T) {
	svc, bot, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	f.byTG[555] = []Subscriber{
		{RemnawaveID: 1, Username: "a", TelegramID: 555},
		{RemnawaveID: 2, Username: "b", TelegramID: 555},
	}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "555")) {
		t.Fatal("should be consumed")
	}
	// Still awaiting an identifier (multi-match did not advance).
	g := svc.getGift(200)
	if g == nil || g.step != stepAwaitingIdentifier {
		t.Fatalf("should stay awaiting identifier: %+v", g)
	}
	last := bot.sent[len(bot.sent)-1]
	if last.ChatID != 200 {
		t.Fatalf("expected prompt to payer: %+v", last)
	}
}

func TestPayffNotFoundStaysInFlow(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "ghost")) {
		t.Fatal("should be consumed")
	}
	g := svc.getGift(200)
	if g == nil || g.step != stepAwaitingIdentifier {
		t.Fatalf("should stay awaiting identifier: %+v", g)
	}
}

func TestPayffCancelClearsState(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	if !svc.HandleText(ctx, giftMsg(200, "/cancel")) {
		t.Fatal("/cancel should be consumed while in flow")
	}
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared")
	}
	// /cancel with no active flow is not consumed.
	if svc.HandleText(ctx, giftMsg(300, "/cancel")) {
		t.Fatal("/cancel without a flow must not be consumed")
	}
}

func TestPayffTTLExpiry(t *testing.T) {
	svc, _, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	// 11 minutes later the session is stale.
	svc.now = func() time.Time { return base.Add(11 * time.Minute) }
	if svc.getGift(200) != nil {
		t.Fatal("stale session should be dropped on access")
	}
	// A typed identifier after expiry is no longer consumed.
	if svc.HandleText(ctx, giftMsg(200, "bob")) {
		t.Fatal("expired flow must not consume text")
	}
}

func TestGiftCancelButton(t *testing.T) {
	svc, bot, f, _ := newGiftService(t)
	ctx := context.Background()
	f.byTG[200] = []Subscriber{{RemnawaveID: 9, Username: "payer", TelegramID: 200}}
	svc.StartGiftFlow(ctx, giftMsg(200, "/payff"))
	cb := &tg.CallbackQuery{ID: "c", From: tg.User{ID: 200},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: 200}}, Data: "gcancel"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("gcancel should be handled")
	}
	if svc.getGift(200) != nil {
		t.Fatal("state should be cleared by gcancel")
	}
	if len(bot.edits) == 0 || bot.edits[len(bot.edits)-1].Keyboard != nil {
		t.Fatalf("expected keyboard cleared: %+v", bot.edits)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/payments/ -run 'Payff|Gift' -v`
Expected: FAIL — `StartGiftFlow`, `HandleText`, `getGift`, and the `gpick:`/`gcancel` dispatch are undefined.

- [ ] **Step 3: Implement the gift handlers**

Replace the entire contents of `internal/payments/gift.go` with:

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

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// --- in-memory conversation state ---

func (s *Service) getGift(chatID int64) *giftState {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gifts[chatID]
	if g == nil {
		return nil
	}
	if s.now().Sub(g.createdAt) > giftTTL {
		delete(s.gifts, chatID)
		return nil
	}
	return g
}

func (s *Service) setGift(chatID int64, g *giftState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gifts[chatID] = g
}

func (s *Service) clearGift(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gifts, chatID)
}

// StartGiftFlow handles /payff. Returns true if it consumed the message.
func (s *Service) StartGiftFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || s.adminID == 0 {
		return false
	}
	chatID := m.Chat.ID

	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("payff: find payer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if len(subs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Эта команда доступна только подписчикам.")
		return true
	}

	s.setGift(chatID, &giftState{
		step:      stepAwaitingIdentifier,
		payerName: subs[0].Username,
		payerTGID: chatID,
		createdAt: s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		"Введите имя пользователя или Telegram ID того, кому оплачиваете. /cancel — отмена.")
	return true
}

// HandleText consumes a free-text message when the chat is mid-/payff, plus
// /cancel. Returns true only when it handled the message.
func (s *Service) HandleText(ctx context.Context, m *tg.Message) bool {
	if m == nil {
		return false
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if text == "/cancel" {
		if s.getGift(chatID) == nil {
			return false
		}
		s.clearGift(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Отменено.")
		return true
	}

	g := s.getGift(chatID)
	if g == nil || g.step != stepAwaitingIdentifier {
		return false
	}

	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID,
			"Введите имя пользователя или Telegram ID, либо /cancel для отмены.")
		return true
	}
	if text == "" || len(text) > 64 {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректный ввод. Введите имя пользователя или Telegram ID.")
		return true
	}

	target, multi, err := s.resolveTarget(ctx, text)
	if err != nil {
		s.logger.Error("payff: resolve target failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if multi {
		_ = s.bot.SendPlain(ctx, chatID,
			"У этого Telegram ID несколько подписок, введите имя пользователя.")
		return true
	}
	if target == nil {
		_ = s.bot.SendPlain(ctx, chatID, "Пользователь не найден, попробуйте ещё раз.")
		return true
	}

	g.target = target
	g.step = stepAwaitingTariff
	s.setGift(chatID, g)

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("payff: list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	prompt := fmt.Sprintf("Оплата для %s (до %s). Выберите период:",
		target.Username, target.ExpireAt.Format("02.01.2006"))
	if len(tariffs) == 0 {
		tariffs = []store.Tariff{{Months: 1, Price: 0}} // fallback: single 1-month option
	}
	_ = s.bot.SendPlainWithKeyboard(ctx, chatID, prompt, s.giftTariffKeyboard(tariffs))
	return true
}

func (s *Service) resolveTarget(ctx context.Context, identifier string) (target *Subscriber, multi bool, err error) {
	if isAllDigits(identifier) {
		tgID, perr := strconv.ParseInt(identifier, 10, 64)
		if perr != nil {
			return nil, false, nil // overflow -> treat as not found
		}
		subs, ferr := s.finder.FindByTelegramID(ctx, tgID)
		if ferr != nil {
			return nil, false, ferr
		}
		switch len(subs) {
		case 0:
			return nil, false, nil
		case 1:
			t := subs[0]
			return &t, false, nil
		default:
			return nil, true, nil
		}
	}
	sub, ferr := s.finder.FindByUsername(ctx, identifier)
	if ferr != nil {
		return nil, false, ferr
	}
	if sub == nil {
		return nil, false, nil
	}
	return sub, false, nil
}

func (s *Service) giftTariffKeyboard(tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		label := fmt.Sprintf("%d мес.", t.Months)
		if t.Price > 0 {
			label = fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price))
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         label,
			CallbackData: fmt.Sprintf("gpick:%d", t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: "Отмена", CallbackData: "gcancel",
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) handleGiftPick(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	g := s.getGift(chatID)
	if g == nil || g.step != stepAwaitingTariff || g.target == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /payff заново.")
		return true
	}

	months, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "gpick:"))
	if err != nil || months < 1 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}

	price := 0
	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("payff: get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff != nil {
		price = tariff.Price
	} else {
		// Only allowed when no tariffs are configured at all (the 1-month fallback).
		all, lerr := s.store.ListTariffs(ctx)
		if lerr != nil {
			s.logger.Error("payff: list tariffs failed", "err", lerr.Error())
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
			return true
		}
		if len(all) > 0 {
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот тариф больше недоступен.")
			return true
		}
	}

	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID:     g.target.RemnawaveID,
		UUID:            g.target.UUID,
		Username:        g.target.Username,
		TelegramID:      g.target.TelegramID,
		Months:          months,
		Price:           price,
		ExpireAt:        g.target.ExpireAt,
		Status:          "pending",
		PayerTelegramID: g.payerTGID,
		PayerUsername:   g.payerName,
	})
	if err != nil {
		s.logger.Error("payff: create request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	text := s.formatGiftRequest(g, months, price)
	s.clearGift(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Подтвердить оплату", CallbackData: fmt.Sprintf("ok:%d", reqID)}},
		},
	}
	if err := s.bot.SendPlainWithKeyboard(ctx, s.adminID, text, kb); err != nil {
		s.logger.Error("payff: notify admin failed", "err", err.Error())
	}
	return true
}

func (s *Service) handleGiftCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearGift(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}

func (s *Service) formatGiftRequest(g *giftState, months, price int) string {
	var b strings.Builder
	b.WriteString("💳 Заявка на оплату за другого пользователя\n\n")
	b.WriteString(fmt.Sprintf("Плательщик: %s (TG %d)\n", g.payerName, g.payerTGID))
	b.WriteString("Получатель: " + g.target.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", g.target.RemnawaveID))
	b.WriteString("UUID: " + g.target.UUID + "\n")
	if g.target.TelegramID != 0 {
		b.WriteString(fmt.Sprintf("Telegram ID: %d\n", g.target.TelegramID))
	}
	b.WriteString("Подписка до: " + g.target.ExpireAt.Format("02.01.2006") + "\n")
	if price > 0 {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес. — %s", months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес.", months))
	}
	return b.String()
}
```

Note: this block **replaces** the entire stub `gift.go` created in Task 3. It re-includes `isAllDigits`, so there is still exactly one definition of it. `TestIsAllDigits` (in `gift_test.go`) continues to reference it.

- [ ] **Step 4: Add callback dispatch**

In `internal/payments/callbacks.go`, add two cases to the `switch` in `HandleCallback`, before `default:`:

```go
	case strings.HasPrefix(cb.Data, "gpick:"):
		return s.handleGiftPick(ctx, cb)
	case cb.Data == "gcancel":
		return s.handleGiftCancel(ctx, cb)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/payments/ -v`
Expected: PASS — all gift tests plus the pre-existing payment tests.

- [ ] **Step 6: Commit**

```bash
git add internal/payments/gift.go internal/payments/gift_flow_test.go internal/payments/callbacks.go
git commit -m "feat(payments): implement /payff gift conversation flow"
```

---

## Task 5: Route `/payff` and free text in main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add routing in `pollTelegramCallbacks`**

In `main.go`, inside the `if u.Message != nil && u.Message.Text != ""` block, after the existing `/start` handling and before the `pay.HandleAdminCommand` call, insert:

```go
				if strings.TrimSpace(u.Message.Text) == "/payff" {
					if pay.StartGiftFlow(ctx, u.Message) {
						continue
					}
				}
				if pay.HandleText(ctx, u.Message) {
					continue
				}
```

The resulting block routes in order: `/start` → `/payff` → mid-flow text / `/cancel` (`HandleText`) → admin commands.

- [ ] **Step 2: Build and run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS, no vet complaints.

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: route /payff and gift-flow text input"
```

---

## Task 6: Document `/payff` and final verification

**Files:**
- Modify: `README.md`
- Modify: `README.ru.md`

- [ ] **Step 1: Document the command (English)**

In `README.md`, in the section that describes the payment / «Я оплатил» flow, add a subsection:

```markdown
### Paying for another user (`/payff`)

Any existing subscriber can pay for someone else:

1. Send `/payff` to the bot.
2. Enter the target's **Remnawave username** or **Telegram ID** (a value that is
   all digits is looked up by Telegram ID, otherwise by username).
3. Pick a period from the tariff buttons (or the single 1-month option when no
   tariffs are configured). `/cancel` or the **Отмена** button aborts.
4. The admin receives a request naming both the payer and the target, with a
   **Подтвердить оплату** button. Confirming extends the target's subscription.

Only existing subscribers may start `/payff`; every request is still gated by
admin confirmation.
```

- [ ] **Step 2: Document the command (Russian)**

In `README.ru.md`, add the mirrored subsection:

```markdown
### Оплата за другого пользователя (`/payff`)

Любой действующий подписчик может оплатить подписку другому человеку:

1. Отправьте боту `/payff`.
2. Введите **имя пользователя Remnawave** или **Telegram ID** получателя (если
   значение состоит только из цифр — поиск по Telegram ID, иначе по имени).
3. Выберите период на кнопках тарифов (или единственный вариант на 1 месяц, если
   тарифы не заданы). Отмена — командой `/cancel` или кнопкой **Отмена**.
4. Администратор получит заявку с именами плательщика и получателя и кнопкой
   **Подтвердить оплату**. Подтверждение продлевает подписку получателя.

Команда `/payff` доступна только действующим подписчикам; каждая заявка
подтверждается администратором.
```

- [ ] **Step 3: Full verification**

Run each and confirm the expected result before claiming completion:

```bash
gofmt -l internal/ main.go      # expect: no output
go vet ./...                    # expect: no output
go build ./...                  # expect: no output
go test ./...                   # expect: all packages ok
```

- [ ] **Step 4: Commit**

```bash
git add README.md README.ru.md
git commit -m "docs: document the /payff pay-for-another-user command"
```

---

## Self-Review Notes (for the implementer)

- **Spec coverage:** payer eligibility (Task 4 `StartGiftFlow`), auto-detect identifier (Task 4 `resolveTarget`), multi-match prompt (Task 4 test), payer columns (Task 2), admin names both parties (Task 4 `formatGiftRequest`), `/cancel` + button (Task 4), routing (Task 5), lookups (Task 1), docs (Task 6) — all covered.
- **Type consistency:** `Subscriber` fields (`RemnawaveID`, `UUID`, `Username`, `TelegramID`, `ExpireAt`) are used identically in `payments.go`, `gift.go`, `payments_test.go`, and `main.go`'s `toSubscriber`. Callback prefixes `gpick:` / `gcancel` match between `giftTariffKeyboard`, `callbacks.go`, and the tests. `New`'s new `finder Finder` parameter position (4th, after `ext`) is used the same way in `main.go` and both test call sites.
- **`gift.go` single definition of `isAllDigits`:** Task 3 creates a stub `gift.go` containing only `isAllDigits`; Task 4 Step 3 replaces the whole file (re-including `isAllDigits`). Net result: one definition.
```
