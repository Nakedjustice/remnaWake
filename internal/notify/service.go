package notify

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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

// muteChecker is the subset of *store.Store the notify job needs to honour
// per-user notification mute preferences.
type muteChecker interface {
	NotificationMuted(ctx context.Context, telegramID int64, kind string) (bool, error)
}

type Service struct {
	rw          userSource
	tg          sender
	pay         payFlow
	marks       sentMarker
	prefs       muteChecker
	logger      *slog.Logger
	dryRun      bool
	winbackDays []int // days after expiry to send win-back messages; empty = off
	now         func() time.Time
	// sendWindow, when non-nil, gates delivery: Run only proceeds if it returns
	// true for the current time. main wires this to the daily RUN_AT window so an
	// off-hour RUN_ON_START sweep (e.g. a restart at 00:09) doesn't page users
	// outside the configured schedule. nil means "always send" (tests, etc.).
	sendWindow func(time.Time) bool
}

func NewService(rw userSource, tg sender, pay payFlow, marks sentMarker, prefs muteChecker, logger *slog.Logger, dryRun bool, winbackDays []int) *Service {
	return &Service{
		rw:          rw,
		tg:          tg,
		pay:         pay,
		marks:       marks,
		prefs:       prefs,
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

	// Stay silent outside the configured daily window. A RUN_ON_START sweep after
	// an off-hour restart would otherwise page everyone at the restart time (e.g.
	// 00:09) instead of RUN_AT. Returning before any send keeps the dedup store
	// untouched, so the next in-window run still delivers.
	if s.sendWindow != nil && !s.sendWindow(startedAt) {
		logger.Info("skipped: outside daily send window")
		return nil
	}

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
		skippedMuted  int
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

		// Honour the user's per-kind mute preference (best-effort: a lookup error
		// falls through and still notifies). Applies on both the dry-run and real
		// paths so logs reflect what would actually be sent.
		if s.prefs != nil {
			if muted, err := s.prefs.NotificationMuted(ctx, *u.TelegramID, kind); err != nil {
				logger.Error("check notification mute failed", "err", err.Error(), "chat_id", *u.TelegramID)
			} else if muted {
				skippedMuted++
				continue
			}
		}

		var text string
		username := html.EscapeString(u.Username)
		if kind == store.NotificationExpiry {
			text = fmt.Sprintf(i18n.T("⏰ %s, ваша подписка истекает %s — через %d %s.\nДля продления оплатите подписку."),
				username, u.ExpireAt.Format("02.01.2006"), days, i18n.PluralDays(days))
		} else {
			text = fmt.Sprintf(i18n.T("⛔️ %s, ваша подписка истекла %s.\nЧтобы продолжить пользоваться сервисом, продлите подписку."),
				username, u.ExpireAt.Format("02.01.2006"))
		}

		chatID := *u.TelegramID
		logEntry := logger.With(
			"user_id", u.ID,
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
		"skipped_muted", skippedMuted,
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

// daysUntil reports the whole days remaining until exp, rounding any partial
// day up. This matches the Remnawave panel, which shows e.g. "8 days left" while
// a subscription still has 7 days and some hours to run. Flooring here made the
// 7-day reminder fire (and read "7 days") when the panel still said 8, which is
// the mismatch users reported. Returns 0 once exp is reached.
func daysUntil(now, exp time.Time) int {
	diff := exp.Sub(now)
	if diff <= 0 {
		return 0
	}
	days := int(diff / (24 * time.Hour))
	if diff%(24*time.Hour) != 0 {
		days++
	}
	return days
}

// daysSince reports the whole days elapsed since exp, flooring any partial day.
// It is the post-expiry counterpart to daysUntil but deliberately rounds the
// other way: a win-back milestone like "1 day after expiry" must mean a full day
// has actually passed, not the first minute past the deadline.
func daysSince(now, exp time.Time) int {
	diff := now.Sub(exp)
	if diff <= 0 {
		return 0
	}
	return int(diff / (24 * time.Hour))
}

// SetSendWindow restricts delivery to the daily window [runAt, runAt+window) in
// loc. Outside it, Run is a no-op that sends nothing and writes no dedup marks,
// so the next in-window run still delivers. The configured RUN_AT run, being
// inside the window, is unaffected. A nil loc or non-positive window clears the
// restriction (Run always proceeds).
func (s *Service) SetSendWindow(loc *time.Location, runAtHour, runAtMin int, window time.Duration) {
	if loc == nil || window <= 0 {
		s.sendWindow = nil
		return
	}
	s.sendWindow = func(now time.Time) bool {
		return withinDailyWindow(now, loc, runAtHour, runAtMin, window)
	}
}

// withinDailyWindow reports whether now (evaluated in loc) falls in the daily
// window [runAt, runAt+window). It also checks the previous day's window so a
// window straddling midnight still matches the early-morning side.
func withinDailyWindow(now time.Time, loc *time.Location, hour, min int, window time.Duration) bool {
	n := now.In(loc)
	start := time.Date(n.Year(), n.Month(), n.Day(), hour, min, 0, 0, loc)
	for _, s := range [2]time.Time{start, start.AddDate(0, 0, -1)} {
		if !n.Before(s) && n.Before(s.Add(window)) {
			return true
		}
	}
	return false
}
