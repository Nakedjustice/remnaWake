# /register Self-Service Telegram Linking — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an existing Remnawave subscriber bind their own Telegram ID to their panel account via a `/register` conversation, writing `telegramId` directly to the panel (refusing if the account is already linked to someone else).

**Architecture:** Mirror the existing `/invite` flow but with no DB table — the flow keeps only in-memory conversation state and performs an immediate `PATCH /api/users` write on confirmation. A new `Registrar` interface decouples the `payments` package from `remnawave`, wired in `main.go` via an adapter, exactly like the existing `Creator`/`Finder` adapters.

**Tech Stack:** Go 1.x, standard library `net/http`, `modernc.org/sqlite` (unused here), Telegram Bot API wrapper in `internal/telegram`. Tests use `net/http/httptest` and hand-written fakes.

---

## Task 1: Add `SetTelegramID` to the Remnawave client

**Files:**
- Modify: `internal/remnawave/client.go` (add method after `ExtendSubscriptionByUUID`, ends at line 122)
- Test: `internal/remnawave/client_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/remnawave/client_test.go`:

```go
func TestSetTelegramIDSendsUUIDAndTelegramID(t *testing.T) {
	const (
		token = "test-api-token"
		uuid  = "b1a2c3d4-0000-1111-2222-333344445555"
	)
	var telegramID int64 = 424242

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/users" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer "+token; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, body)
		}
		if got, want := payload["uuid"], uuid; got != want {
			t.Fatalf("body uuid = %v, want %v", got, want)
		}
		// JSON numbers decode to float64.
		if got, want := payload["telegramId"], float64(telegramID); got != want {
			t.Fatalf("body telegramId = %v, want %v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"` + uuid + `"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if err := client.SetTelegramID(context.Background(), uuid, telegramID); err != nil {
		t.Fatalf("SetTelegramID returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/remnawave/ -run TestSetTelegramID -v`
Expected: FAIL — `client.SetTelegramID undefined (type *Client has no field or method SetTelegramID)`

- [ ] **Step 3: Write the implementation**

In `internal/remnawave/client.go`, add this method directly after `ExtendSubscriptionByUUID` (after line 122, before `GetUserByUsername`):

```go
func (c *Client) SetTelegramID(ctx context.Context, uuid string, telegramID int64) error {
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
	payload := map[string]interface{}{
		"uuid":       uuid,
		"telegramId": telegramID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setRequestHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("set telegram id: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("set telegram id: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set telegram id: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/remnawave/ -run TestSetTelegramID -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/remnawave/client.go internal/remnawave/client_test.go
git commit -m "feat(remnawave): add SetTelegramID to write telegramId via PATCH"
```

---

## Task 2: Add `Registrar` interface and register state to the Service

This task wires the new dependency and state container into `payments.Service`. After it, the package still compiles and all existing tests pass, but no `/register` behavior exists yet.

**Files:**
- Modify: `internal/payments/payments.go` (interfaces near line 35; `Service` struct lines 73-88; `New` lines 90-105)
- Modify: `internal/payments/payments_test.go` (fakes + `newTestService` line 84-96; the no-admin `New` call at line 101)

- [ ] **Step 1: Add a fake Registrar and thread it through test setup**

In `internal/payments/payments_test.go`, add this fake after `fakeCreator` (after line 82):

```go
type fakeRegistrar struct {
	uuid       string
	telegramID int64
	calls      int
	err        error
}

func (f *fakeRegistrar) SetTelegramID(_ context.Context, uuid string, telegramID int64) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.uuid = uuid
	f.telegramID = telegramID
	return nil
}
```

