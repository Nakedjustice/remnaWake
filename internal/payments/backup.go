package payments

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

const (
	// maxBackupIntervalDays bounds the admin-entered cadence. Without a ceiling a
	// typo ("30000") silently parks scheduled backups for decades with no warning.
	maxBackupIntervalDays = 365
	// backupIntervalSlack absorbs the gap between two RUN_AT firings being slightly
	// under a whole number of days in absolute time, which would otherwise push
	// every backup out by one extra day. Two things shrink it: the last-success
	// stamp is written after the sweep, the FX refresh, the reminders and every
	// admin upload have finished, and the scheduler fires at a fixed *local* time,
	// so a DST spring-forward makes consecutive firings only 23 hours apart. The
	// value therefore has to clear one hour of DST plus the job's own runtime, and
	// stays far below 24h so a second run on the same day is still suppressed.
	backupIntervalSlack = 2 * time.Hour
)

// maxBackupUploadBytes is the Telegram Bot API ceiling for bot file uploads;
// a var so tests can lower it.
var maxBackupUploadBytes = 50 * 1024 * 1024

var (
	// ErrBackupDryRun reports that manual delivery is intentionally disabled.
	ErrBackupDryRun = errors.New("backup is disabled in dry-run mode")
	// ErrBackupTooLarge reports that the final upload exceeds Telegram's limit.
	ErrBackupTooLarge = errors.New("backup exceeds the Telegram upload limit")
	// ErrBackupCreate reports a snapshot or ZIP creation failure.
	ErrBackupCreate = errors.New("backup creation failed")
	// ErrBackupDelivery reports that Telegram rejected the document upload.
	ErrBackupDelivery = errors.New("backup delivery failed")
)

// clampBackupIntervalDays folds any interval — an environment value, a value
// persisted before the ceiling existed, or admin input — into the supported
// range. Every writer of s.backupIntervalDays goes through it, which is what
// lets RunScheduledBackup multiply the day count into a time.Duration safely.
func clampBackupIntervalDays(days int) int {
	if days < 1 {
		return 1
	}
	if days > maxBackupIntervalDays {
		return maxBackupIntervalDays
	}
	return days
}

// InitBackupConfig loads persisted admin settings over deployment-provided
// defaults. It is called once at startup before callback polling begins.
func (s *Service) InitBackupConfig(envEnabled bool, envIntervalDays int) {
	ctx := context.Background()
	enabled := envEnabled
	intervalDays := clampBackupIntervalDays(envIntervalDays)
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
			intervalDays = clampBackupIntervalDays(parsed)
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
	if intervalDays < 1 || intervalDays > maxBackupIntervalDays {
		return fmt.Errorf("backup interval must be between 1 and %d days", maxBackupIntervalDays)
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
			// intervalDays is clamped to maxBackupIntervalDays on every write, so
			// this multiplication cannot overflow time.Duration.
			due := time.Duration(intervalDays)*24*time.Hour - backupIntervalSlack
			if elapsed := s.now().Sub(last); elapsed < due {
				return
			}
		}
	}
	if s.dryRun {
		s.logger.Info("dry-run: would send database backup to admins")
		return
	}

	filename, data, err := s.buildBackupDocument(ctx)
	if err == nil && len(data) > maxBackupUploadBytes {
		err = ErrBackupTooLarge
	}
	if err != nil {
		if errors.Is(err, ErrBackupTooLarge) {
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

// applyBackupInterval parses, validates and persists an admin-supplied cadence,
// then re-renders the settings card. Shared by the preset buttons and the
// free-text prompt so both paths keep identical rules and messages. Returns
// false when the input was rejected and the admin should try again.
func (s *Service) applyBackupInterval(ctx context.Context, chatID int64, raw string) bool {
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || days < 1 || days > maxBackupIntervalDays {
		_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
			i18n.T("Введите целое число от 1 до %d. Пример: 3"), maxBackupIntervalDays))
		return false
	}
	// Changing the cadence of an active schedule restarts it from today, so the
	// admin sees the new interval take effect from the next daily run.
	enabled, current := s.backupConfig()
	if err := s.setBackupConfig(ctx, enabled, days, enabled && days != current); err != nil {
		s.logger.Error("admin: save backup interval failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Ошибка сохранения настройки."))
		return false
	}
	s.sendBackupSettings(ctx, chatID)
	return true
}

