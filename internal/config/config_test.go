package config

import "testing"

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
	if got, want := err.Error(), `invalid TELEGRAM_ADMIN_ID: "not-a-number"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
