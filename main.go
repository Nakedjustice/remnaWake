package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/config"
	"github.com/Nakedjustice/remnaWake/internal/notify"
	"github.com/Nakedjustice/remnaWake/internal/payments"
	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/scheduler"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func main() {
	bootLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		bootLogger.Error("config load failed", "err", err.Error())
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	logger.Info("config loaded",
		"remnawave_host", redactURL(cfg.Remnawave.BaseURL),
		"telegram_endpoint", "api.telegram.org",
		"timezone", cfg.Scheduler.Timezone,
		"run_at", cfg.Scheduler.RunAt,
		"dry_run", cfg.DryRun,
		"run_on_start", cfg.RunOnStart,
		"payment_notifications_enabled", cfg.Telegram.AdminID != 0,
	)

	rwClient, err := remnawave.NewClient(cfg.Remnawave.BaseURL, cfg.Remnawave.APIToken, cfg.HTTP.Timeout)
	if err != nil {
		logger.Error("remnawave client init failed", "err", err.Error())
		os.Exit(1)
	}

	bot := tgbot.NewBot(cfg.Telegram.BotToken, cfg.Telegram.ParseMode, cfg.HTTP.Timeout)

	db, err := store.New(cfg.DBPath)
	if err != nil {
		logger.Error("store init failed", "err", err.Error(), "db_path", cfg.DBPath)
		os.Exit(1)
	}
	defer db.Close()

	pay := payments.New(db, bot, rwClient, cfg.Telegram.AdminID, cfg.Currency, cfg.DryRun, logger)
	svc := notify.NewService(rwClient, bot, pay, logger, cfg.DryRun)

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.Telegram.AdminID != 0 {
		go pollTelegramCallbacks(rootCtx, bot, pay, logger)
	}

	if cfg.RunOnStart {
		go func() {
			runCtx, runCancel := context.WithTimeout(rootCtx, 5*time.Minute)
			defer runCancel()
			svc.Run(runCtx)
		}()
	}

	job := scheduler.New(func(ctx context.Context) { _ = svc.Run(ctx) }, logger, cfg.Scheduler.Timezone, cfg.Scheduler.RunAt)
	sched, err := job.Start(rootCtx)
	if err != nil {
		logger.Error("scheduler start failed", "err", err.Error())
		os.Exit(1)
	}
	defer sched.Shutdown()

	<-rootCtx.Done()
	logger.Info("shutdown signal received, exiting")
}

func redactURL(raw string) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > 40 {
		return raw[:37] + "..."
	}
	return raw
}

func pollTelegramCallbacks(ctx context.Context, bot *tgbot.Bot, pay *payments.Service, logger *slog.Logger) {
	logger.Info("telegram callback polling started")
	defer logger.Info("telegram callback polling stopped")

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := bot.GetUpdates(ctx, offset, 10)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("telegram get updates failed", "err", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for i := range updates {
			u := updates[i]
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.CallbackQuery != nil {
				if pay.HandleCallback(ctx, u.CallbackQuery) {
					continue
				}
				logger.Debug("ignored telegram callback", "data", u.CallbackQuery.Data)
				continue
			}
			if u.Message != nil && u.Message.Text != "" {
				if strings.TrimSpace(u.Message.Text) == "/start" {
					logger.Info("received /start command", "chat_id", u.Message.Chat.ID)
					if err := bot.SendWelcome(ctx, u.Message.Chat.ID); err != nil {
						logger.Error("send welcome message failed", "err", err.Error(), "chat_id", u.Message.Chat.ID)
					}
					continue
				}
				if pay.HandleAdminCommand(ctx, u.Message) {
					continue
				}
			}
		}
	}
}
