package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

var triggerDays = []int{7, 3, 1}

type Service struct {
	rw     *remnawave.Client
	tg     *tgbot.Bot
	pay    *payments.Service
	logger *slog.Logger
	dryRun bool
	now    func() time.Time
}

func NewService(rw *remnawave.Client, tg *tgbot.Bot, pay *payments.Service, logger *slog.Logger, dryRun bool) *Service {
	return &Service{
		rw:     rw,
		tg:     tg,
		pay:    pay,
		logger: logger,
		dryRun: dryRun,
		now:    time.Now,
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
		notified      int
		failed        int
	)

	for i := range users {
		u := &users[i]
		if u.Status != remnawave.StatusActive {
			skippedStatus++
			continue
		}
		active++
		if u.TelegramID == nil || *u.TelegramID == 0 {
			skippedNoTg++
			continue
		}
		if u.ExpireAt.IsZero() {
			skippedNoExp++
			continue
		}
		if !u.ExpireAt.After(now) {
			continue
		}

		days := daysUntil(now, u.ExpireAt)
		if !shouldNotify(days) {
			continue
		}

		text := fmt.Sprintf("⏰ Подписка истекает через %d %s. Для продления оплатите подписку.",
			days, pluralDays(days))

		chatID := *u.TelegramID
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
		keyboard := s.pay.PaymentButton(u.ID)

		logEntry := logger.With(
			"user_id", u.ID,
			"uuid", u.UUID,
			"username", u.Username,
			"chat_id", chatID,
			"expire_at", u.ExpireAt.Format(time.RFC3339),
			"days_left", days,
		)

		if s.dryRun {
			logEntry.Info("dry-run: would send", "text", text)
			notified++
			continue
		}

		if err := s.tg.SendWithKeyboard(ctx, chatID, text, keyboard); err != nil {
			logEntry.Error("telegram send failed", "err", err.Error())
			failed++
			continue
		}
		logEntry.Info("notification sent", "text", text)
		notified++
	}

	logger.Info("job done",
		"active", active,
		"skipped_no_telegram", skippedNoTg,
		"skipped_no_expire", skippedNoExp,
		"skipped_inactive", skippedStatus,
		"notified", notified,
		"failed", failed,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	return nil
}

func shouldNotify(days int) bool {
	for _, d := range triggerDays {
		if days == d {
			return true
		}
	}
	return false
}

func daysUntil(now, exp time.Time) int {
	// Anchor both midnights in the same location; the panel returns expireAt in
	// UTC while `now` is in the configured TZ, and mixing zones skews the count.
	loc := now.Location()
	exp = exp.In(loc)
	expMidnight := time.Date(exp.Year(), exp.Month(), exp.Day(), 0, 0, 0, 0, loc)
	nowMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	diff := expMidnight.Sub(nowMidnight)
	if diff <= 0 {
		return 0
	}
	days := int(diff / (24 * time.Hour))
	if days < 0 {
		return 0
	}
	return days
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
