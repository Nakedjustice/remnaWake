package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
)

type Config struct {
	Remnawave  RemnawaveConfig
	Telegram   TelegramConfig
	Scheduler  SchedulerConfig
	HTTP       HTTPConfig
	WebApp     WebAppConfig
	Winback    WinbackConfig
	Platega    PlategaConfig
	Stars      StarsConfig
	Trial      TrialConfig
	Referral   ReferralConfig
	AutoUpdate AutoUpdateConfig
	Lang       i18n.Lang
	LogLevel   slog.Level
	DryRun     bool
	RunOnStart bool
	DBPath     string
	Currency   string
}

type RemnawaveConfig struct {
	BaseURL  string
	APIToken string
}

type TelegramConfig struct {
	BotToken  string
	ParseMode string
	AdminIDs  []int64
}

type SchedulerConfig struct {
	Timezone string
	RunAt    string
}

type HTTPConfig struct {
	Timeout time.Duration
}

// WebAppConfig configures the embedded Telegram Mini App server. The mini app
// is enabled only when PublicURL is set; Listen is the local bind address that
// a reverse proxy (providing HTTPS at PublicURL) forwards to.
type WebAppConfig struct {
	PublicURL string
	Listen    string
}

// Enabled reports whether the mini app server should be started.
func (w WebAppConfig) Enabled() bool {
	return w.PublicURL != ""
}

// WinbackConfig configures post-expiry win-back notifications: a "subscription
// expired" message sent the given number of days after the expiry date.
type WinbackConfig struct {
	Enabled bool
	Days    []int
}

// PlategaConfig configures the optional Platega payment gateway. Credentials
// come from the environment (like the Remnawave token); whether Platega is the
// *active* provider is an admin runtime toggle, not config. Platega is only
// available when both MerchantID and Secret are set.
type PlategaConfig struct {
	MerchantID string
	Secret     string
	Method     string // "sbp" or "card"
	Currency   string // ISO code for the API (e.g. "RUB"); distinct from the CURRENCY label
	ReturnURL  string // where Platega returns the user after payment
}

// Enabled reports whether Platega credentials are configured.
func (p PlategaConfig) Enabled() bool {
	return p.MerchantID != "" && p.Secret != ""
}

// StarsConfig configures the optional Telegram Stars (XTR) payment provider.
// Stars uses the bot's own token, so the only configuration is the on/off flag
// and the conversion rate from the tariff price (in the CURRENCY unit) to Stars.
// Whether Stars is an *enabled* provider is an admin runtime toggle, not config.
type StarsConfig struct {
	Enabled bool
	// Rate is how many price units (e.g. RUB) equal one Star. The Stars amount
	// for a tariff is ceil(price / Rate).
	Rate int
}

// Available reports whether the Telegram Stars provider may be offered.
func (s StarsConfig) Available() bool {
	return s.Enabled && s.Rate > 0
}

// TrialConfig configures the optional one-time free trial. When Enabled, a new
// user (one with no linked profile) can create a trial profile lasting Days
// days, once per Telegram ID.
type TrialConfig struct {
	Enabled         bool
	Days            int
	TrafficLimitGB  int
	HwidDeviceLimit int
	SquadUUID       string
}

// ReferralConfig configures the optional invite-referral bonus. When Enabled, an
// approved invite grants the inviter InviterDays bonus days on their own
// subscription and the invitee InviteeDays extra days on top of the invite term.
type ReferralConfig struct {
	Enabled     bool
	InviterDays int
	InviteeDays int
}

// AutoUpdateConfig configures the optional update checker: the bot polls the
// configured image's registry manifest digest and DMs admins when it changes.
// When a Watchtower sidecar is wired in (WatchtowerURL set), the admin can apply
// the update with one tap; otherwise the notification carries manual instructions.
type AutoUpdateConfig struct {
	Enabled         bool
	Image           string        // image ref watched for new digests (ghcr.io/<repo>:<tag>)
	CheckInterval   time.Duration // how often to poll the registry
	WatchtowerURL   string        // base URL of the Watchtower HTTP API; empty = notify-only
	WatchtowerToken string        // bearer token for the Watchtower HTTP API
}

// WatchtowerConfigured reports whether one-tap install is available.
func (a AutoUpdateConfig) WatchtowerConfigured() bool {
	return a.WatchtowerURL != ""
}

