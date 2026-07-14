#!/usr/bin/env bash
set -Eeuo pipefail

REPO="shuijiao1/Zeno"
DEFAULT_IMAGE="ghcr.io/shuijiao1/zeno:latest"
PUBLIC_INSTALL_URL="https://zeno.shuijiao.de"
AGENT_INSTALL_URL="https://zeno.shuijiao.de/agent/install.sh"
AGENT_WINDOWS_INSTALL_URL="https://zeno.shuijiao.de/agent/install.ps1"
NOTIFICATION_CREDENTIAL_KEY_FILE="/run/secrets/zeno_notification_credential_key"
IMAGE="${ZENO_IMAGE:-}"
REQUESTED_IMAGE=""
TARGET_VERSION_LABEL=""
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
DB_CHECK_TIMEOUT_SECONDS="${ZENO_DB_CHECK_TIMEOUT_SECONDS:-300}"
VERIFY_ATTESTATION="${ZENO_VERIFY_ATTESTATION:-true}"
TRUSTED_PROXIES="${ZENO_TRUSTED_PROXIES:-}"
DOCKER_SUBNET="${ZENO_DOCKER_SUBNET:-}"
DOCKER_GATEWAY="${ZENO_DOCKER_GATEWAY:-}"
CONTAINER_IP="${ZENO_CONTAINER_IP:-}"
ROLLBACK_IMAGE_ID=""
ROLLBACK_IMAGE_REF=""
ROLLBACK_IMAGE_DIGEST=""
ROLLBACK_IMAGE_REVISION=""

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

json_escape_string() {
  local value="$1"
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '%s' "$value"
}

ensure_single_keyring_file() {
  local keyring_path="$1"
  local source_secret_path="$2"
  reject_symlink "$keyring_path" "notification keyring" || return 1
  if [ -s "$keyring_path" ]; then
    return 0
  fi
  local secret_value=""
  IFS= read -r secret_value < "$source_secret_path" || [ -n "$secret_value" ] || return 1
  [ -n "$secret_value" ] || return 1
  local escaped_value
  escaped_value=$(json_escape_string "$secret_value") || return 1
  local tmp="${keyring_path}.new.$$"
  rm -f -- "$tmp"
  printf '{"active_key_id":"primary","keys":{"primary":"%s"}}\n' "$escaped_value" > "$tmp" || return 1
  chown "0:$ZENO_GID" "$tmp" || return 1
  chmod 640 "$tmp" || return 1
  mv -f -- "$tmp" "$keyring_path" || return 1
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

validate_bool() {
  local value="$1"
  local name="$2"
  case "$value" in
    true|false) ;;
    *) fail "${name} 必须是 true 或 false" ;;
  esac
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
  if [ -z "$IMAGE" ]; then
    if value=$(read_env_value ZENO_UPDATE_IMAGE); then
      IMAGE="$value"
    elif value=$(read_env_value ZENO_IMAGE); then
      IMAGE="$value"
    fi
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
  if [ -z "$TRUSTED_PROXIES" ] && value=$(read_env_value ZENO_TRUSTED_PROXIES); then
    TRUSTED_PROXIES="$value"
  fi
  if [ -z "$DOCKER_SUBNET" ] && value=$(read_env_value ZENO_DOCKER_SUBNET); then
    DOCKER_SUBNET="$value"
  fi
  if [ -z "$DOCKER_GATEWAY" ] && value=$(read_env_value ZENO_DOCKER_GATEWAY); then
    DOCKER_GATEWAY="$value"
  fi
  if [ -z "$CONTAINER_IP" ] && value=$(read_env_value ZENO_CONTAINER_IP); then
    CONTAINER_IP="$value"
  fi

  IMAGE="${IMAGE:-$DEFAULT_IMAGE}"
  HOST_PORT="${HOST_PORT:-18980}"
  CONTAINER_NAME="${CONTAINER_NAME:-zeno}"
  TZ_VALUE="${TZ_VALUE:-Asia/Shanghai}"
  DOCKER_SUBNET="${DOCKER_SUBNET:-172.30.250.0/29}"
  DOCKER_GATEWAY="${DOCKER_GATEWAY:-172.30.250.1}"
  CONTAINER_IP="${CONTAINER_IP:-172.30.250.2}"
  TRUSTED_PROXIES="${TRUSTED_PROXIES:-${DOCKER_GATEWAY}/32}"
  validate_image_reference "$IMAGE"
  validate_positive_int "$BACKUP_KEEP_COUNT" ZENO_BACKUP_KEEP_COUNT
  validate_positive_int "$FAILED_STATE_KEEP_COUNT" ZENO_FAILED_STATE_KEEP_COUNT
  validate_positive_int "$BUILD_KEEP_COUNT" ZENO_BUILD_KEEP_COUNT
  validate_non_negative_int "$MIN_FREE_BYTES" ZENO_MIN_FREE_BYTES
  validate_positive_int "$DB_CHECK_TIMEOUT_SECONDS" ZENO_DB_CHECK_TIMEOUT_SECONDS
  validate_bool "$VERIFY_ATTESTATION" ZENO_VERIFY_ATTESTATION
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

