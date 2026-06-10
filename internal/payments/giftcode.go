package payments

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// Alphabet excludes 0/O/1/I/L so a retyped code can't be misread.
const giftCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
const giftCodeLen = 16

// giftDeepLinkPrefix is the /start payload prefix that routes to redemption.
const giftDeepLinkPrefix = "gift_"

func generateGiftCode() (string, error) {
	buf := make([]byte, giftCodeLen)
	out := make([]byte, giftCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		// 31-char alphabet: modulo bias is negligible for non-secret-key use,
		// but reject the top of the byte range anyway to keep it uniform.
		for b >= 248 { // 248 = 8 * 31
			nb := make([]byte, 1)
			if _, err := rand.Read(nb); err != nil {
				return "", err
			}
			b = nb[0]
		}
		out[i] = giftCodeAlphabet[int(b)%len(giftCodeAlphabet)]
	}
	return string(out), nil
}

// --- in-memory conversation state ---

func (s *Service) getGiftCode(chatID int64) *giftCodeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.giftCodes[chatID]
	if g == nil {
		return nil
	}
	if s.now().Sub(g.createdAt) > giftCodeTTL {
		delete(s.giftCodes, chatID)
		return nil
	}
	return g
}

func (s *Service) setGiftCode(chatID int64, g *giftCodeState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.giftCodes[chatID] = g
}

func (s *Service) clearGiftCode(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.giftCodes, chatID)
}

// StartGiftCodeFlow handles /gift. Returns true if it consumed the message.
func (s *Service) StartGiftCodeFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() {
		return false
	}
	s.beginGiftCodeFlow(ctx, m.Chat.ID)
	return true
}

// beginGiftCodeFlow checks buyer eligibility (subscribers only)
// and prompts for the tariff. chatID is the buyer's private chat = Telegram ID.
func (s *Service) beginGiftCodeFlow(ctx context.Context, chatID int64) {
	if !s.isEnabled() {
		return
	}
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("gift: find buyer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if len(subs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Эта команда доступна только подписчикам.")
		return
	}

	s.setGiftCode(chatID, &giftCodeState{
		buyerName: subs[0].Username,
		buyerTGID: chatID,
		createdAt: s.now(),
	})

	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req != "" {
		_ = s.bot.SendPlain(ctx, chatID, "Реквизиты для оплаты:\n\n"+req)
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("gift: list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if len(tariffs) == 0 {
		tariffs = []store.Tariff{{Months: 1, Price: 0}} // fallback: single 1-month option
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		"🎁 Подарочная подписка. Выберите период:", s.giftCodeTariffKeyboard(tariffs))
}

