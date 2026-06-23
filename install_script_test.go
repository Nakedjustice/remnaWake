package main

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptV2Contract(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"./install.sh doctor",
		"./install.sh update",
		"WEBAPP_HOST_PORT=$WEBAPP_HOST_PORT",
		"127.0.0.1:%s:8080",
		"TRIAL_ENABLED=$TRIAL_ENABLED",
		"REFERRAL_ENABLED=$REFERRAL_ENABLED",
		`AUTOUPDATE_IMAGE="$(env_default AUTOUPDATE_IMAGE "ghcr.io/nakedjustice/remnawake:main")"`,
		"docker-compose.override.yml",
		"watchtower:",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("install.sh is missing %q", want)
		}
	}
}

func TestInstallSmokeScriptCoversSelectedPortAndUpdate(t *testing.T) {
	b, err := os.ReadFile("scripts/install-smoke.sh")
	if err != nil {
		t.Fatalf("read smoke script: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"WEBAPP_HOST_PORT=9090",
		"127.0.0.1:9090:8080",
		"TRIAL_ENABLED=true",
		"REFERRAL_ENABLED=true",
		"bash \"$ROOT/install.sh\" doctor",
		"bash \"$ROOT/install.sh\" update",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("smoke script is missing %q", want)
		}
	}
}
