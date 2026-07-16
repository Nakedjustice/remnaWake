package payments

import (
	"context"
	"errors"
	"testing"
)

func enableTrialWithApproval(svc *Service) {
	svc.InitTrialConfig(TrialConfig{Enabled: true, Days: 7, TrafficLimitGB: 10, HwidDeviceLimit: 1, RequireApproval: true})
}

func TestClaimTrialRequiresApprovalForNewUserWithoutReferral(t *testing.T) {
	svc, st, creator, _ := webTrialFixture(t, &fakeFinder{})
	enableTrialWithApproval(svc)
	ctx := context.Background()

	if _, _, err := svc.claimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialPendingApproval) {
		t.Fatalf("err = %v, want ErrTrialPendingApproval", err)
	}
	if len(creator.created) != 0 {
		t.Fatalf("no profile should be created yet, got %+v", creator.created)
	}
	pending, err := st.GetPendingTrialRequestByTelegramID(ctx, 555)
	if err != nil || pending == nil || pending.Username != "newbie" {
		t.Fatalf("pending request = %+v err=%v", pending, err)
	}
	// The one-time claim is held, so a duplicate submit is reported as pending.
	if _, _, err := svc.claimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialRequestPending) {
		t.Fatalf("duplicate submit err = %v, want ErrTrialRequestPending", err)
	}
}

func TestClaimTrialReferredUserSkipsApproval(t *testing.T) {
	svc, st, creator, registrar := webTrialFixture(t, &fakeFinder{})
	enableTrialWithApproval(svc)
	ctx := context.Background()
	// A vouched-for user: someone referred them via a referral link.
	if _, err := st.CreateReferral(ctx, 555, 999, svc.now()); err != nil {
		t.Fatalf("seed referral: %v", err)
	}

	created, _, err := svc.claimTrial(ctx, 555, "newbie")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if created == nil || len(creator.created) != 1 {
		t.Fatalf("referred user should get an instant trial, created=%+v", creator.created)
	}
	if registrar.telegramID != 555 {
		t.Fatalf("telegram not bound: %d", registrar.telegramID)
	}
	if pending, _ := st.GetPendingTrialRequestByTelegramID(ctx, 555); pending != nil {
		t.Fatalf("referred user should not create a pending request: %+v", pending)
	}
}

func TestApprovalOffKeepsInstantTrial(t *testing.T) {
	svc, st, creator, _ := webTrialFixture(t, &fakeFinder{})
	svc.InitTrialConfig(TrialConfig{Enabled: true, Days: 7, TrafficLimitGB: 10, HwidDeviceLimit: 1, RequireApproval: false})
	ctx := context.Background()

	created, _, err := svc.claimTrial(ctx, 555, "newbie")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if created == nil || len(creator.created) != 1 {
		t.Fatalf("instant trial expected, created=%+v", creator.created)
	}
	if pending, _ := st.GetPendingTrialRequestByTelegramID(ctx, 555); pending != nil {
		t.Fatalf("no pending request expected: %+v", pending)
	}
}

func TestApproveTrialRequestCreatesProfile(t *testing.T) {
	svc, st, creator, registrar := webTrialFixture(t, &fakeFinder{})
	enableTrialWithApproval(svc)
	ctx := context.Background()

	if _, _, err := svc.claimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialPendingApproval) {
		t.Fatalf("submit: %v", err)
	}
	pending, _ := st.GetPendingTrialRequestByTelegramID(ctx, 555)

	req, created, _, err := svc.approveTrialRequest(ctx, pending.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if created == nil || len(creator.created) != 1 || creator.created[0] != "newbie" {
		t.Fatalf("profile not created on approve: %+v", creator.created)
	}
	if registrar.telegramID != 555 {
		t.Fatalf("telegram not bound: %d", registrar.telegramID)
	}
	if req == nil || req.TelegramID != 555 {
		t.Fatalf("req = %+v", req)
	}
	got, _ := st.GetTrialRequest(ctx, pending.ID)
	if got.Status != "approved" {
		t.Fatalf("status = %s, want approved", got.Status)
	}
	// Approving again is rejected as already resolved.
	if _, _, _, err := svc.approveTrialRequest(ctx, pending.ID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("re-approve err = %v, want ErrRequestResolved", err)
	}
}

