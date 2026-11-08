```bash
#!/usr/bin/env bash
# =====================================================================
#  ONE command to install or update the Agent on the server PC.
#  Run it on your Mac, from the tinker-agent folder:
#
#      ./scripts/deploy.sh USER@SERVER_IP
#
#  It builds the Agent, copies it to the server, and runs install.sh
#  there (Java, service, token, firewall, everything).
# =====================================================================
set -euo pipefail

TARGET="${1:-}"
if [ -z "$TARGET" ]; then
  echo "Usage: ./scripts/deploy.sh USER@SERVER_IP   (for example theo@192.168.1.50)"
  exit 1
fi
cd "$(dirname "$0")/.." # always work from the repo root

# Reuse one SSH connection, so you only type the server password once.
SSH_OPTS=(-o ControlMaster=auto -o ControlPath="/tmp/tinker-ssh-%r@%h-%p" -o ControlPersist=120)
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) SSH_OPTS=() ;; esac # Windows can't reuse

echo "==> Checking the server's processor type…"
case "$(ssh "${SSH_OPTS[@]}" "$TARGET" uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unknown processor type"; exit 1 ;;
esac

echo "==> Building the Agent for Linux ($ARCH)…"
mkdir -p dist
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o dist/tinker-agent ./cmd/tinker-agent

echo "==> Copying it to $TARGET…"
scp "${SSH_OPTS[@]}" -q dist/tinker-agent scripts/install.sh "$TARGET":/tmp/

echo "==> Installing (type the server's password if it asks for one)…"
ssh "${SSH_OPTS[@]}" -t "$TARGET" "sudo bash /tmp/install.sh /tmp/tinker-agent"
```