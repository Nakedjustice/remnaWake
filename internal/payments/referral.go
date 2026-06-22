package payments

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// referralDeepLinkPrefix is the /start payload prefix that records a referral.
const referralDeepLinkPrefix = "ref_"

// referralDeepLink returns the personal t.me referral link for a user, or ""
// when the bot username is unknown (the chat flow then falls back to manual
// /start instructions; the mini app simply hides the link).
func (s *Service) referralDeepLink(telegramID int64) string {
	bu := s.getBotUsername()
	if bu == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=%s%d", bu, referralDeepLinkPrefix, telegramID)
}

// StartReferral records a pending referral from a /start ref_<referrerID> deep
// link. chatID is the clicker's private chat = their Telegram ID. Best-effort:
// it never blocks the welcome screen that follows. A referral is recorded only
// for genuinely new users (no linked profile) brought in by an existing
// subscriber; self-referrals and repeat clicks are ignored (first referrer
// wins). When the program is disabled nothing is recorded.
func (s *Service) StartReferral(ctx context.Context, chatID int64, rawReferrerID string) {
	if enabled, _, _ := s.referralConfig(); !enabled {
		return
	}
	referrerID, err := strconv.ParseInt(rawReferrerID, 10, 64)
	if err != nil || referrerID == 0 || referrerID == chatID {
		return
	}
	// New users only: someone who already has a profile cannot be "referred".
	subs, err := s.finder.FindByTelegramID(ctx, chatID)
	if err != nil {
		s.logger.Error("referral: find clicker failed", "err", err.Error())
		return
	}
	if len(subs) > 0 {
		return
	}
	// The referrer must be a real subscriber; skip junk links.
	refSubs, err := s.finder.FindByTelegramID(ctx, referrerID)
	if err != nil {
		s.logger.Error("referral: find referrer failed", "err", err.Error())
		return
	}
	if len(refSubs) == 0 {
		return
	}

	recorded, err := s.store.CreateReferral(ctx, chatID, referrerID, s.now())
	if err != nil {
		s.logger.Error("referral: record failed", "err", err.Error())
		return
	}
	if recorded {
		_ = s.bot.SendPlain(ctx, chatID,
			i18n.T("🤝 Вы перешли по приглашению. Оформите первую подписку — и вы оба получите бонусные дни!"))
	}
}

// settleReferral pays out a link referral when the referred user makes their
// first payment: it credits the referrer with inviterDays exactly once (the
// invitee bonus is folded into the payment extension by the caller). Shared by
// the bot and mini app confirm paths; best-effort so it never fails a
// confirmation, and idempotent via the conditional CreditReferral update.
func (s *Service) settleReferral(ctx context.Context, ref *store.Referral, inviterDays, inviteeDays int) {
	ok, err := s.store.CreditReferral(ctx, ref.ReferredTelegramID, s.now())
	if err != nil {
		s.logger.Error("referral: credit failed", "err", err.Error())
		return
	}
	if !ok {
		return // already credited by a previous confirmation
	}
	if bonus := s.extendUserByDays(ctx, ref.ReferrerTelegramID, inviterDays); bonus > 0 {
		_ = s.bot.SendPlain(ctx, ref.ReferrerTelegramID, fmt.Sprintf(
			i18n.T("🎉 Приглашённый вами друг оформил первую подписку — вам начислено %d %s бонуса."),
			bonus, i18n.PluralDays(bonus)))
	}
	if inviteeDays > 0 {
		_ = s.bot.SendPlain(ctx, ref.ReferredTelegramID, fmt.Sprintf(
			i18n.T("🎉 Вам начислено %d %s бонуса за регистрацию по приглашению."),
			inviteeDays, i18n.PluralDays(inviteeDays)))
	}
}

// handleMenuReferral shows the user their personal referral link and stats.
func (s *Service) handleMenuReferral(ctx context.Context, cb *tg.CallbackQuery) bool {
	_ = s.bot.AnswerCallbackQuery(ctx, cb.ID, "")
	if cb.Message == nil {
		return true
	}
	chatID := cb.Message.Chat.ID
	enabled, inviterDays, inviteeDays := s.referralConfig()
	if !enabled {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Реферальная программа сейчас отключена."))
		return true
	}
	_ = s.bot.SendPlain(ctx, chatID, s.referralMessage(ctx, chatID, inviterDays, inviteeDays))
	return true
}

// referralMessage builds the referral text: how the program works, the user's
// personal link (or a manual /start fallback when the bot username is unknown)
// and their invited/converted counts.
func (s *Service) referralMessage(ctx context.Context, chatID int64, inviterDays, inviteeDays int) string {
	msg := fmt.Sprintf(i18n.T("🤝 Приглашайте друзей\n\nКогда друг перейдёт по вашей ссылке и оформит первую подписку, вы получите %d %s, а он — %d %s."),
		inviterDays, i18n.PluralDays(inviterDays), inviteeDays, i18n.PluralDays(inviteeDays))
	if link := s.referralDeepLink(chatID); link != "" {
		msg += i18n.T("\n\nВаша ссылка:\n") + link
	} else {
		msg += fmt.Sprintf(i18n.T("\n\nВаш реферальный код: %d\nПопросите друга отправить боту: /start %s%d"),
			chatID, referralDeepLinkPrefix, chatID)
	}
	if invited, credited, err := s.store.ReferralStats(ctx, chatID); err != nil {
		s.logger.Error("referral: stats failed", "err", err.Error())
	} else {
		msg += fmt.Sprintf(i18n.T("\n\nПриглашено: %d · Оформили подписку: %d"), invited, credited)
	}
	return msg
}
