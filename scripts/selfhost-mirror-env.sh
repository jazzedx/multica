#!/usr/bin/env bash
# Creates a complete .env when none exists: the same random secrets as
# selfhost-build, plus the settings of an internal private deployment. An
# existing .env is left untouched.
# It has to exist before the make process that runs compose starts: make reads
# .env only at startup,
# and would otherwise pass the Makefile's defaults (POSTGRES_PASSWORD=multica,
# for one) to compose.
# The verification code can be replaced when invoking make:
#   make selfhost-mirror-env MULTICA_DEV_VERIFICATION_CODE=123456
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${1:-$ROOT_DIR/.env}"

if [ -f "$env_file" ]; then
  echo "==> $env_file already exists; leaving it unchanged"
  exit 0
fi

code="${MULTICA_DEV_VERIFICATION_CODE:-886644}"
if ! [[ "$code" =~ ^[0-9]{6}$ ]]; then
  echo "MULTICA_DEV_VERIFICATION_CODE must be six digits, got: $code" >&2
  exit 1
fi

tmp="$(mktemp "$env_file.XXXXXX")"
trap 'rm -f "$tmp" "$tmp.next"' EXIT
cp "$ROOT_DIR/.env.example" "$tmp"

set_env_value() {
  local key=$1 value=$2
  awk -v key="$key" -v value="$value" '
    index($0, key "=") == 1 { if (!done) print key "=" value; done = 1; next }
    { print }
    END { if (!done) print key "=" value }
  ' "$tmp" >"$tmp.next"
  mv "$tmp.next" "$tmp"
}

jwt="$(openssl rand -hex 32)"
pgpass="$(openssl rand -hex 24)"
vcskey="$(openssl rand -base64 32)"

set_env_value JWT_SECRET "$jwt"
set_env_value POSTGRES_PASSWORD "$pgpass"
sed -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$pgpass\2#" "$tmp" >"$tmp.next"
mv "$tmp.next" "$tmp"
set_env_value MULTICA_VCS_SECRET_KEY "$vcskey"

# The backend accepts a fixed verification code only when APP_ENV is not production.
set_env_value APP_ENV development
set_env_value MULTICA_DEV_VERIFICATION_CODE "$code"
set_env_value DO_NOT_TRACK 1

mv "$tmp" "$env_file"
echo "==> Created $env_file: random JWT_SECRET, POSTGRES_PASSWORD and MULTICA_VCS_SECRET_KEY, APP_ENV=development, MULTICA_DEV_VERIFICATION_CODE=$code, DO_NOT_TRACK=1"
