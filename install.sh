#!/usr/bin/env bash
#
# Installer and maintenance helper for remnaWake.
#
# Usage:
#   chmod +x install.sh
#   ./install.sh
#   ./install.sh configure
#   ./install.sh doctor
#   ./install.sh update
#   ./install.sh backup
#
set -euo pipefail

# Reattach the terminal when piped (curl ... | bash), unless tests explicitly
# keep stdin attached to a here-doc.
if [ "${REMNAWAKE_NO_TTY:-}" != "1" ] && [ ! -t 0 ] && [ -e /dev/tty ] && (exec </dev/tty) 2>/dev/null; then
  exec </dev/tty
fi

REPO="${REMNAWAKE_REPO:-Nakedjustice/remnaWake}"
REPO_RAW_OVERRIDE="${REMNAWAKE_REPO_RAW:-}"
DEPLOY_IMAGE_OVERRIDE="${REMNAWAKE_DEPLOY_IMAGE:-}"
REPO_RAW="${REPO_RAW_OVERRIDE:-https://raw.githubusercontent.com/Nakedjustice/remnaWake/main}"
DEFAULT_DEPLOY_IMAGE="${DEPLOY_IMAGE_OVERRIDE:-ghcr.io/nakedjustice/remnawake:main}"

__src="${BASH_SOURCE[0]:-}"
if [ -n "$__src" ] && [ -f "$__src" ]; then
  INSTALL_DIR="$(cd -- "$(dirname -- "$__src")" >/dev/null 2>&1 && pwd)"
else
  INSTALL_DIR="$PWD"
fi
INSTALL_DIR="${REMNAWAKE_DIR:-$INSTALL_DIR}"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

ENV_FILE="$INSTALL_DIR/.env"
COMPOSE_FILE="$INSTALL_DIR/docker-compose.yml"
OVERRIDE_FILE="$INSTALL_DIR/docker-compose.override.yml"
BACKUP_DIR="$INSTALL_DIR/backups"

# Absolute path to this script, but only when it was run from a real file:
# `curl ... | bash` has no file to compare or replace, so self-update is skipped.
SCRIPT_FILE=""
if [ -n "$__src" ] && [ -f "$__src" ]; then
  SCRIPT_FILE="$(cd -- "$(dirname -- "$__src")" >/dev/null 2>&1 && pwd)/$(basename -- "$__src")"
fi

if [ -t 1 ]; then
  BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"
  GREEN="$(printf '\033[32m')"; YELLOW="$(printf '\033[33m')"
  RED="$(printf '\033[31m')"; CYAN="$(printf '\033[36m')"; RESET="$(printf '\033[0m')"
else
  BOLD=""; DIM=""; GREEN=""; YELLOW=""; RED=""; CYAN=""; RESET=""
fi

info()  { printf '%s\n' "${CYAN}$*${RESET}"; }
ok()    { printf '%s\n' "${GREEN}$*${RESET}"; }
warn()  { printf '%s\n' "${YELLOW}$*${RESET}" >&2; }
err()   { printf '%s\n' "${RED}$*${RESET}" >&2; }

usage() {
  cat <<EOF
remnaWake installer

Usage:
  ./install.sh [configure]
  ./install.sh menu
  ./install.sh doctor
  ./install.sh update
  ./install.sh backup
  ./install.sh restore <backup.db>
  ./install.sh --help

Modes:
  configure  Interactive setup. On a first install it walks every section in
             order; when an .env already exists it opens the reconfigure menu.
  menu       Open the reconfigure menu directly to edit individual sections.
  doctor     Check Docker, compose config, .env keys, ports and update settings.
  update     Back up config, pull the image and restart with Docker Compose.
  backup     Copy .env and compose files into ./backups.
  restore    Replace the bot database with a backup .db file (e.g. one the bot
             sent to an admin DM). Stops the bot, saves the current database
             to ./backups, copies the file into the botdata volume, restarts.

Environment:
  REMNAWAKE_DIR       Install directory. Default: script directory or current dir.
  REMNAWAKE_REPO      GitHub repository used for channel raw URLs.
  REMNAWAKE_REPO_RAW  Raw GitHub URL used to fetch docker-compose.yml.
  REMNAWAKE_CHANNEL   Deployment channel: main or dev. Default: main.
  REMNAWAKE_DEPLOY_IMAGE
                      Image used for new installs unless overridden in .env.
EOF
}

fetch() {
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    return 127
  fi
}

timestamp() {
  date +%Y%m%d%H%M%S
}

# sha256 of a file. Fails (empty output) when no hashing tool is installed.
file_digest() {
  local file="$1"
  [ -f "$file" ] || return 1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | cut -d' ' -f1
  else
    return 1
  fi
}

# Download the current channel's install.sh to a temp file and print its path.
# Uses its own short-timeout fetch rather than fetch(): a freshness check must
# never hang an update or a doctor run behind an unreachable GitHub.
fetch_remote_installer() {
  local tmp
  tmp="$(mktemp)" || return 1
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --max-time 15 "$REPO_RAW/install.sh" -o "$tmp" 2>/dev/null || { rm -f "$tmp"; return 1; }
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$tmp" --timeout=15 --tries=1 "$REPO_RAW/install.sh" 2>/dev/null || { rm -f "$tmp"; return 1; }
  else
    rm -f "$tmp"
    return 1
  fi
  # A captive portal or GitHub error page must never be mistaken for the
  # installer and copied over a working one.
  if ! head -n 1 "$tmp" | grep -q '^#!/usr/bin/env bash'; then
    rm -f "$tmp"
    return 1
  fi
  printf '%s' "$tmp"
}

# Replace install.sh with the current channel version when they differ.
#
# `update` pulls the bot image but historically left the installer alone, so an
# operator could run a months-old install.sh against a current bot: its doctor
# checks and its docker-compose.override.yml generation both lag the image, and
# nothing ever says so. The rename is atomic and the running bash keeps reading
# the old inode, so replacing the file mid-run is safe.
refresh_installer() {
  local remote local_digest remote_digest
  if [ -z "$SCRIPT_FILE" ] || [ "${REMNAWAKE_SKIP_SELF_UPDATE:-}" = "1" ]; then
    return 0
  fi
  if ! remote="$(fetch_remote_installer)"; then
    warn "Could not check whether install.sh is current (no network, or curl/wget missing)."
    return 0
  fi
  local_digest="$(file_digest "$SCRIPT_FILE" 2>/dev/null || true)"
  remote_digest="$(file_digest "$remote" 2>/dev/null || true)"
  if [ -z "$local_digest" ] || [ -z "$remote_digest" ]; then
    rm -f "$remote"
    warn "Could not compare install.sh versions (no sha256sum or shasum available)."
    return 0
  fi
  if [ "$local_digest" = "$remote_digest" ]; then
    rm -f "$remote"
    ok "install.sh is current for the $REMNAWAKE_CHANNEL channel."
    return 0
  fi
  backup_file "$SCRIPT_FILE"
  chmod +x "$remote"
  mv "$remote" "$SCRIPT_FILE"
  ok "Updated install.sh to the current $REMNAWAKE_CHANNEL version."
  warn "It takes effect on the next run — re-run ./install.sh doctor to use the new checks."
}

backup_file() {
  local file="$1"
  [ -f "$file" ] || return 0
  local dest="$file.bak.$(timestamp)"
  cp "$file" "$dest"
  ok "Backed up $(basename "$file") to $dest"
}

run_backup() {
  mkdir -p "$BACKUP_DIR"
  local stamp dest copied=0
  stamp="$(timestamp)"
  for file in "$ENV_FILE" "$COMPOSE_FILE" "$OVERRIDE_FILE"; do
    if [ -f "$file" ]; then
      dest="$BACKUP_DIR/$(basename "$file").$stamp"
      cp "$file" "$dest"
      ok "Backed up $(basename "$file") to $dest"
      copied=$((copied + 1))
    fi
  done
  if [ "$copied" -eq 0 ]; then
    warn "No .env or compose files found to back up in $INSTALL_DIR"
  fi
}

env_get() {
  local key="$1" line
  [ -f "$ENV_FILE" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      "$key="*) printf '%s' "${line#*=}"; return 0 ;;
    esac
  done <"$ENV_FILE"
  return 1
}

env_default() {
  local key="$1" fallback="${2:-}" value
  if value="$(env_get "$key")"; then
    printf '%s' "$value"
  else
    printf '%s' "$fallback"
  fi
}

known_env_key() {
  case "$1" in
    REMNAWAKE_CHANNEL)
      return 0
      ;;
    REMNAWAVE_BASE_URL|REMNAWAVE_API_TOKEN|TELEGRAM_BOT_TOKEN|TELEGRAM_PARSE_MODE|TELEGRAM_ADMIN_ID)
      return 0
      ;;
    TZ|RUN_AT|LOG_LEVEL|HTTP_TIMEOUT|DRY_RUN|RUN_ON_START|CURRENCY|BOT_LANG)
      return 0
      ;;
    WINBACK_ENABLED|WINBACK_DAYS|WEBAPP_URL|WEBAPP_LISTEN|WEBAPP_HOST_PORT)
      return 0
      ;;
    PLATEGA_MERCHANT_ID|PLATEGA_SECRET|PLATEGA_METHOD|PLATEGA_CURRENCY|PLATEGA_RETURN_URL)
      return 0
      ;;
    TELEGRAM_STARS_ENABLED|TELEGRAM_STARS_RATE)
      return 0
      ;;
    TRIAL_ENABLED|TRIAL_DAYS|TRIAL_TRAFFIC_LIMIT_GB|TRIAL_HWID_DEVICE_LIMIT|TRIAL_SQUAD_UUID)
      return 0
      ;;
    REFERRAL_ENABLED|REFERRAL_INVITER_BONUS_DAYS|REFERRAL_INVITEE_BONUS_DAYS)
      return 0
      ;;
    AUTOUPDATE_ENABLED|AUTOUPDATE_IMAGE|AUTOUPDATE_CHECK_INTERVAL|WATCHTOWER_URL|WATCHTOWER_TOKEN)
      return 0
      ;;
    XRAY_CHECKER_URL|XRAY_CHECKER_USERNAME|XRAY_CHECKER_PASSWORD|XRAY_CHECKER_POLL_INTERVAL|XRAY_CHECKER_SUB_URL|XRAY_CHECKER_METHOD)
      return 0
      ;;
    XRAY_CHECKER_PUBLIC_URL|XRAY_CHECKER_BASE_PATH|XRAY_CHECKER_HOST_PORT)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

preserved_env_lines() {
  local line key
  [ -f "$ENV_FILE" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    [[ "$line" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]] || continue
    key="${line%%=*}"
    if ! known_env_key "$key"; then
      printf '%s\n' "$line"
    fi
  done <"$ENV_FILE"
}

ask() {
  local __var="$1" prompt="$2" default="${3:-}" validator="${4:-}" secret="${5:-}"
  local input display_default="" value
  [ -n "$default" ] && display_default=" ${DIM}[$default]${RESET}"

  while true; do
    printf '%s' "${BOLD}${prompt}${RESET}${display_default}: " >&2
    if [ "$secret" = "secret" ]; then
      read -r -s input
      printf '\n' >&2
    else
      read -r input
    fi

    value="${input:-$default}"
    if [ -z "$value" ]; then
      err "  -> A value is required."
      continue
    fi
    if [ -n "$validator" ] && ! "$validator" "$value"; then
      continue
    fi
    printf -v "$__var" '%s' "$value"
    return 0
  done
}

ask_optional() {
  local __var="$1" prompt="$2" default="${3:-}" validator="${4:-}" secret="${5:-}"
  local input display_default="" value
  [ -n "$default" ] && display_default=" ${DIM}[$default]${RESET}"

  while true; do
    printf '%s' "${BOLD}${prompt}${RESET}${display_default}: " >&2
    if [ "$secret" = "secret" ]; then
      read -r -s input
      printf '\n' >&2
    else
      read -r input
    fi
    value="${input:-$default}"
    if [ -n "$value" ] && [ -n "$validator" ] && ! "$validator" "$value"; then
      continue
    fi
    printf -v "$__var" '%s' "$value"
    return 0
  done
}

ask_yes_no() {
  local prompt="$1" default="${2:-y}" ans
  local hint="[Y/n]"; [ "$default" = "n" ] && hint="[y/N]"
  while true; do
    printf '%s' "${BOLD}${prompt}${RESET} ${DIM}${hint}${RESET} " >&2
    read -r ans
    ans="${ans:-$default}"
    case "$ans" in
      y|Y|yes|YES) return 0 ;;
      n|N|no|NO)   return 1 ;;
      *) err "  -> Please answer y or n." ;;
    esac
  done
}

