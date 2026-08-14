#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"

cat >"$TMP/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  "compose version")
    echo "Docker Compose version v2.mock"
    ;;
  "compose config")
    echo "services: {}"
    ;;
  "compose pull")
    touch "$REMNAWAKE_SMOKE_DIR/docker-pull"
    ;;
  "compose up -d")
    touch "$REMNAWAKE_SMOKE_DIR/docker-up"
    ;;
  "compose stop bot")
    touch "$REMNAWAKE_SMOKE_DIR/docker-stop"
    ;;
  "compose cp bot:/data/bot.db "*)
    # Safety copy out of the volume: create the destination file.
    touch "${@: -1}"
    touch "$REMNAWAKE_SMOKE_DIR/docker-cp-out"
    ;;
  "compose cp "*)
    touch "$REMNAWAKE_SMOKE_DIR/docker-cp-in"
    ;;
  "compose ps")
    echo "NAME STATUS"
    ;;
  "info")
    echo "mock docker"
    ;;
  *)
    echo "unexpected docker call: $*" >&2
    exit 2
    ;;
esac
MOCK
chmod +x "$TMP/bin/docker"

cat >"$TMP/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
dest=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      dest="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[ -n "$dest" ] || { echo "curl mock needs -o" >&2; exit 2; }
cp "$REMNAWAKE_SMOKE_ROOT/docker-compose.yml" "$dest"
MOCK
chmod +x "$TMP/bin/curl"

export PATH="$TMP/bin:$PATH"
export REMNAWAKE_DIR="$TMP/app"
export REMNAWAKE_NO_TTY=1
export REMNAWAKE_SMOKE_ROOT="$ROOT"
export REMNAWAKE_SMOKE_DIR="$TMP"

bash "$ROOT/install.sh" configure <<'EOF'
main
https://panel.example.com
remnawave-token
123456789:AAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
123456
Europe/Moscow
09:00
y
https://bot.example.com
n
n
y
7
25
2

y
30
5
n
9090
n
y
ghcr.io/nakedjustice/remnawake:main
6h
y
y
https://panel.example.com/sub
2m
y
/checker
2112
n
EOF

grep -q '^WEBAPP_HOST_PORT=9090$' "$REMNAWAKE_DIR/.env"
grep -q '^WEBAPP_LISTEN=:8080$' "$REMNAWAKE_DIR/.env"
grep -q '^TRIAL_ENABLED=true$' "$REMNAWAKE_DIR/.env"
grep -q '^REFERRAL_ENABLED=true$' "$REMNAWAKE_DIR/.env"
grep -q '^AUTOUPDATE_IMAGE=ghcr.io/nakedjustice/remnawake:main$' "$REMNAWAKE_DIR/.env"
grep -q '127.0.0.1:9090:8080' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q '^  watchtower:$' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: nickfedor/watchtower:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'pull_policy: always' "$REMNAWAKE_DIR/docker-compose.override.yml"
# Watchtower must be left to negotiate the Docker API version with the daemon.
# Pinning it is what stranded the old sidecar on new daemons in the first place.
! grep -q 'DOCKER_API_VERSION' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q '^XRAY_CHECKER_URL=http://xray-checker:2112/checker$' "$REMNAWAKE_DIR/.env"
grep -q '^XRAY_CHECKER_SUB_URL=https://panel.example.com/sub$' "$REMNAWAKE_DIR/.env"
grep -q '^XRAY_CHECKER_PUBLIC_URL=https://bot.example.com/checker/$' "$REMNAWAKE_DIR/.env"
grep -q '^XRAY_CHECKER_BASE_PATH=/checker$' "$REMNAWAKE_DIR/.env"
grep -q '^  xray-checker:$' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: kutovoys/xray-checker:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q '127.0.0.1:2112:2112' "$REMNAWAKE_DIR/docker-compose.override.yml"

