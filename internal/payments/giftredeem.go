package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Код не найден или недействителен."))
		return
	}

	g, err := s.store.GetGiftCodeByCode(ctx, code)
	if err != nil {
		s.logger.Error("redeem: get gift code failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	if g == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Код не найден или недействителен."))
		return
	}
	switch g.Status {
	case "issued":
		// proceed
	case "pending":
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Оплата этого подарка ещё не подтверждена. Попробуйте позже."))
		return
	case "redeemed":
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот код уже был активирован."))
		return
	default: // revoked, rejected
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Код недействителен."))
		return
	}

	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("redeem: find redeemer failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
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
			i18n.T("🎁 Вам подарили подписку на %d мес.!\n\nВведите желаемое имя пользователя для вашего профиля (буквы, цифры и «_», от 3 до 32 символов). /cancel — отмена."),
			g.Months))
	case 1:
		// Existing subscriber: ask for confirmation so an accidental tap on
		// the deep link doesn't consume the gift.
		st.candidates = subs
		s.setRedeem(chatID, st)
		kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("✅ Активировать"), CallbackData: "gc_use:0"}},
			{{Text: i18n.T("Отмена"), CallbackData: "gc_redeem_cancel"}},
		}}
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
			fmt.Sprintf(i18n.T("🎁 Вам подарили подписку на %d мес.\n\nАктивировать для профиля «%s» (подписка до %s)? Срок будет продлён на %d мес."),
				g.Months, subs[0].Username, subs[0].ExpireAt.Format("02.01.2006"), g.Months),
			kb)
	default:
		// Several profiles on this Telegram ID: let the user pick which to extend.
		st.candidates = subs
		s.setRedeem(chatID, st)
		rows := make([][]tg.InlineKeyboardButton, 0, len(subs)+1)
		for i, sub := range subs {
			rows = append(rows, []tg.InlineKeyboardButton{{
				Text:         fmt.Sprintf(i18n.T("%s (до %s)"), sub.Username, sub.ExpireAt.Format("02.01.2006")),
				CallbackData: fmt.Sprintf("gc_use:%d", i),
			}})
		}
		rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("Отмена"), CallbackData: "gc_redeem_cancel"}})
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
			fmt.Sprintf(i18n.T("🎁 Вам подарили подписку на %d мес. Выберите профиль для продления:"), g.Months),
			&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
	}
}

// handleGiftUse processes the profile choice when the redeemer has several.
func (s *Service) handleGiftUse(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}
	chatID := cb.Message.Chat.ID
	st := s.getRedeem(chatID)
	if st == nil || len(st.candidates) == 0 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Сессия истекла. Откройте ссылку ещё раз."))
		return true
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "gc_use:"))
	if err != nil || idx < 0 || idx >= len(st.candidates) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
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
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Отменено."))
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
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите имя пользователя или /cancel для отмены."))
		return true
	}
	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов."))
		return true
	}

	existing, err := s.finder.FindByUsername(ctx, text)
	if err != nil {
		s.logger.Error("redeem: check username failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if existing != nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Это имя занято, попробуйте другое."))
		return true
	}

	s.redeemCreate(ctx, chatID, st, text)
	return true
}

// redeemExtend claims the code, then extends an existing subscription. The
// claim happens first so a concurrent redemption can never double-spend; if
// the panel call fails afterwards, the code is rolled back to issued.
func (s *Service) redeemExtend(ctx context.Context, chatID int64, st *redeemState, sub Subscriber) {
	result, err := s.redeemGiftExtend(ctx, chatID, st, sub)
	if err != nil {
		if errors.Is(err, ErrGiftUsed) {
			s.clearRedeem(chatID)
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот код уже был активирован."))
			return
		}
		s.logger.Error("redeem: extend failed", "uuid", sub.UUID, "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка активации подарка. Попробуйте позже."))
		return
	}
	s.clearRedeem(chatID)
	key := "✅ Подарок активирован! Подписка «%s» продлена на %d мес. до %s."
	if s.dryRun {
		key = "✅ (dry-run) Подарок активирован! Подписка «%s» продлена на %d мес. до %s."
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T(key), sub.Username, st.months, result.ExpireAt))
}

// redeemCreate claims the code, then creates a fresh panel profile bound to
// the redeemer's Telegram ID and delivers the subscription link.
func (s *Service) redeemCreate(ctx context.Context, chatID int64, st *redeemState, username string) {
	result, err := s.redeemGiftCreate(ctx, chatID, st, username)
	if err != nil {
		if errors.Is(err, ErrGiftUsed) {
			s.clearRedeem(chatID)
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот код уже был активирован."))
			return
		}
		s.logger.Error("redeem: create failed", "username", username, "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка активации подарка. Попробуйте позже."))
		return
	}
	s.clearRedeem(chatID)
	key := "✅ Подарок активирован! Профиль «%s» создан, подписка до %s."
	if s.dryRun {
		key = "✅ (dry-run) Подарок активирован! Профиль «%s» создан, подписка до %s."
	}
	msg := fmt.Sprintf(i18n.T(key), result.Username, result.ExpireAt)
	if result.LinkFailed {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Профиль создан, но привязать Telegram не удалось. Используйте /register для привязки."))
	}
	if result.SubscriptionURL != "" {
		msg += i18n.T("\n\nВаша ссылка на подписку:\n") + result.SubscriptionURL
	}
	_ = s.bot.SendPlain(ctx, chatID, msg)
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
		fmt.Sprintf(i18n.T("🎁 Ваш подарочный код %s активирован: подписка «%s»."), g.Code, username))
}

