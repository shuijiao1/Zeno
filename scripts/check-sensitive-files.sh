#!/usr/bin/env bash
set -euo pipefail

failed=0
while IFS= read -r path; do
  case "/$path/" in
    */.env/*|*/data/*|*/secrets/*|*.db/*|*.db-wal/*|*.db-shm/*|*.sqlite/*|*.sqlite3/*|*.pem/*|*.key/*|*.p12/*|*.pfx/*|*/id_rsa/*|*/id_ed25519/*)
      echo "refusing tracked sensitive path: $path" >&2
      failed=1
      ;;
  esac
done < <(git ls-files)

if git grep -I -n -E -- '-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----' -- . ':!scripts/check-sensitive-files.sh' >/dev/null; then
  echo "refusing tracked private-key material" >&2
  failed=1
fi

exit "$failed"