v_url() {
  case "$1" in
    http://*|https://*) return 0 ;;
    *) err "  -> Must start with http:// or https://"; return 1 ;;
  esac
}

v_https_url() {
  case "$1" in
    https://*) return 0 ;;
    *) err "  -> Telegram Mini Apps require an https:// URL."; return 1 ;;
  esac
}

v_path() {
  case "$1" in
    *' '*) err "  -> The path cannot contain spaces."; return 1 ;;
    /*) return 0 ;;
    *) err "  -> Enter a path beginning with / (e.g. /checker)."; return 1 ;;
  esac
}

v_bot_token() {
  if printf '%s' "$1" | grep -Eq '^[0-9]+:[A-Za-z0-9_-]+$'; then
    return 0
  fi
  warn "  -> That does not look like a BotFather token (e.g. 123456789:AA...)."
  ask_yes_no "    Use it anyway?" "n" && return 0
  return 1
}

v_admin_id() {
  local input="$1"
  if [ "$input" = "0" ]; then
    warn "  -> 0 entered: payment, invite and register admin flows will be disabled."
    return 0
  fi
  local old_ifs="$IFS" token valid=0
  IFS=','
  for token in $input; do
    IFS="$old_ifs"
    token="$(printf '%s' "$token" | tr -d ' \t')"
    [ -z "$token" ] && continue
    if ! printf '%s' "$token" | grep -Eq '^[0-9]+$'; then
      err "  -> Each ID must be digits only. Got: \"$token\""
      IFS="$old_ifs"
      return 1
    fi
    valid=$((valid + 1))
  done
  IFS="$old_ifs"
  if [ "$valid" -eq 0 ]; then
    err "  -> Must be a numeric Telegram user ID, or 0 to disable."
    return 1
  fi
  return 0
}

v_time_hhmm() {
  if printf '%s' "$1" | grep -Eq '^([01][0-9]|2[0-3]):[0-5][0-9]$'; then
    return 0
  fi
  err "  -> Must be HH:MM in 24h format (e.g. 09:00)."
  return 1
}

v_tz() {
  if [ -d /usr/share/zoneinfo ]; then
    if [ -f "/usr/share/zoneinfo/$1" ]; then return 0; fi
    err "  -> Unknown timezone. Use an IANA name like Europe/Moscow or UTC."
    return 1
  fi
  return 0
}

v_duration() {
  if printf '%s' "$1" | grep -Eq '^[0-9]+(ns|us|µs|ms|s|m|h)$'; then
    return 0
  fi
  err "  -> Must be a Go duration like 15s, 500ms, 1m."
  return 1
}

v_loglevel() {
  case "$1" in
    debug|info|warn|warning|error) return 0 ;;
    *) err "  -> One of: debug, info, warn, error."; return 1 ;;
  esac
}

v_bool() {
  case "$1" in
    true|false) return 0 ;;
    *) err "  -> Must be true or false."; return 1 ;;
  esac
}

v_lang() {
  case "$1" in
    ru|en) return 0 ;;
    *) err "  -> One of: ru, en."; return 1 ;;
  esac
}

v_channel() {
  case "$1" in
    main|dev) return 0 ;;
    *) err "  -> One of: main, dev."; return 1 ;;
  esac
}

v_days_list() {
  local old_ifs="$IFS" token valid=0
  IFS=','
  for token in $1; do
    IFS="$old_ifs"
    token="$(printf '%s' "$token" | tr -d ' \t')"
    [ -z "$token" ] && continue
    if ! printf '%s' "$token" | grep -Eq '^[1-9][0-9]*$'; then
      err "  -> Each value must be a positive integer. Got: \"$token\""
      IFS="$old_ifs"
      return 1
    fi
    valid=$((valid + 1))
  done
  IFS="$old_ifs"
  if [ "$valid" -eq 0 ]; then
    err "  -> Enter at least one positive integer."
    return 1
  fi
  return 0
}

v_platega_method() {
  case "$1" in
    sbp|card|cards) return 0 ;;
    *) err "  -> One of: sbp, card."; return 1 ;;
  esac
}

v_posint() {
  case "$1" in
    ''|*[!0-9]*) err "  -> Enter a positive whole number."; return 1 ;;
    0) err "  -> Enter a positive whole number."; return 1 ;;
    *) return 0 ;;
  esac
}

v_nonnegint() {
  case "$1" in
    ''|*[!0-9]*) err "  -> Enter a whole number, 0 or higher."; return 1 ;;
    *) return 0 ;;
  esac
}

v_port() {
  case "$1" in
    ''|*[!0-9]*) err "  -> Enter a TCP port number."; return 1 ;;
  esac
  if [ "$1" -ge 1 ] && [ "$1" -le 65535 ]; then
    return 0
  fi
  err "  -> Port must be between 1 and 65535."
  return 1
}

v_domain() {
  if printf '%s' "$1" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$'; then
    return 0
  fi
  err "  -> Enter a bare domain like bot.example.com (no http://, no path)."
  return 1
}

print_alongside_warning() {
  printf '\n' >&2
  warn "================================================================"
  warn "  AUTO-CONFIG SUPPORTS MANUAL REMNAWAVE INSTALLS ONLY"
  warn "----------------------------------------------------------------"
  warn "  It assumes the documented layout: an external docker network named"
  warn "  'remnawave-network' and a Caddyfile at /opt/remnawave/caddy/Caddyfile."
  warn ""
  warn "  One-click panel installers may use a different network, Caddyfile path,"
  warn "  or nginx. Verify your setup before relying on the automatic steps."
  warn "================================================================"
}

print_caddy_manual() {
  warn "Add this site to your Remnawave Caddyfile (next to the panel entries):"
  warn ""
  warn "    https://${caddy_host} {"
  if [ "$CHECKER_PUBLIC_ENABLED" = "yes" ] && [ -n "$XRAY_CHECKER_BASE_PATH" ]; then
    warn "        redir ${XRAY_CHECKER_BASE_PATH} ${XRAY_CHECKER_BASE_PATH}/"
    warn "        handle ${XRAY_CHECKER_BASE_PATH}/* {"
    warn "            reverse_proxy remnaWake-xray-checker:2112"
    warn "        }"
  fi
  warn "        reverse_proxy remnaWake-bot:8080"
  warn "    }"
  warn ""
  warn "Then make sure ${caddy_host} resolves to this server and reload Caddy:"
  warn "  cd \"$(dirname "$CADDYFILE")\" && docker compose restart caddy"
}

channel_image() {
  if [ -n "$DEPLOY_IMAGE_OVERRIDE" ]; then
    printf '%s' "$DEPLOY_IMAGE_OVERRIDE"
    return 0
  fi
  case "$1" in
    dev) printf 'ghcr.io/nakedjustice/remnawake-dev:latest' ;;
    *) printf 'ghcr.io/nakedjustice/remnawake:main' ;;
  esac
}

channel_repo_raw() {
  if [ -n "$REPO_RAW_OVERRIDE" ]; then
    printf '%s' "$REPO_RAW_OVERRIDE"
    return 0
  fi
  case "$1" in
    dev) printf 'https://raw.githubusercontent.com/%s/dev' "$REPO" ;;
    *) printf 'https://raw.githubusercontent.com/%s/main' "$REPO" ;;
  esac
}

apply_channel_defaults() {
  REMNAWAKE_CHANNEL="${REMNAWAKE_CHANNEL:-main}"
  REPO_RAW="$(channel_repo_raw "$REMNAWAKE_CHANNEL")"
  DEFAULT_DEPLOY_IMAGE="$(channel_image "$REMNAWAKE_CHANNEL")"
}

select_release_channel() {
  info "-- Release channel ----------------------------------------"
  ask REMNAWAKE_CHANNEL "Deployment channel (main = stable, dev = unstable)" "$REMNAWAKE_CHANNEL" v_channel
  if [ "$REMNAWAKE_CHANNEL" = "dev" ]; then
    warn "The dev channel may be unstable and can contain unreviewed or pre-release changes."
    warn "Use it only for testing before merging dev into main."
    if ! ask_yes_no "Use the unstable dev channel anyway?" "n"; then
      REMNAWAKE_CHANNEL="main"
      ok "Using stable main channel."
    fi
  fi
  apply_channel_defaults
  AUTOUPDATE_IMAGE="$DEFAULT_DEPLOY_IMAGE"
}

load_defaults() {
  REMNAWAKE_CHANNEL="$(env_default REMNAWAKE_CHANNEL "${REMNAWAKE_CHANNEL:-main}")"
  v_channel "$REMNAWAKE_CHANNEL" >/dev/null 2>&1 || REMNAWAKE_CHANNEL="main"
  apply_channel_defaults

  REMNAWAVE_BASE_URL="$(env_default REMNAWAVE_BASE_URL "")"
  REMNAWAVE_API_TOKEN="$(env_default REMNAWAVE_API_TOKEN "")"
  TELEGRAM_BOT_TOKEN="$(env_default TELEGRAM_BOT_TOKEN "")"
  TELEGRAM_PARSE_MODE="$(env_default TELEGRAM_PARSE_MODE "HTML")"
  TELEGRAM_ADMIN_ID="$(env_default TELEGRAM_ADMIN_ID "0")"

  TZ="$(env_default TZ "Europe/Moscow")"
  RUN_AT="$(env_default RUN_AT "09:00")"
  LOG_LEVEL="$(env_default LOG_LEVEL "info")"
  HTTP_TIMEOUT="$(env_default HTTP_TIMEOUT "15s")"
  DRY_RUN="$(env_default DRY_RUN "false")"
  RUN_ON_START="$(env_default RUN_ON_START "true")"
  CURRENCY="$(env_default CURRENCY "₽")"
  BOT_LANG="$(env_default BOT_LANG "ru")"

  WINBACK_ENABLED="$(env_default WINBACK_ENABLED "true")"
  WINBACK_DAYS="$(env_default WINBACK_DAYS "1,3")"

  WEBAPP_URL="$(env_default WEBAPP_URL "")"
  WEBAPP_LISTEN=":8080"
  WEBAPP_HOST_PORT="$(env_default WEBAPP_HOST_PORT "8080")"

  PLATEGA_MERCHANT_ID="$(env_default PLATEGA_MERCHANT_ID "")"
  PLATEGA_SECRET="$(env_default PLATEGA_SECRET "")"
  PLATEGA_METHOD="$(env_default PLATEGA_METHOD "sbp")"
  PLATEGA_CURRENCY="$(env_default PLATEGA_CURRENCY "RUB")"
  PLATEGA_RETURN_URL="$(env_default PLATEGA_RETURN_URL "https://t.me")"

  TELEGRAM_STARS_ENABLED="$(env_default TELEGRAM_STARS_ENABLED "false")"
  TELEGRAM_STARS_RATE="$(env_default TELEGRAM_STARS_RATE "")"

  TRIAL_ENABLED="$(env_default TRIAL_ENABLED "false")"
  TRIAL_DAYS="$(env_default TRIAL_DAYS "3")"
  TRIAL_TRAFFIC_LIMIT_GB="$(env_default TRIAL_TRAFFIC_LIMIT_GB "10")"
  TRIAL_HWID_DEVICE_LIMIT="$(env_default TRIAL_HWID_DEVICE_LIMIT "1")"
  TRIAL_SQUAD_UUID="$(env_default TRIAL_SQUAD_UUID "")"

  REFERRAL_ENABLED="$(env_default REFERRAL_ENABLED "false")"
  REFERRAL_INVITER_BONUS_DAYS="$(env_default REFERRAL_INVITER_BONUS_DAYS "30")"
  REFERRAL_INVITEE_BONUS_DAYS="$(env_default REFERRAL_INVITEE_BONUS_DAYS "0")"

  AUTOUPDATE_ENABLED="$(env_default AUTOUPDATE_ENABLED "false")"
  AUTOUPDATE_IMAGE="$(env_default AUTOUPDATE_IMAGE "$DEFAULT_DEPLOY_IMAGE")"
  AUTOUPDATE_CHECK_INTERVAL="$(env_default AUTOUPDATE_CHECK_INTERVAL "6h")"
  WATCHTOWER_URL="$(env_default WATCHTOWER_URL "")"
  WATCHTOWER_TOKEN="$(env_default WATCHTOWER_TOKEN "")"

  XRAY_CHECKER_URL="$(env_default XRAY_CHECKER_URL "")"
  XRAY_CHECKER_USERNAME="$(env_default XRAY_CHECKER_USERNAME "")"
  XRAY_CHECKER_PASSWORD="$(env_default XRAY_CHECKER_PASSWORD "")"
  XRAY_CHECKER_POLL_INTERVAL="$(env_default XRAY_CHECKER_POLL_INTERVAL "2m")"
  XRAY_CHECKER_SUB_URL="$(env_default XRAY_CHECKER_SUB_URL "")"
  XRAY_CHECKER_METHOD="$(env_default XRAY_CHECKER_METHOD "ip")"
  XRAY_CHECKER_PUBLIC_URL="$(env_default XRAY_CHECKER_PUBLIC_URL "")"
  XRAY_CHECKER_BASE_PATH="$(env_default XRAY_CHECKER_BASE_PATH "")"
  XRAY_CHECKER_HOST_PORT="$(env_default XRAY_CHECKER_HOST_PORT "2112")"

  ALONGSIDE_REMNAWAVE="no"
  HOST_PROXY_ENABLED="no"
  # Restore the dashboard-exposed flag from persisted state. The override gates
  # METRICS_BASE_PATH on this flag, but the flag is only set to "yes" by
  # section_xray. A partial reconfigure that edits an unrelated section never
  # walks section_xray, so leaving this at "no" silently drops METRICS_BASE_PATH:
  # the checker reverts to serving at root and both the public /checker dashboard
  # and the bot's /checker/metrics poll start returning 404 even though .env still
  # advertises the sub-path. Inferring it from the persisted public URL keeps the
  # override consistent with .env across reconfigures.
  CHECKER_PUBLIC_ENABLED="no"
  if [ -n "$XRAY_CHECKER_PUBLIC_URL" ]; then
    CHECKER_PUBLIC_ENABLED="yes"
  fi
  caddy_host=""
}

bool_default() {
  [ "$1" = "true" ] && printf 'y' || printf 'n'
}

nonempty_default() {
  [ -n "$1" ] && printf 'y' || printf 'n'
}

generate_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

ensure_compose_file() {
  if [ -f "$COMPOSE_FILE" ]; then
    return 0
  fi
  info "Fetching docker-compose.yml..."
  if ! fetch "$REPO_RAW/docker-compose.yml" "$COMPOSE_FILE"; then
    err "Could not download docker-compose.yml (need curl or wget)."
    err "Grab it manually next to .env: $REPO_RAW/docker-compose.yml"
    exit 1
  fi
  ok "Wrote docker-compose.yml"
}

write_env_file() {
  local preserved tmp_env
  preserved="$(preserved_env_lines || true)"

  umask 177
  tmp_env="$(mktemp "$ENV_FILE.XXXXXX")"
  trap 'rm -f "$tmp_env"' EXIT

  cat >"$tmp_env" <<EOF
# Generated by install.sh on $(date -u +"%Y-%m-%dT%H:%M:%SZ")
# Keep this file private: it contains API tokens.

REMNAWAKE_CHANNEL=$REMNAWAKE_CHANNEL

REMNAWAVE_BASE_URL=$REMNAWAVE_BASE_URL
REMNAWAVE_API_TOKEN=$REMNAWAVE_API_TOKEN

TELEGRAM_BOT_TOKEN=$TELEGRAM_BOT_TOKEN
TELEGRAM_PARSE_MODE=$TELEGRAM_PARSE_MODE
TELEGRAM_ADMIN_ID=$TELEGRAM_ADMIN_ID

TZ=$TZ
RUN_AT=$RUN_AT
LOG_LEVEL=$LOG_LEVEL
HTTP_TIMEOUT=$HTTP_TIMEOUT
DRY_RUN=$DRY_RUN
RUN_ON_START=$RUN_ON_START

CURRENCY=$CURRENCY
BOT_LANG=$BOT_LANG

# Win-back notifications: messages sent to expired users N days after expiry.
WINBACK_ENABLED=$WINBACK_ENABLED
WINBACK_DAYS=$WINBACK_DAYS

# Telegram Mini App and Platega webhook web server. WEBAPP_HOST_PORT is the
# host-only reverse-proxy port; the container keeps listening on 8080.
WEBAPP_URL=$WEBAPP_URL
WEBAPP_LISTEN=$WEBAPP_LISTEN
WEBAPP_HOST_PORT=$WEBAPP_HOST_PORT

# Platega payment gateway (optional): online SBP/card payments.
PLATEGA_MERCHANT_ID=$PLATEGA_MERCHANT_ID
PLATEGA_SECRET=$PLATEGA_SECRET
PLATEGA_METHOD=$PLATEGA_METHOD
PLATEGA_CURRENCY=$PLATEGA_CURRENCY
PLATEGA_RETURN_URL=$PLATEGA_RETURN_URL

# Telegram Stars (optional): native in-app XTR payments using the bot token.
TELEGRAM_STARS_ENABLED=$TELEGRAM_STARS_ENABLED
TELEGRAM_STARS_RATE=$TELEGRAM_STARS_RATE

# Free trial defaults. Admins can change these later from the bot or Mini App.
TRIAL_ENABLED=$TRIAL_ENABLED
TRIAL_DAYS=$TRIAL_DAYS
TRIAL_TRAFFIC_LIMIT_GB=$TRIAL_TRAFFIC_LIMIT_GB
TRIAL_HWID_DEVICE_LIMIT=$TRIAL_HWID_DEVICE_LIMIT
TRIAL_SQUAD_UUID=$TRIAL_SQUAD_UUID

# Referral bonus defaults. Admins can change these later from the bot or Mini App.
REFERRAL_ENABLED=$REFERRAL_ENABLED
REFERRAL_INVITER_BONUS_DAYS=$REFERRAL_INVITER_BONUS_DAYS
REFERRAL_INVITEE_BONUS_DAYS=$REFERRAL_INVITEE_BONUS_DAYS

# Auto-update: notify admins when a new image is published. One-tap install
# requires the watchtower sidecar in docker-compose.override.yml.
AUTOUPDATE_ENABLED=$AUTOUPDATE_ENABLED
AUTOUPDATE_IMAGE=$AUTOUPDATE_IMAGE
AUTOUPDATE_CHECK_INTERVAL=$AUTOUPDATE_CHECK_INTERVAL
WATCHTOWER_URL=$WATCHTOWER_URL
WATCHTOWER_TOKEN=$WATCHTOWER_TOKEN

# Xray Checker proxy monitoring (optional): the bot polls a kutovoys/xray-checker
# sidecar and DMs admins when a proxy goes down or recovers. XRAY_CHECKER_URL
# empty = disabled. XRAY_CHECKER_SUB_URL is the subscription the sidecar probes.
XRAY_CHECKER_URL=$XRAY_CHECKER_URL
XRAY_CHECKER_USERNAME=$XRAY_CHECKER_USERNAME
XRAY_CHECKER_PASSWORD=$XRAY_CHECKER_PASSWORD
XRAY_CHECKER_POLL_INTERVAL=$XRAY_CHECKER_POLL_INTERVAL
XRAY_CHECKER_SUB_URL=$XRAY_CHECKER_SUB_URL
XRAY_CHECKER_METHOD=$XRAY_CHECKER_METHOD
# Public URL of the checker's own web dashboard; when set the bot shows admins a
# button to open it. XRAY_CHECKER_BASE_PATH (e.g. /checker) serves it under the
# Mini App domain via the sidecar's METRICS_BASE_PATH; empty = served at the host
# root. XRAY_CHECKER_HOST_PORT is the 127.0.0.1 port published for a host proxy.
XRAY_CHECKER_PUBLIC_URL=$XRAY_CHECKER_PUBLIC_URL
XRAY_CHECKER_BASE_PATH=$XRAY_CHECKER_BASE_PATH
XRAY_CHECKER_HOST_PORT=$XRAY_CHECKER_HOST_PORT
EOF

  if [ -n "$preserved" ]; then
    {
      printf '\n# Preserved custom keys from the previous .env.\n'
      printf '%s\n' "$preserved"
    } >>"$tmp_env"
  fi

  mv "$tmp_env" "$ENV_FILE"
  trap - EXIT
  chmod 600 "$ENV_FILE"
  umask 022
  ok "Wrote configuration to $ENV_FILE (permissions 600)."
}

write_override_file() {
  local base_image need_bot_detail="no" need_autoupdate_network="no" need_remnawave_network="no" tmp
  base_image="$(compose_image || true)"
  if [ "$HOST_PROXY_ENABLED" = "yes" ] || [ "$ALONGSIDE_REMNAWAVE" = "yes" ] || [ -n "$WATCHTOWER_URL" ] || { [ -n "$base_image" ] && [ "$AUTOUPDATE_IMAGE" != "$base_image" ]; }; then
    need_bot_detail="yes"
  fi
  [ "$ALONGSIDE_REMNAWAVE" = "yes" ] && need_remnawave_network="yes"
  [ -n "$WATCHTOWER_URL" ] && need_autoupdate_network="yes"

  tmp="$(mktemp "$OVERRIDE_FILE.XXXXXX")"
  trap 'rm -f "$tmp"' EXIT
  {
    printf '# Generated by install.sh. Re-run ./install.sh configure to change it.\n'
    printf '# The base docker-compose.yml is intentionally left untouched.\n\n'
    printf 'services:\n'
    if [ "$need_bot_detail" = "yes" ]; then
      printf '  bot:\n'
      if [ -n "$base_image" ] && [ "$AUTOUPDATE_IMAGE" != "$base_image" ]; then
        printf '    image: %s\n' "$AUTOUPDATE_IMAGE"
      fi
      if [ "$HOST_PROXY_ENABLED" = "yes" ]; then
        printf '    ports:\n'
        printf '      - "127.0.0.1:%s:8080"\n' "$WEBAPP_HOST_PORT"
      fi
      if [ "$need_remnawave_network" = "yes" ] || [ "$need_autoupdate_network" = "yes" ]; then
        printf '    networks:\n'
        [ "$need_remnawave_network" = "yes" ] && printf '      - remnawave-network\n'
        [ "$need_autoupdate_network" = "yes" ] && printf '      - autoupdate\n'
      fi
    else
      printf '  bot: {}\n'
    fi
    if [ -n "$WATCHTOWER_URL" ]; then
      printf '\n'
      printf '  watchtower:\n'
      printf '    image: containrrr/watchtower:latest\n'
      printf '    pull_policy: always\n'
      printf '    container_name: remnaWake-watchtower\n'
      printf '    restart: unless-stopped\n'
      printf '    environment:\n'
      printf '      DOCKER_API_VERSION: "1.40"\n'
      printf '      WATCHTOWER_HTTP_API_UPDATE: "true"\n'
      printf '      WATCHTOWER_HTTP_API_TOKEN: "${WATCHTOWER_TOKEN}"\n'
      printf '      WATCHTOWER_SCOPE: "remnawake"\n'
      printf '    volumes:\n'
      printf '      - /var/run/docker.sock:/var/run/docker.sock\n'
      printf '    networks:\n'
      printf '      - autoupdate\n'
    fi
    if [ -n "$XRAY_CHECKER_SUB_URL" ]; then
      printf '\n'
      printf '  xray-checker:\n'
      printf '    image: kutovoys/xray-checker:latest\n'
      printf '    pull_policy: always\n'
      printf '    container_name: remnaWake-xray-checker\n'
      printf '    restart: unless-stopped\n'
      printf '    environment:\n'
      printf '      SUBSCRIPTION_URL: "${XRAY_CHECKER_SUB_URL}"\n'
      printf '      PROXY_CHECK_METHOD: "${XRAY_CHECKER_METHOD}"\n'
      printf '      METRICS_PROTECTED: "true"\n'
      printf '      METRICS_USERNAME: "${XRAY_CHECKER_USERNAME}"\n'
      printf '      METRICS_PASSWORD: "${XRAY_CHECKER_PASSWORD}"\n'
      printf '      METRICS_PORT: "2112"\n'
      # A non-empty base path serves the dashboard (and /metrics) under a sub-path
      # so it can sit on the Mini App domain; the bot's XRAY_CHECKER_URL includes it.
      if [ "$CHECKER_PUBLIC_ENABLED" = "yes" ] && [ -n "$XRAY_CHECKER_BASE_PATH" ]; then
        printf '      METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"\n'
      fi
      # A host-level reverse proxy reaches the dashboard via a published loopback
      # port (Caddy/alongside-Remnawave uses the container name instead).
      if [ "$CHECKER_PUBLIC_ENABLED" = "yes" ] && [ "$HOST_PROXY_ENABLED" = "yes" ]; then
        printf '    ports:\n'
        printf '      - "127.0.0.1:%s:2112"\n' "$XRAY_CHECKER_HOST_PORT"
      fi
      # When the bot has custom networks (watchtower / alongside-remnawave) it is
      # no longer on the compose default network, so the checker must join the
      # same network(s) for the bot to reach xray-checker:2112.
      if [ "$need_remnawave_network" = "yes" ] || [ "$need_autoupdate_network" = "yes" ]; then
        printf '    networks:\n'
        [ "$need_remnawave_network" = "yes" ] && printf '      - remnawave-network\n'
        [ "$need_autoupdate_network" = "yes" ] && printf '      - autoupdate\n'
      fi
    fi
    if [ "$need_remnawave_network" = "yes" ] || [ "$need_autoupdate_network" = "yes" ]; then
      printf '\nnetworks:\n'
      if [ "$need_remnawave_network" = "yes" ]; then
        printf '  remnawave-network:\n'
        printf '    name: remnawave-network\n'
        printf '    external: true\n'
      fi
      if [ "$need_autoupdate_network" = "yes" ]; then
        printf '  autoupdate:\n'
        printf '    driver: bridge\n'
      fi
    fi
  } >"$tmp"
  mv "$tmp" "$OVERRIDE_FILE"
  trap - EXIT
  ok "Wrote installer-managed docker-compose.override.yml"
}

# checker_route_wanted reports whether a same-domain dashboard route should be
# wired into Caddy (exposure on AND served under a sub-path).
checker_route_wanted() {
  [ "$CHECKER_PUBLIC_ENABLED" = "yes" ] && [ -n "$XRAY_CHECKER_BASE_PATH" ]
}

# caddy_checker_block prints the dashboard route lines (4-space indented). The
# sidecar serves the dashboard at "<base>/" and 301-redirects the bare path, and
# the base path is preserved upstream (handle, not handle_path), so it must match
# the sidecar's METRICS_BASE_PATH.
caddy_checker_block() {
  printf '    redir %s %s/\n' "$XRAY_CHECKER_BASE_PATH" "$XRAY_CHECKER_BASE_PATH"
  printf '    handle %s/* {\n        reverse_proxy remnaWake-xray-checker:2112\n    }\n' "$XRAY_CHECKER_BASE_PATH"
}

# inject_caddy_checker_route inserts the dashboard route just inside the existing
# "https://<caddy_host> {" site block. Returns non-zero (leaving the file
# untouched) if that opening line cannot be found, so the caller can fall back to
# printing manual instructions.
inject_caddy_checker_route() {
  local cf="$1" tmp
  tmp="$(mktemp "$cf.XXXXXX")" || return 1
  if awk -v host="$caddy_host" -v base="$XRAY_CHECKER_BASE_PATH" '
    { print }
    !done && index($0, "https://" host) > 0 && $0 ~ /\{[ \t]*$/ {
      print "    redir " base " " base "/"
      print "    handle " base "/* {"
      print "        reverse_proxy remnaWake-xray-checker:2112"
      print "    }"
      done = 1
    }
    END { if (!done) exit 3 }
  ' "$cf" >"$tmp"; then
    backup_file "$cf"
    mv "$tmp" "$cf"
    return 0
  fi
  rm -f "$tmp"
  return 1
}

wire_caddy_if_needed() {
  if [ "$ALONGSIDE_REMNAWAVE" != "yes" ] || [ -z "$caddy_host" ]; then
    return 0
  fi
  CADDYFILE="${REMNAWAVE_CADDYFILE:-/opt/remnawave/caddy/Caddyfile}"
  if [ ! -f "$CADDYFILE" ] || [ ! -w "$CADDYFILE" ]; then
    warn "Remnawave Caddyfile not found or not writable at $CADDYFILE"
    warn "Override the path with REMNAWAVE_CADDYFILE=/path/to/Caddyfile."
    print_caddy_manual
    return 0
  fi
  if grep -q 'reverse_proxy remnaWake-bot:8080' "$CADDYFILE"; then
    ok "Caddyfile already proxies remnaWake-bot:8080; left unchanged."
    # The bot site already exists, but a fresh dashboard route may still be
    # needed inside it — inject it rather than only printing instructions.
    if checker_route_wanted && ! grep -q 'reverse_proxy remnaWake-xray-checker:2112' "$CADDYFILE"; then
      if inject_caddy_checker_route "$CADDYFILE"; then
        ok "Added the ${XRAY_CHECKER_BASE_PATH} dashboard route to the existing $caddy_host site."
        warn "Reload Caddy: cd \"$(dirname "$CADDYFILE")\" && docker compose restart caddy"
      else
        warn "Could not edit the $caddy_host site automatically. Add this inside it (before reverse_proxy remnaWake-bot:8080):"
        caddy_checker_block | while IFS= read -r line; do warn "    $line"; done
      fi
    fi
    return 0
  fi
  if ask_yes_no "Append a Caddy site for $caddy_host to $CADDYFILE?" "y"; then
    backup_file "$CADDYFILE"
    {
      printf '\nhttps://%s {\n' "$caddy_host"
      checker_route_wanted && caddy_checker_block
      printf '    reverse_proxy remnaWake-bot:8080\n}\n'
    } >>"$CADDYFILE"
    ok "Added the bot site to $CADDYFILE."
    warn "Make sure $caddy_host resolves to this server, then reload Caddy:"
    warn "  cd \"$(dirname "$CADDYFILE")\" && docker compose restart caddy"
  else
    print_caddy_manual
  fi
}

detect_compose() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    printf 'docker compose'
  elif command -v docker-compose >/dev/null 2>&1; then
    printf 'docker-compose'
  else
    return 1
  fi
}

service_image() {
  local file="$1" service="$2"
  [ -f "$file" ] || return 1
  awk -v svc="$service" '
    { sub(/\r$/, "") }
    $0 == "services:" { in_services=1; next }
    in_services && $0 ~ /^[^ ]/ { in_services=0 }
    in_services && $0 == "  " svc ":" { in_service=1; next }
    in_service && $0 ~ /^  [^ ].*:/ { in_service=0 }
    in_service && $0 ~ /^    image:[[:space:]]*/ {
      sub(/^    image:[[:space:]]*/, "")
      gsub(/"/, "")
      print
      exit
    }
  ' "$file"
}

