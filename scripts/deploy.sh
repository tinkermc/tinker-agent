#!/usr/bin/env bash
# =====================================================================
#  ONE command to install or update the Agent on the server PC.
#  Run it from the tinker-agent folder:
#
#      Mac:                ./scripts/deploy.sh USER@SERVER_IP
#      Windows (Git Bash): ./scripts/deploy.sh USER@SERVER_IP
#
#  It builds the Agent, copies it to the server, and runs install.sh
#  there (Java, service, token, firewall, everything).
# =====================================================================
set -euo pipefail

say() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\n\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

TARGET="${1:-}"
if [ -z "$TARGET" ]; then
  echo "Usage: ./scripts/deploy.sh USER@SERVER_IP   (for example theo@192.168.1.50)"
  exit 1
fi
cd "$(dirname "$0")/.." # always work from the repo root

command -v go >/dev/null || die "Go isn't installed (or the terminal can't find it). Run: go version"
command -v ssh >/dev/null || die "ssh isn't installed. On Windows, run this from Git Bash."

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