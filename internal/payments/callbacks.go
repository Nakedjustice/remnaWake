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
	case cb.Data == "menu:tariffs":
		return s.handleMenuTariffs(ctx, cb)
	case cb.Data == "menu:cabinet":
		return s.handleMenuCabinet(ctx, cb)
	case strings.HasPrefix(cb.Data, "cab:pay:"):
		return s.handleCabinetPay(ctx, cb)
	case cb.Data == "cab:cancel":
		return s.handleCabinetCancel(ctx, cb)
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
	case cb.Data == "menu:gift":
		return s.handleMenuGift(ctx, cb)
	case cb.Data == "menu:mygifts":
		return s.handleMenuMyGifts(ctx, cb)
	case cb.Data == "mg:menu":
		return s.handleMyGiftsMenu(ctx, cb)
	case strings.HasPrefix(cb.Data, "mg:list:"):
		return s.handleMyGiftsList(ctx, cb)
	case cb.Data == "mg:noop":
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
		return true
	case strings.HasPrefix(cb.Data, "gc_link:"):
		return s.handleGiftCodeResend(ctx, cb)
	case strings.HasPrefix(cb.Data, "gc_pick:"):
		return s.handleGiftCodePick(ctx, cb)
	case cb.Data == "gc_cancel":
		return s.handleGiftCodeCancel(ctx, cb)
	case strings.HasPrefix(cb.Data, "gc_ok:"):
		return s.handleGiftCodeApprove(ctx, cb)
	case strings.HasPrefix(cb.Data, "gc_rej:"):
		return s.handleGiftCodeReject(ctx, cb)
	case strings.HasPrefix(cb.Data, "gc_use:"):
		return s.handleGiftUse(ctx, cb)
	case cb.Data == "gc_redeem_cancel":
		return s.handleGiftRedeemCancel(ctx, cb)
	case strings.HasPrefix(cb.Data, "adm:"):
		return s.handleAdminMenu(ctx, cb)
	default:
		return false
	}
}

func (s *Service) handlePay(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "pay:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	// Show payment requisites to the user, if the admin has set any.
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	if req != "" && cb.Message != nil {
		_ = s.bot.SendPlain(ctx, cb.Message.Chat.ID, i18n.T("Реквизиты для оплаты:\n\n")+req)
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
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
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Выберите количество месяцев."))
	return true
}

func (s *Service) handlePick(ctx context.Context, cb *tg.CallbackQuery) bool {
	parts := strings.Split(strings.TrimPrefix(cb.Data, "pick:"), ":")
	if len(parts) != 2 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}

	tariff, err := s.store.GetTariff(ctx, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}

	s.createRequestAndNotify(ctx, cb, userID, months, tariff.Price)
	return true
}

func (s *Service) handleBack(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "back:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.PaymentButton(userID))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	return true
}

// createRequestAndNotify looks up the remembered user, writes a pending request,
// clears the user's keyboard, and DMs all admins a confirm button.
func (s *Service) createRequestAndNotify(ctx context.Context, cb *tg.CallbackQuery, userID int64, months, price int) {
	u, err := s.store.GetNotifiedUser(ctx, userID)
	if err != nil {
		s.logger.Error("get notified user failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}
	if u == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось найти данные. Дождитесь следующего уведомления."))
		return
	}

	if _, err := s.createPaymentRequest(ctx, u, months, price); err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}

	// Clear the user's keyboard and acknowledge.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отправлена администратору."))
}

