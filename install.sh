#!/usr/bin/env bash
#
# Interactive installer for remnawave-notify-bot.
#
# Asks for everything the bot needs (the values that end up in ./.env),
# validates the answers, writes a locked-down .env file, and optionally
# builds and starts the container with Docker Compose.
#
# Usage:
#   chmod +x install.sh
#   ./install.sh
#
set -euo pipefail

# --- Resolve repo root (directory this script lives in) ---------------------
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
cd "$SCRIPT_DIR"

ENV_FILE="$SCRIPT_DIR/.env"

# --- Colours (disabled when not a terminal) ---------------------------------
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

# --- Prompt helpers ---------------------------------------------------------
# Read a value, re-prompting until the validator passes.
# Args: <var_name> <prompt> <default> <validator_fn|""> [secret]
ask() {
  local __var="$1" prompt="$2" default="${3:-}" validator="${4:-}" secret="${5:-}"
  local input display_default="" value
  [ -n "$default" ] && display_default=" ${DIM}[$default]${RESET}"

  while true; do
    if [ "$secret" = "secret" ]; then
      printf '%s' "${BOLD}${prompt}${RESET}${display_default}: " >&2
      read -r -s input
      printf '\n' >&2
    else
      printf '%s' "${BOLD}${prompt}${RESET}${display_default}: " >&2
      read -r input
    fi

    value="${input:-$default}"

    if [ -z "$value" ]; then
      err "  → A value is required."
      continue
    fi

    if [ -n "$validator" ] && ! "$validator" "$value"; then
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
      *) err "  → Please answer y or n." ;;
    esac
  done
}

# --- Validators -------------------------------------------------------------
v_url() {
  case "$1" in
    http://*|https://*) return 0 ;;
    *) err "  → Must start with http:// or https://"; return 1 ;;
  esac
}

v_nonempty() { [ -n "$1" ] && return 0; err "  → Cannot be empty."; return 1; }

v_bot_token() {
  if printf '%s' "$1" | grep -Eq '^[0-9]+:[A-Za-z0-9_-]+$'; then
    return 0
  fi
  warn "  → That does not look like a BotFather token (e.g. 123456789:AA...)."
  ask_yes_no "    Use it anyway?" "n" && return 0
  return 1
}

v_admin_id() {
  # Allow 0 (disables the payment-confirmation flow) or a positive integer.
  if printf '%s' "$1" | grep -Eq '^[0-9]+$'; then
    [ "$1" = "0" ] && warn "  → 0 entered: the «Я оплатил» payment flow will be disabled."
    return 0
  fi
  err "  → Must be a numeric Telegram user ID (digits only)."
  return 1
}

v_time_hhmm() {
  if printf '%s' "$1" | grep -Eq '^([01][0-9]|2[0-3]):[0-5][0-9]$'; then
    return 0
  fi
  err "  → Must be HH:MM in 24h format (e.g. 09:00)."
  return 1
}

v_tz() {
  # Best-effort: if the zoneinfo DB is present, require the zone to exist.
  if [ -d /usr/share/zoneinfo ]; then
    if [ -f "/usr/share/zoneinfo/$1" ]; then return 0; fi
    err "  → Unknown timezone. Use an IANA name like Europe/Moscow or UTC."
    return 1
  fi
  return 0
}

v_duration() {
  if printf '%s' "$1" | grep -Eq '^[0-9]+(ns|us|µs|ms|s|m|h)$'; then
    return 0
  fi
  err "  → Must be a Go duration like 15s, 500ms, 1m."
  return 1
}

v_loglevel() {
  case "$1" in
    debug|info|warn|warning|error) return 0 ;;
    *) err "  → One of: debug, info, warn, error."; return 1 ;;
  esac
}

v_bool() {
  case "$1" in
    true|false) return 0 ;;
    *) err "  → Must be true or false."; return 1 ;;
  esac
}

# --- Banner -----------------------------------------------------------------
cat >&2 <<EOF
${BOLD}remnawave-notify-bot installer${RESET}
${DIM}This will collect the settings the bot needs and write them to ./.env${RESET}

EOF

# --- Guard against clobbering an existing .env ------------------------------
if [ -f "$ENV_FILE" ]; then
  warn "An .env file already exists at $ENV_FILE"
  if ask_yes_no "Overwrite it? (a timestamped backup will be kept)" "n"; then
    backup="$ENV_FILE.bak.$(date +%Y%m%d%H%M%S)"
    cp "$ENV_FILE" "$backup"
    ok "Backed up existing .env to $backup"
  else
    err "Aborting so the existing .env is left untouched."
    exit 1
  fi
fi