bash "$ROOT/install.sh" doctor >"$TMP/doctor-stable.log" 2>&1
grep -q 'remnaWake doctor v2' "$TMP/doctor-stable.log"
grep -q 'Routes and ports' "$TMP/doctor-stable.log"
grep -q 'Generated compose' "$TMP/doctor-stable.log"
grep -q 'Watchtower' "$TMP/doctor-stable.log"
grep -q 'xray-checker' "$TMP/doctor-stable.log"
grep -q 'Backups' "$TMP/doctor-stable.log"
grep -q 'Detected: host-reverse-proxy' "$TMP/doctor-stable.log"
grep -q 'OK: host proxy route' "$TMP/doctor-stable.log"
grep -q '127.0.0.1:9090:8080' "$TMP/doctor-stable.log"
grep -q 'OK: base bot image' "$TMP/doctor-stable.log"
grep -q 'ghcr.io/nakedjustice/remnawake:main' "$TMP/doctor-stable.log"
grep -q 'OK: Watchtower image' "$TMP/doctor-stable.log"
grep -q 'nickfedor/watchtower:latest' "$TMP/doctor-stable.log"
grep -q 'OK: Watchtower Docker API' "$TMP/doctor-stable.log"
grep -q 'OK: XRAY_CHECKER_URL base path' "$TMP/doctor-stable.log"
grep -q 'OK: xray-checker METRICS_BASE_PATH' "$TMP/doctor-stable.log"
grep -q 'WARN: backup directory' "$TMP/doctor-stable.log"
grep -q './install.sh backup' "$TMP/doctor-stable.log"

cp "$REMNAWAKE_DIR/docker-compose.override.yml" "$TMP/override.good"
sed 's#nickfedor/watchtower:latest#nickfedor/watchtower:old#' "$TMP/override.good" >"$REMNAWAKE_DIR/docker-compose.override.yml"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-watchtower-drift.log" 2>&1
grep -q 'WARN: Watchtower image' "$TMP/doctor-watchtower-drift.log"

# An install still on the archived containrrr sidecar must be told to migrate,
# and a leftover pinned Docker API version must be flagged too.
sed 's#image: nickfedor/watchtower:latest#image: containrrr/watchtower:latest#' "$TMP/override.good" >"$REMNAWAKE_DIR/docker-compose.override.yml"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-watchtower-legacy.log" 2>&1
grep -q 'WARN: Watchtower image' "$TMP/doctor-watchtower-legacy.log"
sed 's#      WATCHTOWER_HTTP_API_UPDATE: "true"#      DOCKER_API_VERSION: "1.40"\n      WATCHTOWER_HTTP_API_UPDATE: "true"#' "$TMP/override.good" >"$REMNAWAKE_DIR/docker-compose.override.yml"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-watchtower-apipin.log" 2>&1
grep -q 'WARN: Watchtower Docker API' "$TMP/doctor-watchtower-apipin.log"
cp "$TMP/override.good" "$REMNAWAKE_DIR/docker-compose.override.yml"

bash "$ROOT/install.sh" update

test -f "$TMP/docker-pull"
test -f "$TMP/docker-up"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-backed-up.log" 2>&1
grep -q 'OK: backup directory' "$TMP/doctor-backed-up.log"
grep -q 'OK: latest backup for .env' "$TMP/doctor-backed-up.log"
grep -q 'OK: latest backup for docker-compose.yml' "$TMP/doctor-backed-up.log"
grep -q 'OK: latest backup for docker-compose.override.yml' "$TMP/doctor-backed-up.log"

export REMNAWAKE_DIR="$TMP/devapp"

bash "$ROOT/install.sh" configure <<'EOF'
dev
y
https://panel.example.com
remnawave-token
123456789:AAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
123456
Europe/Moscow
09:00
n
n
n
n
n
n
n
n
EOF

grep -q '^REMNAWAKE_CHANNEL=dev$' "$REMNAWAKE_DIR/.env"
grep -q '^AUTOUPDATE_IMAGE=ghcr.io/nakedjustice/remnawake-dev:latest$' "$REMNAWAKE_DIR/.env"
grep -q 'image: ghcr.io/nakedjustice/remnawake-dev:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-dev.log" 2>&1
! grep -q 'nickfedor/watchtower' "$TMP/doctor-dev.log"

# Reconfigure menu: editing a single section against the first install must
# change only that section and preserve the inferred override (host proxy port,
# watchtower and xray-checker sidecars) without re-visiting those sections.
export REMNAWAKE_DIR="$TMP/app"
bash "$ROOT/install.sh" menu <<'EOF'
4
Asia/Tokyo
10:30
S
n
Q
EOF

