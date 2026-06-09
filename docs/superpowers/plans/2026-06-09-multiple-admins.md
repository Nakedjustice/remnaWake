# Multiple Admins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `TELEGRAM_ADMIN_ID` to accept a comma-separated list of Telegram user IDs so the bot can have multiple admins; all admins receive payment/invite notifications and any one can confirm/reject (button clears from all copies).

**Architecture:** Config parses the env var into `[]int64`; payments.Service holds `adminIDs []int64` with `isAdmin()` / `isEnabled()` helpers; per-admin input state is tracked in `map[int64]adminInputState`; admin notifications are broadcast to all admins and message IDs are stored so a single confirm clears every copy of the button.

**Tech Stack:** Go 1.21+, SQLite (modernc), Telegram Bot API (custom HTTP client), bash (install script)

---

## File Map

| File | Change |
|------|--------|
| `internal/config/config.go` | `AdminID int64` → `AdminIDs []int64`; add `parseAdminIDs` |
| `internal/config/config_test.go` | Update + add multi-ID tests |
| `internal/telegram/bot.go` | `sendMessage` returns `(int64, error)`; `SendPlainWithKeyboard` returns `(int64, error)` |
| `internal/telegram/bot_test.go` | Add `SendPlainWithKeyboard` message-ID test |
| `internal/payments/payments.go` | `adminIDs []int64`, `isAdmin()`, `isEnabled()`, `adminMsgRef`, ref maps, per-admin input map |
| `internal/payments/payments_test.go` | Update fake bot, `newTestService`, add broadcast tests |
| `internal/payments/commands.go` | All reply fns gain `chatID int64` param; input state uses map |
| `internal/payments/callbacks.go` | `isAdmin()` checks; `chatID` plumbed through admin menu; button-clearing uses ref loop |
| `internal/payments/invite.go` | Broadcast invite notifications; ref tracking; `isAdmin()` checks |
| `internal/payments/gift.go` | `isEnabled()` checks; broadcast gift notifications; ref tracking |
| `internal/payments/admin_menu_test.go` | `svc.adminInput[1000].step`, `SendAdminMenu(ctx, 1000)` |
| `internal/payments/requisites_test.go` | Update direct `New(...)` call to pass `[]int64{1000}` |
| `main.go` | Pass `cfg.Telegram.AdminIDs`; loop `SetMyCommandsForChat` |
| `install.sh` | Comma-aware `v_admin_id`; updated prompt and summary |

---

