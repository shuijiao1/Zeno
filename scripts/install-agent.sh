#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: install-agent.sh --controller-url <url> --node-id <id> (--token <token>|--token-file <path>) [options]

Installs or updates only zeno-agent.service on the current Linux host.
Use this on non-controller servers after creating/rotating the node token in Admin.

Options:
  --agent-binary <path>       Default: ../zeno-agent relative to this script
  --install-dir <dir>         Default: /opt/zeno
  --data-dir <dir>            Default: <install-dir>/data
  --systemd-dir <dir>         Default: /etc/systemd/system
  --run-user <user>           Default: root
  --controller-url <url>      Required
  --node-id <id>              Required
  --token <token>             Write token to <data-dir>/agent-token
  --token-file <path>         Existing/written token file path
  --interval <duration>       Default: 2s
  --version <version>         Default: REVISION next to agent binary, or unknown
  --dry-run                   Copy/render but do not call systemctl
  -h, --help                  Show help
USAGE
}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
agent_binary="$script_dir/../zeno-agent"
install_dir="/opt/zeno"
data_dir=""
systemd_dir="/etc/systemd/system"
run_user="root"
controller_url=""
node_id=""
token=""
token_file=""
interval="2s"
version=""
dry_run=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --agent-binary) agent_binary="${2:-}"; shift 2 ;;
    --install-dir) install_dir="${2:-}"; shift 2 ;;
    --data-dir) data_dir="${2:-}"; shift 2 ;;
    --systemd-dir) systemd_dir="${2:-}"; shift 2 ;;
    --run-user) run_user="${2:-}"; shift 2 ;;
    --controller-url) controller_url="${2:-}"; shift 2 ;;
    --node-id) node_id="${2:-}"; shift 2 ;;
    --token) token="${2:-}"; shift 2 ;;
    --token-file) token_file="${2:-}"; shift 2 ;;
    --interval) interval="${2:-}"; shift 2 ;;
    --version) version="${2:-}"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$controller_url" ] || [ -z "$node_id" ]; then
  echo "--controller-url and --node-id are required" >&2
  exit 2
fi
if [ ! -x "$agent_binary" ]; then
  echo "agent binary not found or not executable: $agent_binary" >&2
  exit 2
fi
if [ -z "$data_dir" ]; then data_dir="$install_dir/data"; fi
if [ -z "$token_file" ]; then token_file="$data_dir/agent-token"; fi
if [ -z "$token" ] && [ ! -s "$token_file" ]; then
  echo "provide --token or an existing --token-file" >&2
  exit 2
fi
if [ -z "$version" ]; then
  if [ -f "$(dirname "$agent_binary")/REVISION" ]; then
    version=$(tr -d '[:space:]' < "$(dirname "$agent_binary")/REVISION")
  else
    version="unknown"
  fi
fi

sed_escape() { printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'; }
mkdir -p "$install_dir/agent" "$data_dir"
install -m 0755 "$agent_binary" "$install_dir/agent/zeno-agent"
if [ -n "$token" ]; then
  umask 077
  printf '%s\n' "$token" > "$token_file"
fi
chmod 600 "$token_file"
unit_output_dir="$systemd_dir"
if [ "$dry_run" -eq 1 ]; then unit_output_dir="$install_dir/systemd-dry-run"; fi
mkdir -p "$unit_output_dir"

# The standalone agent uses a generated unit instead of the release current/ symlink.
sed \
  -e "s/{{RUN_USER}}/$(sed_escape "$run_user")/g" \
  -e "s/{{INSTALL_DIR}}/$(sed_escape "$install_dir")/g" \
  -e "s/{{AGENT_BINARY}}/$(sed_escape "$install_dir/agent/zeno-agent")/g" \
  -e "s/{{AGENT_WORKING_DIR}}/$(sed_escape "$install_dir/agent")/g" \
  -e "s/{{AGENT_AFTER_CONTROLLER}}//g" \
  -e "s/{{AGENT_REQUIRES_CONTROLLER}}//g" \
  -e "s/{{CONTROLLER_URL}}/$(sed_escape "$controller_url")/g" \
  -e "s/{{NODE_ID}}/$(sed_escape "$node_id")/g" \
  -e "s/{{AGENT_INTERVAL}}/$(sed_escape "$interval")/g" \
  -e "s/{{AGENT_TOKEN_FILE}}/$(sed_escape "$token_file")/g" \
  -e "s/{{VERSION}}/$(sed_escape "$version")/g" \
  -e '/{{/d' \
  "$script_dir/../packaging/systemd/zeno-agent.service" > "$unit_output_dir/zeno-agent.service"

if [ "$dry_run" -eq 1 ]; then
  printf 'dry-run ok: agent=%s unit=%s/zeno-agent.service\n' "$install_dir/agent/zeno-agent" "$unit_output_dir"
  exit 0
fi
systemctl daemon-reload
systemctl enable zeno-agent.service >/dev/null 2>&1 || true
systemctl restart zeno-agent.service
systemctl is-active --quiet zeno-agent.service
printf 'agent install ok: node=%s version=%s\n' "$node_id" "$version"
