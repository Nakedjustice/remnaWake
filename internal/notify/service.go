package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/remnawave-notify-bot/internal/remnawave"
	tgbot "github.com/anomalyco/remnawave-notify-bot/internal/telegram"
)

var triggerDays = []int{7, 3, 1}

const (
	paymentCallbackPrefix      = "paid:"
	confirmPaymentCallbackPrefix = "confirm_payment:"
)

type Service struct {
	rw              *remnawave.Client
	tg              *tgbot.Bot
	logger          *slog.Logger
	dryRun          bool
	adminTelegramID int64
	now             func() time.Time

	paidUsersMu    sync.RWMutex
	paidUsers      map[int64]paymentUser
	confirmedUUIDs map[string]bool
}

type paymentUser struct {
	ID         int64
	UUID       string
	Username   string
	TelegramID int64
	ExpireAt   time.Time
}

func NewService(rw *remnawave.Client, tg *tgbot.Bot, logger *slog.Logger, dryRun bool, adminTelegramID int64) *Service {
	return &Service{
		rw:              rw,
		tg:              tg,
		logger:          logger,
		dryRun:          dryRun,
		adminTelegramID: adminTelegramID,
		now:             time.Now,
		paidUsers:       make(map[int64]paymentUser),
		confirmedUUIDs:  make(map[string]bool),
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
		keyboard := s.paymentKeyboard(u)

		chatID := *u.TelegramID
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

func (s *Service) HandleCallback(ctx context.Context, cb *tgbot.CallbackQuery) bool {
	if cb == nil {
		return false
	}

	// Handle payment confirmation (admin button)
	if strings.HasPrefix(cb.Data, confirmPaymentCallbackPrefix) {
		return s.handleConfirmPayment(ctx, cb)
	}

	// Handle user payment notification (user button)
	if strings.HasPrefix(cb.Data, paymentCallbackPrefix) {
		return s.handleUserPayment(ctx, cb)
	}

	return false
}

func (s *Service) handleConfirmPayment(ctx context.Context, cb *tgbot.CallbackQuery) bool {
	// Only the configured admin may confirm a payment.
	if s.adminTelegramID == 0 || cb.From.ID != s.adminTelegramID {
		s.logger.Warn("unauthorized confirm payment attempt", "from_id", cb.From.ID)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Недостаточно прав для подтверждения оплаты.")
		return true
	}

	uuid, err := parseConfirmPaymentCallbackData(cb.Data)
	if err != nil {
		s.logger.Warn("invalid confirm payment callback", "data", cb.Data, "err", err.Error())
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку.")
		return true
	}

	// Idempotency: never extend the same subscription twice on repeated taps.
	s.paidUsersMu.RLock()
	alreadyConfirmed := s.confirmedUUIDs[uuid]
	s.paidUsersMu.RUnlock()
	if alreadyConfirmed {
		s.logger.Info("confirm payment ignored: already confirmed", "uuid", uuid)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Подписка уже была продлена ранее.")
		return true
	}

	// Find user by UUID in memory
	var foundUser *paymentUser
	s.paidUsersMu.RLock()
	for _, u := range s.paidUsers {
		if u.UUID == uuid {
			uCopy := u
			foundUser = &uCopy
			break
		}
	}
	s.paidUsersMu.RUnlock()

	if foundUser == nil {
		s.logger.Warn("user not found by UUID", "uuid", uuid)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Пользователь не найден в памяти бота.")
		return true
	}

	// Extend by 1 month from whichever is later: now or the current expiry,
	// so a late confirmation never lands the new expiry in the past.
	base := foundUser.ExpireAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, 1, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would extend subscription", "uuid", uuid, "old_expire", foundUser.ExpireAt.Format(time.RFC3339), "new_expire", newExpireAt.Format(time.RFC3339))
		s.markConfirmed(foundUser.ID, uuid, newExpireAt)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Подписка продлена (dry-run).")
		_ = s.tg.SendPlain(ctx, s.adminTelegramID, fmt.Sprintf("✅ Подписка для %s продлена до %s (dry-run)", foundUser.Username, newExpireAt.Format("02.01.2006")))
		return true
	}

	if err := s.rw.ExtendSubscriptionByUUID(ctx, uuid, newExpireAt); err != nil {
		s.logger.Error("extend subscription failed", "uuid", uuid, "err", err.Error())
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Ошибка продления подписки. Проверьте логи.")
		return true
	}

	s.markConfirmed(foundUser.ID, uuid, newExpireAt)
	s.logger.Info("subscription extended", "uuid", uuid, "old_expire", foundUser.ExpireAt.Format(time.RFC3339), "new_expire", newExpireAt.Format(time.RFC3339))
	_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "✅ Подписка успешно продлена на 1 месяц!")
	_ = s.tg.SendPlain(ctx, s.adminTelegramID, fmt.Sprintf("✅ Подписка для %s продлена до %s", foundUser.Username, newExpireAt.Format("02.01.2006")))

	return true
}

// markConfirmed records a successful extension so repeated confirm taps are
// ignored, and refreshes the cached expiry for the user.
func (s *Service) markConfirmed(userID int64, uuid string, newExpireAt time.Time) {
	s.paidUsersMu.Lock()
	defer s.paidUsersMu.Unlock()
	s.confirmedUUIDs[uuid] = true
	if existing, ok := s.paidUsers[userID]; ok {
		existing.ExpireAt = newExpireAt
		s.paidUsers[userID] = existing
	}
}

