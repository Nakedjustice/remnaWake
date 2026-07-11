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
	if s.registrationBlocked(ctx, cb.From.ID) && cb.Data != "menu:support" && cb.Data != "sup:close" {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Доступ к боту ограничен. Обратитесь в поддержку."))
		return true
	}
	switch {
	case strings.HasPrefix(cb.Data, "pay:"):
		return s.handlePay(ctx, cb)
	case strings.HasPrefix(cb.Data, "pick:"):
		return s.handlePick(ctx, cb)
	case strings.HasPrefix(cb.Data, "pln:"):
		return s.handlePlanPick(ctx, cb)
	case strings.HasPrefix(cb.Data, "back:"):
		return s.handleBack(ctx, cb)
	case strings.HasPrefix(cb.Data, "ok:"):
		return s.handleConfirm(ctx, cb)
	case strings.HasPrefix(cb.Data, "rej:"):
		return s.handleReject(ctx, cb)
	case cb.Data == "menu:tariffs":
		return s.handleMenuTariffs(ctx, cb)
	case cb.Data == "menu:cabinet":
		return s.handleMenuCabinet(ctx, cb)
	case strings.HasPrefix(cb.Data, "cab:pay:"):
		return s.handleCabinetPay(ctx, cb)
	case strings.HasPrefix(cb.Data, "tex:pay:"):
		return s.handleTrafficExtensionPay(ctx, cb)
	case strings.HasPrefix(cb.Data, "tex:pick:"):
		return s.handleTrafficExtensionPick(ctx, cb)
	case strings.HasPrefix(cb.Data, "plcheck:"):
		return s.handlePlategaCheck(ctx, cb)
	case strings.HasPrefix(cb.Data, "chg:"):
		return s.handlePlanChangeConfirm(ctx, cb)
	case strings.HasPrefix(cb.Data, "chgv:"):
		return s.handlePlanChangeProviderConfirm(ctx, cb)
	case strings.HasPrefix(cb.Data, "payvia:"):
		return s.handlePayVia(ctx, cb)
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
	case cb.Data == "menu:trial":
		return s.handleMenuTrial(ctx, cb)
	case cb.Data == "menu:referral":
		return s.handleMenuReferral(ctx, cb)
	case cb.Data == "notif:menu":
		return s.handleNotifMenu(ctx, cb)
	case cb.Data == "notif:expiry":
		return s.handleNotifToggle(ctx, cb, store.NotificationExpiry)
	case cb.Data == "notif:winback":
		return s.handleNotifToggle(ctx, cb, store.NotificationWinback)
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
	case strings.HasPrefix(cb.Data, "tr_ok:"):
		return s.handleTrialApprove(ctx, cb)
	case strings.HasPrefix(cb.Data, "tr_rej:"):
		return s.handleTrialReject(ctx, cb)
	case strings.HasPrefix(cb.Data, "gc_use:"):
		return s.handleGiftUse(ctx, cb)
	case cb.Data == "gc_redeem_cancel":
		return s.handleGiftRedeemCancel(ctx, cb)
	case cb.Data == "upd:install":
		return s.handleUpdateInstall(ctx, cb)
	case cb.Data == "upd:dismiss":
		return s.handleUpdateDismiss(ctx, cb)
	case cb.Data == "menu:support":
		return s.handleMenuSupport(ctx, cb)
	case cb.Data == "sup:close":
		return s.handleSupportClose(ctx, cb)
	case strings.HasPrefix(cb.Data, "sup:reply:"):
		return s.handleSupportReplyStart(ctx, cb)
	case strings.HasPrefix(cb.Data, guardUnblockCallbackPrefix):
		return s.handleRegistrationUnblock(ctx, cb)
	case strings.HasPrefix(cb.Data, "adm:infrapaid:"):
		return s.handleInfraPaid(ctx, cb)
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

	// With custom presets configured, let the user pick a plan first; the
	// months keyboard follows from the pln: callback. With only the built-in
	// standard plan the flow is unchanged.
	if plans, err := s.listPlans(ctx); err != nil {
		s.logger.Error("list plans failed", "err", err.Error())
	} else if len(plans) > 1 {
		if cb.Message != nil {
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.planKeyboard(userID, plans))
		}
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Выберите тариф."))
		return true
	}

	tariffs, err := s.store.ListTariffs(ctx, store.PlanStandard)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}

	if len(tariffs) == 0 {
		// Fallback: behave like the old single-button flow (1 month).
		s.createRequestAndNotify(ctx, cb, userID, 1, 0, store.PlanStandard, false)
		return true
	}

	// Show tariff options on the same message.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.tariffKeyboard(userID, store.PlanStandard, tariffs))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Выберите количество месяцев."))
	return true
}

