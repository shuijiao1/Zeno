#!/usr/bin/env bash
set -euo pipefail

REPO="shuijiao1/Zeno"
IMAGE="${ZENO_IMAGE:-ghcr.io/shuijiao1/zeno:latest}"
INSTALL_DIR="${ZENO_INSTALL_DIR:-/opt/zeno}"
HOST_PORT="${ZENO_HOST_PORT:-18980}"
CONTAINER_NAME="${ZENO_CONTAINER_NAME:-zeno}"
TZ_VALUE="${TZ:-Asia/Shanghai}"

fail() {
  echo "错误: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "未找到 $1"
}

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
  else
    head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '='
  fi
}

wait_health() {
  local url="http://127.0.0.1:${HOST_PORT}/health"
  for _ in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "请用 root 执行，或使用 sudo bash <(curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh)"
fi

need docker
need curl
if ! docker compose version >/dev/null 2>&1; then
  fail "未找到 docker compose 插件，请先安装 Docker Compose v2"
fi

mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/secrets"
chmod 700 "$INSTALL_DIR/secrets"

admin_secret="$INSTALL_DIR/secrets/zeno_admin_token"
agent_secret="$INSTALL_DIR/secrets/zeno_agent_token"
legacy_admin="$INSTALL_DIR/data/admin-token"
legacy_agent="$INSTALL_DIR/data/agent-token"

if [ ! -s "$admin_secret" ]; then
  if [ -s "$legacy_admin" ]; then
    install -m 600 "$legacy_admin" "$admin_secret"
  else
    umask 077
    random_secret > "$admin_secret"
  fi
fi
if [ ! -s "$agent_secret" ]; then
  if [ -s "$legacy_agent" ]; then
    install -m 600 "$legacy_agent" "$agent_secret"
  else
    umask 077
    random_secret > "$agent_secret"
  fi
fi
chmod 600 "$admin_secret" "$agent_secret"

cat > "$INSTALL_DIR/.env" <<EOF_ENV
ZENO_IMAGE=${IMAGE}
ZENO_CONTAINER_NAME=${CONTAINER_NAME}
ZENO_HOST_PORT=${HOST_PORT}
TZ=${TZ_VALUE}
EOF_ENV

cat > "$INSTALL_DIR/docker-compose.yml" <<'EOF_COMPOSE'
services:
  zeno:
    image: ${ZENO_IMAGE:-ghcr.io/shuijiao1/zeno:latest}
    container_name: ${ZENO_CONTAINER_NAME:-zeno}
    restart: unless-stopped
    environment:
      TZ: ${TZ:-Asia/Shanghai}
    ports:
      - "127.0.0.1:${ZENO_HOST_PORT:-18980}:18980"
    volumes:
      - ./data:/data
      - ./secrets:/run/secrets:ro
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://127.0.0.1:18980/health >/dev/null || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
EOF_COMPOSE

cd "$INSTALL_DIR"
docker compose pull
docker compose up -d

if ! wait_health; then
  docker compose ps >&2 || true
  docker compose logs --tail=120 zeno >&2 || true
  fail "Zeno 启动后 /health 未通过"
fi

cat <<EOF_OK
Zeno 已安装并启动
- 安装目录: ${INSTALL_DIR}
- 本机监听: http://127.0.0.1:${HOST_PORT}
- 数据目录: ${INSTALL_DIR}/data
- 首次后台 bootstrap token: ${admin_secret}

如需公网访问，请用 Caddy/Nginx 反代到 127.0.0.1:${HOST_PORT}，不要直接暴露端口。
EOF_OK
