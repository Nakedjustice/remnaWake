package config

import (
	"strings"
	"testing"
	"time"
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

func TestLoadPlategaDisabledByDefault(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Platega.Enabled() {
		t.Fatal("Platega should be disabled without merchant id/secret")
	}
}

func TestLoadPlategaEnabledWithDefaults(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("PLATEGA_MERCHANT_ID", " m1 ")
	t.Setenv("PLATEGA_SECRET", " s1 ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Platega.Enabled() {
		t.Fatal("Platega should be enabled with merchant id + secret")
	}
	if cfg.Platega.MerchantID != "m1" || cfg.Platega.Secret != "s1" {
		t.Fatalf("creds = %q/%q, want m1/s1 (trimmed)", cfg.Platega.MerchantID, cfg.Platega.Secret)
	}
	if cfg.Platega.Currency != "RUB" {
		t.Fatalf("Currency = %q, want RUB", cfg.Platega.Currency)
	}
	if cfg.Platega.ReturnURL != "https://t.me" {
		t.Fatalf("ReturnURL = %q, want https://t.me", cfg.Platega.ReturnURL)
	}
	if code, err := cfg.Platega.MethodCode(); err != nil || code != 2 {
		t.Fatalf("MethodCode = %d, %v; want 2 (sbp), nil", code, err)
	}
}

func TestLoadPlategaCardMethod(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("PLATEGA_MERCHANT_ID", "m1")
	t.Setenv("PLATEGA_SECRET", "s1")
	t.Setenv("PLATEGA_METHOD", "card")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if code, err := cfg.Platega.MethodCode(); err != nil || code != 11 {
		t.Fatalf("MethodCode = %d, %v; want 11 (cards), nil", code, err)
	}
}

func TestLoadPlategaRejectsBadMethod(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("PLATEGA_MERCHANT_ID", "m1")
	t.Setenv("PLATEGA_SECRET", "s1")
	t.Setenv("PLATEGA_METHOD", "bitcoin")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with an unknown PLATEGA_METHOD")
	}
	if !strings.Contains(err.Error(), "PLATEGA_METHOD") {
		t.Fatalf("error = %q, want mention of PLATEGA_METHOD", err.Error())
	}
}

func TestLoadAutoUpdateDisabledByDefault(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AutoUpdate.Enabled {
		t.Fatal("AutoUpdate should be disabled by default")
	}
	// Defaults are still populated so they're ready when enabled.
	if cfg.AutoUpdate.Image != "ghcr.io/nakedjustice/remnawake:main" {
		t.Fatalf("Image = %q, want ghcr.io/nakedjustice/remnawake:main", cfg.AutoUpdate.Image)
	}
	if cfg.AutoUpdate.CheckInterval != 6*time.Hour {
		t.Fatalf("CheckInterval = %v, want 6h", cfg.AutoUpdate.CheckInterval)
	}
	if cfg.AutoUpdate.WatchtowerConfigured() {
		t.Fatal("Watchtower should be unconfigured without WATCHTOWER_URL")
	}
}

func TestLoadAutoUpdateEnabled(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("AUTOUPDATE_ENABLED", "true")
	t.Setenv("AUTOUPDATE_CHECK_INTERVAL", "30m")
	t.Setenv("WATCHTOWER_URL", "http://watchtower:8080/")
	t.Setenv("WATCHTOWER_TOKEN", " secret ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AutoUpdate.Enabled {
		t.Fatal("AutoUpdate should be enabled")
	}
	if cfg.AutoUpdate.CheckInterval != 30*time.Minute {
		t.Fatalf("CheckInterval = %v, want 30m", cfg.AutoUpdate.CheckInterval)
	}
	if cfg.AutoUpdate.WatchtowerURL != "http://watchtower:8080" {
		t.Fatalf("WatchtowerURL = %q, want trimmed without trailing slash", cfg.AutoUpdate.WatchtowerURL)
	}
	if cfg.AutoUpdate.WatchtowerToken != "secret" {
		t.Fatalf("WatchtowerToken = %q, want trimmed", cfg.AutoUpdate.WatchtowerToken)
	}
	if !cfg.AutoUpdate.WatchtowerConfigured() {
		t.Fatal("Watchtower should be configured")
	}
}

