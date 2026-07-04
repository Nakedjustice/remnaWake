package payments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
)

// settingBackupLastAt is the settings key holding the RFC3339 time of the last
// successfully delivered scheduled backup (code-managed state, not admin-facing).
const settingBackupLastAt = "db_backup_last_at"

// maxBackupUploadBytes is the Telegram Bot API ceiling for bot file uploads;
// a var so tests can lower it.
var maxBackupUploadBytes = 50 * 1024 * 1024

var errBackupTooLarge = fmt.Errorf("backup exceeds the Telegram upload limit")

// SetBackupConfig enables the scheduled database backup. Called once from
// main.go before the scheduler starts (same lifecycle as dryRun, no locking).
func (s *Service) SetBackupConfig(enabled bool, intervalDays int) {
	s.backupEnabled = enabled
	s.backupIntervalDays = intervalDays
}

// RunScheduledBackup sends a snapshot of the database to every admin's DM when
// the backup feature is on and at least the configured interval has passed
// since the last delivered backup. Runs inside the daily scheduler job.
func (s *Service) RunScheduledBackup(ctx context.Context) {
	if !s.backupEnabled || !s.isEnabled() {
		return
	}
	value, found, err := s.store.GetSetting(ctx, settingBackupLastAt)
	if err != nil {
		s.logger.Error("backup: read last-sent stamp failed", "err", err.Error())
		return
	}
	if found {
		if last, err := time.Parse(time.RFC3339, value); err == nil {
			interval := time.Duration(s.backupIntervalDays) * 24 * time.Hour
			if s.now().Sub(last) < interval {
				return
			}
		}
	}
	if s.dryRun {
		s.logger.Info("dry-run: would send database backup to admins")
		return
	}

	filename, data, err := s.buildBackupDocument(ctx)
	if err != nil {
		if err == errBackupTooLarge {
			warning := i18n.T("⚠️ Резервная копия базы данных больше 50 МБ и не может быть отправлена в Telegram. Сделайте бэкап на сервере вручную.")
			for _, adminID := range s.adminIDs {
				_ = s.bot.SendPlain(ctx, adminID, warning)
			}
		}
		s.logger.Error("backup: build failed", "err", err.Error())
		return
	}

	caption := i18n.T("📦 Плановая резервная копия базы данных бота. Для восстановления: ./install.sh restore <файл>.")
	sentAny := false
	for _, adminID := range s.adminIDs {
		if _, _, err := s.bot.SendDocumentUpload(ctx, adminID, filename, data, caption, nil); err != nil {
			s.logger.Error("backup: send failed", "admin", adminID, "err", err.Error())
			continue
		}
		sentAny = true
	}
	// Stamp only when someone received the file so a fully failed round is
	// retried on the next daily run.
	if sentAny {
		stamp := s.now().UTC().Format(time.RFC3339)
		if err := s.store.UpsertSetting(ctx, settingBackupLastAt, stamp); err != nil {
			s.logger.Error("backup: write last-sent stamp failed", "err", err.Error())
		}
		s.logger.Info("backup: sent to admins", "file", filename, "bytes", len(data))
	}
}

// cmdBackup handles the /backup admin command: an immediate one-off backup
// sent only to the requesting admin, independent of the schedule and stamp.
func (s *Service) cmdBackup(ctx context.Context, chatID int64) {
	if s.dryRun {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Резервная копия не отправлена (dry-run)."))
		return
	}
	filename, data, err := s.buildBackupDocument(ctx)
	if err != nil {
		if err == errBackupTooLarge {
			_ = s.bot.SendPlain(ctx, chatID, i18n.T("⚠️ Резервная копия базы данных больше 50 МБ и не может быть отправлена в Telegram. Сделайте бэкап на сервере вручную."))
			return
		}
		s.logger.Error("backup: build failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось создать резервную копию."))
		return
	}
	caption := i18n.T("📦 Резервная копия базы данных бота. Для восстановления: ./install.sh restore <файл>.")
	if _, _, err := s.bot.SendDocumentUpload(ctx, chatID, filename, data, caption, nil); err != nil {
		s.logger.Error("backup: send failed", "admin", chatID, "err", err.Error())
	}
}

// buildBackupDocument snapshots the database into a temp file via VACUUM INTO,
// reads it back and removes the temp file.
func (s *Service) buildBackupDocument(ctx context.Context) (filename string, data []byte, err error) {
	tmpDir, err := os.MkdirTemp("", "remnawake-backup-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// VACUUM INTO refuses to overwrite, so the file must not exist yet.
	tmpPath := filepath.Join(tmpDir, "backup.db")
	if err := s.store.BackupTo(ctx, tmpPath); err != nil {
		return "", nil, err
	}
	data, err = os.ReadFile(tmpPath)
	if err != nil {
		return "", nil, fmt.Errorf("read backup: %w", err)
	}
	if len(data) > maxBackupUploadBytes {
		return "", nil, errBackupTooLarge
	}
	filename = "remnawake-backup-" + s.now().Format("2006-01-02-1504") + ".db"
	return filename, data, nil
}
