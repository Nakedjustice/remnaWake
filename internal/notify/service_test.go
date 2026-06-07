package notify

import (
	"testing"
	"time"
)

func TestDaysUntil(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)

	tests := []struct {
		name string
		now  time.Time
		exp  time.Time
		want int
	}{
		{
			// User scenario: Remnawave shows "3 days" on June 7 for expiry June 11.
			// expireAt is midnight MSK (stored as UTC); diff = 3d 7h 50m → 3.
			name: "3d7h remaining rounds down to 3",
			now:  time.Date(2026, 6, 7, 16, 10, 0, 0, msk),
			exp:  time.Date(2026, 6, 10, 21, 0, 0, 0, time.UTC), // 2026-06-11 00:00 MSK
			want: 3,
		},
		{
			// Exactly 7 days: on-the-hour trigger.
			name: "exactly 7 days",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 9, 0, 0, 0, msk),
			want: 7,
		},
		{
			// 6d 23h — falls short of 7, should be 6.
			name: "6h23h rounds to 6 not 7",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 8, 0, 0, 0, msk),
			want: 6,
		},
		{
			// UTC expiry, now in MSK — zone difference should not matter.
			name: "UTC expiry with MSK now",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 6, 0, 0, 0, time.UTC), // same instant as 09:00 MSK
			want: 7,
		},
		{
			name: "already expired",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC), // 03:00 MSK, in the past
			want: 0,
		},
		{
			name: "expires exactly now",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := daysUntil(tc.now, tc.exp); got != tc.want {
				t.Fatalf("daysUntil = %d, want %d", got, tc.want)
			}
		})
	}
}
