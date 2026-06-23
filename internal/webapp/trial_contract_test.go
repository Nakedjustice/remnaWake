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
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("mini app is missing %q", want)
		}
	}
}
