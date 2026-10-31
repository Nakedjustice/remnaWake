package main

import (
	"context"
	"sync"
	"testing"
	"time"

	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func msgUpdate(id, chatID int64, text string) tgbot.Update {
	return tgbot.Update{UpdateID: id, Message: &tgbot.Message{Chat: tgbot.Chat{ID: chatID}, Text: text}}
}

func TestUpdateChatIDCoversEveryUpdateShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    tgbot.Update
		want int64
	}{
		{"message", msgUpdate(1, 10, "hi"), 10},
		{"callback with message", tgbot.Update{CallbackQuery: &tgbot.CallbackQuery{
			From: tgbot.User{ID: 99}, Message: &tgbot.Message{Chat: tgbot.Chat{ID: 10}}}}, 10},
		{"callback without message", tgbot.Update{CallbackQuery: &tgbot.CallbackQuery{
			From: tgbot.User{ID: 10}}}, 10},
		{"pre-checkout", tgbot.Update{PreCheckoutQuery: &tgbot.PreCheckoutQuery{From: tgbot.User{ID: 10}}}, 10},
		{"empty", tgbot.Update{UpdateID: 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := updateChatID(tc.u); got != tc.want {
				t.Fatalf("updateChatID = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDispatchKeepsPerChatOrder is the invariant that makes concurrency safe
// here: one chat's updates drive a single conversation state machine, so they
// must still be handled one at a time, in arrival order.
func TestDispatchKeepsPerChatOrder(t *testing.T) {
	updates := []tgbot.Update{
		msgUpdate(1, 10, "first"),
		msgUpdate(2, 20, "other chat"),
		msgUpdate(3, 10, "second"),
		msgUpdate(4, 10, "third"),
	}
	var mu sync.Mutex
	seen := map[int64][]string{}
	dispatchUpdates(context.Background(), updates, func(_ context.Context, u tgbot.Update) {
		mu.Lock()
		seen[u.Message.Chat.ID] = append(seen[u.Message.Chat.ID], u.Message.Text)
		mu.Unlock()
	})

	want := []string{"first", "second", "third"}
	got := seen[10]
	if len(got) != len(want) {
		t.Fatalf("chat 10 saw %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chat 10 saw %v, want %v", got, want)
		}
	}
	if len(seen[20]) != 1 {
		t.Fatalf("chat 20 saw %v, want one update", seen[20])
	}
}

// TestDispatchRunsDifferentChatsInParallel: each handler blocks until the other
// chat has arrived, so this can only finish if they run at the same time. Before
// the change, one user's slow panel call delayed everyone in the batch.
func TestDispatchRunsDifferentChatsInParallel(t *testing.T) {
	arrived := make(chan int64, 2)
	proceed := make(chan struct{})
	updates := []tgbot.Update{msgUpdate(1, 10, "a"), msgUpdate(2, 20, "b")}

	done := make(chan struct{})
	go func() {
		defer close(done)
		dispatchUpdates(context.Background(), updates, func(_ context.Context, u tgbot.Update) {
			arrived <- u.Message.Chat.ID
			<-proceed
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("handlers did not run in parallel")
		}
	}
	close(proceed)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not return")
	}
}

// TestDispatchWaitsForTheWholeBatch keeps Telegram's at-least-once delivery
// intact: the next getUpdates confirms this batch, so nothing may still be in
// flight when dispatch returns.
func TestDispatchWaitsForTheWholeBatch(t *testing.T) {
	var handled int64
	var mu sync.Mutex
	updates := make([]tgbot.Update, 0, 50)
	for i := 0; i < 50; i++ {
		updates = append(updates, msgUpdate(int64(i), int64(i), "x"))
	}
	dispatchUpdates(context.Background(), updates, func(_ context.Context, _ tgbot.Update) {
		time.Sleep(time.Millisecond)
		mu.Lock()
		handled++
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if handled != 50 {
		t.Fatalf("handled %d updates, want all 50 before returning", handled)
	}
}

// TestDispatchBoundsConcurrency: a batch is up to 100 updates and each handler
// can hit the panel, so the fan-out must be capped.
func TestDispatchBoundsConcurrency(t *testing.T) {
	var mu sync.Mutex
	var running, peak int
	updates := make([]tgbot.Update, 0, 60)
	for i := 0; i < 60; i++ {
		updates = append(updates, msgUpdate(int64(i), int64(i), "x"))
	}
	dispatchUpdates(context.Background(), updates, func(_ context.Context, _ tgbot.Update) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if peak > updateConcurrency {
		t.Fatalf("peak concurrency %d, want at most %d", peak, updateConcurrency)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency %d, want real parallelism", peak)
	}
}

// TestDispatchStopsOnContextCancellation avoids working through a whole batch
// while the process is shutting down.
func TestDispatchStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	updates := []tgbot.Update{msgUpdate(1, 10, "a"), msgUpdate(2, 20, "b")}
	called := 0
	dispatchUpdates(ctx, updates, func(_ context.Context, _ tgbot.Update) { called++ })
	if called != 0 {
		t.Fatalf("handled %d updates after cancellation, want 0", called)
	}
}