## Task 1: Config — parse multi-admin IDs

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/config/config_test.go` (keep existing tests, add these):

```go
func TestLoadAdminIDSingle(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 1 || cfg.Telegram.AdminIDs[0] != 123456 {
		t.Fatalf("AdminIDs = %v, want [123456]", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDMultiple(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "111, 222, 333")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 3 || cfg.Telegram.AdminIDs[1] != 222 {
		t.Fatalf("AdminIDs = %v, want [111 222 333]", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDZeroDisables(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 0 {
		t.Fatalf("AdminIDs = %v, want empty (disabled)", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDInvalidToken(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "111,bad")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with invalid TELEGRAM_ADMIN_ID token")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_ADMIN_ID") {
		t.Fatalf("error = %q, want mention of TELEGRAM_ADMIN_ID", err.Error())
	}
}
```

Add `"strings"` to imports in `config_test.go`.

- [ ] **Step 2: Run tests to confirm they fail**

```
cd internal/config && go test ./... -run "TestLoadAdminID" -v
```

Expected: FAIL (AdminIDs field doesn't exist yet).

- [ ] **Step 3: Update `TelegramConfig` struct**

In `internal/config/config.go`, replace:
```go
type TelegramConfig struct {
	BotToken  string
	ParseMode string
	AdminID   int64
}
```
with:
```go
type TelegramConfig struct {
	BotToken  string
	ParseMode string
	AdminIDs  []int64
}
```

- [ ] **Step 4: Add `parseAdminIDs` and update `Load`**

Add this function anywhere in `internal/config/config.go` (after the `getenvInt64` function is fine):

```go
func parseAdminIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, nil
	}
	tokens := strings.Split(raw, ",")
	out := make([]int64, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" || t == "0" {
			continue
		}
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TELEGRAM_ADMIN_ID: %q", raw)
		}
		out = append(out, n)
	}
	return out, nil
}
```

In `Load()`, replace the `Telegram` field initialization:
```go
// Before:
Telegram: TelegramConfig{
    BotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
    ParseMode: getenv("TELEGRAM_PARSE_MODE", "HTML"),
    AdminID:   getenvInt64("TELEGRAM_ADMIN_ID", 0),
},
```
with:
```go
// After: parse before building cfg so we can return early on error
```

Move the AdminIDs parsing before the `cfg := &Config{...}` block:

```go
func Load() (*Config, error) {
	adminIDs, err := parseAdminIDs(os.Getenv("TELEGRAM_ADMIN_ID"))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Remnawave: RemnawaveConfig{
			BaseURL:  strings.TrimRight(os.Getenv("REMNAWAVE_BASE_URL"), "/"),
			APIToken: strings.TrimSpace(os.Getenv("REMNAWAVE_API_TOKEN")),
		},
		Telegram: TelegramConfig{
			BotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
			ParseMode: getenv("TELEGRAM_PARSE_MODE", "HTML"),
			AdminIDs:  adminIDs,
		},
		Scheduler: SchedulerConfig{
			Timezone: getenv("TZ", "Europe/Moscow"),
			RunAt:    getenv("RUN_AT", "09:00"),
		},
		LogLevel:   parseLogLevel(getenv("LOG_LEVEL", "info")),
		DryRun:     getenvBool("DRY_RUN", false),
		RunOnStart: getenvBool("RUN_ON_START", true),
		DBPath:     getenv("DB_PATH", "/data/bot.db"),
		Currency:   getenv("CURRENCY", "₽"),
	}

	timeout := getenv("HTTP_TIMEOUT", "15s")
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP_TIMEOUT: %w", err)
	}
	cfg.HTTP.Timeout = d

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

- [ ] **Step 5: Remove the old `TELEGRAM_ADMIN_ID` check from `validate()`**

In `validate()`, remove these lines:
```go
if rawAdminID := os.Getenv("TELEGRAM_ADMIN_ID"); rawAdminID != "" {
    if _, err := strconv.ParseInt(rawAdminID, 10, 64); err != nil {
        return fmt.Errorf("invalid TELEGRAM_ADMIN_ID: %q", rawAdminID)
    }
}
```

- [ ] **Step 6: Update existing test `TestLoadValidatesTelegramAdminID`**

The error message is now produced by `parseAdminIDs`, not `validate()`, but the content is the same. The test should still pass unchanged — verify this in the next step.

- [ ] **Step 7: Run all config tests**

```
cd internal/config && go test ./... -v
```

Expected: all PASS.

- [ ] **Step 8: Commit**

```
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): parse TELEGRAM_ADMIN_ID as comma-separated list"
```

---

## Task 2: Bot — `SendPlainWithKeyboard` returns message ID

**Files:**
- Modify: `internal/telegram/bot.go`
- Modify: `internal/telegram/bot_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/telegram/bot_test.go`:

```go
func TestSendPlainWithKeyboardReturnsMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":100,"type":"private"},"text":"hi"}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL

	msgID, err := b.SendPlainWithKeyboard(context.Background(), 100, "hi", nil)
	if err != nil {
		t.Fatalf("SendPlainWithKeyboard: %v", err)
	}
	if msgID != 42 {
		t.Fatalf("msgID = %d, want 42", msgID)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```
cd internal/telegram && go test ./... -run TestSendPlainWithKeyboardReturnsMessageID -v
```

Expected: FAIL (wrong number of return values).

- [ ] **Step 3: Add `sendMessageResponse` and update `sendMessage`**

In `internal/telegram/bot.go`, add a new response type after `apiResponse`:

```go
type sendMessageResponse struct {
	apiResponse
	Result Message `json:"result"`
}
```

Replace the `sendMessage` method:

```go
func (b *Bot) sendMessage(ctx context.Context, payload sendMessageRequest) (int64, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	endpoint := b.apiBase + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		var ar apiResponse
		if err := json.Unmarshal(raw, &ar); err == nil && ar.Parameters != nil && ar.Parameters.RetryAfter > 0 {
			return 0, fmt.Errorf("telegram rate limited, retry_after=%ds", ar.Parameters.RetryAfter)
		}
		return 0, fmt.Errorf("telegram rate limited, status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("telegram send failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar sendMessageResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return 0, fmt.Errorf("telegram send not ok: %s", ar.Description)
	}
	return ar.Result.MessageID, nil
}
```

- [ ] **Step 4: Update callers of `sendMessage` inside `bot.go`**

`SendPlain` — discard message ID:
```go
func (b *Bot) SendPlain(ctx context.Context, chatID int64, text string) error {
	payload := sendMessageRequest{
		ChatID: chatID,
		Text:   text,
	}
	_, err := b.sendMessage(ctx, payload)
	return err
}
```

`SendPlainWithKeyboard` — return message ID:
```go
func (b *Bot) SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	payload := sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	}
	return b.sendMessage(ctx, payload)
}
```

`SendWithKeyboard` — discard message ID:
```go
func (b *Bot) SendWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) error {
	payload := sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   b.parseMode,
		ReplyMarkup: keyboard,
	}
	_, err := b.sendMessage(ctx, payload)
	return err
}
```

(`Send` calls `SendWithKeyboard` so it needs no change.)

- [ ] **Step 5: Run all bot tests**

```
cd internal/telegram && go test ./... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```
git add internal/telegram/bot.go internal/telegram/bot_test.go
git commit -m "feat(telegram): SendPlainWithKeyboard returns sent message ID"
```

---

