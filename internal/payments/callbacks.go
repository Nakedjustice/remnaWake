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
	// A blocked identity keeps the whole support surface (chat and tickets) as
	// its escape hatch, mirroring the mini app's authenticateSupport exemption.
	if s.registrationBlocked(ctx, cb.From.ID) && cb.Data != "menu:support" && !strings.HasPrefix(cb.Data, "sup:") {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Доступ к боту ограничен. Обратитесь в поддержку."))
		return true
	}
	for _, r := range callbackRoutes {
		if (r.exact && cb.Data == r.match) || (!r.exact && strings.HasPrefix(cb.Data, r.match)) {
			return r.fn(s, ctx, cb)
		}
	}
	return false
}

// callbackRoutes maps inline-keyboard callback data to its handler in
// dispatch order: the first match wins, exactly like the switch this
// replaces. exact compares the whole payload; otherwise the payload must
// carry the match as a prefix. Keep the order when adding routes.
var callbackRoutes = []struct {
	match string
	exact bool
	fn    func(*Service, context.Context, *tg.CallbackQuery) bool
}{
	{"pay:", false, (*Service).handlePay},
	{"pick:", false, (*Service).handlePick},
	{"pln:", false, (*Service).handlePlanPick},
	{"back:", false, (*Service).handleBack},
	{"ok:", false, (*Service).handleConfirm},
	{"rej:", false, (*Service).handleReject},
	{"menu:tariffs", true, (*Service).handleMenuTariffs},
	{"menu:cabinet", true, (*Service).handleMenuCabinet},
	{"cab:pay:", false, (*Service).handleCabinetPay},
	{"tex:pay:", false, (*Service).handleTrafficExtensionPay},
	{"tex:pick:", false, (*Service).handleTrafficExtensionPick},
	{"dev:", false, (*Service).handleDeviceCallback},
	{"plcheck:", false, (*Service).handlePlategaCheck},
	{"chg:", false, (*Service).handlePlanChangeConfirm},
	{"chgv:", false, (*Service).handlePlanChangeProviderConfirm},
	{"payvia:", false, (*Service).handlePayVia},
	{"cab:cancel", true, (*Service).handleCabinetCancel},
	{"menu:invite", true, (*Service).handleMenuInvite},
	{"inv_submit", true, (*Service).handleInviteSubmit},
	{"inv_ok:", false, (*Service).handleInviteApprove},
	{"inv_rej:", false, (*Service).handleInviteReject},
	{"inv_cancel", true, (*Service).handleInviteCancel},
	{"menu:register", true, (*Service).handleMenuRegister},
	{"menu:trial", true, (*Service).handleMenuTrial},
	{"menu:referral", true, (*Service).handleMenuReferral},
	{"notif:menu", true, (*Service).handleNotifMenu},
	{"notif:expiry", true, (*Service).handleNotifExpiry},
	{"notif:winback", true, (*Service).handleNotifWinback},
	{"reg_confirm", true, (*Service).handleRegisterConfirm},
	{"reg_cancel", true, (*Service).handleRegisterCancel},
	{"menu:gift", true, (*Service).handleMenuGift},
	{"menu:mygifts", true, (*Service).handleMenuMyGifts},
	{"mg:menu", true, (*Service).handleMyGiftsMenu},
	{"mg:list:", false, (*Service).handleMyGiftsList},
	{"mg:noop", true, (*Service).handleNoop},
	{"gc_link:", false, (*Service).handleGiftCodeResend},
	{"gc_pick:", false, (*Service).handleGiftCodePick},
	{"gc_cancel", true, (*Service).handleGiftCodeCancel},
	{"gc_ok:", false, (*Service).handleGiftCodeApprove},
	{"gc_rej:", false, (*Service).handleGiftCodeReject},
	{"tr_ok:", false, (*Service).handleTrialApprove},
	{"tr_rej:", false, (*Service).handleTrialReject},
	{"gc_use:", false, (*Service).handleGiftUse},
	{"gc_redeem_cancel", true, (*Service).handleGiftRedeemCancel},
	{"upd:install", true, (*Service).handleUpdateInstall},
	{"upd:dismiss", true, (*Service).handleUpdateDismiss},
	{"menu:support", true, (*Service).handleMenuSupport},
	{"sup:close", true, (*Service).handleSupportClose},
	{"sup:reply:", false, (*Service).handleSupportReplyStart},
	{"sup:list", true, (*Service).handleSupportTicketList},
	{"sup:new", true, (*Service).handleSupportTicketNew},
	{"sup:t:", false, (*Service).handleSupportTicketOpen},
	{"sup:re:", false, (*Service).handleSupportTicketReplyStart},
	{"sup:cl:", false, (*Service).handleSupportTicketClose},
	{guardUnblockCallbackPrefix, false, (*Service).handleRegistrationUnblock},
	{"adm:infrapaid:", false, (*Service).handleInfraPaid},
	{"adm:", false, (*Service).handleAdminMenu},
}

