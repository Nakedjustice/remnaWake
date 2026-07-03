package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// starsCurrency is the Telegram Stars currency code.
const starsCurrency = "XTR"

// starsAmount converts a tariff price (in the CURRENCY unit) to a whole number
// of Telegram Stars using the configured rate (price units per Star), rounding
// up so the charge never undercuts the price. Returns at least 1 Star.
func (s *Service) starsAmount(price int) int {
	s.mu.Lock()
	rate := s.starsRate
	s.mu.Unlock()
	if rate <= 0 {
		rate = 1
	}
	stars := (price + rate - 1) / rate // ceil(price / rate)
	if stars < 1 {
		stars = 1
	}
	return stars
}

// starsInvoiceContent builds the title, description and price line for a Stars
// invoice, shared by the chat and Mini App flows.
func (s *Service) starsInvoiceContent(username string, months, price int) (title, desc string, prices []tg.LabeledPrice) {
	title = i18n.T("Продление подписки")
	desc = fmt.Sprintf(i18n.T("Продление подписки «%s» на %d мес."), username, months)
	label := fmt.Sprintf(i18n.T("%d мес."), months)
	prices = []tg.LabeledPrice{{Label: label, Amount: s.starsAmount(price)}}
	return title, desc, prices
}

func (s *Service) starsTrafficInvoiceContent(username string, trafficGB, price int) (title, desc string, prices []tg.LabeledPrice) {
	title = i18n.T("Докупка трафика")
	desc = fmt.Sprintf(i18n.T("Докупка %d ГБ трафика для «%s»."), trafficGB, username)
	label := fmt.Sprintf(i18n.T("+%d ГБ"), trafficGB)
	prices = []tg.LabeledPrice{{Label: label, Amount: s.starsAmount(price)}}
	return title, desc, prices
}

// startStarsPayment creates a pending Telegram Stars payment request (no admin
// DM) and returns its id. The invoice itself is sent separately so both the
// chat flow (SendInvoice) and the Mini App flow (CreateInvoiceLink) can reuse it.
func (s *Service) startStarsPayment(ctx context.Context, u *store.NotifiedUser, months, price int, plan string) (int64, error) {
	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending", Provider: ProviderTelegramStars, Plan: plan,
	})
	if err != nil {
		s.logger.Error("stars: create payment request failed", "err", err.Error())
		return 0, err
	}
	return reqID, nil
}

func (s *Service) startStarsTrafficExtension(ctx context.Context, u *store.NotifiedUser, trafficGB, price int, baseBytes int64) (int64, error) {
	return s.createTrafficExtensionPaymentRequest(ctx, u, trafficGB, price, baseBytes, ProviderTelegramStars, nil)
}

// starsPayload builds the invoice payload that correlates a successful_payment
// (or pre_checkout_query) back to its payment request.
func starsPayload(reqID int64) string {
	return plategaPayloadPrefix + strconv.FormatInt(reqID, 10)
}

// reqIDFromPayload parses the request id from an invoice payload ("req:<id>").
func reqIDFromPayload(payload string) (int64, error) {
	return strconv.ParseInt(strings.TrimPrefix(payload, plategaPayloadPrefix), 10, 64)
}

// startStarsAndPrompt opens a Telegram Stars payment from a bot callback and
// sends the user a native Stars invoice. Used by the pick / cabinet flows when
// Telegram Stars is the chosen provider.
func (s *Service) startStarsAndPrompt(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, months, price int, plan string) {
	chatID := cb.From.ID
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	reqID, err := s.startStarsPayment(ctx, u, months, price, plan)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	title, desc, prices := s.starsInvoiceContent(u.Username, months, price)
	if _, err := s.bot.SendInvoice(ctx, chatID, title, desc, starsPayload(reqID), prices); err != nil {
		s.logger.Error("stars: send invoice failed", "req_id", reqID, "err", err.Error())
		// Withdraw the dangling pending request so it cannot be confirmed later.
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("stars: withdraw request failed", "req_id", reqID, "err", derr.Error())
		}
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
}

// startStarsInvoiceLink opens a Telegram Stars payment for the Mini App and
// returns an invoice link the app opens with Telegram.WebApp.openInvoice.
func (s *Service) startStarsInvoiceLink(ctx context.Context, u *store.NotifiedUser, months, price int, plan string) (string, error) {
	reqID, err := s.startStarsPayment(ctx, u, months, price, plan)
	if err != nil {
		return "", err
	}
	title, desc, prices := s.starsInvoiceContent(u.Username, months, price)
	link, err := s.bot.CreateInvoiceLink(ctx, title, desc, starsPayload(reqID), prices)
	if err != nil {
		s.logger.Error("stars: create invoice link failed", "req_id", reqID, "err", err.Error())
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("stars: withdraw request failed", "req_id", reqID, "err", derr.Error())
		}
		return "", err
	}
	return link, nil
}