Then update the two `New(...)` call sites to pass a registrar. Replace the body of `newTestService` (lines 84-96) so it constructs and returns the fake registrar:

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
	svc := New(st, bot, ext, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, 1000 /*adminID*/, "₽", false /*dryRun*/, logger)
	return svc, bot, ext, st
}
```

And update the no-admin construction in `TestPaymentButtonNilWithoutAdmin` (line 101):

```go
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, 0, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./internal/payments/ -run TestPaymentButton -v`
Expected: FAIL — build error: `too many arguments in call to New` (signature not updated yet).

- [ ] **Step 3: Add the interface, struct field, map, and constructor parameter**

In `internal/payments/payments.go`, add the `Registrar` interface immediately after the `Creator` interface (after line 37):

```go
// Registrar links an existing panel user to a Telegram ID.
type Registrar interface {
	SetTelegramID(ctx context.Context, uuid string, telegramID int64) error
}
```

Add fields to the `Service` struct. Change the dependency block (currently lines 74-82) and the state block (lines 84-88) so the struct reads:

```go
type Service struct {
	store     *store.Store
	bot       BotSender
	extender  Extender
	creator   Creator
	registrar Registrar
	adminID   int64
	currency  string
	dryRun    bool
	logger    *slog.Logger
	now       func() time.Time

	finder    Finder
	mu        sync.Mutex
	gifts     map[int64]*giftState
	invites   map[int64]*inviteState
	registers map[int64]*registerState
}
```

Update `New` (lines 90-105) to accept and store the registrar and initialize the map:

```go
func New(st *store.Store, bot BotSender, ext Extender, creator Creator, finder Finder, registrar Registrar, adminID int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	return &Service{
		store:     st,
		bot:       bot,
		extender:  ext,
		creator:   creator,
		registrar: registrar,
		finder:    finder,
		adminID:   adminID,
		currency:  currency,
		dryRun:    dryRun,
		logger:    logger,
		now:       time.Now,
		gifts:     make(map[int64]*giftState),
		invites:   make(map[int64]*inviteState),
		registers: make(map[int64]*registerState),
	}
}
```

> Note: `registerState` is referenced here but defined in Task 3. The package will not build until Task 3 adds that type. That is expected — Step 4 below confirms the expected build failure, and Task 3 Step 4 is where the suite goes green.

- [ ] **Step 4: Run tests to verify the expected remaining failure**

Run: `go test ./internal/payments/ 2>&1 | head -20`
Expected: FAIL — build error: `undefined: registerState`. (The `New` signature error from Step 2 is now resolved; the only remaining error is the missing type, which Task 3 adds.)

- [ ] **Step 5: Commit**

```bash
git add internal/payments/payments.go internal/payments/payments_test.go
git commit -m "feat(payments): add Registrar dependency and register state container"
```

---

## Task 3: Implement the register flow state machine

**Files:**
- Create: `internal/payments/register.go`
- Test: `internal/payments/register_flow_test.go` (created in Task 4; this task makes the package build again)

This task adds `register.go` with all flow logic. It references `s.registrar`, `s.finder`, `isValidUsername` (already in `invite.go`), and the bot interface — all already present. After this task the package builds and the existing suite passes.

- [ ] **Step 1: Create `internal/payments/register.go`**

```go
package payments

import (
	"context"
	"fmt"
	"strings"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

type registerState struct {
	requesterTGID int64
	username      string // empty = still awaiting username input
	uuid          string // resolved panel UUID once a free account is found
	createdAt     time.Time
}

const registerTTL = 10 * time.Minute

func (s *Service) getRegister(chatID int64) *registerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.registers[chatID]
	if r == nil {
		return nil
	}
	if s.now().Sub(r.createdAt) > registerTTL {
		delete(s.registers, chatID)
		return nil
	}
	return r
}

func (s *Service) setRegister(chatID int64, r *registerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registers[chatID] = r
}

func (s *Service) clearRegister(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registers, chatID)
}

// StartRegisterFlow handles /register. Returns true if the message was consumed.
func (s *Service) StartRegisterFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || s.adminID == 0 || s.registrar == nil {
		return false
	}
	s.beginRegisterFlow(ctx, m.Chat.ID)
	return true
}

