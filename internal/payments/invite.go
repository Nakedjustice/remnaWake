package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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
	// Guard the menu-button path too: a stale button could be tapped after the
	// admin list was cleared, which would otherwise orphan a pending request.
	if !s.isEnabled() {
		return
	}
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("invite: find inviter failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	if len(subs) == 0 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Эта команда доступна только подписчикам."))
		return
	}

	s.setInvite(chatID, &inviteState{
		inviterName: subs[0].Username,
		inviterTGID: chatID,
		createdAt:   s.now(),
	})
	_ = s.bot.SendPlain(ctx, chatID,
		i18n.T("Введите желаемое имя пользователя для нового участника. /cancel — отмена."))
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
			i18n.T("Введите имя пользователя или /cancel для отмены."))
		return true
	}

	if !isValidUsername(text) {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов."))
		return true
	}

	tariff, err := s.store.GetTariff(ctx, 1)
	if err != nil {
		s.logger.Error("invite: get tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
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
	priceStr := i18n.T("бесплатно")
	if inv.price > 0 {
		priceStr = s.priceLabel(inv.price)
	}
	text := fmt.Sprintf(
		i18n.T("Создать пользователя «%s»?\nСтоимость: 1 мес. — %s"),
		inv.newUsername, priceStr,
	)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("Отправить заявку"), CallbackData: "inv_submit"}},
			{{Text: i18n.T("Отмена"), CallbackData: "inv_cancel"}},
		},
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

// handleInviteSubmit processes the "Отправить заявку" button press.
func (s *Service) handleInviteSubmit(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка."))
		return true
	}
	chatID := cb.Message.Chat.ID
	inv := s.getInvite(chatID)
	if inv == nil || inv.newUsername == "" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Сессия истекла. Запустите /invite заново."))
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
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}

	inviterName := inv.inviterName
	inviterTGID := inv.inviterTGID
	newUsername := inv.newUsername
	price := inv.price

	s.clearInvite(chatID)
	_ = s.bot.EditMessageReplyMarkup(ctx, chatID, cb.Message.MessageID, nil)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отправлена администратору."))

	s.notifyAdminsInviteRequest(ctx, reqID, inviterName, inviterTGID, newUsername, price)
	return true
}

// notifyAdminsInviteRequest sends every admin the pending invite request with
// approve/reject buttons and remembers the message refs so resolving clears
// the buttons in all admin chats. Shared by the chat flow and the mini app.
func (s *Service) notifyAdminsInviteRequest(ctx context.Context, reqID int64, inviterName string, inviterTGID int64, newUsername string, price int) {
	priceStr := i18n.T("бесплатно")
	if price > 0 {
		priceStr = s.priceLabel(price)
	}
	text := fmt.Sprintf(
		i18n.T("👤 Заявка на приглашение\n\nОт: %s (TG %d)\nНовый пользователь: %s\nСтоимость: 1 мес. — %s"),
		inviterName, inviterTGID, newUsername, priceStr,
	)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✅ Одобрить"), CallbackData: fmt.Sprintf("inv_ok:%d", reqID)},
			{Text: i18n.T("❌ Отклонить"), CallbackData: fmt.Sprintf("inv_rej:%d", reqID)},
		}},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		if err != nil {
			s.logger.Error("invite: notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	s.putAdminMsgs(s.inviteMsgs, reqID, refs)
}

// handleInviteApprove processes admin's "Одобрить" button.
func (s *Service) handleInviteApprove(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized invite approve attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "inv_ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	req, created, expireAt, err := s.approveInviteRequest(ctx, reqID)
	if errors.Is(err, ErrPanelCreateFailed) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка создания пользователя. Проверьте логи."))
		return true
	}
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, resolveErrorText(err))
		return true
	}

	if created == nil { // dry-run
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Пользователь создан (dry-run)."))
		_ = s.bot.SendPlain(ctx, cb.From.ID,
			fmt.Sprintf(i18n.T("✅ (dry-run) Пользователь «%s» создан, подписка до %s."),
				req.NewUsername, expireAt.Format("02.01.2006")))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Пользователь создан!"))
	_ = s.bot.SendPlain(ctx, cb.From.ID,
		fmt.Sprintf(i18n.T("✅ Пользователь «%s» создан (UUID: %s), подписка до %s."),
			created.Username, created.UUID, expireAt.Format("02.01.2006")))
	return true
}

