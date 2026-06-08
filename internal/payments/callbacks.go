package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// HandleCallback dispatches an inline-button callback. Returns true if handled.
func (s *Service) HandleCallback(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb == nil {
		return false
	}
	switch {
	case strings.HasPrefix(cb.Data, "pay:"):
		return s.handlePay(ctx, cb)
	case strings.HasPrefix(cb.Data, "pick:"):
		return s.handlePick(ctx, cb)
	case strings.HasPrefix(cb.Data, "back:"):
		return s.handleBack(ctx, cb)
	case strings.HasPrefix(cb.Data, "ok:"):
		return s.handleConfirm(ctx, cb)
	case strings.HasPrefix(cb.Data, "gpick:"):
		return s.handleGiftPick(ctx, cb)
	case cb.Data == "gcancel":
		return s.handleGiftCancel(ctx, cb)
	case cb.Data == "menu:payff":
		return s.handleMenuPayff(ctx, cb)
	case cb.Data == "menu:tariffs":
		return s.handleMenuTariffs(ctx, cb)
	case cb.Data == "menu:invite":
		return s.handleMenuInvite(ctx, cb)
	case cb.Data == "inv_submit":
		return s.handleInviteSubmit(ctx, cb)
	case strings.HasPrefix(cb.Data, "inv_ok:"):
		return s.handleInviteApprove(ctx, cb)
	case strings.HasPrefix(cb.Data, "inv_rej:"):
		return s.handleInviteReject(ctx, cb)
	case cb.Data == "inv_cancel":
		return s.handleInviteCancel(ctx, cb)
	case cb.Data == "menu:register":
		return s.handleMenuRegister(ctx, cb)
	case cb.Data == "reg_confirm":
		return s.handleRegisterConfirm(ctx, cb)
	case cb.Data == "reg_cancel":
		return s.handleRegisterCancel(ctx, cb)
	default:
		return false
	}
}

func (s *Service) handlePay(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "pay:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	if len(tariffs) == 0 {
		// Fallback: behave like the old single-button flow (1 month).
		s.createRequestAndNotify(ctx, cb, userID, 1, 0)
		return true
	}

	// Show tariff options on the same message.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.tariffKeyboard(userID, tariffs))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Выберите количество месяцев.")
	return true
}

func (s *Service) handlePick(ctx context.Context, cb *tg.CallbackQuery) bool {
	parts := strings.Split(strings.TrimPrefix(cb.Data, "pick:"), ":")
	if len(parts) != 2 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}

	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот тариф больше недоступен.")
		return true
	}

	s.createRequestAndNotify(ctx, cb, userID, months, tariff.Price)
	return true
}

func (s *Service) handleBack(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "back:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.PaymentButton(userID))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	return true
}

// createRequestAndNotify looks up the remembered user, writes a pending request,
// clears the user's keyboard, and DMs the admin a confirm button.
func (s *Service) createRequestAndNotify(ctx context.Context, cb *tg.CallbackQuery, userID int64, months, price int) {
	u, err := s.store.GetNotifiedUser(ctx, userID)
	if err != nil {
		s.logger.Error("get notified user failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return
	}
	if u == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось найти данные. Дождитесь следующего уведомления.")
		return
	}

	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending",
	})
	if err != nil {
		s.logger.Error("create payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return
	}

	// Clear the user's keyboard and acknowledge.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	// Notify the admin with details + confirm button.
	text := s.formatAdminRequest(u, months, price)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Подтвердить оплату", CallbackData: fmt.Sprintf("ok:%d", reqID)}},
		},
	}
	if err := s.bot.SendPlainWithKeyboard(ctx, s.adminID, text, kb); err != nil {
		s.logger.Error("notify admin failed", "err", err.Error())
	}
}

func (s *Service) handleConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if s.adminID == 0 || cb.From.ID != s.adminID {
		s.logger.Warn("unauthorized confirm attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав для подтверждения оплаты.")
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("get payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if req == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if req.Status == "confirmed" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подписка уже была продлена.")
		return true
	}

	base := req.ExpireAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, req.Months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would extend", "uuid", req.UUID, "months", req.Months, "new_expire", newExpireAt.Format("2006-01-02"))
		_, _ = s.store.ConfirmPaymentRequest(ctx, reqID, s.now())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подписка продлена (dry-run).")
		return true
	}

	if err := s.extender.ExtendSubscriptionByUUID(ctx, req.UUID, newExpireAt); err != nil {
		s.logger.Error("extend subscription failed", "uuid", req.UUID, "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка продления подписки. Проверьте логи.")
		return true
	}

	if _, err := s.store.ConfirmPaymentRequest(ctx, reqID, s.now()); err != nil {
		s.logger.Error("mark confirmed failed", "err", err.Error())
	}

	// Remove the admin's confirm button to prevent re-taps.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Подписка продлена!")
	_ = s.bot.SendPlain(ctx, s.adminID, fmt.Sprintf("✅ Подписка для %s продлена на %d мес. до %s",
		req.Username, req.Months, newExpireAt.Format("02.01.2006")))
	return true
}

func (s *Service) tariffKeyboard(userID int64, tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("pick:%d:%d", userID, t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: "← Назад", CallbackData: fmt.Sprintf("back:%d", userID),
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) formatAdminRequest(u *store.NotifiedUser, months, price int) string {
	var b strings.Builder
	b.WriteString("💳 Заявка на оплату\n\n")
	b.WriteString("Клиент: " + u.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", u.RemnawaveID))
	b.WriteString("UUID: " + u.UUID + "\n")
	b.WriteString(fmt.Sprintf("Telegram ID: %d\n", u.TelegramID))
	b.WriteString("Подписка до: " + u.ExpireAt.Format("02.01.2006") + "\n")
	if price > 0 {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес. — %s", months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес.", months))
	}
	return b.String()
}