compose_image() {
  service_image "$COMPOSE_FILE" bot
}

override_image() {
  service_image "$OVERRIDE_FILE" bot
}

mask() {
  local s="$1"
  [ "${#s}" -le 8 ] && { printf '****'; return; }
  printf '%s...%s' "${s:0:4}" "${s: -4}"
}

print_summary() {
  local platega_summary="disabled (P2P only)" stars_summary="disabled" trial_summary="disabled" referral_summary="disabled"
  local web_host_summary="not published" xray_checker_summary="disabled"
  [ -n "$XRAY_CHECKER_URL" ] && xray_checker_summary="enabled (poll $XRAY_CHECKER_POLL_INTERVAL)"
  [ -n "$PLATEGA_MERCHANT_ID" ] && platega_summary="enabled ($PLATEGA_METHOD, $PLATEGA_CURRENCY)"
  [ "$TELEGRAM_STARS_ENABLED" = "true" ] && stars_summary="enabled (rate $TELEGRAM_STARS_RATE)"
  [ "$TRIAL_ENABLED" = "true" ] && trial_summary="enabled (${TRIAL_DAYS}d, ${TRIAL_TRAFFIC_LIMIT_GB}GB, ${TRIAL_HWID_DEVICE_LIMIT} device limit)"
  [ "$REFERRAL_ENABLED" = "true" ] && referral_summary="enabled (inviter +${REFERRAL_INVITER_BONUS_DAYS}d, invitee +${REFERRAL_INVITEE_BONUS_DAYS}d)"
  [ "$HOST_PROXY_ENABLED" = "yes" ] && web_host_summary="127.0.0.1:${WEBAPP_HOST_PORT} -> container:8080"
  [ "$ALONGSIDE_REMNAWAVE" = "yes" ] && web_host_summary="not published; Caddy uses remnaWake-bot:8080"

  cat >&2 <<EOF

${BOLD}Summary${RESET}
  Panel URL          : $REMNAWAVE_BASE_URL
  Remnawave token    : $(mask "$REMNAWAVE_API_TOKEN")
  Bot token          : $(mask "$TELEGRAM_BOT_TOKEN")
  Admin ID(s)        : $TELEGRAM_ADMIN_ID
  Timezone / run-at  : $TZ at $RUN_AT
  Parse / log / http : $TELEGRAM_PARSE_MODE / $LOG_LEVEL / $HTTP_TIMEOUT
  Currency / language: $CURRENCY / $BOT_LANG
  Channel            : $REMNAWAKE_CHANNEL
  Image              : $AUTOUPDATE_IMAGE
  Mini App           : ${WEBAPP_URL:-disabled}
  Web host port      : $web_host_summary
  Platega            : $platega_summary
  Telegram Stars     : $stars_summary
  Trial              : $trial_summary
  Referral           : $referral_summary
  Auto-update        : $AUTOUPDATE_ENABLED
  Xray Checker       : $xray_checker_summary
  Proxy dashboard    : ${XRAY_CHECKER_PUBLIC_URL:-disabled}

EOF
}