// handlePlanPick shows the months keyboard for the preset the user picked from
// the plan chooser (pln:<userID>:<plan>).
func (s *Service) handlePlanPick(ctx context.Context, cb *tg.CallbackQuery) bool {
	parts := strings.SplitN(strings.TrimPrefix(cb.Data, "pln:"), ":", 2)
	if len(parts) != 2 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	plan, err := s.getPlan(ctx, parts[1])
	if errors.Is(err, ErrPlanUnknown) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}
	if err != nil {
		s.logger.Error("get plan failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}

	tariffs, err := s.store.ListTariffs(ctx, plan.Code)
	if err != nil {
		s.logger.Error("list tariffs failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if len(tariffs) == 0 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, s.tariffKeyboard(userID, plan.Code, tariffs))
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Выберите количество месяцев."))
	return true
}

// planKeyboard offers one button per purchasable preset (pln:<userID>:<code>).
func (s *Service) planKeyboard(userID int64, plans []store.Plan) *tg.InlineKeyboardMarkup {
	rows := make([][]tg.InlineKeyboardButton, 0, len(plans)+1)
	for _, p := range plans {
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         p.Name,
			CallbackData: fmt.Sprintf("pln:%d:%s", userID, p.Code),
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: i18n.T("← Назад"), CallbackData: fmt.Sprintf("back:%d", userID),
	}})
	return &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) handlePick(ctx context.Context, cb *tg.CallbackQuery) bool {
	// pick:<userID>:<months> (standard, incl. stale keyboards) or
	// pick:<userID>:<months>:<plan> for custom presets.
	parts := strings.Split(strings.TrimPrefix(cb.Data, "pick:"), ":")
	if len(parts) != 2 && len(parts) != 3 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	plan := store.PlanStandard
	if len(parts) == 3 {
		plan = parts[2]
	}

	tariff, err := s.store.GetTariff(ctx, plan, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}

	s.createRequestAndNotify(ctx, cb, userID, months, tariff.Price, plan, false)
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
func (s *Service) createRequestAndNotify(ctx context.Context, cb *tg.CallbackQuery, userID int64, months, price int, plan string, planChangeConfirmed bool) {
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

	// When more than one provider is enabled, let the user choose; otherwise
	// route straight to the only enabled provider.
	providers := s.enabledProviders()
	if len(providers) > 1 {
		s.routeProviderWithPlanChangeGate(ctx, cb, u, userID, months, price, plan, "", planChangeConfirmed)
		return
	}
	s.routeProviderWithPlanChangeGate(ctx, cb, u, userID, months, price, plan, providers[0], planChangeConfirmed)
}

// routeProvider dispatches a renewal to the given payment provider. Platega and
// Telegram Stars are automatic; p2p falls back to the manual admin flow.
func (s *Service) routeProvider(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, userID int64, months, price int, plan, provider string) {
	switch provider {
	case ProviderPlatega:
		s.startPlategaAndPrompt(ctx, cb, u, months, price, plan)
	case ProviderTelegramStars:
		s.startStarsAndPrompt(ctx, cb, u, months, price, plan)
	default:
		s.startP2PRequest(ctx, cb, u, userID, months, price, plan)
	}
}

// routeProviderWithPlanChangeGate forces plan switches through an explicit
// confirmation before any payment provider can create an invoice/request.
func (s *Service) routeProviderWithPlanChangeGate(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, userID int64, months, price int, plan, provider string, planChangeConfirmed bool) {
	if !planChangeConfirmed {
		req := &store.PaymentRequest{
			RemnawaveID: u.RemnawaveID,
			UUID:        u.UUID,
			Username:    u.Username,
			TelegramID:  u.TelegramID,
			Months:      months,
			Price:       price,
			ExpireAt:    u.ExpireAt,
			Plan:        plan,
		}
		if preview := s.renewalPlanChangePreview(ctx, req, true); preview != nil {
			s.promptPlanChangeConfirm(ctx, cb, userID, months, plan, provider, preview)
			return
		}
	}

	if provider == "" {
		s.promptProviderChoice(ctx, cb, userID, months, plan, s.enabledProviders(), planChangeConfirmed)
		return
	}
	s.routeProvider(ctx, cb, u, userID, months, price, plan, provider)
}

// startP2PRequest runs the manual admin-confirmed flow: with the screenshot
// requirement on it defers the request until the user attaches a payment photo,
// otherwise it creates a pending request and DMs the admins a confirm button.
func (s *Service) startP2PRequest(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, userID int64, months, price int, plan string) {
	// With the screenshot requirement on, the request is deferred until the
	// user sends a payment photo; nothing reaches the admins yet. A callback
	// can arrive without its message (inaccessible/too old) — the tariff
	// buttons live in the user's private chat, so cb.From.ID is the same chat.
	if s.getRequireScreenshot() {
		chatID := cb.From.ID
		if cb.Message != nil {
			chatID = cb.Message.Chat.ID
			_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
		}
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Прикрепите чек об оплате."))
		s.startPayPhotoFlow(ctx, chatID, userID, months, price, plan)
		return
	}

	if _, err := s.createPaymentRequest(ctx, u, months, price, plan, nil); err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return
	}

	// Clear the user's keyboard and acknowledge.
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отправлена администратору."))
}

// promptProviderChoice shows the user a keyboard with one button per enabled
// provider. Confirmed plan-change flows add :<plan>:ok so stale provider
// keyboards still pass through the confirmation gate.
func (s *Service) promptProviderChoice(ctx context.Context, cb *tg.CallbackQuery, userID int64, months int, plan string, providers []string, planChangeConfirmed bool) {
	rows := make([][]tg.InlineKeyboardButton, 0, len(providers))
	for _, p := range providers {
		data := fmt.Sprintf("payvia:%d:%d:%s", userID, months, p)
		if planChangeConfirmed {
			data += ":" + normalizePlan(plan) + ":ok"
		} else if plan != "" && plan != store.PlanStandard {
			data += ":" + plan
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         providerButtonLabel(p),
			CallbackData: data,
		}})
	}
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: rows}
	if cb.Message != nil {
		_ = s.bot.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID,
			i18n.T("Выберите способ оплаты:"), kb)
	} else {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, cb.From.ID, i18n.T("Выберите способ оплаты:"), kb)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
}