func (s *Service) beginRegisterFlow(ctx context.Context, chatID int64) {
	s.setRegister(chatID, &registerState{
		requesterTGID: chatID,
		createdAt:     s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		"Введите имя вашего профила (Можно посмотреть в приложении). /cancel — отмена.")
}

// handleMenuRegister starts the register flow from the menu button.
func (s *Service) handleMenuRegister(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginRegisterFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleRegisterUsernameInput processes free-text input during an active register
// flow. Returns true if the message was consumed.
func (s *Service) handleRegisterUsernameInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	r := s.getRegister(chatID)
	if r == nil {
		return false
	}

	text := strings.TrimSpace(m.Text)

	// If a username is already resolved, the user sent text instead of tapping a
	// button — re-show the confirmation.
	if r.username != "" {
		s.showRegisterConfirm(ctx, chatID, r)
		return true
	}

	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID,
			"Введите имя вашего профила или /cancel для отмены.")
		return true
	}

	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов.")
		return true
	}

	sub, err := s.finder.FindByUsername(ctx, text)
	if err != nil {
		s.logger.Error("register: find by username failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if sub == nil {
		_ = s.bot.SendPlain(ctx, chatID,
			"Профиль с таким именем не найден. Попробуйте ещё раз.")
		return true
	}

	// Account already linked: idempotent if it's this user, refuse otherwise.
	if sub.TelegramID != 0 {
		s.clearRegister(chatID)
		if sub.TelegramID == r.requesterTGID {
			_ = s.bot.SendPlain(ctx, chatID,
				"Этот профиль уже привязан к вашему Telegram.")
		} else {
			_ = s.bot.SendPlain(ctx, chatID,
				"Этот профиль уже привязан к другому Telegram. Обратитесь к администратору.")
		}
		return true
	}

	r.username = sub.Username
	r.uuid = sub.UUID
	s.setRegister(chatID, r)
	s.showRegisterConfirm(ctx, chatID, r)
	return true
}

func (s *Service) showRegisterConfirm(ctx context.Context, chatID int64, r *registerState) {
	text := fmt.Sprintf("Привязать ваш Telegram к профилю «%s»?", r.username)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Привязать", CallbackData: "reg_confirm"}},
			{{Text: "Отмена", CallbackData: "reg_cancel"}},
		},
	}
	_ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleRegisterConfirm processes the "Привязать" button press.
func (s *Service) handleRegisterConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	r := s.getRegister(chatID)
	if r == nil || r.username == "" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /register заново.")
		return true
	}

	username := r.username
	uuid := r.uuid
	tgID := r.requesterTGID

	if s.dryRun {
		s.logger.Info("dry-run: would set telegram id", "username", username, "uuid", uuid, "telegram_id", tgID)
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Готово (dry-run).")
		_ = s.bot.SendPlain(ctx, chatID,
			fmt.Sprintf("✅ Готово! Ваш Telegram привязан к профилю «%s» (dry-run).", username))
		return true
	}

	if err := s.registrar.SetTelegramID(ctx, uuid, tgID); err != nil {
		s.logger.Error("register: set telegram id failed", "uuid", uuid, "err", err.Error())
		s.clearRegister(chatID)
		_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка привязки. Попробуйте позже.")
		return true
	}

	s.clearRegister(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Привязано!")
	_ = s.bot.SendPlain(ctx, chatID,
		fmt.Sprintf("✅ Готово! Ваш Telegram привязан к профилю «%s».", username))
	return true
}

// handleRegisterCancel processes the "Отмена" button shown during confirmation.
func (s *Service) handleRegisterCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearRegister(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}
```

- [ ] **Step 2: Run the package build to confirm it compiles**

Run: `go build ./internal/payments/`
Expected: success (no output). The `undefined: registerState` error from Task 2 is resolved.

- [ ] **Step 3: Run the existing payments suite**

Run: `go test ./internal/payments/`
Expected: PASS (existing tests unaffected; new flow not yet exercised).

- [ ] **Step 4: Commit**

```bash
git add internal/payments/register.go
git commit -m "feat(payments): add /register flow state machine"
```

---

## Task 4: Wire callbacks, text routing, menu, and `/cancel`

**Files:**
- Modify: `internal/payments/callbacks.go` (switch in `HandleCallback`, lines 18-47)
- Modify: `internal/payments/gift.go` (`HandleText` lines 153-221; `SendMenu` lines 91-110)
- Test: `internal/payments/register_flow_test.go` (create)

- [ ] **Step 1: Write the failing flow tests**

Create `internal/payments/register_flow_test.go`:

```go
package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// newRegisterService builds a Service with controllable finder + registrar fakes.
func newRegisterService(t *testing.T) (*Service, *fakeBot, *fakeFinder, *fakeRegistrar) {
	t.Helper()
	svc, bot, _, _ := newTestService(t) // adminID == 1000
	f := &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	reg := &fakeRegistrar{}
	svc.finder = f
	svc.registrar = reg
	return svc, bot, f, reg
}

