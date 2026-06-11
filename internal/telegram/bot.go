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

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/textutil"
)

type Bot struct {
	token     string
	apiBase   string
	parseMode string
	http      *http.Client
	wait      func(ctx context.Context, d time.Duration) error
}

type sendMessageRequest struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type editMessageReplyMarkupRequest struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type editMessageTextRequest struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	Text        string                `json:"text"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type sendPhotoRequest struct {
	ChatID      int64  `json:"chat_id"`
	Photo       string `json:"photo"` // file_id of a photo already on Telegram servers
	Caption     string `json:"caption,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
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
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

// WebAppInfo points an inline button or the chat menu button at a Telegram
// Mini App URL (must be HTTPS).
type WebAppInfo struct {
	URL string `json:"url"`
}

// ReplyKeyboardMarkup is a persistent keyboard shown under the input field.
type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
	IsPersistent   bool               `json:"is_persistent,omitempty"`
}

type KeyboardButton struct {
	Text string `json:"text"`
}

// cabinetButtonLabelRU is the Russian source text of the cabinet reply button.
const cabinetButtonLabelRU = "👤 Личный кабинет"

// CabinetButtonLabel returns the localized reply-keyboard button text that
// opens the personal cabinet.
func CabinetButtonLabel() string { return i18n.T(cabinetButtonLabelRU) }

// IsCabinetButton reports whether an incoming message presses the cabinet
// reply button. It matches the Russian source label as well as the localized
// one, so keyboards installed before a language switch keep working.
func IsCabinetButton(text string) bool {
	return text == cabinetButtonLabelRU || text == CabinetButtonLabel()
}

// MainReplyKeyboard returns the persistent reply keyboard with the cabinet button.
func MainReplyKeyboard() *ReplyKeyboardMarkup {
	return &ReplyKeyboardMarkup{
		Keyboard:       [][]KeyboardButton{{{Text: CabinetButtonLabel()}}},
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
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
	// Photo holds the available sizes of an attached photo, smallest first
	// (Telegram orders them ascending — the last entry is the largest).
	Photo   []PhotoSize `json:"photo,omitempty"`
	Caption string      `json:"caption,omitempty"`
}

// PhotoSize is one resolution variant of a photo attached to a message.
type PhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
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

type sendMessageResponse struct {
	apiResponse
	Result Message `json:"result"`
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
		wait: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
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
	_, err := b.sendMessage(ctx, payload)
	return err
}

func (b *Bot) SendPlainWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) (int64, error) {
	payload := sendMessageRequest{
		ChatID: chatID,
		Text:   text,
	}
	if keyboard != nil {
		payload.ReplyMarkup = keyboard
	}
	return b.sendMessage(ctx, payload)
}

// SendPlainWithReplyKeyboard sends a plain message carrying a persistent reply
// keyboard (the buttons shown under the input field).
func (b *Bot) SendPlainWithReplyKeyboard(ctx context.Context, chatID int64, text string, keyboard *ReplyKeyboardMarkup) error {
	payload := sendMessageRequest{
		ChatID: chatID,
		Text:   text,
	}
	if keyboard != nil {
		payload.ReplyMarkup = keyboard
	}
	_, err := b.sendMessage(ctx, payload)
	return err
}

func (b *Bot) SendWithKeyboard(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboardMarkup) error {
	payload := sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: b.parseMode,
	}
	if keyboard != nil {
		payload.ReplyMarkup = keyboard
	}
	_, err := b.sendMessage(ctx, payload)
	return err
}

func (b *Bot) SendWelcome(ctx context.Context, chatID int64) error {
	text := i18n.T(i18n.WelcomeRU)

	keyboard := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: i18n.T("🔗 Привязать аккаунт"), CallbackData: "menu:register"}},
		},
	}

	_, err := b.SendPlainWithKeyboard(ctx, chatID, text, keyboard)
	return err
}

const (
	// sendMaxAttempts bounds 429 retries per message; other errors fail fast.
	sendMaxAttempts = 3
	// sendRetryAfterCap limits how long one retry_after wait may block a send.
	sendRetryAfterCap = 30 * time.Second
)

func (b *Bot) sendMessage(ctx context.Context, payload sendMessageRequest) (int64, error) {
	return b.sendWithRetry(ctx, "sendMessage", payload)
}

// SendPhoto sends a photo already stored on Telegram servers (by file_id) with
// an optional caption and inline keyboard, returning the new message ID.
func (b *Bot) SendPhoto(ctx context.Context, chatID int64, fileID, caption string, keyboard *InlineKeyboardMarkup) (int64, error) {
	payload := sendPhotoRequest{
		ChatID:  chatID,
		Photo:   fileID,
		Caption: caption,
	}
	if keyboard != nil {
		payload.ReplyMarkup = keyboard
	}
	return b.sendWithRetry(ctx, "sendPhoto", payload)
}

