package payments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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
	Text              string
	Keyboard          *tg.InlineKeyboardMarkup
}
type sentPhoto struct {
	ChatID   int64
	FileID   string
	Caption  string
	Keyboard *tg.InlineKeyboardMarkup
	MsgID    int64
}
type sentInvoice struct {
	ChatID  int64
	Title   string
	Payload string
	Prices  []tg.LabeledPrice
	MsgID   int64
}
type fakeBot struct {
	sent            []sentMsg
	photos          []sentPhoto
	docs            []sentPhoto // same shape: chat, file_id, caption, keyboard
	answers         []string
	edits           []editCall
	invoices        []sentInvoice
	invoiceLinks    []sentInvoice
	precheckoutAck  []bool
	msgIDSeq        int64
	photoUploads    int
	documentUploads int
	// sendErrs makes every send method fail for specific chat IDs.
	sendErrs map[int64]error
	// invoiceErr makes SendInvoice / CreateInvoiceLink fail when set.
	invoiceErr error
	plainSent  chan sentMsg
}

func (f *fakeBot) SendPlain(_ context.Context, chatID int64, text string) error {
	if err := f.sendErrs[chatID]; err != nil {
		return err
	}
	msg := sentMsg{ChatID: chatID, Text: text}
	f.sent = append(f.sent, msg)
	if f.plainSent != nil {
		f.plainSent <- msg
	}
	return nil
}
func (f *fakeBot) SendPlainWithKeyboard(_ context.Context, chatID int64, text string, kb *tg.InlineKeyboardMarkup) (int64, error) {
	if err := f.sendErrs[chatID]; err != nil {
		return 0, err
	}
	f.msgIDSeq++
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, nil
}
func (f *fakeBot) SendPhoto(_ context.Context, chatID int64, fileID, caption string, kb *tg.InlineKeyboardMarkup) (int64, error) {
	if err := f.sendErrs[chatID]; err != nil {
		return 0, err
	}
	f.msgIDSeq++
	f.photos = append(f.photos, sentPhoto{ChatID: chatID, FileID: fileID, Caption: caption, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, nil
}
func (f *fakeBot) SendDocument(_ context.Context, chatID int64, fileID, caption string, kb *tg.InlineKeyboardMarkup) (int64, error) {
	if err := f.sendErrs[chatID]; err != nil {
		return 0, err
	}
	f.msgIDSeq++
	f.docs = append(f.docs, sentPhoto{ChatID: chatID, FileID: fileID, Caption: caption, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, nil
}
func (f *fakeBot) SendPhotoUpload(_ context.Context, chatID int64, _ string, _ []byte, caption string, kb *tg.InlineKeyboardMarkup) (int64, string, error) {
	if err := f.sendErrs[chatID]; err != nil {
		return 0, "", err
	}
	f.msgIDSeq++
	f.photoUploads++
	fileID := "uploaded-photo-id"
	f.photos = append(f.photos, sentPhoto{ChatID: chatID, FileID: fileID, Caption: caption, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, fileID, nil
}
func (f *fakeBot) SendDocumentUpload(_ context.Context, chatID int64, _ string, _ []byte, caption string, kb *tg.InlineKeyboardMarkup) (int64, string, error) {
	if err := f.sendErrs[chatID]; err != nil {
		return 0, "", err
	}
	f.msgIDSeq++
	f.documentUploads++
	fileID := "uploaded-document-id"
	f.docs = append(f.docs, sentPhoto{ChatID: chatID, FileID: fileID, Caption: caption, Keyboard: kb, MsgID: f.msgIDSeq})
	return f.msgIDSeq, fileID, nil
}
func (f *fakeBot) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeBot) EditMessageReplyMarkup(_ context.Context, chatID, messageID int64, kb *tg.InlineKeyboardMarkup) error {
	f.edits = append(f.edits, editCall{ChatID: chatID, MessageID: messageID, Keyboard: kb})
	return nil
}
func (f *fakeBot) EditMessageText(_ context.Context, chatID, messageID int64, text string, kb *tg.InlineKeyboardMarkup) error {
	f.edits = append(f.edits, editCall{ChatID: chatID, MessageID: messageID, Text: text, Keyboard: kb})
	return nil
}
func (f *fakeBot) SendInvoice(_ context.Context, chatID int64, title, _, payload string, prices []tg.LabeledPrice) (int64, error) {
	if f.invoiceErr != nil {
		return 0, f.invoiceErr
	}
	f.msgIDSeq++
	f.invoices = append(f.invoices, sentInvoice{ChatID: chatID, Title: title, Payload: payload, Prices: prices, MsgID: f.msgIDSeq})
	return f.msgIDSeq, nil
}
func (f *fakeBot) CreateInvoiceLink(_ context.Context, title, _, payload string, prices []tg.LabeledPrice) (string, error) {
	if f.invoiceErr != nil {
		return "", f.invoiceErr
	}
	f.invoiceLinks = append(f.invoiceLinks, sentInvoice{Title: title, Payload: payload, Prices: prices})
	return "https://t.me/invoice/" + payload, nil
}
func (f *fakeBot) AnswerPreCheckoutQuery(_ context.Context, _ string, ok bool, _ string) error {
	f.precheckoutAck = append(f.precheckoutAck, ok)
	return nil
}

type fakeExtender struct {
	uuid   string
	expire time.Time
	calls  int
	err    error
}

func (f *fakeExtender) ExtendSubscriptionByUUID(_ context.Context, uuid string, newExpireAt time.Time) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.uuid = uuid
	f.expire = newExpireAt
	return nil
}

type fakeFinder struct {
	byTG    map[int64][]Subscriber
	byName  map[string]*Subscriber
	byShort map[string]*Subscriber
	all     []Subscriber
	listErr error
}

func (f *fakeFinder) FindByTelegramID(_ context.Context, id int64) ([]Subscriber, error) {
	return f.byTG[id], nil
}
func (f *fakeFinder) FindByUsername(_ context.Context, name string) (*Subscriber, error) {
	return f.byName[name], nil
}
func (f *fakeFinder) FindByShortUUID(_ context.Context, shortUUID string) (*Subscriber, error) {
	return f.byShort[shortUUID], nil
}
func (f *fakeFinder) ListAll(_ context.Context) ([]Subscriber, error) {
	return f.all, f.listErr
}

type fakeCreator struct {
	created    []string
	squads     [][]string // squad UUIDs passed with each CreateUser call
	strategies []string   // traffic-reset strategy passed with each call
	specs      []CreateUserSpec
}

func (f *fakeCreator) CreateUser(_ context.Context, spec CreateUserSpec) (*CreatedUser, error) {
	f.created = append(f.created, spec.Username)
	f.squads = append(f.squads, spec.SquadUUIDs)
	f.strategies = append(f.strategies, spec.TrafficLimitStrategy)
	f.specs = append(f.specs, spec)
	return &CreatedUser{UUID: "fake-uuid", Username: spec.Username}, nil
}

// fakeUpdater records UpdateUser calls for the "Manage user" flow and the
// confirm path (which extends and stamps the plan through one PATCH). When ext
// is set, expiry patches are mirrored into it — including its injected error —
// so extension assertions and panel-failure simulation keep working against
// the extender fake regardless of which panel call carried the expiry.
type fakeUpdater struct {
	calls []UserPatch
	uuids []string
	err   error
	ext   Extender
}

func (f *fakeUpdater) UpdateUser(ctx context.Context, uuid string, patch UserPatch) error {
	if f.err != nil {
		return f.err
	}
	if f.ext != nil && patch.ExpireAt != nil {
		if err := f.ext.ExtendSubscriptionByUUID(ctx, uuid, *patch.ExpireAt); err != nil {
			return err
		}
	}
	f.uuids = append(f.uuids, uuid)
	f.calls = append(f.calls, patch)
	return nil
}

// fakeSquadLister returns a Default-Squad by default so creation flows that
// rely on the by-name fallback keep working in tests.
type fakeSquadLister struct {
	squads []InternalSquad
	err    error
	calls  int
}

func newFakeSquadLister() *fakeSquadLister {
	return &fakeSquadLister{squads: []InternalSquad{{UUID: "default-squad-uuid", Name: "Default-Squad"}}}
}

func (f *fakeSquadLister) GetInternalSquads(_ context.Context) ([]InternalSquad, error) {
	f.calls++
	return f.squads, f.err
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
	svc := New(st, bot, ext, &fakeCreator{}, &fakeUpdater{ext: ext}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false /*dryRun*/, logger)
	return svc, bot, ext, st
}

func TestPaymentButtonNilWithoutAdmin(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeUpdater{}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), []int64{}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	got, _ := st.GetTariff(ctx, store.PlanStandard, 3)
	if got == nil || got.Price != 450 {
		t.Fatalf("tariff not stored: %+v", got)
	}

	if !svc.HandleAdminCommand(ctx, msg(1000, "/tariffs")) {
		t.Fatal("tariffs should be handled")
	}
	if !svc.HandleAdminCommand(ctx, msg(1000, "/deltariff 3")) {
		t.Fatal("deltariff should be handled")
	}
	if again, _ := st.GetTariff(ctx, store.PlanStandard, 3); again != nil {
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
	_ = st.UpsertTariff(ctx, store.PlanStandard, 1, 150)
	_ = st.UpsertTariff(ctx, store.PlanStandard, 3, 450)

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
	_ = st.UpsertTariff(ctx, store.PlanStandard, 3, 450)

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
	svc, bot, ext, st := newTestService(t)
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

	// The paying user (not just the admin) is told their payment was accepted.
	var userNotified bool
	for _, m := range bot.sent {
		if m.ChatID == 777 && strings.Contains(m.Text, "продлена") {
			userNotified = true
		}
	}
	if !userNotified {
		t.Fatalf("user not notified about acceptance: %+v", bot.sent)
	}

	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("ok:%d", id)))
	if ext.calls != 1 {
		t.Fatalf("second confirm must be a no-op, calls=%d", ext.calls)
	}
}

func TestRejectViaCallbackNotifiesUserAndClearsButtons(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 3, Price: 450, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Status: "pending",
	})
	svc.putAdminMsgs(svc.payMsgs, id, []adminMsgRef{{chatID: 1000, messageID: 5}})

	// Non-admin must not be able to reject.
	if !svc.HandleCallback(ctx, cbq(2222, fmt.Sprintf("rej:%d", id))) {
		t.Fatal("should be handled (denied)")
	}
	req, _ := st.GetPaymentRequest(ctx, id)
	if req.Status != "pending" {
		t.Fatalf("status after non-admin reject = %q, want pending", req.Status)
	}

	if !svc.HandleCallback(ctx, cbq(1000 /*admin*/, fmt.Sprintf("rej:%d", id))) {
		t.Fatal("reject should be handled")
	}
	if ext.calls != 0 {
		t.Fatal("reject must not extend the subscription")
	}
	req, _ = st.GetPaymentRequest(ctx, id)
	if req.Status != "rejected" {
		t.Fatalf("status = %q, want rejected", req.Status)
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected admin buttons cleared, got %+v", bot.edits)
	}
	var userNotified bool
	for _, m := range bot.sent {
		if m.ChatID == 777 && strings.Contains(m.Text, "отклонена") {
			userNotified = true
		}
	}
	if !userNotified {
		t.Fatalf("user not notified about rejection: %+v", bot.sent)
	}

	// Second tap is a no-op.
	svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("rej:%d", id)))
	req, _ = st.GetPaymentRequest(ctx, id)
	if req.Status != "rejected" {
		t.Fatalf("status after second reject = %q, want rejected", req.Status)
	}
}

