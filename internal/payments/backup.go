package payments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

const (
	settingBackupEnabled      = "db_backup_enabled"
	settingBackupIntervalDays = "db_backup_interval_days"
	// settingBackupLastAt holds the RFC3339 time of the last successfully
	// delivered scheduled backup (code-managed state, not admin-facing).
	settingBackupLastAt = "db_backup_last_at"
)

// maxBackupUploadBytes is the Telegram Bot API ceiling for bot file uploads;
// a var so tests can lower it.
var maxBackupUploadBytes = 50 * 1024 * 1024

var errBackupTooLarge = fmt.Errorf("backup exceeds the Telegram upload limit")

// InitBackupConfig loads persisted admin settings over deployment-provided
// defaults. It is called once at startup before callback polling begins.
func (s *Service) InitBackupConfig(envEnabled bool, envIntervalDays int) {
	ctx := context.Background()
	enabled := envEnabled
	intervalDays := envIntervalDays
	if intervalDays < 1 {
		intervalDays = 1
	}
	if value, found, err := s.store.GetSetting(ctx, settingBackupEnabled); err != nil {
		s.logger.Error("load backup enabled setting failed", "err", err.Error())
	} else if found {
		switch value {
		case "0":
			enabled = false
		case "1":
			enabled = true
		}
	}
	if value, found, err := s.store.GetSetting(ctx, settingBackupIntervalDays); err != nil {
		s.logger.Error("load backup interval setting failed", "err", err.Error())
	} else if found {
		if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed >= 1 {
			intervalDays = parsed
		}
	}
	s.mu.Lock()
	s.backupEnabled = enabled
	s.backupIntervalDays = intervalDays
	s.mu.Unlock()
}

func (s *Service) backupConfig() (enabled bool, intervalDays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backupEnabled, s.backupIntervalDays
}

// setBackupConfig persists the complete schedule before publishing it to the
// in-memory snapshot. resetCadence makes the next daily run eligible by
// atomically removing the last-success timestamp.
func (s *Service) setBackupConfig(ctx context.Context, enabled bool, intervalDays int, resetCadence bool) error {
	if intervalDays < 1 {
		return fmt.Errorf("backup interval must be positive")
	}
	var deleteKeys []string
	if resetCadence {
		deleteKeys = []string{settingBackupLastAt}
	}
	if err := s.store.UpdateSettings(ctx, map[string]string{
		settingBackupEnabled:      boolSetting(enabled),
		settingBackupIntervalDays: strconv.Itoa(intervalDays),
	}, deleteKeys); err != nil {
		return err
	}
	s.mu.Lock()
	s.backupEnabled = enabled
	s.backupIntervalDays = intervalDays
	s.mu.Unlock()
	return nil
}

// RunScheduledBackup sends a snapshot of the database to every admin's DM when
// the backup feature is on and at least the configured interval has passed
// since the last delivered backup. Runs inside the daily scheduler job.
func (s *Service) RunScheduledBackup(ctx context.Context) {
	enabled, intervalDays := s.backupConfig()
	if !enabled || !s.isEnabled() {
		return
	}
	value, found, err := s.store.GetSetting(ctx, settingBackupLastAt)
	if err != nil {
		s.logger.Error("backup: read last-sent stamp failed", "err", err.Error())
		return
	}
	if found {
		if last, err := time.Parse(time.RFC3339, value); err == nil {
			// Compare whole 24-hour periods without multiplying an unbounded
			// admin-provided day count into time.Duration (which could overflow).
			elapsed := s.now().Sub(last)
			if elapsed < 0 || elapsed/(24*time.Hour) < time.Duration(intervalDays) {
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

// sendBackupSettings renders the admin-facing scheduled-backup configuration.
func (s *Service) sendBackupSettings(ctx context.Context, chatID int64) {
	enabled, intervalDays := s.backupConfig()
	state := i18n.T("выключены")
	toggle := i18n.T("✅ Включить")
	if enabled {
		state = i18n.T("включены")
		toggle = i18n.T("🚫 Выключить")
	}
	label := func(days int) string {
		prefix := ""
		if days == intervalDays {
			prefix = "✅ "
		}
		return fmt.Sprintf("%s%d %s", prefix, days, i18n.PluralDays(days))
	}
	text := fmt.Sprintf(i18n.T("💾 Резервные копии базы данных\n\nСтатус: %s\nИнтервал: каждые %d %s\nВремя запуска: общее расписание RUN_AT."),
		state, intervalDays, i18n.PluralDays(intervalDays))
	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: toggle, CallbackData: "adm:backup:toggle"}},
		{
			{Text: label(1), CallbackData: "adm:backup:days:1"},
			{Text: label(3), CallbackData: "adm:backup:days:3"},
		},
		{
			{Text: label(7), CallbackData: "adm:backup:days:7"},
			{Text: label(30), CallbackData: "adm:backup:days:30"},
		},
		{{Text: i18n.T("✏️ Другой интервал"), CallbackData: "adm:backup:custom"}},
		{{Text: i18n.T("← Меню"), CallbackData: "adm:menu"}},
	}}
	_, _ = s.bot.SendPlainWithKeyboard(ctx, chatID, text, kb)
}

func (s *Service) handleBackupToggle(ctx context.Context, chatID int64) {
	enabled, intervalDays := s.backupConfig()
	newEnabled := !enabled
	if err := s.setBackupConfig(ctx, newEnabled, intervalDays, newEnabled); err != nil {
		s.logger.Error("admin: save backup enabled failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return
	}
	s.sendBackupSettings(ctx, chatID)
}

func (s *Service) handleBackupIntervalPick(ctx context.Context, chatID int64, raw string) {
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 1. Пример: 3"))
		return
	}
	enabled, current := s.backupConfig()
	if err := s.setBackupConfig(ctx, enabled, days, enabled && days != current); err != nil {
		s.logger.Error("admin: save backup interval failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return
	}
	s.sendBackupSettings(ctx, chatID)
}

func (s *Service) startBackupIntervalInput(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputBackupInterval
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите интервал резервного копирования в днях (целое ≥ 1):"))
}

func (s *Service) consumeBackupInterval(ctx context.Context, chatID int64, text string) bool {
	days, err := strconv.Atoi(text)
	if err != nil || days < 1 {
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Введите целое число ≥ 1. Пример: 3"))
		return true
	}
	enabled, current := s.backupConfig()
	if err := s.setBackupConfig(ctx, enabled, days, enabled && days != current); err != nil {
		s.logger.Error("admin: save backup interval failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	s.sendBackupSettings(ctx, chatID)
	return true
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
