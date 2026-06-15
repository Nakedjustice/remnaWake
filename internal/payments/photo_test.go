package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func photoMsg(chatID int64, fileIDs ...string) *tg.Message {
	m := &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}}
	for _, id := range fileIDs {
		m.Photo = append(m.Photo, tg.PhotoSize{FileID: id})
	}
	return m
}

func docMsg(chatID int64, fileID, mime, name string) *tg.Message {
	return &tg.Message{
		MessageID: 1,
		Chat:      tg.Chat{ID: chatID},
		Document:  &tg.Document{FileID: fileID, MimeType: mime, FileName: name},
	}
}

func enableScreenshot(t *testing.T, svc *Service) {
	t.Helper()
	if err := svc.setRequireScreenshot(context.Background(), true); err != nil {
		t.Fatalf("enable screenshot: %v", err)
	}
}

func TestPickWithScreenshotDefersRequest(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 3, 450)
	enableScreenshot(t, svc)

	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3")) {
		t.Fatal("pick should be handled")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("request must not be created before the photo: %+v", req)
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected keyboard cleared, got %+v", bot.edits)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("awaiting-photo state not set")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "чека") {
		t.Fatalf("expected screenshot prompt: %+v", bot.sent)
	}
	for _, m := range bot.sent {
		if m.ChatID == 1000 {
			t.Fatalf("admin must not be notified before the photo: %+v", m)
		}
	}
}

func TestTextWhileAwaitingPhotoReminds(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil

	if !svc.HandleText(ctx, msg(777, "вот оплата")) {
		t.Fatal("text should be consumed by the reminder")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "фото") {
		t.Fatalf("expected photo reminder: %+v", bot.sent)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("state must survive a text reminder")
	}
}

func TestCancelClearsAwaitingPhoto(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)

	if !svc.HandleText(ctx, msg(777, "/cancel")) {
		t.Fatal("/cancel should be handled")
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared by /cancel")
	}
	if bot.sent[len(bot.sent)-1].Text != "Отменено." {
		t.Fatalf("expected cancel confirmation: %+v", bot.sent)
	}
}

func TestPhotoCreatesRequestWithScreenshot(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil

	if !svc.HandlePhoto(ctx, photoMsg(777, "small-id", "big-id")) {
		t.Fatal("photo should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.Months != 3 || req.Price != 450 || req.ScreenshotFileID != "big-id" || req.ScreenshotIsDocument {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.photos) != 1 {
		t.Fatalf("expected one admin photo, got %+v", bot.photos)
	}
	p := bot.photos[0]
	if p.ChatID != 1000 || p.FileID != "big-id" {
		t.Fatalf("admin photo wrong: %+v", p)
	}
	if !strings.Contains(p.Caption, "alice") {
		t.Fatalf("caption should carry the request text: %q", p.Caption)
	}
	if p.Keyboard == nil || p.Keyboard.InlineKeyboard[0][0].CallbackData != "ok:1" {
		t.Fatalf("expected confirm button on the photo: %+v", p.Keyboard)
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared after the photo")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "отправлена администратору") {
		t.Fatalf("expected user confirmation: %+v", bot.sent)
	}
}

func TestPdfDocumentCreatesRequestWithScreenshot(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil

	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("pdf document should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.Months != 3 || req.ScreenshotFileID != "pdf-id" || !req.ScreenshotIsDocument {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.docs) != 1 {
		t.Fatalf("expected one admin document, got %+v", bot.docs)
	}
	d := bot.docs[0]
	if d.ChatID != 1000 || d.FileID != "pdf-id" {
		t.Fatalf("admin document wrong: %+v", d)
	}
	if !strings.Contains(d.Caption, "alice") {
		t.Fatalf("caption should carry the request text: %q", d.Caption)
	}
	if d.Keyboard == nil || d.Keyboard.InlineKeyboard[0][0].CallbackData != "ok:1" {
		t.Fatalf("expected confirm button on the document: %+v", d.Keyboard)
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared after the document")
	}
	if len(bot.photos) != 0 {
		t.Fatalf("no photo message expected for a pdf: %+v", bot.photos)
	}
}

func TestPdfByFilenameAccepted(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)

	// Some banks send PDFs with a generic octet-stream mime type.
	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/octet-stream", "Чек.PDF")) {
		t.Fatal("pdf-by-filename should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "pdf-id" {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.docs) != 1 {
		t.Fatalf("expected one admin document, got %+v", bot.docs)
	}
}

