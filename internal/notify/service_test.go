package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// --- fakes for Run ---

type fakeSource struct{ users []remnawave.User }

func (f *fakeSource) GetUsers(_ context.Context) ([]remnawave.User, error) { return f.users, nil }

type sentNote struct {
	chatID int64
	text   string
}

type fakeSender struct {
	sent []sentNote
	fail int // fail the next N sends
}

func (f *fakeSender) SendWithKeyboard(_ context.Context, chatID int64, text string, _ *tgbot.InlineKeyboardMarkup) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("telegram down")
	}
	f.sent = append(f.sent, sentNote{chatID: chatID, text: text})
	return nil
}

type fakePay struct{ remembered []store.NotifiedUser }

func (f *fakePay) RememberUser(_ context.Context, u store.NotifiedUser) error {
	f.remembered = append(f.remembered, u)
	return nil
}
func (f *fakePay) PaymentButton(_ int64) *tgbot.InlineKeyboardMarkup { return nil }

var runNow = time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)

func newRunService(t *testing.T, users []remnawave.User, winbackDays []int) (*Service, *fakeSender, *fakePay) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	snd := &fakeSender{}
	pay := &fakePay{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(&fakeSource{users: users}, snd, pay, st, st, logger, false, winbackDays)
	svc.now = func() time.Time { return runNow }
	return svc, snd, pay
}

// newRunServiceWithStore is like newRunService but also returns the store so a
// test can set per-user notification preferences before running.
func newRunServiceWithStore(t *testing.T, users []remnawave.User, winbackDays []int) (*Service, *fakeSender, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	snd := &fakeSender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(&fakeSource{users: users}, snd, &fakePay{}, st, st, logger, false, winbackDays)
	svc.now = func() time.Time { return runNow }
	return svc, snd, st
}

func TestRunSkipsMutedUsers(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "expiring", remnawave.StatusActive, runNow.Add(7*24*time.Hour), 100),
		rwUser(2, "lapsed", remnawave.StatusExpired, runNow.Add(-25*time.Hour), 200),
	}
	svc, snd, st := newRunServiceWithStore(t, users, []int{1, 3})
	ctx := context.Background()

	// User 100 mutes only expiry reminders; user 200 mutes only win-back.
	if err := st.SetNotificationPref(ctx, 100, store.NotificationExpiry, true); err != nil {
		t.Fatalf("mute expiry: %v", err)
	}
	if err := st.SetNotificationPref(ctx, 200, store.NotificationWinback, true); err != nil {
		t.Fatalf("mute winback: %v", err)
	}

	if err := svc.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("both users muted their respective notice, expected none sent, got %+v", snd.sent)
	}

	// Unmuting expiry for user 100 lets the reminder through on the next run.
	if err := st.SetNotificationPref(ctx, 100, store.NotificationExpiry, false); err != nil {
		t.Fatalf("unmute expiry: %v", err)
	}
	if err := svc.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(snd.sent) != 1 || snd.sent[0].chatID != 100 {
		t.Fatalf("expected reminder to 100 after unmute, got %+v", snd.sent)
	}
}

func rwUser(id int64, username string, status remnawave.Status, expireAt time.Time, tgID int64) remnawave.User {
	u := remnawave.User{
		ID: id, Username: username,
		Status: status, ExpireAt: expireAt,
	}
	if tgID != 0 {
		u.TelegramID = &tgID
	}
	return u
}

func TestRunSendsReminderOnceAcrossRuns(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "alice", remnawave.StatusActive, runNow.Add(7*24*time.Hour), 100),
		rwUser(2, "bob", remnawave.StatusActive, runNow.Add(5*24*time.Hour), 200), // not a milestone
	}
	svc, snd, pay := newRunService(t, users, nil)

	// Simulates RUN_ON_START + a restart on the same milestone day.
	for run := 0; run < 2; run++ {
		if err := svc.Run(context.Background()); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if len(snd.sent) != 1 || snd.sent[0].chatID != 100 {
		t.Fatalf("expected exactly one reminder to alice, got %+v", snd.sent)
	}
	if !strings.Contains(snd.sent[0].text, "истекает") {
		t.Fatalf("unexpected text: %q", snd.sent[0].text)
	}
	if len(pay.remembered) != 1 || pay.remembered[0].RemnawaveID != 1 {
		t.Fatalf("RememberUser calls: %+v", pay.remembered)
	}
}

