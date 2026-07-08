#!/usr/bin/env bash
set -euo pipefail

REPO="shuijiao1/Zeno"
IMAGE="${ZENO_IMAGE:-}"
INSTALL_DIR="${ZENO_INSTALL_DIR:-/opt/zeno}"
HOST_PORT="${ZENO_HOST_PORT:-}"
CONTAINER_NAME="${ZENO_CONTAINER_NAME:-}"
TZ_VALUE="${TZ:-}"
BACKUP_DIR=""

fail() {
  echo "错误: $*" >&2
  if [ -n "${BACKUP_DIR:-}" ]; then
    echo "本次安装/更新前已备份到: ${BACKUP_DIR}" >&2
  fi
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

read_env_value() {
  local key="$1"
  local file="$INSTALL_DIR/.env"
  [ -f "$file" ] || return 1
  sed -n "s/^${key}=//p" "$file" | tail -n1
}

load_existing_env_defaults() {
  local value
  if [ -z "$IMAGE" ] && value=$(read_env_value ZENO_IMAGE); then
    IMAGE="$value"
  fi
  if [ -z "$HOST_PORT" ] && value=$(read_env_value ZENO_HOST_PORT); then
    HOST_PORT="$value"
  fi
  if [ -z "$CONTAINER_NAME" ] && value=$(read_env_value ZENO_CONTAINER_NAME); then
    CONTAINER_NAME="$value"
  fi
  if [ -z "$TZ_VALUE" ] && value=$(read_env_value TZ); then
    TZ_VALUE="$value"
  fi

  IMAGE="${IMAGE:-ghcr.io/shuijiao1/zeno:latest}"
  HOST_PORT="${HOST_PORT:-18980}"
  CONTAINER_NAME="${CONTAINER_NAME:-zeno}"
  TZ_VALUE="${TZ_VALUE:-Asia/Shanghai}"
}

backup_existing_install() {
  [ -d "$INSTALL_DIR" ] || return 0
  local has_existing=0
  local name
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$INSTALL_DIR/$name" ]; then
      has_existing=1
      break
    fi
  done
  [ "$has_existing" -eq 1 ] || return 0

  local backup_root="$INSTALL_DIR/backups"
  BACKUP_DIR="$backup_root/install-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$backup_root" "$BACKUP_DIR" 2>/dev/null || true

  for name in .env docker-compose.yml data secrets; do
    if [ -e "$INSTALL_DIR/$name" ]; then
      cp -a "$INSTALL_DIR/$name" "$BACKUP_DIR/"
    fi
  done
  printf '%s\n' "$BACKUP_DIR" > "$INSTALL_DIR/.last-install-backup"
  chmod 600 "$INSTALL_DIR/.last-install-backup" 2>/dev/null || true
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

load_existing_env_defaults
backup_existing_install

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
chmod 600 "$INSTALL_DIR/.env"

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
  fail "Zeno 启动后 /health 未通过；请检查上方日志，可用备份目录恢复 .env、docker-compose.yml、data 和 secrets"
fi

cat <<EOF_OK
Zeno 已安装并启动
- 安装目录: ${INSTALL_DIR}
- 本机监听: http://127.0.0.1:${HOST_PORT}
- 数据目录: ${INSTALL_DIR}/data
- 首次后台 bootstrap token: ${admin_secret}
$(if [ -n "${BACKUP_DIR:-}" ]; then printf '%s\n' "- 更新前备份: ${BACKUP_DIR}"; fi)

如需公网访问，请用 Caddy/Nginx 反代到 127.0.0.1:${HOST_PORT}，不要直接暴露端口。
EOF_OK