reject_symlink() {
  local path="$1"
  local label="$2"
  if [ -L "$path" ]; then
    echo "拒绝符号链接 ${label}: ${path}" >&2
    return 1
  fi
}

private_dir() {
  local dir="$1"
  local owner="$2"
  local mode="${3:-700}"
  reject_symlink "$dir" "目录" || return 1
  mkdir -p "$dir" || return 1
  chown "$owner" "$dir" || return 1
  chmod "$mode" "$dir" || return 1
}

secure_secret_tree() {
  local dir="$1"
  reject_symlink "$dir" "secrets 目录" || return 1
  mkdir -p "$dir" || return 1
  # Take write ownership away from the runtime UID before inspecting entries,
  # so a same-UID host process cannot race root into following a replacement.
  chown "0:$ZENO_GID" "$dir" || return 1
  chmod 750 "$dir" || return 1
  if find "$dir" -mindepth 1 -type l -print -quit | grep -q .; then
    echo "secrets 目录包含符号链接，拒绝继续: $dir" >&2
    return 1
  fi
  find "$dir" -type d -exec chown "0:$ZENO_GID" {} + -exec chmod 750 {} + || return 1
  find "$dir" -type f -exec chown "0:$ZENO_GID" {} + -exec chmod 640 {} + || return 1
}

set_runtime_permissions_for() {
  local root="$1"
  if [ -d "$root/data" ]; then
    find "$root/data" -type d -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 700 {} + || return 1
    find "$root/data" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '*-wal' -o -name '*-shm' \) \
      -exec chown "$ZENO_UID:$ZENO_GID" {} + -exec chmod 600 {} + || return 1
  fi
  if [ -d "$root/secrets" ]; then
    secure_secret_tree "$root/secrets" || return 1
  fi
}

