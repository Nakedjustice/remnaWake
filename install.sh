#!/usr/bin/env bash
#
# Interactive installer for remnaWake.
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
    printf '%s' "${BOLD}${prompt}${RESET}${display_default}: " >&2
    if [ "$secret" = "secret" ]; then
      read -r -s input
      printf '\n' >&2
    else
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

v_https_url() {
  case "$1" in
    https://*) return 0 ;;
    *) err "  → Telegram Mini Apps require an https:// URL."; return 1 ;;
  esac
}

v_bot_token() {
  if printf '%s' "$1" | grep -Eq '^[0-9]+:[A-Za-z0-9_-]+$'; then
    return 0
  fi
  warn "  → That does not look like a BotFather token (e.g. 123456789:AA...)."
  ask_yes_no "    Use it anyway?" "n" && return 0
  return 1
}

v_admin_id() {
  local input="$1"
  # A single "0" disables the admin-gated flows.
  if [ "$input" = "0" ]; then
    warn "  → 0 entered: the «Я оплатил» payment and «/invite» flows will be disabled."
    return 0
  fi
  # Otherwise accept a comma-separated list of numeric IDs (spaces tolerated).
  local old_ifs="$IFS" token valid=0
  IFS=','
  for token in $input; do
    IFS="$old_ifs"
    token="$(printf '%s' "$token" | tr -d ' \t')"
    [ -z "$token" ] && continue
    if ! printf '%s' "$token" | grep -Eq '^[0-9]+$'; then
      err "  → Each ID must be digits only. Got: \"$token\""
      IFS="$old_ifs"
      return 1
    fi
    valid=$((valid + 1))
  done
  IFS="$old_ifs"
  if [ "$valid" -eq 0 ]; then
    err "  → Must be a numeric Telegram user ID (digits only), or 0 to disable."
    return 1
  fi
  return 0
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

v_lang() {
  case "$1" in
    ru|en) return 0 ;;
    *) err "  → One of: ru, en."; return 1 ;;
  esac
}

v_days_list() {
  local old_ifs="$IFS" token
  IFS=','
  for token in $1; do
    IFS="$old_ifs"
    token="$(printf '%s' "$token" | tr -d ' \t')"
    [ -z "$token" ] && continue
    if ! printf '%s' "$token" | grep -Eq '^[1-9][0-9]*$'; then
      err "  → Each value must be a positive integer. Got: \"$token\""
      IFS="$old_ifs"
      return 1
    fi
  done
  IFS="$old_ifs"
  return 0
}

v_platega_method() {
  case "$1" in
    sbp|card|cards) return 0 ;;
    *) err "  → One of: sbp, card."; return 1 ;;
  esac
}

# --- Banner -----------------------------------------------------------------
cat >&2 <<EOF
${BOLD}remnaWake installer${RESET}
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
ask REMNAWAVE_API_TOKEN "Remnawave API token (panel → API tokens)" "" "" secret

printf '\n' >&2
info "── Telegram ────────────────────────────────────────────────"
ask TELEGRAM_BOT_TOKEN  "Telegram bot token (from @BotFather, e.g. 123456789:AA...)" "" v_bot_token secret
ask TELEGRAM_ADMIN_ID   "Telegram admin user ID(s) (numeric, comma-separated e.g. 123456 or 123456,789012; 0 to disable)" "0" v_admin_id

# --- Scheduling -------------------------------------------------------------
printf '\n' >&2
info "── Schedule ────────────────────────────────────────────────"
ask TZ      "Timezone (IANA name)" "Europe/Moscow" v_tz
ask RUN_AT  "Daily run time (HH:MM, local to the timezone above)" "09:00" v_time_hhmm

# --- Mini App (optional) ------------------------------------------------------
printf '\n' >&2
info "── Telegram Mini App (optional) ────────────────────────────"
WEBAPP_URL=""
WEBAPP_LISTEN=":8080"
if ask_yes_no "Enable the Mini App personal cabinet? (needs an HTTPS reverse proxy in front)" "n"; then
  ask WEBAPP_URL "Public Mini App URL (e.g. https://bot.example.com)" "" v_https_url
  WEBAPP_URL="${WEBAPP_URL%/}"
