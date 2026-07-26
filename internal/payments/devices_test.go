package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// fakeDeviceManager records the panel calls the device flows make and mutates
// its own listing so a revoke is observable through a follow-up read.
type fakeDeviceManager struct {
	devices map[string][]Device // panel uuid -> registered devices
	listErr error
	delErr  error
	deleted []string // "<uuid>|<hwid>" per DeleteDevice call
	lists   int
}

func (f *fakeDeviceManager) ListDevices(_ context.Context, uuid string) ([]Device, error) {
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.devices[uuid], nil
}

func (f *fakeDeviceManager) DeleteDevice(_ context.Context, uuid, hwid string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, uuid+"|"+hwid)
	kept := make([]Device, 0, len(f.devices[uuid]))
	for _, d := range f.devices[uuid] {
		if d.HWID != hwid {
			kept = append(kept, d)
		}
	}
	f.devices[uuid] = kept
	return nil
}

var deviceNow = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

// deviceService seeds two profiles for Telegram user 777 (a capped one that is
// full and an uncapped one) plus an unrelated profile for 888, so ownership and
// the unlimited case are covered by every test that needs them.
func deviceService(t *testing.T) (*Service, *fakeBot, *fakeDeviceManager) {
	t.Helper()
	svc, bot, _, _ := newTestService(t)
	svc.now = func() time.Time { return deviceNow }
	capped, uncapped := 2, 0
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{
		777: {
			{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777, HwidDeviceLimit: &capped},
			{RemnawaveID: 43, UUID: "uuid-43", Username: "alice-work", TelegramID: 777, HwidDeviceLimit: &uncapped},
		},
		888: {{RemnawaveID: 99, UUID: "uuid-99", Username: "bob", TelegramID: 888, HwidDeviceLimit: &capped}},
	}}
	dm := &fakeDeviceManager{devices: map[string][]Device{
		"uuid-42": {
			{HWID: "hw-phone", Platform: "iOS", OSVersion: "17.4", DeviceModel: "iPhone 15", CreatedAt: deviceNow.AddDate(0, -1, 0)},
			{HWID: "hw-laptop", Platform: "macOS", DeviceModel: "MacBook Air", CreatedAt: deviceNow.AddDate(0, 0, -3)},
		},
		"uuid-43": {{HWID: "hw-work", Platform: "Windows"}},
		"uuid-99": {{HWID: "hw-bob", Platform: "Android"}},
	}}
	svc.SetDeviceManager(dm)
	return svc, bot, dm
}

func TestDeviceOverviewReportsEveryProfileSeparately(t *testing.T) {
	svc, _, _ := deviceService(t)
	got, err := svc.DeviceOverview(context.Background(), 777)
	if err != nil {
		t.Fatalf("DeviceOverview: %v", err)
	}
	if len(got.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want one section per linked profile", got.Profiles)
	}
	capped := got.Profiles[0]
	if capped.Username != "alice" || capped.RemnawaveID != 42 || capped.Limit != 2 || capped.Used != 2 || !capped.Full {
		t.Fatalf("capped profile = %+v", capped)
	}
	if capped.Unlimited {
		t.Fatalf("a limit of 2 must not report unlimited: %+v", capped)
	}
	if len(capped.Devices) != 2 || capped.Devices[0].HWID != "hw-phone" {
		t.Fatalf("devices = %+v", capped.Devices)
	}
	// Panel-supplied metadata is passed through verbatim; only the timestamp is
	// reformatted for display.
	if capped.Devices[0].DeviceModel != "iPhone 15" || capped.Devices[0].Platform != "iOS" || capped.Devices[0].OSVersion != "17.4" {
		t.Fatalf("device metadata = %+v", capped.Devices[0])
	}
	if capped.Devices[0].CreatedAt != "26.06.2026" {
		t.Fatalf("created at = %q", capped.Devices[0].CreatedAt)
	}
	// A zero HWID limit is Remnawave's "unlimited": slots exist but never fill.
	unlimited := got.Profiles[1]
	if !unlimited.Unlimited || unlimited.Full || unlimited.Limit != 0 || unlimited.Used != 1 {
		t.Fatalf("unlimited profile = %+v", unlimited)
	}
}

