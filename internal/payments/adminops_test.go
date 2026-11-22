package payments

import (
	"context"
	"errors"
	"testing"
)

// The tests in this package address admin operations through the same single
// door the transports use (RunAdminOp), so they verify the guard, the registry
// wiring and the op body in one go — no per-method wrapper tests needed.

// runOp runs an admin op that returns a result DTO and asserts its type.
// When the op errors, the (zero, err) pair is returned without type checks.
func runOp[Out any](t *testing.T, ctx context.Context, svc *Service, actorID int64, name string, in any) (Out, error) {
	t.Helper()
	raw, err := svc.RunAdminOp(ctx, actorID, name, in)
	var out Out
	if err != nil {
		return out, err
	}
	if raw == nil {
		t.Fatalf("admin op %q returned nil result", name)
	}
	typed, ok := raw.(Out)
	if !ok {
		t.Fatalf("admin op %q returned %T, want %T", name, raw, out)
	}
	return typed, nil
}

// runOpErr runs an acknowledge-only admin op and returns its error.
func runOpErr(ctx context.Context, svc *Service, actorID int64, name string, in any) error {
	_, err := svc.RunAdminOp(ctx, actorID, name, in)
	return err
}

// adminTrialIn converts the internal TrialConfig to the registry payload type.
func adminTrialIn(c TrialConfig) *AdminTrialInput {
	return &AdminTrialInput{
		Enabled: c.Enabled, Days: c.Days, TrafficLimitGB: c.TrafficLimitGB,
		HwidDeviceLimit: c.HwidDeviceLimit, SquadUUID: c.SquadUUID,
		RequireApproval: c.RequireApproval,
	}
}

// The door itself: non-admins never reach an op, unknown names are rejected,
// and the guard runs before the registry lookup either way.
func TestRunAdminOpDoor(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.RunAdminOp(ctx, 9999, "panel.get", nil); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin: err = %v, want ErrNotAdmin", err)
	}
	if _, err := svc.RunAdminOp(ctx, 9999, "totally.bogus", nil); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin unknown op: err = %v, want ErrNotAdmin (guard first)", err)
	}
	if _, err := svc.RunAdminOp(ctx, adminTG, "totally.bogus", nil); !errors.Is(err, ErrBadInput) {
		t.Fatalf("admin unknown op: err = %v, want ErrBadInput", err)
	}
}
