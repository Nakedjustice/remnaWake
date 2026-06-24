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
  ./install.sh --help

Modes:
  configure  Interactive setup. On a first install it walks every section in
             order; when an .env already exists it opens the reconfigure menu.
  menu       Open the reconfigure menu directly to edit individual sections.
  doctor     Check Docker, compose config, .env keys, ports and update settings.
  update     Back up config, pull the image and restart with Docker Compose.
  backup     Copy .env and compose files into ./backups.

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
  CHECKER_PUBLIC_ENABLED="no"
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
  local failures=0 warnings=0 compose value base_image autoupdate_image effective_image web_port mode

  check_ok() { ok "OK: $*"; }
  check_warn() { warn "WARN: $*"; warnings=$((warnings + 1)); }
  check_fail() { err "FAIL: $*"; failures=$((failures + 1)); }

  REMNAWAKE_CHANNEL="$(env_default REMNAWAKE_CHANNEL "${REMNAWAKE_CHANNEL:-main}")"
  v_channel "$REMNAWAKE_CHANNEL" >/dev/null 2>&1 || REMNAWAKE_CHANNEL="main"
  apply_channel_defaults

  info "remnaWake doctor"

  value="$REMNAWAKE_CHANNEL"
  if v_channel "$value" >/dev/null 2>&1; then
    check_ok "REMNAWAKE_CHANNEL is $value"
  else
    check_fail "REMNAWAKE_CHANNEL is invalid: $value"
  fi

  [ -f "$ENV_FILE" ] && check_ok ".env exists" || check_fail ".env is missing; run ./install.sh configure"
  [ -f "$COMPOSE_FILE" ] && check_ok "docker-compose.yml exists" || check_fail "docker-compose.yml is missing"
  [ -f "$OVERRIDE_FILE" ] && check_ok "docker-compose.override.yml exists" || check_warn "docker-compose.override.yml is missing; run ./install.sh configure"

  for key in REMNAWAVE_BASE_URL REMNAWAVE_API_TOKEN TELEGRAM_BOT_TOKEN; do
    if value="$(env_get "$key")" && [ -n "$value" ]; then
      check_ok "$key is set"
    else
      check_fail "$key is missing"
    fi
  done

  if [ -f "$ENV_FILE" ]; then
    mode=""
    if mode="$(stat -c %a "$ENV_FILE" 2>/dev/null)"; then
      [ "$mode" = "600" ] && check_ok ".env permissions are 600" || check_warn ".env permissions are $mode, expected 600"
    else
      check_warn "Could not inspect .env permissions on this platform"
    fi
  fi

  web_port="$(env_default WEBAPP_HOST_PORT "8080")"
  if v_port "$web_port" >/dev/null 2>&1; then
    check_ok "WEBAPP_HOST_PORT is valid ($web_port)"
    if port_in_use "$web_port"; then
      check_warn "Port $web_port appears to be in use; this is OK if remnaWake is already running"
    else
      check_ok "Port $web_port does not appear to be listening"
    fi
  else
    check_fail "WEBAPP_HOST_PORT is invalid: $web_port"
  fi

  if command -v docker >/dev/null 2>&1; then
    check_ok "docker command exists"
    if docker info >/dev/null 2>&1; then
      check_ok "Docker daemon is accessible"
    else
      check_warn "Docker command exists but daemon is not accessible"
    fi
  else
    check_fail "docker command not found"
  fi

  if compose="$(detect_compose)"; then
    check_ok "Docker Compose is available ($compose)"
    if [ -f "$COMPOSE_FILE" ] && $compose config >/dev/null 2>&1; then
      check_ok "docker compose config succeeds"
    else
      check_fail "docker compose config failed"
    fi
  else
    check_fail "Docker Compose not found"
  fi

  if [ -f "$COMPOSE_FILE" ]; then
    base_image="$(compose_image || true)"
    case "$base_image" in
      *remnawake*) check_ok "Compose image looks correct ($base_image)" ;;
      "") check_warn "Could not read image from docker-compose.yml" ;;
      *) check_warn "Compose image does not contain remnawake: $base_image" ;;
    esac
    autoupdate_image="$(env_default AUTOUPDATE_IMAGE "$DEFAULT_DEPLOY_IMAGE")"
    effective_image="$(override_image || true)"
    [ -n "$effective_image" ] || effective_image="$base_image"
    if [ -n "$effective_image" ] && [ "$autoupdate_image" != "$effective_image" ]; then
      check_warn "AUTOUPDATE_IMAGE ($autoupdate_image) differs from effective compose image ($effective_image)"
    else
      check_ok "AUTOUPDATE_IMAGE matches effective compose image"
    fi
  fi

  if [ "$(env_default AUTOUPDATE_ENABLED "false")" = "true" ]; then
    value="$(env_default AUTOUPDATE_CHECK_INTERVAL "6h")"
    v_duration "$value" >/dev/null 2>&1 && check_ok "AUTOUPDATE_CHECK_INTERVAL is valid" || check_fail "AUTOUPDATE_CHECK_INTERVAL is invalid: $value"
    if [ -n "$(env_default WATCHTOWER_URL "")" ]; then
      [ -n "$(env_default WATCHTOWER_TOKEN "")" ] && check_ok "Watchtower token is set" || check_fail "WATCHTOWER_TOKEN is required when WATCHTOWER_URL is set"
      if grep -q 'watchtower:' "$OVERRIDE_FILE" 2>/dev/null; then
        check_ok "Watchtower service is present in override"
        grep -q 'image: containrrr/watchtower:latest' "$OVERRIDE_FILE" 2>/dev/null && check_ok "Watchtower image uses latest tag" || check_warn "Watchtower image is stale; rerun ./install.sh configure"
        grep -q 'pull_policy: always' "$OVERRIDE_FILE" 2>/dev/null && check_ok "Watchtower pull policy refreshes the sidecar" || check_warn "Watchtower pull_policy is missing; rerun ./install.sh configure"
        grep -q 'DOCKER_API_VERSION: "1.40"' "$OVERRIDE_FILE" 2>/dev/null && check_ok "Watchtower Docker API version is compatible" || check_warn "Watchtower Docker API version is missing; rerun ./install.sh configure"
      else
        check_fail "Watchtower URL is set but override has no watchtower service"
      fi
    else
      check_ok "Auto-update is notify-only (no Watchtower URL)"
    fi
  fi

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
  run_backup
  info "Pulling image..."
  $compose pull
  info "Restarting..."
  $compose up -d
  ok "Update complete. Useful commands:"
  printf '%s\n' "  $compose ps" >&2
  printf '%s\n' "  $compose logs -f" >&2
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
