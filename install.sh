#!/usr/bin/env bash
set -Eeuo pipefail

REPO="shuijiao1/Zeno"
DEFAULT_IMAGE="ghcr.io/shuijiao1/zeno:latest"
PUBLIC_INSTALL_URL="https://zeno.shuijiao.de"
AGENT_INSTALL_URL="https://zeno.shuijiao.de/agent/install.sh"
AGENT_WINDOWS_INSTALL_URL="https://zeno.shuijiao.de/agent/install.ps1"
NOTIFICATION_CREDENTIAL_KEY_FILE="/run/secrets/zeno_notification_credential_key"
IMAGE="${ZENO_IMAGE:-}"
INSTALL_DIR="${ZENO_INSTALL_DIR:-/opt/zeno}"
HOST_PORT="${ZENO_HOST_PORT:-}"
CONTAINER_NAME="${ZENO_CONTAINER_NAME:-}"
TZ_VALUE="${TZ:-}"
BACKUP_DIR=""
BACKUP_READY=0
STAGING_DIR=""
FAILED_STATE_DIR=""
HAD_EXISTING_INSTALL=0
CURRENT_STOPPED=0
ROLLBACK_ACTIVE=0
STEP_MESSAGE=""
ZENO_UID=10001
ZENO_GID=10001
BACKUP_KEEP_COUNT="${ZENO_BACKUP_KEEP_COUNT:-5}"
FAILED_STATE_KEEP_COUNT="${ZENO_FAILED_STATE_KEEP_COUNT:-3}"
BUILD_KEEP_COUNT="${ZENO_BUILD_KEEP_COUNT:-3}"
MIN_FREE_BYTES="${ZENO_MIN_FREE_BYTES:-67108864}"

fail() {
  echo "错误: $*" >&2
  if [ -n "${BACKUP_DIR:-}" ]; then
    if [ "$BACKUP_READY" -eq 1 ]; then
      echo "本次安装/更新前已备份到: ${BACKUP_DIR}" >&2
    else
      echo "本次安装/更新备份未完成，残缺备份不会用于恢复: ${BACKUP_DIR}" >&2
    fi
  fi
  if [ -n "${FAILED_STATE_DIR:-}" ]; then
    echo "失败现场已保留到: ${FAILED_STATE_DIR}" >&2
  fi
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "未找到 $1"
}

cleanup_staging() {
  if [ -n "${STAGING_DIR:-}" ] && [ -d "$STAGING_DIR" ]; then
    rm -rf -- "$STAGING_DIR"
  fi
  if [ "$BACKUP_READY" -eq 0 ] && [ -n "${BACKUP_DIR:-}" ] && [ -d "$BACKUP_DIR" ]; then
    case "$(basename "$BACKUP_DIR")" in
      .partial-install-*) rm -rf -- "$BACKUP_DIR" ;;
    esac
  fi
}

on_exit() {
  cleanup_staging
}

trap on_exit EXIT

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
  else
    head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '='
  fi
}

read_env_value_from() {
  local file="$1"
  local key="$2"
  [ -f "$file" ] || return 1
  sed -n "s/^${key}=//p" "$file" | tail -n1
}

read_env_value() {
  read_env_value_from "$INSTALL_DIR/.env" "$1"
}

validate_positive_int() {
  local value="$1"
  local name="$2"
  if ! [[ "$value" =~ ^[0-9]+$ ]] || [ "$value" -lt 1 ]; then
    fail "${name} 必须是正整数"
  fi
}

validate_non_negative_int() {
  local value="$1"
  local name="$2"
  if ! [[ "$value" =~ ^[0-9]+$ ]]; then
    fail "${name} 必须是非负整数"
  fi
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
  validate_positive_int "$BACKUP_KEEP_COUNT" ZENO_BACKUP_KEEP_COUNT
  validate_positive_int "$FAILED_STATE_KEEP_COUNT" ZENO_FAILED_STATE_KEEP_COUNT
  validate_positive_int "$BUILD_KEEP_COUNT" ZENO_BUILD_KEEP_COUNT
  validate_non_negative_int "$MIN_FREE_BYTES" ZENO_MIN_FREE_BYTES
}

compose_from() {
  local dir="$1"
  shift
  docker compose --env-file "$dir/.env" -f "$dir/docker-compose.yml" "$@"
}

compose_current() {
  compose_from "$INSTALL_DIR" "$@"
}

