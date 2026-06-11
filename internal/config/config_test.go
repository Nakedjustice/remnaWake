package config

import (
	"strings"
	"testing"
)

func TestLoadRequiresRemnawaveAPIToken(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://remnawave.example.com")
	t.Setenv("TELEGRAM_BOT_TOKEN", "telegram-token")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without REMNAWAVE_API_TOKEN")
	}
	if got, want := err.Error(), "REMNAWAVE_API_TOKEN is required"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestLoadUsesRemnawaveAPIToken(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://remnawave.example.com/")
	t.Setenv("REMNAWAVE_API_TOKEN", " api-token ")
	t.Setenv("TELEGRAM_BOT_TOKEN", "telegram-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got, want := cfg.Remnawave.APIToken, "api-token"; got != want {
		t.Fatalf("APIToken = %q, want %q", got, want)
	}
	if got, want := cfg.Remnawave.BaseURL, "https://remnawave.example.com"; got != want {
		t.Fatalf("BaseURL = %q, want %q", got, want)
	}
}

func TestLoadDefaultsDBPathAndCurrency(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("DB_PATH", "")
	t.Setenv("CURRENCY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "/data/bot.db" {
		t.Fatalf("DBPath = %q, want /data/bot.db", cfg.DBPath)
	}
	if cfg.Currency != "₽" {
		t.Fatalf("Currency = %q, want ₽", cfg.Currency)
	}
}

func TestLoadValidatesTelegramAdminID(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://remnawave.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "api-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "telegram-token")
	t.Setenv("TELEGRAM_ADMIN_ID", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded with invalid TELEGRAM_ADMIN_ID")
	}
	if got, want := err.Error(), `invalid TELEGRAM_ADMIN_ID token: "not-a-number"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestLoadAdminIDSingle(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 1 || cfg.Telegram.AdminIDs[0] != 123456 {
		t.Fatalf("AdminIDs = %v, want [123456]", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDMultiple(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "111, 222, 333")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 3 || cfg.Telegram.AdminIDs[1] != 222 {
		t.Fatalf("AdminIDs = %v, want [111 222 333]", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDZeroDisables(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telegram.AdminIDs) != 0 {
		t.Fatalf("AdminIDs = %v, want empty (disabled)", cfg.Telegram.AdminIDs)
	}
}

func TestLoadAdminIDRejectsNegative(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "111,-222")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with a negative TELEGRAM_ADMIN_ID token")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_ADMIN_ID") {
		t.Fatalf("error = %q, want mention of TELEGRAM_ADMIN_ID", err.Error())
	}
}

func TestLoadWinbackDefaults(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Winback.Enabled {
		t.Fatal("Winback should default to enabled")
	}
	if len(cfg.Winback.Days) != 2 || cfg.Winback.Days[0] != 1 || cfg.Winback.Days[1] != 3 {
		t.Fatalf("Winback.Days = %v, want [1 3]", cfg.Winback.Days)
	}
}

func TestLoadWinbackCustomDays(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("WINBACK_DAYS", "2, 7,14")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Winback.Days) != 3 || cfg.Winback.Days[0] != 2 || cfg.Winback.Days[2] != 14 {
		t.Fatalf("Winback.Days = %v, want [2 7 14]", cfg.Winback.Days)
	}
}

func TestLoadWinbackDisabled(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("WINBACK_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Winback.Enabled {
		t.Fatal("Winback should be disabled")
	}
}

func TestLoadWinbackRejectsBadDays(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("WINBACK_DAYS", "1,zero")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with invalid WINBACK_DAYS token")
	}
	if !strings.Contains(err.Error(), "WINBACK_DAYS") {
		t.Fatalf("error = %q, want mention of WINBACK_DAYS", err.Error())
	}
}

func TestLoadAdminIDInvalidToken(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_ADMIN_ID", "111,bad")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with invalid TELEGRAM_ADMIN_ID token")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_ADMIN_ID") {
		t.Fatalf("error = %q, want mention of TELEGRAM_ADMIN_ID", err.Error())
	}
}