// handleNoop acknowledges pagination label buttons that carry no action.
func (s *Service) handleNoop(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	return true
}

// handleNotifExpiry and handleNotifWinback bind the notification kind for the
// two toggle buttons so they fit the shared route-table signature.
func (s *Service) handleNotifExpiry(ctx context.Context, cb *tg.CallbackQuery) bool {
	return s.handleNotifToggle(ctx, cb, store.NotificationExpiry)
}

func (s *Service) handleNotifWinback(ctx context.Context, cb *tg.CallbackQuery) bool {
	return s.handleNotifToggle(ctx, cb, store.NotificationWinback)
}

// assertCallbackOwner resolves the remembered profile a callback targets and
// verifies the presser owns it. Callback data is caller-controlled — a keyboard
// can be forwarded and the id in it edited — so every handler that acts on a
// Remnawave id taken from cb.Data must pass through here first.
//
// A missing profile, a profile owned by somebody else and a profile with no
// Telegram link deliberately share one answer: a distinct "not yours" would
// turn the buttons into an oracle for enumerating valid Remnawave ids. This
// mirrors CheckPlategaPayment, which makes the same choice on the mini app side.
func (s *Service) assertCallbackOwner(ctx context.Context, cb *tg.CallbackQuery, remnawaveID int64) (*store.NotifiedUser, bool) {
	u, err := s.store.GetNotifiedUser(ctx, remnawaveID)
	if err != nil {
		s.logger.Error("get notified user failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return nil, false
	}
	// TelegramID == 0 means the snapshot has no linked account, so ownership
	// cannot be established and nobody may act on it — checked separately from
	// the comparison below because an update without a sender would otherwise
	// match such a profile.
	if u == nil || u.TelegramID == 0 || u.TelegramID != cb.From.ID {
		s.logger.Warn("callback owner mismatch", "from_id", cb.From.ID, "remnawave_id", remnawaveID, "data", cb.Data)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось найти данные. Дождитесь следующего уведомления."))
		return nil, false
	}
	return u, true
}

func (s *Service) handlePay(ctx context.Context, cb *tg.CallbackQuery) bool {
	userID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "pay:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}
	u, ok := s.assertCallbackOwner(ctx, cb, userID)
	if !ok {
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
		s.createRequestAndNotify(ctx, cb, u, 1, 0, store.PlanStandard, false)
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
	if _, ok := s.assertCallbackOwner(ctx, cb, userID); !ok {
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

// tariffPrice resolves the tariff for a callback and, when it fails to load
// or is gone, acknowledges the button with the matching user-visible answer.
// ok=false means the caller must stop and return true.
func (s *Service) tariffPrice(ctx context.Context, cb *tg.CallbackQuery, plan string, months int) (int, bool) {
	tariff, err := s.store.GetTariff(ctx, plan, months)
	if err != nil {
		s.logger.Error("get tariff failed", "err", err.Error())
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка, попробуйте позже."))
		return 0, false
	}
	if tariff == nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот тариф больше недоступен."))
		return 0, false
	}
	return tariff.Price, true
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
	u, ok := s.assertCallbackOwner(ctx, cb, userID)
	if !ok {
		return true
	}

	price, ok := s.tariffPrice(ctx, cb, plan, months)
	if !ok {
		return true
	}

	s.createRequestAndNotify(ctx, cb, u, months, price, plan, false)
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

// createRequestAndNotify writes a pending request for the profile the caller
// already resolved through assertCallbackOwner, clears the user's keyboard, and
// DMs all admins a confirm button.
func (s *Service) createRequestAndNotify(ctx context.Context, cb *tg.CallbackQuery, u *store.NotifiedUser, months, price int, plan string, planChangeConfirmed bool) {
	// When more than one provider is enabled, let the user choose; otherwise
	// route straight to the only enabled provider.
	providers := s.enabledProviders()
	if len(providers) > 1 {
		s.routeProviderWithPlanChangeGate(ctx, cb, u, u.RemnawaveID, months, price, plan, "", planChangeConfirmed)
		return
	}
	s.routeProviderWithPlanChangeGate(ctx, cb, u, u.RemnawaveID, months, price, plan, providers[0], planChangeConfirmed)
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
			i18n.T("⚠️ Переход на более дешёвый тариф\n\nВы переходите с «%s» на «%s». Оставшееся время не будет пересчитано: срок после покупки на %d мес. будет до %s. После подтверждения оплаты лимиты нового тарифа применятся сразу. Счётчик использованного трафика будет сброшен до нуля.\n\nПродолжить?"),
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
	u, ok := s.assertCallbackOwner(ctx, cb, userID)
	if !ok {
		return true
	}
	// Reject a provider that is no longer enabled (stale keyboard).
	if !s.providerAvailable(provider) || !s.isProviderEnabled(provider) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот способ оплаты больше недоступен."))
		return true
	}

	price, ok := s.tariffPrice(ctx, cb, plan, months)
	if !ok {
		return true
	}
	s.routeProviderWithPlanChangeGate(ctx, cb, u, userID, months, price, plan, provider, planChangeConfirmed)
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
	u, ok := s.assertCallbackOwner(ctx, cb, userID)
	if !ok {
		return true
	}

	price, ok := s.tariffPrice(ctx, cb, plan, months)
	if !ok {
		return true
	}
	s.createRequestAndNotify(ctx, cb, u, months, price, plan, true)
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
	u, ok := s.assertCallbackOwner(ctx, cb, userID)
	if !ok {
		return true
	}
	if !s.providerAvailable(provider) || !s.isProviderEnabled(provider) {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Этот способ оплаты больше недоступен."))
		return true
	}
	price, ok := s.tariffPrice(ctx, cb, plan, months)
	if !ok {
		return true
	}
	s.routeProvider(ctx, cb, u, userID, months, price, plan, provider)
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
		RemnawaveID: u.RemnawaveID, Username: u.Username,
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
	if err := s.notifyAdminsWithReceipt(ctx, reqID, text, kb, att, fileID, isDoc,
		"withdraw unnotified request failed", "payment request"); err != nil {
		return 0, err
	}
	return reqID, nil
}

// notifyAdminsWithReceipt DMs every admin the pending-request notification: a
// photo/document upload when att carries fresh bytes (storing the resulting
// file_id once), a file_id resend when the receipt is already stored, or a
// plain message otherwise. When no admin receives it the request is withdrawn
// (reporting success would strand it) and an error is returned. withdrawLog
// names the withdrawal failure; kind names the request in the returned error.
func (s *Service) notifyAdminsWithReceipt(ctx context.Context, reqID int64, text string, kb *tg.InlineKeyboardMarkup, att *receiptAttachment, fileID string, isDoc bool, withdrawLog, kind string) error {
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		var msgID int64
		var err error
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
		if _, derr := s.store.DeletePendingPaymentRequest(ctx, reqID); derr != nil {
			s.logger.Error(withdrawLog, "request_id", reqID, "err", derr.Error())
		}
		return fmt.Errorf("%s %d: notifying every admin failed", kind, reqID)
	}
	s.putAdminMsgs(s.payMsgs, reqID, refs)
	return nil
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
	if !s.requireAdminCB(ctx, cb, "reject") {
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
	s.clearAdminButtons(ctx, s.payMsgs, reqID, "clear admin confirm button failed")
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
	for _, r := range admRoutes {
		if (r.exact && cb.Data == r.match) || (!r.exact && strings.HasPrefix(cb.Data, r.match)) {
			r.fn(s, ctx, cb)
			return true
		}
	}
	return true
}

// admRoutes maps admin "adm:" callback data to its handler; the first match
// wins, preserving the order of the switch this table replaces. Handlers see
// the whole callback: the chat is cb.From.ID and payload-carrying handlers
// read cb.Data themselves.
var admRoutes = []struct {
	match string
	exact bool
	fn    func(*Service, context.Context, *tg.CallbackQuery)
}{
	{"adm:menu", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.SendAdminMenu(ctx, cb.From.ID) }},
	{"adm:pending", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendPendingSummary(ctx, cb.From.ID) }},
	{"adm:pd:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendPendingCategory(ctx, cb.From.ID, strings.TrimPrefix(cb.Data, "adm:pd:")) }},
	{"adm:tariffs", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminTariffs(ctx, cb.From.ID) }},
	{"adm:stats", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminStats(ctx, cb.From.ID) }},
	{"adm:backup", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.cmdBackup(ctx, cb.From.ID) }},
	{"adm:del_list", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminDelList(ctx, cb.From.ID) }},
	{"adm:traffic_ext", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.cmdListTrafficExtensions(ctx, cb.From.ID) }},
	{"adm:req", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminRequisites(ctx, cb.From.ID) }},
	{"adm:del:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminDelTariff(ctx, cb.From.ID, cb.Data) }},
	{"adm:setreq", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startSetRequisitesFlow(ctx, cb.From.ID) }},
	{"adm:shot_toggle", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminScreenshotToggle(ctx, cb.From.ID) }},
	{"adm:guard", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendRegistrationGuardPatternSettings(ctx, cb.From.ID) }},
	{"adm:guard:add", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startRegistrationGuardPatternInput(ctx, cb.From.ID) }},
	{"adm:guard:del:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleRegistrationGuardPatternDelete(ctx, cb.From.ID, cb.Data) }},
	{"adm:providers", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminProviderList(ctx, cb.From.ID) }},
	{"adm:provtog:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminProviderToggle(ctx, cb.From.ID, cb.Data) }},
	{"adm:addtariff", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startAddTariffFlow(ctx, cb.From.ID) }},
	{"adm:gifts", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminGiftList(ctx, cb.From.ID) }},
	{"adm:gbuyers", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminGiftBuyers(ctx, cb) }},
	{"adm:gbuyer:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminGiftBuyer(ctx, cb) }},
	{"adm:glist:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminGiftList(ctx, cb) }},
	{"adm:gnoop", true, func(_ *Service, _ context.Context, _ *tg.CallbackQuery) { /* pagination label button */ }},
	{"adm:grev:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminGiftRevoke(ctx, cb) }},
	{"adm:squad", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminSquadList(ctx, cb.From.ID) }},
	{"adm:sq:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminSquadPick(ctx, cb.From.ID, cb.Data) }},
	{"adm:user", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startUserLookup(ctx, cb.From.ID) }},
	{"adm:treset", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendDefaultTrafficResetPicker(ctx, cb.From.ID) }},
	{"adm:treset:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleDefaultTrafficResetPick(ctx, cb.From.ID, cb.Data) }},
	{"adm:u:card", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.reloadUserCard(ctx, cb.From.ID) }},
	{"adm:u:extset", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) {
		s.startUserInput(ctx, cb.From.ID, adminInputUserExpiry, i18n.T("Введите количество дней (можно со знаком «−» для уменьшения):"))
	}},
	{"adm:u:ext:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleUserExpiryDelta(ctx, cb.From.ID, cb.Data) }},
	{"adm:u:hwid", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) {
		s.startUserInput(ctx, cb.From.ID, adminInputUserHwid, i18n.T("Введите лимит устройств (целое ≥ 0, 0 — без ограничения):"))
	}},
	{"adm:u:traffic", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) {
		s.startUserInput(ctx, cb.From.ID, adminInputUserTraffic, i18n.T("Введите лимит трафика в GB (целое ≥ 0, 0 — без ограничения):"))
	}},
	{"adm:u:status", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleUserStatusToggle(ctx, cb.From.ID) }},
	{"adm:u:reset", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendUserResetStrategies(ctx, cb.From.ID) }},
	{"adm:u:reset:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleUserResetPick(ctx, cb.From.ID, cb.Data) }},
	{"adm:u:squads", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendUserSquads(ctx, cb.From.ID) }},
	{"adm:u:sq:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleUserSquadToggle(ctx, cb.From.ID, cb.Data) }},
	{"adm:trial", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendTrialAdminCard(ctx, cb.From.ID) }},
	{"adm:trial:toggle", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleTrialToggle(ctx, cb.From.ID) }},
	{"adm:trial:approval", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleTrialApprovalToggle(ctx, cb.From.ID) }},
	{"adm:trial:days", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startTrialDaysInput(ctx, cb.From.ID) }},
	{"adm:trial:traffic", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) {
		s.startTrialLimitInput(ctx, cb.From.ID, adminInputTrialTraffic, i18n.T("Введите лимит трафика пробного периода в GB (целое ≥ 0, 0 — без ограничения):"))
	}},
	{"adm:trial:hwid", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) {
		s.startTrialLimitInput(ctx, cb.From.ID, adminInputTrialHwid, i18n.T("Введите лимит устройств пробного периода (целое ≥ 0, 0 — без ограничения):"))
	}},
	{"adm:trial:squad", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendTrialSquadList(ctx, cb.From.ID) }},
	{"adm:trial:sq:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleTrialSquadPick(ctx, cb.From.ID, cb.Data) }},
	{"adm:referral", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendReferralAdminCard(ctx, cb.From.ID) }},
	{"adm:referral:toggle", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleReferralToggle(ctx, cb.From.ID) }},
	{"adm:referral:inviter", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startReferralBonusInput(ctx, cb.From.ID, adminInputReferralInviter) }},
	{"adm:referral:invitee", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startReferralBonusInput(ctx, cb.From.ID, adminInputReferralInvitee) }},
	{"adm:upd", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendUpdateSettings(ctx, cb.From.ID) }},
	{"adm:upd:check", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.manualUpdateCheck(ctx, cb.From.ID) }},
	{"adm:upd:interval", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startUpdateIntervalInput(ctx, cb.From.ID) }},
	{"adm:backup_settings", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendBackupSettings(ctx, cb.From.ID) }},
	{"adm:backup:toggle", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleBackupToggle(ctx, cb.From.ID) }},
	{"adm:backup:days:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.applyBackupInterval(ctx, cb.From.ID, strings.TrimPrefix(cb.Data, "adm:backup:days:")) }},
	{"adm:backup:custom", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startBackupIntervalInput(ctx, cb.From.ID) }},
	{"adm:checker", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendProxyHealth(ctx, cb.From.ID) }},
	{"adm:sup", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminTicketInbox(ctx, cb.From.ID) }},
	{"adm:sup:t:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.sendAdminTicketThread(ctx, cb.From.ID, cb.Data) }},
	{"adm:sup:re:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startAdminTicketReply(ctx, cb.From.ID, cb.Data) }},
	{"adm:sup:cl:", false, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleAdminTicketClose(ctx, cb.From.ID, cb.Data) }},
	{"adm:bcast", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.startBroadcastFlow(ctx, cb.From.ID) }},
	{"adm:bc_send", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleBroadcastSend(ctx, cb.From.ID, cb) }},
	{"adm:bc_cancel", true, func(s *Service, ctx context.Context, cb *tg.CallbackQuery) { s.handleBroadcastCancel(ctx, cb.From.ID, cb) }},
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
	s.setAdminInputStep(chatID, adminInputRequisites)
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте новый текст реквизитов:"))
}

