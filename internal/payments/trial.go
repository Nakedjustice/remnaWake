package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// trialState tracks a free-trial conversation: the user has been asked for a
// desired profile username and we are awaiting their input.
type trialState struct {
	createdAt time.Time
}

const trialTTL = 10 * time.Minute

func (s *Service) getTrial(chatID int64) *trialState {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.trials[chatID]
	if tr == nil {
		return nil
	}
	if s.now().Sub(tr.createdAt) > trialTTL {
		delete(s.trials, chatID)
		return nil
	}
	return tr
}

func (s *Service) setTrial(chatID int64, tr *trialState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trials[chatID] = tr
}

func (s *Service) clearTrial(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.trials, chatID)
}

// trialAvailable reports whether the free trial can be offered and started:
// the payment flow is on, the trial is enabled, and user creation is wired.
// It is the single guard shared by the offer check and beginTrialFlow so an
// advertised button always leads to a working flow.
func (s *Service) trialAvailable() bool {
	cfg := s.trialConfig()
	return s.isEnabled() && cfg.Enabled && s.creator != nil
}

// TrialOffered reports whether the free trial should be advertised to users.
// Exposes trialAvailable so the welcome-screen button is only shown when the
// flow behind it will actually run.
func (s *Service) TrialOffered() bool {
	return s.trialAvailable()
}

// StartTrialFlow handles /trial. Returns true if the message was consumed (which
// only happens when the trial is enabled).
func (s *Service) StartTrialFlow(ctx context.Context, m *tg.Message) bool {
	if m == nil {
		return false
	}
	return s.beginTrialFlow(ctx, m.Chat.ID)
}

// handleMenuTrial starts the trial flow from the menu button.
func (s *Service) handleMenuTrial(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message != nil {
		s.beginTrialFlow(ctx, cb.Message.Chat.ID)
	}
	return true
}

// beginTrialFlow asks a new user for a desired profile username. Returns false
// (not consumed) when the trial is disabled, so command routing can fall
// through. Returns true once it has replied — including the ineligible case.
func (s *Service) beginTrialFlow(ctx context.Context, chatID int64) bool {
	if !s.trialAvailable() {
		return false
	}
	cfg := s.trialConfig()
	// The trial is for brand-new users only: anyone who already has a linked
	// profile is ineligible.
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("trial: find caller failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка, попробуйте позже."))
		return true
	}
	if len(subs) > 0 {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("У вас уже есть профиль — пробный период доступен только новым пользователям."))
		return true
	}
	s.setTrial(chatID, &trialState{createdAt: s.now()})
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
		i18n.T("🎁 Бесплатный пробный период на %d %s!\nТрафик: %s\nУстройства: %s\n\nВведите имя профиля: латинские буквы A–Z/a–z, цифры 0–9, «_» или «-», от 3 до 36 символов, без пробелов. /cancel — отмена."),
		cfg.Days, i18n.PluralDays(cfg.Days), formatLimit(int64(cfg.TrafficLimitGB), " GB"), formatLimit(int64(cfg.HwidDeviceLimit), "")))
	return true
}

// handleTrialUsernameInput consumes a free-text message while a trial is
// awaiting the desired profile username. Returns true when it handled it.
// Username validity and availability are enforced by the shared claimTrial core.
func (s *Service) handleTrialUsernameInput(ctx context.Context, m *tg.Message) bool {
	chatID := m.Chat.ID
	if s.getTrial(chatID) == nil {
		return false
	}

	text := strings.TrimSpace(m.Text)
	if strings.HasPrefix(text, "/") {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите имя профиля: латинские буквы A–Z/a–z, цифры 0–9, «_» или «-», от 3 до 36 символов, без пробелов. /cancel — отмена."))
		return true
	}
	s.createTrial(ctx, chatID, text)
	return true
}

