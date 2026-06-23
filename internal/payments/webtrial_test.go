package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// webTrialFixture builds a service with an admin and explicit creator/registrar
// fakes for the mini app trial-claim tests.
func webTrialFixture(t *testing.T, finder *fakeFinder) (*Service, *store.Store, *fakeCreator, *fakeRegistrar) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	creator := &fakeCreator{}
	registrar := &fakeRegistrar{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, &fakeBot{}, &fakeExtender{}, creator, &fakeUpdater{}, finder, registrar, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	return svc, st, creator, registrar
}

func TestWebClaimTrialSuccess(t *testing.T) {
	svc, _, creator, registrar := webTrialFixture(t, &fakeFinder{})
	svc.InitTrial(true, 7)
	res, err := svc.ClaimTrial(context.Background(), 555, "newbie")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if res == nil || res.Username != "newbie" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(creator.created) != 1 || creator.created[0] != "newbie" {
		t.Fatalf("profile not created: %+v", creator.created)
	}
	if registrar.telegramID != 555 {
		t.Fatalf("telegram not bound: %d", registrar.telegramID)
	}
}

func TestWebClaimTrialSentinels(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled", func(t *testing.T) {
		svc, _, _, _ := webTrialFixture(t, &fakeFinder{})
		if _, err := svc.ClaimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialDisabled) {
			t.Fatalf("err = %v, want ErrTrialDisabled", err)
		}
	})

	t.Run("already used", func(t *testing.T) {
		svc, st, _, _ := webTrialFixture(t, &fakeFinder{})
		svc.InitTrial(true, 7)
		_, _ = st.ClaimTrial(ctx, 555, "old", svc.now())
		if _, err := svc.ClaimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialAlreadyUsed) {
			t.Fatalf("err = %v, want ErrTrialAlreadyUsed", err)
		}
	})

	t.Run("not eligible", func(t *testing.T) {
		finder := &fakeFinder{byTG: map[int64][]Subscriber{555: {{UUID: "u", Username: "sub", TelegramID: 555}}}}
		svc, _, _, _ := webTrialFixture(t, finder)
		svc.InitTrial(true, 7)
		if _, err := svc.ClaimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialNotEligible) {
			t.Fatalf("err = %v, want ErrTrialNotEligible", err)
		}
	})

	t.Run("username taken", func(t *testing.T) {
		finder := &fakeFinder{byName: map[string]*Subscriber{"taken": {Username: "taken"}}}
		svc, _, _, _ := webTrialFixture(t, finder)
		svc.InitTrial(true, 7)
		if _, err := svc.ClaimTrial(ctx, 555, "taken"); !errors.Is(err, ErrUsernameTaken) {
			t.Fatalf("err = %v, want ErrUsernameTaken", err)
		}
	})

	t.Run("invalid username", func(t *testing.T) {
		svc, _, _, _ := webTrialFixture(t, &fakeFinder{})
		svc.InitTrial(true, 7)
		if _, err := svc.ClaimTrial(ctx, 555, "ab"); !errors.Is(err, ErrBadInput) {
			t.Fatalf("err = %v, want ErrBadInput", err)
		}
	})
}

func TestWebSetNotificationPref(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	if err := svc.SetNotificationPref(ctx, 777, store.NotificationExpiry, true); err != nil {
		t.Fatalf("set pref: %v", err)
	}
	if em, _, _ := st.GetNotificationPrefs(ctx, 777); !em {
		t.Fatal("expiry should be muted")
	}
	if err := svc.SetNotificationPref(ctx, 777, "bogus", true); !errors.Is(err, ErrBadInput) {
		t.Fatalf("unknown kind err = %v, want ErrBadInput", err)
	}
}

func TestCabinetDataNotificationsAndTrial(t *testing.T) {
	svc, _, _, _ := newTestService(t) // empty finder: caller unlinked
	svc.InitTrial(true, 7)
	data, err := svc.CabinetData(context.Background(), 555)
	if err != nil {
		t.Fatalf("cabinet data: %v", err)
	}
	if data.Notifications == nil {
		t.Fatal("notifications block should always be present")
	}
	if !data.TrialAvailable {
		t.Fatal("trial should be available for an unlinked user when enabled")
	}
}

func TestAdminSetTrialPersists(t *testing.T) {
	svc, _, _, _ := newTestService(t) // admin 1000
	ctx := context.Background()
	if err := svc.AdminSetTrial(ctx, 1000, TrialConfig{Enabled: true, Days: 5, TrafficLimitGB: 10, HwidDeviceLimit: 1}); err != nil {
		t.Fatalf("set trial: %v", err)
	}
	if cfg := svc.trialConfig(); !cfg.Enabled || cfg.Days != 5 {
		t.Fatalf("trial config = %+v, want enabled with 5 days", cfg)
	}
	if err := svc.AdminSetTrial(ctx, 2222, TrialConfig{Enabled: true, Days: 5, TrafficLimitGB: 10, HwidDeviceLimit: 1}); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin err = %v, want ErrNotAdmin", err)
	}
}

func TestAdminSetReferralPersists(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.AdminSetReferral(ctx, 1000, true, 30, 10); err != nil {
		t.Fatalf("set referral: %v", err)
	}
	if en, inv, ee := svc.referralConfig(); !en || inv != 30 || ee != 10 {
		t.Fatalf("referral config = (%v,%d,%d), want (true,30,10)", en, inv, ee)
	}
	if err := svc.AdminSetReferral(ctx, 2222, true, 1, 0); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin err = %v, want ErrNotAdmin", err)
	}
}

func TestAdminSetReferralRejectsAllZeroEnable(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	// A fresh service has zero bonuses; enabling without setting any is rejected.
	if err := svc.AdminSetReferral(context.Background(), 1000, true, 0, 0); !errors.Is(err, ErrBadInput) {
		t.Fatalf("all-zero enable err = %v, want ErrBadInput", err)
	}
}

func TestInitTrialPersistedOverridesEnv(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	// Persist values that differ from the env defaults passed to InitTrial.
	_ = st.UpsertSetting(ctx, trialEnabledKey, "1")
	_ = st.UpsertSetting(ctx, trialDaysKey, "14")
	svc.InitTrial(false, 3) // env: disabled, 3 days
	if cfg := svc.trialConfig(); !cfg.Enabled || cfg.Days != 14 {
		t.Fatalf("persisted setting should win: got %+v, want enabled with 14 days", cfg)
	}
}