## Task 3: Payments Service — adminIDs, helpers, per-admin input state

**Files:**
- Modify: `internal/payments/payments.go`
- Modify: `internal/payments/commands.go`
- Modify: `internal/payments/callbacks.go`
- Modify: `internal/payments/gift.go`
- Modify: `internal/payments/payments_test.go`
- Modify: `internal/payments/admin_menu_test.go`
- Modify: `internal/payments/requisites_test.go`

- [ ] **Step 1: Update `BotSender` interface and `fakeBot` in tests first (compile gate)**

In `internal/payments/payments.go`, update the interface:

```go
type BotSender interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) (int64, error)
	AnswerCallbackQuery(ctx context.Context, id, text string) error
	EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error
}
```

In `internal/payments/payments_test.go`, update `sentMsg`, `fakeBot`, and `newTestService`:

```go
type sentMsg struct {
	ChatID   int64
	Text     string
	Keyboard *tg.InlineKeyboardMarkup
	MsgID    int64
}

type fakeBot struct {
	sent      []sentMsg
	answers   []string
	edits     []editCall
	msgIDSeq  int64
}

func (f *fakeBot) SendPlain(_ context.Context, chatID int64, text string) error {
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text})
	return nil
}
func (f *fakeBot) SendPlainWithKeyboard(_ context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) (int64, error) {
	f.msgIDSeq++
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, nil
}
```

Also update `newTestService` (change `1000` to `[]int64{1000}` — we'll compile after updating `New` in Step 3):

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
	svc := New(st, bot, ext, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, []int64{1000}, "₽", false, logger)
	return svc, bot, ext, st
}
```

Update `TestPaymentButtonNilWithoutAdmin`:
```go
func TestPaymentButtonNilWithoutAdmin(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, []int64{}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.PaymentButton(42) != nil {
		t.Fatal("expected nil button when no admins")
	}
}
```

In `internal/payments/requisites_test.go`, update line 80:
```go
// Before:
svc2 := New(st, svc.bot, svc.extender, svc.creator, svc.finder, svc.registrar, 1000, "₽", false, svc.logger)
// After:
svc2 := New(st, svc.bot, svc.extender, svc.creator, svc.finder, svc.registrar, []int64{1000}, "₽", false, svc.logger)
```

- [ ] **Step 2: Update `Service` struct and `New` in `payments.go`**

Replace the `Service` struct and `New` function:

```go
type adminMsgRef struct {
	chatID    int64
	messageID int64
}

type Service struct {
	store     *store.Store
	bot       BotSender
	extender  Extender
	creator   Creator
	registrar Registrar
	adminIDs  []int64
	currency  string
	dryRun    bool
	logger    *slog.Logger
	now       func() time.Time

	finder     Finder
	mu         sync.Mutex
	gifts      map[int64]*giftState
	invites    map[int64]*inviteState
	registers  map[int64]*registerState

	adminInput map[int64]adminInputState
	payMsgs    map[int64][]adminMsgRef
	inviteMsgs map[int64][]adminMsgRef
	requisites string // protected by mu; empty = not set
}

func New(st *store.Store, bot BotSender, ext Extender, creator Creator, finder Finder, registrar Registrar, adminIDs []int64, currency string, dryRun bool, logger *slog.Logger) *Service {
	s := &Service{
		store:      st,
		bot:        bot,
		extender:   ext,
		creator:    creator,
		registrar:  registrar,
		finder:     finder,
		adminIDs:   adminIDs,
		currency:   currency,
		dryRun:     dryRun,
		logger:     logger,
		now:        time.Now,
		gifts:      make(map[int64]*giftState),
		invites:    make(map[int64]*inviteState),
		registers:  make(map[int64]*registerState),
		adminInput: make(map[int64]adminInputState),
		payMsgs:    make(map[int64][]adminMsgRef),
		inviteMsgs: make(map[int64][]adminMsgRef),
	}
	if value, found, err := st.GetSetting(context.Background(), requisitesKey); err != nil {
		logger.Error("load requisites failed", "err", err.Error())
	} else if found {
		s.requisites = value
	}
	return s
}
```

Add helper methods after `New`:

```go
func (s *Service) isAdmin(id int64) bool {
	for _, a := range s.adminIDs {
		if a == id {
			return true
		}
	}
	return false
}

func (s *Service) isEnabled() bool {
	return len(s.adminIDs) > 0
}
```

Update `PaymentButton`:
```go
func (s *Service) PaymentButton(userID int64) *tg.InlineKeyboardMarkup {
	if !s.isEnabled() {
		return nil
	}
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Я оплатил", CallbackData: fmt.Sprintf("pay:%d", userID)}},
		},
	}
}
```

- [ ] **Step 3: Rewrite `commands.go` with `chatID` param and per-admin input map**

Replace the entire `internal/payments/commands.go` with:

```go
package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func (s *Service) HandleAdminCommand(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() || !s.isAdmin(m.Chat.ID) {
		return false
	}
	chatID := m.Chat.ID
	if s.consumeAdminInput(ctx, m) {
		return true
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/admin":
		s.SendAdminMenu(ctx, chatID)
		return true
	case "/tariffs":
		s.cmdListTariffs(ctx, chatID)
		return true
	case "/settariff":
		s.cmdSetTariff(ctx, chatID, fields)
		return true
	case "/deltariff":
		s.cmdDelTariff(ctx, chatID, fields)
		return true
	case "/setrequisites":
		s.cmdSetRequisites(ctx, chatID)
		return true
	case "/requisites":
		s.cmdShowRequisites(ctx, chatID)
		return true
	default:
		return false
	}
}