// createTrial runs the shared claim core and translates its result/sentinels to
// the bot chat messages. Thin wrapper around claimTrial so the bot and the mini
// app share identical eligibility, claiming and creation logic.
func (s *Service) createTrial(ctx context.Context, chatID int64, username string) {
	created, expireAt, err := s.claimTrial(ctx, chatID, username)
	switch {
	case errors.Is(err, ErrTrialDisabled):
		s.clearTrial(chatID)
		return
	case errors.Is(err, ErrBadInput):
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("Некорректное имя профиля. Используйте латинские буквы A–Z/a–z, цифры 0–9, «_» или «-»; от 3 до 36 символов, без пробелов."))
		return
	case errors.Is(err, ErrTrialNotEligible):
		s.clearTrial(chatID)
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("У вас уже есть профиль — пробный период доступен только новым пользователям."))
		return
	case errors.Is(err, ErrUsernameTaken):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Это имя занято, попробуйте другое."))
		return
	case errors.Is(err, ErrTrialAlreadyUsed):
		s.clearTrial(chatID)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Вы уже использовали пробный период."))
		return
	case errors.Is(err, ErrTrialPendingApproval):
		s.clearTrial(chatID)
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("🕓 Заявка на пробный период отправлена администратору. Мы уведомим вас о решении."))
		return
	case errors.Is(err, ErrTrialRequestPending):
		s.clearTrial(chatID)
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("🕓 Ваша заявка на пробный период уже на рассмотрении у администратора."))
		return
	case err != nil:
		s.clearTrial(chatID)
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка активации пробного периода. Попробуйте позже."))
		return
	}

	s.clearTrial(chatID)
	if created == nil { // dry-run
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
			i18n.T("✅ (dry-run) Пробный период активирован! Профиль «%s» создан, подписка до %s."),
			username, expireAt.Format("02.01.2006")))
		return
	}
	msg := fmt.Sprintf(i18n.T("✅ Пробный период активирован! Профиль «%s» создан, подписка до %s."),
		created.Username, expireAt.Format("02.01.2006"))
	if created.SubscriptionURL != "" {
		msg += i18n.T("\n\nВаша ссылка на подписку:\n") + created.SubscriptionURL
	}
	_ = s.bot.SendPlain(ctx, chatID, msg)
}

// claimTrial is the transport-agnostic free-trial core shared by the bot flow
// and the mini app endpoint. It validates eligibility, atomically claims the
// one-time trial, then creates a fresh panel profile bound to telegramID. The
// claim happens before creation so a retry or a race cannot grant two trials;
// if creation fails afterwards the claim is released. On dry-run it returns a
// nil CreatedUser with the computed expiry. It sends no Telegram messages and
// returns sentinel errors (ErrTrialDisabled / ErrBadInput / ErrTrialNotEligible
// / ErrUsernameTaken / ErrTrialAlreadyUsed / ErrPanelCreateFailed).
func (s *Service) claimTrial(ctx context.Context, telegramID int64, username string) (*CreatedUser, time.Time, error) {
	cfg := s.trialConfig()
	if !cfg.Enabled || s.creator == nil {
		return nil, time.Time{}, ErrTrialDisabled
	}
	username, err := normalizeProfileUsername(username)
	if err != nil {
		return nil, time.Time{}, err
	}

	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("trial: find caller: %w", err)
	}
	if len(subs) > 0 {
		return nil, time.Time{}, ErrTrialNotEligible
	}

	existing, err := s.finder.FindByUsername(ctx, username)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("trial: check username: %w", err)
	}
	if existing != nil {
		return nil, time.Time{}, ErrUsernameTaken
	}

	// Admin-approval gate: a new user without a referral does not get an instant
	// trial; their request is queued for an admin decision instead. Referred
	// users (vouched for by an existing subscriber) skip the gate.
	if cfg.RequireApproval && !s.hasReferral(ctx, telegramID) {
		return nil, time.Time{}, s.submitTrialForApproval(ctx, telegramID, username)
	}

	return s.grantTrial(ctx, telegramID, username, cfg, false)
}