func (s *Service) promptPlanChangeConfirm(ctx context.Context, cb *tg.CallbackQuery, userID int64, months int, plan, provider string, preview *PlanChangePreview) {
	var text string
	switch preview.Kind {
	case PlanChangeDowngradeImmediate:
		text = fmt.Sprintf(
			i18n.T("⚠️ Переход на более дешёвый тариф\n\nВы переходите с «%s» на «%s». Оставшееся время не будет пересчитано: срок после покупки на %d мес. будет до %s. После подтверждения оплаты лимиты нового тарифа применятся сразу.\n\nПродолжить?"),
			preview.CurrentPlan, preview.TargetPlan, months, preview.NewExpireAt,
		)
	default:
		text = fmt.Sprintf(
			i18n.T("⚠️ Переход на более дорогой тариф\n\nВы переходите с «%s» на «%s». Оставшееся время до %s будет пересчитано по цене. После покупки на %d мес. подписка будет действовать до %s.\n\nПродолжить?"),
			preview.CurrentPlan, preview.TargetPlan, preview.CurrentExpireAt, months, preview.NewExpireAt,
		)
	}

	plan = normalizePlan(plan)
	confirmData := fmt.Sprintf("chg:%d:%d:%s", userID, months, plan)
	if provider != "" {
		confirmData = fmt.Sprintf("chgv:%d:%d:%s:%s", userID, months, provider, plan)
	}
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{{
		{Text: i18n.T("✅ Продолжить"), CallbackData: confirmData},
		{Text: i18n.T("Отмена"), CallbackData: "cab:cancel"},
	}}}
	if cb.Message != nil {
		_ = s.bot.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, kb)
	} else {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, cb.From.ID, text, kb)
	}
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
}