compose_staging() {
  compose_from "$STAGING_DIR" "$@"
}

private_dir() {
  local dir="$1"
  local owner="$2"
  mkdir -p "$dir" || return 1
  chown "$owner" "$dir" || return 1
  chmod 700 "$dir" || return 1
}

set_runtime_permissions_for() {
  local root="$1"
  if [ -d "$root/data" ]; then
    find "$root/data" -type d -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 700 {} + || return 1
    find "$root/data" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '*-wal' -o -name '*-shm' \) \
      -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 600 {} + || return 1
  fi
  if [ -d "$root/secrets" ]; then
    find "$root/secrets" -type d -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 700 {} + || return 1
    find "$root/secrets" -type f -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 600 {} + || return 1
  fi
}

set_install_permissions() {
  # Runtime directories are private by default. /data and /run/secrets must be
  # readable/writable by the fixed non-root container user (10001:10001), while
  # backups/build artefacts/staging stay root-only and are never mounted into the
  # running container.
  private_dir "$INSTALL_DIR/data" "$ZENO_UID:$ZENO_GID" || return 1
  private_dir "$INSTALL_DIR/secrets" "$ZENO_UID:$ZENO_GID" || return 1
  private_dir "$INSTALL_DIR/backups" "0:0" || return 1
  private_dir "$INSTALL_DIR/builds" "0:0" || return 1
  private_dir "$INSTALL_DIR/.staging" "0:0" || return 1

  set_runtime_permissions_for "$INSTALL_DIR" || return 1
}

set_lifecycle_permissions() {
  private_dir "$INSTALL_DIR/backups" "0:0" || return 1
  private_dir "$INSTALL_DIR/builds" "0:0" || return 1
  private_dir "$INSTALL_DIR/.staging" "0:0" || return 1
}

portable_stat_mtime() {
  if stat -c '%Y %n' "$1" >/dev/null 2>&1; then
    stat -c '%Y %n' "$1"
  else
    stat -f '%m %N' "$1"
  fi
}

prune_named_dirs() {
  local root="$1"
  local pattern="$2"
  local keep="$3"
  local effective_keep="$keep"
  if [ "$pattern" = 'install-*' ] && [ -n "${BACKUP_DIR:-}" ] && [ "$BACKUP_READY" -eq 1 ] && [ "$keep" -gt 0 ]; then
    effective_keep=$((keep - 1))
  fi
  [ -d "$root" ] || return 0
  find "$root" -mindepth 1 -maxdepth 1 -type d -name "$pattern" -print \
    | while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        if [ "$pattern" = 'install-*' ] && [ ! -f "$candidate/.zeno-backup-complete" ]; then
          continue
        fi
        if [ -n "${BACKUP_DIR:-}" ] && [ "$candidate" = "$BACKUP_DIR" ]; then
          continue
        fi
        portable_stat_mtime "$candidate"
      done \
    | sort -rn \
    | awk -v keep="$effective_keep" 'NR>keep {sub(/^[^ ]+ /, ""); print}' \
    | while IFS= read -r old_dir; do
        [ -n "$old_dir" ] && rm -rf -- "$old_dir"
      done
}

prune_named_files() {
  local root="$1"
  local pattern="$2"
  local keep="$3"
  [ -d "$root" ] || return 0
  find "$root" -mindepth 1 -maxdepth 1 -type f -name "$pattern" -print \
    | while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        portable_stat_mtime "$candidate"
      done \
    | sort -rn \
    | awk -v keep="$keep" 'NR>keep {sub(/^[^ ]+ /, ""); print}' \
    | while IFS= read -r old_file; do
        [ -n "$old_file" ] && rm -f -- "$old_file"
      done
}

prune_lifecycle_artifacts() {
  prune_named_dirs "$INSTALL_DIR/backups" 'install-*' "$BACKUP_KEEP_COUNT"
  prune_named_dirs "$INSTALL_DIR/backups" '.partial-install-*' 1
  prune_named_dirs "$INSTALL_DIR/backups" 'failed-*' "$FAILED_STATE_KEEP_COUNT"
  prune_named_dirs "$INSTALL_DIR/builds" 'release-*' "$BUILD_KEEP_COUNT"
  prune_named_dirs "$INSTALL_DIR/builds" 'build-*' "$BUILD_KEEP_COUNT"
  prune_named_files "$INSTALL_DIR/builds" 'zeno-*.tar.gz' "$BUILD_KEEP_COUNT"
  prune_named_files "$INSTALL_DIR/builds" 'zeno-*.zip' "$BUILD_KEEP_COUNT"
  prune_named_dirs "$INSTALL_DIR/.staging" 'install-*' 1
}

