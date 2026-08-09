package payments

import (
	"context"
	"errors"
	"time"
)

// broadcastSendDelay paces broadcast sends to stay under Telegram's ~30
// messages-per-second bot limit.
const broadcastSendDelay = 50 * time.Millisecond

// ErrBroadcastInProgress reports that a broadcast is already running. Both
// transports return immediately now, so a second tap is one click away — and it
// would deliver the message to every user twice.
var ErrBroadcastInProgress = errors.New("broadcast already in progress")

// startBroadcast runs a broadcast detached from its caller and hands the result
// to report. Shared by the bot callback and the mini app admin API: at ~20
// messages a second a real panel takes minutes, which neither the sequential
// update loop nor an HTTP request can be held open for.
//
// The run context is detached from the caller's: an HTTP request context is
// cancelled the moment its response is written, which would kill the send loop
// on its first message.
func (s *Service) startBroadcast(ctx context.Context, text string, report func(ctx context.Context, sent, failed int, err error)) error {
	s.mu.Lock()
	if s.broadcasting {
		s.mu.Unlock()
		return ErrBroadcastInProgress
	}
	s.broadcasting = true
	s.mu.Unlock()

	runCtx := context.WithoutCancel(ctx)
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		sent, failed, err := s.broadcastMessage(runCtx, text)
		s.mu.Lock()
		s.broadcasting = false
		s.mu.Unlock()
		report(runCtx, sent, failed, err)
	}()
	return nil
}

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
