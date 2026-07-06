#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: deploy-local-release.sh --archive <zeno-*.tar.gz> [options]

Installs or updates a Zeno Controller release on the current Linux host.
Safe order: stop Agent -> switch current -> restart Controller -> wait /health -> start Agent.

Options:
  --archive <path>              Release archive to install (required)
  --install-dir <dir>           Default: /opt/zeno
  --data-dir <dir>              Default: <install-dir>/data
  --systemd-dir <dir>           Default: /etc/systemd/system
  --run-user <user>             Default: root
  --controller-addr <addr>      Default: 0.0.0.0:18980
  --controller-url <url>        Default: http://127.0.0.1:18980
  --node-id <id>                Default: hytron
  --agent-interval <duration>   Default: 2s
  --agent-version <version>     Default: release REVISION
  --agent-token-file <path>     Default: <data-dir>/agent-token
  --admin-token-file <path>     Default: <data-dir>/admin-token
  --seed-preview                Pass -seed-preview to Controller
  --no-agent                    Do not manage zeno-agent.service
  --health-timeout <seconds>    Default: 60
  --dry-run                     Extract/render into install-dir but do not call systemctl or switch live services
  -h, --help                    Show help
USAGE
}

archive=""
install_dir="/opt/zeno"
data_dir=""
systemd_dir="/etc/systemd/system"
run_user="root"
controller_addr="0.0.0.0:18980"
controller_url="http://127.0.0.1:18980"
node_id="hytron"
agent_interval="2s"
agent_version=""
agent_token_file=""
admin_token_file=""
seed_preview=0
manage_agent=1
health_timeout=60
dry_run=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --archive) archive="${2:-}"; shift 2 ;;
    --install-dir) install_dir="${2:-}"; shift 2 ;;
    --data-dir) data_dir="${2:-}"; shift 2 ;;
    --systemd-dir) systemd_dir="${2:-}"; shift 2 ;;
    --run-user) run_user="${2:-}"; shift 2 ;;
    --controller-addr) controller_addr="${2:-}"; shift 2 ;;
    --controller-url) controller_url="${2:-}"; shift 2 ;;
    --node-id) node_id="${2:-}"; shift 2 ;;
    --agent-interval) agent_interval="${2:-}"; shift 2 ;;
    --agent-version) agent_version="${2:-}"; shift 2 ;;
    --agent-token-file) agent_token_file="${2:-}"; shift 2 ;;
    --admin-token-file) admin_token_file="${2:-}"; shift 2 ;;
    --seed-preview) seed_preview=1; shift ;;
    --no-agent) manage_agent=0; shift ;;
    --health-timeout) health_timeout="${2:-}"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$archive" ] || [ ! -f "$archive" ]; then
  echo "--archive must point to an existing release archive" >&2
  exit 2
fi
case "$health_timeout" in
  ''|*[!0-9]*) echo "--health-timeout must be an integer" >&2; exit 2 ;;
esac
if [ -z "$data_dir" ]; then data_dir="$install_dir/data"; fi
if [ -z "$agent_token_file" ]; then agent_token_file="$data_dir/agent-token"; fi
if [ -z "$admin_token_file" ]; then admin_token_file="$data_dir/admin-token"; fi

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }; }
need_cmd tar
need_cmd sed
if [ "$dry_run" -eq 0 ]; then
  need_cmd systemctl
  need_cmd curl
fi

generate_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48
    printf '\n'
  fi
}

ensure_secret_file() {
  local path="$1"
  mkdir -p "$(dirname "$path")"
  if [ ! -s "$path" ]; then
    umask 077
    generate_secret > "$path"
  fi
  chmod 600 "$path"
}

sed_escape() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

