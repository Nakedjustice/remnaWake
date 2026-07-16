package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeProfileUsername(t *testing.T) {
	t.Parallel()

	exactly36 := strings.Repeat("a", 36)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "minimum length", input: "abc", want: "abc"},
		{name: "maximum length", input: exactly36, want: exactly36},
		{name: "uppercase digits underscore and hyphen", input: "User_01-test", want: "User_01-test"},
		{name: "outer whitespace is trimmed", input: "  valid-name_01 \t\n", want: "valid-name_01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeProfileUsername(tt.input)
			if err != nil {
				t.Fatalf("normalizeProfileUsername(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeProfileUsername(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeProfileUsernameRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "too short", input: "ab"},
		{name: "too long", input: strings.Repeat("a", 37)},
		{name: "cyrillic", input: "профиль"},
		{name: "accented latin", input: "café"},
		{name: "cjk", input: "用户名称"},
		{name: "unicode numerals", input: "user１２３"},
		{name: "embedded space", input: "user name"},
		{name: "dot", input: "user.name"},
		{name: "emoji", input: "user🙂"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeProfileUsername(tt.input)
			if !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("normalizeProfileUsername(%q) = %q, %v; want ErrInvalidUsername", tt.input, got, err)
			}
			if !errors.Is(err, ErrBadInput) {
				t.Fatalf("normalizeProfileUsername(%q) error %v must remain compatible with ErrBadInput", tt.input, err)
			}
			if got != "" {
				t.Fatalf("normalizeProfileUsername(%q) returned invalid username %q", tt.input, got)
			}
		})
	}
}

func TestProfileLookupUsernameKeepsLegacyUnicodeCompatibility(t *testing.T) {
	t.Parallel()

	if !isValidProfileLookupUsername("профиль") {
		t.Fatal("existing Unicode profile username should remain valid for registration lookup")
	}
	if isValidNewProfileUsername("профиль") {
		t.Fatal("Unicode username must remain invalid for new profile creation")
	}
}

type countingProfileUsernameFinder struct {
	telegramCalls int
	usernameCalls int
}

func (f *countingProfileUsernameFinder) FindByTelegramID(context.Context, int64) ([]Subscriber, error) {
	f.telegramCalls++
	return nil, nil
}

func (f *countingProfileUsernameFinder) FindByUsername(context.Context, string) (*Subscriber, error) {
	f.usernameCalls++
	return nil, nil
}

func (*countingProfileUsernameFinder) FindByShortUUID(context.Context, string) (*Subscriber, error) {
	return nil, nil
}

func (*countingProfileUsernameFinder) ListAll(context.Context) ([]Subscriber, error) {
	return nil, nil
}

func TestTrialInvalidUsernameCanBeRetriedWithoutSideEffects(t *testing.T) {
	for _, invalid := range []string{"профиль", "with space"} {
		invalid := invalid
		t.Run(invalid, func(t *testing.T) {
			svc, st, _, creator, _ := trialFixture(t, &fakeFinder{})
			finder := &countingProfileUsernameFinder{}
			svc.finder = finder
			svc.InitTrial(true, 7)
			ctx := context.Background()

			if !svc.StartTrialFlow(ctx, msg(555, "/trial")) {
				t.Fatal("trial flow should start")
			}
			callsBeforeInvalid := finder.telegramCalls + finder.usernameCalls

			if !svc.HandleText(ctx, msg(555, invalid)) {
				t.Fatal("invalid username should be handled")
			}
			if got := finder.telegramCalls + finder.usernameCalls; got != callsBeforeInvalid {
				t.Fatalf("invalid username reached finder: calls before=%d after=%d", callsBeforeInvalid, got)
			}
			if len(creator.created) != 0 {
				t.Fatalf("invalid username reached profile creator: %+v", creator.created)
			}
			if svc.getTrial(555) == nil {
				t.Fatal("trial input state should remain active for a corrected username")
			}

			// Prove the invalid attempt did not reserve the one-time claim, then put
			// the store back in its original state before retrying the conversation.
			if ok, err := st.ClaimTrial(ctx, 555, "probe", svc.now()); err != nil || !ok {
				t.Fatalf("trial claim was consumed by invalid input: ok=%v err=%v", ok, err)
			}
			if err := st.ReleaseTrial(ctx, 555); err != nil {
				t.Fatalf("release probe claim: %v", err)
			}

			if !svc.HandleText(ctx, msg(555, "valid-name_01")) {
				t.Fatal("corrected username should be handled")
			}
			if len(creator.created) != 1 || creator.created[0] != "valid-name_01" {
				t.Fatalf("corrected username did not create trial profile: %+v", creator.created)
			}
			if svc.getTrial(555) != nil {
				t.Fatal("trial input state should clear after successful retry")
			}
		})
	}
}
