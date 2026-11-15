#!/usr/bin/env bash
# =====================================================================
#  ONE command to install or update the Agent on the server PC.
#  Run it from a checkout, or download this file and run it directly:
#
#      Mac:                ./scripts/deploy.sh USER@SERVER_IP
#      Windows (Git Bash): ./scripts/deploy.sh USER@SERVER_IP
#
#  When run by itself, it fetches the source from GitHub, builds the
#  Agent, copies it to the server, and runs install.sh there.
# =====================================================================
set -euo pipefail

say() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\n\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

TARGET="${1:-}"
if [ -z "$TARGET" ]; then
  echo "Usage: ./scripts/deploy.sh USER@SERVER_IP   (for example theo@192.168.1.50)"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$SCRIPT_DIR/../go.mod" ] && [ -d "$SCRIPT_DIR/../cmd/tinker-agent" ] \
    && [ -f "$SCRIPT_DIR/../scripts/install.sh" ]; then
  REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
else
  command -v git >/dev/null || die "git isn't installed; it is needed to download the Agent source."
  TEMP_DIR="$(mktemp -d)" || die "Couldn't create a temporary directory to download the Agent source."
  trap 'rm -rf "$TEMP_DIR"' EXIT
  REPO_ROOT="$TEMP_DIR/tinker-agent"
  say "Downloading the Agent source from GitHub…"
  git clone --depth 1 https://github.com/tinkermc/tinker-agent.git "$REPO_ROOT" \
    || die "Couldn't download the source from GitHub. Check your internet connection."
fi
cd "$REPO_ROOT"

command -v go >/dev/null || die "Go isn't installed (or the terminal can't find it). Run: go version"
command -v ssh >/dev/null || die "ssh isn't installed. On Windows, run this from Git Bash."
command -v scp >/dev/null || die "scp isn't installed. On Windows, run this from Git Bash."

# Reuse one SSH connection, so you only type the server password once.
SSH_OPTS=(-o ControlMaster=auto -o ControlPath="/tmp/tinker-ssh-%r@%h-%p" -o ControlPersist=120)
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) SSH_OPTS=() ;; esac # Windows can't reuse

say "Connecting to ${TARGET}… (if it asks for a password, type it and press Enter: nothing shows while you type)"
MACHINE="$(ssh "${SSH_OPTS[@]}" -o ConnectTimeout=10 "$TARGET" uname -m)" \
  || die "Couldn't log in to $TARGET over SSH. Check the IP and username, and that 'ssh $TARGET' works on its own."
case "$MACHINE" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "Unknown processor type: $MACHINE" ;;
esac

say "Building the Agent for Linux ($ARCH)…"
mkdir -p dist
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o dist/tinker-agent ./cmd/tinker-agent

# Windows editors can save files with Windows line endings, which break
# bash on Linux. Send a cleaned copy of install.sh.
tr -d '\r' < scripts/install.sh > dist/install.sh

say "Copying it to ${TARGET}…"
scp "${SSH_OPTS[@]}" -q dist/tinker-agent dist/install.sh "$TARGET":/tmp/

say "Installing on the server (it may ask for the server's password again, for sudo)…"
ssh "${SSH_OPTS[@]}" -t "$TARGET" "sudo bash /tmp/install.sh /tmp/tinker-agent"