func TestDeviceOverviewRefusesUnlinkedAndUnconfigured(t *testing.T) {
	svc, _, _ := deviceService(t)
	if _, err := svc.DeviceOverview(context.Background(), 555); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unlinked = %v, want ErrNotLinked", err)
	}
	svc.SetDeviceManager(nil)
	if _, err := svc.DeviceOverview(context.Background(), 777); !errors.Is(err, ErrDevicesUnavailable) {
		t.Fatalf("unconfigured = %v, want ErrDevicesUnavailable", err)
	}
}

func TestDeviceOverviewSurfacesPanelFailure(t *testing.T) {
	svc, _, dm := deviceService(t)
	dm.listErr = errors.New("panel down")
	if _, err := svc.DeviceOverview(context.Background(), 777); !errors.Is(err, ErrPanelUnavailable) {
		t.Fatalf("panel failure = %v, want ErrPanelUnavailable", err)
	}
}

func TestRevokeDeviceFreesSlotAndConcealsForeignTargets(t *testing.T) {
	svc, _, dm := deviceService(t)
	ctx := context.Background()

	// A profile the caller does not own is refused even though it exists.
	if _, err := svc.RevokeDevice(ctx, 777, 99, "hw-bob"); !errors.Is(err, ErrProfileUnknown) {
		t.Fatalf("foreign profile = %v, want ErrProfileUnknown", err)
	}
	// A device that belongs to another profile is reported exactly like a
	// missing one, so the endpoint cannot be used to probe for real hwids.
	if _, err := svc.RevokeDevice(ctx, 777, 42, "hw-bob"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("foreign device = %v, want ErrDeviceNotFound", err)
	}
	if len(dm.deleted) != 0 {
		t.Fatalf("nothing may be revoked yet: %v", dm.deleted)
	}

	got, err := svc.RevokeDevice(ctx, 777, 42, "hw-phone")
	if err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if len(dm.deleted) != 1 || dm.deleted[0] != "uuid-42|hw-phone" {
		t.Fatalf("deleted = %v", dm.deleted)
	}
	// The caller gets the refreshed overview so the freed slot is visible.
	if got.Profiles[0].Used != 1 || got.Profiles[0].Full {
		t.Fatalf("refreshed profile = %+v", got.Profiles[0])
	}
	// Revoking the same slot twice is now a miss, not a second panel call.
	if _, err := svc.RevokeDevice(ctx, 777, 42, "hw-phone"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("second revoke = %v, want ErrDeviceNotFound", err)
	}
	if len(dm.deleted) != 1 {
		t.Fatalf("second revoke must not reach the panel: %v", dm.deleted)
	}
}

