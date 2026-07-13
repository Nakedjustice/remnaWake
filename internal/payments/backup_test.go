package payments

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// sqliteMagic is the first 15 bytes of every SQLite database file.
var sqliteMagic = []byte("SQLite format 3")

func newBackupTestService(t *testing.T, adminIDs []int64, dryRun bool) (*Service, *fakeBot, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	ext := &fakeExtender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, ext, &fakeCreator{}, &fakeUpdater{ext: ext}, &fakeFinder{}, &fakeRegistrar{}, newFakeSquadLister(), adminIDs, "₽", dryRun, logger)
	return svc, bot, st
}

func TestScheduledBackupSendsToAllAdmins(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000, 2000}, false)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()

	svc.RunScheduledBackup(ctx)

	if bot.documentUploads != 2 {
		t.Fatalf("documentUploads = %d, want 2", bot.documentUploads)
	}
	if len(bot.docUploadData) != 2 {
		t.Fatalf("captured uploads = %d, want 2", len(bot.docUploadData))
	}
	for i, data := range bot.docUploadData {
		if !bytes.HasPrefix(data, sqliteMagic) {
			t.Fatalf("upload %d is not a SQLite file", i)
		}
	}
	for i, name := range bot.docUploadNames {
		if filepath.Ext(name) != ".db" {
			t.Fatalf("upload %d filename = %q, want .db extension", i, name)
		}
	}
	if _, found, err := st.GetSetting(ctx, "db_backup_last_at"); err != nil || !found {
		t.Fatalf("db_backup_last_at not written: found=%v err=%v", found, err)
	}
}

func TestScheduledBackupDisabledByDefault(t *testing.T) {
	svc, bot, _ := newBackupTestService(t, []int64{1000}, false)
	svc.RunScheduledBackup(context.Background())
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 when disabled", bot.documentUploads)
	}
}

func TestInitBackupConfigUsesEnvironmentDefaults(t *testing.T) {
	svc, _, _ := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 7)
	enabled, interval := svc.backupConfig()
	if !enabled || interval != 7 {
		t.Fatalf("backup config = enabled:%v interval:%d, want true/7", enabled, interval)
	}
}

func TestInitBackupConfigPersistedValuesWin(t *testing.T) {
	svc, _, st := newBackupTestService(t, []int64{1000}, false)
	ctx := context.Background()
	if err := st.UpsertSettings(ctx, map[string]string{
		settingBackupEnabled:      "1",
		settingBackupIntervalDays: "30",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	svc.InitBackupConfig(false, 3)
	enabled, interval := svc.backupConfig()
	if !enabled || interval != 30 {
		t.Fatalf("backup config = enabled:%v interval:%d, want true/30", enabled, interval)
	}
}

func TestInitBackupConfigInvalidPersistedValuesFallBack(t *testing.T) {
	svc, _, st := newBackupTestService(t, []int64{1000}, false)
	ctx := context.Background()
	if err := st.UpsertSettings(ctx, map[string]string{
		settingBackupEnabled:      "invalid",
		settingBackupIntervalDays: "0",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	svc.InitBackupConfig(true, 7)
	enabled, interval := svc.backupConfig()
	if !enabled || interval != 7 {
		t.Fatalf("backup config = enabled:%v interval:%d, want true/7", enabled, interval)
	}
}

func TestScheduledBackupRespectsInterval(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()

	recent := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if err := st.UpsertSetting(ctx, "db_backup_last_at", recent); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}
	svc.RunScheduledBackup(ctx)
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 before interval elapses", bot.documentUploads)
	}

	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	if err := st.UpsertSetting(ctx, "db_backup_last_at", old); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}
	svc.RunScheduledBackup(ctx)
	if bot.documentUploads != 1 {
		t.Fatalf("documentUploads = %d, want 1 after interval elapsed", bot.documentUploads)
	}
	value, found, err := st.GetSetting(ctx, "db_backup_last_at")
	if err != nil || !found {
		t.Fatalf("stamp missing after send: found=%v err=%v", found, err)
	}
	if value == old {
		t.Fatal("stamp not refreshed after send")
	}
}

func TestScheduledBackupVeryLargeIntervalDoesNotOverflow(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	maxInt := int(^uint(0) >> 1)
	svc.InitBackupConfig(true, maxInt)
	ctx := context.Background()
	old := time.Now().AddDate(-400, 0, 0).UTC().Format(time.RFC3339)
	if err := st.UpsertSetting(ctx, settingBackupLastAt, old); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}

	svc.RunScheduledBackup(ctx)
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d for interval %s, want 0", bot.documentUploads, strconv.Itoa(maxInt))
	}
}

