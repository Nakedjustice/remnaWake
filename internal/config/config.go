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
)

type Config struct {
	Remnawave  RemnawaveConfig
	Telegram   TelegramConfig
	Scheduler  SchedulerConfig
	HTTP       HTTPConfig
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

func Load() (*Config, error) {
	adminIDs, err := parseAdminIDs(os.Getenv("TELEGRAM_ADMIN_ID"))
	if err != nil {
		return nil, err
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
	if _, err := time.Parse("15:04", c.Scheduler.RunAt); err != nil {
		return fmt.Errorf("invalid RUN_AT (expected HH:MM): %q", c.Scheduler.RunAt)
	}
	if _, err := time.LoadLocation(c.Scheduler.Timezone); err != nil {
		return fmt.Errorf("invalid TZ: %q", c.Scheduler.Timezone)
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