func (s *Service) startBroadcastFlow(ctx context.Context, chatID int64) {
	s.mutateAdminInput(chatID, func(st *adminInputState) {
		st.step = adminInputBroadcast
		st.pendingBroadcast = ""
	})
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Отправьте текст рассылки следующим сообщением."))
}

func (s *Service) handleBroadcastSend(ctx context.Context, chatID int64, cb *tg.CallbackQuery) {
	var text string
	s.mutateAdminInput(chatID, func(st *adminInputState) {
		text = st.pendingBroadcast
		st.pendingBroadcast = ""
	})
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	if text == "" {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Нет текста для рассылки. Начните заново через меню."))
		return
	}
	// The update loop is sequential; a large broadcast paced at ~20 msg/s would
	// block the bot for the whole run, so send in the background and report.
	err := s.startBroadcast(ctx, text, func(ctx context.Context, sent, failed int, err error) {
		if err != nil {
			s.logger.Error("broadcast: list users failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка получения списка пользователей, рассылка не выполнена."))
			return
		}
		_ = s.bot.SendPlain(ctx, chatID,
			fmt.Sprintf(i18n.T("Рассылка завершена: отправлено %d, ошибок %d."), sent, failed))
	})
	if errors.Is(err, ErrBroadcastInProgress) {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Рассылка уже выполняется. Дождитесь её завершения."))
		return
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Рассылка запущена…"))
}