print_proxy_checklist() {
  if [ "$CHECKER_PUBLIC_ENABLED" = "yes" ]; then
    warn "Xray Checker dashboard ($XRAY_CHECKER_PUBLIC_URL):"
    warn "  - Open it from /admin -> 'Proxy web dashboard', or directly in a browser."
    warn "  - It is behind the checker's basic auth (XRAY_CHECKER_USERNAME / XRAY_CHECKER_PASSWORD)."
    if [ "$HOST_PROXY_ENABLED" = "yes" ]; then
      if [ -n "$XRAY_CHECKER_BASE_PATH" ]; then
        warn "  - nginx (the dashboard lives at ${XRAY_CHECKER_BASE_PATH}/ — bare path 301-redirects):"
        warn "      location = ${XRAY_CHECKER_BASE_PATH} { return 301 ${XRAY_CHECKER_BASE_PATH}/; }"
        warn "      location ${XRAY_CHECKER_BASE_PATH}/ { proxy_pass http://127.0.0.1:${XRAY_CHECKER_HOST_PORT}${XRAY_CHECKER_BASE_PATH}/; }"
      else
        warn "  - Point your reverse proxy ${XRAY_CHECKER_PUBLIC_URL} -> 127.0.0.1:${XRAY_CHECKER_HOST_PORT}."
      fi
    elif [ "$ALONGSIDE_REMNAWAVE" = "yes" ]; then
      warn "  - Caddy route ${XRAY_CHECKER_BASE_PATH}/* -> remnaWake-xray-checker:2112 (added to the bot site above)."
    fi
  fi
  if [ -z "$WEBAPP_URL" ] && [ -z "$PLATEGA_MERCHANT_ID" ]; then
    return 0
  fi
  local webhook_host
  webhook_host="${caddy_host:-$(printf '%s' "$WEBAPP_URL" | sed -E 's#^https?://##; s#/.*$##')}"
  [ -z "$webhook_host" ] && webhook_host="<your-public-host>"
  if [ "$ALONGSIDE_REMNAWAVE" = "yes" ]; then
    warn "Reverse proxy (alongside Remnawave / containerised Caddy):"
    warn "  - docker-compose.override.yml joins the external 'remnawave-network'."
    warn "  - Confirm that this is the network your Remnawave Caddy uses: docker network ls"
    warn "  - Caddy site $webhook_host -> remnaWake-bot:8080."
  else
    warn "Reverse proxy (host-level nginx / standalone Caddy):"
    warn "  - docker-compose.override.yml publishes 127.0.0.1:${WEBAPP_HOST_PORT}:8080."
    warn "  - Point HTTPS proxy ${WEBAPP_URL:-https://$webhook_host} -> 127.0.0.1:${WEBAPP_HOST_PORT}."
  fi
  if [ -n "$PLATEGA_MERCHANT_ID" ]; then
    warn "  - Platega: set the dashboard notification URL to https://$webhook_host/platega/callback"
  fi
}