// sendWithRetry posts payload to the given API method, waiting out 429s like
// sendMessage always has.
func (b *Bot) sendWithRetry(ctx context.Context, method string, payload any) (int64, error) {
	for attempt := 1; ; attempt++ {
		msgID, retryAfter, err := b.doSend(ctx, method, payload)
		if err == nil {
			return msgID, nil
		}
		if retryAfter <= 0 || attempt >= sendMaxAttempts {
			return 0, err
		}
		if retryAfter > sendRetryAfterCap {
			retryAfter = sendRetryAfterCap
		}
		if werr := b.wait(ctx, retryAfter); werr != nil {
			return 0, werr
		}
	}
}

// doSend performs a single message-producing API call. A positive retryAfter
// signals a 429 the caller may wait out and retry.
func (b *Bot) doSend(ctx context.Context, method string, payload any) (int64, time.Duration, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, 0, err
	}

	endpoint := b.apiBase + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		var ar apiResponse
		if err := json.Unmarshal(raw, &ar); err == nil && ar.Parameters != nil && ar.Parameters.RetryAfter > 0 {
			return 0, time.Duration(ar.Parameters.RetryAfter) * time.Second,
				fmt.Errorf("telegram rate limited, retry_after=%ds", ar.Parameters.RetryAfter)
		}
		return 0, 0, fmt.Errorf("telegram rate limited, status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("telegram send failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar sendMessageResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return 0, 0, fmt.Errorf("telegram send decode: %w", err)
	}
	if !ar.OK {
		return 0, 0, fmt.Errorf("telegram send not ok: %s", ar.Description)
	}
	return ar.Result.MessageID, 0, nil
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

func (b *Bot) EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, keyboard *InlineKeyboardMarkup) error {
	payload := editMessageReplyMarkupRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		ReplyMarkup: keyboard,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/editMessageReplyMarkup", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram edit reply markup: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram edit reply markup failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram edit reply markup not ok: %s", ar.Description)
	}
	return nil
}

// EditMessageText replaces both the text and the inline keyboard of an
// existing message (keyboard nil = remove buttons).
func (b *Bot) EditMessageText(ctx context.Context, chatID, messageID int64, text string, keyboard *InlineKeyboardMarkup) error {
	payload := editMessageTextRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/editMessageText", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram edit message text: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram edit message text failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram edit message text not ok: %s", ar.Description)
	}
	return nil
}

// BotCommand is one entry in the bot's command menu (the "Menu" button and the
// "/" autocomplete list).
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands registers the bot's command menu via the setMyCommands API.
func (b *Bot) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	payload := map[string]any{"commands": commands}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/setMyCommands", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram set my commands: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram set my commands failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram set my commands not ok: %s", ar.Description)
	}
	return nil
}

// SetMyCommandsForChat registers the bot's command menu for a specific chat via the setMyCommands API.
func (b *Bot) SetMyCommandsForChat(ctx context.Context, chatID int64, commands []BotCommand) error {
	payload := map[string]any{
		"commands": commands,
		"scope": map[string]any{
			"type":    "chat",
			"chat_id": chatID,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/setMyCommands", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram set my commands for chat: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram set my commands for chat failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram set my commands for chat not ok: %s", ar.Description)
	}
	return nil
}

// SetChatMenuButton sets the default menu button (left of the input field) to
// open the Mini App at url with the given label, via the setChatMenuButton API.
func (b *Bot) SetChatMenuButton(ctx context.Context, text, url string) error {
	return b.setChatMenuButton(ctx, map[string]any{
		"type":    "web_app",
		"text":    text,
		"web_app": WebAppInfo{URL: url},
	})
}

// ResetChatMenuButton restores the default menu button (the commands menu).
// Telegram stores the menu button on the bot account, so a previously set
// Mini App button survives restarts and must be reset explicitly when the
// webapp is disabled.
func (b *Bot) ResetChatMenuButton(ctx context.Context) error {
	return b.setChatMenuButton(ctx, map[string]any{"type": "default"})
}

func (b *Bot) setChatMenuButton(ctx context.Context, menuButton map[string]any) error {
	payload := map[string]any{"menu_button": menuButton}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/setChatMenuButton", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram set chat menu button: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram set chat menu button failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err == nil && !ar.OK {
		return fmt.Errorf("telegram set chat menu button not ok: %s", ar.Description)
	}
	return nil
}

type getMeResponse struct {
	apiResponse
	Result User `json:"result"`
}

// GetMe returns the bot's own account info via the getMe API. Used at startup
// to learn the bot username for building deep links.
func (b *Bot) GetMe(ctx context.Context) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.apiBase+"/getMe", nil)
	if err != nil {
		return nil, err
	}

	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram get me: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram get me failed: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(raw), 300))
	}

	var ar getMeResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("telegram get me decode: %w", err)
	}
	if !ar.OK {
		return nil, fmt.Errorf("telegram get me not ok: %s", ar.Description)
	}
	return &ar.Result, nil
}

func (b *Bot) EndpointHost() string {
	u, _ := url.Parse(b.apiBase)
	return u.Host
}

func ParseChatID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
