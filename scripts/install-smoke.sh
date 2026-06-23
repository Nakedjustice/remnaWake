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
n
EOF

grep -q '^WEBAPP_HOST_PORT=9090$' "$REMNAWAKE_DIR/.env"
grep -q '^WEBAPP_LISTEN=:8080$' "$REMNAWAKE_DIR/.env"
grep -q '^TRIAL_ENABLED=true$' "$REMNAWAKE_DIR/.env"
grep -q '^REFERRAL_ENABLED=true$' "$REMNAWAKE_DIR/.env"
grep -q '^AUTOUPDATE_IMAGE=ghcr.io/nakedjustice/remnawake:main$' "$REMNAWAKE_DIR/.env"
grep -q '127.0.0.1:9090:8080' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q '^  watchtower:$' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: containrrr/watchtower:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'pull_policy: always' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'DOCKER_API_VERSION: "1.40"' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q '^XRAY_CHECKER_URL=http://xray-checker:2112$' "$REMNAWAKE_DIR/.env"
grep -q '^XRAY_CHECKER_SUB_URL=https://panel.example.com/sub$' "$REMNAWAKE_DIR/.env"
grep -q '^  xray-checker:$' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: kutovoys/xray-checker:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"

bash "$ROOT/install.sh" doctor >"$TMP/doctor-stable.log" 2>&1
! grep -q 'containrrr/watchtower' "$TMP/doctor-stable.log"
bash "$ROOT/install.sh" update

test -f "$TMP/docker-pull"
test -f "$TMP/docker-up"

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
! grep -q 'containrrr/watchtower' "$TMP/doctor-dev.log"

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
grep -q 'image: containrrr/watchtower:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"
grep -q 'image: kutovoys/xray-checker:latest' "$REMNAWAKE_DIR/docker-compose.override.yml"

echo "install smoke passed"
