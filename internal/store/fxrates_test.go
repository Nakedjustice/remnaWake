package store

import (
	"context"
	"testing"
	"time"
)

func TestFxRatesUpsert(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if got, err := st.ListFxRates(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v err %v", got, err)
	}
	if r, err := st.GetFxRate(ctx, "USD"); err != nil || r != nil {
		t.Fatalf("missing: %+v err %v", r, err)
	}

	at := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	if err := st.UpsertAutoFxRate(ctx, "USD", 90.5, at); err != nil {
		t.Fatalf("auto: %v", err)
	}
	// A manual upsert must not clear the auto rate (and vice versa).
	if err := st.UpsertManualFxRate(ctx, "USD", 88); err != nil {
		t.Fatalf("manual: %v", err)
	}
	r, err := st.GetFxRate(ctx, "USD")
	if err != nil || r == nil {
		t.Fatalf("get: %+v err %v", r, err)
	}
	if !r.HasAuto || r.AutoRate != 90.5 || !r.AutoUpdatedAt.Equal(at) {
		t.Fatalf("auto side: %+v", r)
	}
	if !r.HasManual || r.ManualRate != 88 {
		t.Fatalf("manual side: %+v", r)
	}

	// Re-fetching updates only the auto side.
	at2 := at.Add(24 * time.Hour)
	if err := st.UpsertAutoFxRate(ctx, "USD", 91, at2); err != nil {
		t.Fatalf("auto2: %v", err)
	}
	r, _ = st.GetFxRate(ctx, "USD")
	if r.AutoRate != 91 || r.ManualRate != 88 {
		t.Fatalf("after refetch: %+v", r)
	}

	if err := st.UpsertManualFxRate(ctx, "EUR", 100); err != nil {
		t.Fatalf("eur: %v", err)
	}
	list, err := st.ListFxRates(ctx)
	if err != nil || len(list) != 2 || list[0].Currency != "EUR" || list[1].Currency != "USD" {
		t.Fatalf("list: %+v err %v", list, err)
	}
}