func (s *Service) cmdSetRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputRequisites
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Отправьте текст реквизитов следующим сообщением.")
}

func (s *Service) cmdShowRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req == "" {
		_ = s.bot.SendPlain(ctx, chatID, "Реквизиты не заданы.")
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, "Реквизиты для оплаты:\n\n"+req)
}

func (s *Service) consumeAdminInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	s.mu.Lock()
	state := s.adminInput[chatID]
	s.mu.Unlock()
	if state.step == adminInputNone {
		return false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	switch state.step {
	case adminInputRequisites:
		return s.consumeRequisitesText(ctx, chatID, text)
	case adminInputTariffMonths:
		return s.consumeTariffMonths(ctx, chatID, text)
	case adminInputTariffPrice:
		return s.consumeTariffPrice(ctx, chatID, text)
	}
	return false
}

func (s *Service) consumeRequisitesText(ctx context.Context, chatID int64, text string) bool {
	if err := s.store.UpsertSetting(ctx, requisitesKey, text); err != nil {
		s.logger.Error("save requisites failed", "err", err.Error())
		s.mu.Lock()
		state := s.adminInput[chatID]
		state.step = adminInputNone
		s.adminInput[chatID] = state
		s.mu.Unlock()
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения реквизитов.")
		return true
	}
	s.mu.Lock()
	s.requisites = text
	state := s.adminInput[chatID]
	state.step = adminInputNone
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Реквизиты сохранены.")
	return true
}

func (s *Service) consumeTariffMonths(ctx context.Context, chatID int64, text string) bool {
	months, err := strconv.Atoi(text)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, "Введите целое число ≥ 1. Пример: 3")
		return true
	}
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.pendingMonths = months
	state.step = adminInputTariffPrice
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Введите цену (целое ≥ 0):")
	return true
}

func (s *Service) consumeTariffPrice(ctx context.Context, chatID int64, text string) bool {
	price, err := strconv.Atoi(text)
	if err != nil || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Введите целое число ≥ 0. Пример: 500")
		return true
	}
	s.mu.Lock()
	months := s.adminInput[chatID].pendingMonths
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения тарифа.")
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
	return true
}

func (s *Service) cmdListTariffs(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка чтения тарифов.")
		return
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Тарифы не заданы. Добавьте: /settariff <месяцев> <цена>")
		return
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, chatID, strings.TrimRight(b.String(), "\n"))
}

func (s *Service) cmdSetTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 3 {
		_ = s.bot.SendPlain(ctx, chatID, "Использование: /settariff <месяцев> <цена>")
		return
	}
	months, err1 := strconv.Atoi(fields[1])
	price, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || months < 1 || price < 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Месяцев — целое ≥ 1, цена — целое ≥ 0. Пример: /settariff 3 450")
		return
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		s.logger.Error("upsert tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка сохранения тарифа.")
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф сохранён: %d мес. — %s", months, s.priceLabel(price)))
}

func (s *Service) SendAdminMenu(ctx context.Context, chatID int64) {
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "📋 Посмотреть тарифы", CallbackData: "adm:tariffs"}},
			{{Text: "➕ Добавить тариф", CallbackData: "adm:addtariff"}},
			{{Text: "❌ Удалить тариф", CallbackData: "adm:del_list"}},
			{{Text: "💳 Посмотреть реквизиты", CallbackData: "adm:req"}},
			{{Text: "✏️ Изменить реквизиты", CallbackData: "adm:setreq"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, "Меню администратора", kb)
}