grep -q '^TZ=Asia/Tokyo$' "$REMNAWAKE_DIR/.env"
grep -q '^RUN_AT=10:30$' "$REMNAWAKE_DIR/.env"
grep -q '^TELEGRAM_BOT_TOKEN=123456789:' "$REMNAWAKE_DIR/.env"
grep -q '127.0.0.1:9090:8080' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: nickfedor/watchtower:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: kutovoys/xray-checker:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
# A partial reconfigure must NOT drop the checker's base path. METRICS_BASE_PATH
# is what mounts the sidecar (and the bot's /checker/metrics poll) under /checker;
# losing it reverts the checker to serving at root, 404-ing both the public
# dashboard and the proxy-health poll even though .env still advertises /checker.
grep -q 'METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"' "$REMNAWAKE_DIR/docker-compose.override.yml"

# Alongside-Remnawave (containerised Caddy) with a PRE-EXISTING bot site: the
# installer must inject the dashboard route into that site, not just warn. This
# is the scenario that previously left https://host/checker returning 404.
export REMNAWAKE_DIR="$TMP/alongapp"
export REMNAWAVE_CADDYFILE="$TMP/Caddyfile"
cat >"$REMNAWAVE_CADDYFILE" <<'CADDY'
https://bot.example.com {
    reverse_proxy remnaWake-bot:8080
}
CADDY

bash "$ROOT/install.sh" configure <<'EOF'
main
https://panel.example.com
remnawave-token
123456789:AAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
123456
Europe/Moscow
09:00
y
https://bot.example.com
n
n
n
n
y
n
n
y
https://panel.example.com/sub
2m
y
/checker
n
EOF

grep -q '^XRAY_CHECKER_PUBLIC_URL=https://bot.example.com/checker/$' "$REMNAWAKE_DIR/.env"
grep -q '^XRAY_CHECKER_URL=http://xray-checker:2112/checker$' "$REMNAWAKE_DIR/.env"
grep -q 'METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"' "$REMNAWAKE_DIR/docker-compose.override.yml"
# The route was injected into the existing bot site (redirect + trailing-slash handle).
grep -q 'redir /checker /checker/' "$REMNAWAVE_CADDYFILE"
grep -q 'handle /checker/\* {' "$REMNAWAVE_CADDYFILE"
grep -q 'reverse_proxy remnaWake-xray-checker:2112' "$REMNAWAVE_CADDYFILE"
grep -q 'reverse_proxy remnaWake-bot:8080' "$REMNAWAVE_CADDYFILE"
bash "$ROOT/install.sh" doctor >"$TMP/doctor-alongside.log" 2>&1
grep -q 'Detected: alongside-remnawave' "$TMP/doctor-alongside.log"
grep -q 'OK: remnawave network' "$TMP/doctor-alongside.log"
grep -q 'OK: Caddy bot route' "$TMP/doctor-alongside.log"
grep -q 'remnaWake-bot:8080' "$TMP/doctor-alongside.log"
grep -q 'OK: XRAY_CHECKER_URL base path' "$TMP/doctor-alongside.log"
grep -q 'OK: xray-checker METRICS_BASE_PATH' "$TMP/doctor-alongside.log"
unset REMNAWAVE_CADDYFILE

# Restore: a valid SQLite backup must be copied into the volume with a safety
# copy of the current database landing in ./backups, and stale WAL sidecars
# must be neutralized before the bot restarts.
export REMNAWAKE_DIR="$TMP/app"
printf 'SQLite format 3\000rest-of-file' >"$TMP/restore-me.db"
rm -f "$TMP/docker-stop" "$TMP/docker-up" "$TMP/docker-cp-in" "$TMP/docker-cp-out"
bash "$ROOT/install.sh" restore "$TMP/restore-me.db" <<'EOF'
y
EOF

test -f "$TMP/docker-cp-out"
test -f "$TMP/docker-stop"
test -f "$TMP/docker-cp-in"
test -f "$TMP/docker-up"
ls "$REMNAWAKE_DIR/backups"/bot.db.pre-restore.* >/dev/null

# A non-SQLite file must be rejected before anything is touched.
rm -f "$TMP/docker-stop"
printf 'not a database' >"$TMP/bogus.db"
if bash "$ROOT/install.sh" restore "$TMP/bogus.db" </dev/null; then
  echo "restore accepted a non-SQLite file" >&2
  exit 1
fi
test ! -f "$TMP/docker-stop"

echo "install smoke passed"