# --- Required settings ------------------------------------------------------
info "── Remnawave panel ─────────────────────────────────────────"
ask REMNAWAVE_BASE_URL  "Remnawave panel URL (e.g. https://panel.example.com)" "" v_url
# Normalise: drop any trailing slash (the bot does this too, but keep .env clean).
REMNAWAVE_BASE_URL="${REMNAWAVE_BASE_URL%/}"
ask REMNAWAVE_API_TOKEN "Remnawave API token (panel → API tokens)" "" v_nonempty secret

printf '\n' >&2
info "── Telegram ────────────────────────────────────────────────"
ask TELEGRAM_BOT_TOKEN  "Telegram bot token (from @BotFather, e.g. 123456789:AA...)" "" v_bot_token secret
ask TELEGRAM_ADMIN_ID   "Telegram admin user ID (numeric, from @userinfobot; 0 to disable payments)" "0" v_admin_id

# --- Scheduling -------------------------------------------------------------
printf '\n' >&2
info "── Schedule ────────────────────────────────────────────────"
ask TZ      "Timezone (IANA name)" "Europe/Moscow" v_tz
ask RUN_AT  "Daily run time (HH:MM, local to the timezone above)" "09:00" v_time_hhmm

# --- Defaults for the rest (overridable via advanced section) ---------------
TELEGRAM_PARSE_MODE="HTML"
LOG_LEVEL="info"
HTTP_TIMEOUT="15s"
DRY_RUN="false"
RUN_ON_START="true"

printf '\n' >&2
if ask_yes_no "Configure advanced options (parse mode, log level, timeout, dry-run)?" "n"; then
  info "── Advanced ────────────────────────────────────────────────"
  ask TELEGRAM_PARSE_MODE "Telegram parse mode (HTML / MarkdownV2)" "HTML"  v_nonempty
  ask LOG_LEVEL           "Log level (debug/info/warn/error)"       "info"  v_loglevel
  ask HTTP_TIMEOUT        "HTTP timeout (Go duration, e.g. 15s)"    "15s"   v_duration
  ask DRY_RUN             "Dry run? log instead of sending (true/false)"   "false" v_bool
  ask RUN_ON_START        "Run once immediately on start (true/false)"     "true"  v_bool
fi

# --- Write .env atomically with strict permissions --------------------------
umask 177  # new files -> 0600
tmp_env="$(mktemp "$ENV_FILE.XXXXXX")"
trap 'rm -f "$tmp_env"' EXIT

cat >"$tmp_env" <<EOF
# Generated by install.sh on $(date -u +"%Y-%m-%dT%H:%M:%SZ")
# Keep this file private — it contains API tokens.

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
EOF

mv "$tmp_env" "$ENV_FILE"
trap - EXIT
chmod 600 "$ENV_FILE"

printf '\n' >&2
ok "Wrote configuration to $ENV_FILE (permissions 600)."

# --- Summary (secrets masked) -----------------------------------------------
mask() { local s="$1"; [ "${#s}" -le 8 ] && { printf '****'; return; }; printf '%s…%s' "${s:0:4}" "${s: -4}"; }
cat >&2 <<EOF

${BOLD}Summary${RESET}
  Panel URL          : $REMNAWAVE_BASE_URL
  Remnawave token    : $(mask "$REMNAWAVE_API_TOKEN")
  Bot token          : $(mask "$TELEGRAM_BOT_TOKEN")
  Admin ID           : $TELEGRAM_ADMIN_ID
  Timezone / run-at  : $TZ at $RUN_AT
  Parse / log / http : $TELEGRAM_PARSE_MODE / $LOG_LEVEL / $HTTP_TIMEOUT
  Dry-run / on-start : $DRY_RUN / $RUN_ON_START

EOF

# --- Detect Docker Compose --------------------------------------------------
COMPOSE=""
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

if [ -z "$COMPOSE" ]; then
  warn "Docker Compose was not found on this machine."
  warn "Install Docker Engine + the Compose plugin, then run:"
  warn "  ${COMPOSE:-docker compose} up -d --build"
  ok "Configuration is ready — start the bot whenever Docker is available."
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  warn "Docker is installed but not accessible by this user."
  warn "You may need to run the next step with sudo, or add your user to the 'docker' group."
fi

printf '\n' >&2
if ask_yes_no "Build and start the bot now with '$COMPOSE up -d --build'?" "y"; then
  info "Building and starting…"
  $COMPOSE up -d --build
  printf '\n' >&2
  ok "Bot is up. Useful commands:"
  printf '%s\n' "  ${DIM}$COMPOSE logs -f${RESET}     # follow logs"  >&2
  printf '%s\n' "  ${DIM}$COMPOSE ps${RESET}          # status"        >&2
  printf '%s\n' "  ${DIM}$COMPOSE down${RESET}        # stop & remove" >&2
else
  ok "Skipped startup. When ready, run:  $COMPOSE up -d --build"
fi
