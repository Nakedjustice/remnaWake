package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
