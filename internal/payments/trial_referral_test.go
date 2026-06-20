package payments

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// trialFixture builds a service with explicit fakes the test keeps references
// to, so it can assert on creation/binding.
func trialFixture(t *testing.T, finder *fakeFinder) (*Service, *store.Store, *fakeBot, *fakeCreator, *fakeRegistrar) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	creator := &fakeCreator{}
	registrar := &fakeRegistrar{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, &fakeExtender{}, creator, &fakeUpdater{}, finder, registrar, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	return svc, st, bot, creator, registrar
}

func TestTrialDisabledDoesNotConsume(t *testing.T) {
	svc, _, _, _, _ := trialFixture(t, &fakeFinder{})
	if svc.StartTrialFlow(context.Background(), msg(555, "/trial")) {
		t.Fatal("disabled trial must not consume the command")
	}
	if svc.getTrial(555) != nil {
		t.Fatal("no trial state should be created when disabled")
	}
}

func TestTrialCreatesProfileForNewUser(t *testing.T) {
	svc, st, _, creator, registrar := trialFixture(t, &fakeFinder{})
	svc.InitTrial(true, 7)
	ctx := context.Background()

	if !svc.StartTrialFlow(ctx, msg(555, "/trial")) {
		t.Fatal("trial flow should start for a new user")
	}
	if svc.getTrial(555) == nil {
		t.Fatal("trial state should be set after starting")
	}
	if !svc.HandleText(ctx, msg(555, "trialuser")) {
		t.Fatal("username input should be handled")
	}
	if len(creator.created) != 1 || creator.created[0] != "trialuser" {
		t.Fatalf("trial profile not created: %+v", creator.created)
	}
	if registrar.calls != 1 || registrar.telegramID != 555 {
		t.Fatalf("telegram id not bound: calls=%d tg=%d", registrar.calls, registrar.telegramID)
	}
	// The one-time claim is recorded, so a fresh claim attempt now fails.
	if ok, _ := st.ClaimTrial(ctx, 555, "x", svc.now()); ok {
		t.Fatal("trial should already be claimed for this Telegram ID")
	}
	if svc.getTrial(555) != nil {
		t.Fatal("trial state should be cleared after activation")
	}
}

func TestTrialRefusedSecondTime(t *testing.T) {
	svc, st, bot, creator, _ := trialFixture(t, &fakeFinder{})
	svc.InitTrial(true, 7)
	ctx := context.Background()

	// Pre-claim simulates a trial already used by this Telegram ID.
	if ok, _ := st.ClaimTrial(ctx, 555, "old", svc.now()); !ok {
		t.Fatal("seed claim should succeed")
	}

	svc.StartTrialFlow(ctx, msg(555, "/trial"))
	if !svc.HandleText(ctx, msg(555, "trialuser")) {
		t.Fatal("username input should be handled")
	}
	if len(creator.created) != 0 {
		t.Fatalf("must not create a profile when trial already used: %+v", creator.created)
	}
	var refused bool
	for _, m := range bot.sent {
		if m.ChatID == 555 && strings.Contains(m.Text, "уже использовали") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("expected 'already used' message: %+v", bot.sent)
	}
}

func TestTrialRefusedForExistingUser(t *testing.T) {
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		555: {{UUID: "u-1", Username: "sub", TelegramID: 555}},
	}}
	svc, _, bot, creator, _ := trialFixture(t, finder)
	svc.InitTrial(true, 7)
	ctx := context.Background()

	if !svc.StartTrialFlow(ctx, msg(555, "/trial")) {
		t.Fatal("flow should reply (and refuse)")
	}
	if svc.getTrial(555) != nil {
		t.Fatal("trial must not start for a user who already has a profile")
	}
	if len(creator.created) != 0 {
		t.Fatal("no profile should be created")
	}
	var refused bool
	for _, m := range bot.sent {
		if m.ChatID == 555 && strings.Contains(m.Text, "уже есть профиль") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("expected 'already have a profile' message: %+v", bot.sent)
	}
}

