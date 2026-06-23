package store

import (
	"context"
	"testing"
)

func TestUpsertSettingsWritesEveryValue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	values := map[string]string{"trial_enabled": "1", "trial_days": "5", "trial_squad_uuid": "sq-1"}
	if err := s.UpsertSettings(ctx, values); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}
	for key, want := range values {
		got, found, err := s.GetSetting(ctx, key)
		if err != nil || !found || got != want {
			t.Fatalf("%s = %q found=%v err=%v, want %q", key, got, found, err, want)
		}
	}
}
