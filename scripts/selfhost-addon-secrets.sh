#!/usr/bin/env bash
# Fills in the secrets an add-on of the self-hosted stack needs and .env still
# leaves empty, so turning an add-on on is one line (MULTICA_ADDONS=gitea).
# Values that are already set are never changed.
#   selfhost-addon-secrets.sh "<comma-separated add-ons>" [env file]
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
addons=",$(printf '%s' "${1:-}" | tr -d '[:space:]'),"
env_file="${2:-$ROOT_DIR/.env}"

[ -f "$env_file" ] || exit 0

current_value() {
  awk -v key="$1" 'index($0, key "=") == 1 { print substr($0, length(key) + 2); exit }' "$env_file"
}

set_value() {
  local key=$1 value=$2 tmp
  tmp="$(mktemp "$env_file.XXXXXX")"
  awk -v key="$key" -v value="$value" '
    index($0, key "=") == 1 { if (!done) print key "=" value; done = 1; next }
    { print }
    END { if (!done) print key "=" value }
  ' "$env_file" >"$tmp"
  # Keep the file's mode: mktemp creates 0600 and .env may be group-readable.
  cat "$tmp" >"$env_file"
  rm -f "$tmp"
}

fill() {
  local key=$1 bytes=$2
  if [ -z "$(current_value "$key")" ]; then
    set_value "$key" "$(openssl rand -hex "$bytes")"
    echo "==> Generated $key in $env_file"
  fi
}

case "$addons" in
  *,gitea,*)
    fill GITEA_DB_PASSWORD 24
    fill GITEA_ADMIN_PASSWORD 12
    ;;
esac