path_size_bytes() {
  local path="$1"
  [ -e "$path" ] || { printf '0\n'; return 0; }
  local blocks
  blocks=$(du -sk "$path" | awk '{print $1}')
  printf '%s\n' $((blocks * 1024))
}

backup_source_size_bytes() {
  local total=0
  local name size
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$INSTALL_DIR/$name" ]; then
      size=$(path_size_bytes "$INSTALL_DIR/$name")
      total=$((total + size))
    fi
  done
  printf '%s\n' "$total"
}

available_bytes_for() {
  local path="$1"
  local probe="$path"
  while [ ! -e "$probe" ] && [ "$probe" != "/" ]; do
    probe=$(dirname "$probe")
  done
  local available_kb
  available_kb=$(df -Pk "$probe" | awk 'NR==2 {print $4}')
  printf '%s\n' $((available_kb * 1024))
}

ensure_disk_space() {
  local path="$1"
  local required_bytes="$2"
  local label="$3"
  local available
  available=$(available_bytes_for "$path")
  if [ "$available" -lt "$required_bytes" ]; then
    fail "${label} 磁盘空间不足：需要至少 ${required_bytes} bytes，可用 ${available} bytes"
  fi
}

preflight_disk_space() {
  local source_bytes=0
  if [ "$HAD_EXISTING_INSTALL" -eq 1 ]; then
    source_bytes=$(backup_source_size_bytes)
  fi
  # Worst-case rollback keeps the failed/current install in place while holding
  # an offline backup, a fully verified restore staging copy, and an expendable
  # SQLite-check scratch copy derived from that staging payload.
  local required=$((source_bytes * 3 + MIN_FREE_BYTES))
  ensure_disk_space "$INSTALL_DIR" "$required" "安装/备份预检"
}

mark_existing_install() {
  [ -d "$INSTALL_DIR" ] || return 0
  local name
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$INSTALL_DIR/$name" ]; then
      HAD_EXISTING_INSTALL=1
      return 0
    fi
  done
}

write_manifest() {
  local dir="$1"
  (
    cd "$dir"
    find . -type f ! -name 'MANIFEST.sha256' ! -name '.zeno-backup-complete' -print \
      | LC_ALL=C sort \
      | while IFS= read -r file; do
          sha256sum "$file"
        done > MANIFEST.sha256
  )
  chmod 600 "$dir/MANIFEST.sha256"
}

verify_manifest() {
  local dir="$1"
  [ -f "$dir/MANIFEST.sha256" ] || { echo "备份缺少 MANIFEST.sha256" >&2; return 1; }
  (cd "$dir" && sha256sum -c MANIFEST.sha256 >/dev/null)
}

verify_backup_marker() {
  local dir="$1"
  [ -f "$dir/.zeno-backup-complete" ] || { echo "备份缺少完整性标记: $dir/.zeno-backup-complete" >&2; return 1; }
}

copy_backup_payload() {
  local source="$1"
  local target="$2"
  local name
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$source/$name" ]; then
      cp -a "$source/$name" "$target/" || return 1
    fi
  done
}

copy_restore_snapshot() {
  local source="$1"
  local target="$2"
  cp -a "$source/." "$target/" || return 1
}

sqlite_quick_check_dir() {
  local data_dir="$1"
  local secrets_dir="$2"
  [ -e "$data_dir/zeno.db" ] || return 0
  docker run --rm \
    -v "$data_dir:/data" \
    -v "$secrets_dir:/run/secrets:ro" \
    "$IMAGE" -db /data/zeno.db -check-db >/dev/null
}