set_install_permissions() {
  # Runtime directories are private by default. /data is writable by the fixed
  # non-root container user; /run/secrets is root-owned and group-readable only.
  # backups/build artefacts/staging stay root-only and are never mounted into the
  # running container.
  private_dir "$INSTALL_DIR/data" "$ZENO_UID:$ZENO_GID" || return 1
  private_dir "$INSTALL_DIR/secrets" "0:$ZENO_GID" 750 || return 1
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

remove_backup_rollback_image() {
  local backup="$1"
  local ref=""
  local current_ref=""
  if [ -f "$backup/BACKUP_INFO" ]; then
    if ! ref=$(read_env_value_from "$backup/BACKUP_INFO" rollback_image_ref); then
      ref=""
    fi
  fi
  [ -n "$ref" ] || return 0
  if [ -f "$INSTALL_DIR/.env" ]; then
    if ! current_ref=$(read_env_value_from "$INSTALL_DIR/.env" ZENO_IMAGE); then
      current_ref=""
    fi
  fi
  [ "$ref" != "$current_ref" ] || return 0
  if ! docker image rm "$ref" >/dev/null 2>&1; then
    echo "警告: 无法清理旧回滚镜像引用 ${ref}" >&2
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
        if [ -n "$old_dir" ]; then
          if [ "$pattern" = 'install-*' ]; then
            remove_backup_rollback_image "$old_dir"
          fi
          rm -rf -- "$old_dir"
        fi
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

capture_rollback_image() {
  [ "$HAD_EXISTING_INSTALL" -eq 1 ] || return 0
  [ -f "$INSTALL_DIR/.env" ] && [ -f "$INSTALL_DIR/docker-compose.yml" ] || return 0

  local container_id=""
  local old_image_ref=""
  if ! old_image_ref=$(read_env_value_from "$INSTALL_DIR/.env" ZENO_IMAGE); then
    old_image_ref=""
  fi
  if ! container_id=$(compose_current ps -q zeno 2>/dev/null); then
    container_id=""
  fi
  if [ -n "$container_id" ]; then
    if ! ROLLBACK_IMAGE_ID=$(docker inspect --format '{{.Image}}' "$container_id" 2>/dev/null); then
      ROLLBACK_IMAGE_ID=""
    fi
  fi
  if [ -z "$ROLLBACK_IMAGE_ID" ] && [ -n "$old_image_ref" ]; then
    if ! ROLLBACK_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$old_image_ref" 2>/dev/null); then
      ROLLBACK_IMAGE_ID=""
    fi
  fi
  if ! [[ "$ROLLBACK_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    fail "无法取得当前版本的不可变 image ID，拒绝执行不可回滚更新"
  fi

  if ! ROLLBACK_IMAGE_DIGEST=$(docker image inspect --format '{{join .RepoDigests "\n"}}' "$ROLLBACK_IMAGE_ID" 2>/dev/null | sed -n '1p'); then
    ROLLBACK_IMAGE_DIGEST=""
  fi
  if ! ROLLBACK_IMAGE_REVISION=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$ROLLBACK_IMAGE_ID" 2>/dev/null); then
    ROLLBACK_IMAGE_REVISION=""
  fi
}

prepare_rollback_image_ref() {
  [ "$HAD_EXISTING_INSTALL" -eq 1 ] || return 0
  [ -n "$ROLLBACK_IMAGE_ID" ] || fail "缺少旧版本 image ID，拒绝停止当前服务"
  local short_id="${ROLLBACK_IMAGE_ID#sha256:}"
  short_id="${short_id:0:12}"
  ROLLBACK_IMAGE_REF="zeno-rollback:$(date +%Y%m%d-%H%M%S)-${short_id}"
  docker image tag "$ROLLBACK_IMAGE_ID" "$ROLLBACK_IMAGE_REF" || fail "创建不可变回滚镜像引用失败"
  docker image inspect "$ROLLBACK_IMAGE_REF" >/dev/null || fail "回滚镜像引用验证失败"
}

resolve_target_image() {
  local image_id=""
  local digest=""
  local requested_repo=""
  local source_label=""
  local revision_label=""
  local version_label=""
  if ! image_id=$(docker image inspect --format '{{.Id}}' "$REQUESTED_IMAGE" 2>/dev/null); then
    fail "拉取后无法解析目标镜像: $REQUESTED_IMAGE"
  fi
  if ! [[ "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    fail "目标镜像缺少有效 image ID: $REQUESTED_IMAGE"
  fi
  requested_repo="${REQUESTED_IMAGE%@sha256:*}"
  if [ "$requested_repo" = "$REQUESTED_IMAGE" ]; then
    local last_component="${REQUESTED_IMAGE##*/}"
    if [[ "$last_component" == *:* ]]; then
      requested_repo="${REQUESTED_IMAGE%:*}"
    fi
  fi
  if ! digest=$(docker image inspect --format '{{join .RepoDigests "\n"}}' "$image_id" 2>/dev/null \
      | awk -v repo="$requested_repo" 'index($0, repo "@sha256:") == 1 { print; exit }'); then
    digest=""
  fi
  if ! source_label=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.source"}}' "$image_id" 2>/dev/null); then
    source_label=""
  fi
  if ! revision_label=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image_id" 2>/dev/null); then
    revision_label=""
  fi
  if ! version_label=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image_id" 2>/dev/null); then
    version_label=""
  fi

  if [[ "$REQUESTED_IMAGE" == ghcr.io/shuijiao1/zeno:* || "$REQUESTED_IMAGE" == ghcr.io/shuijiao1/zeno@sha256:* ]]; then
    [ "$source_label" = "https://github.com/shuijiao1/Zeno" ] || fail "官方镜像 source label 不匹配，拒绝安装"
    [[ "$revision_label" =~ ^[0-9a-f]{40}$ ]] || fail "官方镜像 revision label 无效，拒绝安装"
    [[ "$version_label" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9][A-Za-z0-9.-]*)?$ ]] || fail "官方镜像 version label 无效，拒绝安装"
    TARGET_VERSION_LABEL="${version_label#v}"
  fi

  if [[ "$REQUESTED_IMAGE" == *@sha256:* ]]; then
    IMAGE="$REQUESTED_IMAGE"
  elif [ -n "$digest" ]; then
    IMAGE="$digest"
  else
    local short_id="${image_id#sha256:}"
    short_id="${short_id:0:12}"
    IMAGE="zeno-candidate:$(date +%Y%m%d-%H%M%S)-${short_id}"
    docker image tag "$image_id" "$IMAGE" || fail "无法为本地目标镜像创建不可变引用"
  fi
  set_env_value_file "$STAGING_DIR/.env" ZENO_IMAGE "$IMAGE" || fail "写入不可变目标镜像失败"
  set_env_value_file "$STAGING_DIR/.env" ZENO_UPDATE_IMAGE "$REQUESTED_IMAGE" || fail "保存后续更新镜像引用失败"
  compose_staging config >/dev/null || fail "不可变目标镜像 Compose 验证失败"
}

verify_official_image_attestation() {
  [[ "$REQUESTED_IMAGE" == ghcr.io/shuijiao1/zeno:* || "$REQUESTED_IMAGE" == ghcr.io/shuijiao1/zeno@sha256:* ]] || return 0
  [ "$VERIFY_ATTESTATION" = "true" ] || {
    echo "警告: 已显式关闭官方镜像 provenance 验证。" >&2
    return 0
  }
  [[ "$IMAGE" == ghcr.io/shuijiao1/zeno@sha256:* ]] || fail "官方镜像未解析为 repo digest，无法验证 provenance"

  local machine
  local gh_arch
  local gh_sha
  machine=$(uname -m)
  case "$machine" in
    x86_64|amd64)
      gh_arch="amd64"
      gh_sha="762569efe785082b7d1feb06995efece1a9cecce16da8503ac6fdbcbea04085b"
      ;;
    aarch64|arm64)
      gh_arch="arm64"
      gh_sha="8bcec9f3ee5c7c3700359a75da774c71221064a0ba017537795aa32ac8bbb481"
      ;;
    armv6l|armv7l)
      gh_arch="armv6"
      gh_sha="72b4949ba83a19938b486c9ec58b23c97d6ec1f17f613084c163503dd3bb0b8d"
      ;;
    *) fail "不支持在 ${machine} 上验证官方镜像 provenance" ;;
  esac

  local gh_version="2.65.0"
  local archive="$STAGING_DIR/gh-${gh_version}-${gh_arch}.tar.gz"
  local extract_dir="$STAGING_DIR/gh-verifier"
  local url="https://github.com/cli/cli/releases/download/v${gh_version}/gh_${gh_version}_linux_${gh_arch}.tar.gz"
  curl -fsSL --max-time 90 -o "$archive" "$url" || fail "下载固定版本 provenance verifier 失败"
  printf '%s  %s\n' "$gh_sha" "$archive" | sha256sum -c - >/dev/null || fail "provenance verifier 校验失败"
  rm -rf -- "$extract_dir"
  mkdir -p "$extract_dir"
  tar -xzf "$archive" -C "$extract_dir" || fail "解压 provenance verifier 失败"
  local verifier="$extract_dir/gh_${gh_version}_linux_${gh_arch}/bin/gh"
  [ -x "$verifier" ] || fail "provenance verifier 缺少可执行文件"
  local certificate_identity="https://github.com/shuijiao1/Zeno/.github/workflows/docker.yml@refs/tags/v${TARGET_VERSION_LABEL}"
  if "$verifier" attestation verify "oci://${IMAGE}" --repo "$REPO" --bundle-from-oci --cert-identity "$certificate_identity" --deny-self-hosted-runners >/dev/null 2>&1; then
    return 0
  fi
  echo "警告: OCI 内置 provenance 不匹配 GitHub workflow identity，改用 GitHub attestation API 验证。" >&2
  "$verifier" attestation verify "oci://${IMAGE}" --repo "$REPO" --cert-identity "$certificate_identity" --deny-self-hosted-runners >/dev/null \
    || fail "官方镜像 provenance 验证失败"
}