func (s *Service) cmdDelTariff(ctx context.Context, chatID int64, fields []string) {
	if len(fields) != 2 {
		_ = s.bot.SendPlain(ctx, chatID, "Использование: /deltariff <месяцев>")
		return
	}
	months, err := strconv.Atoi(fields[1])
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, "Месяцев — целое ≥ 1. Пример: /deltariff 3")
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка удаления тарифа.")
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. не найден.", months))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. удалён.", months))
}
```

- [ ] **Step 4: Update `callbacks.go` admin-menu and auth checks**

In `callbacks.go`, update `handleConfirm` auth check:
```go
// Before:
if s.adminID == 0 || cb.From.ID != s.adminID {
// After:
if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
```

Update `handleAdminMenu`:
```go
func (s *Service) handleAdminMenu(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	chatID := cb.From.ID
	switch {
	case cb.Data == "adm:menu":
		s.SendAdminMenu(ctx, chatID)
	case cb.Data == "adm:tariffs":
		s.sendAdminTariffs(ctx, chatID)
	case cb.Data == "adm:del_list":
		s.sendAdminDelList(ctx, chatID)
	case cb.Data == "adm:req":
		s.sendAdminRequisites(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:del:"):
		s.handleAdminDelTariff(ctx, chatID, cb.Data)
	case cb.Data == "adm:setreq":
		s.startSetRequisitesFlow(ctx, chatID)
	case cb.Data == "adm:addtariff":
		s.startAddTariffFlow(ctx, chatID)
	}
	return true
}
```

Update `sendAdminTariffs`, `sendAdminDelList`, `sendAdminRequisites`, `handleAdminDelTariff`, `startSetRequisitesFlow`, `startAddTariffFlow` to accept `chatID int64` and replace all `s.adminID` with `chatID`:

```go
func (s *Service) sendAdminTariffs(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("admin: list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка чтения тарифов.")
		return
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "← Меню", CallbackData: "adm:menu"}},
		},
	}
	if len(tariffs) == 0 {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, "Тарифы не заданы.", kb)
		return
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(b.String(), "\n"), kb)
}

func (s *Service) sendAdminDelList(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("admin: list tariffs for delete failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка чтения тарифов.")
		return
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("adm:del:%d", t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: "← Меню", CallbackData: "adm:menu"}})
	text := "Выберите тариф для удаления:"
	if len(tariffs) == 0 {
		text = "Тарифы не заданы."
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) sendAdminRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "← Меню", CallbackData: "adm:menu"}},
		},
	}
	text := "Реквизиты не заданы."
	if req != "" {
		text = "Реквизиты для оплаты:\n\n" + req
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

func (s *Service) handleAdminDelTariff(ctx context.Context, chatID int64, data string) {
	monthsStr := strings.TrimPrefix(data, "adm:del:")
	months, err := strconv.Atoi(monthsStr)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, "Не удалось распознать тариф.")
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("admin: delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка удаления тарифа.")
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. не найден.", months))
	} else {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf("Тариф на %d мес. удалён.", months))
	}
	s.sendAdminDelList(ctx, chatID)
}

func (s *Service) startSetRequisitesFlow(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputRequisites
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Отправьте новый текст реквизитов:")
}

func (s *Service) startAddTariffFlow(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputTariffMonths
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, "Введите количество месяцев (целое ≥ 1):")
}
```

- [ ] **Step 5: Update `gift.go` — `isEnabled()` checks and `SendMenu` call**

In `gift.go`, update `StartGiftFlow`:
```go
func (s *Service) StartGiftFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() {
		return false
	}
	s.beginGiftFlow(ctx, m.Chat.ID)
	return true
}
```

Update `SendMenu`:
```go
func (s *Service) SendMenu(ctx context.Context, chatID int64) bool {
	text := "Меню\n\n" +
		"/tariff — посмотреть тарифы\n" +
		"/payff — оплатить подписку за другого пользователя\n" +
		"/invite — пригласить нового пользователя\n" +
		"/register — привязать свой Telegram к профилю\n" +
		"/cancel — отменить текущее действие"
	if !s.isEnabled() {
		_ = s.bot.SendPlain(ctx, chatID, text)
		return true
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "💵 Тарифы", CallbackData: "menu:tariffs"}},
			{{Text: "💳 Оплатить за другого", CallbackData: "menu:payff"}},
			{{Text: "👤 Пригласить пользователя", CallbackData: "menu:invite"}},
			{{Text: "🔗 Привязать аккаунт", CallbackData: "menu:register"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
	return true
}
```

- [ ] **Step 6: Update `admin_menu_test.go` — fix direct field and method accesses**

In `admin_menu_test.go`, change all `svc.adminInput.step` → `svc.adminInput[1000].step`:

```go
// TestAdmSetReqCallbackStartsFlow: replace
svc.mu.Lock()
step := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
step := svc.adminInput[1000].step
svc.mu.Unlock()
```

```go
// TestAdmAddTariffCallbackStartsFlow: replace
svc.mu.Lock()
step := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
step := svc.adminInput[1000].step
svc.mu.Unlock()
```

```go
// TestAdmAddTariffFullFlow: two occurrences — replace both
// (after months step)
svc.mu.Lock()
step := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
step := svc.adminInput[1000].step
svc.mu.Unlock()

// (final state reset check)
svc.mu.Lock()
finalStep := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
finalStep := svc.adminInput[1000].step
svc.mu.Unlock()
```

```go
// TestAdmAddTariffInvalidInput: replace
svc.mu.Lock()
step := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
step := svc.adminInput[1000].step
svc.mu.Unlock()
```

```go
// TestAdmInputFlowCancelledByCommand: replace
svc.mu.Lock()
step := svc.adminInput.step
svc.mu.Unlock()
// with:
svc.mu.Lock()
step := svc.adminInput[1000].step
svc.mu.Unlock()
```

Also update the `TestSendAdminMenu` call — `SendAdminMenu` now needs a `chatID`:
```go
// Before:
svc.SendAdminMenu(context.Background())
// After:
svc.SendAdminMenu(context.Background(), 1000)
```

- [ ] **Step 7: Run all payments tests**

```
cd internal/payments && go test ./... -v
```

Expected: all PASS.

- [ ] **Step 8: Commit**

```
git add internal/payments/payments.go internal/payments/payments_test.go \
        internal/payments/commands.go internal/payments/callbacks.go \
        internal/payments/gift.go internal/payments/admin_menu_test.go \
        internal/payments/requisites_test.go
git commit -m "feat(payments): support multiple admin IDs with per-admin input state"
```

---

## Task 4: Broadcast notifications and button clearing

**Files:**
- Modify: `internal/payments/callbacks.go`
- Modify: `internal/payments/invite.go`
- Modify: `internal/payments/gift.go`
- Modify: `internal/payments/payments_test.go`

- [ ] **Step 1: Write failing tests for broadcast behaviour**

Add to `internal/payments/payments_test.go`:

```go
func newTestServiceTwoAdmins(t *testing.T) (*Service, *fakeBot, *fakeExtender, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	ext := &fakeExtender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, ext, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, []int64{1000, 2000}, "₽", false, logger)
	return svc, bot, ext, st
}