// createPaymentRequest writes a pending payment request and DMs all admins a
// confirm button. Shared by the chat callback flow and the mini app API.
func (s *Service) createPaymentRequest(ctx context.Context, u *store.NotifiedUser, months, price int) (int64, error) {
	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending",
	})
	if err != nil {
		s.logger.Error("create payment request failed", "err", err.Error())
		return 0, err
	}

	// Notify all admins with details + confirm button.
	text := s.formatAdminRequest(u, months, price)
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("Подтвердить оплату"), CallbackData: fmt.Sprintf("ok:%d", reqID)}},
		},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		if err != nil {
			s.logger.Error("notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	s.putAdminMsgs(s.payMsgs, reqID, refs)
	return reqID, nil
}

func (s *Service) handleConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized confirm attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав для подтверждения оплаты."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	req, newExpireAt, err := s.confirmPaymentRequest(ctx, reqID)
	switch {
	case errors.Is(err, ErrRequestNotFound):
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка не найдена."))
		return true
	case errors.Is(err, ErrRequestResolved):
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Подписка уже была продлена."))
		return true
	case errors.Is(err, ErrConfirmedNotMarked):
		s.logger.Error("confirm payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Подписка продлена, но статус заявки не обновился."))
		_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(
			i18n.T("⚠️ Подписка для %s продлена до %s, но заявку №%d не удалось отметить подтверждённой в базе. Не подтверждайте её повторно — это продлит подписку ещё раз."),
			req.Username, newExpireAt.Format("02.01.2006"), reqID))
		return true
	case err != nil:
		s.logger.Error("confirm payment request failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка продления подписки. Проверьте логи."))
		if req != nil {
			_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(
				i18n.T("❌ Не удалось продлить подписку для %s (заявка №%d): ошибка панели.\nЗаявка осталась в ожидании — попробуйте подтвердить ещё раз."),
				req.Username, reqID))
		}
		return true
	}

	if s.dryRun {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Подписка продлена (dry-run)."))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Подписка продлена!"))
	_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(i18n.T("✅ Подписка для %s продлена на %d мес. до %s"),
		req.Username, req.Months, newExpireAt.Format("02.01.2006")))
	return true
}

// clearPayButtons removes the confirm button from every admin's copy of the
// payment notification for reqID, then forgets the stored refs.
func (s *Service) clearPayButtons(ctx context.Context, reqID int64) {
	refs := s.takeAdminMsgs(s.payMsgs, reqID)
	for _, ref := range refs {
		if err := s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil); err != nil {
			s.logger.Warn("clear admin confirm button failed", "chat_id", ref.chatID, "err", err.Error())
		}
	}
}

func (s *Service) tariffKeyboard(userID int64, tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf(i18n.T("%d мес. — %s"), t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("pick:%d:%d", userID, t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: i18n.T("← Назад"), CallbackData: fmt.Sprintf("back:%d", userID),
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) handleAdminMenu(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	chatID := cb.From.ID
	switch {
	case cb.Data == "adm:menu":
		s.SendAdminMenu(ctx, chatID)
	case cb.Data == "adm:tariffs":
		s.sendAdminTariffs(ctx, chatID)
	case cb.Data == "adm:stats":
		s.sendAdminStats(ctx, chatID)
	case cb.Data == "adm:del_list":
		s.sendAdminDelList(ctx, chatID)
	case cb.Data == "adm:req":
		s.sendAdminRequisites(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:del:"):
		s.handleAdminDelTariff(ctx, chatID, cb.Data)
	case cb.Data == "adm:setreq":
		s.startSetRequisitesFlow(ctx, chatID)
	case cb.Data == "adm:addtariff":
		s.startAddTariffFlow(ctx, chatID)
	case cb.Data == "adm:gifts":
		s.sendAdminGiftList(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:grev:"):
		s.handleAdminGiftRevoke(ctx, chatID, cb.Data)
	case cb.Data == "adm:bcast":
		s.startBroadcastFlow(ctx, chatID)
	case cb.Data == "adm:bc_send":
		s.handleBroadcastSend(ctx, chatID, cb)
	case cb.Data == "adm:bc_cancel":
		s.handleBroadcastCancel(ctx, chatID, cb)
	}
	return true
}

func (s *Service) sendAdminTariffs(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("admin: list tariffs failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка чтения тарифов."))
		return
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
		},
	}
	if len(tariffs) == 0 {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Тарифы не заданы."), kb)
		return
	}
	var b strings.Builder
	b.WriteString(i18n.T("Тарифы:\n"))
	for _, t := range tariffs {
		b.WriteString(fmt.Sprintf(i18n.T("%d мес. — %s\n"), t.Months, s.priceLabel(t.Price)))
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, strings.TrimRight(b.String(), "\n"), kb)
}

func (s *Service) sendAdminDelList(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("admin: list tariffs for delete failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка чтения тарифов."))
		return
	}
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf(i18n.T("%d мес. — %s"), t.Months, s.priceLabel(t.Price)),
			CallbackData: fmt.Sprintf("adm:del:%d", t.Months),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	text := i18n.T("Выберите тариф для удаления:")
	if len(tariffs) == 0 {
		text = i18n.T("Тарифы не заданы.")
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (s *Service) sendAdminRequisites(ctx context.Context, chatID int64) {
	s.mu.Lock()
	req := s.requisites
	s.mu.Unlock()
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
		},
	}
	text := i18n.T("Реквизиты не заданы.")
	if req != "" {
		text = i18n.T("Реквизиты для оплаты:\n\n") + req
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

func (s *Service) handleAdminDelTariff(ctx context.Context, chatID int64, data string) {
	monthsStr := strings.TrimPrefix(data, "adm:del:")
	months, err := strconv.Atoi(monthsStr)
	if err != nil || months < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось распознать тариф."))
		return
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		s.logger.Error("admin: delete tariff failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка удаления тарифа."))
		return
	}
	if !deleted {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф на %d мес. не найден."), months))
	} else {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Тариф на %d мес. удалён."), months))
	}
	s.sendAdminDelList(ctx, chatID)
}

