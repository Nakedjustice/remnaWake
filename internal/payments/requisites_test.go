package payments

import (
	"context"
	"strings"
	"testing"
)

func TestSetRequisitesTwoStepFlow(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	// Step 1: admin asks to set requisites -> bot prompts.
	if !svc.HandleAdminCommand(ctx, msg(1000, "/setrequisites")) {
		t.Fatal("/setrequisites should be handled")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "следующим сообщением") {
		t.Fatalf("expected prompt, got %+v", bot.sent)
	}

	// Step 2: admin sends the requisites text -> stored + confirmed.
	if !svc.HandleAdminCommand(ctx, msg(1000, "Карта 1234 5678")) {
		t.Fatal("requisites text should be captured")
	}
	if bot.sent[len(bot.sent)-1].Text != "Реквизиты сохранены." {
		t.Fatalf("expected save confirmation, got %+v", bot.sent)
	}

	// Persisted to DB.
	got, found, err := st.GetSetting(ctx, requisitesKey)
	if err != nil || !found || got != "Карта 1234 5678" {
		t.Fatalf("not persisted: got=%q found=%v err=%v", got, found, err)
	}
	// And cached in memory.
	if svc.requisites != "Карта 1234 5678" {
		t.Fatalf("in-memory cache not updated: %q", svc.requisites)
	}
}

func TestRequisitesNotCapturedWhenNotAwaiting(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	// A plain admin message that isn't a known command and no pending flow.
	if svc.HandleAdminCommand(context.Background(), msg(1000, "just some text")) {
		t.Fatal("plain text should not be handled when not awaiting requisites")
	}
}

func TestShowRequisites(t *testing.T) {
	svc, bot, _, _ := newTestService(t)
	ctx := context.Background()

	// Not set yet.
	if !svc.HandleAdminCommand(ctx, msg(1000, "/requisites")) {
		t.Fatal("/requisites should be handled")
	}
	if bot.sent[len(bot.sent)-1].Text != "Реквизиты не заданы." {
		t.Fatalf("expected not-set notice, got %+v", bot.sent)
	}

	// Set, then show.
	_ = svc.HandleAdminCommand(ctx, msg(1000, "/setrequisites"))
	_ = svc.HandleAdminCommand(ctx, msg(1000, "СБП +79001234567"))
	if !svc.HandleAdminCommand(ctx, msg(1000, "/requisites")) {
		t.Fatal("/requisites should be handled")
	}
	last := bot.sent[len(bot.sent)-1].Text
	if !strings.HasPrefix(last, "Реквизиты для оплаты:") || !strings.Contains(last, "СБП +79001234567") {
		t.Fatalf("unexpected requisites output: %q", last)
	}
}

func TestNewLoadsRequisitesFromStore(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, requisitesKey, "preset card"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A fresh service over the same store should pick up the persisted value.
	svc2 := New(st, svc.bot, svc.extender, svc.creator, svc.finder, svc.registrar, []int64{1000}, "₽", false, svc.logger)
	if svc2.requisites != "preset card" {
		t.Fatalf("requisites not loaded at New: %q", svc2.requisites)
	}
}

func TestPayShowsRequisitesToUser(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	svc.requisites = "Карта 0000"

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	var shown bool
	for _, m := range bot.sent {
		if m.ChatID == 777 && strings.Contains(m.Text, "Карта 0000") {
			shown = true
		}
	}
	if !shown {
		t.Fatalf("requisites not sent to user: %+v", bot.sent)
	}
}

func TestPayNoRequisitesUnchanged(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	// requisites empty -> no extra plain message to the user.

	if !svc.HandleCallback(ctx, cbq(777, "pay:42")) {
		t.Fatal("pay should be handled")
	}
	for _, m := range bot.sent {
		if m.ChatID == 777 {
			t.Fatalf("unexpected message to user when no requisites: %+v", m)
		}
	}
}
