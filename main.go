package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/config"
	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/notify"
	"github.com/Nakedjustice/remnaWake/internal/payments"
	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/scheduler"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
	"github.com/Nakedjustice/remnaWake/internal/webapp"
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
	i18n.SetLang(cfg.Lang)

	logger.Info("config loaded",
		"remnawave_host", redactURL(cfg.Remnawave.BaseURL),
		"telegram_endpoint", "api.telegram.org",
		"timezone", cfg.Scheduler.Timezone,
		"run_at", cfg.Scheduler.RunAt,
		"dry_run", cfg.DryRun,
		"run_on_start", cfg.RunOnStart,
		"payment_notifications_enabled", len(cfg.Telegram.AdminIDs) > 0,
		"webapp_enabled", cfg.WebApp.Enabled(),
		"winback_enabled", cfg.Winback.Enabled,
		"winback_days", cfg.Winback.Days,
		"lang", string(cfg.Lang),
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

	pay := payments.New(db, bot, rwClient, rwCreator{rwClient}, rwFinder{rwClient}, rwRegistrar{rwClient}, rwCreator{rwClient}, cfg.Telegram.AdminIDs, cfg.Currency, cfg.DryRun, logger)
	var winbackDays []int
	if cfg.Winback.Enabled {
		winbackDays = cfg.Winback.Days
	}
	svc := notify.NewService(rwClient, bot, pay, db, logger, cfg.DryRun, winbackDays)

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if me, err := bot.GetMe(rootCtx); err != nil {
		logger.Warn("getMe failed, gift deep links will fall back to raw codes", "err", err.Error())
	} else {
		pay.SetBotUsername(me.Username)
	}

	if len(cfg.Telegram.AdminIDs) > 0 {
		if err := bot.SetMyCommands(rootCtx, userBotCommands()); err != nil {
			logger.Warn("set bot commands failed", "err", err.Error())
		}
		for _, adminID := range cfg.Telegram.AdminIDs {
			if err := bot.SetMyCommandsForChat(rootCtx, adminID, adminBotCommands()); err != nil {
				logger.Warn("set admin bot commands failed", "err", err.Error(), "admin_id", adminID)
			}
		}
		go pollTelegramCallbacks(rootCtx, bot, pay, logger)
	}

	if cfg.WebApp.Enabled() {
		pay.SetWebAppURL(cfg.WebApp.PublicURL)
		if err := bot.SetChatMenuButton(rootCtx, i18n.T("Кабинет"), cfg.WebApp.PublicURL); err != nil {
			logger.Warn("set chat menu button failed", "err", err.Error())
		}
		srv := webapp.NewServer(pay, pay, cfg.Telegram.BotToken, logger)
		go func() {
			if err := srv.Run(rootCtx, cfg.WebApp.Listen); err != nil {
				logger.Error("mini app server failed", "err", err.Error())
			}
		}()
	} else {
		// The menu button persists on Telegram's side; drop a stale Mini App
		// button left over from a run with WEBAPP_URL set.
		if err := bot.ResetChatMenuButton(rootCtx); err != nil {
			logger.Warn("reset chat menu button failed", "err", err.Error())
		}
	}

	if cfg.RunOnStart {
		go func() {
			runCtx, runCancel := context.WithTimeout(rootCtx, 5*time.Minute)
			defer runCancel()
			svc.Run(runCtx)
		}()
	}

	job := scheduler.New(func(ctx context.Context) {
		_ = svc.Run(ctx)
		if cfg.DryRun {
			logger.Info("gift cleanup skipped (dry run)")
			return
		}
		if n, err := db.DeleteResolvedGiftCodes(ctx); err != nil {
			logger.Error("gift cleanup failed", "err", err.Error())
		} else if n > 0 {
			logger.Info("gift cleanup done", "deleted", n)
		}
	}, logger, cfg.Scheduler.Timezone, cfg.Scheduler.RunAt)
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
				text := strings.TrimSpace(u.Message.Text)
				if text == "/start" || strings.HasPrefix(text, "/start ") {
					payload := strings.TrimSpace(strings.TrimPrefix(text, "/start"))
					if code, ok := strings.CutPrefix(payload, "gift_"); ok && code != "" {
						logger.Info("received gift deep link", "chat_id", u.Message.Chat.ID)
						pay.StartGiftRedemption(ctx, u.Message.Chat.ID, code)
						continue
					}
					logger.Info("received /start command", "chat_id", u.Message.Chat.ID)
					if err := bot.SendWelcome(ctx, u.Message.Chat.ID); err != nil {
						logger.Error("send welcome message failed", "err", err.Error(), "chat_id", u.Message.Chat.ID)
					}
					if err := bot.SendPlainWithReplyKeyboard(ctx, u.Message.Chat.ID,
						fmt.Sprintf(i18n.T("Кнопка «%s» теперь всегда под полем ввода 👇"), tgbot.CabinetButtonLabel()), tgbot.MainReplyKeyboard()); err != nil {
						logger.Error("send reply keyboard failed", "err", err.Error(), "chat_id", u.Message.Chat.ID)
					}
					continue
				}
				if text == "/me" || text == "/cabinet" || tgbot.IsCabinetButton(text) {
					if pay.SendCabinet(ctx, u.Message.Chat.ID) {
						continue
					}
				}
				switch text {
				case "/menu", "/help":
					pay.SendMenu(ctx, u.Message.Chat.ID)
					continue
				case "/tariff":
					pay.SendTariffs(ctx, u.Message.Chat.ID)
					continue
				case "/gift":
					if pay.StartGiftCodeFlow(ctx, u.Message) {
						continue
					}
				case "/mygifts":
					pay.SendMyGifts(ctx, u.Message.Chat.ID)
					continue
				case "/invite":
					if pay.StartInviteFlow(ctx, u.Message) {
						continue
					}
				case "/register":
					if pay.StartRegisterFlow(ctx, u.Message) {
						continue
					}
				}
				if pay.HandleText(ctx, u.Message) {
					continue
				}
				if pay.HandleAdminCommand(ctx, u.Message) {
					continue
				}
			}
		}
	}
}