func TestConfirmExtendFailureNotifiesAdminAndStaysRetryable(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	ext.err = errors.New("panel down")
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 3, Price: 450, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Status: "pending",
	})
	svc.putAdminMsgs(svc.payMsgs, id, []adminMsgRef{{chatID: 1000, messageID: 5}})

	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("ok:%d", id))) {
		t.Fatal("confirm should be handled")
	}

	// Persistent admin message (not just the callback toast).
	var adminWarned bool
	for _, m := range bot.sent {
		if m.ChatID == 1000 && strings.Contains(m.Text, "Не удалось продлить подписку для alice") {
			adminWarned = true
		}
	}
	if !adminWarned {
		t.Fatalf("admin not warned in chat: %+v", bot.sent)
	}
	// Request stays pending with its confirm buttons alive so a retry works.
	req, _ := st.GetPaymentRequest(ctx, id)
	if req.Status != "pending" {
		t.Fatalf("status = %q, want pending", req.Status)
	}
	if len(bot.edits) != 0 {
		t.Fatalf("confirm buttons must not be cleared on failure: %+v", bot.edits)
	}

	// Panel recovers -> the same button confirms successfully.
	ext.err = nil
	if !svc.HandleCallback(ctx, cbq(1000, fmt.Sprintf("ok:%d", id))) {
		t.Fatal("retry confirm should be handled")
	}
	req, _ = st.GetPaymentRequest(ctx, id)
	if req.Status != "confirmed" {
		t.Fatalf("status after retry = %q, want confirmed", req.Status)
	}
	if len(bot.edits) == 0 {
		t.Fatal("confirm buttons should be cleared after success")
	}
}

