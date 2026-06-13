#!/usr/bin/env bash
#
# Bootstrap downloader for remnaWake.
#
# Downloads only the interactive installer (install.sh) onto the server and
# runs it; the installer in turn fetches docker-compose.yml and the bot runs
# from the pre-built image — the full source repo is never needed. Designed to
# be run straight from a URL:
#
#   curl -fsSL https://raw.githubusercontent.com/Nakedjustice/remnaWake/main/get.sh | bash
#
# (Equivalent to piping install.sh directly; this wrapper just adds a tidy
# install location and a root/permissions pre-check.)
#
# Overridable via environment variables:
#   REPO=owner/name      (default: Nakedjustice/remnaWake)
#   BRANCH=branch         (default: main)
#   TARGET_DIR=path       (default: /opt/remnaWake)
#
set -euo pipefail

REPO="${REPO:-Nakedjustice/remnaWake}"
BRANCH="${BRANCH:-main}"
TARGET_DIR="${TARGET_DIR:-/opt/remnaWake}"

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

printf '%s\n' "${BOLD}remnaWake — downloading $REPO@$BRANCH${RESET}" >&2
printf '%s\n' "${DIM}Install location: $TARGET_DIR${RESET}" >&2

# --- Ensure the target's parent exists and is writable ----------------------
# /opt typically requires root; fail early with a clear message instead of a
# cryptic permission error mid-clone.
parent="$(dirname "$TARGET_DIR")"
need_root=0
if [ ! -d "$parent" ]; then
  mkdir -p "$parent" 2>/dev/null || need_root=1
fi
if [ -d "$parent" ] && [ ! -w "$parent" ]; then
  need_root=1
fi
if [ "$need_root" -eq 1 ] && [ "$(id -u)" -ne 0 ]; then
  err "Installing to $TARGET_DIR requires root privileges."
  err "Re-run with sudo:"
  err "  curl -fsSL https://raw.githubusercontent.com/$REPO/$BRANCH/get.sh | sudo bash"
  err "…or pick a writable location:"
  err "  curl -fsSL https://raw.githubusercontent.com/$REPO/$BRANCH/get.sh | TARGET_DIR=\"\$HOME/remnaWake\" bash"
  exit 1
fi

# --- Download just the installer (it fetches docker-compose.yml itself) -----
mkdir -p "$TARGET_DIR"
url="https://raw.githubusercontent.com/$REPO/$BRANCH/install.sh"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url" -o "$TARGET_DIR/install.sh"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TARGET_DIR/install.sh" "$url"
else
  err "Need curl or wget to download the installer."
  exit 1
fi

cd "$TARGET_DIR"
chmod +x install.sh 2>/dev/null || true
ok "Downloaded the installer to $(pwd)"

# --- Launch the interactive installer ---------------------------------------
# Point the installer at the same REPO/BRANCH so it fetches the matching
# docker-compose.yml. When run via `curl ... | bash`, stdin is the pipe, so the
# installer's prompts must be reattached to the real terminal via /dev/tty.
export REMNAWAKE_REPO_RAW="https://raw.githubusercontent.com/$REPO/$BRANCH"
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