# ---------------------------------------------------------------------------
# Configuration sections
#
# Each section collects one group of settings into the global variables. They
# are called in order by run_all_sections() for a fresh/guided install, and
# individually by the reconfiguration menu (configure_menu). Optional sections
# keep their own enable/disable prompt so the menu can toggle a feature off.
# ---------------------------------------------------------------------------

section_panel() {
  info "-- Remnawave panel -----------------------------------------"
  warn "  Requires Remnawave panel v3.0.0 or newer."
  warn "  Panel v3 identifies users by a numeric id instead of a uuid, and"
  warn "  remnaWake has no v2 compatibility mode. Upgrade the panel first."
  ask REMNAWAVE_BASE_URL  "Remnawave panel URL (e.g. https://panel.example.com)" "$REMNAWAVE_BASE_URL" v_url
  REMNAWAVE_BASE_URL="${REMNAWAVE_BASE_URL%/}"
  ask REMNAWAVE_API_TOKEN "Remnawave API token (panel -> API tokens)" "$REMNAWAVE_API_TOKEN" "" secret
}

section_telegram() {
  info "-- Telegram -----------------------------------------------"
  ask TELEGRAM_BOT_TOKEN "Telegram bot token (from @BotFather, e.g. 123456789:AA...)" "$TELEGRAM_BOT_TOKEN" v_bot_token secret
  ask TELEGRAM_ADMIN_ID  "Telegram admin user ID(s), comma-separated; 0 to disable" "$TELEGRAM_ADMIN_ID" v_admin_id
}

section_schedule() {
  info "-- Schedule -----------------------------------------------"
  ask TZ     "Timezone (IANA name)" "$TZ" v_tz
  ask RUN_AT "Daily run time (HH:MM, local to the timezone above)" "$RUN_AT" v_time_hhmm
}

section_miniapp() {
  info "-- Telegram Mini App (optional) ---------------------------"
  if ask_yes_no "Enable the Mini App personal cabinet? (needs HTTPS reverse proxy)" "$(nonempty_default "$WEBAPP_URL")"; then
    ask WEBAPP_URL "Public Mini App URL (e.g. https://bot.example.com)" "$WEBAPP_URL" v_https_url
    WEBAPP_URL="${WEBAPP_URL%/}"
  else
    WEBAPP_URL=""
  fi
}

section_platega() {
  info "-- Platega payment gateway (optional) ---------------------"
  warn "Default is manual P2P. Platega adds online SBP/card payments."
  if ask_yes_no "Configure Platega online payments now?" "$(nonempty_default "$PLATEGA_MERCHANT_ID")"; then
    ask PLATEGA_MERCHANT_ID "Platega merchant id" "$PLATEGA_MERCHANT_ID" ""
    ask PLATEGA_SECRET      "Platega secret (X-Secret)" "$PLATEGA_SECRET" "" secret
    ask PLATEGA_METHOD      "Payment method (sbp / card)" "$PLATEGA_METHOD" v_platega_method
    ask PLATEGA_CURRENCY    "Currency code sent to Platega (ISO, e.g. RUB)" "$PLATEGA_CURRENCY" ""
    ask PLATEGA_RETURN_URL  "Return URL after payment (e.g. your bot link)" "$PLATEGA_RETURN_URL" v_url
    PLATEGA_RETURN_URL="${PLATEGA_RETURN_URL%/}"
  else
    PLATEGA_MERCHANT_ID=""
    PLATEGA_SECRET=""
    PLATEGA_METHOD="sbp"
    PLATEGA_CURRENCY="RUB"
    PLATEGA_RETURN_URL="https://t.me"
  fi
}

section_stars() {
  info "-- Telegram Stars (optional) ------------------------------"
  if ask_yes_no "Enable Telegram Stars payments now?" "$(bool_default "$TELEGRAM_STARS_ENABLED")"; then
    TELEGRAM_STARS_ENABLED="true"
    ask TELEGRAM_STARS_RATE "Price units per 1 Star (Stars charged = ceil(price / rate))" "${TELEGRAM_STARS_RATE:-1}" v_posint
  else
    TELEGRAM_STARS_ENABLED="false"
    TELEGRAM_STARS_RATE=""
  fi
}

section_trial() {
  info "-- Free trial (optional) -----------------------------------"
  if ask_yes_no "Enable one-time free trial defaults?" "$(bool_default "$TRIAL_ENABLED")"; then
    TRIAL_ENABLED="true"
    ask TRIAL_DAYS "Trial length in days" "$TRIAL_DAYS" v_posint
    ask TRIAL_TRAFFIC_LIMIT_GB "Trial traffic limit in GB (0 = unlimited)" "$TRIAL_TRAFFIC_LIMIT_GB" v_nonnegint
    ask TRIAL_HWID_DEVICE_LIMIT "Trial device limit (0 = unlimited)" "$TRIAL_HWID_DEVICE_LIMIT" v_nonnegint
    ask_optional TRIAL_SQUAD_UUID "Trial squad UUID (empty = default squad)" "$TRIAL_SQUAD_UUID" ""
  else
    TRIAL_ENABLED="false"
  fi
}

section_referral() {
  info "-- Referral bonus (optional) ------------------------------"
  if ask_yes_no "Enable referral bonus defaults?" "$(bool_default "$REFERRAL_ENABLED")"; then
    REFERRAL_ENABLED="true"
    while true; do
      ask REFERRAL_INVITER_BONUS_DAYS "Bonus days for inviter" "$REFERRAL_INVITER_BONUS_DAYS" v_nonnegint
      ask REFERRAL_INVITEE_BONUS_DAYS "Bonus days for invitee" "$REFERRAL_INVITEE_BONUS_DAYS" v_nonnegint
      if [ "$REFERRAL_INVITER_BONUS_DAYS" -gt 0 ] || [ "$REFERRAL_INVITEE_BONUS_DAYS" -gt 0 ]; then
        break
      fi
      err "  -> At least one referral bonus must be positive."
    done
  else
    REFERRAL_ENABLED="false"
  fi
}

section_proxy() {
  if [ -z "$WEBAPP_URL" ] && [ -z "$PLATEGA_MERCHANT_ID" ]; then
    [ "${MENU_MODE:-0}" = "1" ] && warn "Reverse proxy applies only when the Mini App or Platega is enabled (options 5 and 6)."
    return 0
  fi
  info "-- Reverse proxy ------------------------------------------"
  warn "The Mini App / Platega webhook need the bot web server reachable over HTTPS."
  local alongside_default="n"
  [ "$ALONGSIDE_REMNAWAVE" = "yes" ] && alongside_default="y"
  if ask_yes_no "Is this bot on the same server as Remnawave, behind its containerised Caddy?" "$alongside_default"; then
    ALONGSIDE_REMNAWAVE="yes"
    HOST_PROXY_ENABLED="no"
    print_alongside_warning
    if [ -n "$WEBAPP_URL" ]; then
      caddy_host="$(printf '%s' "$WEBAPP_URL" | sed -E 's#^https?://##; s#/.*$##')"
    else
      ask caddy_host "Public domain for the bot (e.g. bot.example.com)" "$caddy_host" v_domain
    fi
  else
    ALONGSIDE_REMNAWAVE="no"
    HOST_PROXY_ENABLED="yes"
    ask WEBAPP_HOST_PORT "Host port for nginx/Caddy to reach the bot on 127.0.0.1" "$WEBAPP_HOST_PORT" v_port
  fi
}

section_advanced() {
  if ask_yes_no "Configure advanced options (parse mode, log level, timeout, dry-run, currency, language, win-back)?" "n"; then
    info "-- Advanced -----------------------------------------------"
    ask TELEGRAM_PARSE_MODE "Telegram parse mode (HTML / MarkdownV2)" "$TELEGRAM_PARSE_MODE" ""
    ask LOG_LEVEL           "Log level (debug/info/warn/error)" "$LOG_LEVEL" v_loglevel
    ask HTTP_TIMEOUT        "HTTP timeout (Go duration, e.g. 15s)" "$HTTP_TIMEOUT" v_duration
    ask DRY_RUN             "Dry run? log instead of sending (true/false)" "$DRY_RUN" v_bool
    ask RUN_ON_START        "Run once immediately on start (true/false)" "$RUN_ON_START" v_bool
    ask CURRENCY            "Currency label shown next to tariff prices" "$CURRENCY" ""
    ask BOT_LANG            "Bot language (ru / en)" "$BOT_LANG" v_lang
    ask WINBACK_ENABLED     "Send win-back messages to expired users (true/false)" "$WINBACK_ENABLED" v_bool
    ask WINBACK_DAYS        "Days after expiry to send win-back (comma-separated, e.g. 1,3)" "$WINBACK_DAYS" v_days_list
  fi
}

section_autoupdate() {
  if ask_yes_no "Enable auto-update notifications (DM admins when a new bot version is released)?" "$(bool_default "$AUTOUPDATE_ENABLED")"; then
    AUTOUPDATE_ENABLED="true"
    ask AUTOUPDATE_IMAGE "Image to check for updates" "$AUTOUPDATE_IMAGE" ""
    ask AUTOUPDATE_CHECK_INTERVAL "Check for updates every (Go duration, e.g. 6h)" "$AUTOUPDATE_CHECK_INTERVAL" v_duration
    if ask_yes_no "Enable one-tap install via a Watchtower sidecar?" "$(nonempty_default "$WATCHTOWER_URL")"; then
      WATCHTOWER_URL="http://watchtower:8080"
      [ -n "$WATCHTOWER_TOKEN" ] || WATCHTOWER_TOKEN="$(generate_token)"
      info "One-tap install enabled. docker-compose.override.yml will include Watchtower."
    else
      WATCHTOWER_URL=""
      WATCHTOWER_TOKEN=""
    fi
  else
    AUTOUPDATE_ENABLED="false"
    WATCHTOWER_URL=""
    WATCHTOWER_TOKEN=""
  fi
}

section_xray() {
  info "-- Xray Checker proxy monitoring (optional) ---------------"
  warn "Runs a kutovoys/xray-checker sidecar that probes each proxy and reports"
  warn "health. The bot shows it in /admin and DMs you when a proxy goes down."
  if ask_yes_no "Enable Xray Checker proxy monitoring?" "$(nonempty_default "$XRAY_CHECKER_URL")"; then
    ask XRAY_CHECKER_SUB_URL "Subscription URL for the checker to monitor" "$XRAY_CHECKER_SUB_URL" v_url
    XRAY_CHECKER_URL="http://xray-checker:2112"
    XRAY_CHECKER_METHOD="${XRAY_CHECKER_METHOD:-ip}"
    [ -n "$XRAY_CHECKER_USERNAME" ] || XRAY_CHECKER_USERNAME="metrics"
    [ -n "$XRAY_CHECKER_PASSWORD" ] || XRAY_CHECKER_PASSWORD="$(generate_token)"
    ask XRAY_CHECKER_POLL_INTERVAL "Poll interval (Go duration, e.g. 2m)" "$XRAY_CHECKER_POLL_INTERVAL" v_duration
    info "Xray Checker enabled. docker-compose.override.yml will include the sidecar."

    # The checker ships its own roomy web dashboard (handier than the compact
    # Mini App tab). Optionally expose it publicly so admins can open it from the
    # bot or a browser. With METRICS_PROTECTED=true it sits behind the same basic
    # auth as /metrics, so it stays admin-only.
    printf '\n' >&2
    if ask_yes_no "Expose the Xray Checker web dashboard at a public URL?" "$(nonempty_default "$XRAY_CHECKER_PUBLIC_URL")"; then
      CHECKER_PUBLIC_ENABLED="yes"
      if [ -n "$WEBAPP_URL" ]; then
        # Same domain as the Mini App: serve the dashboard under a sub-path. The
        # sidecar's METRICS_BASE_PATH relocates every endpoint (dashboard AND
        # /metrics) under this prefix, so the bot's internal poll URL must include
        # it too.
        [ -n "$XRAY_CHECKER_BASE_PATH" ] || XRAY_CHECKER_BASE_PATH="/checker"
        ask XRAY_CHECKER_BASE_PATH "Dashboard path under ${WEBAPP_URL}" "$XRAY_CHECKER_BASE_PATH" v_path
        XRAY_CHECKER_BASE_PATH="${XRAY_CHECKER_BASE_PATH%/}"
        # The sidecar serves the dashboard at the base path WITH a trailing slash
        # (the bare path 301-redirects), so the public URL points at "<base>/".
        XRAY_CHECKER_PUBLIC_URL="${WEBAPP_URL%/}${XRAY_CHECKER_BASE_PATH}/"
        XRAY_CHECKER_URL="http://xray-checker:2112${XRAY_CHECKER_BASE_PATH}"
        if [ "$HOST_PROXY_ENABLED" = "yes" ]; then
          ask XRAY_CHECKER_HOST_PORT "Host port for your nginx/Caddy to reach the checker on 127.0.0.1" "$XRAY_CHECKER_HOST_PORT" v_port
        fi
      else
        # No Mini App: the admin picks their own public URL; the dashboard is
        # served at that host's root (no base path).
        ask XRAY_CHECKER_PUBLIC_URL "Public URL for the dashboard (e.g. https://checker.example.com)" "$XRAY_CHECKER_PUBLIC_URL" v_https_url
        XRAY_CHECKER_PUBLIC_URL="${XRAY_CHECKER_PUBLIC_URL%/}"
        XRAY_CHECKER_BASE_PATH=""
        XRAY_CHECKER_URL="http://xray-checker:2112"
        ask XRAY_CHECKER_HOST_PORT "Host port for your reverse proxy to reach the checker on 127.0.0.1" "$XRAY_CHECKER_HOST_PORT" v_port
      fi
      info "Dashboard exposed at: $XRAY_CHECKER_PUBLIC_URL"
    else
      CHECKER_PUBLIC_ENABLED="no"
      XRAY_CHECKER_PUBLIC_URL=""
      XRAY_CHECKER_BASE_PATH=""
    fi
  else
    XRAY_CHECKER_URL=""
    XRAY_CHECKER_SUB_URL=""
    XRAY_CHECKER_USERNAME=""
    XRAY_CHECKER_PASSWORD=""
    CHECKER_PUBLIC_ENABLED="no"
    XRAY_CHECKER_PUBLIC_URL=""
    XRAY_CHECKER_BASE_PATH=""
  fi
}

