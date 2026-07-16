package store

import (
	"context"
	"testing"
)

func TestSettingsUpsertAndGet(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, found, err := st.GetSetting(ctx, "payment_requisites"); err != nil || found {
		t.Fatalf("missing key: found=%v err=%v", found, err)
	}

	if err := st.UpsertSetting(ctx, "payment_requisites", "card 1234"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, found, err := st.GetSetting(ctx, "payment_requisites")
	if err != nil || !found || got != "card 1234" {
		t.Fatalf("get after insert: got=%q found=%v err=%v", got, found, err)
	}

	if err := st.UpsertSetting(ctx, "payment_requisites", "sbp +7900"); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, found, err = st.GetSetting(ctx, "payment_requisites")
	if err != nil || !found || got != "sbp +7900" {
		t.Fatalf("get after update: got=%q found=%v err=%v", got, found, err)
	}
}

func TestUpdateSettingsUpsertsAndDeletesAtomically(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, "old", "value"); err != nil {
		t.Fatalf("seed old setting: %v", err)
	}

	if err := st.UpdateSettings(ctx, map[string]string{
		"enabled":  "1",
		"interval": "7",
	}, []string{"old"}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if _, found, err := st.GetSetting(ctx, "old"); err != nil || found {
		t.Fatalf("deleted setting: found=%v err=%v", found, err)
	}
	for key, want := range map[string]string{"enabled": "1", "interval": "7"} {
		got, found, err := st.GetSetting(ctx, key)
		if err != nil || !found || got != want {
			t.Fatalf("%s: got=%q found=%v err=%v", key, got, found, err)
		}
	}
}
