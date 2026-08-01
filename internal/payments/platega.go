package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// plategaPayloadPrefix is the prefix of the payload echoed back by Platega so a
// webhook callback (which carries only a transaction id) can be correlated; the
// authoritative correlation is the stored provider_txn_id, this is a fallback /
// debugging aid.
const plategaPayloadPrefix = "req:"

// plategaStatusConfirmed is the Platega transaction status that means paid.
const plategaStatusConfirmed = "CONFIRMED"

// startPlategaPayment creates a pending platega payment request (no admin DM),
// opens a Platega transaction for it and returns the request id and the redirect
// URL the user must open to pay. The transaction id is stored on the request so
// the webhook and the "check payment" button can finalize it.
func (s *Service) startPlategaPayment(ctx context.Context, u *store.NotifiedUser, months, price int, plan string) (int64, string, error) {
	s.mu.Lock()
	gw := s.platega
	method := s.plategaMethod
	currency := s.plategaCurrency
	returnURL := s.plategaReturnURL
	s.mu.Unlock()
	if gw == nil {
		return 0, "", ErrPaymentsDisabled
	}

	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending", Provider: ProviderPlatega, Plan: plan,
	})
	if err != nil {
		s.logger.Error("platega: create payment request failed", "err", err.Error())
		return 0, "", fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	desc := fmt.Sprintf(i18n.T("Продление подписки «%s» на %d мес."), u.Username, months)
	payload := plategaPayloadPrefix + strconv.FormatInt(reqID, 10)
	txnID, redirect, err := gw.CreateTransaction(ctx, method, float64(price), currency, desc, returnURL, payload)
	if err != nil {
		s.logger.Error("platega: create transaction failed", "req_id", reqID, "err", err.Error())
		// Withdraw the dangling pending request so it cannot be confirmed later.
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("platega: withdraw request failed", "req_id", reqID, "err", derr.Error())
		}
		return 0, "", err
	}
	if err := s.store.SetPaymentRequestProviderTxn(ctx, reqID, txnID); err != nil {
		s.logger.Error("platega: store txn id failed", "req_id", reqID, "txn", txnID, "err", err.Error())
		return 0, "", err
	}
	return reqID, redirect, nil
}

func (s *Service) startPlategaTrafficExtension(ctx context.Context, u *store.NotifiedUser, trafficGB, price int, baseBytes int64) (int64, string, error) {
	s.mu.Lock()
	gw := s.platega
	method := s.plategaMethod
	currency := s.plategaCurrency
	returnURL := s.plategaReturnURL
	s.mu.Unlock()
	if gw == nil {
		return 0, "", ErrPaymentsDisabled
	}

	reqID, err := s.createTrafficExtensionPaymentRequest(ctx, u, trafficGB, price, baseBytes, ProviderPlatega, nil)
	if err != nil {
		if errors.Is(err, ErrTrafficExtensionActive) {
			return 0, "", err
		}
		return 0, "", fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	desc := fmt.Sprintf(i18n.T("Докупка %d ГБ трафика для «%s»."), trafficGB, u.Username)
	payload := plategaPayloadPrefix + strconv.FormatInt(reqID, 10)
	txnID, redirect, err := gw.CreateTransaction(ctx, method, float64(price), currency, desc, returnURL, payload)
	if err != nil {
		s.logger.Error("platega: create traffic transaction failed", "req_id", reqID, "err", err.Error())
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("platega: withdraw traffic request failed", "req_id", reqID, "err", derr.Error())
		}
		return 0, "", err
	}
	if err := s.store.SetPaymentRequestProviderTxn(ctx, reqID, txnID); err != nil {
		s.logger.Error("platega: store txn id failed", "req_id", reqID, "txn", txnID, "err", err.Error())
		return 0, "", err
	}
	return reqID, redirect, nil
}

// startPlategaAndPrompt opens a Platega payment from a bot callback and sends the
// user the «Оплатить» / «Проверить оплату» keyboard. Used by the pick / cabinet
// flows when Platega is the active provider.
func (s *Service) startPlategaAndPrompt(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, months, price int, plan string) {
	chatID := cb.From.ID
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	reqID, payURL, err := s.startPlategaPayment(ctx, u, months, price, plan)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, fmt.Sprintf(
		i18n.T("💳 Оплата %s за %d мес. Нажмите «Оплатить», а после оплаты — «Проверить оплату»."),
		s.priceLabel(price), months), s.plategaPayKeyboard(reqID, payURL))
}

// plategaPayKeyboard builds the «Оплатить» (URL) + «Проверить оплату» keyboard
// shown to the user after a Platega transaction is opened.
func (s *Service) plategaPayKeyboard(reqID int64, payURL string) *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("💳 Оплатить"), URL: payURL}},
			{{Text: i18n.T("🔄 Проверить оплату"), CallbackData: fmt.Sprintf("plcheck:%d", reqID)}},
		},
	}
}