sqlite_quick_check_scratch_copy() {
  local data_dir="$1"
  local secrets_dir="$2"
  [ -e "$data_dir/zeno.db" ] || return 0
  local scratch="$INSTALL_DIR/.staging/check-$(date +%Y%m%d-%H%M%S)-$$-${RANDOM}"
  rm -rf -- "$scratch"
  mkdir -p "$INSTALL_DIR/.staging" || return 1
  chmod 700 "$INSTALL_DIR/.staging" || return 1
  mkdir -p "$scratch/data" "$scratch/secrets" || return 1

  if [ -d "$data_dir" ]; then
    if ! cp -a "$data_dir/." "$scratch/data/"; then
      rm -rf -- "$scratch"
      return 1
    fi
  fi
  if [ -d "$secrets_dir" ]; then
    if ! cp -a "$secrets_dir/." "$scratch/secrets/"; then
      rm -rf -- "$scratch"
      return 1
    fi
  fi
  if ! set_runtime_permissions_for "$scratch"; then
    rm -rf -- "$scratch"
    return 1
  fi
  if ! sqlite_quick_check_dir "$scratch/data" "$scratch/secrets"; then
    rm -rf -- "$scratch"
    return 1
  fi
  rm -rf -- "$scratch"
}

sqlite_quick_check_current() {
  sqlite_quick_check_dir "$INSTALL_DIR/data" "$INSTALL_DIR/secrets"
}

verify_backup_for_restore() {
  local dir="$1"
  verify_backup_marker "$dir" || return 1
  verify_manifest "$dir" || return 1
}

atomic_record_last_backup() {
  local target="$INSTALL_DIR/.last-install-backup"
  local tmp="${target}.new.$$"
  printf '%s\n' "$BACKUP_DIR" > "$tmp"
  chmod 600 "$tmp"
  mv -f "$tmp" "$target"
}

create_offline_backup() {
  [ "$HAD_EXISTING_INSTALL" -eq 1 ] || return 0
  local backup_root="$INSTALL_DIR/backups"
  local stamp
  stamp=$(date +%Y%m%d-%H%M%S)
  local final="$backup_root/install-${stamp}-$$"
  local partial="$backup_root/.partial-install-${stamp}-$$"
  BACKUP_DIR="$partial"
  BACKUP_READY=0
  rm -rf -- "$partial"
  mkdir -p "$partial"
  chmod 700 "$backup_root" "$partial"

  copy_backup_payload "$INSTALL_DIR" "$partial"
  printf 'created_at=%s\nsource=%s\nimage=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$INSTALL_DIR" "$IMAGE" > "$partial/BACKUP_INFO"
  chmod 600 "$partial/BACKUP_INFO"
  sqlite_quick_check_scratch_copy "$partial/data" "$partial/secrets"
  write_manifest "$partial"
  verify_manifest "$partial"
  printf 'complete_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$partial/.zeno-backup-complete"
  chmod 600 "$partial/.zeno-backup-complete"
  mv -T "$partial" "$final"
  BACKUP_DIR="$final"
  BACKUP_READY=1
  atomic_record_last_backup
}

restart_current_install() {
  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    compose_current up -d >/dev/null || return 1
    local current_port="$HOST_PORT"
    local env_port
    if env_port=$(read_env_value_from "$INSTALL_DIR/.env" ZENO_HOST_PORT); then
      current_port="$env_port"
    fi
    wait_ready "$current_port" || return 1
  fi
}

preserve_failed_state() {
  local backup_root="$INSTALL_DIR/backups"
  local failed="$backup_root/failed-install-$(date +%Y%m%d-%H%M%S)-$$"
  mkdir -p "$failed" || return 1
  chmod 700 "$failed" || return 1
  local name
  local moved=0
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$INSTALL_DIR/$name" ]; then
      mv "$INSTALL_DIR/$name" "$failed/" || return 1
      moved=1
    fi
  done
  if [ "$moved" -eq 1 ]; then
    printf '%s\n' "$failed" > "$INSTALL_DIR/.last-failed-install-state" || return 1
    chmod 600 "$INSTALL_DIR/.last-failed-install-state" || return 1
    FAILED_STATE_DIR="$failed"
  else
    rmdir "$failed" || return 1
  fi
}

