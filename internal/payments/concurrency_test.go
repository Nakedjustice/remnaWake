package payments

import (
	"context"
	"fmt"
	"sync"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// TestServiceHandlesManyChatsConcurrently exercises the property the update
// dispatcher relies on: different chats may be handled at the same time.
//
// Go's runtime aborts the process on an unsynchronised map access, so this
// catches the failure mode that matters most for the per-chat state maps on
// Service — without needing the race detector, which cannot run on a box with
// no C compiler.
func TestServiceHandlesManyChatsConcurrently(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{}}
	ctx := context.Background()

	const chats = 64
	var wg sync.WaitGroup
	for i := 0; i < chats; i++ {
		chatID := int64(2000 + i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One chat's own sequence stays ordered, exactly as the dispatcher
			// guarantees; the parallelism is across chats.
			svc.StartInviteFlow(ctx, msg(chatID, "/invite"))
			svc.HandleText(ctx, msg(chatID, fmt.Sprintf("user%d", chatID)))
			svc.StartGiftCodeFlow(ctx, msg(chatID, "/gift"))
			svc.StartRegisterFlow(ctx, msg(chatID, "/register"))
			svc.SendCabinet(ctx, chatID)
			svc.SendTariffs(ctx, chatID)
			svc.SendMenu(ctx, chatID)
			svc.HandleCallback(ctx, &tg.CallbackQuery{
				ID: "c", From: tg.User{ID: chatID},
				Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: chatID}},
				Data:    "menu:cabinet",
			})
		}()
	}
	wg.Wait()
}

// TestServiceMixesBotAndMiniAppTraffic covers the combination that already
// exists in production — the HTTP server serves the mini app while the poll
// loop handles updates — now with several bot chats in flight too.
func TestServiceMixesBotAndMiniAppTraffic(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{}}
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		chatID := int64(3000 + i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			svc.StartGiftCodeFlow(ctx, msg(chatID, "/gift"))
			svc.SendCabinet(ctx, chatID)
		}()
		go func() {
			defer wg.Done()
			if _, err := svc.CabinetData(ctx, chatID); err != nil {
				t.Errorf("CabinetData(%d): %v", chatID, err)
			}
		}()
	}
	wg.Wait()
}

// TestPerChatStateStaysIsolated: concurrent chats must not overwrite each
// other's conversation position, which is what the maps keyed by chat ID are
// for. Serialising per chat is only worth anything if the entries are distinct.
func TestPerChatStateStaysIsolated(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	const chats = 32
	// The invite flow is subscribers-only, so give every chat a linked profile.
	byTG := make(map[int64][]Subscriber, chats)
	for i := 0; i < chats; i++ {
		chatID := int64(4000 + i)
		byTG[chatID] = []Subscriber{{RemnawaveID: chatID, Username: fmt.Sprintf("user%d", chatID), TelegramID: chatID}}
	}
	svc.finder = &fakeFinder{byTG: byTG}

	var wg sync.WaitGroup
	for i := 0; i < chats; i++ {
		chatID := int64(4000 + i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.StartInviteFlow(ctx, msg(chatID, "/invite"))
		}()
	}
	wg.Wait()

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.invites) != chats {
		t.Fatalf("%d invite flows recorded, want one per chat (%d)", len(svc.invites), chats)
	}
}