render_template() {
  local template="$1"
  local output="$2"
  local version="$3"
  local reported_agent_version="$4"
  local seed_flag=""
  if [ "$seed_preview" -eq 1 ]; then seed_flag=" -seed-preview"; fi
  mkdir -p "$(dirname "$output")"
  sed \
    -e "s/{{RUN_USER}}/$(sed_escape "$run_user")/g" \
    -e "s/{{INSTALL_DIR}}/$(sed_escape "$install_dir")/g" \
    -e "s/{{DATA_DIR}}/$(sed_escape "$data_dir")/g" \
    -e "s/{{CONTROLLER_BINARY}}/$(sed_escape "$install_dir/current/zeno-controller")/g" \
    -e "s/{{AGENT_BINARY}}/$(sed_escape "$install_dir/current/zeno-agent")/g" \
    -e "s/{{AGENT_WORKING_DIR}}/$(sed_escape "$install_dir/current")/g" \
    -e "s/{{AGENT_AFTER_CONTROLLER}}/$(sed_escape " zeno-controller.service")/g" \
    -e "s/{{AGENT_REQUIRES_CONTROLLER}}/$(sed_escape "Requires=zeno-controller.service")/g" \
    -e "s/{{CONTROLLER_ADDR}}/$(sed_escape "$controller_addr")/g" \
    -e "s/{{CONTROLLER_URL}}/$(sed_escape "$controller_url")/g" \
    -e "s/{{NODE_ID}}/$(sed_escape "$node_id")/g" \
    -e "s/{{AGENT_INTERVAL}}/$(sed_escape "$agent_interval")/g" \
    -e "s/{{AGENT_VERSION}}/$(sed_escape "$reported_agent_version")/g" \
    -e "s/{{AGENT_TOKEN_FILE}}/$(sed_escape "$agent_token_file")/g" \
    -e "s/{{ADMIN_TOKEN_FILE}}/$(sed_escape "$admin_token_file")/g" \
    -e "s/{{SEED_PREVIEW_FLAG}}/$(sed_escape "$seed_flag")/g" \
    -e "s/{{VERSION}}/$(sed_escape "$version")/g" \
    "$template" > "$output"
}

