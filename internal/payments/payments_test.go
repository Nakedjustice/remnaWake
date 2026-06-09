package payments

import (
	"context"
	"fmt"
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
	MsgID    int64
}
type editCall struct {
	ChatID, MessageID int64
	Keyboard          *tg.InlineKeyboardMarkup
}
type fakeBot struct {
	sent     []sentMsg
	answers  []string
	edits    []editCall
	msgIDSeq int64
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
func (f *fakeBot) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeBot) EditMessageReplyMarkup(_ context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error {
	f.edits = append(f.edits, editCall{ChatID: chatID, MessageID: messageID, Keyboard: kb})
	return nil
}

type fakeExtender struct {
	uuid   string
	expire time.Time
	calls  int
}

func (f *fakeExtender) ExtendSubscriptionByUUID(_ context.Context, uuid string, newExpireAt time.Time) error {
	f.uuid = uuid
	f.expire = newExpireAt
	f.calls++
	return nil
}

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

type fakeCreator struct {
	created []string
}

func (f *fakeCreator) CreateUser(_ context.Context, username string, _ time.Time) (*CreatedUser, error) {
	f.created = append(f.created, username)
	return &CreatedUser{UUID: "fake-uuid", Username: username}, nil
}

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
	svc := New(st, bot, ext, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, []int64{1000}, "₽", false /*dryRun*/, logger)
	return svc, bot, ext, st
}

func TestPaymentButtonNilWithoutAdmin(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{}, []int64{}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.PaymentButton(42) != nil {
		t.Fatal("expected nil button when no admins")
	}
}

func TestPaymentButtonHasPayCallback(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	kb := svc.PaymentButton(42)
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "pay:42" {
		t.Fatalf("unexpected button: %+v", kb)
	}
}

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
	if kb == nil || len(kb.InlineKeyboard) != 3 { // 2 tariff rows + 1 back row
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