// MethodCode maps the configured Method to a Platega payment-method code.
func (p PlategaConfig) MethodCode() (int, error) {
	switch strings.ToLower(strings.TrimSpace(p.Method)) {
	case "", "sbp":
		return 2, nil // platega.MethodSBP (СБП/QR)
	case "card", "cards":
		return 11, nil // platega.MethodCards (карточный эквайринг)
	default:
		return 0, fmt.Errorf("invalid PLATEGA_METHOD: %q (supported: sbp, card)", p.Method)
	}
}

func Load() (*Config, error) {
	adminIDs, err := parseAdminIDs(os.Getenv("TELEGRAM_ADMIN_ID"))
	if err != nil {
		return nil, err
	}

	winbackDays, err := parseDaysList("WINBACK_DAYS", getenv("WINBACK_DAYS", "1,3"))
	if err != nil {
		return nil, err
	}

	lang, ok := i18n.Parse(getenv("BOT_LANG", "ru"))
	if !ok {
		return nil, fmt.Errorf("invalid BOT_LANG: %q (supported: ru, en)", os.Getenv("BOT_LANG"))
	}

	cfg := &Config{
		Remnawave: RemnawaveConfig{
			BaseURL:  strings.TrimRight(os.Getenv("REMNAWAVE_BASE_URL"), "/"),
			APIToken: strings.TrimSpace(os.Getenv("REMNAWAVE_API_TOKEN")),
		},
		Telegram: TelegramConfig{
			BotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
			ParseMode: getenv("TELEGRAM_PARSE_MODE", "HTML"),
			AdminIDs:  adminIDs,
		},
		Scheduler: SchedulerConfig{
			Timezone: getenv("TZ", "Europe/Moscow"),
			RunAt:    getenv("RUN_AT", "09:00"),
		},
		WebApp: WebAppConfig{
			PublicURL: strings.TrimRight(strings.TrimSpace(os.Getenv("WEBAPP_URL")), "/"),
			Listen:    getenv("WEBAPP_LISTEN", ":8080"),
		},
		Winback: WinbackConfig{
			Enabled: getenvBool("WINBACK_ENABLED", true),
			Days:    winbackDays,
		},
		Platega: PlategaConfig{
			MerchantID: strings.TrimSpace(os.Getenv("PLATEGA_MERCHANT_ID")),
			Secret:     strings.TrimSpace(os.Getenv("PLATEGA_SECRET")),
			Method:     getenv("PLATEGA_METHOD", "sbp"),
			Currency:   getenv("PLATEGA_CURRENCY", "RUB"),
			ReturnURL:  strings.TrimRight(strings.TrimSpace(getenv("PLATEGA_RETURN_URL", "https://t.me")), "/"),
		},
		Stars: StarsConfig{
			Enabled: getenvBool("TELEGRAM_STARS_ENABLED", false),
		},
		Trial: TrialConfig{
			Enabled:         getenvBool("TRIAL_ENABLED", false),
			Days:            getenvInt("TRIAL_DAYS", 3),
			TrafficLimitGB:  getenvInt("TRIAL_TRAFFIC_LIMIT_GB", 10),
			HwidDeviceLimit: getenvInt("TRIAL_HWID_DEVICE_LIMIT", 1),
			SquadUUID:       strings.TrimSpace(os.Getenv("TRIAL_SQUAD_UUID")),
		},
		Referral: ReferralConfig{
			Enabled:     getenvBool("REFERRAL_ENABLED", false),
			InviterDays: getenvInt("REFERRAL_INVITER_BONUS_DAYS", 30),
			InviteeDays: getenvInt("REFERRAL_INVITEE_BONUS_DAYS", 0),
		},
		AutoUpdate: AutoUpdateConfig{
			Enabled:         getenvBool("AUTOUPDATE_ENABLED", false),
			Image:           strings.TrimSpace(getenv("AUTOUPDATE_IMAGE", "ghcr.io/nakedjustice/remnawave:main")),
			WatchtowerURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("WATCHTOWER_URL")), "/"),
			WatchtowerToken: strings.TrimSpace(os.Getenv("WATCHTOWER_TOKEN")),
		},
		Lang:       lang,
		LogLevel:   parseLogLevel(getenv("LOG_LEVEL", "info")),
		DryRun:     getenvBool("DRY_RUN", false),
		RunOnStart: getenvBool("RUN_ON_START", true),
		DBPath:     getenv("DB_PATH", "/data/bot.db"),
		Currency:   getenv("CURRENCY", "₽"),
	}

	timeout := getenv("HTTP_TIMEOUT", "15s")
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP_TIMEOUT: %w", err)
	}
	cfg.HTTP.Timeout = d

	interval := getenv("AUTOUPDATE_CHECK_INTERVAL", "6h")
	iv, err := time.ParseDuration(interval)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTOUPDATE_CHECK_INTERVAL: %w", err)
	}
	cfg.AutoUpdate.CheckInterval = iv

	if rate := strings.TrimSpace(os.Getenv("TELEGRAM_STARS_RATE")); rate != "" {
		r, err := strconv.Atoi(rate)
		if err != nil {
			return nil, fmt.Errorf("invalid TELEGRAM_STARS_RATE: %q (expected a positive integer)", rate)
		}
		cfg.Stars.Rate = r
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Remnawave.BaseURL == "" {
		return errors.New("REMNAWAVE_BASE_URL is required")
	}
	if u, err := url.Parse(c.Remnawave.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid REMNAWAVE_BASE_URL: %q", c.Remnawave.BaseURL)
	}
	if c.Remnawave.APIToken == "" {
		return errors.New("REMNAWAVE_API_TOKEN is required")
	}
	if c.Telegram.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	if c.WebApp.Enabled() {
		u, err := url.Parse(c.WebApp.PublicURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("invalid WEBAPP_URL (Telegram Mini Apps require an https URL): %q", c.WebApp.PublicURL)
		}
	}
	if _, err := time.Parse("15:04", c.Scheduler.RunAt); err != nil {
		return fmt.Errorf("invalid RUN_AT (expected HH:MM): %q", c.Scheduler.RunAt)
	}
	if c.Winback.Enabled && len(c.Winback.Days) == 0 {
		return errors.New("WINBACK_DAYS must list at least one day when WINBACK_ENABLED is true")
	}
	if _, err := time.LoadLocation(c.Scheduler.Timezone); err != nil {
		return fmt.Errorf("invalid TZ: %q", c.Scheduler.Timezone)
	}
	if c.Platega.Enabled() {
		if _, err := c.Platega.MethodCode(); err != nil {
			return err
		}
	}
	if c.Stars.Enabled && c.Stars.Rate <= 0 {
		return errors.New("TELEGRAM_STARS_RATE must be a positive integer when TELEGRAM_STARS_ENABLED is true")
	}
	if c.Trial.Enabled && c.Trial.Days < 1 {
		return errors.New("TRIAL_DAYS must be a positive integer when TRIAL_ENABLED is true")
	}
	if c.Trial.TrafficLimitGB < 0 {
		return errors.New("TRIAL_TRAFFIC_LIMIT_GB must be non-negative")
	}
	if c.Trial.HwidDeviceLimit < 0 {
		return errors.New("TRIAL_HWID_DEVICE_LIMIT must be non-negative")
	}
	if c.Referral.Enabled {
		if c.Referral.InviterDays < 0 || c.Referral.InviteeDays < 0 {
			return errors.New("REFERRAL_INVITER_BONUS_DAYS and REFERRAL_INVITEE_BONUS_DAYS must be non-negative")
		}
		if c.Referral.InviterDays == 0 && c.Referral.InviteeDays == 0 {
			return errors.New("at least one of REFERRAL_INVITER_BONUS_DAYS / REFERRAL_INVITEE_BONUS_DAYS must be positive when REFERRAL_ENABLED is true")
		}
	}
	if c.AutoUpdate.Enabled {
		if c.AutoUpdate.CheckInterval <= 0 {
			return fmt.Errorf("AUTOUPDATE_CHECK_INTERVAL must be positive: %v", c.AutoUpdate.CheckInterval)
		}
		if c.AutoUpdate.Image == "" {
			return errors.New("AUTOUPDATE_IMAGE is required when AUTOUPDATE_ENABLED is true")
		}
	}
	return nil
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// getenvInt reads an integer env var, falling back to def when unset, empty or
// unparseable.
func getenvInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getenvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// parseDaysList parses a comma-separated list of positive day numbers
// (e.g. "1,3"); name is the env var used in error messages.
func parseDaysList(name, raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	tokens := strings.Split(raw, ",")
	out := make([]int, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid %s token: %q", name, t)
		}
		out = append(out, n)
	}
	return out, nil
}

func parseAdminIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, nil
	}
	tokens := strings.Split(raw, ",")
	out := make([]int64, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" || t == "0" {
			continue
		}
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid TELEGRAM_ADMIN_ID token: %q", t)
		}
		out = append(out, n)
	}
	return out, nil
}