func (s *Service) handleUserPayment(ctx context.Context, cb *tgbot.CallbackQuery) bool {
	userID, err := parsePaymentCallbackData(cb.Data)
	if err != nil {
		s.logger.Warn("invalid payment callback", "data", cb.Data, "err", err.Error())
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Не удалось распознать заявку. Напишите администратору.")
		return true
	}

	if s.adminTelegramID == 0 {
		s.logger.Warn("payment callback ignored: TELEGRAM_ADMIN_ID is not configured", "user_id", userID)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Заявка получена, но администратор не настроен.")
		return true
	}

	paidUser, found := s.lookupPaymentUser(userID)
	text := formatPaymentNotification(paidUser, found, cb)
	// Returns nil when the user (and thus UUID) is unknown, so the admin never
	// gets a confirm button that cannot work.
	keyboard := s.confirmPaymentKeyboard(paidUser.UUID)

	if s.dryRun {
		s.logger.Info("dry-run: would notify admin about payment", "admin_telegram_id", s.adminTelegramID, "text", text)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Спасибо! Заявка об оплате получена.")
		return true
	}

	// Sent as plain text (no parse mode) because it embeds user-controlled
	// names that would otherwise break HTML entity parsing.
	if err := s.tg.SendPlainWithKeyboard(ctx, s.adminTelegramID, text, keyboard); err != nil {
		s.logger.Error("payment admin notification failed", "err", err.Error(), "user_id", userID)
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Не удалось отправить уведомление. Напишите администратору.")
		return true
	}

	s.logger.Info("payment admin notification sent", "admin_telegram_id", s.adminTelegramID, "user_id", userID)
	_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Спасибо! Заявка об оплате получена.")
	return true
}

func (s *Service) paymentKeyboard(u *remnawave.User) *tgbot.InlineKeyboardMarkup {
	if s.adminTelegramID == 0 || u == nil {
		return nil
	}
	s.rememberPaymentUser(u)
	return &tgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]tgbot.InlineKeyboardButton{
			{
				{
					Text:         "Я оплатил",
					CallbackData: paymentCallbackData(u.ID),
				},
			},
		},
	}
}

func (s *Service) confirmPaymentKeyboard(uuid string) *tgbot.InlineKeyboardMarkup {
	if s.adminTelegramID == 0 || uuid == "" {
		return nil
	}
	return &tgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]tgbot.InlineKeyboardButton{
			{
				{
					Text:         "Подтвердить оплату",
					CallbackData: confirmPaymentCallbackData(uuid),
				},
			},
		},
	}
}

func (s *Service) rememberPaymentUser(u *remnawave.User) {
	telegramID := int64(0)
	if u.TelegramID != nil {
		telegramID = *u.TelegramID
	}
	s.paidUsersMu.Lock()
	defer s.paidUsersMu.Unlock()
	s.paidUsers[u.ID] = paymentUser{
		ID:         u.ID,
		UUID:       u.UUID,
		Username:   u.Username,
		TelegramID: telegramID,
		ExpireAt:   u.ExpireAt,
	}
}

func (s *Service) lookupPaymentUser(userID int64) (paymentUser, bool) {
	s.paidUsersMu.RLock()
	defer s.paidUsersMu.RUnlock()
	u, ok := s.paidUsers[userID]
	return u, ok
}

func paymentCallbackData(userID int64) string {
	return fmt.Sprintf("%s%d", paymentCallbackPrefix, userID)
}

func parsePaymentCallbackData(data string) (int64, error) {
	raw := strings.TrimPrefix(data, paymentCallbackPrefix)
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID == 0 {
		return 0, fmt.Errorf("invalid callback data %q", data)
	}
	return userID, nil
}

func confirmPaymentCallbackData(uuid string) string {
	return fmt.Sprintf("%s%s", confirmPaymentCallbackPrefix, uuid)
}

func parseConfirmPaymentCallbackData(data string) (string, error) {
	raw := strings.TrimPrefix(data, confirmPaymentCallbackPrefix)
	if raw == "" {
		return "", fmt.Errorf("invalid callback data %q", data)
	}
	return raw, nil
}

func formatPaymentNotification(u paymentUser, found bool, cb *tgbot.CallbackQuery) string {
	var b strings.Builder
	b.WriteString("💳 Пользователь нажал кнопку «Я оплатил»")
	if found {
		b.WriteString("\n\nКлиент: ")
		b.WriteString(u.Username)
		b.WriteString(fmt.Sprintf("\nRemnawave ID: %d", u.ID))
		if u.UUID != "" {
			b.WriteString("\nUUID: ")
			b.WriteString(u.UUID)
		}
		if u.TelegramID != 0 {
			b.WriteString(fmt.Sprintf("\nTelegram ID из Remnawave: %d", u.TelegramID))
		}
		if !u.ExpireAt.IsZero() {
			b.WriteString("\nПодписка до: ")
			b.WriteString(u.ExpireAt.Format(time.RFC3339))
		}
	} else {
		b.WriteString("\n\nRemnawave ID: ")
		b.WriteString(strings.TrimPrefix(cb.Data, paymentCallbackPrefix))
		b.WriteString("\nДетали клиента не найдены в памяти бота. Дождитесь следующего запуска уведомлений или проверьте ID в Remnawave.")
	}

	b.WriteString("\n\nНажал: ")
	b.WriteString(formatTelegramUser(cb.From))
	if cb.Message != nil {
		b.WriteString(fmt.Sprintf("\nЧат: %d", cb.Message.Chat.ID))
	}
	return b.String()
}

func formatTelegramUser(u tgbot.User) string {
	name := strings.TrimSpace(strings.Join([]string{u.FirstName, u.LastName}, " "))
	parts := make([]string, 0, 3)
	if name != "" {
		parts = append(parts, name)
	}
	if u.Username != "" {
		parts = append(parts, "@"+u.Username)
	}
	parts = append(parts, fmt.Sprintf("%d", u.ID))
	return strings.Join(parts, " / ")
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
