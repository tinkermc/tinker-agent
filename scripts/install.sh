```bash
#!/usr/bin/env bash
# =====================================================================
#  Tinker Agent installer: sets up EVERYTHING on the Linux server PC.
#
#  You normally don't run this yourself: scripts/deploy.sh (on your Mac)
#  copies it over and runs it. To run it by hand on the server:
#      sudo bash install.sh /path/to/tinker-agent
#
#  Safe to run again: it updates the Agent and keeps your token,
#  settings and Minecraft servers.
# =====================================================================
set -euo pipefail

say()  { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*"; }
die()  { printf '\n\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# ---- 0. Checks ------------------------------------------------------
[ "$(id -u)" -eq 0 ] || exec sudo bash "$0" "$@"   # re-run with sudo

BINARY="${1:-$(dirname "$0")/tinker-agent}"
[ -f "$BINARY" ] || die "Can't find the tinker-agent program at $BINARY"
command -v apt-get >/dev/null || die "This installer only supports Ubuntu/Debian for now."
command -v systemctl >/dev/null || die "systemd is required."

# The Agent runs as your normal user (not root), the same as in Part 1.
RUN_USER="${TINKER_USER:-${SUDO_USER:-}}"
[ -n "$RUN_USER" ] && [ "$RUN_USER" != "root" ] \
  || die "Run this with sudo from your normal account, or set TINKER_USER=yourname."
id "$RUN_USER" >/dev/null 2>&1 || die "User $RUN_USER doesn't exist."
RUN_HOME="$(getent passwd "$RUN_USER" | cut -d: -f6)"
DATA_DIR="$RUN_HOME/tinker/servers"
ENV_FILE=/etc/tinker-agent.env
SERVICE_FILE=/etc/systemd/system/tinker-agent.service
PORT=7122

# ---- 1. Tools + Java 25 ---------------------------------------------
say "Installing tools (curl)…"
apt-get update -qq
apt-get install -y -qq curl ca-certificates >/dev/null

# Prints the major version of a java program, like 25.
java_major() { "$1" -version 2>&1 | sed -n 's/.* version "\([0-9]*\).*/\1/p' | head -n1; }

# Finds an installed Java that's 25 or newer.
find_java() {
  local j
  for j in "$(command -v java 2>/dev/null || true)" /usr/lib/jvm/*/bin/java; do
    [ -x "$j" ] || continue
    if [ "$(java_major "$j")" -ge 25 ] 2>/dev/null; then
      readlink -f "$j"
      return 0
    fi
  done
  return 1
}

if JAVA_BIN="$(find_java)"; then
  say "Java $(java_major "$JAVA_BIN") is already installed"
else
  say "Installing Java 25 (Minecraft 26.x needs it)… this can take a few minutes"
  if ! apt-get install -y -qq openjdk-25-jre-headless >/dev/null 2>&1; then
    say "Ubuntu doesn't offer Java 25 here, so using Eclipse Temurin instead…"
    apt-get install -y -qq gpg >/dev/null
    curl -fsSL https://packages.adoptium.net/artifactory/api/gpg/key/public \
      | gpg --dearmor --yes -o /usr/share/keyrings/adoptium.gpg
    CODENAME="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
    echo "deb [signed-by=/usr/share/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb $CODENAME main" \
      > /etc/apt/sources.list.d/adoptium.list
    apt-get update -qq
    apt-get install -y -qq temurin-25-jre >/dev/null
  fi
  JAVA_BIN="$(find_java)" || die "Java 25 didn't install. Paste this whole output to the Tinker team."
  say "Installed Java $(java_major "$JAVA_BIN")"
fi

# ---- 2. The Agent program -------------------------------------------
say "Installing the Agent program to /usr/local/bin/tinker-agent…"
install -m 0755 "$BINARY" /usr/local/bin/tinker-agent.new
mv -f /usr/local/bin/tinker-agent.new /usr/local/bin/tinker-agent

say "Making sure the servers folder exists: $DATA_DIR"
sudo -u "$RUN_USER" mkdir -p "$DATA_DIR"

# ---- 3. Settings + token (password) ---------------------------------
TOKEN=""
if [ -f "$ENV_FILE" ]; then
  TOKEN="$(sed -n 's/^TINKER_TOKEN=//p' "$ENV_FILE" | head -n1)"
fi
if [ -n "$TOKEN" ]; then
  say "Keeping your existing token"
else
  say "Creating a new token (the password Dash uses)…"
  TOKEN="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi
cat > "$ENV_FILE" <<EOF
# Tinker Agent settings. After editing: sudo systemctl restart tinker-agent
TINKER_TOKEN=$TOKEN
TINKER_DATA_DIR=$DATA_DIR
TINKER_LISTEN=:$PORT
TINKER_JAVA=$JAVA_BIN
EOF
chmod 600 "$ENV_FILE" # only root can read the token

# ---- 4. Run it 24/7 with systemd ------------------------------------
say "Setting up the background service…"
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Tinker Agent
After=network-online.target
Wants=network-online.target

[Service]
User=$RUN_USER
EnvironmentFile=$ENV_FILE
ExecStart=/usr/local/bin/tinker-agent
Restart=on-failure
RestartSec=3
# On stop, only the Agent is told to quit. It then stops each Minecraft
# server properly so worlds are saved. Anything left after 90s is killed.
KillMode=mixed
TimeoutStopSec=90

[Install]
WantedBy=multi-user.target
EOF

# ---- 5. Crafty Controller (for importing) ---------------------------
if getent group crafty >/dev/null; then
  say "Found Crafty Controller: letting the Agent read its servers (for importing)"
  usermod -aG crafty "$RUN_USER"
fi

# ---- 6. Firewall ----------------------------------------------------
if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  say "Opening the firewall (Agent: home network only; Minecraft: 25565-25575)…"
  ufw allow from 192.168.0.0/16 to any port "$PORT" proto tcp >/dev/null
  ufw allow from 10.0.0.0/8 to any port "$PORT" proto tcp >/dev/null
  ufw allow 25565:25575/tcp >/dev/null
fi

# ---- 7. Start (or restart) it ---------------------------------------
systemctl daemon-reload
systemctl enable tinker-agent >/dev/null 2>&1
if systemctl is-active --quiet tinker-agent; then
  say "Restarting the Agent (running Minecraft servers are saved and stopped first)…"
else
  say "Starting the Agent…"
fi
systemctl restart tinker-agent

# ---- 8. Check it really answers -------------------------------------
OK=""
for _ in $(seq 1 20); do
  if curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       -d '{}' "http://127.0.0.1:$PORT/tinker.v1.AgentService/GetInfo" >/dev/null 2>&1; then
    OK=yes
    break
  fi
  sleep 0.5
done
[ -n "$OK" ] || die "The Agent didn't start. See why with: journalctl -u tinker-agent -n 50"

if [ -x "$RUN_HOME/tinker-agent" ]; then
  warn "The old copy at $RUN_HOME/tinker-agent isn't used any more. You can delete it."
fi

IP="$(hostname -I | awk '{print $1}')"
cat <<EOF

  ┌────────────────────────────────────────────────────────────┐
     ✅ Tinker Agent is running!

     In Tinker Dash → Connection tab, enter:
        Address:  $IP
        Port:     $PORT
        Token:    $TOKEN

     See the token again any time:  sudo cat $ENV_FILE
     Watch the Agent's log:          journalctl -u tinker-agent -f
  └────────────────────────────────────────────────────────────┘

EOF
```