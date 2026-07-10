#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/package-release.sh [--sha <revision>] [--out-dir <dir>] [--skip-tests] [--allow-dirty]

Builds a Linux amd64 Zeno controller release archive containing:
  zeno-controller, web/, REVISION, README.md, docs/, scripts/, packaging/systemd/

The standalone zeno-agent is released from the separate Zeno-Agent repository.
Default output: build/releases/zeno-<sha>-linux-amd64.tar.gz
USAGE
}

sha=""
out_dir="build/releases"
skip_tests=0
allow_dirty=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --sha)
      sha="${2:-}"
      shift 2
      ;;
    --out-dir)
      out_dir="${2:-}"
      shift 2
      ;;
    --skip-tests)
      skip_tests=1
      shift
      ;;
    --allow-dirty)
      allow_dirty=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

dirty_state=$(git status --porcelain=v1)
if [ -n "$dirty_state" ] && [ "$allow_dirty" -eq 0 ]; then
  echo "working tree has uncommitted changes; commit first or pass --allow-dirty" >&2
  exit 1
fi
if [ -z "$sha" ]; then
  sha=$(git rev-parse --short HEAD)
  if [ -n "$dirty_state" ]; then
    sha="${sha}-dirty"
  fi
fi
if [ -z "$sha" ]; then
  echo "empty revision" >&2
  exit 1
fi

if [ "$skip_tests" -eq 0 ]; then
  go test ./... >&2
  npm --prefix web test -- --run >&2
fi
npm --prefix web run build >&2

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/zeno-package.XXXXXX")
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

release_name="zeno-${sha}-linux-amd64"
release_dir="$work_dir/$release_name"
mkdir -p "$release_dir/web" "$release_dir/docs" "$release_dir/scripts" "$release_dir/packaging/systemd"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$release_dir/zeno-controller" ./cmd/controller
cp -a web/dist/. "$release_dir/web/"
printf '%s\n' "$sha" > "$release_dir/REVISION"
cp README.md "$release_dir/"
cp docs/*.md "$release_dir/docs/"
cp scripts/deploy-local-release.sh scripts/import-guko-servers.py "$release_dir/scripts/"
cp packaging/systemd/zeno-controller.service "$release_dir/packaging/systemd/"
chmod +x "$release_dir/zeno-controller" "$release_dir/scripts/"*.sh

mkdir -p "$out_dir"
archive="$out_dir/$release_name.tar.gz"
tar -C "$work_dir" -czf "$archive" "$release_name"

printf '%s\n' "$archive"
