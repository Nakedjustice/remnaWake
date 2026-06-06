#!/usr/bin/env bash
#
# Bootstrap downloader for remnawave-notify-bot.
#
# Downloads the project source onto the current server and launches the
# interactive installer (install.sh). Designed to be run straight from a URL:
#
#   curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | bash
#
# Overridable via environment variables:
#   REPO=owner/name      (default: Nakedjustice/remnaWake)
#   BRANCH=branch         (default: main)
#   TARGET_DIR=path       (default: remnaWake)
#
set -euo pipefail

REPO="${REPO:-Nakedjustice/remnaWake}"
BRANCH="${BRANCH:-main}"
TARGET_DIR="${TARGET_DIR:-remnaWake}"

if [ -t 1 ]; then
  BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"
  GREEN="$(printf '\033[32m')"; YELLOW="$(printf '\033[33m')"
  RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"
else
  BOLD=""; DIM=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi
ok()   { printf '%s\n' "${GREEN}$*${RESET}"; }
warn() { printf '%s\n' "${YELLOW}$*${RESET}" >&2; }
err()  { printf '%s\n' "${RED}$*${RESET}" >&2; }

printf '%s\n' "${BOLD}remnawave-notify-bot — downloading $REPO@$BRANCH${RESET}" >&2

# --- Refuse to clobber a non-empty target dir -------------------------------
if [ -e "$TARGET_DIR" ] && [ -n "$(ls -A "$TARGET_DIR" 2>/dev/null || true)" ]; then
  err "Directory '$TARGET_DIR' already exists and is not empty."
  err "Remove it, or re-run with TARGET_DIR=<other-path>."
  exit 1
fi

# --- Download: prefer git, fall back to a tarball ---------------------------
if command -v git >/dev/null 2>&1; then
  git clone --depth 1 --branch "$BRANCH" "https://github.com/$REPO.git" "$TARGET_DIR"
else
  warn "git not found — downloading a tarball instead."
  mkdir -p "$TARGET_DIR"
  url="https://codeload.github.com/$REPO/tar.gz/refs/heads/$BRANCH"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" | tar -xz -C "$TARGET_DIR" --strip-components=1
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url" | tar -xz -C "$TARGET_DIR" --strip-components=1
  else
    err "Need one of: git, curl, or wget to download the project."
    exit 1
  fi
fi

cd "$TARGET_DIR"
chmod +x install.sh 2>/dev/null || true
ok "Downloaded to $(pwd)"

# --- Launch the interactive installer ---------------------------------------
# When this script is run via `curl ... | bash`, stdin is the pipe, so the
# installer's prompts must be reattached to the real terminal via /dev/tty.
run_installer() {
  if [ -t 0 ]; then
    bash install.sh
  else
    bash install.sh </dev/tty
  fi
}

if [ -t 0 ] || { [ -e /dev/tty ] && (exec </dev/tty) 2>/dev/null; }; then
  printf '\n' >&2
  ans=""
  if [ -t 0 ]; then
    printf '%s' "${BOLD}Run the installer now?${RESET} ${DIM}[Y/n]${RESET} " >&2
    read -r ans
  else
    printf '%s' "${BOLD}Run the installer now?${RESET} ${DIM}[Y/n]${RESET} " >&2
    read -r ans </dev/tty
  fi
  case "${ans:-y}" in
    n|N|no|NO) ok "Skipped. To install later:  cd $TARGET_DIR && ./install.sh" ;;
    *) run_installer ;;
  esac
else
  ok "No terminal detected. To install:  cd $TARGET_DIR && ./install.sh"
fi
