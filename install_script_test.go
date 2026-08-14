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
		"./install.sh restore <backup.db>",
		"restore_mode()",
		"SQLite format 3",
		"$compose cp bot:/data/bot.db",
		"bot.db.pre-restore.",
		"$compose stop bot",
		"bot:/data/bot.db-wal",
		"bot:/data/bot.db-shm",
		"Deployment channel (main = stable, dev = unstable)",
		"REMNAWAKE_CHANNEL=$REMNAWAKE_CHANNEL",
		"ghcr.io/nakedjustice/remnawake-dev:latest",
		"WEBAPP_HOST_PORT=$WEBAPP_HOST_PORT",
		"127.0.0.1:%s:8080",
		"TRIAL_ENABLED=$TRIAL_ENABLED",
		"REFERRAL_ENABLED=$REFERRAL_ENABLED",
		`AUTOUPDATE_IMAGE="$(env_default AUTOUPDATE_IMAGE "$DEFAULT_DEPLOY_IMAGE")"`,
		"docker-compose.override.yml",
		// The archived containrrr image was replaced by the maintained fork;
		// the Docker API version is deliberately left unpinned so Watchtower
		// negotiates it with whatever daemon the host runs.
		"nickfedor/watchtower:latest",
		"pull_policy: always",
		"unset so Watchtower negotiates with the daemon",
		`service_image "$OVERRIDE_FILE" bot`,
		// The panel-version requirement must reach the operator both while
		// entering the panel URL and when diagnosing an existing install.
		"Requires Remnawave panel v3.0.0 or newer.",
		"non-empty; panel must be Remnawave v3.0.0 or newer",
		"Expose the Xray Checker web dashboard at a public URL?",
		"XRAY_CHECKER_PUBLIC_URL=$XRAY_CHECKER_PUBLIC_URL",
		`METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"`,
		"127.0.0.1:%s:2112",
		"reverse_proxy remnaWake-xray-checker:2112",
		// Only the image is pulled on update, so the installer keeps itself
		// current too: an operator on a months-old install.sh runs doctor checks
		// and override generation that predate the bot they are running.
		"refresh_installer()",
		"fetch_remote_installer()",
		"$REPO_RAW/install.sh",
		"REMNAWAKE_SKIP_SELF_UPDATE",
		`doctor_section "Installer"`,
		"install.sh freshness",
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
		"REMNAWAKE_CHANNEL=dev",
		"AUTOUPDATE_IMAGE=ghcr.io/nakedjustice/remnawake-dev:latest",
		"image: ghcr.io/nakedjustice/remnawake-dev:latest",
		"WEBAPP_HOST_PORT=9090",
		"127.0.0.1:9090:8080",
		"TRIAL_ENABLED=true",
		"REFERRAL_ENABLED=true",
		"bash \"$ROOT/install.sh\" doctor",
		"bash \"$ROOT/install.sh\" update",
		"bash \"$ROOT/install.sh\" restore",
		"bot.db.pre-restore.",
		"image: nickfedor/watchtower:latest",
		"pull_policy: always",
		`! grep -q 'DOCKER_API_VERSION'`,
		"image: containrrr/watchtower:latest",
		"! grep -q 'nickfedor/watchtower'",
		"^XRAY_CHECKER_PUBLIC_URL=https://bot.example.com/checker/$",
		`METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"`,
		"127.0.0.1:2112:2112",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("smoke script is missing %q", want)
		}
	}
}

func TestGetScriptLeavesChannelRawSelectionToInstaller(t *testing.T) {
	b, err := os.ReadFile("get.sh")
	if err != nil {
		t.Fatalf("read get.sh: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`export REMNAWAKE_REPO="$REPO"`,
		"release-channel prompt",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("get.sh is missing %q", want)
		}
	}
	if strings.Contains(src, `export REMNAWAKE_REPO_RAW="https://raw.githubusercontent.com/$REPO/$BRANCH"`) {
		t.Fatal("get.sh should not force REMNAWAKE_REPO_RAW because install.sh selects main/dev raw files")
	}
}