// RedeemGift activates an issued gift for an owned profile. When the user has
// no linked profiles it creates and links a new one, matching the bot flow.
func (s *Service) RedeemGift(ctx context.Context, telegramID int64, rawCode string, remnawaveID int64, username string) (*WebGiftRedemptionResult, error) {
	code := strings.ToUpper(strings.TrimSpace(rawCode))
	if !isValidGiftCodeFormat(code) {
		return nil, ErrGiftInvalid
	}
	g, err := s.store.GetGiftCodeByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("load gift: %w", err)
	}
	if g == nil || (g.Status != "issued" && g.Status != "redeemed") {
		return nil, ErrGiftInvalid
	}
	if g.Status == "redeemed" {
		return nil, ErrGiftUsed
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("%w: find profiles: %v", ErrPanelUnavailable, err)
	}
	st := &redeemState{giftID: g.ID, code: g.Code, months: g.Months, createdAt: s.now()}
	if len(subs) == 0 {
		username = strings.TrimSpace(username)
		if !isValidUsername(username) {
			return nil, ErrInvalidProfileQuery
		}
		existing, err := s.finder.FindByUsername(ctx, username)
		if err != nil {
			return nil, fmt.Errorf("%w: check username: %v", ErrPanelUnavailable, err)
		}
		if existing != nil {
			return nil, ErrUsernameTaken
		}
		return s.redeemGiftCreate(ctx, telegramID, st, username)
	}
	for _, sub := range subs {
		if sub.RemnawaveID == remnawaveID {
			return s.redeemGiftExtend(ctx, telegramID, st, sub)
		}
	}
	return nil, ErrProfileUnknown
}

func (s *Service) redeemGiftExtend(ctx context.Context, telegramID int64, st *redeemState, sub Subscriber) (*WebGiftRedemptionResult, error) {
	ok, err := s.store.RedeemGiftCode(ctx, st.code, telegramID, sub.Username, s.now())
	if err != nil {
		return nil, fmt.Errorf("claim gift: %w", err)
	}
	if !ok {
		return nil, ErrGiftUsed
	}
	base := sub.ExpireAt
	if base.Before(s.now()) {
		base = s.now()
	}
	newExpireAt := base.AddDate(0, st.months, 0)
	if !s.dryRun {
		if err := s.extender.ExtendSubscriptionByUUID(ctx, sub.UUID, newExpireAt); err != nil {
			_, _ = s.store.ReissueGiftCode(ctx, st.giftID)
			return nil, fmt.Errorf("%w: extend gift profile: %v", ErrPanelUnavailable, err)
		}
	}
	s.notifyGiftRedeemed(ctx, st.giftID, telegramID, sub.Username)
	return &WebGiftRedemptionResult{Username: sub.Username, SubscriptionURL: sub.SubscriptionURL, ExpireAt: newExpireAt.Format("02.01.2006")}, nil
}

func (s *Service) redeemGiftCreate(ctx context.Context, telegramID int64, st *redeemState, username string) (*WebGiftRedemptionResult, error) {
	if s.creator == nil {
		return nil, ErrPanelCreateFailed
	}
	ok, err := s.store.RedeemGiftCode(ctx, st.code, telegramID, username, s.now())
	if err != nil {
		return nil, fmt.Errorf("claim gift: %w", err)
	}
	if !ok {
		return nil, ErrGiftUsed
	}
	expireAt := s.now().AddDate(0, st.months, 0)
	if s.dryRun {
		s.notifyGiftRedeemed(ctx, st.giftID, telegramID, username)
		return &WebGiftRedemptionResult{Username: username, ExpireAt: expireAt.Format("02.01.2006")}, nil
	}
	squadUUID, err := s.resolveDefaultSquadUUID(ctx)
	if err != nil {
		_, _ = s.store.ReissueGiftCode(ctx, st.giftID)
		return nil, fmt.Errorf("resolve squad: %w", err)
	}
	created, err := s.creator.CreateUser(ctx, username, expireAt, []string{squadUUID}, s.getDefaultTrafficReset())
	if err != nil {
		_, _ = s.store.ReissueGiftCode(ctx, st.giftID)
		return nil, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
	}
	linkFailed := false
	if s.registrar != nil {
		if err := s.registrar.SetTelegramID(ctx, created.UUID, telegramID); err != nil {
			s.logger.Error("redeem web: set telegram id failed", "uuid", created.UUID, "err", err.Error())
			linkFailed = true
		}
	}
	s.notifyGiftRedeemed(ctx, st.giftID, telegramID, created.Username)
	return &WebGiftRedemptionResult{Username: created.Username, SubscriptionURL: created.SubscriptionURL, ExpireAt: expireAt.Format("02.01.2006"), LinkFailed: linkFailed}, nil
}