func TestBackupConfigChangeForcesNextScheduledRun(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, settingBackupLastAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}

	svc.handleBackupIntervalPick(ctx, 1000, "30")
	if bot.documentUploads != 0 {
		t.Fatalf("changing interval uploaded %d documents, want none", bot.documentUploads)
	}
	if _, found, err := st.GetSetting(ctx, settingBackupLastAt); err != nil || found {
		t.Fatalf("last stamp after interval change: found=%v err=%v", found, err)
	}

	svc.RunScheduledBackup(ctx)
	if bot.documentUploads != 1 {
		t.Fatalf("documentUploads = %d, want 1 at next scheduled run", bot.documentUploads)
	}
}

func TestBackupTogglePersistsAndDoesNotSendImmediately(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(false, 7)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, settingBackupLastAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}

	svc.handleBackupToggle(ctx, 1000)
	if bot.documentUploads != 0 {
		t.Fatalf("enabling uploaded %d documents, want none", bot.documentUploads)
	}
	for key, want := range map[string]string{
		settingBackupEnabled:      "1",
		settingBackupIntervalDays: "7",
	} {
		got, found, err := st.GetSetting(ctx, key)
		if err != nil || !found || got != want {
			t.Fatalf("%s: got=%q found=%v err=%v", key, got, found, err)
		}
	}
	if _, found, err := st.GetSetting(ctx, settingBackupLastAt); err != nil || found {
		t.Fatalf("last stamp after enabling: found=%v err=%v", found, err)
	}

	// Reloading from the same store simulates a restart and must preserve the
	// admin override over different environment defaults.
	svc.InitBackupConfig(false, 1)
	enabled, interval := svc.backupConfig()
	if !enabled || interval != 7 {
		t.Fatalf("reloaded config = enabled:%v interval:%d, want true/7", enabled, interval)
	}
}

func TestBackupToggleOffSuppressesScheduledRun(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()

	svc.handleBackupToggle(ctx, 1000)
	svc.RunScheduledBackup(ctx)
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 while disabled", bot.documentUploads)
	}
	got, found, err := st.GetSetting(ctx, settingBackupEnabled)
	if err != nil || !found || got != "0" {
		t.Fatalf("stored enabled = %q found=%v err=%v", got, found, err)
	}
}

func TestBackupSaveFailureDoesNotPublishRuntimeConfig(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(false, 3)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	svc.handleBackupToggle(context.Background(), 1000)
	enabled, interval := svc.backupConfig()
	if enabled || interval != 3 {
		t.Fatalf("runtime config changed after save failure: enabled=%v interval=%d", enabled, interval)
	}
	if len(bot.sent) != 1 || bot.sent[0].Keyboard != nil || !strings.Contains(bot.sent[0].Text, "Ошибка") {
		t.Fatalf("expected save error without refreshed settings card: %+v", bot.sent)
	}
}

func TestBackupAdminCardAndPreset(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(false, 3)
	ctx := context.Background()

	if !svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb", From: tg.User{ID: 1000}, Data: "adm:backup"}) {
		t.Fatal("adm:backup should be handled")
	}
	if len(bot.sent) != 1 || bot.sent[0].Keyboard == nil {
		t.Fatalf("expected backup settings card: %+v", bot.sent)
	}
	if !strings.Contains(bot.sent[0].Text, "Интервал: каждые 3 дня") {
		t.Fatalf("unexpected card text: %q", bot.sent[0].Text)
	}
	rows := bot.sent[0].Keyboard.InlineKeyboard
	if len(rows) != 5 || rows[4][0].CallbackData != "adm:menu" {
		t.Fatalf("unexpected backup keyboard: %+v", rows)
	}

	bot.sent = nil
	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb2", From: tg.User{ID: 1000}, Data: "adm:backup:days:7"})
	got, found, err := st.GetSetting(ctx, settingBackupIntervalDays)
	if err != nil || !found || got != "7" {
		t.Fatalf("stored interval = %q found=%v err=%v", got, found, err)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "7 дней") {
		t.Fatalf("expected refreshed settings card: %+v", bot.sent)
	}
}

