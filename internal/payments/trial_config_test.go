package payments

import (
	"context"
	"errors"
	"testing"
)

func TestTrialCreationUsesShapingSnapshot(t *testing.T) {
	svc, _, _, creator, _ := trialFixture(t, &fakeFinder{})
	svc.InitTrialConfig(TrialConfig{
		Enabled: true, Days: 7, TrafficLimitGB: 10, HwidDeviceLimit: 1,
		SquadUUID: "trial-squad",
	})
	svc.squads = &fakeSquadLister{squads: []InternalSquad{{UUID: "trial-squad", Name: "Trial"}}}

	if _, _, err := svc.claimTrial(context.Background(), 555, "trialuser"); err != nil {
		t.Fatalf("claim trial: %v", err)
	}
	if len(creator.specs) != 1 {
		t.Fatalf("create specs = %d, want 1", len(creator.specs))
	}
	spec := creator.specs[0]
	if spec.TrafficLimitBytes != 10*bytesPerGB || spec.HwidDeviceLimit != 1 || spec.TrafficLimitStrategy != "NO_RESET" {
		t.Fatalf("unexpected trial shaping: %+v", spec)
	}
	if len(spec.SquadUUIDs) != 1 || spec.SquadUUIDs[0] != "trial-squad" {
		t.Fatalf("trial squads = %v", spec.SquadUUIDs)
	}
}

func TestTrialMissingConfiguredSquadDoesNotConsumeClaim(t *testing.T) {
	svc, st, _, creator, _ := trialFixture(t, &fakeFinder{})
	svc.InitTrialConfig(TrialConfig{Enabled: true, Days: 3, TrafficLimitGB: 10, HwidDeviceLimit: 1, SquadUUID: "gone"})
	svc.squads = &fakeSquadLister{squads: []InternalSquad{{UUID: "other", Name: "Other"}}}

	if _, _, err := svc.claimTrial(context.Background(), 555, "trialuser"); !errors.Is(err, ErrPanelCreateFailed) {
		t.Fatalf("claim err = %v, want ErrPanelCreateFailed", err)
	}
	if len(creator.specs) != 0 {
		t.Fatal("creator called for a stale trial squad")
	}
	if ok, err := st.ClaimTrial(context.Background(), 555, "retry", svc.now()); err != nil || !ok {
		t.Fatalf("claim was consumed before squad resolution: ok=%v err=%v", ok, err)
	}
}

func TestAdminTrialConfigValidationAndPersistence(t *testing.T) {
	svc, _, _, st := newTestService(t)
	svc.squads = &fakeSquadLister{squads: []InternalSquad{{UUID: "trial-squad", Name: "Trial"}}}
	cfg := TrialConfig{Enabled: true, Days: 5, TrafficLimitGB: 25, HwidDeviceLimit: 2, SquadUUID: "trial-squad"}
	if err := svc.AdminSetTrial(context.Background(), 1000, cfg); err != nil {
		t.Fatalf("set trial: %v", err)
	}
	if got := svc.trialConfig(); got != cfg {
		t.Fatalf("trial config = %+v, want %+v", got, cfg)
	}
	for key, want := range map[string]string{
		trialEnabledKey: "1", trialDaysKey: "5", trialTrafficLimitGBKey: "25",
		trialHwidLimitKey: "2", trialSquadUUIDKey: "trial-squad",
	} {
		got, found, err := st.GetSetting(context.Background(), key)
		if err != nil || !found || got != want {
			t.Fatalf("setting %s = %q found=%v err=%v, want %q", key, got, found, err, want)
		}
	}
	bad := cfg
	bad.SquadUUID = "missing"
	if err := svc.AdminSetTrial(context.Background(), 1000, bad); !errors.Is(err, ErrBadInput) {
		t.Fatalf("missing squad err = %v, want ErrBadInput", err)
	}
	bad = cfg
	bad.TrafficLimitGB = -1
	if err := svc.AdminSetTrial(context.Background(), 1000, bad); !errors.Is(err, ErrBadInput) {
		t.Fatalf("negative traffic err = %v, want ErrBadInput", err)
	}
}

func TestCabinetIncludesTrialTerms(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.InitTrialConfig(TrialConfig{Enabled: true, Days: 4, TrafficLimitGB: 0, HwidDeviceLimit: 0})
	data, err := svc.CabinetData(context.Background(), 555)
	if err != nil {
		t.Fatalf("cabinet: %v", err)
	}
	if !data.TrialAvailable || data.TrialTerms == nil || data.TrialTerms.Days != 4 || data.TrialTerms.TrafficLimitGB != 0 || data.TrialTerms.HwidDeviceLimit != 0 {
		t.Fatalf("trial terms = %+v, available=%v", data.TrialTerms, data.TrialAvailable)
	}
}
