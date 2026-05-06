#!/bin/bash
set -e

REPO="https://github.com/yuyuyuyu52/CLIProxyAPI.git"
INSTALL_DIR="/opt/cliproxy"
API_KEY="${1:-$(openssl rand -hex 16)}"

echo "==> Installing CLIProxyAPI to $INSTALL_DIR"

# 1. Docker
if ! command -v docker &>/dev/null; then
  echo "==> Installing Docker..."
  curl -fsSL https://get.docker.com | sh
  systemctl enable docker
  systemctl start docker
else
  echo "==> Docker already installed, skipping"
fi

# 2. Clone
if [ -d "$INSTALL_DIR/.git" ]; then
  echo "==> Updating existing repo..."
  git -C "$INSTALL_DIR" pull
else
  echo "==> Cloning repo..."
  git clone "$REPO" "$INSTALL_DIR"
fi

cd "$INSTALL_DIR"
mkdir -p auths logs

# 3. Config
if [ ! -f config.yaml ]; then
  echo "==> Creating config.yaml..."
  cat > config.yaml <<EOF
host: ""
port: 8317

auth-dir: "~/.cli-proxy-api"

api-keys:
  - "$API_KEY"

claude-header-defaults:
  stabilize-device-profile: true
  os: "MacOS"
  arch: "arm64"

logging-to-file: true
logs-max-total-size-mb: 500
EOF
  echo "==> API key: $API_KEY"
else
  echo "==> config.yaml already exists, skipping"
fi

# 4. docker-compose patch
if ! grep -q "cliproxy:local" docker-compose.yml; then
  sed -i 's|image: ${CLI_PROXY_IMAGE:-eceasy/cli-proxy-api:latest}|image: cliproxy:local|' docker-compose.yml
  sed -i 's|pull_policy: always|# pull_policy: always|' docker-compose.yml
fi

# 5. Build
echo "==> Building Docker image (this takes a few minutes)..."
docker build -t cliproxy:local .

# 6. Start
echo "==> Starting service..."
docker compose up -d

echo ""
echo "=============================="
echo "  Installation complete!"
echo "=============================="
echo ""
echo "  API endpoint : http://$(curl -s ifconfig.me 2>/dev/null || echo 'your-server-ip'):8317/v1"
echo "  API key      : $API_KEY"
echo ""
echo "  Put your Claude token files into: $INSTALL_DIR/auths/"
echo "  Then run: docker compose -f $INSTALL_DIR/docker-compose.yml restart"
echo ""
echo "  Codex CLI config:"
echo "    export OPENAI_API_BASE=http://$(curl -s ifconfig.me 2>/dev/null || echo 'your-server-ip'):8317/v1"
echo "    export OPENAI_API_KEY=$API_KEY"
echo ""
