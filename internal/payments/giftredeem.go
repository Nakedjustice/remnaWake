package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// --- in-memory conversation state ---

func (s *Service) getRedeem(chatID int64) *redeemState {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.redeems[chatID]
	if r == nil {
		return nil
	}
	if s.now().Sub(r.createdAt) > giftCodeTTL {
		delete(s.redeems, chatID)
		return nil
	}
	return r
}

func (s *Service) setRedeem(chatID int64, r *redeemState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redeems[chatID] = r
}

func (s *Service) clearRedeem(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.redeems, chatID)
}

// isValidGiftCodeFormat cheaply rejects garbage before a DB lookup.
func isValidGiftCodeFormat(code string) bool {
	if len(code) != giftCodeLen {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(giftCodeAlphabet, r) {
			return false
		}
	}
	return true
}

// StartGiftRedemption handles a /start gift_<code> deep link. chatID is the
// redeemer's private chat, which equals their Telegram ID.
func (s *Service) StartGiftRedemption(ctx context.Context, chatID int64, rawCode string) {
	code := strings.ToUpper(strings.TrimSpace(rawCode))
	if !isValidGiftCodeFormat(code) {
		_ = s.bot.SendPlain(ctx, chatID, "Код не найден или недействителен.")
		return
	}

	g, err := s.store.GetGiftCodeByCode(ctx, code)
	if err != nil {
		s.logger.Error("redeem: get gift code failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if g == nil {
		_ = s.bot.SendPlain(ctx, chatID, "Код не найден или недействителен.")
		return
	}
	switch g.Status {
	case "issued":
		// proceed
	case "pending":
		_ = s.bot.SendPlain(ctx, chatID, "Оплата этого подарка ещё не подтверждена. Попробуйте позже.")
		return
	case "redeemed":
		_ = s.bot.SendPlain(ctx, chatID, "Этот код уже был активирован.")
		return
	default: // revoked, rejected
		_ = s.bot.SendPlain(ctx, chatID, "Код недействителен.")
		return
	}

	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("redeem: find redeemer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}

	st := &redeemState{
		giftID:    g.ID,
		code:      g.Code,
		months:    g.Months,
		createdAt: s.now(),
	}

	switch len(subs) {
	case 0:
		// New user: ask for the desired profile username, create on input.
		st.awaitingUsername = true
		s.setRedeem(chatID, st)
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
			"🎁 Вам подарили подписку на %d мес.!\n\nВведите желаемое имя пользователя для вашего профиля (буквы, цифры и «_», от 3 до 32 символов). /cancel — отмена.",
			g.Months))
	case 1:
		// Existing subscriber: ask for confirmation so an accidental tap on
		// the deep link doesn't consume the gift.
		st.candidates = subs
		s.setRedeem(chatID, st)
		kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "✅ Активировать", CallbackData: "gc_use:0"}},
			{{Text: "Отмена", CallbackData: "gc_redeem_cancel"}},
		}}
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
			fmt.Sprintf("🎁 Вам подарили подписку на %d мес.\n\nАктивировать для профиля «%s» (подписка до %s)? Срок будет продлён на %d мес.",
				g.Months, subs[0].Username, subs[0].ExpireAt.Format("02.01.2006"), g.Months),
			kb)
	default:
		// Several profiles on this Telegram ID: let the user pick which to extend.
		st.candidates = subs
		s.setRedeem(chatID, st)
		rows := make([][]tg.InlineKeyboardButton, 0, len(subs)+1)
		for i, sub := range subs {
			rows = append(rows, []tg.InlineKeyboardButton{{
				Text:         fmt.Sprintf("%s (до %s)", sub.Username, sub.ExpireAt.Format("02.01.2006")),
				CallbackData: fmt.Sprintf("gc_use:%d", i),
			}})
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: "Отмена", CallbackData: "gc_redeem_cancel"}})
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
			fmt.Sprintf("🎁 Вам подарили подписку на %d мес. Выберите профиль для продления:", g.Months),
			&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	}
}

// handleGiftUse processes the profile choice when the redeemer has several.
func (s *Service) handleGiftUse(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	st := s.getRedeem(chatID)
	if st == nil || len(st.candidates) == 0 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Откройте ссылку ещё раз.")
		return true
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "gc_use:"))
	if err != nil || idx < 0 || idx >= len(st.candidates) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать выбор.")
		return true
	}
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	s.redeemExtend(ctx, chatID, st, st.candidates[idx])
	return true
}

// handleGiftRedeemCancel cancels an in-progress redemption conversation.
// The code itself stays issued, so the link can be opened again later.
func (s *Service) handleGiftRedeemCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearRedeem(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}

// handleRedeemUsernameInput consumes a free-text message while a redemption is
// awaiting the desired profile username. Returns true when it handled it.
func (s *Service) handleRedeemUsernameInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	st := s.getRedeem(chatID)
	if st == nil || !st.awaitingUsername {
		return false
	}

	text := strings.TrimSpace(m.Text)
	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID, "Введите имя пользователя или /cancel для отмены.")
		return true
	}
	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов.")
		return true
	}

	existing, err := s.finder.FindByUsername(ctx, text)
	if err != nil {
		s.logger.Error("redeem: check username failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if existing != nil {
		_ = s.bot.SendPlain(ctx, chatID, "Это имя занято, попробуйте другое.")
		return true
	}

	s.redeemCreate(ctx, chatID, st, text)
	return true
}