# Walk every section in the canonical order. Used for first-time and scripted
# (non-interactive) installs. section_proxy self-skips when neither the Mini App
# nor Platega is enabled, so its prompts only appear when relevant.
run_all_sections() {
  select_release_channel
  printf '\n' >&2
  section_panel
  printf '\n' >&2
  section_telegram
  printf '\n' >&2
  section_schedule
  printf '\n' >&2
  section_miniapp
  printf '\n' >&2
  section_platega
  printf '\n' >&2
  section_stars
  printf '\n' >&2
  section_trial
  printf '\n' >&2
  section_referral
  if [ -n "$WEBAPP_URL" ] || [ -n "$PLATEGA_MERCHANT_ID" ]; then
    printf '\n' >&2
    section_proxy
  fi
  printf '\n' >&2
  section_advanced
  printf '\n' >&2
  section_autoupdate
  printf '\n' >&2
  section_xray
}

# Write the .env / compose files from the current globals, then offer to start.
# Backs up any existing files first so a rewrite is always recoverable.
apply_config() {
  [ -f "$ENV_FILE" ] && backup_file "$ENV_FILE"
  [ -f "$OVERRIDE_FILE" ] && backup_file "$OVERRIDE_FILE"

  ensure_compose_file
  write_env_file
  write_override_file
  wire_caddy_if_needed
  print_summary
  print_proxy_checklist

  if [ -n "$PLATEGA_MERCHANT_ID" ]; then
    warn "Platega: the bot starts on P2P; enable Platega from /admin or the Mini App."
  fi

  local compose
  if ! compose="$(detect_compose)"; then
    warn "Docker Compose was not found on this machine."
    warn "Install Docker Engine + Compose, then run: docker compose up -d"
    ok "Configuration is ready."
    return 0
  fi

  if command -v docker >/dev/null 2>&1 && ! docker info >/dev/null 2>&1; then
    warn "Docker is installed but not accessible by this user."
    warn "You may need sudo or docker group membership for the start step."
  fi

  printf '\n' >&2
  if ask_yes_no "Pull the pre-built image and start the bot now with '$compose up -d'?" "y"; then
    info "Pulling image and starting..."
    $compose up -d
    printf '\n' >&2
    ok "Bot is up. Useful commands:"
    printf '%s\n' "  ${DIM}$compose logs -f${RESET}                 # follow logs" >&2
    printf '%s\n' "  ${DIM}$compose ps${RESET}                      # status" >&2
    printf '%s\n' "  ${DIM}./install.sh doctor${RESET}              # check setup" >&2
    printf '%s\n' "  ${DIM}./install.sh update${RESET}              # pull and restart" >&2
  else
    ok "Skipped startup. When ready, run: $compose up -d"
  fi
}

# ---------------------------------------------------------------------------
# Reconfiguration menu
#
# Shown for interactive runs when an .env already exists, so a single setting
# can be changed without walking every prompt. Edits stay in memory until the
# user saves; saving runs apply_config (which writes files and offers to start).
# ---------------------------------------------------------------------------

masked_or_unset() {
  [ -n "$1" ] && mask "$1" || printf '<not set>'
}

# Recover the reverse-proxy mode from the existing override so saving from the
# menu without re-visiting that section preserves the current wiring.
infer_proxy_state() {
  ALONGSIDE_REMNAWAVE="no"
  HOST_PROXY_ENABLED="no"
  caddy_host=""
  [ -f "$OVERRIDE_FILE" ] || return 0
  if grep -q 'remnawave-network' "$OVERRIDE_FILE" 2>/dev/null; then
    ALONGSIDE_REMNAWAVE="yes"
    [ -n "$WEBAPP_URL" ] && caddy_host="$(printf '%s' "$WEBAPP_URL" | sed -E 's#^https?://##; s#/.*$##')"
  fi
  if grep -Eq '127\.0\.0\.1:[0-9]+:8080' "$OVERRIDE_FILE" 2>/dev/null; then
    HOST_PROXY_ENABLED="yes"
  fi
}

menu_proxy_summary() {
  if [ "$ALONGSIDE_REMNAWAVE" = "yes" ]; then
    printf 'alongside Remnawave (Caddy -> remnaWake-bot:8080)'
  elif [ "$HOST_PROXY_ENABLED" = "yes" ]; then
    printf '127.0.0.1:%s -> container:8080' "$WEBAPP_HOST_PORT"
  elif [ -n "$WEBAPP_URL" ] || [ -n "$PLATEGA_MERCHANT_ID" ]; then
    printf 'not configured yet'
  else
    printf 'n/a (enable Mini App or Platega first)'
  fi
}

print_menu() {
  local platega stars trial referral autoupdate xray proxy panel bot adv dirty_note
  platega="disabled (P2P only)"
  [ -n "$PLATEGA_MERCHANT_ID" ] && platega="enabled ($PLATEGA_METHOD, $PLATEGA_CURRENCY)"
  stars="disabled"
  [ "$TELEGRAM_STARS_ENABLED" = "true" ] && stars="enabled (rate $TELEGRAM_STARS_RATE)"
  trial="disabled"
  [ "$TRIAL_ENABLED" = "true" ] && trial="enabled (${TRIAL_DAYS}d, ${TRIAL_TRAFFIC_LIMIT_GB}GB, ${TRIAL_HWID_DEVICE_LIMIT} device)"
  referral="disabled"
  [ "$REFERRAL_ENABLED" = "true" ] && referral="enabled (inviter +${REFERRAL_INVITER_BONUS_DAYS}d, invitee +${REFERRAL_INVITEE_BONUS_DAYS}d)"
  autoupdate="disabled"
  if [ "$AUTOUPDATE_ENABLED" = "true" ]; then
    autoupdate="notify-only (every $AUTOUPDATE_CHECK_INTERVAL)"
    [ -n "$WATCHTOWER_URL" ] && autoupdate="one-tap (every $AUTOUPDATE_CHECK_INTERVAL)"
  fi
  xray="disabled"
  [ -n "$XRAY_CHECKER_URL" ] && xray="enabled (poll $XRAY_CHECKER_POLL_INTERVAL)"
  proxy="$(menu_proxy_summary)"
  panel="${REMNAWAVE_BASE_URL:-<not set>} (token $(masked_or_unset "$REMNAWAVE_API_TOKEN"))"
  bot="token $(masked_or_unset "$TELEGRAM_BOT_TOKEN"), admin(s) $TELEGRAM_ADMIN_ID"
  adv="parse $TELEGRAM_PARSE_MODE, log $LOG_LEVEL, http $HTTP_TIMEOUT, $CURRENCY/$BOT_LANG, win-back $WINBACK_ENABLED"
  dirty_note=""
  [ "$CONFIG_DIRTY" = "1" ] && dirty_note=" ${YELLOW}(unsaved changes)${RESET}"

  cat >&2 <<EOF

${BOLD}remnaWake configuration menu${RESET}
${DIM}Pick a number to edit that section. Changes are held until you Save.${RESET}

  ${BOLD} 1${RESET}) Release channel    ${DIM}${REMNAWAKE_CHANNEL}${RESET}
  ${BOLD} 2${RESET}) Remnawave panel    ${DIM}${panel}${RESET}
  ${BOLD} 3${RESET}) Telegram bot       ${DIM}${bot}${RESET}
  ${BOLD} 4${RESET}) Schedule           ${DIM}${TZ} at ${RUN_AT}${RESET}
  ${BOLD} 5${RESET}) Mini App           ${DIM}${WEBAPP_URL:-disabled}${RESET}
  ${BOLD} 6${RESET}) Platega payments   ${DIM}${platega}${RESET}
  ${BOLD} 7${RESET}) Telegram Stars     ${DIM}${stars}${RESET}
  ${BOLD} 8${RESET}) Free trial         ${DIM}${trial}${RESET}
  ${BOLD} 9${RESET}) Referral bonus     ${DIM}${referral}${RESET}
  ${BOLD}10${RESET}) Reverse proxy      ${DIM}${proxy}${RESET}
  ${BOLD}11${RESET}) Advanced options   ${DIM}${adv}${RESET}
  ${BOLD}12${RESET}) Auto-update        ${DIM}${autoupdate}${RESET}
  ${BOLD}13${RESET}) Xray Checker       ${DIM}${xray}${RESET}

  ${BOLD} A${RESET}) Reconfigure everything (guided run-through)
  ${BOLD} S${RESET}) Save and apply changes
  ${BOLD} Q${RESET}) Quit${dirty_note}
EOF
}

# True (0) when the required keys are present, otherwise prints what is missing.
menu_required_ok() {
  local missing=0
  [ -n "$REMNAWAVE_BASE_URL" ]   || { err "  -> Missing: Remnawave panel URL (option 2)"; missing=1; }
  [ -n "$REMNAWAVE_API_TOKEN" ]  || { err "  -> Missing: Remnawave API token (option 2)"; missing=1; }
  [ -n "$TELEGRAM_BOT_TOKEN" ]   || { err "  -> Missing: Telegram bot token (option 3)"; missing=1; }
  [ "$missing" -eq 0 ]
}

menu_save() {
  if ! menu_required_ok; then
    err "Cannot save until the required values above are set."
    return 1
  fi
  apply_config
  CONFIG_DIRTY=0
  ok "Saved. Pick another option to keep editing, or Q to quit."
}

menu_quit() {
  if [ "$CONFIG_DIRTY" = "1" ]; then
    if ask_yes_no "You have unsaved changes. Save them before quitting?" "y"; then
      menu_save || return 1
    else
      warn "Quit without saving; .env left unchanged."
    fi
  fi
  return 0
}