func TestNonPdfDocumentReminds(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil

	if !svc.HandleDocument(ctx, docMsg(777, "zip-id", "application/zip", "archive.zip")) {
		t.Fatal("non-pdf document should be consumed with a reminder")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("no request should be created for a non-pdf: %+v", req)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("state must survive a non-pdf document")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "PDF") {
		t.Fatalf("expected pdf reminder: %+v", bot.sent)
	}
	if len(bot.docs) != 0 {
		t.Fatalf("admin must not receive the rejected file: %+v", bot.docs)
	}
}

func TestDocumentIgnoredWithoutState(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if svc.HandleDocument(context.Background(), docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("document without state must be ignored")
	}
	if len(bot.sent) != 0 || len(bot.docs) != 0 {
		t.Fatalf("nothing should be sent: %+v %+v", bot.sent, bot.docs)
	}
}

func TestConfirmClearsDocumentMessageButtons(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("pdf document should be handled")
	}
	docMsgID := bot.docs[0].MsgID

	if !svc.HandleCallback(ctx, cbq(1000, "ok:1")) {
		t.Fatal("confirm should be handled")
	}
	if ext.calls != 1 {
		t.Fatalf("subscription not extended: %d calls", ext.calls)
	}
	var cleared bool
	for _, e := range bot.edits {
		if e.ChatID == 1000 && e.MessageID == docMsgID && e.Keyboard == nil {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("document message buttons not cleared: %+v", bot.edits)
	}
}

func TestConfirmClearsPhotoMessageButtons(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	if !svc.HandlePhoto(ctx, photoMsg(777, "big-id")) {
		t.Fatal("photo should be handled")
	}
	photoMsgID := bot.photos[0].MsgID

	if !svc.HandleCallback(ctx, cbq(1000, "ok:1")) {
		t.Fatal("confirm should be handled")
	}
	if ext.calls != 1 {
		t.Fatalf("subscription not extended: %d calls", ext.calls)
	}
	var cleared bool
	for _, e := range bot.edits {
		if e.ChatID == 1000 && e.MessageID == photoMsgID && e.Keyboard == nil {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("photo message buttons not cleared: %+v", bot.edits)
	}
}

func TestPhotoIgnoredWithoutState(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)

	if svc.HandlePhoto(ctx, photoMsg(777, "big-id")) {
		t.Fatal("photo without state must be ignored")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("nothing should be sent: %+v", bot.sent)
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("no request should exist: %+v", req)
	}
}

func TestPhotoAfterTTLGetsExpiryNotice(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil
	svc.now = func() time.Time { return time.Now().Add(payPhotoTTL + time.Minute) }

	if !svc.HandlePhoto(ctx, photoMsg(777, "big-id")) {
		t.Fatal("expired photo must be consumed with an expiry notice")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("no request should exist: %+v", req)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "истек") {
		t.Fatalf("expected expiry notice: %+v", bot.sent)
	}
	if len(bot.photos) != 0 {
		t.Fatalf("admin must not be notified: %+v", bot.photos)
	}

	// The expired state was evicted with the notice; the next photo is a
	// plain unrelated photo again.
	bot.sent = nil
	if svc.HandlePhoto(ctx, photoMsg(777, "big-id")) {
		t.Fatal("photo after the notice must be ignored")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("no second notice expected: %+v", bot.sent)
	}
}

func TestDocumentAfterTTLGetsExpiryNotice(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil
	svc.now = func() time.Time { return time.Now().Add(payPhotoTTL + time.Minute) }

	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("expired document must be consumed with an expiry notice")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("no request should exist: %+v", req)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "истек") {
		t.Fatalf("expected expiry notice: %+v", bot.sent)
	}
	if len(bot.docs) != 0 {
		t.Fatalf("admin must not be notified: %+v", bot.docs)
	}
}