// redeemExtend claims the code, then extends an existing subscription. The
// claim happens first so a concurrent redemption can never double-spend; if
// the panel call fails afterwards, the code is rolled back to issued.
func (s *Service) redeemExtend(ctx context.Context, chatID int64, st *redeemState, sub Subscriber) {
	ok, err := s.store.RedeemGiftCode(ctx, st.code, chatID, sub.Username, s.now())
	if err != nil {
		s.logger.Error("redeem: claim failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if !ok {
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Этот код уже был активирован.")
		return
	}

	base := sub.ExpireAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, st.months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would extend via gift", "uuid", sub.UUID, "months", st.months,
			"new_expire", newExpireAt.Format("2006-01-02"))
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
			"✅ (dry-run) Подарок активирован! Подписка «%s» продлена на %d мес. до %s.",
			sub.Username, st.months, newExpireAt.Format("02.01.2006")))
		s.notifyGiftRedeemed(ctx, st.giftID, chatID, sub.Username)
		return
	}

	if err := s.extender.ExtendSubscriptionByUUID(ctx, sub.UUID, newExpireAt); err != nil {
		s.logger.Error("redeem: extend failed", "uuid", sub.UUID, "err", err.Error())
		if _, rerr := s.store.ReissueGiftCode(ctx, st.giftID); rerr != nil {
			s.logger.Error("redeem: rollback to issued failed", "gift_id", st.giftID, "err", rerr.Error())
		}
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка активации подарка. Попробуйте позже.")
		return
	}

	s.clearRedeem(chatID)
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
		"✅ Подарок активирован! Подписка «%s» продлена на %d мес. до %s.",
		sub.Username, st.months, newExpireAt.Format("02.01.2006")))
	s.notifyGiftRedeemed(ctx, st.giftID, chatID, sub.Username)
}

// redeemCreate claims the code, then creates a fresh panel profile bound to
// the redeemer's Telegram ID and delivers the subscription link.
func (s *Service) redeemCreate(ctx context.Context, chatID int64, st *redeemState, username string) {
	if s.creator == nil {
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}

	ok, err := s.store.RedeemGiftCode(ctx, st.code, chatID, username, s.now())
	if err != nil {
		s.logger.Error("redeem: claim failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if !ok {
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, "Этот код уже был активирован.")
		return
	}

	expireAt := s.now().AddDate(0, st.months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would create user via gift", "username", username,
			"telegram_id", chatID, "expire_at", expireAt.Format("2006-01-02"))
		s.clearRedeem(chatID)
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
			"✅ (dry-run) Подарок активирован! Профиль «%s» создан, подписка до %s.",
			username, expireAt.Format("02.01.2006")))
		s.notifyGiftRedeemed(ctx, st.giftID, chatID, username)
		return
	}

	created, err := s.creator.CreateUser(ctx, username, expireAt)
	if err != nil {
		s.logger.Error("redeem: create user failed", "username", username, "err", err.Error())
		if _, rerr := s.store.ReissueGiftCode(ctx, st.giftID); rerr != nil {
			s.logger.Error("redeem: rollback to issued failed", "gift_id", st.giftID, "err", rerr.Error())
		}
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка активации подарка. Попробуйте позже.")
		return
	}

	// The profile exists and is paid for; a failed Telegram binding is
	// recoverable via /register, so it must not fail the redemption.
	if s.registrar != nil {
		if err := s.registrar.SetTelegramID(ctx, created.UUID, chatID); err != nil {
			s.logger.Error("redeem: set telegram id failed", "uuid", created.UUID, "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID,
				"Профиль создан, но привязать Telegram не удалось. Используйте /register для привязки.")
		}
	}

	s.clearRedeem(chatID)
	msg := fmt.Sprintf("✅ Подарок активирован! Профиль «%s» создан, подписка до %s.",
		created.Username, expireAt.Format("02.01.2006"))
	if created.SubscriptionURL != "" {
		msg += "\n\nВаша ссылка на подписку:\n" + created.SubscriptionURL
	}
	_ = s.bot.SendPlain(ctx, chatID, msg)
	s.notifyGiftRedeemed(ctx, st.giftID, chatID, created.Username)
}

// notifyGiftRedeemed tells the buyer their gift was activated. Skipped when
// the buyer redeemed their own code.
func (s *Service) notifyGiftRedeemed(ctx context.Context, giftID, redeemerTGID int64, username string) {
	g, err := s.store.GetGiftCode(ctx, giftID)
	if err != nil || g == nil {
		if err != nil {
			s.logger.Error("redeem: load gift for notify failed", "err", err.Error())
		}
		return
	}
	if g.BuyerTelegramID == 0 || g.BuyerTelegramID == redeemerTGID {
		return
	}
	_ = s.bot.SendPlain(ctx, g.BuyerTelegramID,
		fmt.Sprintf("🎁 Ваш подарочный код %s активирован: подписка «%s».", g.Code, username))
}