func regMsg(chatID int64, text string) *tg.Message {
	return &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}, Text: text}
}

func regConfirmCB(chatID int64, data string) *tg.CallbackQuery {
	return &tg.CallbackQuery{ID: "c", From: tg.User{ID: chatID},
		Message: &tg.Message{MessageID: 5, Chat: tg.Chat{ID: chatID}}, Data: data}
}

func TestRegisterHappyPathBindsFreeAccount(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}

	if !svc.StartRegisterFlow(ctx, regMsg(200, "/register")) {
		t.Fatal("start should be handled")
	}
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("username should be consumed")
	}
	// Confirmation keyboard shown.
	var confirmShown bool
	for _, m := range bot.sent {
		if m.ChatID == 200 && m.Keyboard != nil &&
			m.Keyboard.InlineKeyboard[0][0].CallbackData == "reg_confirm" {
			confirmShown = true
		}
	}
	if !confirmShown {
		t.Fatalf("confirm keyboard not shown: %+v", bot.sent)
	}

	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 || reg.uuid != "u-42" || reg.telegramID != 200 {
		t.Fatalf("registrar not called correctly: calls=%d uuid=%s tgid=%d", reg.calls, reg.uuid, reg.telegramID)
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared after confirm")
	}
}

func TestRegisterNotFoundStaysInFlow(t *testing.T) {
	svc, bot, _, reg := newRegisterService(t)
	ctx := context.Background()
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "ghost")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("must not write for an unknown username")
	}
	if svc.getRegister(200) == nil {
		t.Fatal("should stay in flow to allow retry")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "не найден") {
		t.Fatalf("expected not-found message: %q", last.Text)
	}
}

func TestRegisterAlreadyLinkedToSelfIsIdempotent(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 200}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("no write needed when already linked to self")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "уже привязан к вашему") {
		t.Fatalf("expected idempotent message: %q", last.Text)
	}
}

func TestRegisterAlreadyLinkedToOtherIsRefused(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 999}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("should be consumed")
	}
	if reg.calls != 0 {
		t.Fatal("must not overwrite someone else's link")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared on refusal")
	}
	last := bot.sent[len(bot.sent)-1]
	if !strings.Contains(last.Text, "другому Telegram") {
		t.Fatalf("expected refusal message: %q", last.Text)
	}
}

func TestRegisterDryRunSkipsWrite(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	svc.dryRun = true
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 0 {
		t.Fatalf("dry-run must not write, calls=%d", reg.calls)
	}
	var ok bool
	for _, m := range bot.sent {
		if m.ChatID == 200 && strings.Contains(m.Text, "dry-run") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected dry-run confirmation: %+v", bot.sent)
	}
}

func TestRegisterWriteErrorReported(t *testing.T) {
	svc, bot, f, reg := newRegisterService(t)
	reg.err = errors.New("boom")
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_confirm")) {
		t.Fatal("reg_confirm should be handled")
	}
	if reg.calls != 1 {
		t.Fatalf("registrar should have been attempted once, calls=%d", reg.calls)
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared after a failed write")
	}
	_ = bot
}

func TestRegisterCancelButtonClearsState(t *testing.T) {
	svc, bot, f, _ := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.HandleText(ctx, regMsg(200, "alice"))
	if !svc.HandleCallback(ctx, regConfirmCB(200, "reg_cancel")) {
		t.Fatal("reg_cancel should be handled")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared by reg_cancel")
	}
	if len(bot.edits) == 0 || bot.edits[len(bot.edits)-1].Keyboard != nil {
		t.Fatalf("expected keyboard cleared: %+v", bot.edits)
	}
}