func (s *Service) giftCodeTariffKeyboard(tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		label := fmt.Sprintf("%d мес.", t.Months)
		if t.Price > 0 {
			label = fmt.Sprintf("%d мес. — %s", t.Months, s.priceLabel(t.Price))
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         label,
			CallbackData: fmt.Sprintf("gc_pick:%d", t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: "Отмена", CallbackData: "gc_cancel",
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// handleMenuGift starts the gift purchase flow from the menu button.
func (s *Service) handleMenuGift(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginGiftCodeFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

func (s *Service) handleGiftCodePick(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	g := s.getGiftCode(chatID)
	if g == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /gift заново.")
		return true
	}

	months, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "gc_pick:"))
	if err != nil || months < 1 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}

	price := 0
	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("gift: get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff != nil {
		price = tariff.Price
	} else {
		// Only allowed when no tariffs are configured at all (the 1-month fallback).
		all, lerr := s.store.ListTariffs(ctx)
		if lerr != nil {
			s.logger.Error("gift: list tariffs failed", "err", lerr.Error())
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
			return true
		}
		if len(all) > 0 {
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот тариф больше недоступен.")
			return true
		}
	}

	giftID, code, err := s.createGiftCodeRow(ctx, g, months, price)
	if err != nil {
		s.logger.Error("gift: create gift code failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	buyerName := g.buyerName
	buyerTGID := g.buyerTGID
	s.clearGiftCode(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	priceStr := "бесплатно"
	if price > 0 {
		priceStr = s.priceLabel(price)
	}
	text := fmt.Sprintf(
		"🎁 Заявка на подарочную подписку\n\nПокупатель: %s (TG %d)\nПериод: %d мес. — %s\nКод: %s",
		buyerName, buyerTGID, months, priceStr, code,
	)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: "✅ Подтвердить оплату", CallbackData: fmt.Sprintf("gc_ok:%d", giftID)},
			{Text: "❌ Отклонить", CallbackData: fmt.Sprintf("gc_rej:%d", giftID)},
		}},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		if err != nil {
			s.logger.Error("gift: notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	s.mu.Lock()
	s.giftMsgs[giftID] = refs
	s.mu.Unlock()
	return true
}

// createGiftCodeRow generates a unique code and inserts the pending row,
// retrying on the (astronomically unlikely) UNIQUE collision.
func (s *Service) createGiftCodeRow(ctx context.Context, g *giftCodeState, months, price int) (int64, string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		code, err := generateGiftCode()
		if err != nil {
			return 0, "", err
		}
		id, err := s.store.CreateGiftCode(ctx, store.GiftCode{
			Code:            code,
			BuyerTelegramID: g.buyerTGID,
			BuyerUsername:   g.buyerName,
			Months:          months,
			Price:           price,
		})
		if err == nil {
			return id, code, nil
		}
		lastErr = err
	}
	return 0, "", lastErr
}

func (s *Service) handleGiftCodeCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearGiftCode(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}

// handleGiftCodeApprove processes admin's payment confirmation: issues the
// code and sends the buyer the deep link. No panel mutation happens here, so
// dry-run needs no special branch.
func (s *Service) handleGiftCodeApprove(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized gift approve attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
		return true
	}

	giftID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "gc_ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	g, err := s.store.GetGiftCode(ctx, giftID)
	if err != nil {
		s.logger.Error("gift: get gift code failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if g == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if g.Status != "pending" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка уже обработана.")
		return true
	}

	ok, err := s.store.IssueGiftCode(ctx, giftID, s.now())
	if err != nil {
		s.logger.Error("gift: issue failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if !ok {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка уже обработана.")
		return true
	}

	s.clearGiftButtons(ctx, giftID)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Код выдан покупателю.")
	_ = s.bot.SendPlain(ctx, cb.From.ID,
		fmt.Sprintf("✅ Подарочный код %s выдан покупателю %s.", g.Code, g.BuyerUsername))

	msg := fmt.Sprintf("🎁 Оплата подтверждена! Подарочная подписка на %d мес.\n\n%s", g.Months, s.giftLinkMessage(g))
	_ = s.bot.SendPlain(ctx, g.BuyerTelegramID, msg)
	return true
}

// giftLinkMessage builds the code + redemption deep link block sent to the
// buyer, falling back to manual /start instructions when the bot username is
// unknown.
func (s *Service) giftLinkMessage(g *store.GiftCode) string {
	msg := fmt.Sprintf("Код: %s\n", g.Code)
	if bu := s.getBotUsername(); bu != "" {
		msg += fmt.Sprintf("Ссылка: https://t.me/%s?start=%s%s\n\nОтправьте ссылку тому, кому дарите подписку. Подарок активируется при переходе по ней.",
			bu, giftDeepLinkPrefix, g.Code)
	} else {
		msg += fmt.Sprintf("\nПередайте код тому, кому дарите подписку: для активации нужно отправить боту команду /start %s%s",
			giftDeepLinkPrefix, g.Code)
	}
	return msg
}

// handleGiftCodeReject processes admin's rejection of a gift purchase.
func (s *Service) handleGiftCodeReject(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized gift reject attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
		return true
	}

	giftID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "gc_rej:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	g, err := s.store.GetGiftCode(ctx, giftID)
	if err != nil {
		s.logger.Error("gift: get gift code failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if g == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if g.Status != "pending" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка уже обработана.")
		return true
	}

	if _, err := s.store.RejectGiftCode(ctx, giftID, s.now()); err != nil {
		s.logger.Error("gift: mark rejected failed", "err", err.Error())
	}

	s.clearGiftButtons(ctx, giftID)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отклонена.")
	_ = s.bot.SendPlain(ctx, g.BuyerTelegramID,
		"❌ Ваша заявка на подарочную подписку отклонена администратором.")
	return true
}

// clearGiftButtons removes the approve/reject buttons from every admin's copy
// of the gift notification, then forgets the stored refs.
func (s *Service) clearGiftButtons(ctx context.Context, giftID int64) {
	s.mu.Lock()
	refs := s.giftMsgs[giftID]
	delete(s.giftMsgs, giftID)
	s.mu.Unlock()
	for _, ref := range refs {
		if err := s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil); err != nil {
			s.logger.Warn("clear admin gift button failed", "chat_id", ref.chatID, "err", err.Error())
		}
	}
}

