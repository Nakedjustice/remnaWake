package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// --- in-memory conversation state ---

func (s *Service) getGift(chatID int64) *giftState {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gifts[chatID]
	if g == nil {
		return nil
	}
	if s.now().Sub(g.createdAt) > giftTTL {
		delete(s.gifts, chatID)
		return nil
	}
	return g
}

func (s *Service) setGift(chatID int64, g *giftState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gifts[chatID] = g
}

func (s *Service) clearGift(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gifts, chatID)
}

// StartGiftFlow handles /payff. Returns true if it consumed the message.
func (s *Service) StartGiftFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() {
		return false
	}
	s.beginGiftFlow(ctx, m.Chat.ID)
	return true
}

// beginGiftFlow checks payer eligibility and, if they are a subscriber, seeds
// the conversation state and prompts for the target identifier. Shared by the
// /payff command and the menu button. chatID is the payer's private chat, which
// equals their Telegram ID.
func (s *Service) beginGiftFlow(ctx context.Context, chatID int64) {
	// Guard the menu-button path too: a stale button could be tapped after the
	// admin list was cleared, which would otherwise orphan a pending request.
	if !s.isEnabled() {
		return
	}
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("payff: find payer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if len(subs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Эта команда доступна только подписчикам.")
		return
	}

	s.setGift(chatID, &giftState{
		step:      stepAwaitingIdentifier,
		payerName: subs[0].Username,
		payerTGID: chatID,
		createdAt: s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		"Введите имя пользователя или Telegram ID того, кому оплачиваете. /cancel — отмена.")
}

// SendMenu replies with the user menu: a short command list plus, when the
// payment flow is enabled, buttons to view tariffs and start the
// pay-for-another-user and invite flows.
func (s *Service) SendMenu(ctx context.Context, chatID int64) bool {
	text := "Меню\n\n" +
		"/tariff — посмотреть тарифы\n" +
		"/payff — оплатить подписку за другого пользователя\n" +
		"/gift — подарить подписку\n" +
		"/invite — пригласить нового пользователя\n" +
		"/register — привязать свой Telegram к профилю\n" +
		"/cancel — отменить текущее действие"
	if !s.isEnabled() {
		_ = s.bot.SendPlain(ctx, chatID, text)
		return true
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "💵 Тарифы", CallbackData: "menu:tariffs"}},
			{{Text: "💳 Оплатить за другого", CallbackData: "menu:payff"}},
			{{Text: "🎁 Подарить подписку", CallbackData: "menu:gift"}},
			{{Text: "👤 Пригласить пользователя", CallbackData: "menu:invite"}},
			{{Text: "🔗 Привязать аккаунт", CallbackData: "menu:register"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
	return true
}

// SendTariffs lists the current tariffs to any user. Returns true (handled).
func (s *Service) SendTariffs(ctx context.Context, chatID int64) bool {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("tariffs: list failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if len(tariffs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Тарифы пока не заданы.")
		return true
	}
	var b strings.Builder
	b.WriteString("Тарифы:\n")
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf("%d мес. — %s\n", t.Months, s.priceLabel(t.Price)))
	}
	_ = s.bot.SendPlain(ctx, chatID, strings.TrimRight(b.String(), "\n"))
	return true
}

// handleMenuPayff starts the gift flow from the menu button.
func (s *Service) handleMenuPayff(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginGiftFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleMenuTariffs lists tariffs from the menu button.
func (s *Service) handleMenuTariffs(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.SendTariffs(ctx, cb.Message.Chat.ID)
	}
	return true
}

