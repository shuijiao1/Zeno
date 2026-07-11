#!/usr/bin/env bash
set -euo pipefail

REPO="shuijiao1/Zeno"
DEFAULT_IMAGE="ghcr.io/shuijiao1/zeno:latest"
IMAGE="${ZENO_IMAGE:-}"
INSTALL_DIR="${ZENO_INSTALL_DIR:-/opt/zeno}"
HOST_PORT="${ZENO_HOST_PORT:-}"
CONTAINER_NAME="${ZENO_CONTAINER_NAME:-}"
TZ_VALUE="${TZ:-}"
BACKUP_DIR=""
HAD_EXISTING_INSTALL=0
ZENO_UID=10001
ZENO_GID=10001

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

validate_image_reference() {
  local image="$1"
  local image_name="${image##*/}"
  if [[ "$image" != *@sha256:* && "$image_name" != *:* ]]; then
    fail "ZENO_IMAGE 必须明确 tag 或 digest，不能使用隐式 latest: ${image}"
  fi
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

  IMAGE="${IMAGE:-$DEFAULT_IMAGE}"
  HOST_PORT="${HOST_PORT:-18980}"
  CONTAINER_NAME="${CONTAINER_NAME:-zeno}"
  TZ_VALUE="${TZ_VALUE:-Asia/Shanghai}"
  validate_image_reference "$IMAGE"
}

compose_cmd() {
  docker compose --env-file "$INSTALL_DIR/.env" -f "$INSTALL_DIR/docker-compose.yml" "$@"
}

set_data_permissions() {
  # The official image runs as the fixed unprivileged zeno user. Migrate
  # existing root-owned bind mounts before any check/start so upgrades do not
  # fail with SQLite or secret permission errors.
  if [ -d "$INSTALL_DIR/data" ]; then
    chown -R "$ZENO_UID:$ZENO_GID" "$INSTALL_DIR/data"
    chmod 700 "$INSTALL_DIR/data"
  fi
  if [ -d "$INSTALL_DIR/secrets" ]; then
    chown -R "$ZENO_UID:$ZENO_GID" "$INSTALL_DIR/secrets"
    chmod 700 "$INSTALL_DIR/secrets"
  fi
  local private_file
  for private_file in \
    "$INSTALL_DIR"/data/*.db "$INSTALL_DIR"/data/*.db-wal "$INSTALL_DIR"/data/*.db-shm \
    "$INSTALL_DIR"/secrets/*; do
    [ -e "$private_file" ] || continue
    chown "$ZENO_UID:$ZENO_GID" "$private_file" || true
    chmod 600 "$private_file" || true
  done
}

prune_old_backups() {
  local backup_root="$INSTALL_DIR/backups"
  [ -d "$backup_root" ] || return 0
  find "$backup_root" -mindepth 1 -maxdepth 1 -type d -name 'install-*' -printf '%T@ %p\n' \
    | sort -rn \
    | awk 'NR>1 {print substr($0, index($0,$2))}' \
    | while IFS= read -r old_backup; do
        [ -n "$old_backup" ] && rm -rf -- "$old_backup"
      done
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
  HAD_EXISTING_INSTALL=1

  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    (cd "$INSTALL_DIR" && docker compose stop zeno >/dev/null 2>&1) || true
  fi

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
  prune_old_backups
}

restore_backup() {
  [ -n "${BACKUP_DIR:-}" ] && [ -d "$BACKUP_DIR" ] || return 1
  echo "正在从备份恢复旧配置和数据: ${BACKUP_DIR}" >&2
  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    compose_cmd stop zeno >/dev/null 2>&1 || true
  fi
  rm -rf "$INSTALL_DIR/.env" "$INSTALL_DIR/docker-compose.yml" "$INSTALL_DIR/data" "$INSTALL_DIR/secrets"
  local name
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$BACKUP_DIR/$name" ]; then
      cp -a "$BACKUP_DIR/$name" "$INSTALL_DIR/"
    fi
  done
  set_data_permissions
  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    compose_cmd up -d >/dev/null || true
  fi
}

rollback_fail() {
  local message="$1"
  if [ "$HAD_EXISTING_INSTALL" -eq 1 ]; then
    restore_backup || true
  fi
  fail "$message"
}

write_compose_file() {
  cat > "$INSTALL_DIR/docker-compose.yml" <<'EOF_COMPOSE'
services:
  zeno:
    image: ${ZENO_IMAGE:?ZENO_IMAGE must be an explicit tag or digest}
    container_name: ${ZENO_CONTAINER_NAME:-zeno}
    restart: unless-stopped
    user: "${ZENO_UID:-10001}:${ZENO_GID:-10001}"
    read_only: true
    tmpfs:
      - /tmp:size=512m,mode=1777
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      TZ: ${TZ:-Asia/Shanghai}
    ports:
      - "127.0.0.1:${ZENO_HOST_PORT:-18980}:18980"
    volumes:
      - ./data:/data
      - ./secrets:/run/secrets:ro
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://127.0.0.1:18980/ready >/dev/null || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 5m
EOF_COMPOSE
}

wait_ready() {
  local url="http://127.0.0.1:${HOST_PORT}/ready"
  for _ in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

sqlite_quick_check() {
  local db_path="$INSTALL_DIR/data/zeno.db"
  [ -e "$db_path" ] || return 0
  docker run --rm \
    -v "$INSTALL_DIR/data:/data" \
    -v "$INSTALL_DIR/secrets:/run/secrets:ro" \
    "$IMAGE" -db /data/zeno.db -check-db >/dev/null
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
set_data_permissions

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
ZENO_UID=${ZENO_UID}
ZENO_GID=${ZENO_GID}
EOF_ENV
chmod 600 "$INSTALL_DIR/.env"
write_compose_file

compose_cmd config >/dev/null || rollback_fail "Docker Compose 配置验证失败，已恢复旧版本"
compose_cmd pull || rollback_fail "拉取镜像失败，已恢复旧版本"
sqlite_quick_check || rollback_fail "SQLite quick_check 失败，已恢复旧版本"
set_data_permissions
compose_cmd up -d || rollback_fail "容器启动失败，已恢复旧版本"

if ! wait_ready; then
  compose_cmd ps >&2 || true
  compose_cmd logs --tail=120 zeno >&2 || true
  rollback_fail "Zeno 启动后 /ready 未通过，已恢复旧版本"
fi
set_data_permissions
prune_old_backups

cat <<EOF_OK
Zeno 已安装并启动
- 安装目录: ${INSTALL_DIR}
- 镜像: ${IMAGE}
- 本机监听: http://127.0.0.1:${HOST_PORT}
- 数据目录: ${INSTALL_DIR}/data
- 首次后台 bootstrap token: ${admin_secret}
$(if [ -n "${BACKUP_DIR:-}" ]; then printf '%s\n' "- 更新前备份: ${BACKUP_DIR}"; fi)

如需公网访问，请用 Caddy/Nginx 反代到 127.0.0.1:${HOST_PORT}，不要直接暴露端口。
EOF_OK
