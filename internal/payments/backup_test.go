package payments

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
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
	svc.SetBackupConfig(true, 1)
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

func TestScheduledBackupRespectsInterval(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, false)
	svc.SetBackupConfig(true, 1)
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

func TestScheduledBackupDryRunSendsNothingAndWritesNoStamp(t *testing.T) {
	svc, bot, st := newBackupTestService(t, []int64{1000}, true /*dryRun*/)
	svc.SetBackupConfig(true, 1)
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
	svc.SetBackupConfig(true, 1)
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
	svc.SetBackupConfig(true, 1)
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