// HandleText consumes a free-text message when the chat is mid-/payff or
// mid-/invite, plus /cancel. Returns true only when it handled the message.
func (s *Service) HandleText(ctx context.Context, m *tg.Message) bool {
	if m == nil {
		return false
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if text == "/cancel" {
		hasGift := s.getGift(chatID) != nil
		hasInvite := s.getInvite(chatID) != nil
		hasRegister := s.getRegister(chatID) != nil
		hasGiftCode := s.getGiftCode(chatID) != nil
		hasRedeem := s.getRedeem(chatID) != nil
		if !hasGift && !hasInvite && !hasRegister && !hasGiftCode && !hasRedeem {
			return false
		}
		s.clearGift(chatID)
		s.clearInvite(chatID)
		s.clearRegister(chatID)
		s.clearGiftCode(chatID)
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Отменено.")
		return true
	}

	g := s.getGift(chatID)
	if g == nil || g.step != stepAwaitingIdentifier {
		if s.handleInviteUsernameInput(ctx, m) {
			return true
		}
		if s.handleRedeemUsernameInput(ctx, m) {
			return true
		}
		return s.handleRegisterUsernameInput(ctx, m)
	}

	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID,
			"Введите имя пользователя или Telegram ID, либо /cancel для отмены.")
		return true
	}
	if text == "" || len(text) > 64 {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректный ввод. Введите имя пользователя или Telegram ID.")
		return true
	}

	target, multi, err := s.resolveTarget(ctx, text)
	if err != nil {
		s.logger.Error("payff: resolve target failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if multi {
		_ = s.bot.SendPlain(ctx, chatID,
			"У этого Telegram ID несколько подписок, введите имя пользователя.")
		return true
	}
	if target == nil {
		_ = s.bot.SendPlain(ctx, chatID, "Пользователь не найден, попробуйте ещё раз.")
		return true
	}

	g.target = target
	g.step = stepAwaitingTariff
	s.setGift(chatID, g)

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("payff: list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	prompt := fmt.Sprintf("Оплата для %s (до %s). Выберите период:",
		target.Username, target.ExpireAt.Format("02.01.2006"))
	if len(tariffs) == 0 {
		tariffs = []store.Tariff{{Months: 1, Price: 0}} // fallback: single 1-month option
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, prompt, s.giftTariffKeyboard(tariffs))
	return true
}

func (s *Service) resolveTarget(ctx context.Context, identifier string) (target *Subscriber, multi bool, err error) {
	if isAllDigits(identifier) {
		tgID, perr := strconv.ParseInt(identifier, 10, 64)
		if perr != nil {
			return nil, false, nil // overflow -> treat as not found
		}
		subs, ferr := s.finder.FindByTelegramID(ctx, tgID)
		if ferr != nil {
			return nil, false, ferr
		}
		switch len(subs) {
		case 0:
			return nil, false, nil
		case 1:
			t := subs[0]
			return &t, false, nil
		default:
			return nil, true, nil
		}
	}
	sub, ferr := s.finder.FindByUsername(ctx, identifier)
	if ferr != nil {
		return nil, false, ferr
	}
	if sub == nil {
		return nil, false, nil
	}
	return sub, false, nil
}

func (s *Service) giftTariffKeyboard(tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		label := fmt.Sprintf("%d мес.", t.Months)
		if t.Price > 0 {
			label = fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price))
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         label,
			CallbackData: fmt.Sprintf("gpick:%d", t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: "Отмена", CallbackData: "gcancel",
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) handleGiftPick(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	g := s.getGift(chatID)
	if g == nil || g.step != stepAwaitingTariff || g.target == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /payff заново.")
		return true
	}

	months, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "gpick:"))
	if err != nil || months < 1 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}

	price := 0
	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("payff: get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff != nil {
		price = tariff.Price
	} else {
		// Only allowed when no tariffs are configured at all (the 1-month fallback).
		all, lerr := s.store.ListTariffs(ctx)
		if lerr != nil {
			s.logger.Error("payff: list tariffs failed", "err", lerr.Error())
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
			return true
		}
		if len(all) > 0 {
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот тариф больше недоступен.")
			return true
		}
	}

	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID:     g.target.RemnawaveID,
		UUID:            g.target.UUID,
		Username:        g.target.Username,
		TelegramID:      g.target.TelegramID,
		Months:          months,
		Price:           price,
		ExpireAt:        g.target.ExpireAt,
		Status:          "pending",
		PayerTelegramID: g.payerTGID,
		PayerUsername:   g.payerName,
	})
	if err != nil {
		s.logger.Error("payff: create request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	text := s.formatGiftRequest(g, months, price)
	s.clearGift(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Подтвердить оплату", CallbackData: fmt.Sprintf("ok:%d", reqID)}},
		},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		if err != nil {
			s.logger.Error("payff: notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	s.mu.Lock()
	s.payMsgs[reqID] = refs
	s.mu.Unlock()
	return true
}

func (s *Service) handleGiftCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearGift(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}

func (s *Service) formatGiftRequest(g *giftState, months, price int) string {
	var b strings.Builder
	b.WriteString("💳 Заявка на оплату за другого пользователя\n\n")
	b.WriteString(fmt.Sprintf("Плательщик: %s (TG %d)\n", g.payerName, g.payerTGID))
	b.WriteString("Получатель: " + g.target.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", g.target.RemnawaveID))
	b.WriteString("UUID: " + g.target.UUID + "\n")
	if g.target.TelegramID != 0 {
		b.WriteString(fmt.Sprintf("Telegram ID: %d\n", g.target.TelegramID))
	}
	b.WriteString("Подписка до: " + g.target.ExpireAt.Format("02.01.2006") + "\n")
	if price > 0 {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес. — %s", months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf("Выбрано: %d мес.", months))
	}
	return b.String()
}