func TestBroadcastNotifiesBothAdmins(t *testing.T) {
	svc, bot, _, st := newTestServiceTwoAdmins(t)
	ctx := context.Background()
	rememberAlice(t, st)

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}

	adminGot := map[int64]bool{}
	for _, m := range bot.sent {
		if m.Keyboard != nil && m.Keyboard.InlineKeyboard[0][0].CallbackData == "ok:1" {
			adminGot[m.ChatID] = true
		}
	}
	if !adminGot[1000] || !adminGot[2000] {
		t.Fatalf("expected both admins to receive confirm button, got: %+v", bot.sent)
	}
}

func TestConfirmClearsBothAdminButtons(t *testing.T) {
	svc, bot, _, st := newTestServiceTwoAdmins(t)
	ctx := context.Background()
	rememberAlice(t, st)

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay: should be handled")
	}
	bot.edits = nil

	if !svc.HandleCallback(ctx, cbq(1000, "ok:1")) {
		t.Fatal("ok: should be handled")
	}

	cleared := map[int64]bool{}
	for _, e := range bot.edits {
		if e.Keyboard == nil {
			cleared[e.ChatID] = true
		}
	}
	if !cleared[1000] || !cleared[2000] {
		t.Fatalf("expected buttons cleared for both admins, edits: %+v", bot.edits)
	}
}

func TestSecondConfirmIsNoop(t *testing.T) {
	svc, _, ext, st := newTestServiceTwoAdmins(t)
	ctx := context.Background()
	rememberAlice(t, st)

	svc.HandleCallback(ctx, cbq(777, "pay:42"))
	svc.HandleCallback(ctx, cbq(1000, "ok:1")) // first confirm
	calls := ext.calls
	svc.HandleCallback(ctx, cbq(2000, "ok:1")) // second confirm — already done
	if ext.calls != calls {
		t.Fatalf("second confirm must not extend again: calls=%d", ext.calls)
	}
}
```

- [ ] **Step 2: Run new tests to confirm they fail**

```
cd internal/payments && go test ./... -run "TestBroadcast|TestConfirmClears|TestSecondConfirm" -v
```

Expected: FAIL (still sending to single admin).

- [ ] **Step 3: Update `createRequestAndNotify` in `callbacks.go`**

Replace the notification block inside `createRequestAndNotify` (the part after `s.bot.EditMessageReplyMarkup` and `s.bot.AnswerCallbackQuery`):

```go
// Notify all admins with details + confirm button; collect message refs.
text := s.formatAdminRequest(u, months, price)
kb := &tg.InlineKeyboardMarkup{
    InlineKeyboard: [][]tg.InlineKeyboardButton{
        {{Text: "Подтвердить оплату", CallbackData: fmt.Sprintf("ok:%d", reqID)}},
    },
}
var refs []adminMsgRef
for _, adminID := range s.adminIDs {
    msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
    if err != nil {
        s.logger.Error("notify admin failed", "admin_id", adminID, "err", err.Error())
        continue
    }
    refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
}
s.mu.Lock()
s.payMsgs[reqID] = refs
s.mu.Unlock()
```

- [ ] **Step 4: Update `handleConfirm` in `callbacks.go` to clear all admin buttons**

In `handleConfirm`, remove the existing `if cb.Message != nil { EditMessageReplyMarkup... }` block and replace it (after the dryRun block and after `ConfirmPaymentRequest`) with:

```go
// Clear the confirm button from every admin's copy of the notification.
s.mu.Lock()
refs := s.payMsgs[reqID]
delete(s.payMsgs, reqID)
s.mu.Unlock()
for _, ref := range refs {
    if err := s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil); err != nil {
        s.logger.Warn("clear admin confirm button failed", "chat_id", ref.chatID, "err", err.Error())
    }
}
_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Подписка продлена!")
_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf("✅ Подписка для %s продлена на %d мес. до %s",
    req.Username, req.Months, newExpireAt.Format("02.01.2006")))