func (s *Service) startStarsTrafficExtensionLink(ctx context.Context, u *store.NotifiedUser, trafficGB, price int, baseBytes int64) (string, error) {
	reqID, err := s.startStarsTrafficExtension(ctx, u, trafficGB, price, baseBytes)
	if err != nil {
		return "", err
	}
	title, desc, prices := s.starsTrafficInvoiceContent(u.Username, trafficGB, price)
	link, err := s.bot.CreateInvoiceLink(ctx, title, desc, starsPayload(reqID), prices)
	if err != nil {
		s.logger.Error("stars: create traffic invoice link failed", "req_id", reqID, "err", err.Error())
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("stars: withdraw traffic request failed", "req_id", reqID, "err", derr.Error())
		}
		return "", err
	}
	return link, nil
}

// HandlePreCheckout answers a pre_checkout_query for a Stars payment. Telegram
// requires an answer within 10 seconds; the bot confirms only when the payload
// maps to a pending Stars request whose amount still matches. Returns true if
// the query was a Stars pre-checkout this service handled.
func (s *Service) HandlePreCheckout(ctx context.Context, q *tg.PreCheckoutQuery) bool {
	if q == nil {
		return false
	}
	reject := func(reason string) bool {
		if err := s.bot.AnswerPreCheckoutQuery(ctx, q.ID, false, reason); err != nil {
			s.logger.Error("stars: reject pre-checkout failed", "err", err.Error())
		}
		return true
	}

	reqID, err := reqIDFromPayload(q.InvoicePayload)
	if err != nil {
		return reject(i18n.T("Не удалось распознать заявку."))
	}
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("stars: pre-checkout lookup failed", "req_id", reqID, "err", err.Error())
		return reject(i18n.T("Ошибка, попробуйте позже."))
	}
	if req == nil || req.Provider != ProviderTelegramStars {
		return reject(i18n.T("Заявка не найдена."))
	}
	if req.Status != "pending" {
		return reject(i18n.T("Эта заявка уже обработана."))
	}
	if q.TotalAmount != s.starsAmount(req.Price) {
		s.logger.Warn("stars: pre-checkout amount mismatch", "req_id", reqID, "got", q.TotalAmount, "want", s.starsAmount(req.Price))
		return reject(i18n.T("Сумма оплаты изменилась, начните оплату заново."))
	}
	if err := s.bot.AnswerPreCheckoutQuery(ctx, q.ID, true, ""); err != nil {
		s.logger.Error("stars: confirm pre-checkout failed", "req_id", reqID, "err", err.Error())
	}
	return true
}

// HandleSuccessfulPayment finalizes a Stars payment: it extends the
// subscription, marks the request confirmed and notifies the user. Idempotent —
// a non-pending request short-circuits and ConfirmPaymentRequest is race-safe.
// Returns true if the message carried a Stars payment this service handled.
func (s *Service) HandleSuccessfulPayment(ctx context.Context, m *tg.Message) bool {
	if m == nil || m.SuccessfulPayment == nil {
		return false
	}
	sp := m.SuccessfulPayment
	reqID, err := reqIDFromPayload(sp.InvoicePayload)
	if err != nil {
		s.logger.Warn("stars: successful payment with unparseable payload", "payload", sp.InvoicePayload)
		return true
	}
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("stars: successful payment lookup failed", "req_id", reqID, "err", err.Error())
		return true
	}
	if req == nil {
		s.logger.Warn("stars: no request for successful payment", "req_id", reqID)
		return true
	}
	if req.Status != "pending" {
		// Already confirmed or rejected — nothing to do (dedup).
		return true
	}

	// Store the Telegram charge id for audit / refunds.
	if err := s.store.SetPaymentRequestProviderTxn(ctx, reqID, sp.TelegramPaymentChargeID); err != nil {
		s.logger.Error("stars: store charge id failed", "req_id", reqID, "err", err.Error())
	}

	confirmed, newExpireAt, err := s.confirmPaymentRequest(ctx, req.ID)
	switch {
	case errors.Is(err, ErrRequestResolved):
		return true // a concurrent finalize already confirmed it
	case err != nil:
		s.logger.Error("stars: confirm request failed", "req_id", reqID, "err", err.Error())
		return true
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
	return true
}
