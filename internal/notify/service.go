package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

var triggerDays = []int{7, 3, 1}

// userSource is the subset of *remnawave.Client the notify job needs.
type userSource interface {
	GetUsers(ctx context.Context) ([]remnawave.User, error)
}

// sender is the subset of *telegram.Bot the notify job needs.
type sender interface {
	SendWithKeyboard(ctx context.Context, chatID int64, text string, kb *tgbot.InlineKeyboardMarkup) error
}

// payFlow is the subset of *payments.Service the notify job needs.
type payFlow interface {
	RememberUser(ctx context.Context, u store.NotifiedUser) error
	PaymentButton(userID int64) *tgbot.InlineKeyboardMarkup
}

// sentMarker is the subset of *store.Store the notify job needs to deduplicate
// notifications across runs and restarts.
type sentMarker interface {
	TryMarkNotificationSent(ctx context.Context, remnawaveID int64, kind string, milestone int, expireAt time.Time) (bool, error)
	UnmarkNotificationSent(ctx context.Context, remnawaveID int64, kind string, milestone int, expireAt time.Time) error
}

type Service struct {
	rw          userSource
	tg          sender
	pay         payFlow
	marks       sentMarker
	logger      *slog.Logger
	dryRun      bool
	winbackDays []int // days after expiry to send win-back messages; empty = off
	now         func() time.Time
}

func NewService(rw userSource, tg sender, pay payFlow, marks sentMarker, logger *slog.Logger, dryRun bool, winbackDays []int) *Service {
	return &Service{
		rw:          rw,
		tg:          tg,
		pay:         pay,
		marks:       marks,
		logger:      logger,
		dryRun:      dryRun,
		winbackDays: winbackDays,
		now:         time.Now,
	}
}

func (s *Service) Run(ctx context.Context) error {
	startedAt := s.now()
	logger := s.logger.With("job", "notify", "started_at", startedAt.Format(time.RFC3339))
	logger.Info("job started")

	users, err := s.rw.GetUsers(ctx)
	if err != nil {
		logger.Error("remnawave get users failed", "err", err.Error())
		return err
	}
	logger.Info("users fetched", "count", len(users))

	now := s.now()
	var (
		active        int
		skippedNoTg   int
		skippedNoExp  int
		skippedStatus int
		skippedDup    int
		notified      int
		winbackSent   int
		failed        int
	)

	for i := range users {
		u := &users[i]

		// Classify: pre-expiry reminder for active users, win-back for expired
		// ones. ACTIVE with a past expiry covers panel status lag; DISABLED and
		// LIMITED accounts were turned off deliberately and get neither.
		var kind string
		var days int
		switch {
		case u.Status == remnawave.StatusActive && (u.ExpireAt.IsZero() || u.ExpireAt.After(now)):
			active++
			if u.TelegramID == nil || *u.TelegramID == 0 {
				skippedNoTg++
				continue
			}
			if u.ExpireAt.IsZero() {
				skippedNoExp++
				continue
			}
			days = daysUntil(now, u.ExpireAt)
			if !shouldNotify(days) {
				continue
			}
			kind = store.NotificationExpiry
		case (u.Status == remnawave.StatusExpired || u.Status == remnawave.StatusActive) &&
			!u.ExpireAt.IsZero() && !u.ExpireAt.After(now):
			if len(s.winbackDays) == 0 {
				continue
			}
			if u.TelegramID == nil || *u.TelegramID == 0 {
				skippedNoTg++
				continue
			}
			days = daysSince(now, u.ExpireAt)
			if !containsDay(s.winbackDays, days) {
				continue
			}
			kind = store.NotificationWinback
		default:
			skippedStatus++
			continue
		}

		var text string
		if kind == store.NotificationExpiry {
			text = fmt.Sprintf("⏰ %s, ваша подписка истекает %s — через %d %s.\nДля продления оплатите подписку.",
				u.Username, u.ExpireAt.Format("02.01.2006"), days, pluralDays(days))
		} else {
			text = fmt.Sprintf("⛔️ %s, ваша подписка истекла %s.\nЧтобы продолжить пользоваться сервисом, продлите подписку.",
				u.Username, u.ExpireAt.Format("02.01.2006"))
		}

		chatID := *u.TelegramID
		logEntry := logger.With(
			"user_id", u.ID,
			"uuid", u.UUID,
			"username", u.Username,
			"chat_id", chatID,
			"expire_at", u.ExpireAt.Format(time.RFC3339),
			"kind", kind,
			"days", days,
		)

		if s.dryRun {
			logEntry.Info("dry-run: would send", "text", text)
			if kind == store.NotificationWinback {
				winbackSent++
			} else {
				notified++
			}
			continue
		}

		// Claim this (user, kind, milestone, expiry) before sending so a restart
		// mid-run cannot deliver the same notice twice.
		ok, err := s.marks.TryMarkNotificationSent(ctx, u.ID, kind, days, u.ExpireAt)
		if err != nil {
			logEntry.Error("mark notification failed", "err", err.Error())
			failed++
			continue
		}
		if !ok {
			skippedDup++
			continue
		}

		// Persist a snapshot so the «Я оплатил» flow works (even after a restart).
		if err := s.pay.RememberUser(ctx, store.NotifiedUser{
			RemnawaveID: u.ID,
			UUID:        u.UUID,
			Username:    u.Username,
			TelegramID:  chatID,
			ExpireAt:    u.ExpireAt,
		}); err != nil {
			s.logger.Error("remember user failed", "err", err.Error(), "user_id", u.ID)
		}

		if err := s.tg.SendWithKeyboard(ctx, chatID, text, s.pay.PaymentButton(u.ID)); err != nil {
			logEntry.Error("telegram send failed", "err", err.Error())
			failed++
			// Release the claim so the next run retries this milestone.
			if uerr := s.marks.UnmarkNotificationSent(ctx, u.ID, kind, days, u.ExpireAt); uerr != nil {
				logEntry.Error("unmark notification failed", "err", uerr.Error())
			}
			continue
		}
		logEntry.Info("notification sent", "text", text)
		if kind == store.NotificationWinback {
			winbackSent++
		} else {
			notified++
		}
	}

	logger.Info("job done",
		"active", active,
		"skipped_no_telegram", skippedNoTg,
		"skipped_no_expire", skippedNoExp,
		"skipped_inactive", skippedStatus,
		"skipped_dup", skippedDup,
		"notified", notified,
		"winback", winbackSent,
		"failed", failed,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	return nil
}

func shouldNotify(days int) bool {
	return containsDay(triggerDays, days)
}

func containsDay(list []int, day int) bool {
	for _, d := range list {
		if day == d {
			return true
		}
	}
	return false
}

func daysUntil(now, exp time.Time) int {
	diff := exp.Sub(now)
	if diff <= 0 {
		return 0
	}
	return int(diff / (24 * time.Hour))
}

// daysSince is the post-expiry mirror of daysUntil: whole days elapsed since exp.
func daysSince(now, exp time.Time) int {
	diff := now.Sub(exp)
	if diff <= 0 {
		return 0
	}
	return int(diff / (24 * time.Hour))
}

func pluralDays(n int) string {
	last := n % 100
	if last >= 11 && last <= 14 {
		return "дней"
	}
	switch n % 10 {
	case 1:
		return "день"
	case 2, 3, 4:
		return "дня"
	default:
		return "дней"
	}
}
