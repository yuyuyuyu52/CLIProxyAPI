#!/bin/bash
set -e

REPO="yuyuyuyu52/CLIProxyAPI"
INSTALL_DIR="/opt/cliproxy"
BIN="$INSTALL_DIR/CLIProxyAPI"
API_KEY="${1:-$(openssl rand -hex 16)}"
MGMT_KEY="${2:-$(openssl rand -hex 16)}"

echo "==> Installing CLIProxyAPI"

# 1. Detect arch
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  BINARY="CLIProxyAPI-linux-amd64" ;;
  aarch64) BINARY="CLIProxyAPI-linux-arm64" ;;
  *)       echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

# 2. Download binary
echo "==> Downloading binary ($BINARY)..."
mkdir -p "$INSTALL_DIR"
curl -fSL \
  "https://github.com/$REPO/releases/download/latest/$BINARY" \
  -o "$BIN"
chmod +x "$BIN"

# 3. Auth dir
mkdir -p /root/.cli-proxy-api

# 4. Config
if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
  SERVER_IP=$(curl -s ifconfig.me 2>/dev/null || echo "your-server-ip")
  echo "==> Creating config.yaml..."
  cat > "$INSTALL_DIR/config.yaml" <<EOF
host: ""
port: 8317

remote-management:
  allow-remote: true
  secret-key: "$MGMT_KEY"
  disable-control-panel: false

auth-dir: "/root/.cli-proxy-api"

api-keys:
  - "$API_KEY"

claude-header-defaults:
  stabilize-device-profile: true
  os: "MacOS"
  arch: "arm64"

logging-to-file: true
logs-max-total-size-mb: 500
EOF
else
  echo "==> config.yaml already exists, skipping"
fi

# 5. systemd service
cat > /etc/systemd/system/cliproxy.service <<EOF
[Unit]
Description=CLIProxyAPI
After=network.target

[Service]
ExecStart=$BIN --config $INSTALL_DIR/config.yaml
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable cliproxy
systemctl restart cliproxy

sleep 2

SERVER_IP=$(curl -s ifconfig.me 2>/dev/null || echo "your-server-ip")

echo ""
echo "=============================="
echo "  Installation complete!"
echo "=============================="
echo ""
echo "  Management panel : http://$SERVER_IP:8317/management.html"
echo "  Management key   : $MGMT_KEY"
echo ""
echo "  API endpoint     : http://$SERVER_IP:8317/v1"
echo "  API key          : $API_KEY"
echo ""
echo "  Codex CLI:"
echo "    export OPENAI_API_BASE=http://$SERVER_IP:8317/v1"
echo "    export OPENAI_API_KEY=$API_KEY"
echo ""
echo "  Service status: systemctl status cliproxy"
echo ""
