package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
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
	called := 0
	svc.SetUpdateTrigger(func(ctx context.Context) error { called++; return nil })

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	if called != 1 {
		t.Fatalf("trigger called %d times, want 1", called)
	}
	// Buttons cleared after a successful trigger.
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("expected buttons cleared: %+v", bot.edits)
	}
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

func TestUpdateInstallReportsTriggerError(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	svc.SetUpdateTrigger(func(ctx context.Context) error { return errors.New("boom") })

	if !svc.HandleCallback(context.Background(), cbq(1000, "upd:install")) {
		t.Fatal("upd:install should be handled")
	}
	// No keyboard edit on failure; an error toast was answered instead.
	if len(bot.edits) != 0 {
		t.Fatalf("buttons should remain on failure: %+v", bot.edits)
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