func TestLoadAutoUpdateRejectsBadInterval(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("AUTOUPDATE_ENABLED", "true")
	t.Setenv("AUTOUPDATE_CHECK_INTERVAL", "nope")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with invalid AUTOUPDATE_CHECK_INTERVAL")
	}
	if !strings.Contains(err.Error(), "AUTOUPDATE_CHECK_INTERVAL") {
		t.Fatalf("error = %q, want mention of AUTOUPDATE_CHECK_INTERVAL", err.Error())
	}
}

func TestLoadAutoUpdateRejectsEmptyImage(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("AUTOUPDATE_ENABLED", "true")
	t.Setenv("AUTOUPDATE_IMAGE", "   ")

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail with empty AUTOUPDATE_IMAGE when enabled")
	}
	if !strings.Contains(err.Error(), "AUTOUPDATE_IMAGE") {
		t.Fatalf("error = %q, want mention of AUTOUPDATE_IMAGE", err.Error())
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

func TestLoadXrayCheckerDisabledByDefault(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.XrayChecker.Enabled() {
		t.Fatal("XrayChecker should be disabled without XRAY_CHECKER_URL")
	}
}

func TestLoadXrayCheckerEnabled(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("XRAY_CHECKER_URL", "http://xray-checker:2112/")
	t.Setenv("XRAY_CHECKER_USERNAME", " metrics ")
	t.Setenv("XRAY_CHECKER_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.XrayChecker.Enabled() {
		t.Fatal("XrayChecker should be enabled with XRAY_CHECKER_URL")
	}
	if cfg.XrayChecker.URL != "http://xray-checker:2112" {
		t.Fatalf("URL = %q, want trimmed without trailing slash", cfg.XrayChecker.URL)
	}
	if cfg.XrayChecker.Username != "metrics" {
		t.Fatalf("Username = %q, want trimmed", cfg.XrayChecker.Username)
	}
	if cfg.XrayChecker.PollInterval != 2*time.Minute {
		t.Fatalf("PollInterval = %v, want 2m default", cfg.XrayChecker.PollInterval)
	}
}

func TestLoadXrayCheckerRejectsBadURL(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("XRAY_CHECKER_URL", "xray-checker:2112")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "XRAY_CHECKER_URL") {
		t.Fatalf("error = %v, want mention of XRAY_CHECKER_URL", err)
	}
}

func TestLoadXrayCheckerRejectsBadInterval(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("XRAY_CHECKER_URL", "http://xray-checker:2112")
	t.Setenv("XRAY_CHECKER_POLL_INTERVAL", "nope")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "XRAY_CHECKER_POLL_INTERVAL") {
		t.Fatalf("error = %v, want mention of XRAY_CHECKER_POLL_INTERVAL", err)
	}
}

func TestLoadXrayCheckerPublicURL(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("XRAY_CHECKER_PUBLIC_URL", "https://bot.example.com/checker/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.XrayChecker.PublicURL != "https://bot.example.com/checker" {
		t.Fatalf("PublicURL = %q, want trimmed without trailing slash", cfg.XrayChecker.PublicURL)
	}
}

func TestLoadXrayCheckerPublicURLEmptyByDefault(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.XrayChecker.PublicURL != "" {
		t.Fatalf("PublicURL = %q, want empty by default", cfg.XrayChecker.PublicURL)
	}
}

func TestLoadXrayCheckerRejectsBadPublicURL(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("XRAY_CHECKER_PUBLIC_URL", "bot.example.com/checker")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "XRAY_CHECKER_PUBLIC_URL") {
		t.Fatalf("error = %v, want mention of XRAY_CHECKER_PUBLIC_URL", err)
	}
}

func TestLoadStarsRequiresPositiveRate(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_STARS_ENABLED", "true")
	// No TELEGRAM_STARS_RATE set -> rate 0 -> rejected.
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TELEGRAM_STARS_RATE") {
		t.Fatalf("error = %v, want mention of TELEGRAM_STARS_RATE", err)
	}
}

func TestLoadStarsEnabledWithRate(t *testing.T) {
	t.Setenv("REMNAWAVE_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "tok")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_STARS_ENABLED", "true")
	t.Setenv("TELEGRAM_STARS_RATE", "2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Stars.Available() || cfg.Stars.Rate != 2 {
		t.Fatalf("stars config = %+v, want available with rate 2", cfg.Stars)
	}
}