restore_backup() {
  if [ -z "${BACKUP_DIR:-}" ] || [ ! -d "$BACKUP_DIR" ] || [ "$BACKUP_READY" -ne 1 ]; then
    echo "没有通过完整性标记的可用备份，无法自动恢复。" >&2
    return 1
  fi
  verify_backup_for_restore "$BACKUP_DIR" || return 1
  echo "正在从备份恢复旧配置和数据: ${BACKUP_DIR}" >&2

  local restore_stage="$INSTALL_DIR/.staging/restore-$(date +%Y%m%d-%H%M%S)-$$"
  rm -rf -- "$restore_stage"
  mkdir -p "$restore_stage" || return 1
  chmod 700 "$restore_stage" || return 1

  if ! copy_restore_snapshot "$BACKUP_DIR" "$restore_stage"; then
    rm -rf -- "$restore_stage"
    return 1
  fi
  if ! verify_manifest "$restore_stage"; then
    rm -rf -- "$restore_stage"
    return 1
  fi
  if ! sqlite_quick_check_scratch_copy "$restore_stage/data" "$restore_stage/secrets"; then
    rm -rf -- "$restore_stage"
    return 1
  fi
  if ! verify_manifest "$restore_stage"; then
    rm -rf -- "$restore_stage"
    return 1
  fi
  if ! set_runtime_permissions_for "$restore_stage"; then
    rm -rf -- "$restore_stage"
    return 1
  fi

  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    compose_current stop zeno >/dev/null || return 1
  fi

  preserve_failed_state || return 1

  local name
  for name in .env docker-compose.yml data secrets; do
    if [ -e "$restore_stage/$name" ]; then
      mv "$restore_stage/$name" "$INSTALL_DIR/" || return 1
    fi
  done
  rm -rf -- "$restore_stage"
  set_install_permissions || return 1

  if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
    compose_current config >/dev/null || return 1
    compose_current up -d >/dev/null || return 1
    local restored_port="$HOST_PORT"
    local env_port
    if env_port=$(read_env_value_from "$INSTALL_DIR/.env" ZENO_HOST_PORT); then
      restored_port="$env_port"
    fi
    wait_ready "$restored_port" || return 1
  fi
}

rollback_on_error() {
  local exit_code=$?
  if [ "$ROLLBACK_ACTIVE" -eq 1 ]; then
    trap - ERR
    ROLLBACK_ACTIVE=0
    local message="${STEP_MESSAGE:-安装/更新失败}"
    echo "错误: ${message}" >&2
    if [ "$HAD_EXISTING_INSTALL" -eq 1 ]; then
      if [ "$BACKUP_READY" -eq 1 ]; then
        if restore_backup; then
          fail "${message}，已恢复旧版本"
        fi
        fail "${message}，且自动恢复失败；请检查当前容器状态并使用备份手动恢复"
      fi
      if [ "$CURRENT_STOPPED" -eq 1 ]; then
        if restart_current_install; then
          fail "${message}，旧版本已重新启动；未完成备份不会用于恢复"
        fi
        fail "${message}，旧版本重新启动失败；未完成备份不会用于恢复"
      fi
    fi
    fail "$message"
  fi
  exit "$exit_code"
}

write_env_file() {
  local dir="$1"
  cat > "$dir/.env" <<EOF_ENV
ZENO_IMAGE=${IMAGE}
ZENO_CONTAINER_NAME=${CONTAINER_NAME}
ZENO_HOST_PORT=${HOST_PORT}
TZ=${TZ_VALUE}
ZENO_UID=${ZENO_UID}
ZENO_GID=${ZENO_GID}
EOF_ENV
  chmod 600 "$dir/.env"
}