// providerButtonLabel returns the user-facing label for a payment-method button.
func providerButtonLabel(provider string) string {
	switch provider {
	case ProviderPlatega:
		return i18n.T("💳 Картой / СБП")
	case ProviderTelegramStars:
		return i18n.T("⭐ Telegram Stars")
	default:
		return i18n.T("💳 Перевод (P2P)")
	}
}

// handlePayVia handles the user's payment-method choice from the provider picker
// and routes the renewal to the chosen provider.
func (s *Service) handlePayVia(ctx context.Context, cb *tg.CallbackQuery) bool {
	// payvia:<userID>:<months>:<provider> (standard, incl. stale keyboards) or
	// payvia:<userID>:<months>:<provider>:<plan> for custom presets.
	// Confirmed plan-change provider choices use payvia:<userID>:<months>:<provider>:<plan>:ok.
	parts := strings.Split(strings.TrimPrefix(cb.Data, "payvia:"), ":")
	if len(parts) != 3 && len(parts) != 4 && len(parts) != 5 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	provider := parts[2]
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	plan := store.PlanStandard
	if len(parts) == 4 {
		plan = parts[3]
	}
	planChangeConfirmed := false
	if len(parts) == 5 {
		if parts[4] != "ok" {
			_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
			return true
		}
		plan = parts[3]
		planChangeConfirmed = true
	}
	// Reject a provider that is no longer enabled (stale keyboard).
	if !s.providerAvailable(provider) || !s.isProviderEnabled(provider) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот способ оплаты больше недоступен."))
		return true
	}

	tariff, err := s.store.GetTariff(ctx, plan, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}
	u, err := s.store.GetNotifiedUser(ctx, userID)
	if err != nil || u == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось найти данные. Дождитесь следующего уведомления."))
		return true
	}
	s.routeProviderWithPlanChangeGate(ctx, cb, u, userID, months, tariff.Price, plan, provider, planChangeConfirmed)
	return true
}

func (s *Service) handlePlanChangeConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	// chg:<userID>:<months>:<plan>
	parts := strings.Split(strings.TrimPrefix(cb.Data, "chg:"), ":")
	if len(parts) != 3 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	plan := parts[2]
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}

	tariff, err := s.store.GetTariff(ctx, plan, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}
	s.createRequestAndNotify(ctx, cb, userID, months, tariff.Price, plan, true)
	return true
}

func (s *Service) handlePlanChangeProviderConfirm(ctx context.Context, cb *tg.CallbackQuery) bool {
	// chgv:<userID>:<months>:<provider>:<plan>
	parts := strings.Split(strings.TrimPrefix(cb.Data, "chgv:"), ":")
	if len(parts) != 4 {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	userID, err1 := strconv.ParseInt(parts[0], 10, 64)
	months, err2 := strconv.Atoi(parts[1])
	provider := parts[2]
	plan := parts[3]
	if err1 != nil || err2 != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать выбор."))
		return true
	}
	if !s.providerAvailable(provider) || !s.isProviderEnabled(provider) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот способ оплаты больше недоступен."))
		return true
	}
	tariff, err := s.store.GetTariff(ctx, plan, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return true
	}
	u, err := s.store.GetNotifiedUser(ctx, userID)
	if err != nil || u == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось найти данные. Дождитесь следующего уведомления."))
		return true
	}
	s.routeProvider(ctx, cb, u, userID, months, tariff.Price, plan, provider)
	return true
}