// grantTrial creates the trial profile and binds it to telegramID. It is the
// instant-path tail of claimTrial and is reused by approveTrialRequest. When
// alreadyClaimed is false (instant path) it first reserves the one-time
// trial_claims row and rolls it back if creation fails. When true (admin
// approval) the claim was already reserved at request time, so it is neither
// re-claimed nor released on a transient panel error — the request stays pending
// for the admin to retry. username must already have passed
// normalizeProfileUsername — both callers normalize before the eligibility
// checks, because they look the name up in the panel first.
func (s *Service) grantTrial(ctx context.Context, telegramID int64, username string, cfg TrialConfig, alreadyClaimed bool) (*CreatedUser, time.Time, error) {
	var squadUUID string
	var err error
	if !s.dryRun {
		squadUUID, err = s.resolveTrialSquadUUID(ctx, cfg.SquadUUID)
		if err != nil {
			s.logger.Error("trial: resolve squad failed", "username", username, "err", err.Error())
			return nil, time.Time{}, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
		}
	}

	if !alreadyClaimed {
		ok, err := s.store.ClaimTrial(ctx, telegramID, username, s.now())
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("trial: claim: %w", err)
		}
		if !ok {
			return nil, time.Time{}, ErrTrialAlreadyUsed
		}
	}

	expireAt := s.now().AddDate(0, 0, cfg.Days)

	if s.dryRun {
		s.logger.Info("dry-run: would create trial user", "username", username,
			"telegram_id", telegramID, "expire_at", expireAt.Format("2006-01-02"))
		return nil, expireAt, nil
	}

	created, err := s.creator.CreateUser(ctx, CreateUserSpec{
		Username:             username,
		ExpireAt:             expireAt,
		SquadUUIDs:           []string{squadUUID},
		TrafficLimitBytes:    int64(cfg.TrafficLimitGB) * bytesPerGB,
		TrafficLimitStrategy: "NO_RESET",
		HwidDeviceLimit:      cfg.HwidDeviceLimit,
	})
	if err != nil {
		s.logger.Error("trial: create user failed", "username", username, "err", err.Error())
		if !alreadyClaimed {
			s.releaseTrialClaim(ctx, telegramID)
		}
		return nil, time.Time{}, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
	}

	// The profile exists; a failed Telegram binding is recoverable via /register,
	// so it must not fail the trial activation.
	if s.registrar != nil {
		if err := s.registrar.SetTelegramID(ctx, created.UUID, telegramID); err != nil {
			s.logger.Error("trial: set telegram id failed", "uuid", created.UUID, "err", err.Error())
		}
	}
	return created, expireAt, nil
}

func (s *Service) resolveTrialSquadUUID(ctx context.Context, configured string) (string, error) {
	if configured == "" {
		return s.resolveDefaultSquadUUID(ctx)
	}
	if s.squads == nil {
		return "", fmt.Errorf("trial squad %q cannot be validated", configured)
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		return "", fmt.Errorf("list internal squads: %w", err)
	}
	for _, squad := range squads {
		if squad.UUID == configured {
			return configured, nil
		}
	}
	return "", fmt.Errorf("configured trial squad %q no longer exists", configured)
}

// releaseTrialClaim rolls back a trial claim after a failed activation so the
// user does not lose their one trial to a transient error.
func (s *Service) releaseTrialClaim(ctx context.Context, telegramID int64) {
	if err := s.store.ReleaseTrial(ctx, telegramID); err != nil {
		s.logger.Error("trial: release claim failed", "telegram_id", telegramID, "err", err.Error())
	}
}

// submitTrialForApproval queues a trial for an admin decision: it reserves the
// one-time trial_claims row (so a rejection is final and a duplicate submit
// collapses to "already used"), records a pending request and DMs every admin
// approve/reject buttons. It returns ErrTrialPendingApproval on success — a
// control signal, not a failure — which the bot and mini app translate to a
// "request sent" message. A pre-existing pending request yields
// ErrTrialRequestPending. Eligibility (no profile, free username) has already
// been validated by the caller (claimTrial).
func (s *Service) submitTrialForApproval(ctx context.Context, telegramID int64, username string) error {
	pending, err := s.store.GetPendingTrialRequestByTelegramID(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("trial: check pending request: %w", err)
	}
	if pending != nil {
		return ErrTrialRequestPending
	}

	ok, err := s.store.ClaimTrial(ctx, telegramID, username, s.now())
	if err != nil {
		return fmt.Errorf("trial: claim: %w", err)
	}
	if !ok {
		return ErrTrialAlreadyUsed
	}

	reqID, err := s.store.CreateTrialRequest(ctx, telegramID, username, s.now())
	if err != nil {
		s.releaseTrialClaim(ctx, telegramID)
		return fmt.Errorf("trial: create request: %w", err)
	}

	s.notifyAdminsTrialRequest(ctx, reqID, telegramID, username)
	return ErrTrialPendingApproval
}