configure_menu() {
  MENU_MODE=1
  CONFIG_DIRTY=0
  infer_proxy_state

  cat >&2 <<EOF
${BOLD}remnaWake configuration${RESET}
${DIM}An existing .env was found. Reconfigure individual sections from the menu;${RESET}
${DIM}nothing is written until you choose Save.${RESET}
EOF

  local choice
  while true; do
    print_menu
    printf '%s' "${BOLD}Choose an option: ${RESET}" >&2
    read -r choice || choice="Q"
    choice="$(printf '%s' "$choice" | tr -d '[:space:]')"
    case "$choice" in
      1)  select_release_channel; CONFIG_DIRTY=1 ;;
      2)  section_panel;     CONFIG_DIRTY=1 ;;
      3)  section_telegram;  CONFIG_DIRTY=1 ;;
      4)  section_schedule;  CONFIG_DIRTY=1 ;;
      5)  section_miniapp;   CONFIG_DIRTY=1 ;;
      6)  section_platega;   CONFIG_DIRTY=1 ;;
      7)  section_stars;     CONFIG_DIRTY=1 ;;
      8)  section_trial;     CONFIG_DIRTY=1 ;;
      9)  section_referral;  CONFIG_DIRTY=1 ;;
      10) section_proxy;     CONFIG_DIRTY=1 ;;
      11) section_advanced;  CONFIG_DIRTY=1 ;;
      12) section_autoupdate; CONFIG_DIRTY=1 ;;
      13) section_xray;      CONFIG_DIRTY=1 ;;
      a|A) run_all_sections; CONFIG_DIRTY=1 ;;
      s|S) menu_save || true ;;
      q|Q) if menu_quit; then return 0; fi ;;
      "")  : ;;
      *)   warn "Unknown option: $choice" ;;
    esac
  done
}

configure_mode() {
  load_defaults

  if [ -t 0 ] && [ -f "$ENV_FILE" ]; then
    configure_menu
    return $?
  fi

  cat >&2 <<EOF
${BOLD}remnaWake installer${RESET}
${DIM}This will collect the settings the bot needs and write them to ./.env.${RESET}
${DIM}Existing .env values are reused as defaults; backups are kept before rewrites.${RESET}

EOF

  run_all_sections
  apply_config
}

menu_mode() {
  load_defaults
  configure_menu
}

port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | grep -Eq "[:.]${port}[[:space:]]" && return 0
  elif command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && return 0
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | grep -Eq "[:.]${port}[[:space:]]" && return 0
  fi
  return 1
}

doctor_mode() {
  local failures=0 warnings=0 compose value expected base_image autoupdate_image effective_image override_bot_image web_port mode
  local web_url platega_id topology xray_url xray_sub xray_base xray_host_port xray_public caddyfile
  local backup_count latest backup_name
  local remote_installer local_digest remote_digest

  doctor_section() { info ""; info "$*"; }
  doctor_finding() {
    local status="$1" label="$2" detected="${3:-not checked}" expected="${4:-not specified}" fix="${5:-no action needed}"
    case "$status" in
      OK)
        ok "OK: $label"
        printf '  Detected: %s\n  Expected: %s\n  Likely fix: %s\n' "$detected" "$expected" "$fix"
        ;;
      WARN)
        warn "WARN: $label"
        printf '  Detected: %s\n  Expected: %s\n  Likely fix: %s\n' "$detected" "$expected" "$fix" >&2
        warnings=$((warnings + 1))
        ;;
      FAIL)
        err "FAIL: $label"
        printf '  Detected: %s\n  Expected: %s\n  Likely fix: %s\n' "$detected" "$expected" "$fix" >&2
        failures=$((failures + 1))
        ;;
    esac
  }
  check_ok() { doctor_finding OK "$@"; }
  check_warn() { doctor_finding WARN "$@"; }
  check_fail() { doctor_finding FAIL "$@"; }
  has_override() { [ -f "$OVERRIDE_FILE" ] && grep -q "$1" "$OVERRIDE_FILE" 2>/dev/null; }
  latest_backup() { ls -1t "$BACKUP_DIR/$1".* 2>/dev/null | sed -n '1p'; }

  REMNAWAKE_CHANNEL="$(env_default REMNAWAKE_CHANNEL "${REMNAWAKE_CHANNEL:-main}")"
  v_channel "$REMNAWAKE_CHANNEL" >/dev/null 2>&1 || REMNAWAKE_CHANNEL="main"
  apply_channel_defaults

  info "remnaWake doctor v2"

  doctor_section "Environment"
  value="$REMNAWAKE_CHANNEL"
  if v_channel "$value" >/dev/null 2>&1; then
    check_ok "REMNAWAKE_CHANNEL" "$value" "main or dev" "no action needed"
  else
    check_fail "REMNAWAKE_CHANNEL" "$value" "main or dev" "./install.sh configure"
  fi

  [ -f "$ENV_FILE" ] && check_ok ".env file" "$ENV_FILE" "present" "no action needed" || check_fail ".env file" "missing" "present" "./install.sh configure"
  [ -f "$COMPOSE_FILE" ] && check_ok "docker-compose.yml file" "$COMPOSE_FILE" "present" "no action needed" || check_fail "docker-compose.yml file" "missing" "present" "./install.sh configure"
  [ -f "$OVERRIDE_FILE" ] && check_ok "docker-compose.override.yml file" "$OVERRIDE_FILE" "present when installer-managed settings are used" "no action needed" || check_warn "docker-compose.override.yml file" "missing" "installer-managed override present" "./install.sh configure"

  for key in REMNAWAVE_BASE_URL REMNAWAVE_API_TOKEN TELEGRAM_BOT_TOKEN; do
    # The panel version cannot be probed offline, so it is stated as an
    # expectation rather than checked: remnaWake has no v2 compatibility mode.
    expected="non-empty"
    [ "$key" = "REMNAWAVE_BASE_URL" ] && expected="non-empty; panel must be Remnawave v3.0.0 or newer"
    if value="$(env_get "$key")" && [ -n "$value" ]; then
      check_ok "$key" "set" "$expected" "no action needed"
    else
      check_fail "$key" "missing or empty" "$expected" "./install.sh configure"
    fi
  done

  if [ -f "$ENV_FILE" ]; then
    mode=""
    if mode="$(stat -c %a "$ENV_FILE" 2>/dev/null)"; then
      [ "$mode" = "600" ] && check_ok ".env permissions" "$mode" "600" "no action needed" || check_warn ".env permissions" "$mode" "600" "chmod 600 .env"
    else
      check_warn ".env permissions" "not available on this platform" "600 on Linux hosts" "inspect permissions manually"
    fi
  fi

  # A stale installer is worth reporting before anything else: its own checks and
  # its override generation lag the running bot, so it can both miss real
  # problems and reintroduce ones already fixed upstream.
  doctor_section "Installer"
  if [ -z "$SCRIPT_FILE" ]; then
    check_warn "install.sh freshness" "running from a pipe, not a file" "run from ./install.sh so the version can be compared" "curl -fsSL $REPO_RAW/install.sh -o install.sh && chmod +x install.sh"
  elif remote_installer="$(fetch_remote_installer)"; then
    local_digest="$(file_digest "$SCRIPT_FILE" 2>/dev/null || true)"
    remote_digest="$(file_digest "$remote_installer" 2>/dev/null || true)"
    rm -f "$remote_installer"
    if [ -z "$local_digest" ] || [ -z "$remote_digest" ]; then
      check_warn "install.sh freshness" "cannot hash files" "sha256sum or shasum available" "install coreutils, or re-download install.sh manually"
    elif [ "$local_digest" = "$remote_digest" ]; then
      check_ok "install.sh freshness" "matches the $REMNAWAKE_CHANNEL channel" "same as $REPO_RAW/install.sh" "no action needed"
    else
      check_warn "install.sh freshness" "differs from the $REMNAWAKE_CHANNEL channel (local ${local_digest:0:12}, remote ${remote_digest:0:12})" "same as $REPO_RAW/install.sh" "./install.sh update refreshes it, or re-download it manually"
    fi
  else
    check_warn "install.sh freshness" "could not download the channel version" "reachable $REPO_RAW/install.sh" "check network access to GitHub; re-run later"
  fi

  doctor_section "Docker"
  if command -v docker >/dev/null 2>&1; then
    check_ok "docker command" "$(command -v docker)" "docker CLI installed" "no action needed"
    if docker info >/dev/null 2>&1; then
      check_ok "Docker daemon" "accessible" "accessible to this user" "no action needed"
    else
      check_warn "Docker daemon" "docker CLI exists but daemon is not accessible" "docker info succeeds" "start Docker and ensure this user can access it"
    fi
  else
    check_fail "docker command" "not found" "docker CLI installed" "install Docker Engine"
  fi

  if compose="$(detect_compose)"; then
    check_ok "Docker Compose" "$compose" "docker compose or docker-compose available" "no action needed"
    if [ -f "$COMPOSE_FILE" ] && $compose config >/dev/null 2>&1; then
      check_ok "compose config" "valid" "docker compose config succeeds" "no action needed"
    else
      check_fail "compose config" "failed" "docker compose config succeeds" "./install.sh configure"
    fi
  else
    check_fail "Docker Compose" "not found" "docker compose or docker-compose available" "install Docker Compose"
  fi

  doctor_section "Routes and ports"
  web_port="$(env_default WEBAPP_HOST_PORT "8080")"
  web_url="$(env_default WEBAPP_URL "")"
  platega_id="$(env_default PLATEGA_MERCHANT_ID "")"
  topology="none"
  if has_override 'remnawave-network'; then
    topology="alongside-remnawave"
  elif [ -n "$web_url" ] || [ -n "$platega_id" ] || has_override "127.0.0.1:${web_port}:8080"; then
    topology="host-reverse-proxy"
  fi
  check_ok "detected topology" "$topology" "host-reverse-proxy, alongside-remnawave, or none" "no action needed"

  if v_port "$web_port" >/dev/null 2>&1; then
    check_ok "WEBAPP_HOST_PORT" "$web_port" "valid TCP port" "no action needed"
    if port_in_use "$web_port"; then
      check_warn "WEBAPP_HOST_PORT listener" "port $web_port is listening" "free port unless remnaWake is already running" "docker compose ps; if another service owns it, run ./install.sh configure"
    else
      check_ok "WEBAPP_HOST_PORT listener" "port $web_port is not listening" "free before startup" "no action needed"
    fi
  else
    check_fail "WEBAPP_HOST_PORT" "$web_port" "valid TCP port" "./install.sh configure"
  fi

  if [ "$topology" = "host-reverse-proxy" ]; then
    if has_override "127.0.0.1:${web_port}:8080"; then
      check_ok "host proxy route" "127.0.0.1:${web_port}:8080 in override" "host reverse proxy targets 127.0.0.1:${web_port}:8080" "no action needed"
    else
      check_fail "host proxy route" "missing 127.0.0.1:${web_port}:8080 in override" "host reverse proxy targets 127.0.0.1:${web_port}:8080" "./install.sh configure"
    fi
  elif [ "$topology" = "alongside-remnawave" ]; then
    has_override 'external: true' && check_ok "remnawave network" "external remnawave-network in override" "bot joins external remnawave-network" "no action needed" || check_fail "remnawave network" "remnawave-network is not marked external" "bot joins external remnawave-network" "./install.sh configure"
    caddyfile="${REMNAWAVE_CADDYFILE:-/opt/remnawave/caddy/Caddyfile}"
    if [ -f "$caddyfile" ]; then
      grep -q 'reverse_proxy remnaWake-bot:8080' "$caddyfile" 2>/dev/null && check_ok "Caddy bot route" "$caddyfile routes to remnaWake-bot:8080" "Caddy routes Mini App host to remnaWake-bot:8080" "no action needed" || check_warn "Caddy bot route" "$caddyfile has no remnaWake-bot:8080 route" "Caddy routes Mini App host to remnaWake-bot:8080" "./install.sh configure"
    else
      check_warn "Caddy bot route" "$caddyfile not readable" "Caddy routes Mini App host to remnaWake-bot:8080" "inspect Remnawave Caddyfile or rerun ./install.sh configure"
    fi
  fi

  doctor_section "Generated compose"
  if [ -f "$COMPOSE_FILE" ]; then
    base_image="$(compose_image || true)"
    if grep -q 'Generated by install.sh' "$COMPOSE_FILE" 2>/dev/null; then
      check_warn "base compose ownership" "installer header found in docker-compose.yml" "base compose stays generic; generated data lives in override" "./install.sh configure"
    else
      check_ok "base compose ownership" "no installer header in docker-compose.yml" "base compose stays generic; generated data lives in override" "no action needed"
    fi
    case "$base_image" in
      *remnawake*) check_ok "base bot image" "$base_image" "remnaWake image in bot service" "no action needed" ;;
      "") check_warn "base bot image" "not detected" "remnaWake image in bot service" "./install.sh configure" ;;
      *) check_warn "base bot image" "$base_image" "remnaWake image in bot service" "./install.sh configure" ;;
    esac
  else
    base_image=""
  fi
  if [ -f "$OVERRIDE_FILE" ]; then
    grep -q '^# Generated by install.sh' "$OVERRIDE_FILE" 2>/dev/null && check_ok "override header" "installer header present" "override is installer-managed" "no action needed" || check_warn "override header" "installer header missing" "override is installer-managed" "./install.sh configure"
    autoupdate_image="$(env_default AUTOUPDATE_IMAGE "$DEFAULT_DEPLOY_IMAGE")"
    override_bot_image="$(override_image || true)"
    effective_image="$override_bot_image"
    [ -n "$effective_image" ] || effective_image="$base_image"
    if [ -n "$base_image" ] && [ "$autoupdate_image" != "$base_image" ]; then
      [ "$override_bot_image" = "$autoupdate_image" ] && check_ok "bot image override" "$override_bot_image" "override bot image equals AUTOUPDATE_IMAGE when it differs from base" "no action needed" || check_warn "bot image override" "${override_bot_image:-missing}" "override bot image equals AUTOUPDATE_IMAGE when it differs from base" "./install.sh configure"
    else
      [ -z "$override_bot_image" ] && check_ok "bot image override" "not present" "no bot image override when AUTOUPDATE_IMAGE matches base" "no action needed" || check_warn "bot image override" "$override_bot_image" "no bot image override when AUTOUPDATE_IMAGE matches base" "./install.sh configure"
    fi
    if [ -n "$effective_image" ] && [ "$autoupdate_image" = "$effective_image" ]; then
      check_ok "AUTOUPDATE_IMAGE" "$autoupdate_image" "matches effective bot image $effective_image" "no action needed"
    else
      check_warn "AUTOUPDATE_IMAGE" "${autoupdate_image:-empty} vs ${effective_image:-empty effective image}" "matches effective bot image" "./install.sh configure"
    fi
  fi

  doctor_section "Watchtower"
  value="$(env_default AUTOUPDATE_ENABLED "false")"
  if [ "$value" = "true" ]; then
    value="$(env_default AUTOUPDATE_CHECK_INTERVAL "6h")"
    v_duration "$value" >/dev/null 2>&1 && check_ok "AUTOUPDATE_CHECK_INTERVAL" "$value" "valid duration such as 6h" "no action needed" || check_fail "AUTOUPDATE_CHECK_INTERVAL" "$value" "valid duration such as 6h" "./install.sh configure"
    if [ -n "$(env_default WATCHTOWER_URL "")" ]; then
      [ -n "$(env_default WATCHTOWER_TOKEN "")" ] && check_ok "WATCHTOWER_TOKEN" "set" "non-empty when WATCHTOWER_URL is set" "no action needed" || check_fail "WATCHTOWER_TOKEN" "missing" "non-empty when WATCHTOWER_URL is set" "./install.sh configure"
      if has_override '^  watchtower:'; then
        check_ok "Watchtower service" "present in override" "watchtower sidecar is generated when WATCHTOWER_URL is set" "no action needed"
        has_override 'image: containrrr/watchtower:latest' && check_ok "Watchtower image" "containrrr/watchtower:latest" "containrrr/watchtower:latest" "no action needed" || check_warn "Watchtower image" "missing or stale" "containrrr/watchtower:latest" "./install.sh configure"
        has_override 'pull_policy: always' && check_ok "Watchtower pull policy" "always" "pull_policy: always" "no action needed" || check_warn "Watchtower pull policy" "missing" "pull_policy: always" "./install.sh configure"
        has_override 'DOCKER_API_VERSION: "1.40"' && check_ok "Watchtower Docker API" "1.40" "DOCKER_API_VERSION: \"1.40\"" "no action needed" || check_warn "Watchtower Docker API" "missing" "DOCKER_API_VERSION: \"1.40\"" "./install.sh configure"
      else
        check_fail "Watchtower service" "missing in override" "watchtower sidecar is generated when WATCHTOWER_URL is set" "./install.sh configure"
      fi
    else
      has_override '^  watchtower:' && check_warn "Watchtower service" "present but WATCHTOWER_URL is empty" "no watchtower sidecar for notify-only auto-update" "./install.sh configure" || check_ok "Watchtower service" "not present" "no watchtower sidecar for notify-only auto-update" "no action needed"
    fi
  else
    has_override '^  watchtower:' && check_warn "Watchtower service" "present while AUTOUPDATE_ENABLED is false" "no watchtower sidecar when auto-update is disabled" "./install.sh configure" || check_ok "Auto-update" "disabled" "watchtower sidecar absent unless configured" "no action needed"
  fi

  doctor_section "xray-checker"
  xray_url="$(env_default XRAY_CHECKER_URL "")"
  xray_sub="$(env_default XRAY_CHECKER_SUB_URL "")"
  xray_base="$(env_default XRAY_CHECKER_BASE_PATH "")"
  xray_host_port="$(env_default XRAY_CHECKER_HOST_PORT "2112")"
  xray_public="$(env_default XRAY_CHECKER_PUBLIC_URL "")"
  if [ -n "$xray_sub" ] || [ -n "$xray_url" ]; then
    [ -n "$xray_sub" ] && check_ok "XRAY_CHECKER_SUB_URL" "set" "subscription URL set when checker is enabled" "no action needed" || check_fail "XRAY_CHECKER_SUB_URL" "missing" "subscription URL set when checker is enabled" "./install.sh configure"
    has_override '^  xray-checker:' && check_ok "xray-checker service" "present in override" "sidecar generated when XRAY_CHECKER_SUB_URL is set" "no action needed" || check_fail "xray-checker service" "missing in override" "sidecar generated when XRAY_CHECKER_SUB_URL is set" "./install.sh configure"
    if [ -n "$xray_base" ]; then
      value="http://xray-checker:2112${xray_base}"
      [ "$xray_url" = "$value" ] && check_ok "XRAY_CHECKER_URL base path" "$xray_url" "$value" "no action needed" || check_fail "XRAY_CHECKER_URL base path" "${xray_url:-empty}" "$value" "./install.sh configure"
      has_override 'METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"' && check_ok "xray-checker METRICS_BASE_PATH" 'METRICS_BASE_PATH: "${XRAY_CHECKER_BASE_PATH}"' "set when XRAY_CHECKER_BASE_PATH is non-empty" "no action needed" || check_fail "xray-checker METRICS_BASE_PATH" "missing" "set when XRAY_CHECKER_BASE_PATH is non-empty" "./install.sh configure"
    fi
    if [ "$topology" = "host-reverse-proxy" ] && [ -n "$xray_public" ]; then
      v_port "$xray_host_port" >/dev/null 2>&1 && check_ok "XRAY_CHECKER_HOST_PORT" "$xray_host_port" "valid TCP port" "no action needed" || check_fail "XRAY_CHECKER_HOST_PORT" "$xray_host_port" "valid TCP port" "./install.sh configure"
      has_override "127.0.0.1:${xray_host_port}:2112" && check_ok "xray-checker host port" "127.0.0.1:${xray_host_port}:2112 in override" "host proxy targets 127.0.0.1:${xray_host_port}:2112" "no action needed" || check_fail "xray-checker host port" "missing 127.0.0.1:${xray_host_port}:2112 in override" "host proxy targets 127.0.0.1:${xray_host_port}:2112" "./install.sh configure"
    fi
    if [ "$topology" = "alongside-remnawave" ] || [ -n "$(env_default WATCHTOWER_URL "")" ]; then
      has_override 'xray-checker:' && has_override 'networks:' && check_ok "xray-checker networks" "network stanza present" "checker joins the same custom network(s) as bot" "no action needed" || check_warn "xray-checker networks" "network stanza missing" "checker joins the same custom network(s) as bot" "./install.sh configure"
    fi
  else
    has_override '^  xray-checker:' && check_warn "xray-checker service" "present while checker env is empty" "no checker sidecar when XRAY_CHECKER_URL and XRAY_CHECKER_SUB_URL are empty" "./install.sh configure" || check_ok "xray-checker service" "not configured" "sidecar absent unless configured" "no action needed"
  fi

  doctor_section "Backups"
  if [ -d "$BACKUP_DIR" ]; then
    backup_count="$(find "$BACKUP_DIR" -type f 2>/dev/null | wc -l | tr -d '[:space:]')"
  else
    backup_count="0"
  fi
  if [ "${backup_count:-0}" -gt 0 ] 2>/dev/null; then
    check_ok "backup directory" "$backup_count file(s) under ./backups" "at least one backup for recoverable rewrites" "no action needed"
  else
    check_warn "backup directory" "no backups under ./backups" "at least one backup for recoverable rewrites" "./install.sh backup"
  fi
  for backup_name in .env docker-compose.yml docker-compose.override.yml; do
    latest="$(latest_backup "$backup_name" || true)"
    if [ -n "$latest" ]; then
      check_ok "latest backup for $backup_name" "$(basename "$latest")" "$backup_name.* under ./backups" "no action needed"
    else
      check_warn "latest backup for $backup_name" "none" "$backup_name.* under ./backups" "./install.sh backup"
    fi
  done

  printf '\n' >&2
  if [ "$failures" -eq 0 ]; then
    ok "Doctor finished with $warnings warning(s)."
    return 0
  fi
  err "Doctor found $failures failure(s) and $warnings warning(s)."
  return 1
}

