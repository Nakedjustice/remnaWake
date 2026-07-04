package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupToProducesOpenableCopy(t *testing.T) {
	dir := t.TempDir()
	src, err := New(filepath.Join(dir, "bot.db"))
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}
	defer src.Close()

	ctx := context.Background()
	if err := src.UpsertSetting(ctx, "backup_probe", "42"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := src.BackupTo(ctx, dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	copyStore, err := New(dest)
	if err != nil {
		t.Fatalf("open backup copy: %v", err)
	}
	defer copyStore.Close()
	value, found, err := copyStore.GetSetting(ctx, "backup_probe")
	if err != nil {
		t.Fatalf("read setting from copy: %v", err)
	}
	if !found || value != "42" {
		t.Fatalf("setting not carried into backup: found=%v value=%q", found, value)
	}
}

func TestBackupToRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	src, err := New(filepath.Join(dir, "bot.db"))
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}
	defer src.Close()

	dest := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(dest, []byte("not empty"), 0o600); err != nil {
		t.Fatalf("prepare existing file: %v", err)
	}
	if err := src.BackupTo(context.Background(), dest); err == nil {
		t.Fatal("expected error when destination already exists")
	}
}