return true
```

Also update the dry-run branch similarly — replace `s.bot.EditMessageReplyMarkup(cb.Message...)` with the refs loop and use `cb.From.ID` for the confirmation message. Full dry-run block:

```go
if s.dryRun {
    s.logger.Info("dry-run: would extend", "uuid", req.UUID, "months", req.Months, "new_expire", newExpireAt.Format("2006-01-02"))
    _, _ = s.store.ConfirmPaymentRequest(ctx, reqID, s.now())
    s.mu.Lock()
    refs := s.payMsgs[reqID]
    delete(s.payMsgs, reqID)
    s.mu.Unlock()
    for _, ref := range refs {
        _ = s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil)
    }
    _ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подписка продлена (dry-run).")
    return true
}
```

- [ ] **Step 5: Update `handleInviteSubmit` in `invite.go` to broadcast**

Replace the `s.bot.SendPlainWithKeyboard(ctx, s.adminID, text, kb)` block with:

```go
var refs []adminMsgRef
for _, adminID := range s.adminIDs {
    msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
    if err != nil {
        s.logger.Error("invite: notify admin failed", "admin_id", adminID, "err", err.Error())
        continue
    }
    refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
}
s.mu.Lock()
s.inviteMsgs[reqID] = refs
s.mu.Unlock()
```

- [ ] **Step 6: Update `handleInviteApprove` in `invite.go` to clear all buttons**

In `handleInviteApprove`, replace the `if cb.Message != nil { EditMessageReplyMarkup... }` call with:

```go
s.mu.Lock()
refs := s.inviteMsgs[reqID]
delete(s.inviteMsgs, reqID)
s.mu.Unlock()
for _, ref := range refs {
    _ = s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil)
}
_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Пользователь создан!")
_ = s.bot.SendPlain(ctx, cb.From.ID,
    fmt.Sprintf("✅ Пользователь «%s» создан (UUID: %s), подписка до %s.",
        created.Username, created.UUID, expireAt.Format("02.01.2006")))
```

Also update the dry-run branch in `handleInviteApprove` the same way.

Update `handleInviteReject` similarly:

```go
s.mu.Lock()
refs := s.inviteMsgs[reqID]
delete(s.inviteMsgs, reqID)
s.mu.Unlock()
for _, ref := range refs {
    _ = s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil)
}
_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отклонена.")
_ = s.bot.SendPlain(ctx, cb.From.ID,
    fmt.Sprintf("❌ Заявка на пользователя «%s» отклонена.", req.NewUsername))
```

Update auth checks in both `handleInviteApprove` and `handleInviteReject`:
```go
// Before (appears near the top of each function):
if s.adminID == 0 || cb.From.ID != s.adminID {
    s.logger.Warn("unauthorized invite approve attempt", "from_id", cb.From.ID)
    _ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
    return true
}
// After:
if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
    s.logger.Warn("unauthorized invite approve attempt", "from_id", cb.From.ID)
    _ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
    return true
}
```
(Same pattern for `handleInviteReject`, just the log message differs.)

- [ ] **Step 7: Update `handleGiftPick` in `gift.go` to broadcast**

Replace `s.bot.SendPlainWithKeyboard(ctx, s.adminID, text, kb)` with:

```go
var refs []adminMsgRef
for _, adminID := range s.adminIDs {
    msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
    if err != nil {
        s.logger.Error("payff: notify admin failed", "admin_id", adminID, "err", err.Error())
        continue
    }
    refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
}
s.mu.Lock()
s.payMsgs[reqID] = refs
s.mu.Unlock()
```

- [ ] **Step 8: Run all payments tests**

```
cd internal/payments && go test ./... -v
```

Expected: all PASS including the new broadcast tests.

- [ ] **Step 9: Commit**

```
git add internal/payments/callbacks.go internal/payments/invite.go \
        internal/payments/gift.go internal/payments/payments_test.go
git commit -m "feat(payments): broadcast admin notifications; clear all buttons on confirm"
```

---

## Task 5: Wire up `main.go`

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update `main.go`**

Replace the three places that reference `cfg.Telegram.AdminID`:

```go
// Log line (line ~39):
// Before:
"payment_notifications_enabled", cfg.Telegram.AdminID != 0,
// After:
"payment_notifications_enabled", len(cfg.Telegram.AdminIDs) > 0,