func (s *Service) startSetRequisitesFlow(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputRequisites
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте новый текст реквизитов:"))
}

func (s *Service) startBroadcastFlow(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputBroadcast
	state.pendingBroadcast = ""
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте текст рассылки следующим сообщением."))
}

func (s *Service) handleBroadcastSend(ctx context.Context, chatID int64, cb *tg.CallbackQuery) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	text := state.pendingBroadcast
	state.pendingBroadcast = ""
	s.adminInput[chatID] = state
	s.mu.Unlock()
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	if text == "" {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Нет текста для рассылки. Начните заново через меню."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Рассылка запущена…"))
	// The update loop is sequential; a large broadcast paced at ~20 msg/s would
	// block the bot for the whole run, so send in the background and report.
	go func() {
		sent, failed, err := s.broadcastMessage(ctx, text)
		if err != nil {
			s.logger.Error("broadcast: list users failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения списка пользователей, рассылка не выполнена."))
			return
		}
		_ = s.bot.SendPlain(ctx, chatID,
			fmt.Sprintf(i18n.T("Рассылка завершена: отправлено %d, ошибок %d."), sent, failed))
	}()
}

func (s *Service) handleBroadcastCancel(ctx context.Context, chatID int64, cb *tg.CallbackQuery) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.pendingBroadcast = ""
	if state.step == adminInputBroadcast {
		state.step = adminInputNone
	}
	s.adminInput[chatID] = state
	s.mu.Unlock()
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Рассылка отменена."))
}

func (s *Service) startAddTariffFlow(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputTariffMonths
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите количество месяцев (целое ≥ 1):"))
}

func (s *Service) formatAdminRequest(u *store.NotifiedUser, months, price int) string {
	var b strings.Builder
	b.WriteString(i18n.T("💳 Заявка на оплату\n\n"))
	b.WriteString(i18n.T("Клиент: ") + u.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", u.RemnawaveID))
	b.WriteString("UUID: " + u.UUID + "\n")
	b.WriteString(fmt.Sprintf("Telegram ID: %d\n", u.TelegramID))
	b.WriteString(i18n.T("Подписка до: ") + u.ExpireAt.Format("02.01.2006") + "\n")
	if price > 0 {
		b.WriteString(fmt.Sprintf(i18n.T("Выбрано: %d мес. — %s"), months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf(i18n.T("Выбрано: %d мес."), months))
	}
	return b.String()
}