func (s *Service) handleBroadcastCancel(ctx context.Context, chatID int64, cb *tg.CallbackQuery) {
	s.mutateAdminInput(chatID, func(st *adminInputState) {
		st.pendingBroadcast = ""
		if st.step == adminInputBroadcast {
			st.step = adminInputNone
		}
	})
	if cb.Message != nil {
		_ = s.bot.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, nil)
	}
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Рассылка отменена."))
}

func (s *Service) startAddTariffFlow(ctx context.Context, chatID int64) {
	s.setAdminInputStep(chatID, adminInputTariffMonths)
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите количество месяцев (целое ≥ 1):"))
}

func (s *Service) formatAdminRequest(ctx context.Context, u *store.NotifiedUser, months, price int, plan string) string {
	var b strings.Builder
	b.WriteString(i18n.T("💳 Заявка на оплату\n\n"))
	b.WriteString(i18n.T("Клиент: ") + u.Username + "\n")
	b.WriteString(fmt.Sprintf("Remnawave ID: %d\n", u.RemnawaveID))
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

// requireAdminCB reports whether the callback tapper is a configured admin of
// an enabled service. Otherwise it warns about the attempt, answers the tap
// with the standard insufficient-rights toast, and reports false. op names
// the attempted action in the warn log.
func (s *Service) requireAdminCB(ctx context.Context, cb *tg.CallbackQuery, op string) bool {
	if s.isEnabled() && s.isAdmin(cb.From.ID) {
		return true
	}
	s.logger.Warn("unauthorized "+op+" attempt", "from_id", cb.From.ID)
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
	return false
}