// createPaymentRequest writes a pending payment request and DMs all admins a
// confirm button (a photo or document message when a payment confirmation
// file is attached — att is nil in the plain flow). Shared by the chat
// callback flow, the photo/document flow and the mini app API.
func (s *Service) createPaymentRequest(ctx context.Context, u *store.NotifiedUser, months, price int, plan string, att *receiptAttachment) (int64, error) {
	var fileID string
	var isDoc bool
	if att != nil {
		fileID, isDoc = att.fileID, att.asDocument
	}
	reqID, err := s.store.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: u.RemnawaveID, UUID: u.UUID, Username: u.Username,
		TelegramID: u.TelegramID, Months: months, Price: price,
		ExpireAt: u.ExpireAt, Status: "pending", Plan: plan,
		ScreenshotFileID: fileID, ScreenshotIsDocument: isDoc,
	})
	if err != nil {
		s.logger.Error("create payment request failed", "err", err.Error())
		return 0, err
	}

	// Notify all admins with details + confirm button.
	text := s.formatAdminRequest(ctx, u, months, price, plan)
	if att != nil && att.note != "" {
		text += "\n\n💬 " + att.note
	}
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✅ Подтвердить оплату"), CallbackData: fmt.Sprintf("ok:%d", reqID)},
			{Text: i18n.T("❌ Отклонить"), CallbackData: fmt.Sprintf("rej:%d", reqID)},
		}},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		var msgID int64
		switch {
		case att != nil && len(att.data) > 0 && fileID == "" && isDoc:
			msgID, fileID, err = s.bot.SendDocumentUpload(ctx, adminID, att.filename, att.data, text, kb)
		case att != nil && len(att.data) > 0 && fileID == "":
			msgID, fileID, err = s.bot.SendPhotoUpload(ctx, adminID, att.filename, att.data, text, kb)
		case fileID != "" && isDoc:
			msgID, err = s.bot.SendDocument(ctx, adminID, fileID, text, kb)
		case fileID != "":
			msgID, err = s.bot.SendPhoto(ctx, adminID, fileID, text, kb)
		default:
			msgID, err = s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		}
		if err != nil {
			s.logger.Error("notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		if att != nil && len(att.data) > 0 && fileID != "" && len(refs) == 0 {
			if serr := s.store.SetPaymentRequestScreenshot(ctx, reqID, fileID, isDoc); serr != nil {
				s.logger.Error("store uploaded receipt file id failed", "request_id", reqID, "err", serr.Error())
			}
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	if len(refs) == 0 && len(s.adminIDs) > 0 {
		// No admin got a confirm button, so reporting success would strand the
		// request: withdraw it and let the caller ask the user to retry.
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error("withdraw unnotified request failed", "request_id", reqID, "err", derr.Error())
		}
		return 0, fmt.Errorf("payment request %d: notifying every admin failed", reqID)
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

	// Notify the paying user that their payment was accepted (mirrors the mini
	// app confirm path; the shared confirmPaymentRequest stays silent because the
	// automatic Stars/Platega flows send their own message).
	if req.TelegramID != 0 {
		if req.Kind == store.PaymentKindTrafficExtension {
			_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
				i18n.T("✅ Для «%s» добавлено %d ГБ трафика до %s."),
				req.Username, req.TrafficGB, newExpireAt.Format("02.01.2006")))
		} else {
			_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
				i18n.T("✅ Подписка «%s» продлена на %d мес. до %s."),
				req.Username, req.Months, newExpireAt.Format("02.01.2006")))
		}
	}

	if s.dryRun {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Подписка продлена (dry-run)."))
		return true
	}

	if req.Kind == store.PaymentKindTrafficExtension {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Трафик добавлен!"))
		_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(i18n.T("✅ Для %s добавлено %d ГБ трафика до %s"),
			req.Username, req.TrafficGB, newExpireAt.Format("02.01.2006")))
	} else {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Подписка продлена!"))
		_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(i18n.T("✅ Подписка для %s продлена на %d мес. до %s"),
			req.Username, req.Months, newExpireAt.Format("02.01.2006")))
	}
	return true
}

// handleReject processes admin's "Отклонить" button on a payment request.
func (s *Service) handleReject(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized reject attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "rej:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	req, err := s.rejectPaymentRequest(ctx, reqID)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, resolveErrorText(err))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отклонена."))
	if req.Kind == store.PaymentKindTrafficExtension {
		_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(
			i18n.T("❌ Заявка на докупку %d ГБ для «%s» отклонена."),
			req.TrafficGB, req.Username))
	} else {
		_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(
			i18n.T("❌ Заявка на продление «%s» на %d мес. отклонена."),
			req.Username, req.Months))
	}
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