func TestImageDocumentAccepted(t *testing.T) {
	// Telegram's "send without compression" delivers screenshots as documents
	// with an image MIME type; they must be accepted like PDFs.
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)

	if !svc.HandleDocument(ctx, docMsg(777, "img-id", "image/png", "screenshot.png")) {
		t.Fatal("image document should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "img-id" {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.docs) != 1 || bot.docs[0].FileID != "img-id" {
		t.Fatalf("expected one admin document, got %+v", bot.docs)
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared after the image document")
	}
}

func TestImageByFilenameAccepted(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)

	if !svc.HandleDocument(ctx, docMsg(777, "img-id", "application/octet-stream", "IMG_1234.JPG")) {
		t.Fatal("image-by-filename should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "img-id" {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.docs) != 1 {
		t.Fatalf("expected one admin document, got %+v", bot.docs)
	}
}

func TestCancelCaptionAbortsReceipt(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)

	// A photo captioned /cancel aborts instead of filing the request.
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil
	m := photoMsg(777, "big-id")
	m.Caption = "/cancel"
	if !svc.HandlePhoto(ctx, m) {
		t.Fatal("captioned photo should be consumed")
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared by the /cancel caption")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("no request should be created: %+v", req)
	}
	if len(bot.sent) != 1 || bot.sent[0].Text != "Отменено." {
		t.Fatalf("expected cancel confirmation: %+v", bot.sent)
	}

	// Same for a document, even a non-PDF one.
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil
	d := docMsg(777, "zip-id", "application/zip", "oops.zip")
	d.Caption = " /cancel "
	if !svc.HandleDocument(ctx, d) {
		t.Fatal("captioned document should be consumed")
	}
	if svc.getPayPhoto(777) != nil {
		t.Fatal("state should be cleared by the /cancel caption")
	}
	if len(bot.sent) != 1 || bot.sent[0].Text != "Отменено." {
		t.Fatalf("expected cancel confirmation: %+v", bot.sent)
	}
}

func TestReceiptCaptionForwardedToAdmins(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)

	d := docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")
	d.Caption = "оплатил с карты жены"
	if !svc.HandleDocument(ctx, d) {
		t.Fatal("pdf document should be handled")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req == nil {
		t.Fatal("request should be created")
	}
	if len(bot.docs) != 1 || !strings.Contains(bot.docs[0].Caption, "оплатил с карты жены") {
		t.Fatalf("admin caption should carry the user's note: %+v", bot.docs)
	}
}

func TestPickWithNilMessageStillRequiresReceipt(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 3, 450)
	enableScreenshot(t, svc)

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 777}, Data: "pick:42:3"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("pick should be handled")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("the receipt requirement must not be bypassed: %+v", req)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("awaiting-receipt state should be keyed by the user's ID")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "чека") {
		t.Fatalf("expected receipt prompt: %+v", bot.sent)
	}
}

func TestMidFlowInputBeatsPayPhotoReminder(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	svc.setRedeem(777, &redeemState{giftID: 1, months: 1, awaitingUsername: true, createdAt: time.Now()})
	bot.sent = nil

	if !svc.HandleText(ctx, msg(777, "ab")) {
		t.Fatal("text should be consumed")
	}
	if len(bot.sent) != 1 || strings.Contains(bot.sent[0].Text, "чека") {
		t.Fatalf("the redeem flow should answer, not the receipt reminder: %+v", bot.sent)
	}
	if svc.getRedeem(777) == nil {
		t.Fatal("redeem state must survive")
	}
}

