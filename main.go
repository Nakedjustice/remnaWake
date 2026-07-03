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

	"github.com/Nakedjustice/remnaWake/internal/autoupdate"
	"github.com/Nakedjustice/remnaWake/internal/config"
	"github.com/Nakedjustice/remnaWake/internal/forex"
	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/notify"
	"github.com/Nakedjustice/remnaWake/internal/payments"
	"github.com/Nakedjustice/remnaWake/internal/platega"
	"github.com/Nakedjustice/remnaWake/internal/remnawave"
	"github.com/Nakedjustice/remnaWake/internal/scheduler"
	"github.com/Nakedjustice/remnaWake/internal/store"
	tgbot "github.com/Nakedjustice/remnaWake/internal/telegram"
	"github.com/Nakedjustice/remnaWake/internal/webapp"
	"github.com/Nakedjustice/remnaWake/internal/xraychecker"
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

	pay := payments.New(db, bot, rwClient, rwCreator{rwClient}, rwUpdater{rwClient}, rwFinder{rwClient}, rwRegistrar{rwClient}, rwCreator{rwClient}, cfg.Telegram.AdminIDs, cfg.Currency, cfg.DryRun, logger)
	// Live FX rates power infrastructure-cost conversion; best-effort, with
	// admin-entered manual rates as the fallback when the source is unreachable.
	pay.SetForex(forex.NewClient())
	if cfg.Platega.Enabled() {
		method, _ := cfg.Platega.MethodCode() // already validated in config.Load
		plClient := platega.New(cfg.Platega.MerchantID, cfg.Platega.Secret, cfg.HTTP.Timeout)
		pay.SetPlatega(plategaGateway{plClient}, method, cfg.Platega.Currency, cfg.Platega.ReturnURL)
		logger.Info("platega gateway configured", "method", cfg.Platega.Method, "currency", cfg.Platega.Currency)
	}
	if cfg.Stars.Available() {
		pay.SetTelegramStars(cfg.Stars.Rate)
		logger.Info("telegram stars provider configured", "rate", cfg.Stars.Rate)
	}
	// Trial and referral are runtime-toggleable: the env values seed the initial
	// defaults, but a persisted admin override (bot or mini app) wins thereafter.
	pay.InitTrialConfig(payments.TrialConfig{
		Enabled: cfg.Trial.Enabled, Days: cfg.Trial.Days, TrafficLimitGB: cfg.Trial.TrafficLimitGB,
		HwidDeviceLimit: cfg.Trial.HwidDeviceLimit, SquadUUID: cfg.Trial.SquadUUID,
		RequireApproval: cfg.Trial.RequireApproval,
	})
	pay.InitReferral(cfg.Referral.Enabled, cfg.Referral.InviterDays, cfg.Referral.InviteeDays)
	var winbackDays []int
	if cfg.Winback.Enabled {
		winbackDays = cfg.Winback.Days
	}
	svc := notify.NewService(rwClient, bot, pay, db, db, logger, cfg.DryRun, winbackDays)
	// Reminders may only go out around RUN_AT. Otherwise a RUN_ON_START sweep
	// after an off-hour restart pages everyone at the restart time (e.g. 00:09)
	// instead of the configured hour. TZ and RUN_AT are already validated in
	// config.Load, so these parses succeed; on the off chance they don't, the
	// window stays unset (always-send) rather than blocking reminders entirely.
	if loc, err := time.LoadLocation(cfg.Scheduler.Timezone); err != nil {
		logger.Warn("send window not set: bad timezone", "err", err.Error())
	} else if runAt, err := time.Parse("15:04", cfg.Scheduler.RunAt); err != nil {
		logger.Warn("send window not set: bad run_at", "err", err.Error())
	} else {
		svc.SetSendWindow(loc, runAt.Hour(), runAt.Minute(), time.Hour)
	}

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

		if cfg.AutoUpdate.Enabled {
			if cfg.AutoUpdate.WatchtowerConfigured() {
				wt := autoupdate.NewWatchtower(cfg.AutoUpdate.WatchtowerURL, cfg.AutoUpdate.WatchtowerToken)
				pay.SetUpdateTrigger(wt.Trigger)
			}
			checker := autoupdate.NewChecker(
				autoupdate.NewDigestFetcher(), db, pay,
				cfg.AutoUpdate.Image, cfg.AutoUpdate.CheckInterval, logger)
			checker.LoadPersistedInterval(rootCtx)
			pay.SetUpdateChecker(checker)
			logger.Info("autoupdate enabled", "image", cfg.AutoUpdate.Image,
				"interval", checker.Interval().String(),
				"watchtower", cfg.AutoUpdate.WatchtowerConfigured())
			go checker.Run(rootCtx)
		}

		if cfg.XrayChecker.Enabled() {
			xc := xraychecker.NewClient(cfg.XrayChecker.URL, cfg.XrayChecker.Username, cfg.XrayChecker.Password, cfg.HTTP.Timeout)
			pay.SetXrayChecker(xrayCheckerAdapter{xc})
			mon := xraychecker.NewMonitor(xc, db, pay, cfg.XrayChecker.PollInterval, logger)
			logger.Info("xray checker enabled", "url", cfg.XrayChecker.URL,
				"interval", cfg.XrayChecker.PollInterval.String())
			go mon.Run(rootCtx)
		}
		// The dashboard link is independent of metrics polling: admins can open the
		// checker's own web UI even when the bot does not poll its /metrics.
		if cfg.XrayChecker.PublicURL != "" {
			pay.SetCheckerURL(cfg.XrayChecker.PublicURL)
			logger.Info("xray checker dashboard link enabled", "url", cfg.XrayChecker.PublicURL)
		}
	}

	if cfg.WebApp.Enabled() {
		pay.SetWebAppURL(cfg.WebApp.PublicURL)
		if err := bot.SetChatMenuButton(rootCtx, i18n.T("Кабинет"), cfg.WebApp.PublicURL); err != nil {
			logger.Warn("set chat menu button failed", "err", err.Error())
		}
	} else {
		// The menu button persists on Telegram's side; drop a stale Mini App
		// button left over from a run with WEBAPP_URL set.
		if err := bot.ResetChatMenuButton(rootCtx); err != nil {
			logger.Warn("reset chat menu button failed", "err", err.Error())
		}
	}

	// The HTTP server hosts the Mini App and the Platega webhook. Start it when
	// either is enabled; the /platega/callback route is always registered but
	// only acts when Platega is configured.
	if cfg.WebApp.Enabled() || cfg.Platega.Enabled() {
		srv := webapp.NewServer(pay, pay, pay, cfg.Telegram.BotToken, logger)
		go func() {
			if err := srv.Run(rootCtx, cfg.WebApp.Listen); err != nil {
				logger.Error("http server failed", "err", err.Error())
			}
		}()
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
		// Refresh FX rates regardless of dry-run (read-only, best-effort).
		if err := pay.RefreshInfraFxRates(ctx); err != nil {
			logger.Warn("infra fx refresh failed", "err", err.Error())
		}
		if cfg.DryRun {
			logger.Info("infra reminders and gift cleanup skipped (dry run)")
			return
		}
		pay.RunInfraPaymentReminders(ctx)
		pay.RunTrafficExtensionResets(ctx)
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
			if u.PreCheckoutQuery != nil {
				if pay.HandlePreCheckout(ctx, u.PreCheckoutQuery) {
					continue
				}
				logger.Debug("ignored pre-checkout query", "payload", u.PreCheckoutQuery.InvoicePayload)
				continue
			}
			if u.CallbackQuery != nil {
				if pay.HandleCallback(ctx, u.CallbackQuery) {
					continue
				}
				logger.Debug("ignored telegram callback", "data", u.CallbackQuery.Data)
				continue
			}
			if u.Message != nil && u.Message.SuccessfulPayment != nil {
				if pay.HandleSuccessfulPayment(ctx, u.Message) {
					continue
				}
			}
			if u.Message != nil && len(u.Message.Photo) > 0 {
				if pay.HandlePhoto(ctx, u.Message) {
					continue
				}
			}
			if u.Message != nil && u.Message.Document != nil {
				if pay.HandleDocument(ctx, u.Message) {
					continue
				}
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
					if id, ok := strings.CutPrefix(payload, "ref_"); ok && id != "" {
						logger.Info("received referral deep link", "chat_id", u.Message.Chat.ID)
						pay.StartReferral(ctx, u.Message.Chat.ID, id)
						// Fall through to the normal welcome + reply keyboard so the
						// referred user still sees the start screen and trial offer.
					}
					logger.Info("received /start command", "chat_id", u.Message.Chat.ID)
					if err := bot.SendWelcome(ctx, u.Message.Chat.ID, pay.TrialOffered()); err != nil {
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
				case "/trial":
					if pay.StartTrialFlow(ctx, u.Message) {
						continue
					}
				case "/support":
					if pay.StartSupport(ctx, u.Message.Chat.ID) {
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
		tgbot.BotCommand{Command: "checkupdates", Description: i18n.T("Проверить обновления")},
		tgbot.BotCommand{Command: "setupdateinterval", Description: i18n.T("Интервал проверки обновлений")},
	)
}

// userBotCommands is the command menu shown to users (the "Menu" button and the
// "/" autocomplete list).
func userBotCommands() []tgbot.BotCommand {
	return []tgbot.BotCommand{
		{Command: "me", Description: i18n.T("Личный кабинет")},
		{Command: "menu", Description: i18n.T("Открыть меню")},
		{Command: "trial", Description: i18n.T("Активировать пробный период")},
		{Command: "tariff", Description: i18n.T("Посмотреть тарифы")},
		{Command: "gift", Description: i18n.T("Подарить подписку")},
		{Command: "mygifts", Description: i18n.T("Мои подарочные подписки")},
		{Command: "invite", Description: i18n.T("Пригласить нового пользователя")},
		{Command: "register", Description: i18n.T("Привязать свой Telegram к профилю")},
		{Command: "support", Description: i18n.T("Связаться с поддержкой")},
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

func (f rwCreator) CreateUser(ctx context.Context, spec payments.CreateUserSpec) (*payments.CreatedUser, error) {
	u, err := f.c.CreateUser(ctx, remnawave.CreateUserSpec{
		Username:             spec.Username,
		ExpireAt:             spec.ExpireAt,
		SquadUUIDs:           spec.SquadUUIDs,
		TrafficLimitBytes:    spec.TrafficLimitBytes,
		TrafficLimitStrategy: spec.TrafficLimitStrategy,
		HwidDeviceLimit:      spec.HwidDeviceLimit,
		Tag:                  spec.Tag,
	})
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

// rwUpdater adapts *remnawave.Client to payments.UserUpdater, converting the
// payments-local UserPatch to remnawave.UserPatch.
type rwUpdater struct{ c *remnawave.Client }

func (u rwUpdater) UpdateUser(ctx context.Context, uuid string, patch payments.UserPatch) error {
	return u.c.UpdateUser(ctx, uuid, remnawave.UserPatch{
		ExpireAt:             patch.ExpireAt,
		HwidDeviceLimit:      patch.HwidDeviceLimit,
		TrafficLimitBytes:    patch.TrafficLimitBytes,
		TrafficLimitStrategy: patch.TrafficLimitStrategy,
		Status:               patch.Status,
		ActiveInternalSquads: patch.ActiveInternalSquads,
		Tag:                  patch.Tag,
	})
}

// plategaGateway adapts *platega.Client to payments.PlategaGateway, flattening
// the Transaction struct into the primitive returns the payments package wants.
type plategaGateway struct{ c *platega.Client }

func (g plategaGateway) CreateTransaction(ctx context.Context, method int, amount float64, currency, desc, returnURL, payload string) (string, string, error) {
	tx, err := g.c.CreateTransaction(ctx, method, amount, currency, desc, returnURL, payload)
	if err != nil {
		return "", "", err
	}
	return tx.ID, tx.Redirect, nil
}

func (g plategaGateway) GetTransaction(ctx context.Context, id string) (string, error) {
	tx, err := g.c.GetTransaction(ctx, id)
	if err != nil {
		return "", err
	}
	return tx.Status, nil
}

// xrayCheckerAdapter adapts *xraychecker.Client to payments.XrayChecker,
// converting xraychecker.ProxyStatus to the payments-local ProxyStatus so the
// payments package stays decoupled from the xraychecker package.
type xrayCheckerAdapter struct{ c *xraychecker.Client }

func (a xrayCheckerAdapter) Status(ctx context.Context) ([]payments.ProxyStatus, error) {
	statuses, err := a.c.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]payments.ProxyStatus, 0, len(statuses))
	for _, p := range statuses {
		out = append(out, payments.ProxyStatus{
			Name: p.Name, Protocol: p.Protocol, Address: p.Address, SubName: p.SubName,
			Up: p.Up, LatencyMs: p.LatencyMs,
		})
	}
	return out, nil
}

func toSubscriber(u remnawave.User) payments.Subscriber {
	var tgID int64
	if u.TelegramID != nil {
		tgID = *u.TelegramID
	}
	squadUUIDs := make([]string, 0, len(u.ActiveInternalSquads))
	squadNames := make([]string, 0, len(u.ActiveInternalSquads))
	for _, sq := range u.ActiveInternalSquads {
		squadUUIDs = append(squadUUIDs, sq.UUID)
		squadNames = append(squadNames, sq.Name)
	}
	return payments.Subscriber{
		RemnawaveID:          u.ID,
		UUID:                 u.UUID,
		Username:             u.Username,
		TelegramID:           tgID,
		ExpireAt:             u.ExpireAt,
		Status:               string(u.Status),
		SubscriptionURL:      u.SubscriptionURL,
		HwidDeviceLimit:      u.HwidDeviceLimit,
		TrafficLimitBytes:    u.TrafficLimitBytes,
		UsedTrafficBytes:     u.UserTraffic.UsedTrafficBytes,
		TrafficLimitStrategy: u.TrafficLimitStrategy,
		SquadUUIDs:           squadUUIDs,
		SquadNames:           squadNames,
	}
}