func TestRegisterCancelCommandClearsState(t *testing.T) {
	svc, _, _, _ := newRegisterService(t)
	ctx := context.Background()
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	if !svc.HandleText(ctx, regMsg(200, "/cancel")) {
		t.Fatal("/cancel should be consumed while in register flow")
	}
	if svc.getRegister(200) != nil {
		t.Fatal("state should be cleared")
	}
}

func TestRegisterTTLExpiry(t *testing.T) {
	svc, _, f, _ := newRegisterService(t)
	ctx := context.Background()
	f.byName["alice"] = &Subscriber{RemnawaveID: 42, UUID: "u-42", Username: "alice", TelegramID: 0}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	svc.StartRegisterFlow(ctx, regMsg(200, "/register"))
	svc.now = func() time.Time { return base.Add(11 * time.Minute) }
	if svc.getRegister(200) != nil {
		t.Fatal("stale session should be dropped on access")
	}
	if svc.HandleText(ctx, regMsg(200, "alice")) {
		t.Fatal("expired flow must not consume text")
	}
}

func TestSendMenuShowsRegisterButton(t *testing.T) {
	svc, bot, _, _ := newRegisterService(t)
	if !svc.SendMenu(context.Background(), 200) {
		t.Fatal("menu should be sent")
	}
	kb := bot.sent[0].Keyboard
	if kb == nil {
		t.Fatalf("expected keyboard: %+v", bot.sent[0])
	}
	found := map[string]bool{}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			found[btn.CallbackData] = true
		}
	}
	if !found["menu:register"] {
		t.Fatalf("register menu button missing: %+v", kb)
	}
}

func TestMenuRegisterButtonStartsFlow(t *testing.T) {
	svc, _, _, _ := newRegisterService(t)
	ctx := context.Background()
	cb := regConfirmCB(200, "menu:register")
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("menu:register should be handled")
	}
	if svc.getRegister(200) == nil {
		t.Fatal("register flow should start from the menu button")
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/payments/ -run TestRegister -v`
Expected: FAIL — `reg_confirm`/`reg_cancel`/`menu:register` are not dispatched, so callbacks return false and the asserting tests fail (e.g. `reg_confirm should be handled`).

- [ ] **Step 3a: Dispatch the new callbacks**

In `internal/payments/callbacks.go`, add three cases to the `switch` in `HandleCallback` (after the `inv_cancel` case at line 44, before `default`):

```go
	case cb.Data == "menu:register":
		return s.handleMenuRegister(ctx, cb)
	case cb.Data == "reg_confirm":
		return s.handleRegisterConfirm(ctx, cb)
	case cb.Data == "reg_cancel":
		return s.handleRegisterCancel(ctx, cb)
```

- [ ] **Step 3b: Route free text and `/cancel` to the register flow**

In `internal/payments/gift.go`, update `HandleText`. Replace the `/cancel` block (lines 160-170) so it also clears register state:

```go
	if text == "/cancel" {
		hasGift := s.getGift(chatID) != nil
		hasInvite := s.getInvite(chatID) != nil
		hasRegister := s.getRegister(chatID) != nil
		if !hasGift && !hasInvite && !hasRegister {
			return false
		}
		s.clearGift(chatID)
		s.clearInvite(chatID)
		s.clearRegister(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Отменено.")
		return true
	}
```

Then replace the gift-dispatch tail (lines 172-174):

```go
	g := s.getGift(chatID)
	if g == nil || g.step != stepAwaitingIdentifier {
		if s.handleInviteUsernameInput(ctx, m) {
			return true
		}
		return s.handleRegisterUsernameInput(ctx, m)
	}
```

- [ ] **Step 3c: Add the register button and line to the menu**

In `internal/payments/gift.go`, `SendMenu` (lines 91-110): add a `/register` line to the text and a button to the keyboard. Update the text literal:

```go
	text := "Меню\n\n" +
		"/tariff — посмотреть тарифы\n" +
		"/payff — оплатить подписку за другого пользователя\n" +
		"/invite — пригласить нового пользователя\n" +
		"/register — привязать свой Telegram к профилю\n" +
		"/cancel — отменить текущее действие"
```

And add the button as a new row in the keyboard (after the invite row):

```go
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "💵 Тарифы", CallbackData: "menu:tariffs"}},
			{{Text: "💳 Оплатить за другого", CallbackData: "menu:payff"}},
			{{Text: "👤 Пригласить пользователя", CallbackData: "menu:invite"}},
			{{Text: "🔗 Привязать аккаунт", CallbackData: "menu:register"}},
		},
	}
