package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestGuardNewTelegramUserBlocksSuspiciousNameAndEscapesAlert(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	svc.SetBotUsername("remna_secure_bot")
	user := &tg.User{
		ID:        42,
		FirstName: `<a href="https://scam.example">Support</a>`,
		Username:  "client",
	}

	if !svc.GuardNewTelegramUser(context.Background(), user) {
		t.Fatal("suspicious registration must be blocked")
	}
	if blocked, err := st.RegistrationBlocked(context.Background(), user.ID); err != nil || !blocked {
		t.Fatalf("stored block = %v, err=%v", blocked, err)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("sent messages = %d, want user and admin alerts", len(bot.sent))
	}
	if got := bot.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData; got != "menu:support" {
		t.Fatalf("support button callback = %q", got)
	}
	adminAlert := bot.sent[1]
	if strings.Contains(adminAlert.Text, `<a href=`) || !strings.Contains(adminAlert.Text, "&lt;a href=") {
		t.Fatalf("admin alert did not escape user input: %q", adminAlert.Text)
	}
	if got := adminAlert.Keyboard.InlineKeyboard[0][0].CallbackData; got != "guard:unblock:42" {
		t.Fatalf("unblock callback = %q", got)
	}
}

func TestRegistrationGuardUnblockDoesNotReblock(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	user := &tg.User{ID: 42, Username: "support_account"}
	if !svc.GuardNewTelegramUser(ctx, user) {
		t.Fatal("suspicious registration must be blocked")
	}
	if !svc.HandleCallback(ctx, cbq(1000, "guard:unblock:42")) {
		t.Fatal("unblock callback must be handled")
	}
	if blocked, err := st.RegistrationBlocked(ctx, 42); err != nil || blocked {
		t.Fatalf("stored block after admin action = %v, err=%v", blocked, err)
	}
	if svc.GuardNewTelegramUser(ctx, user) {
		t.Fatal("an admin-unblocked user must not be blocked again on /start")
	}
	if len(bot.edits) != 1 || bot.edits[0].Keyboard != nil {
		t.Fatalf("unblock should clear the admin button: %+v", bot.edits)
	}
}

func TestGuardNewTelegramUserAllowsTrustedAdminAndSafeUser(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	if svc.GuardNewTelegramUser(ctx, &tg.User{ID: 1000, Username: "support_admin"}) {
		t.Fatal("admin must not be blocked")
	}
	if svc.GuardNewTelegramUser(ctx, &tg.User{ID: 42, FirstName: "Alice", Username: "alice"}) {
		t.Fatal("safe registration must not be blocked")
	}
	if blocked, err := st.RegistrationBlocked(ctx, 42); err != nil || blocked {
		t.Fatalf("safe user stored state = %v, err=%v", blocked, err)
	}
	if len(bot.sent) != 0 {
		t.Fatalf("safe registrations should not notify: %+v", bot.sent)
	}
}

func TestSuspiciousRegistrationPatternIncludesBotUsername(t *testing.T) {
	if got := suspiciousRegistrationPattern(tg.User{Username: "remna_secure_bot_news"}, "remna_secure_bot", nil); got != "remna_secure_bot" {
		t.Fatalf("bot-name pattern = %q", got)
	}
	if got := suspiciousRegistrationPattern(tg.User{FirstName: "Alice", Username: "alice"}, "remna_secure_bot", nil); got != "" {
		t.Fatalf("safe user pattern = %q", got)
	}
}

