package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
)

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

func TestMultipartReceiptUploadsReturnReusableFileIDs(t *testing.T) {
	for _, tc := range []struct {
		method, field string
		call          func(*Bot) (int64, string, error)
		result        string
	}{
		{"/sendPhoto", "photo", func(b *Bot) (int64, string, error) {
			return b.SendPhotoUpload(context.Background(), 100, "r.png", []byte("png"), "paid", nil)
		}, `{"ok":true,"result":{"message_id":42,"photo":[{"file_id":"small"},{"file_id":"photo-file"}]}}`},
		{"/sendDocument", "document", func(b *Bot) (int64, string, error) {
			return b.SendDocumentUpload(context.Background(), 100, "r.pdf", []byte("pdf"), "paid", nil)
		}, `{"ok":true,"result":{"message_id":43,"document":{"file_id":"document-file"}}}`},
	} {
		t.Run(tc.field, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.method {
					t.Errorf("path=%s", r.URL.Path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Errorf("multipart: %v", err)
				}
				if r.FormValue("chat_id") != "100" || r.FormValue("caption") != "paid" {
					t.Errorf("form=%v", r.Form)
				}
				f, _, err := r.FormFile(tc.field)
				if err != nil {
					t.Errorf("file: %v", err)
				} else {
					_ = f.Close()
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.result))
			}))
			defer srv.Close()
			b := NewBot("token", "", time.Second)
			b.apiBase = srv.URL
			msgID, fileID, err := tc.call(b)
			if err != nil || msgID == 0 || !strings.HasSuffix(fileID, "-file") {
				t.Fatalf("msg=%d file=%q err=%v", msgID, fileID, err)
			}
		})
	}
}

func TestSendMessageRetriesAfterRateLimit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11,"chat":{"id":100,"type":"private"},"text":"hi"}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL
	var waited time.Duration
	b.wait = func(ctx context.Context, d time.Duration) error {
		waited += d
		return nil
	}

	msgID, err := b.SendPlainWithKeyboard(context.Background(), 100, "hi", nil)
	if err != nil {
		t.Fatalf("send after retry: %v", err)
	}
	if msgID != 11 {
		t.Fatalf("msgID = %d, want 11", msgID)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if waited != 7*time.Second {
		t.Fatalf("waited = %v, want 7s", waited)
	}
}

func TestSendMessageGivesUpAfterMaxAttempts(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL
	b.wait = func(ctx context.Context, d time.Duration) error { return nil }

	if _, err := b.SendPlainWithKeyboard(context.Background(), 100, "hi", nil); err == nil {
		t.Fatal("expected rate-limit error")
	}
	if calls != sendMaxAttempts {
		t.Fatalf("calls = %d, want %d", calls, sendMaxAttempts)
	}
}

func TestSendMessageStopsWhenContextCancelledDuringWait(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":5}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	b.wait = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}

	_, err := b.SendPlainWithKeyboard(ctx, 100, "hi", nil)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after cancel)", calls)
	}
}

// TestSendWelcomeLocalizedEN also guards that the welcome text in bot.go and
// the dictionary key in i18n/en.go stay byte-identical — a mismatch would
// silently fall back to Russian.
func TestSendWelcomeLocalizedEN(t *testing.T) {
	i18n.SetLang(i18n.EN)
	t.Cleanup(func() { i18n.SetLang(i18n.RU) })

	var body map[string]any
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rawBody = string(raw)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL
	if err := b.SendWelcome(context.Background(), 1, true); err != nil {
		t.Fatalf("SendWelcome: %v", err)
	}
	text, _ := body["text"].(string)
	if !strings.Contains(text, "Link account") || !strings.Contains(text, "I paid") {
		t.Fatalf("welcome not translated (dictionary key mismatch?): %q", text)
	}
	// With the trial enabled both the trial and register buttons are offered.
	if !strings.Contains(rawBody, "menu:trial") || !strings.Contains(rawBody, "menu:register") {
		t.Fatalf("welcome keyboard missing trial/register buttons: %q", rawBody)
	}

	// With the trial disabled only the register button is shown.
	if err := b.SendWelcome(context.Background(), 1, false); err != nil {
		t.Fatalf("SendWelcome: %v", err)
	}
	if strings.Contains(rawBody, "menu:trial") {
		t.Fatalf("welcome keyboard should omit trial button when disabled: %q", rawBody)
	}
	if !strings.Contains(rawBody, "menu:register") {
		t.Fatalf("welcome keyboard missing register button: %q", rawBody)
	}

	if IsCabinetButton("👤 Личный кабинет") != true || IsCabinetButton("👤 My account") != true {
		t.Fatal("IsCabinetButton must match both the RU source and the localized label")
	}
}

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

func TestSetMyCommandsPostsCommandList(t *testing.T) {
	var gotPath string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL

	cmds := []BotCommand{
		{Command: "gift", Description: "Подарить подписку"},
		{Command: "menu", Description: "Меню"},
	}
	if err := b.SetMyCommands(context.Background(), cmds); err != nil {
		t.Fatalf("set: %v", err)
	}
	if gotPath != "/setMyCommands" {
		t.Fatalf("path = %q", gotPath)
	}
	list, ok := body["commands"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("commands wrong: %v", body)
	}
	first := list[0].(map[string]any)
	if first["command"] != "gift" || first["description"] != "Подарить подписку" {
		t.Fatalf("first command wrong: %v", first)
	}
}

func TestSetMyCommandsForChatSendsCorrectScope(t *testing.T) {
	var gotPath string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	b := NewBot("token", "", time.Second)
	b.apiBase = srv.URL

	cmds := []BotCommand{{Command: "admin", Description: "Panel"}}
	if err := b.SetMyCommandsForChat(context.Background(), 9999, cmds); err != nil {
		t.Fatalf("SetMyCommandsForChat: %v", err)
	}
	if gotPath != "/setMyCommands" {
		t.Fatalf("path = %q", gotPath)
	}
	scope, ok := body["scope"].(map[string]any)
	if !ok {
		t.Fatalf("scope missing: %v", body)
	}
	if scope["type"] != "chat" {
		t.Fatalf("scope type = %q, want chat", scope["type"])
	}
	if scope["chat_id"].(float64) != 9999 {
		t.Fatalf("chat_id = %v", scope["chat_id"])
	}
}
