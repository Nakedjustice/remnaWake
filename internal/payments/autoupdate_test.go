package payments

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotifyUpdateAvailableDMsAdmins(t *testing.T) {
	svc, bot, _, _ := newTestService(t) // admin == 1000
	svc.NotifyUpdateAvailable(context.Background(), "sha256:oldoldoldold111", "sha256:newnewnewnew222")

	if len(bot.sent) != 1 || bot.sent[0].ChatID != 1000 {
		t.Fatalf("expected one DM to admin 1000: %+v", bot.sent)
	}
	if !strings.Contains(bot.sent[0].Text, "oldoldoldol") || !strings.Contains(bot.sent[0].Text, "newnewnewne") {
		t.Fatalf("message should carry short digests: %q", bot.sent[0].Text)
	}
	found := keyboardData(bot.sent[0].Keyboard)
	if !found["upd:install"] || !found["upd:dismiss"] {
		t.Fatalf("expected install + dismiss buttons: %+v", found)
	}
}

func TestUpdateInstallTriggersWatchtower(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.updateDelay = 10 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	svc.SetUpdateTrigger(func(ctx context.Context) error {
		close(started)
		<-release
		return nil
	})

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	// Telegram is acknowledged and buttons are removed before the asynchronous
	// Watchtower request starts.
	if len(bot.answers) != 1 || len(bot.edits) != 1 {
		t.Fatalf("expected callback answer and button removal first: answers=%v edits=%+v", bot.answers, bot.edits)
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected buttons cleared: %+v", bot.edits)
	}
	select {
	case <-started:
		t.Fatal("trigger started before the callback handler returned")
	default:
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("trigger did not start asynchronously")
	}
	close(release)
}

func TestUpdateInstallRejectsNonAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	called := 0
	svc.SetUpdateTrigger(func(ctx context.Context) error { called++; return nil })

	if !svc.HandleCallback(context.Background(), cbq(2222, "upd:install")) {
		t.Fatal("callback should be handled (with a rejection)")
	}
	if called != 0 {
		t.Fatal("non-admin must not trigger an update")
	}
}

func TestUpdateInstallManualFallbackWhenNoTrigger(t *testing.T) {
	svc, bot, _, _ := newTestService(t) // no trigger wired

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	if len(bot.edits) != 1 || !strings.Contains(bot.edits[0].Text, "docker compose pull") {
		t.Fatalf("expected manual instructions: %+v", bot.edits)
	}
}

func TestUpdateInstallDryRunDoesNotTrigger(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.dryRun = true
	called := 0
	svc.SetUpdateTrigger(func(ctx context.Context) error { called++; return nil })

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	if called != 0 {
		t.Fatal("dry run must not trigger Watchtower")
	}
}

func TestUpdateInstallIgnoresRepeatedTapWhileRunning(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.updateDelay = 10 * time.Millisecond
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	svc.SetUpdateTrigger(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	})

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("first upd:install should be handled")
	}
	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("repeated upd:install should be handled")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("trigger did not start")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("trigger called %d times, want 1", got)
	}
	if len(bot.answers) != 2 || bot.answers[1] != "Обновление уже запущено." {
		t.Fatalf("expected already-running answer: %+v", bot.answers)
	}
	close(release)
}

func TestUpdateInstallReportsTriggerError(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.updateDelay = 10 * time.Millisecond
	bot.plainSent = make(chan sentMsg, 1)
	svc.SetUpdateTrigger(func(ctx context.Context) error { return errors.New("boom") })

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	var failure sentMsg
	select {
	case failure = <-bot.plainSent:
	case <-time.After(time.Second):
		t.Fatal("admin did not receive asynchronous trigger failure")
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("buttons should be cleared before triggering: %+v", bot.edits)
	}
	if failure.ChatID != 1000 || !strings.Contains(failure.Text, "docker compose pull") {
		t.Fatalf("unexpected trigger failure message: %+v", failure)
	}
}

func TestUpdateDismissClearsButtons(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:dismiss")) {
		t.Fatal("upd:dismiss should be handled")
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected buttons cleared: %+v", bot.edits)
	}
}

func TestParseUpdateIntervalSupportsMinutesHoursDays(t *testing.T) {
	tests := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"6h":  6 * time.Hour,
		"1d":  24 * time.Hour,
		"2d":  48 * time.Hour,
	}
	for raw, want := range tests {
		got, err := parseUpdateInterval(raw)
		if err != nil {
			t.Fatalf("parseUpdateInterval(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseUpdateInterval(%q) = %v, want %v", raw, got, want)
		}
	}
}
