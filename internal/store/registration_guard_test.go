package store

import (
	"context"
	"testing"
)

func TestRegistrationGuardIsIdempotentAndCanBeUnblocked(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	guard := RegistrationGuard{
		TelegramID: 42, Username: "support_account", FirstName: "Support",
		MatchedPattern: "support", Blocked: true,
	}

	created, blocked, err := st.RecordRegistration(ctx, guard)
	if err != nil || !created || !blocked {
		t.Fatalf("first record = created:%v blocked:%v err:%v", created, blocked, err)
	}

	created, blocked, err = st.RecordRegistration(ctx, RegistrationGuard{TelegramID: 42})
	if err != nil || created || !blocked {
		t.Fatalf("repeat record = created:%v blocked:%v err:%v", created, blocked, err)
	}

	changed, err := st.UnblockRegistration(ctx, 42)
	if err != nil || !changed {
		t.Fatalf("unblock = changed:%v err:%v", changed, err)
	}
	if blocked, err = st.RegistrationBlocked(ctx, 42); err != nil || blocked {
		t.Fatalf("blocked after unblock = %v, err=%v", blocked, err)
	}

	created, blocked, err = st.RecordRegistration(ctx, guard)
	if err != nil || created || blocked {
		t.Fatalf("record after admin unblock = created:%v blocked:%v err:%v", created, blocked, err)
	}
	if changed, err = st.UnblockRegistration(ctx, 42); err != nil || changed {
		t.Fatalf("second unblock = changed:%v err:%v", changed, err)
	}
}

func TestListBlockedRegistrationGuardsExcludesUnblocked(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	for _, guard := range []RegistrationGuard{
		{TelegramID: 41, Username: "support_one", MatchedPattern: "support", Blocked: true},
		{TelegramID: 42, Username: "admin_one", MatchedPattern: "admin", Blocked: true},
		{TelegramID: 43, Username: "safe_user", Blocked: false},
	} {
		if _, _, err := st.RecordRegistration(ctx, guard); err != nil {
			t.Fatalf("record %+v: %v", guard, err)
		}
	}
	if changed, err := st.UnblockRegistration(ctx, 41); err != nil || !changed {
		t.Fatalf("unblock first guard = changed:%v err:%v", changed, err)
	}

	guards, err := st.ListBlockedRegistrationGuards(ctx)
	if err != nil {
		t.Fatalf("list blocked guards: %v", err)
	}
	if len(guards) != 1 || guards[0].TelegramID != 42 || guards[0].Username != "admin_one" ||
		guards[0].MatchedPattern != "admin" || guards[0].CreatedAt.IsZero() {
		t.Fatalf("blocked guards = %+v", guards)
	}
}