func TestRegistrationGuardCustomPatternAdminFlow(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	svc.SendAdminMenu(ctx, 1000)
	if !keyboardData(bot.sent[0].Keyboard)["adm:guard"] {
		t.Fatal("admin menu must expose registration guard settings")
	}
	bot.sent = nil

	if !svc.HandleCallback(ctx, cbq(1000, "adm:guard")) {
		t.Fatal("guard settings callback must be handled")
	}
	if len(bot.sent) != 1 || !keyboardData(bot.sent[0].Keyboard)["adm:guard:add"] {
		t.Fatalf("guard settings screen = %+v", bot.sent)
	}
	bot.sent = nil

	if !svc.HandleCallback(ctx, cbq(1000, "adm:guard:add")) {
		t.Fatal("add-pattern callback must be handled")
	}
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputRegistrationGuardPattern {
		t.Fatalf("input step = %v", step)
	}
	bot.sent = nil

	if !svc.HandleAdminCommand(ctx, msg(1000, "Brand Helper")) {
		t.Fatal("custom pattern input must be consumed")
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "brand helper") {
		t.Fatalf("save reply = %+v", bot.sent)
	}
	if got, found, err := st.GetSetting(ctx, registrationGuardPatternsKey); err != nil || !found || got != `["brand helper"]` {
		t.Fatalf("stored patterns = %q found=%v err=%v", got, found, err)
	}
	restartExt := &fakeExtender{}
	restarted := New(st, &fakeBot{}, restartExt, &fakeCreator{}, &fakeUpdater{ext: restartExt}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(),
		[]int64{1000}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := restarted.customRegistrationGuardPatterns(); len(got) != 1 || got[0] != "brand helper" {
		t.Fatalf("patterns after restart = %v", got)
	}
	if !svc.GuardNewTelegramUser(ctx, &tg.User{ID: 42, FirstName: "Brand Helper Team"}) {
		t.Fatal("custom pattern must block a new registration immediately")
	}

	bot.sent = nil
	if !svc.HandleCallback(ctx, cbq(1000, "adm:guard:del:0")) {
		t.Fatal("delete-pattern callback must be handled")
	}
	if got := svc.customRegistrationGuardPatterns(); len(got) != 0 {
		t.Fatalf("patterns after delete = %v", got)
	}
	if svc.GuardNewTelegramUser(ctx, &tg.User{ID: 43, FirstName: "Brand Helper Team"}) {
		t.Fatal("deleted custom pattern must stop matching")
	}
}

func TestRegistrationGuardCustomPatternValidation(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	for _, pattern := range []string{"x", "two\nlines"} {
		if _, err := svc.addRegistrationGuardPattern(ctx, 1000, pattern); !errors.Is(err, errRegistrationGuardPatternInvalid) {
			t.Fatalf("pattern %q error = %v", pattern, err)
		}
	}
	if added, err := svc.addRegistrationGuardPattern(ctx, 1000, "support"); err != nil || added {
		t.Fatalf("built-in duplicate = added:%v err:%v", added, err)
	}
}

func TestRegistrationGuardMiniAppAdminMethodsAndPanelQueue(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.AdminAddRegistrationGuardPattern(ctx, 42, "brand helper"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin add error = %v", err)
	}
	if err := svc.AdminAddRegistrationGuardPattern(ctx, adminTG, "Brand Helper"); err != nil {
		t.Fatalf("admin add pattern: %v", err)
	}
	if err := svc.AdminAddRegistrationGuardPattern(ctx, adminTG, "brand helper"); !errors.Is(err, ErrRegistrationGuardPatternExists) {
		t.Fatalf("duplicate add error = %v", err)
	}

	blockedUser := &tg.User{ID: 77, FirstName: "Brand Helper", Username: "client"}
	if !svc.CheckTelegramRegistration(ctx, blockedUser) {
		t.Fatal("custom pattern must block the Mini App identity")
	}
	panel, err := svc.AdminPanelData(ctx, adminTG)
	if err != nil {
		t.Fatalf("admin panel: %v", err)
	}
	if len(panel.RegistrationGuardPatterns) != 1 || panel.RegistrationGuardPatterns[0] != "brand helper" {
		t.Fatalf("panel patterns = %v", panel.RegistrationGuardPatterns)
	}
	if len(panel.BlockedRegistrations) != 1 || panel.BlockedRegistrations[0].TelegramID != blockedUser.ID ||
		panel.BlockedRegistrations[0].MatchedPattern != "brand helper" || panel.BlockedRegistrations[0].CreatedAt == "" {
		t.Fatalf("panel blocks = %+v", panel.BlockedRegistrations)
	}

	if err := svc.AdminUnblockRegistration(ctx, adminTG, blockedUser.ID); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if err := svc.AdminUnblockRegistration(ctx, adminTG, blockedUser.ID); !errors.Is(err, ErrRegistrationGuardNotFound) {
		t.Fatalf("stale unblock error = %v", err)
	}
	if err := svc.AdminDeleteRegistrationGuardPattern(ctx, adminTG, "BRAND HELPER"); err != nil {
		t.Fatalf("delete pattern: %v", err)
	}
	if err := svc.AdminDeleteRegistrationGuardPattern(ctx, adminTG, "brand helper"); !errors.Is(err, ErrRegistrationGuardPatternNotFound) {
		t.Fatalf("stale delete error = %v", err)
	}
}