func TestRejectTrialRequestIsFinal(t *testing.T) {
	svc, st, creator, _ := webTrialFixture(t, &fakeFinder{})
	enableTrialWithApproval(svc)
	ctx := context.Background()

	if _, _, err := svc.claimTrial(ctx, 555, "newbie"); !errors.Is(err, ErrTrialPendingApproval) {
		t.Fatalf("submit: %v", err)
	}
	pending, _ := st.GetPendingTrialRequestByTelegramID(ctx, 555)

	if _, err := svc.rejectTrialRequest(ctx, pending.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, _ := st.GetTrialRequest(ctx, pending.ID)
	if got.Status != "rejected" {
		t.Fatalf("status = %s, want rejected", got.Status)
	}
	// Rejection is final: the claim is kept, so a re-request is "already used".
	if _, _, err := svc.claimTrial(ctx, 555, "second"); !errors.Is(err, ErrTrialAlreadyUsed) {
		t.Fatalf("re-request after reject err = %v, want ErrTrialAlreadyUsed", err)
	}
	if len(creator.created) != 0 {
		t.Fatalf("no profile should ever be created, got %+v", creator.created)
	}
}

func TestApproveLegacyInvalidTrialRejectsAndReleasesClaim(t *testing.T) {
	svc, st, creator, _ := webTrialFixture(t, &fakeFinder{})
	enableTrialWithApproval(svc)
	ctx := context.Background()

	if ok, err := st.ClaimTrial(ctx, 555, "профиль", svc.now()); err != nil || !ok {
		t.Fatalf("seed trial claim: ok=%v err=%v", ok, err)
	}
	reqID, err := st.CreateTrialRequest(ctx, 555, "профиль", svc.now())
	if err != nil {
		t.Fatalf("seed legacy trial request: %v", err)
	}

	if _, _, _, err := svc.approveTrialRequest(ctx, reqID); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("approve invalid legacy request err=%v, want ErrInvalidUsername", err)
	}
	if len(creator.created) != 0 {
		t.Fatalf("invalid legacy request reached profile creator: %+v", creator.created)
	}
	if req, _ := st.GetTrialRequest(ctx, reqID); req == nil || req.Status != "rejected" {
		t.Fatalf("invalid legacy request was not rejected: %+v", req)
	}
	if ok, err := st.ClaimTrial(ctx, 555, "corrected-name", svc.now()); err != nil || !ok {
		t.Fatalf("invalid legacy request did not release trial claim: ok=%v err=%v", ok, err)
	}
}

func TestSetTrialApprovalPersists(t *testing.T) {
	svc, _, _, _ := newTestService(t) // admin 1000
	ctx := context.Background()
	if err := svc.AdminSetTrial(ctx, 1000, TrialConfig{Enabled: true, Days: 5, TrafficLimitGB: 10, HwidDeviceLimit: 1, RequireApproval: true}); err != nil {
		t.Fatalf("set trial: %v", err)
	}
	if !svc.trialConfig().RequireApproval {
		t.Fatal("RequireApproval should be true in memory")
	}
	// Reload over differing env defaults: the persisted value wins.
	svc.InitTrialConfig(TrialConfig{Enabled: false, Days: 3})
	if !svc.trialConfig().RequireApproval {
		t.Fatal("RequireApproval should persist across InitTrialConfig")
	}
}

func TestAdminPanelExposesTrialApprovalAndRequests(t *testing.T) {
	svc, _, _, st := newTestService(t) // admin 1000
	ctx := context.Background()
	svc.InitTrialConfig(TrialConfig{Enabled: true, Days: 7, TrafficLimitGB: 10, HwidDeviceLimit: 1, RequireApproval: true})
	if _, err := st.CreateTrialRequest(ctx, 555, "newbie", svc.now()); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	panel, err := svc.AdminPanelData(ctx, 1000)
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	if !panel.TrialRequireApproval {
		t.Fatal("panel should expose TrialRequireApproval=true")
	}
	if len(panel.TrialRequests) != 1 || panel.TrialRequests[0].Username != "newbie" || panel.TrialRequests[0].TelegramID != 555 {
		t.Fatalf("panel trial requests = %+v", panel.TrialRequests)
	}
}