func TestBackupCustomIntervalInput(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()

	svc.HandleCallback(ctx, &tg.CallbackQuery{ID: "cb", From: tg.User{ID: 1000}, Data: "adm:backup:custom"})
	svc.mu.Lock()
	step := svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputBackupInterval {
		t.Fatalf("step = %v, want adminInputBackupInterval", step)
	}

	bot.sent = nil
	if !svc.HandleAdminCommand(ctx, msg(1000, "zero")) {
		t.Fatal("invalid custom interval should be consumed")
	}
	svc.mu.Lock()
	step = svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputBackupInterval {
		t.Fatalf("invalid input cleared step: %v", step)
	}

	bot.sent = nil
	if !svc.HandleAdminCommand(ctx, msg(1000, "14")) {
		t.Fatal("valid custom interval should be consumed")
	}
	got, found, err := st.GetSetting(ctx, settingBackupIntervalDays)
	if err != nil || !found || got != "14" {
		t.Fatalf("stored interval = %q found=%v err=%v", got, found, err)
	}
	svc.mu.Lock()
	step = svc.adminInput[1000].step
	svc.mu.Unlock()
	if step != adminInputNone {
		t.Fatalf("valid input did not clear step: %v", step)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].Text, "14 дней") {
		t.Fatalf("expected refreshed card: %+v", bot.sent)
	}
}

func TestScheduledBackupDryRunSendsNothingAndWritesNoStamp(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, true /*dryRun*/)
	svc.InitBackupConfig(true, 1)
	ctx := context.Background()

	svc.RunScheduledBackup(ctx)

	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 in dry run", bot.documentUploads)
	}
	if _, found, _ := st.GetSetting(ctx, "db_backup_last_at"); found {
		t.Fatal("dry run must not write db_backup_last_at")
	}
}

func TestScheduledBackupSendFailureKeepsStampClear(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	bot.sendErrs = map[int64]error{1000: io.ErrClosedPipe}
	ctx := context.Background()

	svc.RunScheduledBackup(ctx)

	if _, found, _ := st.GetSetting(ctx, "db_backup_last_at"); found {
		t.Fatal("stamp must not be written when every send failed")
	}
}

func TestBackupCommandSendsToRequestingAdminOnly(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000, 2000}, false)
	ctx := context.Background()

	if !svc.HandleAdminCommand(ctx, msg(1000, "/backup")) {
		t.Fatal("expected /backup to be handled for an admin")
	}
	if bot.documentUploads != 1 {
		t.Fatalf("documentUploads = %d, want 1", bot.documentUploads)
	}
	if len(bot.docs) != 1 || bot.docs[0].ChatID != 1000 {
		t.Fatalf("document went to %+v, want chat 1000", bot.docs)
	}
	if !bytes.HasPrefix(bot.docUploadData[0], sqliteMagic) {
		t.Fatal("/backup upload is not a SQLite file")
	}
	if _, found, _ := st.GetSetting(ctx, "db_backup_last_at"); found {
		t.Fatal("/backup must not touch the scheduled-backup stamp")
	}
}

func TestBackupCommandIgnoredForNonAdmin(t *testing.T) {
	svc, bot, _ := newBackupTestService(t, []int64{1000}, false)
	if svc.HandleAdminCommand(context.Background(), msg(2222, "/backup")) {
		t.Fatal("non-admin /backup must not be handled")
	}
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0", bot.documentUploads)
	}
}

func TestBackupCommandDryRun(t *testing.T) {
	svc, bot, _ := newBackupTestService(t, []int64{1000}, true /*dryRun*/)
	if !svc.HandleAdminCommand(context.Background(), msg(1000, "/backup")) {
		t.Fatal("expected /backup to be handled in dry run")
	}
	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 in dry run", bot.documentUploads)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected a text notice instead of a document in dry run")
	}
}

func TestBackupOversizeSendsWarningInsteadOfDocument(t *testing.T) {
	svc, bot, _ := newBackupTestService(t, []int64{1000}, false)
	svc.InitBackupConfig(true, 1)
	old := maxBackupUploadBytes
	maxBackupUploadBytes = 16
	t.Cleanup(func() { maxBackupUploadBytes = old })

	svc.RunScheduledBackup(context.Background())

	if bot.documentUploads != 0 {
		t.Fatalf("documentUploads = %d, want 0 for oversize backup", bot.documentUploads)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected a warning message for oversize backup")
	}
}
