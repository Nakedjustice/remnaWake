package notify

import (
	"testing"
	"time"
)

func TestDaysUntilNormalizesTimezones(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60) // UTC+3, like Europe/Moscow

	// now is in the configured TZ; the panel returns expireAt in UTC.
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, msk)

	tests := []struct {
		name string
		exp  time.Time
		want int
	}{
		{
			// 2026-06-13 22:00Z == 2026-06-14 01:00 MSK -> 8 local days away.
			// The old zone-mixing code reported 7 here.
			name: "utc expiry crossing local midnight",
			exp:  time.Date(2026, 6, 13, 22, 0, 0, 0, time.UTC),
			want: 8,
		},
		{
			name: "same-day local expiry counts as 7",
			exp:  time.Date(2026, 6, 13, 5, 0, 0, 0, time.UTC), // 08:00 MSK on the 13th
			want: 7,
		},
		{
			name: "already past",
			exp:  time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := daysUntil(now, tc.exp); got != tc.want {
				t.Fatalf("daysUntil(%s, %s) = %d, want %d", now, tc.exp, got, tc.want)
			}
		})
	}
}