func TestAdminStatsCommand(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.finder = &fakeFinder{all: []Subscriber{
		{RemnawaveID: 1, Username: "a", Status: "ACTIVE", ExpireAt: now.Add(3 * 24 * time.Hour), TelegramID: 11},
		{RemnawaveID: 2, Username: "b", Status: "ACTIVE", ExpireAt: now.Add(30 * 24 * time.Hour)},
		{RemnawaveID: 3, Username: "c", Status: "EXPIRED", ExpireAt: now.Add(-24 * time.Hour), TelegramID: 33},
	}}
	_, _ = st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 1, UUID: "u", Username: "a", TelegramID: 11,
		Months: 1, Price: 150, ExpireAt: now, Status: "pending",
	})

	if svc.HandleAdminCommand(ctx, msg(2222, "/stats")) {
		t.Fatal("non-admin /stats must not be handled")
	}
	if !svc.HandleAdminCommand(ctx, msg(1000, "/stats")) {
		t.Fatal("/stats should be handled for admin")
	}
	report := bot.sent[len(bot.sent)-1].Text
	for _, want := range []string{
		"всего: 3", "активных: 2", "истекают в ближайшие 7 дней: 1",
		"истекших: 1", "с привязанным Telegram: 2", "ожидают подтверждения: 1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

// TestAdminMenuLocalizedEN spot-checks that a payments flow replies in
// English when BOT_LANG=en is active.
func TestAdminMenuLocalizedEN(t *testing.T) {
	i18n.SetLang(i18n.EN)
	t.Cleanup(func() { i18n.SetLang(i18n.RU) })

	svc, bot, _, _ := newTestService(t)
	svc.SendAdminMenu(context.Background(), 1000)
	if len(bot.sent) != 1 || bot.sent[0].Text != "Admin menu" {
		t.Fatalf("expected English admin menu, got %+v", bot.sent)
	}
	if got := bot.sent[0].Keyboard.InlineKeyboard[0][0].Text; got != "📊 Statistics" {
		t.Fatalf("first button = %q, want 📊 Statistics", got)
	}
}

func TestAdminMsgTTLEviction(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	svc.now = func() time.Time { return base }
	svc.putAdminMsgs(svc.payMsgs, 1, []adminMsgRef{{chatID: 1000, messageID: 5}})

	svc.now = func() time.Time { return base.Add(adminMsgTTL + time.Hour) }
	svc.putAdminMsgs(svc.payMsgs, 2, []adminMsgRef{{chatID: 1000, messageID: 6}})

	svc.mu.Lock()
	_, oldKept := svc.payMsgs[1]
	_, newKept := svc.payMsgs[2]
	svc.mu.Unlock()
	if oldKept || !newKept {
		t.Fatalf("eviction wrong: old=%v new=%v", oldKept, newKept)
	}
	// Clearing the evicted request is a harmless no-op.
	svc.clearPayButtons(context.Background(), 1)
	if len(bot.edits) != 0 {
		t.Fatalf("no edits expected for evicted entry: %+v", bot.edits)
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
	svc := New(st, bot, ext, &fakeCreator{}, &fakeUpdater{ext: ext}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000, 2000}, "₽", false, logger)
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

func TestMenuFlowsInertWhenDisabled(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	bot := &fakeBot{}
	// Finder would return a subscriber, so the flow would proceed if not gated.
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{RemnawaveID: 1, UUID: "u-1", Username: "sub", TelegramID: 555}},
	}}
	svc := New(st, bot, &fakeExtender{}, &fakeCreator{}, &fakeUpdater{}, finder, &fakeRegistrar{}, newFakeSquadLister(), []int64{}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	// Menu-button entry points must be inert when no admins are configured.
	svc.beginGiftCodeFlow(ctx, 555)
	if svc.getGiftCode(555) != nil {
		t.Fatal("gift flow must not start when disabled")
	}
	svc.beginInviteFlow(ctx, 555)
	if svc.getInvite(555) != nil {
		t.Fatal("invite flow must not start when disabled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("no messages should be sent when disabled: %+v", bot.sent)
	}
}

func TestInviteApproveClearsBothAdminButtons(t *testing.T) {
	svc, bot, _, _ := newTestServiceTwoAdmins(t)
	ctx := context.Background()

	// Seed an active invite ready to submit, in the inviter's chat (id 300).
	svc.setInvite(300, &inviteState{
		inviterName: "payer",
		inviterTGID: 300,
		newUsername: "newbie",
		price:       0,
		createdAt:   svc.now(),
	})

	// Submit -> notifies both admins and records inviteMsgs refs.
	submitCb := &tg.CallbackQuery{ID: "c1", From: tg.User{ID: 300},
		Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: 300}}, Data: "inv_submit"}
	if !svc.HandleCallback(ctx, submitCb) {
		t.Fatal("inv_submit should be handled")
	}
	bot.edits = nil

	// Approve from admin 1000 -> clears the button on both admins' copies.
	approveCb := &tg.CallbackQuery{ID: "c2", From: tg.User{ID: 1000},
		Message: &tg.Message{MessageID: 10, Chat: tg.Chat{ID: 1000}}, Data: "inv_ok:1"}
	if !svc.HandleCallback(ctx, approveCb) {
		t.Fatal("inv_ok should be handled")
	}

	cleared := map[int64]bool{}
	for _, e := range bot.edits {
		if e.Keyboard == nil {
			cleared[e.ChatID] = true
		}
	}
	if !cleared[1000] || !cleared[2000] {
		t.Fatalf("expected invite buttons cleared for both admins, edits: %+v", bot.edits)
	}
}
