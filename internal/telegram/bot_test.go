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
