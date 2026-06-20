package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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
	enabled, days := s.trialConfig()
	if !s.isEnabled() || !enabled || s.creator == nil {
		return false
	}
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
		i18n.T("🎁 Бесплатный пробный период на %d %s!\n\nВведите желаемое имя пользователя для вашего профиля (буквы, цифры и «_», от 3 до 32 символов). /cancel — отмена."),
		days, i18n.PluralDays(days)))
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
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите имя пользователя или /cancel для отмены."))
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
			i18n.T("Некорректное имя: только буквы, цифры и «_», от 3 до 32 символов."))
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
	enabled, days := s.trialConfig()
	if !enabled || s.creator == nil {
		return nil, time.Time{}, ErrTrialDisabled
	}
	if !isValidUsername(username) {
		return nil, time.Time{}, ErrBadInput
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

	ok, err := s.store.ClaimTrial(ctx, telegramID, username, s.now())
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("trial: claim: %w", err)
	}
	if !ok {
		return nil, time.Time{}, ErrTrialAlreadyUsed
	}

	expireAt := s.now().AddDate(0, 0, days)

	if s.dryRun {
		s.logger.Info("dry-run: would create trial user", "username", username,
			"telegram_id", telegramID, "expire_at", expireAt.Format("2006-01-02"))
		return nil, expireAt, nil
	}

	squadUUID, err := s.resolveDefaultSquadUUID(ctx)
	if err != nil {
		s.logger.Error("trial: resolve default squad failed", "username", username, "err", err.Error())
		s.releaseTrialClaim(ctx, telegramID)
		return nil, time.Time{}, fmt.Errorf("%w: %v", ErrPanelCreateFailed, err)
	}

	created, err := s.creator.CreateUser(ctx, username, expireAt, []string{squadUUID}, s.getDefaultTrafficReset())
	if err != nil {
		s.logger.Error("trial: create user failed", "username", username, "err", err.Error())
		s.releaseTrialClaim(ctx, telegramID)
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

// releaseTrialClaim rolls back a trial claim after a failed activation so the
// user does not lose their one trial to a transient error.
func (s *Service) releaseTrialClaim(ctx context.Context, telegramID int64) {
	if err := s.store.ReleaseTrial(ctx, telegramID); err != nil {
		s.logger.Error("trial: release claim failed", "telegram_id", telegramID, "err", err.Error())
	}
}