func TestRunEscapesProfileNameInHTMLReminder(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, `<a href="https://scam.example">Support</a>`, remnawave.StatusActive, runNow.Add(7*24*time.Hour), 100),
	}
	svc, snd, _ := newRunService(t, users, nil)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(snd.sent) != 1 {
		t.Fatalf("sent = %+v", snd.sent)
	}
	if strings.Contains(snd.sent[0].text, `<a href=`) || !strings.Contains(snd.sent[0].text, "&lt;a href=") {
		t.Fatalf("profile name was not escaped: %q", snd.sent[0].text)
	}
}

func TestRunRetriesAfterSendFailure(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "alice", remnawave.StatusActive, runNow.Add(3*24*time.Hour), 100),
	}
	svc, snd, _ := newRunService(t, users, nil)
	snd.fail = 1

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("first run should fail to send: %+v", snd.sent)
	}
	// The failed send released its claim, so the next run retries.
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(snd.sent) != 1 {
		t.Fatalf("expected retry to deliver, got %+v", snd.sent)
	}
}

func TestRunWinbackMilestones(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "day1", remnawave.StatusExpired, runNow.Add(-25*time.Hour), 100),
		rwUser(2, "day3", remnawave.StatusExpired, runNow.Add(-73*time.Hour), 200),
		rwUser(3, "day2", remnawave.StatusExpired, runNow.Add(-49*time.Hour), 300),      // not a milestone
		rwUser(4, "lagged", remnawave.StatusActive, runNow.Add(-25*time.Hour), 400),     // panel status lag
		rwUser(5, "disabled", remnawave.StatusDisabled, runNow.Add(-25*time.Hour), 500), // never solicited
		rwUser(6, "limited", remnawave.StatusLimited, runNow.Add(-25*time.Hour), 600),   // never solicited
		rwUser(7, "no-telegram", remnawave.StatusExpired, runNow.Add(-25*time.Hour), 0), // unreachable
	}
	svc, snd, pay := newRunService(t, users, []int{1, 3})

	for run := 0; run < 2; run++ { // second run must be all dups
		if err := svc.Run(context.Background()); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	got := map[int64]string{}
	for _, m := range snd.sent {
		got[m.chatID] = m.text
	}
	if len(snd.sent) != 3 {
		t.Fatalf("expected win-back for day1, day3 and lagged only, got %+v", snd.sent)
	}
	for _, chatID := range []int64{100, 200, 400} {
		if !strings.Contains(got[chatID], "истекла") {
			t.Fatalf("chat %d missing win-back text: %q", chatID, got[chatID])
		}
	}
	// Snapshots persisted so the «Я оплатил» flow works for expired users too.
	if len(pay.remembered) != 3 {
		t.Fatalf("RememberUser calls: %+v", pay.remembered)
	}
}

func TestRunWinbackDisabled(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "day1", remnawave.StatusExpired, runNow.Add(-25*time.Hour), 100),
	}
	svc, snd, _ := newRunService(t, users, nil)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("win-back disabled, nothing should send: %+v", snd.sent)
	}
}

func TestRunDryRunWritesNoMarkers(t *testing.T) {
	users := []remnawave.User{
		rwUser(1, "alice", remnawave.StatusActive, runNow.Add(7*24*time.Hour), 100),
	}
	svc, snd, _ := newRunService(t, users, nil)
	svc.dryRun = true

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("dry run must not send: %+v", snd.sent)
	}
	// A later real run still owns the claim: dry-run left no marker behind.
	svc.dryRun = false
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if len(snd.sent) != 1 {
		t.Fatalf("real run should send: %+v", snd.sent)
	}
}