// adminBotCommands returns the command menu for the admin chat (user commands + /admin).
func adminBotCommands() []tgbot.BotCommand {
	return append(userBotCommands(),
		tgbot.BotCommand{Command: "admin", Description: i18n.T("Панель администратора")},
		tgbot.BotCommand{Command: "stats", Description: i18n.T("Статистика")},
	)
}

// userBotCommands is the command menu shown to users (the "Menu" button and the
// "/" autocomplete list).
func userBotCommands() []tgbot.BotCommand {
	return []tgbot.BotCommand{
		{Command: "me", Description: i18n.T("Личный кабинет")},
		{Command: "menu", Description: i18n.T("Открыть меню")},
		{Command: "tariff", Description: i18n.T("Посмотреть тарифы")},
		{Command: "gift", Description: i18n.T("Подарить подписку")},
		{Command: "mygifts", Description: i18n.T("Мои подарочные подписки")},
		{Command: "invite", Description: i18n.T("Пригласить нового пользователя")},
		{Command: "register", Description: i18n.T("Привязать свой Telegram к профилю")},
		{Command: "cancel", Description: i18n.T("Отменить текущее действие")},
		{Command: "help", Description: i18n.T("Помощь")},
		{Command: "start", Description: i18n.T("О боте")},
	}
}

// rwFinder adapts *remnawave.Client to payments.Finder, converting User -> Subscriber.
type rwFinder struct{ c *remnawave.Client }

func (f rwFinder) FindByTelegramID(ctx context.Context, telegramID int64) ([]payments.Subscriber, error) {
	us, err := f.c.GetUserByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	out := make([]payments.Subscriber, 0, len(us))
	for i := range us {
		out = append(out, toSubscriber(us[i]))
	}
	return out, nil
}

func (f rwFinder) FindByUsername(ctx context.Context, username string) (*payments.Subscriber, error) {
	u, err := f.c.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	s := toSubscriber(*u)
	return &s, nil
}

func (f rwFinder) FindByShortUUID(ctx context.Context, shortUUID string) (*payments.Subscriber, error) {
	u, err := f.c.GetUserByShortUUID(ctx, shortUUID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	s := toSubscriber(*u)
	return &s, nil
}

func (f rwFinder) ListAll(ctx context.Context) ([]payments.Subscriber, error) {
	us, err := f.c.GetUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]payments.Subscriber, 0, len(us))
	for i := range us {
		out = append(out, toSubscriber(us[i]))
	}
	return out, nil
}

// rwCreator adapts *remnawave.Client to payments.Creator and
// payments.SquadLister.
type rwCreator struct{ c *remnawave.Client }

func (f rwCreator) CreateUser(ctx context.Context, username string, expireAt time.Time, squadUUIDs []string) (*payments.CreatedUser, error) {
	u, err := f.c.CreateUser(ctx, username, expireAt, squadUUIDs)
	if err != nil {
		return nil, err
	}
	return &payments.CreatedUser{UUID: u.UUID, Username: u.Username, SubscriptionURL: u.SubscriptionURL}, nil
}

func (f rwCreator) GetInternalSquads(ctx context.Context) ([]payments.InternalSquad, error) {
	squads, err := f.c.GetInternalSquads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]payments.InternalSquad, 0, len(squads))
	for _, sq := range squads {
		out = append(out, payments.InternalSquad{UUID: sq.UUID, Name: sq.Name})
	}
	return out, nil
}

// rwRegistrar adapts *remnawave.Client to payments.Registrar.
type rwRegistrar struct{ c *remnawave.Client }

func (r rwRegistrar) SetTelegramID(ctx context.Context, uuid string, telegramID int64) error {
	return r.c.SetTelegramID(ctx, uuid, telegramID)
}

func toSubscriber(u remnawave.User) payments.Subscriber {
	var tgID int64
	if u.TelegramID != nil {
		tgID = *u.TelegramID
	}
	return payments.Subscriber{
		RemnawaveID:     u.ID,
		UUID:            u.UUID,
		Username:        u.Username,
		TelegramID:      tgID,
		ExpireAt:        u.ExpireAt,
		Status:          string(u.Status),
		SubscriptionURL: u.SubscriptionURL,
	}
}