func TestRevokeDeviceHonorsDryRun(t *testing.T) {
	svc, _, dm := deviceService(t)
	svc.dryRun = true
	if _, err := svc.RevokeDevice(context.Background(), 777, 42, "hw-phone"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if len(dm.deleted) != 0 {
		t.Fatalf("dry run must not mutate the panel: %v", dm.deleted)
	}
}

// A slot revoked from another session between the listing and the confirmation
// comes back as ErrDeviceNotFound rather than a generic panel error.
func TestRevokeDeviceMapsPanelNotFound(t *testing.T) {
	svc, _, dm := deviceService(t)
	dm.delErr = ErrDeviceNotFound
	if _, err := svc.RevokeDevice(context.Background(), 777, 42, "hw-phone"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("err = %v, want ErrDeviceNotFound", err)
	}
	dm.delErr = errors.New("panel down")
	if _, err := svc.RevokeDevice(context.Background(), 777, 42, "hw-phone"); !errors.Is(err, ErrPanelUnavailable) {
		t.Fatalf("err = %v, want ErrPanelUnavailable", err)
	}
}

func TestDeviceCallbackConfirmsBeforeRevoking(t *testing.T) {
	svc, bot, dm := deviceService(t)
	ctx := context.Background()

	if !svc.HandleCallback(ctx, cbq(777, "dev:list")) {
		t.Fatal("dev:list should be handled")
	}
	listing := bot.sent[len(bot.sent)-1]
	if !strings.Contains(listing.Text, "iPhone 15") || !strings.Contains(listing.Text, "alice-work") {
		t.Fatalf("listing text = %q", listing.Text)
	}
	// One revoke button per registered device, addressed by short index because
	// an hwid never fits in Telegram's 64-byte callback payload.
	var revokeData []string
	for _, r := range listing.Keyboard.InlineKeyboard {
		if strings.HasPrefix(r[0].CallbackData, "dev:rev:") {
			revokeData = append(revokeData, r[0].CallbackData)
		}
	}
	if len(revokeData) != 3 || revokeData[0] != "dev:rev:0" {
		t.Fatalf("revoke buttons = %v", revokeData)
	}

	// Tapping a revoke button must only ask; nothing reaches the panel yet.
	if !svc.HandleCallback(ctx, cbq(777, "dev:rev:0")) {
		t.Fatal("dev:rev should be handled")
	}
	if len(dm.deleted) != 0 {
		t.Fatalf("confirmation step must not revoke: %v", dm.deleted)
	}
	confirm := bot.sent[len(bot.sent)-1]
	if confirm.Keyboard.InlineKeyboard[0][0].CallbackData != "dev:rev:0:ok" {
		t.Fatalf("confirm keyboard = %+v", confirm.Keyboard.InlineKeyboard)
	}

	if !svc.HandleCallback(ctx, cbq(777, "dev:rev:0:ok")) {
		t.Fatal("dev:rev:ok should be handled")
	}
	if len(dm.deleted) != 1 || dm.deleted[0] != "uuid-42|hw-phone" {
		t.Fatalf("deleted = %v", dm.deleted)
	}
	// The refreshed listing is pushed back so the user sees the freed slot.
	if strings.Contains(bot.sent[len(bot.sent)-1].Text, "iPhone 15") {
		t.Fatalf("stale listing after revoke: %q", bot.sent[len(bot.sent)-1].Text)
	}
}

// The index in dev:rev: is meaningless without the presser's own slot map, and
// the map is keyed by cb.From.ID — a forwarded keyboard revokes nothing.
func TestDeviceCallbackNeverTrustsCallbackDataForIdentity(t *testing.T) {
	svc, bot, dm := deviceService(t)
	ctx := context.Background()
	if !svc.HandleCallback(ctx, cbq(777, "dev:list")) {
		t.Fatal("dev:list should be handled")
	}
	if !svc.HandleCallback(ctx, cbq(888, "dev:rev:0:ok")) {
		t.Fatal("dev:rev:ok should be handled")
	}
	if len(dm.deleted) != 0 {
		t.Fatalf("another user's tap must revoke nothing: %v", dm.deleted)
	}
	if len(bot.answers) == 0 || bot.answers[len(bot.answers)-1] == "" {
		t.Fatalf("the stranger must get an answer: %v", bot.answers)
	}
}

func TestDeviceSlotTTLEviction(t *testing.T) {
	svc, bot, dm := deviceService(t)
	ctx := context.Background()
	if !svc.HandleCallback(ctx, cbq(777, "dev:list")) {
		t.Fatal("dev:list should be handled")
	}
	svc.now = func() time.Time { return deviceNow.Add(deviceSlotTTL + time.Second) }
	if !svc.HandleCallback(ctx, cbq(777, "dev:rev:0:ok")) {
		t.Fatal("dev:rev:ok should be handled")
	}
	if len(dm.deleted) != 0 {
		t.Fatalf("an expired slot map must revoke nothing: %v", dm.deleted)
	}
	svc.mu.Lock()
	_, kept := svc.deviceSlots[777]
	svc.mu.Unlock()
	if kept {
		t.Fatal("expired slot map must be evicted")
	}
	if len(bot.answers) == 0 || bot.answers[len(bot.answers)-1] == "" {
		t.Fatalf("expected an answer explaining the stale keyboard: %v", bot.answers)
	}
}

func TestDeviceCallbackWithoutLinkedProfile(t *testing.T) {
	svc, bot, _ := deviceService(t)
	cb := &tg.CallbackQuery{ID: "cbid", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 50, Chat: tg.Chat{ID: 555}}, Data: "dev:list"}
	if !svc.HandleCallback(context.Background(), cb) {
		t.Fatal("dev:list should be handled")
	}
	if len(bot.sent) != 0 {
		t.Fatalf("no device screen may be rendered for an unlinked user: %+v", bot.sent)
	}
	if len(bot.answers) != 1 || bot.answers[0] == "" {
		t.Fatalf("unlinked user must be told why the screen is empty: %v", bot.answers)
	}
}
