package payments

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

type inviteState struct {
	inviterName string
	inviterTGID int64
	newUsername string // empty = still awaiting username input
	price       int
	createdAt   time.Time
}

const inviteTTL = 10 * time.Minute

func (s *Service) getInvite(chatID int64) *inviteState {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv := s.invites[chatID]
	if inv == nil {
		return nil
	}
	if s.now().Sub(inv.createdAt) > inviteTTL {
		delete(s.invites, chatID)
		return nil
	}
	return inv
}

func (s *Service) setInvite(chatID int64, inv *inviteState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invites[chatID] = inv
}

func (s *Service) clearInvite(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.invites, chatID)
}

// StartInviteFlow handles /invite. Returns true if the message was consumed.
func (s *Service) StartInviteFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil || !s.isEnabled() || s.creator == nil {
		return false
	}
	s.beginInviteFlow(ctx, m.Chat.ID)
	return true
}

func (s *Service) beginInviteFlow(ctx context.Context, chatID int64) {
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("invite: find inviter failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return
	}
	if len(subs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, "Эта команда доступна только подписчикам.")
		return
	}

	s.setInvite(chatID, &inviteState{
		inviterName: subs[0].Username,
		inviterTGID: chatID,
		createdAt:   s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		"Введите желаемое имя пользователя для нового участника. /cancel — отмена.")
}

// handleMenuInvite starts the invite flow from the menu button.
func (s *Service) handleMenuInvite(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginInviteFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// handleInviteUsernameInput processes free-text input during an active invite flow.
// Returns true if the message was consumed.
func (s *Service) handleInviteUsernameInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	inv := s.getInvite(chatID)
	if inv == nil {
		return false
	}

	text := strings.TrimSpace(m.Text)

	// If username already set, user sent a text message instead of tapping a button — re-show.
	if inv.newUsername != "" {
		s.showInviteConfirm(ctx, chatID, inv)
		return true
	}

	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID,
			"Введите имя пользователя или /cancel для отмены.")
		return true
	}

	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			"Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов.")
		return true
	}

	tariff, err := s.store.GetTariff(ctx, 1)
	if err != nil {
		s.logger.Error("invite: get tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, "Ошибка, попробуйте позже.")
		return true
	}
	if tariff == nil {
		// No 1-month tariff configured; proceed with price 0
		tariff = &store.Tariff{Months: 1, Price: 0}
	}

	inv.newUsername = text
	inv.price = tariff.Price
	s.setInvite(chatID, inv)
	s.showInviteConfirm(ctx, chatID, inv)
	return true
}

func (s *Service) showInviteConfirm(ctx context.Context, chatID int64, inv *inviteState) {
	priceStr := "бесплатно"
	if inv.price > 0 {
		priceStr = s.priceLabel(inv.price)
	}
	text := fmt.Sprintf(
		"Создать пользователя «%s»?\nСтоимость: 1 мес. — %s",
		inv.newUsername, priceStr,
	)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: "Отправить заявку", CallbackData: "inv_submit"}},
			{{Text: "Отмена", CallbackData: "inv_cancel"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleInviteSubmit processes the "Отправить заявку" button press.
func (s *Service) handleInviteSubmit(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка.")
		return true
	}
	chatID := cb.Message.Chat.ID
	inv := s.getInvite(chatID)
	if inv == nil || inv.newUsername == "" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Сессия истекла. Запустите /invite заново.")
		return true
	}

	reqID, err := s.store.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: inv.inviterTGID,
		InviterUsername:   inv.inviterName,
		NewUsername:       inv.newUsername,
		Months:            1,
		Price:             inv.price,
		Status:            "pending",
	})
	if err != nil {
		s.logger.Error("invite: create request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}

	inviterName := inv.inviterName
	inviterTGID := inv.inviterTGID
	newUsername := inv.newUsername
	price := inv.price

	s.clearInvite(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отправлена администратору.")

	priceStr := "бесплатно"
	if price > 0 {
		priceStr = s.priceLabel(price)
	}
	text := fmt.Sprintf(
		"👤 Заявка на приглашение\n\nОт: %s (TG %d)\nНовый пользователь: %s\nСтоимость: 1 мес. — %s",
		inviterName, inviterTGID, newUsername, priceStr,
	)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("inv_ok:%d", reqID)},
			{Text: "❌ Отклонить", CallbackData: fmt.Sprintf("inv_rej:%d", reqID)},
		}},
	}
	for _, adminID := range s.adminIDs {
		if _, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb); err != nil {
			s.logger.Error("invite: notify admin failed", "admin_id", adminID, "err", err.Error())
		}
	}
	return true
}

