package main

import (
	"context"
	"sync"

	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// updateConcurrency caps how many chats are handled at once. Almost every
// handler ends in a panel or Bot API call, so the useful number is bounded by
// how many of those the panel will happily serve, not by CPU.
const updateConcurrency = 8

// updateChatID returns the chat an update belongs to. It is the same key the
// conversation state on payments.Service is stored under, which is what makes
// "one chat at a time" the right unit of serialisation.
func updateChatID(u tgbot.Update) int64 {
	switch {
	case u.CallbackQuery != nil:
		if u.CallbackQuery.Message != nil {
			return u.CallbackQuery.Message.Chat.ID
		}
		return u.CallbackQuery.From.ID
	case u.PreCheckoutQuery != nil:
		return u.PreCheckoutQuery.From.ID
	case u.Message != nil:
		return u.Message.Chat.ID
	}
	return 0
}

// dispatchUpdates handles a batch of updates, in parallel across chats and
// strictly in order within one chat.
//
// The update loop used to run the whole batch on one goroutine, so a single
// handler waiting on the panel (up to HTTP_TIMEOUT) delayed every other user in
// the batch behind it.
//
// It returns only once the batch is fully handled. That is deliberate: the next
// getUpdates call is what confirms these updates to Telegram, so anything still
// in flight at that point would be lost on a crash instead of redelivered.
func dispatchUpdates(ctx context.Context, updates []tgbot.Update, handle func(context.Context, tgbot.Update)) {
	if ctx.Err() != nil {
		return
	}
	if len(updates) < 2 {
		for _, u := range updates {
			handle(ctx, u)
		}
		return
	}

	groups := make(map[int64][]tgbot.Update, len(updates))
	order := make([]int64, 0, len(updates))
	for _, u := range updates {
		id := updateChatID(u)
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], u)
	}
	// A batch from one chat has nothing to parallelise; stay on this goroutine.
	if len(order) == 1 {
		for _, u := range groups[order[0]] {
			if ctx.Err() != nil {
				return
			}
			handle(ctx, u)
		}
		return
	}

	sem := make(chan struct{}, updateConcurrency)
	var wg sync.WaitGroup
	for _, id := range order {
		wg.Add(1)
		go func(batch []tgbot.Update) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			for _, u := range batch {
				if ctx.Err() != nil {
					return
				}
				handle(ctx, u)
			}
		}(groups[id])
	}
	wg.Wait()
}