func TestAllAdminNotifyFailedReportsError(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	enableScreenshot(t, svc)
	svc.startPayPhotoFlow(ctx, 777, 42, 3, 450)
	bot.sent = nil
	bot.sendErrs = map[int64]error{1000: errors.New("telegram down")}

	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("document should be consumed")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "Ошибка") {
		t.Fatalf("user must get an error, not a success confirmation: %+v", bot.sent)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("state must survive so the user can resend the receipt")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("the unnotified request must be withdrawn: %+v", req)
	}

	// Once Telegram recovers, resending the same file succeeds.
	bot.sendErrs = nil
	bot.sent = nil
	if !svc.HandleDocument(ctx, docMsg(777, "pdf-id", "application/pdf", "receipt.pdf")) {
		t.Fatal("retry should be handled")
	}
	if len(bot.docs) != 1 {
		t.Fatalf("expected one admin document on retry, got %+v", bot.docs)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "отправлена администратору") {
		t.Fatalf("expected user confirmation on retry: %+v", bot.sent)
	}
}

func TestPickAllAdminNotifyFailedAnswersError(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 3, 450)
	bot.sendErrs = map[int64]error{1000: errors.New("telegram down")}

	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3")) {
		t.Fatal("pick should be handled")
	}
	if len(bot.answers) == 0 || !strings.Contains(bot.answers[len(bot.answers)-1], "Ошибка") {
		t.Fatalf("expected an error answer: %+v", bot.answers)
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("the unnotified request must be withdrawn: %+v", req)
	}
}

func TestPickWithoutScreenshotUnchanged(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	_ = st.UpsertTariff(ctx, 3, 450)

	if !svc.HandleCallback(ctx, cbq(777, "pick:42:3")) {
		t.Fatal("pick should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "" {
		t.Fatalf("request wrong: %+v", req)
	}
	if len(bot.photos) != 0 {
		t.Fatalf("no photos expected with the toggle off: %+v", bot.photos)
	}
}

func TestAdminScreenshotTogglePersists(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	if !svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:shot_toggle"}) {
		t.Fatal("adm:shot_toggle should be handled")
	}
	if !svc.getRequireScreenshot() {
		t.Fatal("toggle should be on after the first flip")
	}
	if len(bot.sent) < 2 || bot.sent[len(bot.sent)-1].Keyboard == nil {
		t.Fatalf("expected confirmation + re-rendered menu: %+v", bot.sent)
	}

	// A fresh service over the same store loads the persisted value.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc2 := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeUpdater{}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	if !svc2.getRequireScreenshot() {
		t.Fatal("setting should persist across restarts")
	}

	if !svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb2", From: tg.User{ID: 1000}, Data: "adm:shot_toggle"}) {
		t.Fatal("second toggle should be handled")
	}
	if svc.getRequireScreenshot() {
		t.Fatal("toggle should be off after the second flip")
	}
}

func TestScreenshotToggleIgnoresNonAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	if !svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 9999}, Data: "adm:shot_toggle"}) {
		t.Fatal("callback should be handled (rejected)")
	}
	if svc.getRequireScreenshot() {
		t.Fatal("non-admin must not flip the toggle")
	}
	if err := svc.AdminSetRequireScreenshot(ctx, 9999, true); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("expected ErrNotAdmin, got %v", err)
	}
}

func TestWebRenewWithScreenshotRequired(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		777: {{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
			ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, &fakeExtender{}, &fakeCreator{}, &fakeUpdater{}, finder, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	ctx := context.Background()
	enableScreenshot(t, svc)

	_, err = svc.CreateRenewRequest(ctx, 777, 42, 1, "")
	if !errors.Is(err, ErrScreenshotRequired) {
		t.Fatalf("expected ErrScreenshotRequired, got %v", err)
	}
	if svc.getPayPhoto(777) == nil {
		t.Fatal("awaiting-photo state not set for the webapp renew")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "чека") {
		t.Fatalf("expected bot DM asking for the screenshot: %+v", bot.sent)
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("request must not be created yet: %+v", req)
	}

	// The deferred request completes through the same photo handler.
	if !svc.HandlePhoto(ctx, photoMsg(777, "web-shot")) {
		t.Fatal("photo should be handled")
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "web-shot" || req.Months != 1 {
		t.Fatalf("request wrong: %+v", req)
	}
}
