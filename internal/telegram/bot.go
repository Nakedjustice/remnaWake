package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/textutil"
)

type Bot struct {
	token     string
	apiBase   string
	parseMode string
	http      *http.Client
}

type sendMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
	Message       *Message       `json:"message,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type getUpdatesResponse struct {
	apiResponse
	Result []Update `json:"result,omitempty"`
}

func NewBot(token, parseMode string, timeout time.Duration) *Bot {
	return &Bot{
		token:     token,
		apiBase:   "https://api.telegram.org/bot" + token,
		parseMode: parseMode,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (b *Bot) Send(ctx context.Context, chatID int64, text string) error {
	return b.SendWithKeyboard(ctx, chatID, text, nil)
}

func (b *Bot) SendPlain(ctx context.Context, chatID int64, text string) error {
	payload := sendMessageRequest{
		ChatID: chatID,
		Text:   text,
	}
	return b.sendMessage(ctx, payload)
}

func (b *Bot) SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) error {
	payload := sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	}
	return b.sendMessage(ctx, payload)
}

func (b *Bot) SendWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) error {
	payload := sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   b.parseMode,
		ReplyMarkup: keyboard,
	}
	return b.sendMessage(ctx, payload)
}

func (b *Bot) SendWelcome(ctx context.Context, chatID int64) error {
	text := `⏰ Привет! Я бот-напоминалка: если ваша подписка на КВН скоро закончится, я сообщу об этом заранее — за 7, 3 или 1 день до окончания.

Также при нажатии кнопки "Я оплатил" администратор получит уведомление.`

	return b.SendPlain(ctx, chatID, text)
}

func (b *Bot) sendMessage(ctx context.Context, payload sendMessageRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := b.apiBase + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		var ar apiResponse
		if err := json.Unmarshal(raw, &ar); err == nil && ar.Parameters != nil && ar.Parameters.RetryAfter > 0 {
			return fmt.Errorf("telegram rate limited, retry_after=%ds", ar.Parameters.RetryAfter)
		}
		return fmt.Errorf("telegram rate limited, status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram send failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram send not ok: %s", ar.Description)
	}
	return nil
}

func (b *Bot) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	payload := getUpdatesRequest{
		Offset:         offset,
		Timeout:        timeout,
		AllowedUpdates: []string{"callback_query", "message"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := b.apiBase + "/getUpdates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram get updates: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram get updates failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar getUpdatesResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("telegram get updates decode: %w", err)
	}
	if !ar.OK {
		return nil, fmt.Errorf("telegram get updates not ok: %s", ar.Description)
	}
	return ar.Result, nil
}

func (b *Bot) AnswerCallbackQuery(ctx context.Context, callbackQueryID, text string) error {
	payload := answerCallbackQueryRequest{
		CallbackQueryID: callbackQueryID,
		Text:            text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := b.apiBase + "/answerCallbackQuery"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram answer callback query: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram answer callback query failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram answer callback query not ok: %s", ar.Description)
	}
	return nil
}

func (b *Bot) EndpointHost() string {
	u, _ := url.Parse(b.apiBase)
	return u.Host
}

func ParseChatID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