update_mode() {
  local compose
  ensure_compose_file
  if ! compose="$(detect_compose)"; then
    err "Docker Compose was not found. Install Docker Engine + Compose first."
    exit 1
  fi
  # The installer follows the same channel as the image it pulls.
  REMNAWAKE_CHANNEL="$(env_default REMNAWAKE_CHANNEL "${REMNAWAKE_CHANNEL:-main}")"
  v_channel "$REMNAWAKE_CHANNEL" >/dev/null 2>&1 || REMNAWAKE_CHANNEL="main"
  apply_channel_defaults
  run_backup
  info "Pulling image..."
  $compose pull
  info "Restarting..."
  $compose up -d
  # After the image, so a GitHub outage can never block the actual update.
  refresh_installer
  ok "Update complete. Useful commands:"
  printf '%s\n' "  $compose ps" >&2
  printf '%s\n' "  $compose logs -f" >&2
}

restore_mode() {
  local file="${1:-}" compose stamp tmp_empty
  if [ -z "$file" ]; then
    err "Usage: ./install.sh restore <backup.db>"
    exit 2
  fi
  if [ ! -f "$file" ]; then
    err "Backup file not found: $file"
    exit 2
  fi
  # Every SQLite database starts with this 15-byte magic header.
  if [ "$(head -c 15 "$file" 2>/dev/null)" != "SQLite format 3" ]; then
    err "Not a SQLite database: $file (expected an 'SQLite format 3' header)."
    exit 2
  fi
  ensure_compose_file
  if ! compose="$(detect_compose)"; then
    err "Docker Compose was not found. Install Docker Engine + Compose first."
    exit 1
  fi
  warn "This will REPLACE the bot database (/data/bot.db) with: $file"
  if ! ask_yes_no "Continue with the restore?" "n"; then
    warn "Restore cancelled; nothing changed."
    exit 0
  fi
  mkdir -p "$BACKUP_DIR"
  stamp="$(timestamp)"
  if $compose cp bot:/data/bot.db "$BACKUP_DIR/bot.db.pre-restore.$stamp" 2>/dev/null; then
    ok "Saved current database to $BACKUP_DIR/bot.db.pre-restore.$stamp"
  else
    warn "Could not copy the current database out (no container or no database yet); continuing."
  fi
  info "Stopping the bot..."
  $compose stop bot
  info "Copying the backup into the botdata volume..."
  $compose cp "$file" bot:/data/bot.db
  # A stale WAL left by the previous database could replay onto the restored
  # file and corrupt it. The runtime image is distroless (no shell to rm
  # with), so neutralize the sidecars by copying zero-byte files over them —
  # SQLite treats a zero-length WAL as empty.
  tmp_empty="$(mktemp)"
  $compose cp "$tmp_empty" bot:/data/bot.db-wal
  $compose cp "$tmp_empty" bot:/data/bot.db-shm
  rm -f "$tmp_empty"
  info "Starting the bot..."
  $compose up -d
  ok "Database restored from $file"
}

main() {
  local mode="${1:-configure}"
  case "$mode" in
    configure|config|"")
      configure_mode
      ;;
    menu|reconfigure)
      menu_mode
      ;;
    doctor|check)
      doctor_mode
      ;;
    update|upgrade)
      update_mode
      ;;
    backup)
      run_backup
      ;;
    restore)
      restore_mode "${2:-}"
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      err "Unknown mode: $mode"
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