// notifyAdminsTrialRequest DMs every admin the pending trial request with
// approve/reject buttons and remembers the message refs so resolving clears the
// buttons in all admin chats. Shared by the bot flow and the mini app.
func (s *Service) notifyAdminsTrialRequest(ctx context.Context, reqID int64, telegramID int64, username string) {
	cfg := s.trialConfig()
	text := fmt.Sprintf(
		i18n.T("🎁 Заявка на пробный период\n\nПользователь: %s (TG %d)\nДлительность: %d %s\nТрафик: %s\nУстройства: %s"),
		username, telegramID, cfg.Days, i18n.PluralDays(cfg.Days),
		formatLimit(int64(cfg.TrafficLimitGB), " GB"), formatLimit(int64(cfg.HwidDeviceLimit), ""))
	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{{
			{Text: i18n.T("✅ Одобрить"), CallbackData: fmt.Sprintf("tr_ok:%d", reqID)},
			{Text: i18n.T("❌ Отклонить"), CallbackData: fmt.Sprintf("tr_rej:%d", reqID)},
		}},
	}
	var refs []adminMsgRef
	for _, adminID := range s.adminIDs {
		msgID, err := s.bot.SendPlainWithKeyboard(ctx, adminID, text, kb)
		if err != nil {
			s.logger.Error("trial: notify admin failed", "admin_id", adminID, "err", err.Error())
			continue
		}
		refs = append(refs, adminMsgRef{chatID: adminID, messageID: msgID})
	}
	s.putAdminMsgs(s.trialReqMsgs, reqID, refs)
}

// handleTrialApprove processes an admin's "Одобрить" button on a trial request.
func (s *Service) handleTrialApprove(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized trial approve attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "tr_ok:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	req, created, expireAt, err := s.approveTrialRequest(ctx, reqID)
	switch {
	case errors.Is(err, ErrPanelCreateFailed):
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Ошибка создания пользователя. Проверьте логи."))
		return true
	case errors.Is(err, ErrTrialNotEligible):
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("У пользователя уже есть профиль."))
		return true
	case errors.Is(err, ErrUsernameTaken):
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Это имя занято, заявку нельзя одобрить."))
		return true
	case err != nil:
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, resolveErrorText(err))
		return true
	}

	if created == nil { // dry-run
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Пробный период одобрен (dry-run)."))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("✅ Пробный период одобрен!"))
	_ = s.bot.SendPlain(ctx, cb.From.ID, fmt.Sprintf(
		i18n.T("✅ Пробный профиль «%s» создан для пользователя (TG %d), подписка до %s."),
		created.Username, req.TelegramID, expireAt.Format("02.01.2006")))
	return true
}

// handleTrialReject processes an admin's "Отклонить" button on a trial request.
func (s *Service) handleTrialReject(ctx context.Context, cb *tg.CallbackQuery) bool {
	if !s.isEnabled() || !s.isAdmin(cb.From.ID) {
		s.logger.Warn("unauthorized trial reject attempt", "from_id", cb.From.ID)
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Недостаточно прав."))
		return true
	}

	reqID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "tr_rej:"), 10, 64)
	if err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Не удалось распознать заявку."))
		return true
	}

	if _, err := s.rejectTrialRequest(ctx, reqID); err != nil {
		_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, resolveErrorText(err))
		return true
	}

	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, i18n.T("Заявка отклонена."))
	return true
}