func (s *Service) tariffKeyboard(userID int64, plan string, tariffs []store.Tariff) *tg.InlineKeyboardMarkup {
	custom := plan != "" && plan != store.PlanStandard
	rows := make([][]tg.InlineKeyboardButton, 0, len(tariffs)+1)
	for _, t := range tariffs {
		data := fmt.Sprintf("pick:%d:%d", userID, t.Months)
		if custom {
			data += ":" + plan
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         fmt.Sprintf(i18n.T("%d мес. — %s"), t.Months, s.priceLabel(t.Price)),
			CallbackData: data,
		}})
	}
	// Back from a custom plan's months returns to the plan chooser (pay:
	// re-runs handlePay); the standard keyboard keeps its original target.
	backData := fmt.Sprintf("back:%d", userID)
	if custom {
		backData = fmt.Sprintf("pay:%d", userID)
	}
	rows = append(rows, []tg.InlineKeyboardButton{{
		Text: i18n.T("← Назад"), CallbackData: backData,
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
	case cb.Data == "adm:pending":
		s.sendPendingSummary(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:pd:"):
		s.sendPendingCategory(ctx, chatID, strings.TrimPrefix(cb.Data, "adm:pd:"))
	case cb.Data == "adm:tariffs":
		s.sendAdminTariffs(ctx, chatID)
	case cb.Data == "adm:stats":
		s.sendAdminStats(ctx, chatID)
	case cb.Data == "adm:backup":
		s.cmdBackup(ctx, chatID)
	case cb.Data == "adm:del_list":
		s.sendAdminDelList(ctx, chatID)
	case cb.Data == "adm:traffic_ext":
		s.cmdListTrafficExtensions(ctx, chatID)
	case cb.Data == "adm:req":
		s.sendAdminRequisites(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:del:"):
		s.handleAdminDelTariff(ctx, chatID, cb.Data)
	case cb.Data == "adm:setreq":
		s.startSetRequisitesFlow(ctx, chatID)
	case cb.Data == "adm:shot_toggle":
		s.handleAdminScreenshotToggle(ctx, chatID)
	case cb.Data == "adm:guard":
		s.sendRegistrationGuardPatternSettings(ctx, chatID)
	case cb.Data == "adm:guard:add":
		s.startRegistrationGuardPatternInput(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:guard:del:"):
		s.handleRegistrationGuardPatternDelete(ctx, chatID, cb.Data)
	case cb.Data == "adm:providers":
		s.sendAdminProviderList(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:provtog:"):
		s.handleAdminProviderToggle(ctx, chatID, cb.Data)
	case cb.Data == "adm:addtariff":
		s.startAddTariffFlow(ctx, chatID)
	case cb.Data == "adm:gifts":
		s.sendAdminGiftList(ctx, chatID)
	case cb.Data == "adm:gbuyers":
		s.handleAdminGiftBuyers(ctx, cb)
	case strings.HasPrefix(cb.Data, "adm:gbuyer:"):
		s.handleAdminGiftBuyer(ctx, cb)
	case strings.HasPrefix(cb.Data, "adm:glist:"):
		s.handleAdminGiftList(ctx, cb)
	case cb.Data == "adm:gnoop":
		// no-op: pagination label button
	case strings.HasPrefix(cb.Data, "adm:grev:"):
		s.handleAdminGiftRevoke(ctx, cb)
	case cb.Data == "adm:squad":
		s.sendAdminSquadList(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:sq:"):
		s.handleAdminSquadPick(ctx, chatID, cb.Data)
	case cb.Data == "adm:user":
		s.startUserLookup(ctx, chatID)
	case cb.Data == "adm:treset":
		s.sendDefaultTrafficResetPicker(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:treset:"):
		s.handleDefaultTrafficResetPick(ctx, chatID, cb.Data)
	case cb.Data == "adm:u:card":
		s.reloadUserCard(ctx, chatID)
	case cb.Data == "adm:u:extset":
		s.startUserInput(ctx, chatID, adminInputUserExpiry, i18n.T("Введите количество дней (можно со знаком «−» для уменьшения):"))
	case strings.HasPrefix(cb.Data, "adm:u:ext:"):
		s.handleUserExpiryDelta(ctx, chatID, cb.Data)
	case cb.Data == "adm:u:hwid":
		s.startUserInput(ctx, chatID, adminInputUserHwid, i18n.T("Введите лимит устройств (целое ≥ 0, 0 — без ограничения):"))
	case cb.Data == "adm:u:traffic":
		s.startUserInput(ctx, chatID, adminInputUserTraffic, i18n.T("Введите лимит трафика в GB (целое ≥ 0, 0 — без ограничения):"))
	case cb.Data == "adm:u:status":
		s.handleUserStatusToggle(ctx, chatID)
	case cb.Data == "adm:u:reset":
		s.sendUserResetStrategies(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:u:reset:"):
		s.handleUserResetPick(ctx, chatID, cb.Data)
	case cb.Data == "adm:u:squads":
		s.sendUserSquads(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:u:sq:"):
		s.handleUserSquadToggle(ctx, chatID, cb.Data)
	case cb.Data == "adm:trial":
		s.sendTrialAdminCard(ctx, chatID)
	case cb.Data == "adm:trial:toggle":
		s.handleTrialToggle(ctx, chatID)
	case cb.Data == "adm:trial:approval":
		s.handleTrialApprovalToggle(ctx, chatID)
	case cb.Data == "adm:trial:days":
		s.startTrialDaysInput(ctx, chatID)
	case cb.Data == "adm:trial:traffic":
		s.startTrialLimitInput(ctx, chatID, adminInputTrialTraffic, i18n.T("Введите лимит трафика пробного периода в GB (целое ≥ 0, 0 — без ограничения):"))
	case cb.Data == "adm:trial:hwid":
		s.startTrialLimitInput(ctx, chatID, adminInputTrialHwid, i18n.T("Введите лимит устройств пробного периода (целое ≥ 0, 0 — без ограничения):"))
	case cb.Data == "adm:trial:squad":
		s.sendTrialSquadList(ctx, chatID)
	case strings.HasPrefix(cb.Data, "adm:trial:sq:"):
		s.handleTrialSquadPick(ctx, chatID, cb.Data)
	case cb.Data == "adm:referral":
		s.sendReferralAdminCard(ctx, chatID)
	case cb.Data == "adm:referral:toggle":
		s.handleReferralToggle(ctx, chatID)
	case cb.Data == "adm:referral:inviter":
		s.startReferralBonusInput(ctx, chatID, adminInputReferralInviter)
	case cb.Data == "adm:referral:invitee":
		s.startReferralBonusInput(ctx, chatID, adminInputReferralInvitee)
	case cb.Data == "adm:upd":
		s.sendUpdateSettings(ctx, chatID)
	case cb.Data == "adm:upd:check":
		s.manualUpdateCheck(ctx, chatID)
	case cb.Data == "adm:upd:interval":
		s.startUpdateIntervalInput(ctx, chatID)
	case cb.Data == "adm:checker":
		s.sendProxyHealth(ctx, chatID)
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
	text, any, err := s.formatTariffListing(ctx)
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
	if !any {
		_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, i18n.T("Тарифы не заданы."), kb)
		return
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

func (s *Service) sendAdminDelList(ctx context.Context, chatID int64) {
	tariffs, err := s.store.ListTariffs(ctx, store.PlanStandard)
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
	deleted, err := s.store.DeleteTariff(ctx, store.PlanStandard, months)
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

// sendAdminSquadList shows the panel's internal squads as buttons so the
// admin can pick the default squad for newly created users; the current
// selection is marked with a check.
func (s *Service) sendAdminSquadList(ctx context.Context, chatID int64) {
	if s.squads == nil {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Список сквадов недоступен."))
		return
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		s.logger.Error("admin: list squads failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения сквадов из панели. Попробуйте позже."))
		return
	}
	selectedUUID, _ := s.defaultSquadSelection(ctx)
	rows := make([][]tg.InlineKeyboardButton, 0, len(squads)+1)
	for _, sq := range squads {
		text := sq.Name
		if sq.UUID == selectedUUID {
			text = "✅ " + text
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         text,
			CallbackData: "adm:sq:" + sq.UUID,
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	text := i18n.T("Выберите сквад, в который добавлять новых пользователей:")
	if len(squads) == 0 {
		text = i18n.T("В панели нет внутренних сквадов.")
	}
	if selectedUUID == "" && len(squads) > 0 {
		text += i18n.T("\n\nСейчас сквад не выбран: используется сквад с именем «Default-Squad», если он есть.")
	}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, &tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleAdminSquadPick persists the squad chosen via an adm:sq:<uuid> button.
func (s *Service) handleAdminSquadPick(ctx context.Context, chatID int64, data string) {
	uuid := strings.TrimPrefix(data, "adm:sq:")
	sq, err := s.setDefaultSquad(ctx, uuid)
	switch {
	case errors.Is(err, ErrBadInput):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Сквад не найден в панели. Обновите список."))
		s.sendAdminSquadList(ctx, chatID)
		return
	case errors.Is(err, ErrPanelUnavailable):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения сквадов из панели. Попробуйте позже."))
		return
	case err != nil:
		s.logger.Error("admin: set default squad failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения сквада."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(i18n.T("Сквад по умолчанию: «%s». Новые пользователи будут добавляться в него."), sq.Name))
}

// handleAdminScreenshotToggle flips the payment-screenshot requirement from
// the adm:shot_toggle button and re-renders the menu with the new state.
func (s *Service) handleAdminScreenshotToggle(ctx context.Context, chatID int64) {
	on := !s.getRequireScreenshot()
	if err := s.setRequireScreenshot(ctx, on); err != nil {
		s.logger.Error("admin: save screenshot setting failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return
	}
	if on {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("📸 Чек об оплате теперь обязателен: заявка уходит администратору вместе с чеком (фото или PDF)."))
	} else {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("📸 Чек об оплате отключён: заявки отправляются без чека."))
	}
	s.SendAdminMenu(ctx, chatID)
}

// sendAdminProviderList shows every configured payment provider as a toggle so
// the admin can enable any combination; enabled providers are marked with a
// check. When more than one is enabled the user picks at pay time.
func (s *Service) sendAdminProviderList(ctx context.Context, chatID int64) {
	rows := make([][]tg.InlineKeyboardButton, 0, len(allProviders)+1)
	for _, name := range allProviders {
		if !s.providerAvailable(name) {
			continue
		}
		mark := "⬜ "
		if s.isProviderEnabled(name) {
			mark = "✅ "
		}
		rows = append(rows, []tg.InlineKeyboardButton{{
			Text:         mark + providerAdminLabel(name),
			CallbackData: "adm:provtog:" + name,
		}})
	}
	rows = append(rows, []tg.InlineKeyboardButton{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}})
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID,
		i18n.T("Способы оплаты (можно включить несколько). Если включено больше одного — пользователь выбирает при оплате."),
		&tg.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// providerAdminLabel returns the admin-facing name of a payment provider.
func providerAdminLabel(provider string) string {
	switch provider {
	case ProviderPlatega:
		return i18n.T("Platega (СБП/карта)")
	case ProviderTelegramStars:
		return i18n.T("Telegram Stars")
	default:
		return i18n.T("P2P (вручную)")
	}
}

// handleAdminProviderToggle flips one provider's enabled state from an
// adm:provtog:<name> button and re-renders the picker.
func (s *Service) handleAdminProviderToggle(ctx context.Context, chatID int64, data string) {
	name := strings.TrimPrefix(data, "adm:provtog:")
	on := !s.isProviderEnabled(name)
	if err := s.setProviderEnabled(ctx, name, on); err != nil {
		if errors.Is(err, ErrBadInput) {
			if on {
				_ = s.bot.SendPlain(ctx, chatID, i18n.T("Этот способ оплаты не настроен."))
			} else {
				_ = s.bot.SendPlain(ctx, chatID, i18n.T("Нельзя отключить последний способ оплаты."))
			}
		} else {
			s.logger.Error("admin: save payment providers failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		}
		s.sendAdminProviderList(ctx, chatID)
		return
	}
	s.sendAdminProviderList(ctx, chatID)
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

func (s *Service) formatAdminRequest(ctx context.Context, u *store.NotifiedUser, months, price int, plan string) string {
	var b strings.Builder
	b.WriteString(i18n.T("💳 Заявка на оплату\n\n"))
	b.WriteString(i18n.T("Клиент: ") + u.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", u.RemnawaveID))
	b.WriteString("UUID: " + u.UUID + "\n")
	b.WriteString(fmt.Sprintf("Telegram ID: %d\n", u.TelegramID))
	b.WriteString(i18n.T("Подписка до: ") + u.ExpireAt.Format("02.01.2006") + "\n")
	if plan != "" && plan != store.PlanStandard {
		name := plan
		if p, err := s.getPlan(ctx, plan); err == nil {
			name = p.Name
		}
		b.WriteString(i18n.T("Тариф: ") + name + "\n")
	}
	if price > 0 {
		b.WriteString(fmt.Sprintf(i18n.T("Выбрано: %d мес. — %s"), months, s.priceLabel(price)))
	} else {
		b.WriteString(fmt.Sprintf(i18n.T("Выбрано: %d мес."), months))
	}
	return b.String()
}
