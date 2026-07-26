package webapp

import (
	"os"
	"strings"
	"testing"
)

func TestTrialFrontendContract(t *testing.T) {
	body, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read mini app: %v", err)
	}
	html := string(body)
	for _, want := range []string{
		"trial_terms", "trial_traffic_limit_gb", "trial_hwid_device_limit", "trial_squad_uuid",
		"traffic_limit_gb", "hwid_device_limit", "squad_uuid", "NO_RESET",
		"PROFILE_USERNAME_PATTERN = /^[A-Za-z0-9_-]{3,36}$/",
		"minLength: 3, maxLength: 36",
		"input.setAttribute('pattern', '[A-Za-z0-9_-]{3,36}')",
		"input.setAttribute('autocapitalize', 'none')",
		"input.setAttribute('autocomplete', 'off')",
		"input.setAttribute('spellcheck', 'false')",
		"error.code === 'invalid_username'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("mini app is missing %q", want)
		}
	}
}

// The scheduled-backup settings must stay reachable from the mini app admin
// panel, not only from the bot admin menu.
func TestBackupScheduleFrontendContract(t *testing.T) {
	body, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read mini app: %v", err)
	}
	html := string(body)
	for _, want := range []string{
		"renderAdminBackups",
		"appEl.appendChild(renderAdminBackups(data))",
		"'/api/admin/backup/schedule'",
		"backup_enabled",
		"backup_interval_days",
		"backup_max_interval_days",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("mini app is missing %q", want)
		}
	}
}