// approveTrialRequest approves a pending trial: re-checks eligibility, creates
// the trial profile (the one-time claim is already held from request time), marks
// the request approved, clears the buttons in every admin's chat and notifies the
// requesting user. On a transient panel error it leaves the request pending so
// the admin can retry. Shared by the bot callback and the mini app admin API. On
// dry-run created is nil.
func (s *Service) approveTrialRequest(ctx context.Context, reqID int64) (*store.TrialRequest, *CreatedUser, time.Time, error) {
	req, err := s.store.GetTrialRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("trial: get request failed", "err", err.Error())
		return nil, nil, time.Time{}, fmt.Errorf("get trial request: %w", err)
	}
	if req == nil {
		return nil, nil, time.Time{}, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return nil, nil, time.Time{}, ErrRequestResolved
	}

	// Requests created before the stricter ASCII contract may contain a name
	// that Remnawave will reject. Resolve those requests without touching the
	// panel and atomically release the one-time claim so the user can submit a
	// corrected username.
	username, validationErr := normalizeProfileUsername(req.Username)
	if validationErr != nil {
		resolved, err := s.store.RejectInvalidTrialRequest(ctx, req.ID, req.TelegramID, req.Username, s.now())
		if err != nil {
			return req, nil, time.Time{}, fmt.Errorf("trial: reject invalid legacy request: %w", err)
		}
		if !resolved {
			return req, nil, time.Time{}, ErrRequestResolved
		}
		s.clearTrialButtons(ctx, reqID)
		if req.TelegramID != 0 {
			_ = s.bot.SendPlain(ctx, req.TelegramID,
				i18n.T("Некорректное имя профиля. Запустите /trial заново и выберите другое имя."))
		}
		return req, nil, time.Time{}, validationErr
	}
	req.Username = username

	// Re-check eligibility: the user may have gained a profile, or the username
	// may have been taken, between request and approval.
	subs, err := s.finder.FindByTelegramID(ctx, req.TelegramID)
	if err != nil {
		return req, nil, time.Time{}, fmt.Errorf("trial: find caller: %w", err)
	}
	if len(subs) > 0 {
		return req, nil, time.Time{}, ErrTrialNotEligible
	}
	existing, err := s.finder.FindByUsername(ctx, req.Username)
	if err != nil {
		return req, nil, time.Time{}, fmt.Errorf("trial: check username: %w", err)
	}
	if existing != nil {
		return req, nil, time.Time{}, ErrUsernameTaken
	}

	created, expireAt, err := s.grantTrial(ctx, req.TelegramID, req.Username, s.trialConfig(), true)
	if err != nil {
		// ErrPanelCreateFailed (and any other error) leaves the request pending.
		return req, nil, time.Time{}, err
	}

	if _, err := s.store.ResolveTrialRequest(ctx, reqID, "approved", s.now()); err != nil {
		s.logger.Error("trial: mark approved failed", "err", err.Error())
	}
	s.clearTrialButtons(ctx, reqID)

	if req.TelegramID != 0 {
		if created == nil { // dry-run
			_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
				i18n.T("✅ (dry-run) Пробный период одобрен! Профиль «%s» создан, подписка до %s."),
				req.Username, expireAt.Format("02.01.2006")))
		} else {
			msg := fmt.Sprintf(i18n.T("✅ Пробный период одобрен! Профиль «%s» создан, подписка до %s."),
				created.Username, expireAt.Format("02.01.2006"))
			if created.SubscriptionURL != "" {
				msg += i18n.T("\n\nВаша ссылка на подписку:\n") + created.SubscriptionURL
			}
			_ = s.bot.SendPlain(ctx, req.TelegramID, msg)
		}
	}
	return req, created, expireAt, nil
}

// rejectTrialRequest rejects a pending trial: marks it rejected, clears the
// buttons in every admin's chat and notifies the user. The one-time claim is
// kept (not released) so a rejection is final. Shared by the bot callback and
// the mini app admin API.
func (s *Service) rejectTrialRequest(ctx context.Context, reqID int64) (*store.TrialRequest, error) {
	req, err := s.store.GetTrialRequest(ctx, reqID)
	if err != nil {
		s.logger.Error("trial: get request failed", "err", err.Error())
		return nil, fmt.Errorf("get trial request: %w", err)
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return nil, ErrRequestResolved
	}

	if _, err := s.store.ResolveTrialRequest(ctx, reqID, "rejected", s.now()); err != nil {
		s.logger.Error("trial: mark rejected failed", "err", err.Error())
	}

	s.clearTrialButtons(ctx, reqID)
	if req.TelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.TelegramID,
			i18n.T("❌ Ваша заявка на пробный период отклонена администратором."))
	}
	return req, nil
}

// clearTrialButtons removes the approve/reject buttons from every admin's copy
// of the trial-request notification, then forgets the stored refs.
func (s *Service) clearTrialButtons(ctx context.Context, reqID int64) {
	refs := s.takeAdminMsgs(s.trialReqMsgs, reqID)
	for _, ref := range refs {
		if err := s.bot.EditMessageReplyMarkup(ctx, ref.chatID, ref.messageID, nil); err != nil {
			s.logger.Warn("clear admin trial button failed", "chat_id", ref.chatID, "err", err.Error())
		}
	}
}