health_url() {
  local host_port
  host_port=${controller_addr#*:}
  if [ "$controller_addr" = "$host_port" ]; then
    host_port="18980"
  fi
  printf 'http://127.0.0.1:%s/health' "$host_port"
}

wait_health() {
  local url="$1"
  local timeout="$2"
  local i
  for i in $(seq 1 "$timeout"); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

extract_parent=$(mktemp -d "${TMPDIR:-/tmp}/zeno-deploy.XXXXXX")
cleanup() { rm -rf "$extract_parent"; }
trap cleanup EXIT

tar -xzf "$archive" -C "$extract_parent"
release_source=$(find "$extract_parent" -mindepth 1 -maxdepth 1 -type d | head -n 1)
if [ -z "$release_source" ]; then
  echo "archive does not contain a release directory" >&2
  exit 1
fi
for required in zeno-controller zeno-agent REVISION packaging/systemd/zeno-controller.service packaging/systemd/zeno-agent.service; do
  if [ ! -e "$release_source/$required" ]; then
    echo "release missing $required" >&2
    exit 1
  fi
done
version=$(tr -d '[:space:]' < "$release_source/REVISION")
if [ -z "$version" ]; then
  echo "release REVISION is empty" >&2
  exit 1
fi
if [ -z "$agent_version" ]; then
  agent_version="$version"
fi
release_name=$(basename "$release_source")
release_dir="$install_dir/releases/$release_name"

mkdir -p "$install_dir/releases" "$data_dir"
ensure_secret_file "$agent_token_file"
ensure_secret_file "$admin_token_file"
rm -rf "$release_dir.tmp"
cp -a "$release_source" "$release_dir.tmp"
mv -Tf "$release_dir.tmp" "$release_dir"

unit_output_dir="$systemd_dir"
controller_unit_backup=""
agent_unit_backup=""
previous=""
agent_was_active=0
if [ "$dry_run" -eq 1 ]; then
  unit_output_dir="$install_dir/systemd-dry-run"
else
  if [ -e "$install_dir/current" ]; then
    previous=$(readlink -f "$install_dir/current" || true)
  fi
  if [ "$manage_agent" -eq 1 ] && systemctl is-active --quiet zeno-agent.service; then
    agent_was_active=1
  fi
  controller_unit_backup="$extract_parent/zeno-controller.service.previous"
  agent_unit_backup="$extract_parent/zeno-agent.service.previous"
  if [ -f "$systemd_dir/zeno-controller.service" ]; then cp "$systemd_dir/zeno-controller.service" "$controller_unit_backup"; fi
  if [ -f "$systemd_dir/zeno-agent.service" ]; then cp "$systemd_dir/zeno-agent.service" "$agent_unit_backup"; fi
fi
render_template "$release_dir/packaging/systemd/zeno-controller.service" "$unit_output_dir/zeno-controller.service" "$version" "$agent_version"
render_template "$release_dir/packaging/systemd/zeno-agent.service" "$unit_output_dir/zeno-agent.service" "$version" "$agent_version"

if [ "$dry_run" -eq 1 ]; then
  printf 'dry-run ok: release=%s units=%s\n' "$release_dir" "$unit_output_dir"
  exit 0
fi

rollback() {
  local reason="$1"
  echo "deploy failed: $reason" >&2
  if [ -n "$previous" ] && [ -d "$previous" ]; then
    local previous_version
    previous_version=$(tr -d '[:space:]' < "$previous/REVISION" 2>/dev/null || true)
    if [ -z "$previous_version" ]; then previous_version="previous"; fi
    ln -sfn "$previous" "$install_dir/current"
    if [ -f "$previous/packaging/systemd/zeno-controller.service" ]; then
      render_template "$previous/packaging/systemd/zeno-controller.service" "$systemd_dir/zeno-controller.service" "$previous_version"
    elif [ -n "${controller_unit_backup:-}" ] && [ -f "$controller_unit_backup" ]; then
      cp "$controller_unit_backup" "$systemd_dir/zeno-controller.service"
    fi
    if [ "$manage_agent" -eq 1 ]; then
      if [ -f "$previous/packaging/systemd/zeno-agent.service" ]; then
        render_template "$previous/packaging/systemd/zeno-agent.service" "$systemd_dir/zeno-agent.service" "$previous_version"
      elif [ -n "${agent_unit_backup:-}" ] && [ -f "$agent_unit_backup" ]; then
        cp "$agent_unit_backup" "$systemd_dir/zeno-agent.service"
      fi
    fi
    systemctl daemon-reload
    systemctl restart zeno-controller.service || true
    if wait_health "$(health_url)" 30; then
      if [ "$manage_agent" -eq 1 ] && [ "$agent_was_active" -eq 1 ]; then
        systemctl start zeno-agent.service || true
      fi
      echo "rolled back to $previous" >&2
    else
      echo "rollback controller health check failed; inspect zeno-controller.service" >&2
    fi
  fi
  exit 1
}

if [ "$manage_agent" -eq 1 ]; then
  systemctl stop zeno-agent.service || true
fi
ln -sfn "$release_dir" "$install_dir/current"
systemctl daemon-reload
systemctl restart zeno-controller.service || rollback "controller restart failed"
if ! wait_health "$(health_url)" "$health_timeout"; then
  journalctl -u zeno-controller.service -n 80 --no-pager >&2 || true
  rollback "controller health check failed"
fi
if [ "$manage_agent" -eq 1 ]; then
  systemctl enable zeno-agent.service >/dev/null 2>&1 || true
  systemctl start zeno-agent.service || rollback "agent start failed"
fi
systemctl enable zeno-controller.service >/dev/null 2>&1 || true
# systemctl enable may update wants/ symlinks after the earlier reload; reload once more
# so post-deploy smoke checks do not see stale unit-file warnings.
systemctl daemon-reload
systemctl is-active --quiet zeno-controller.service || rollback "controller inactive after deploy"
if [ "$manage_agent" -eq 1 ]; then
  systemctl is-active --quiet zeno-agent.service || rollback "agent inactive after deploy"
fi
printf 'deploy ok: revision=%s release=%s\n' "$version" "$release_dir"
