package payments

import (
	"context"
	"time"
)

// broadcastSendDelay paces broadcast sends to stay under Telegram's ~30
// messages-per-second bot limit.
const broadcastSendDelay = 50 * time.Millisecond

// broadcastMessage sends text to every panel user with a linked Telegram ID,
// regardless of subscription status, deduplicated by chat ID (several panel
// profiles may share one Telegram account). Send failures are logged and
// counted but do not stop the loop; err is non-nil only when the user list
// itself cannot be fetched.
func (s *Service) broadcastMessage(ctx context.Context, text string) (sent, failed int, err error) {
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		return 0, 0, err
	}
	seen := make(map[int64]bool, len(subs))
	chatIDs := make([]int64, 0, len(subs))
	for i := range subs {
		id := subs[i].TelegramID
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		chatIDs = append(chatIDs, id)
	}
	for i, chatID := range chatIDs {
		if s.dryRun {
			s.logger.Info("dry-run: would broadcast", "telegram_id", chatID)
			sent++
			continue
		}
		if i > 0 {
			time.Sleep(broadcastSendDelay)
		}
		if err := s.bot.SendPlain(ctx, chatID, text); err != nil {
			s.logger.Warn("broadcast send failed", "telegram_id", chatID, "err", err.Error())
			failed++
			continue
		}
		sent++
	}
	s.logger.Info("broadcast finished", "sent", sent, "failed", failed)
	return sent, failed, nil
}