// payments.New call (line ~57):
// Before:
pay := payments.New(db, bot, rwClient, rwCreator{rwClient}, rwFinder{rwClient}, rwRegistrar{rwClient}, cfg.Telegram.AdminID, cfg.Currency, cfg.DryRun, logger)
// After:
pay := payments.New(db, bot, rwClient, rwCreator{rwClient}, rwFinder{rwClient}, rwRegistrar{rwClient}, cfg.Telegram.AdminIDs, cfg.Currency, cfg.DryRun, logger)

// Admin commands setup block (line ~63):
// Before:
if cfg.Telegram.AdminID != 0 {
    if err := bot.SetMyCommands(rootCtx, userBotCommands()); err != nil {
        logger.Warn("set bot commands failed", "err", err.Error())
    }
    if err := bot.SetMyCommandsForChat(rootCtx, cfg.Telegram.AdminID, adminBotCommands()); err != nil {
        logger.Warn("set admin bot commands failed", "err", err.Error())
    }
    go pollTelegramCallbacks(rootCtx, bot, pay, logger)
}
// After:
if len(cfg.Telegram.AdminIDs) > 0 {
    if err := bot.SetMyCommands(rootCtx, userBotCommands()); err != nil {
        logger.Warn("set bot commands failed", "err", err.Error())
    }
    for _, adminID := range cfg.Telegram.AdminIDs {
        if err := bot.SetMyCommandsForChat(rootCtx, adminID, adminBotCommands()); err != nil {
            logger.Warn("set admin bot commands failed", "err", err.Error(), "admin_id", adminID)
        }
    }
    go pollTelegramCallbacks(rootCtx, bot, pay, logger)
}
```

- [ ] **Step 2: Build to confirm no compile errors**

```
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run full test suite**

```
go test ./...
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```
git add main.go
git commit -m "feat: wire multiple AdminIDs through to payments service and bot commands"
```

---

## Task 6: install.sh — comma-aware validator

**Files:**
- Modify: `install.sh`

- [ ] **Step 1: Update `v_admin_id` validator**

Replace the existing `v_admin_id` function:

```bash
v_admin_id() {
  local input="$1"
  # Single "0" disables the feature.
  if [ "$input" = "0" ]; then
    warn "  → 0 entered: the «Я оплатил» payment and «/invite» flows will be disabled."
    return 0
  fi
  # Split on commas and validate each token.
  local old_ifs="$IFS"
  IFS=','
  local valid=0
  for token in $input; do
    IFS="$old_ifs"
    token="$(printf '%s' "$token" | tr -d ' \t')"
    [ -z "$token" ] && continue
    if ! printf '%s' "$token" | grep -Eq '^[0-9]+$'; then
      err "  → Each ID must be digits only. Got: \"$token\""
      IFS="$old_ifs"
      return 1
    fi
    valid=$((valid + 1))
  done
  IFS="$old_ifs"
  if [ "$valid" -eq 0 ]; then
    err "  → Must be a numeric Telegram user ID (digits only), or 0 to disable."
    return 1
  fi
  return 0
}
```

- [ ] **Step 2: Update the prompt text**

Replace the `ask TELEGRAM_ADMIN_ID` line:

```bash
# Before:
ask TELEGRAM_ADMIN_ID   "Telegram admin user ID (numeric, from @userinfobot; 0 to disable payments & invites)" "0" v_admin_id
# After:
ask TELEGRAM_ADMIN_ID   "Telegram admin user ID(s) (numeric, comma-separated e.g. 123456 or 123456,789012; 0 to disable)" "0" v_admin_id
```

- [ ] **Step 3: Update the summary line**

Replace the `Admin ID` line in the summary block:

```bash
# Before:
  Admin ID           : $TELEGRAM_ADMIN_ID
# After:
  Admin ID(s)        : $TELEGRAM_ADMIN_ID
```

- [ ] **Step 4: Manual smoke test of the validator**

```bash
bash -c '
source ./install.sh 2>/dev/null || true

# Test valid single ID
v_admin_id "123456" && echo "PASS: single ID"

# Test valid multiple IDs
v_admin_id "123456,789012" && echo "PASS: multiple IDs"

# Test zero disables
v_admin_id "0" && echo "PASS: zero"

# Test invalid token
v_admin_id "123,bad" && echo "FAIL: should reject" || echo "PASS: rejected bad token"

# Test spaces between IDs
v_admin_id "123 , 456" && echo "PASS: spaces trimmed"
'
```

Note: the above won't work exactly as-is because `install.sh` has `set -euo pipefail` and interactive prompts. Instead, test by temporarily copying just the function to a test script or by doing a quick manual interactive run of `./install.sh` with each input type.

- [ ] **Step 5: Commit**

```
git add install.sh
git commit -m "feat(install): support comma-separated admin IDs in v_admin_id validator"
```

---

## Final: Full test run and branch check

- [ ] **Run full test suite from repo root**

```
go test ./...
```

Expected: all PASS.

- [ ] **Confirm branch is clean**

```
git status
git log --oneline -8
```

Expected: clean working tree, 6 feature commits visible.