func (s *Service) startBackupIntervalInput(ctx context.Context, chatID int64) {
	s.mu.Lock()
	state := s.adminInput[chatID]
	state.step = adminInputBackupInterval
	s.adminInput[chatID] = state
	s.mu.Unlock()
	_ = s.bot.SendPlain(ctx, chatID, fmt.Sprintf(
		i18n.T("Введите интервал резервного копирования в днях (целое от 1 до %d):"), maxBackupIntervalDays))
}

func (s *Service) consumeBackupInterval(ctx context.Context, chatID int64, text string) bool {
	if !s.applyBackupInterval(ctx, chatID, text) {
		// Keep the input step so the admin can correct the value in place.
		return true
	}
	s.mu.Lock()
	delete(s.adminInput, chatID)
	s.mu.Unlock()
	return true
}

// cmdBackup handles the /backup admin command: an immediate one-off backup
// sent only to the requesting admin, independent of the schedule and stamp.
func (s *Service) cmdBackup(ctx context.Context, chatID int64) {
	err := s.AdminSendBackup(ctx, chatID)
	switch {
	case err == nil:
		return
	case errors.Is(err, ErrBackupDryRun):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Резервная копия не отправлена (dry-run)."))
	case errors.Is(err, ErrBackupTooLarge):
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("⚠️ Резервная копия базы данных больше 50 МБ и не может быть отправлена в Telegram. Сделайте бэкап на сервере вручную."))
	case errors.Is(err, ErrBackupDelivery):
		s.logger.Error("backup: send failed", "admin", chatID, "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось отправить резервную копию."))
	default:
		s.logger.Error("backup: build failed", "err", err.Error())
		_ = s.bot.SendPlain(ctx, chatID, i18n.T("Не удалось создать резервную копию."))
	}
}

// AdminSendBackup creates a one-off ZIP archive and sends it only to the
// requesting admin's Telegram chat. It is shared by /backup, the Telegram
// admin-menu callback, and the authenticated Mini App admin endpoint.
func (s *Service) AdminSendBackup(ctx context.Context, telegramID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if s.dryRun {
		return ErrBackupDryRun
	}
	filename, data, err := s.buildBackupArchive(ctx)
	if err != nil {
		return err
	}
	caption := i18n.T("📦 Резервная копия базы данных бота. Извлеките файл .db из ZIP, затем выполните: ./install.sh restore <файл>.")
	if _, _, err := s.bot.SendDocumentUpload(ctx, telegramID, filename, data, caption, nil); err != nil {
		return fmt.Errorf("%w: %v", ErrBackupDelivery, err)
	}
	return nil
}

func (s *Service) buildBackupArchive(ctx context.Context) (filename string, data []byte, err error) {
	dbFilename, dbData, err := s.buildBackupDocument(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrBackupCreate, err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entry, err := zw.Create(dbFilename)
	if err != nil {
		return "", nil, fmt.Errorf("%w: create zip entry: %v", ErrBackupCreate, err)
	}
	if _, err := entry.Write(dbData); err != nil {
		return "", nil, fmt.Errorf("%w: write zip entry: %v", ErrBackupCreate, err)
	}
	if err := zw.Close(); err != nil {
		return "", nil, fmt.Errorf("%w: close zip: %v", ErrBackupCreate, err)
	}
	if buf.Len() > maxBackupUploadBytes {
		return "", nil, ErrBackupTooLarge
	}
	return strings.TrimSuffix(dbFilename, ".db") + ".zip", buf.Bytes(), nil
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
	filename = "remnawake-backup-" + s.now().Format("2006-01-02-1504") + ".db"
	return filename, data, nil
}