// finalizePlategaByTxn verifies a transaction with Platega and, when CONFIRMED,
// extends the subscription, marks the request confirmed and notifies the user.
// Idempotent and safe to call from both the webhook and the "check" button: a
// non-pending request short-circuits, and ConfirmPaymentRequest is race-safe.
func (s *Service) finalizePlategaByTxn(ctx context.Context, txnID string) error {
	s.mu.Lock()
	gw := s.platega
	s.mu.Unlock()
	if gw == nil {
		return ErrPaymentsDisabled
	}

	req, err := s.store.GetPaymentRequestByProviderTxn(ctx, txnID)
	if err != nil {
		return fmt.Errorf("lookup platega request: %w", err)
	}
	if req == nil {
		s.logger.Warn("platega: no request for transaction", "txn", txnID)
		return nil
	}
	if req.Status != "pending" {
		// Already confirmed or rejected — nothing to do (dedup).
		return nil
	}

	status, err := gw.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("%w: verify %s: %v", ErrProviderUnavailable, txnID, err)
	}
	if !strings.EqualFold(status, plategaStatusConfirmed) {
		s.logger.Info("platega: transaction not confirmed", "txn", txnID, "status", status)
		return nil
	}

	confirmed, newExpireAt, err := s.confirmPaymentRequest(ctx, req.ID)
	switch {
	case errors.Is(err, ErrRequestResolved):
		return nil // a concurrent finalize already confirmed it
	case err != nil:
		return fmt.Errorf("confirm platega request %d: %w", req.ID, err)
	}

	if confirmed.TelegramID != 0 {
		if confirmed.Kind == store.PaymentKindTrafficExtension {
			_ = s.bot.SendPlain(ctx, confirmed.TelegramID, fmt.Sprintf(
				i18n.T("✅ Оплата получена! Для «%s» добавлено %d ГБ трафика до %s."),
				confirmed.Username, confirmed.TrafficGB, newExpireAt.Format("02.01.2006")))
		} else {
			_ = s.bot.SendPlain(ctx, confirmed.TelegramID, fmt.Sprintf(
				i18n.T("✅ Оплата получена! Подписка для «%s» продлена до %s."),
				confirmed.Username, newExpireAt.Format("02.01.2006")))
		}
	}
	return nil
}

// plategaWebhook is the JSON body Platega POSTs to the callback URL. It carries
// only an id; the real status is fetched from the API for verification.
type plategaWebhook struct {
	ID            string `json:"id"`
	TransactionID string `json:"transactionId"`
}

// HandlePlategaWebhook verifies and finalizes a Platega callback. It returns nil
// for already-handled / non-confirmed events so Platega is not asked to retry.
func (s *Service) HandlePlategaWebhook(ctx context.Context, body []byte) error {
	var n plategaWebhook
	if err := json.Unmarshal(body, &n); err != nil {
		return fmt.Errorf("platega webhook: bad json: %w", err)
	}
	id := n.ID
	if id == "" {
		id = n.TransactionID
	}
	if id == "" {
		s.logger.Warn("platega webhook: empty transaction id")
		return nil
	}
	return s.finalizePlategaByTxn(ctx, id)
}

// CheckPlategaPayment verifies only a request owned by the authenticated user.
// Missing and foreign requests intentionally share one error to prevent probing.
func (s *Service) CheckPlategaPayment(ctx context.Context, telegramID, requestID int64) (*WebPaymentStatus, error) {
	req, err := s.store.GetPaymentRequest(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("load payment request: %w", err)
	}
	if req == nil || req.TelegramID != telegramID || req.Provider != ProviderPlatega || req.ProviderTxnID == "" {
		return nil, ErrPaymentRequestInaccessible
	}
	if req.Status == "pending" {
		if err := s.finalizePlategaByTxn(ctx, req.ProviderTxnID); err != nil {
			return nil, err
		}
		req, err = s.store.GetPaymentRequest(ctx, requestID)
		if err != nil {
			return nil, fmt.Errorf("reload payment request: %w", err)
		}
	}
	return &WebPaymentStatus{Status: req.Status}, nil
}

// handlePlategaCheck handles the «Проверить оплату» button: it verifies the
// transaction and confirms the subscription if Platega reports it paid.
func (s *Service) handlePlategaCheck(ctx context.Context, cb *tg.CallbackQuery) bool {
	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "plcheck:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil || req == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка не найдена."))
		return true
	}
	if req.Status == "confirmed" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Подписка уже продлена."))
		return true
	}
	if err := s.finalizePlategaByTxn(ctx, req.ProviderTxnID); err != nil {
		s.logger.Error("platega: check failed", "req_id", reqID, "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка проверки оплаты, попробуйте позже."))
		return true
	}
	// Re-read to report the outcome.
	if updated, _ := s.store.GetPaymentRequest(ctx, reqID); updated != nil && updated.Status == "confirmed" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Оплата получена, подписка продлена!"))
		if cb.Message != nil {
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
		}
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID,
		i18n.T("Оплата ещё не поступила. Если вы оплатили — подождите минуту и нажмите снова."))
	return true
}