// referralFixture builds a service where inviter 300 already has a profile.
func referralFixture(t *testing.T, inviterExpiry time.Time) (*Service, *fakeBot, *fakeExtender) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	ext := &fakeExtender{}
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{UUID: "inv-uuid", Username: "payer", TelegramID: 300, ExpireAt: inviterExpiry}},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, ext, &fakeCreator{}, &fakeUpdater{}, finder, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	svc.now = func() time.Time { return time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) }
	return svc, bot, ext
}

func approveSeededInvite(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	svc.setInvite(300, &inviteState{inviterName: "payer", inviterTGID: 300, newUsername: "newbie", price: 0, createdAt: svc.now()})
	submit := &tg.CallbackQuery{ID: "c1", From: tg.User{ID: 300}, Message: &tg.Message{MessageID: 9, Chat: tg.Chat{ID: 300}}, Data: "inv_submit"}
	if !svc.HandleCallback(ctx, submit) {
		t.Fatal("inv_submit should be handled")
	}
	approve := &tg.CallbackQuery{ID: "c2", From: tg.User{ID: 1000}, Message: &tg.Message{MessageID: 10, Chat: tg.Chat{ID: 1000}}, Data: "inv_ok:1"}
	if !svc.HandleCallback(ctx, approve) {
		t.Fatal("inv_ok should be handled")
	}
}

func TestReferralAwardsInviterAndInvitee(t *testing.T) {
	inviterExpiry := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	svc, bot, ext := referralFixture(t, inviterExpiry)
	svc.InitReferral(true, 30, 5) // inviter +30 days, invitee +5 days

	approveSeededInvite(t, svc)

	// Inviter bonus: their own subscription is extended by 30 days.
	if ext.calls != 1 || ext.uuid != "inv-uuid" {
		t.Fatalf("inviter bonus not applied: calls=%d uuid=%s", ext.calls, ext.uuid)
	}
	wantInviter := inviterExpiry.AddDate(0, 0, 30)
	if !ext.expire.Equal(wantInviter) {
		t.Fatalf("inviter new expiry = %s, want %s", ext.expire, wantInviter)
	}

	// Invitee bonus: the new profile gets 1 month + 5 bonus days, reflected in
	// the approval message's expiry date.
	wantInvitee := svc.now().AddDate(0, 1, 0).AddDate(0, 0, 5)
	var inviteeDateShown, bonusNotified bool
	for _, m := range bot.sent {
		if m.ChatID != 300 {
			continue
		}
		if strings.Contains(m.Text, wantInvitee.Format("02.01.2006")) {
			inviteeDateShown = true
		}
		if strings.Contains(m.Text, "начислено") {
			bonusNotified = true
		}
	}
	if !inviteeDateShown {
		t.Fatalf("invitee bonus not reflected in expiry date %s: %+v", wantInvitee.Format("02.01.2006"), bot.sent)
	}
	if !bonusNotified {
		t.Fatalf("inviter not told about their bonus: %+v", bot.sent)
	}
}

func TestReferralDisabledNoBonus(t *testing.T) {
	inviterExpiry := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	svc, bot, ext := referralFixture(t, inviterExpiry)
	// Referral not enabled.

	approveSeededInvite(t, svc)

	if ext.calls != 0 {
		t.Fatalf("no inviter bonus expected when referral disabled: calls=%d", ext.calls)
	}
	// Invitee expiry is exactly 1 month, with no bonus days added.
	wantPlain := svc.now().AddDate(0, 1, 0)
	for _, m := range bot.sent {
		if m.ChatID == 300 && strings.Contains(m.Text, "начислено") {
			t.Fatalf("inviter must not be told about a bonus: %q", m.Text)
		}
	}
	var plainShown bool
	for _, m := range bot.sent {
		if m.ChatID == 300 && strings.Contains(m.Text, wantPlain.Format("02.01.2006")) {
			plainShown = true
		}
	}
	if !plainShown {
		t.Fatalf("expected plain 1-month expiry %s: %+v", wantPlain.Format("02.01.2006"), bot.sent)
	}
}