fi

# --- Platega payment gateway (optional) -------------------------------------
printf '\n' >&2
info "── Platega payment gateway (optional) ──────────────────────"
PLATEGA_MERCHANT_ID=""
PLATEGA_SECRET=""
PLATEGA_METHOD="sbp"
PLATEGA_CURRENCY="RUB"
PLATEGA_RETURN_URL="https://t.me"
warn "Default is manual P2P (you confirm payments yourself). Platega adds online"
warn "SBP/card payments; you can switch the active provider later from the admin menu."
if ask_yes_no "Configure Platega online payments now?" "n"; then
  ask PLATEGA_MERCHANT_ID "Platega merchant id (from the Platega dashboard)" "" ""
  ask PLATEGA_SECRET      "Platega secret (X-Secret)" "" "" secret
  ask PLATEGA_METHOD      "Payment method (sbp / card)" "sbp" v_platega_method
  ask PLATEGA_CURRENCY    "Currency code sent to Platega (ISO, e.g. RUB)" "RUB" ""
  ask PLATEGA_RETURN_URL  "Return URL after payment (e.g. your bot link)" "https://t.me" v_url
  PLATEGA_RETURN_URL="${PLATEGA_RETURN_URL%/}"
fi

# --- Defaults for the rest (overridable via advanced section) ---------------
TELEGRAM_PARSE_MODE="HTML"
LOG_LEVEL="info"
HTTP_TIMEOUT="15s"
DRY_RUN="false"
RUN_ON_START="true"
CURRENCY="₽"
BOT_LANG="ru"
WINBACK_ENABLED="true"
WINBACK_DAYS="1,3"

printf '\n' >&2
if ask_yes_no "Configure advanced options (parse mode, log level, timeout, dry-run, currency, language, win-back)?" "n"; then
  info "── Advanced ────────────────────────────────────────────────"
  ask TELEGRAM_PARSE_MODE "Telegram parse mode (HTML / MarkdownV2)" "HTML"  ""
  ask LOG_LEVEL           "Log level (debug/info/warn/error)"       "info"  v_loglevel
  ask HTTP_TIMEOUT        "HTTP timeout (Go duration, e.g. 15s)"    "15s"   v_duration
  ask DRY_RUN             "Dry run? log instead of sending (true/false)"   "false" v_bool
  ask RUN_ON_START        "Run once immediately on start (true/false)"     "true"  v_bool
  ask CURRENCY            "Currency label shown next to tariff prices"     "₽"     ""
  ask BOT_LANG            "Bot language (ru / en)"                         "ru"    v_lang
  ask WINBACK_ENABLED     "Send win-back messages to expired users (true/false)" "true" v_bool
  ask WINBACK_DAYS        "Days after expiry to send win-back (comma-separated, e.g. 1,3)" "1,3" v_days_list
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

CURRENCY=$CURRENCY
BOT_LANG=$BOT_LANG

# Win-back notifications: messages sent to expired users N days after expiry.
WINBACK_ENABLED=$WINBACK_ENABLED
WINBACK_DAYS=$WINBACK_DAYS

# Telegram Mini App: public HTTPS URL served by your reverse proxy
# (empty = mini app disabled) and the local bind address behind it.
WEBAPP_URL=$WEBAPP_URL
WEBAPP_LISTEN=$WEBAPP_LISTEN

# Platega payment gateway (optional): online SBP/card payments as an alternative
# to manual P2P. Empty merchant id/secret = Platega off (P2P only). The active
# provider is switched at runtime from the bot /admin menu or the Mini App admin
# panel. Set the Platega dashboard notification URL to <public-host>/platega/callback.
PLATEGA_MERCHANT_ID=$PLATEGA_MERCHANT_ID
PLATEGA_SECRET=$PLATEGA_SECRET
PLATEGA_METHOD=$PLATEGA_METHOD
PLATEGA_CURRENCY=$PLATEGA_CURRENCY
PLATEGA_RETURN_URL=$PLATEGA_RETURN_URL
EOF