```

- [ ] **Step 4: Run the register tests to verify they pass**

Run: `go test ./internal/payments/ -run TestRegister -v`
Expected: PASS (all `TestRegister*`, `TestSendMenuShowsRegisterButton`, `TestMenuRegisterButtonStartsFlow`).

- [ ] **Step 5: Run the full payments suite to check for regressions**

Run: `go test ./internal/payments/`
Expected: PASS (existing `TestSendMenuShowsTariffAndPayffButtons` still passes — the new button is additive).

- [ ] **Step 6: Commit**

```bash
git add internal/payments/callbacks.go internal/payments/gift.go internal/payments/register_flow_test.go
git commit -m "feat(payments): wire /register callbacks, text routing, and menu button"
```

---

## Task 5: Wire `/register` into `main.go`

**Files:**
- Modify: `main.go` (`payments.New` call line 57; poll-loop switch lines 146-161; `userBotCommands` lines 175-185; adapters near line 214)

- [ ] **Step 1: Add the `rwRegistrar` adapter**

In `main.go`, after the `rwCreator` adapter (after line 223, before `toSubscriber`):

```go
// rwRegistrar adapts *remnawave.Client to payments.Registrar.
type rwRegistrar struct{ c *remnawave.Client }

func (r rwRegistrar) SetTelegramID(ctx context.Context, uuid string, telegramID int64) error {
	return r.c.SetTelegramID(ctx, uuid, telegramID)
}
```

- [ ] **Step 2: Pass the registrar into `payments.New`**

In `main.go`, update the construction (line 57):

```go
	pay := payments.New(db, bot, rwClient, rwCreator{rwClient}, rwFinder{rwClient}, rwRegistrar{rwClient}, cfg.Telegram.AdminID, cfg.Currency, cfg.DryRun, logger)
```

- [ ] **Step 3: Route the `/register` command**

In `main.go`, add a case to the message-text `switch` (after the `/invite` case, lines 157-160):

```go
				case "/register":
					if pay.StartRegisterFlow(ctx, u.Message) {
						continue
					}
```

- [ ] **Step 4: Advertise the command**

In `main.go`, `userBotCommands` (lines 176-184): add an entry after the `invite` command:

```go
		{Command: "register", Description: "Привязать свой Telegram к профилю"},
```

- [ ] **Step 5: Build and run the whole suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds; all packages PASS.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat: route /register command and wire Registrar adapter"
```

---

## Task 6: Final verification

- [ ] **Step 1: Vet and full test run**

Run: `go vet ./... && go test ./...`
Expected: no vet warnings; all tests PASS.

- [ ] **Step 2: Confirm `go build` produces a binary**

Run: `go build -o bot . && echo built`
Expected: `built` (then `rm -f bot` / `Remove-Item bot` to clean up if desired).

- [ ] **Step 3: Manual smoke checklist (document only, no code)**

Confirm by reading the diff that:
- `/register` prompt text is exactly "Введите имя вашего профила (Можно посмотреть в приложении). /cancel — отмена."
- The confirm button writes `telegramId == requester chat ID`.
- An already-linked-to-other account is refused with no write.

---

## Notes for the implementer

- `isValidUsername` already exists in `internal/payments/invite.go`; do not redefine it.
- The poll loop only runs when `TELEGRAM_ADMIN_ID != 0`, so `/register` is naturally unreachable without an admin configured — consistent with `/invite` and `/payff`. `StartRegisterFlow` also guards on `s.adminID == 0` and `s.registrar == nil`.
- `Subscriber.TelegramID` is an `int64` (already flattened from the panel's `*int64` in `toSubscriber`), so `0` means "unlinked".
- Keep commit messages scoped per task as shown; do not squash.