set_env_value_file() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp="${file}.new.$$"
  awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    index($0, key "=") == 1 { print key "=" value; found = 1; next }
    { print }
    END { if (!found) print key "=" value }
  ' "$file" > "$tmp" || return 1
  chmod 600 "$tmp" || return 1
  mv -f "$tmp" "$file" || return 1
}

backup_info_value() {
  local dir="$1"
  local key="$2"
  read_env_value_from "$dir/BACKUP_INFO" "$key"
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
  reject_symlink "$data_dir/zeno.db" "SQLite 数据库" || return 1
  timeout --foreground "${DB_CHECK_TIMEOUT_SECONDS}s" docker run --rm \
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
  printf 'created_at=%s\nsource=%s\nimage=%s\nrollback_image_id=%s\nrollback_image_ref=%s\nrollback_image_digest=%s\nrollback_image_revision=%s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$INSTALL_DIR" "$IMAGE" \
    "$ROLLBACK_IMAGE_ID" "$ROLLBACK_IMAGE_REF" "$ROLLBACK_IMAGE_DIGEST" "$ROLLBACK_IMAGE_REVISION" > "$partial/BACKUP_INFO"
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
    [ -n "$ROLLBACK_IMAGE_REF" ] || return 1
    ZENO_IMAGE="$ROLLBACK_IMAGE_REF" compose_current up -d >/dev/null || return 1
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

  local rollback_ref
  local update_ref
  if ! rollback_ref=$(backup_info_value "$restore_stage" rollback_image_ref); then
    rollback_ref=""
  fi
  if ! update_ref=$(read_env_value_from "$restore_stage/.env" ZENO_IMAGE); then
    update_ref=""
  fi
  if [ -z "$rollback_ref" ]; then
    rollback_ref="$ROLLBACK_IMAGE_REF"
  fi
  [ -n "$rollback_ref" ] || { rm -rf -- "$restore_stage"; return 1; }
  docker image inspect "$rollback_ref" >/dev/null || { rm -rf -- "$restore_stage"; return 1; }
  set_env_value_file "$restore_stage/.env" ZENO_IMAGE "$rollback_ref" || { rm -rf -- "$restore_stage"; return 1; }
  if [ -n "$update_ref" ]; then
    set_env_value_file "$restore_stage/.env" ZENO_UPDATE_IMAGE "$update_ref" || { rm -rf -- "$restore_stage"; return 1; }
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
    else
      if [ -f "$INSTALL_DIR/docker-compose.yml" ] && [ -f "$INSTALL_DIR/.env" ]; then
        if ! compose_current down --remove-orphans >/dev/null 2>&1; then
          echo "警告: 首次安装失败后停止容器未成功，请人工检查。" >&2
        fi
      fi
      if preserve_failed_state; then
        fail "${message}；首次安装失败现场已隔离，未保留运行中的正式实例"
      fi
      fail "${message}；首次安装失败，且失败现场隔离未完成"
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
ZENO_TRUSTED_PROXIES=${TRUSTED_PROXIES}
ZENO_DOCKER_SUBNET=${DOCKER_SUBNET}
ZENO_DOCKER_GATEWAY=${DOCKER_GATEWAY}
ZENO_CONTAINER_IP=${CONTAINER_IP}
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
      ZENO_NOTIFICATION_AUTHORITY_KEYRING_FILE: /run/secrets/zeno_notification_authority_keyring.json
      ZENO_NOTIFICATION_CREDENTIAL_KEYRING_FILE: /run/secrets/zeno_notification_credential_keyring.json
      ZENO_TRUSTED_PROXIES: ${ZENO_TRUSTED_PROXIES:-172.30.250.1/32}
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
    networks:
      zeno_proxy:
        ipv4_address: ${ZENO_CONTAINER_IP:-172.30.250.2}
networks:
  zeno_proxy:
    driver: bridge
    ipam:
      config:
        - subnet: ${ZENO_DOCKER_SUBNET:-172.30.250.0/29}
          gateway: ${ZENO_DOCKER_GATEWAY:-172.30.250.1}
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
need timeout
need tar
if ! docker compose version >/dev/null 2>&1; then
  fail "未找到 docker compose 插件，请先安装 Docker Compose v2"
fi

load_existing_env_defaults
REQUESTED_IMAGE="$IMAGE"
mkdir -p "$INSTALL_DIR"
reject_symlink "$INSTALL_DIR" "安装目录" || fail "安装目录不能是符号链接"
chown 0:0 "$INSTALL_DIR"
chmod 700 "$INSTALL_DIR"
mark_existing_install
capture_rollback_image
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
resolve_target_image
verify_official_image_attestation
prepare_rollback_image_ref

admin_secret="$INSTALL_DIR/secrets/zeno_admin_token"
agent_secret="$INSTALL_DIR/secrets/zeno_agent_token"
notification_authority_secret="$INSTALL_DIR/secrets/zeno_notification_authority"
notification_credential_secret="$INSTALL_DIR/secrets/zeno_notification_credential_key"
notification_authority_keyring="$INSTALL_DIR/secrets/zeno_notification_authority_keyring.json"
notification_credential_keyring="$INSTALL_DIR/secrets/zeno_notification_credential_keyring.json"
legacy_admin="$INSTALL_DIR/data/admin-token"
legacy_agent="$INSTALL_DIR/data/agent-token"

# Notification encryption/authority keys are deliberately file-backed outside
# SQLite and .env. Create them before the offline backup so failed upgrades can
# restore the exact key material needed to decrypt existing notification
# credentials; only paths are exposed through compose/env.
secure_secret_tree "$INSTALL_DIR/secrets" || fail "创建或加固 secrets 目录失败"
for secret_path in "$notification_authority_secret" "$notification_credential_secret" "$notification_authority_keyring" "$notification_credential_keyring" "$admin_secret" "$agent_secret"; do
  reject_symlink "$secret_path" "secret 文件" || fail "secret 文件不能是符号链接"
done

trap rollback_on_error ERR
ROLLBACK_ACTIVE=1
STEP_MESSAGE="初始化通知密钥失败"
if [ ! -s "$notification_authority_secret" ]; then
  umask 077
  random_secret > "$notification_authority_secret" || fail "生成通知 authority key 失败"
fi
if [ ! -s "$notification_credential_secret" ]; then
  umask 077
  random_secret > "$notification_credential_secret" || fail "生成通知凭据加密 key 失败"
fi
chown "0:$ZENO_GID" "$notification_authority_secret" "$notification_credential_secret" || fail "设置通知 key 所有者失败"
chmod 640 "$notification_authority_secret" "$notification_credential_secret" || fail "设置通知 key 权限失败"

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
    reject_symlink "$legacy_admin" "legacy admin token" || fail "legacy admin token 不能是符号链接"
    install -o 0 -g "$ZENO_GID" -m 640 "$legacy_admin" "$admin_secret"
  else
    umask 077
    random_secret > "$admin_secret"
  fi
fi
if [ ! -s "$agent_secret" ]; then
  if [ -s "$legacy_agent" ]; then
    reject_symlink "$legacy_agent" "legacy agent token" || fail "legacy agent token 不能是符号链接"
    install -o 0 -g "$ZENO_GID" -m 640 "$legacy_agent" "$agent_secret"
  else
    umask 077
    random_secret > "$agent_secret"
  fi
fi
chown "0:$ZENO_GID" "$admin_secret" "$agent_secret" "$notification_authority_secret" "$notification_credential_secret"
chmod 640 "$admin_secret" "$agent_secret" "$notification_authority_secret" "$notification_credential_secret"
ensure_single_keyring_file "$notification_authority_keyring" "$notification_authority_secret" || fail "生成通知 authority keyring 失败"
ensure_single_keyring_file "$notification_credential_keyring" "$notification_credential_secret" || fail "生成通知凭据 keyring 失败"
chown "0:$ZENO_GID" "$notification_authority_keyring" "$notification_credential_keyring"
chmod 640 "$notification_authority_keyring" "$notification_credential_keyring"
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