func TestDaysUntil(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)

	tests := []struct {
		name string
		now  time.Time
		exp  time.Time
		want int
	}{
		{
			// The reported bug: the Remnawave panel rounds a partial day up, so a
			// subscription with 7 days and a few hours left shows "8 days" there.
			// daysUntil must round up too, or the reminder reads "7" against the
			// panel's "8" — and fires a day early.
			name: "7d8h rounds up to 8 (panel parity)",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 17, 0, 0, 0, msk),
			want: 8,
		},
		{
			// User scenario: Remnawave shows "4 days" on June 7 for expiry June 11.
			// expireAt is midnight MSK (stored as UTC); diff = 3d 7h 50m → rounds
			// up to 4.
			name: "3d7h remaining rounds up to 4",
			now:  time.Date(2026, 6, 7, 16, 10, 0, 0, msk),
			exp:  time.Date(2026, 6, 10, 21, 0, 0, 0, time.UTC), // 2026-06-11 00:00 MSK
			want: 4,
		},
		{
			// Exactly 7 days: a whole number of days needs no rounding.
			name: "exactly 7 days",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 9, 0, 0, 0, msk),
			want: 7,
		},
		{
			// 6d 23h — a seventh day is still running, so it rounds up to 7.
			name: "6d23h rounds up to 7",
			now:  time.Date(2026, 6, 6, 9, 0, 0, 0, msk),
			exp:  time.Date(2026, 6, 13, 8, 0, 0, 0, msk),
			want: 7,
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

func TestDaysSince(t *testing.T) {
	now := time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		exp  time.Time
		want int
	}{
		{name: "expired 1h ago rounds down to 0", exp: now.Add(-time.Hour), want: 0},
		{name: "expired 25h ago is day 1", exp: now.Add(-25 * time.Hour), want: 1},
		{name: "expired exactly 3 days ago", exp: now.Add(-3 * 24 * time.Hour), want: 3},
		{name: "not yet expired", exp: now.Add(time.Hour), want: 0},
		{name: "expires exactly now", exp: now, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := daysSince(now, tc.exp); got != tc.want {
				t.Fatalf("daysSince = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRunSkipsOutsideSendWindow(t *testing.T) {
	// alice sits at the 7-day milestone at both the off-hour restart and the
	// scheduled run, so any delivery difference is down to the send window.
	expiry := time.Date(2026, 6, 18, 0, 9, 0, 0, time.UTC) // off-hour + 7d
	users := []remnawave.User{
		rwUser(1, "alice", remnawave.StatusActive, expiry, 100),
	}
	svc, snd, _ := newRunService(t, users, nil)
	svc.SetSendWindow(time.UTC, 9, 0, time.Hour) // deliver only during the 09:00 hour

	// A RUN_ON_START sweep after a 00:09 restart must send nothing and, crucially,
	// leave the dedup store untouched.
	offHour := time.Date(2026, 6, 11, 0, 9, 0, 0, time.UTC)
	svc.now = func() time.Time { return offHour }
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("off-hour run: %v", err)
	}
	if len(snd.sent) != 0 {
		t.Fatalf("off-hour run must not send: %+v", snd.sent)
	}

	// The scheduled 09:00 run then delivers, proving the off-hour run wrote no mark.
	svc.now = func() time.Time { return runNow } // 2026-06-11 09:00 UTC
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("in-window run: %v", err)
	}
	if len(snd.sent) != 1 || snd.sent[0].chatID != 100 {
		t.Fatalf("in-window run should deliver to alice, got %+v", snd.sent)
	}
}

func TestWithinDailyWindow(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	at := func(h, m int) time.Time { return time.Date(2026, 6, 11, h, m, 0, 0, msk) }

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"at run_at", at(9, 0), true},
		{"mid window", at(9, 30), true},
		{"last minute of window", at(9, 59), true},
		{"window end is exclusive", at(10, 0), false},
		{"off-hour restart", at(0, 9), false},
		{"just before run_at", at(8, 59), false},
		// Same instant as 09:30 MSK expressed in UTC still lands in the MSK window.
		{"foreign-zone instant inside window", time.Date(2026, 6, 11, 6, 30, 0, 0, time.UTC), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinDailyWindow(tc.now, msk, 9, 0, time.Hour); got != tc.want {
				t.Fatalf("withinDailyWindow = %v, want %v", got, tc.want)
			}
		})
	}

	// A window straddling midnight must still match the early-morning side.
	if !withinDailyWindow(at(0, 9).AddDate(0, 0, 1), msk, 23, 30, time.Hour) {
		t.Fatal("expected 00:09 to fall in the previous day's 23:30 window")
	}
}