write_compose_file() {
  local dir="$1"
  cat > "$dir/docker-compose.yml" <<'EOF_COMPOSE'
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
      ZENO_NOTIFICATIONS_DISABLED: ${ZENO_NOTIFICATIONS_DISABLED:-false}
      ZENO_NOTIFICATION_CREDENTIAL_KEY_FILE: /run/secrets/zeno_notification_credential_key
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
  local port="$1"
  local url="http://127.0.0.1:${port}/ready"
  for _ in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

show_new_container_diagnostics() {
  if ! compose_current ps >&2; then
    echo "无法获取 docker compose ps 诊断信息。" >&2
  fi
  if ! compose_current logs --tail=120 zeno >&2; then
    echo "无法获取 docker compose logs 诊断信息。" >&2
  fi
}

wait_ready_or_fail() {
  if wait_ready "$HOST_PORT"; then
    return 0
  fi
  show_new_container_diagnostics
  return 1
}

atomic_install_file() {
  local src="$1"
  local dest="$2"
  local tmp="${dest}.new.$$"
  cp "$src" "$tmp"
  if [ "$(basename "$dest")" = ".env" ]; then
    chmod 600 "$tmp"
  fi
  mv -f "$tmp" "$dest"
}

if [ "$(id -u)" -ne 0 ]; then
  fail "请用 root 执行，或使用 sudo bash <(curl -fsSL ${PUBLIC_INSTALL_URL})"
fi

need docker
need curl
need sha256sum
if ! docker compose version >/dev/null 2>&1; then
  fail "未找到 docker compose 插件，请先安装 Docker Compose v2"
fi

load_existing_env_defaults
mkdir -p "$INSTALL_DIR"
mark_existing_install
set_lifecycle_permissions
preflight_disk_space
prune_lifecycle_artifacts

STAGING_DIR="$INSTALL_DIR/.staging/install-$(date +%Y%m%d-%H%M%S)-$$"
mkdir -p "$STAGING_DIR"
chmod 700 "$STAGING_DIR"
write_env_file "$STAGING_DIR"
write_compose_file "$STAGING_DIR"

# Static validation and image retrieval happen before the old service is
# stopped. The outage window starts only after these network/config checks pass.
compose_staging config >/dev/null || fail "Docker Compose 配置验证失败，未停止旧版本"
compose_staging pull || fail "拉取镜像失败，未停止旧版本"

admin_secret="$INSTALL_DIR/secrets/zeno_admin_token"
agent_secret="$INSTALL_DIR/secrets/zeno_agent_token"
notification_authority_secret="$INSTALL_DIR/secrets/zeno_notification_authority"
notification_credential_secret="$INSTALL_DIR/secrets/zeno_notification_credential_key"
legacy_admin="$INSTALL_DIR/data/admin-token"
legacy_agent="$INSTALL_DIR/data/agent-token"

# Notification encryption/authority keys are deliberately file-backed outside
# SQLite and .env. Create them before the offline backup so failed upgrades can
# restore the exact key material needed to decrypt existing notification
# credentials; only paths are exposed through compose/env.
mkdir -p "$INSTALL_DIR/secrets" || fail "创建 secrets 目录失败"
chmod 700 "$INSTALL_DIR/secrets" || fail "设置 secrets 目录权限失败"
if [ ! -s "$notification_authority_secret" ]; then
  umask 077
  random_secret > "$notification_authority_secret" || fail "生成通知 authority key 失败"
fi
if [ ! -s "$notification_credential_secret" ]; then
  umask 077
  random_secret > "$notification_credential_secret" || fail "生成通知凭据加密 key 失败"
fi
chmod 600 "$notification_authority_secret" "$notification_credential_secret" || fail "设置通知 key 权限失败"

trap rollback_on_error ERR
ROLLBACK_ACTIVE=1

STEP_MESSAGE="停止旧版本失败"
if [ "$HAD_EXISTING_INSTALL" -eq 1 ] && [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
  compose_current stop zeno >/dev/null
  CURRENT_STOPPED=1
fi

STEP_MESSAGE="创建离线备份失败"
create_offline_backup

STEP_MESSAGE="设置运行目录权限失败"
set_install_permissions

STEP_MESSAGE="SQLite quick_check 失败"
sqlite_quick_check_current

STEP_MESSAGE="初始化密钥失败"
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
chmod 600 "$admin_secret" "$agent_secret" "$notification_authority_secret" "$notification_credential_secret"
set_install_permissions

STEP_MESSAGE="原子切换配置失败"
atomic_install_file "$STAGING_DIR/.env" "$INSTALL_DIR/.env"
atomic_install_file "$STAGING_DIR/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"

STEP_MESSAGE="容器启动失败"
set_install_permissions
compose_current up -d >/dev/null
CURRENT_STOPPED=0

STEP_MESSAGE="Zeno 启动后 /ready 未通过"
wait_ready_or_fail

ROLLBACK_ACTIVE=0
trap - ERR
set_install_permissions
prune_lifecycle_artifacts

cat <<EOF_OK
Zeno 已安装并启动
- 安装目录: ${INSTALL_DIR}
- 镜像: ${IMAGE}
- 本机监听: http://127.0.0.1:${HOST_PORT}
- 数据目录: ${INSTALL_DIR}/data
- Agent 安装入口: ${AGENT_INSTALL_URL} / ${AGENT_WINDOWS_INSTALL_URL}
- 通知凭据加密 key 文件: ${NOTIFICATION_CREDENTIAL_KEY_FILE}
- 首次后台 bootstrap token: ${admin_secret}
$(if [ -n "${BACKUP_DIR:-}" ] && [ "$BACKUP_READY" -eq 1 ]; then printf '%s\n' "- 更新前备份: ${BACKUP_DIR}"; fi)

如需公网访问，请用 Caddy/Nginx 反代到 127.0.0.1:${HOST_PORT}，不要直接暴露端口。
EOF_OK