// SendMyGifts lists the gift codes the user bought with their statuses, plus
// a button to re-send the redemption link for issued (not yet activated)
// gifts in case the original message was lost. Returns true (handled).
func (s *Service) SendMyGifts(ctx context.Context, chatID int64) bool {
	gifts, err := s.store.ListGiftCodesByBuyer(ctx, chatID)
	if err != nil {
		s.logger.Error("gift: list by buyer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if len(gifts) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Вы ещё не покупали подарочных подписок.")
		return true
	}

	var b strings.Builder
	b.WriteString("🎁 Ваши подарочные подписки:\n")
	var rows [][]tg.InlineKeyboardButton
	for _, g := range gifts {
		b.WriteString(fmt.Sprintf("\n%s — %d мес. (заявка от %s)\n%s",
			g.Code, g.Months, g.CreatedAt.Format("02.01.2006"), giftStatusLabel(&g)))
		b.WriteString("\n")
		if g.Status == "issued" {
			rows = append(rows, []tg.InlineKeyboardButton{{
				Text:         fmt.Sprintf("🔗 Ссылка для %s", g.Code),
				CallbackData: fmt.Sprintf("gc_link:%d", g.ID),
			}})
		}
	}
	text := strings.TrimRight(b.String(), "\n")
	if len(rows) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, text)
		return true
	}
	text += "\n\nЕсли вы потеряли сообщение со ссылкой, получите её заново:"
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	return true
}

func giftStatusLabel(g *store.GiftCode) string {
	switch g.Status {
	case "pending":
		return "⏳ Ожидает подтверждения оплаты"
	case "issued":
		return "📨 Выдан, ожидает активации"
	case "redeemed":
		if g.RedeemedUsername != "" {
			return "✅ Активирован (" + g.RedeemedUsername + ")"
		}
		return "✅ Активирован"
	case "rejected":
		return "❌ Отклонён"
	case "revoked":
		return "🚫 Отозван"
	}
	return g.Status
}

// handleMenuMyGifts shows the user's gift list from the menu button.
func (s *Service) handleMenuMyGifts(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.SendMyGifts(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleGiftCodeResend re-sends the redemption link of an issued gift to its
// buyer, for when the original message with the link was lost.
func (s *Service) handleGiftCodeResend(ctx context.Context, cb *tg.CallbackQuery) bool {
	giftID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "gc_link:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать подарок.")
		return true
	}
	g, err := s.store.GetGiftCode(ctx, giftID)
	if err != nil {
		s.logger.Error("gift: get gift code failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	// Only the buyer may request the link again.
	if g == nil || g.BuyerTelegramID != cb.From.ID {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Подарок не найден.")
		return true
	}
	if g.Status != "issued" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Этот подарок уже активирован, отклонён или отозван.")
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	_ = s.bot.SendPlain(ctx, g.BuyerTelegramID,
		fmt.Sprintf("🎁 Подарочная подписка на %d мес.\n\n%s", g.Months, s.giftLinkMessage(g)))
	return true
}

// sendAdminGiftList shows issued (unredeemed) gift codes with revoke buttons.
func (s *Service) sendAdminGiftList(ctx context.Context, chatID int64) {
	codes, err := s.store.ListGiftCodesByStatus(ctx, "issued")
	if err != nil {
		s.logger.Error("admin: list gift codes failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка чтения подарочных кодов.")
		return
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(codes)+1)
	for _, c := range codes {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf("🚫 %s — %d мес. (от %s)", c.Code, c.Months, c.BuyerUsername),
			CallbackData: fmt.Sprintf("adm:grev:%d", c.ID),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: "← Меню", CallbackData: "adm:menu"}})
	text := "Активные подарочные коды (нажмите, чтобы отозвать):"
	if len(codes) == 0 {
		text = "Активных подарочных кодов нет."
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleAdminGiftRevoke revokes an issued gift code from the admin list.
func (s *Service) handleAdminGiftRevoke(ctx context.Context, chatID int64, data string) {
	giftID, err := strconv.ParseInt(strings.TrimPrefix(data, "adm:grev:"), 10, 64)
	if err != nil {
		_ = s.bot.SendPlain(ctx, chatID, "Не удалось распознать код.")
		return
	}
	ok, err := s.store.RevokeGiftCode(ctx, giftID, s.now())
	if err != nil {
		s.logger.Error("admin: revoke gift code failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка отзыва кода.")
		return
	}
	if !ok {
		_ = s.bot.SendPlain(ctx, chatID, "Код уже использован или отозван.")
	} else {
		_ = s.bot.SendPlain(ctx, chatID, "Код отозван.")
		if g, gerr := s.store.GetGiftCode(ctx, giftID); gerr == nil && g != nil && g.BuyerTelegramID != 0 {
			_ = s.bot.SendPlain(ctx, g.BuyerTelegramID,
				fmt.Sprintf("🚫 Ваш подарочный код %s отозван администратором.", g.Code))
		}
	}
	s.sendAdminGiftList(ctx, chatID)
}