mv "$tmp_env" "$ENV_FILE"
trap - EXIT
chmod 600 "$ENV_FILE"

printf '\n' >&2
ok "Wrote configuration to $ENV_FILE (permissions 600)."

# --- Summary (secrets masked) -----------------------------------------------
mask() { local s="$1"; [ "${#s}" -le 8 ] && { printf '****'; return; }; printf '%s…%s' "${s:0:4}" "${s: -4}"; }
platega_summary="disabled (P2P only)"
[ -n "$PLATEGA_MERCHANT_ID" ] && platega_summary="enabled ($PLATEGA_METHOD, $PLATEGA_CURRENCY)"
cat >&2 <<EOF

${BOLD}Summary${RESET}
  Panel URL          : $REMNAWAVE_BASE_URL
  Remnawave token    : $(mask "$REMNAWAVE_API_TOKEN")
  Bot token          : $(mask "$TELEGRAM_BOT_TOKEN")
  Admin ID(s)        : $TELEGRAM_ADMIN_ID
  Timezone / run-at  : $TZ at $RUN_AT
  Parse / log / http : $TELEGRAM_PARSE_MODE / $LOG_LEVEL / $HTTP_TIMEOUT
  Dry-run / on-start : $DRY_RUN / $RUN_ON_START
  Currency           : $CURRENCY
  Mini App           : ${WEBAPP_URL:-disabled}
  Platega            : $platega_summary

EOF

if [ -n "$WEBAPP_URL" ]; then
  warn "Mini App checklist:"
  warn "  1. Uncomment the 'ports' section in docker-compose.yml (exposes ${WEBAPP_LISTEN#:} on 127.0.0.1)."
  warn "  2. Point your HTTPS reverse proxy at it: $WEBAPP_URL → 127.0.0.1:${WEBAPP_LISTEN#:}"
  warn "     (nginx and Caddy templates are in the README, section «Telegram Mini App»)."
fi

if [ -n "$PLATEGA_MERCHANT_ID" ]; then
  warn "Platega checklist:"
  warn "  1. The webhook is served by the same HTTP server as the Mini App: uncomment the"
  warn "     'ports' section in docker-compose.yml (exposes ${WEBAPP_LISTEN#:} on 127.0.0.1)"
  warn "     and put an HTTPS reverse proxy in front of it."
  warn "  2. In the Platega dashboard set the notification (webhook) URL to:"
  warn "       https://<your-public-host>/platega/callback"
  warn "  3. Switch the active provider to Platega from the bot /admin menu or the"
  warn "     Mini App admin panel (the bot starts on P2P until you do)."
fi

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
  warn "  ${COMPOSE:-docker compose} up -d"
  ok "Configuration is ready — start the bot whenever Docker is available."
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  warn "Docker is installed but not accessible by this user."
  warn "You may need to run the next step with sudo, or add your user to the 'docker' group."
fi

printf '\n' >&2
if ask_yes_no "Pull the pre-built image and start the bot now with '$COMPOSE up -d'?" "y"; then
  info "Pulling image and starting…"
  $COMPOSE up -d
  printf '\n' >&2
  ok "Bot is up. Useful commands:"
  printf '%s\n' "  ${DIM}$COMPOSE logs -f${RESET}     # follow logs"           >&2
  printf '%s\n' "  ${DIM}$COMPOSE ps${RESET}          # status"                 >&2
  printf '%s\n' "  ${DIM}$COMPOSE pull && $COMPOSE up -d${RESET}  # update to latest" >&2
  printf '%s\n' "  ${DIM}$COMPOSE down${RESET}        # stop & remove"          >&2
else
  ok "Skipped startup. When ready, run:  $COMPOSE up -d"
fi