// handleInviteApprove processes admin's "Одобрить" button.
func (s *Service) handleInviteApprove(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized invite approve attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "inv_ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	req, err := s.store.GetInviteRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("invite: get request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if req == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if req.Status != "pending" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка уже обработана.")
		return true
	}

	expireAt := s.now().AddDate(0, req.Months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would create user", "username", req.NewUsername, "expire_at", expireAt.Format("2006-01-02"))
		_, _ = s.store.ResolveInviteRequest(ctx, reqID, "approved", s.now())
		if cb.Message != nil {
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
		}
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Пользователь создан (dry-run).")
		_ = s.bot.SendPlain(ctx, cb.From.ID,
			fmt.Sprintf("✅ (dry-run) Пользователь «%s» создан, подписка до %s.",
				req.NewUsername, expireAt.Format("02.01.2006")))
		if req.InviterTelegramID != 0 {
			_ = s.bot.SendPlain(ctx, req.InviterTelegramID,
				fmt.Sprintf("✅ Ваша заявка одобрена! Пользователь «%s» создан (dry-run).", req.NewUsername))
		}
		return true
	}

	created, err := s.creator.CreateUser(ctx, req.NewUsername, expireAt)
	if err != nil {
		s.logger.Error("invite: create user in panel failed", "username", req.NewUsername, "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка создания пользователя. Проверьте логи.")
		return true
	}

	if _, err := s.store.ResolveInviteRequest(ctx, reqID, "approved", s.now()); err != nil {
		s.logger.Error("invite: mark approved failed", "err", err.Error())
	}

	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "✅ Пользователь создан!")
	_ = s.bot.SendPlain(ctx, cb.From.ID,
		fmt.Sprintf("✅ Пользователь «%s» создан (UUID: %s), подписка до %s.",
			created.Username, created.UUID, expireAt.Format("02.01.2006")))

	if req.InviterTelegramID != 0 {
		msg := fmt.Sprintf("✅ Заявка одобрена! Пользователь «%s» создан, подписка до %s.",
			created.Username, expireAt.Format("02.01.2006"))
		if created.SubscriptionURL != "" {
			msg += "\n\nСсылка на подписку для нового пользователя:\n" + created.SubscriptionURL
		}
		_ = s.bot.SendPlain(ctx, req.InviterTelegramID, msg)
	}
	return true
}

// handleInviteReject processes admin's "Отклонить" button.
func (s *Service) handleInviteReject(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized invite reject attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав.")
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "inv_rej:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	req, err := s.store.GetInviteRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("invite: get request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Ошибка, попробуйте позже.")
		return true
	}
	if req == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка не найдена.")
		return true
	}
	if req.Status != "pending" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка уже обработана.")
		return true
	}

	if _, err := s.store.ResolveInviteRequest(ctx, reqID, "rejected", s.now()); err != nil {
		s.logger.Error("invite: mark rejected failed", "err", err.Error())
	}

	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Заявка отклонена.")
	_ = s.bot.SendPlain(ctx, cb.From.ID,
		fmt.Sprintf("❌ Заявка на пользователя «%s» отклонена.", req.NewUsername))

	if req.InviterTelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.InviterTelegramID,
			fmt.Sprintf("❌ Ваша заявка на пользователя «%s» отклонена администратором.", req.NewUsername))
	}
	return true
}

// handleInviteCancel processes the "Отмена" button shown during username confirmation.
func (s *Service) handleInviteCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearInvite(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "Отменено.")
	return true
}

// isValidUsername accepts Remnawave usernames: 3-32 chars, letters/digits/underscore.
func isValidUsername(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}