// approveInviteRequest approves a pending invite: creates the user in the
// panel (skipped in dry-run, where created is nil), marks the request
// approved, clears the approve buttons in every admin's chat and notifies the
// inviter. Shared by the bot callback and the mini app admin API; admin-facing
// notifications stay with each transport.
func (s *Service) approveInviteRequest(ctx context.Context, reqID int64) (*store.InviteRequest, *CreatedUser, time.Time, error) {
	req, err := s.store.GetInviteRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("invite: get request failed", "err", err.Error())
		return nil, nil, time.Time{}, fmt.Errorf("get invite request: %w", err)
	}
	if req == nil {
		return nil, nil, time.Time{}, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return nil, nil, time.Time{}, ErrRequestResolved
	}

	expireAt := s.now().AddDate(0, req.Months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would create user", "username", req.NewUsername, "expire_at", expireAt.Format("2006-01-02"))
		_, _ = s.store.ResolveInviteRequest(ctx, reqID, "approved", s.now())
		s.clearInviteButtons(ctx, reqID)
		if req.InviterTelegramID != 0 {
			_ = s.bot.SendPlain(ctx, req.InviterTelegramID,
				fmt.Sprintf(i18n.T("✅ Ваша заявка одобрена! Пользователь «%s» создан (dry-run)."), req.NewUsername))
		}
		return req, nil, expireAt, nil
	}

	squadUUID, err := s.resolveDefaultSquadUUID(ctx)
	if err != nil {
		s.logger.Error("invite: resolve default squad failed", "username", req.NewUsername, "err", err.Error())
		return req, nil, expireAt, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
	}

	created, err := s.creator.CreateUser(ctx, req.NewUsername, expireAt, []string{squadUUID})
	if err != nil {
		s.logger.Error("invite: create user in panel failed", "username", req.NewUsername, "err", err.Error())
		return req, nil, expireAt, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
	}

	if _, err := s.store.ResolveInviteRequest(ctx, reqID, "approved", s.now()); err != nil {
		s.logger.Error("invite: mark approved failed", "err", err.Error())
	}

	s.clearInviteButtons(ctx, reqID)
	if req.InviterTelegramID != 0 {
		msg := fmt.Sprintf(i18n.T("✅ Заявка одобрена! Пользователь «%s» создан, подписка до %s."),
			created.Username, expireAt.Format("02.01.2006"))
		if created.SubscriptionURL != "" {
			msg += i18n.T("\n\nСсылка на подписку для нового пользователя:\n") + created.SubscriptionURL
		}
		_ = s.bot.SendPlain(ctx, req.InviterTelegramID, msg)
	}
	return req, created, expireAt, nil
}

// handleInviteReject processes admin's "Отклонить" button.
func (s *Service) handleInviteReject(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized invite reject attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "inv_rej:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	req, err := s.rejectInviteRequest(ctx, reqID)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, resolveErrorText(err))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отклонена."))
	_ = s.bot.SendPlain(ctx, cb.From.ID,
		fmt.Sprintf(i18n.T("❌ Заявка на пользователя «%s» отклонена."), req.NewUsername))
	return true
}

// rejectInviteRequest rejects a pending invite: marks it rejected, clears the
// approve buttons in every admin's chat and notifies the inviter. Shared by
// the bot callback and the mini app admin API.
func (s *Service) rejectInviteRequest(ctx context.Context, reqID int64) (*store.InviteRequest, error) {
	req, err := s.store.GetInviteRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("invite: get request failed", "err", err.Error())
		return nil, fmt.Errorf("get invite request: %w", err)
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return nil, ErrRequestResolved
	}

	if _, err := s.store.ResolveInviteRequest(ctx, reqID, "rejected", s.now()); err != nil {
		s.logger.Error("invite: mark rejected failed", "err", err.Error())
	}

	s.clearInviteButtons(ctx, reqID)
	if req.InviterTelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.InviterTelegramID,
			fmt.Sprintf(i18n.T("❌ Ваша заявка на пользователя «%s» отклонена администратором."), req.NewUsername))
	}
	return req, nil
}

// clearInviteButtons removes the approve/reject buttons from every admin's copy
// of the invite notification for reqID, then forgets the stored refs.
func (s *Service) clearInviteButtons(ctx context.Context, reqID int64) {
	refs := s.takeAdminMsgs(s.inviteMsgs, reqID)
	for _, ref := range refs {
		if err := s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil); err != nil {
			s.logger.Warn("clear admin invite button failed", "chat_id", ref.chatID, "err", err.Error())
		}
	}
}

// handleInviteCancel processes the "Отмена" button shown during username confirmation.
func (s *Service) handleInviteCancel(ctx context.Context, cb *tg.CallbackQuery) bool {
	if cb.Message != nil {
		s.clearInvite(cb.Message.Chat.ID)
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Отменено."))
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
