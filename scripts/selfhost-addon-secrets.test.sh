#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$ROOT_DIR/scripts/selfhost-addon-secrets.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

value() {
  awk -v key="$2" 'index($0, key "=") == 1 { print substr($0, length(key) + 2); exit }' "$1"
}

# Without the add-on nothing is written.
env_file="$work/none.env"
printf 'JWT_SECRET=x\nGITEA_DB_PASSWORD=\n' >"$env_file"
bash "$script" "ci,proxy" "$env_file" >/dev/null
[ -z "$(value "$env_file" GITEA_DB_PASSWORD)" ] || fail "a secret was generated without the gitea add-on"

# With it, empty and missing values are filled in and the rest is kept.
env_file="$work/gitea.env"
printf 'JWT_SECRET=x\nGITEA_DB_PASSWORD=\nOTHER=1\n' >"$env_file"
bash "$script" "gitea, ci" "$env_file" >/dev/null
db="$(value "$env_file" GITEA_DB_PASSWORD)"
admin="$(value "$env_file" GITEA_ADMIN_PASSWORD)"
[ "${#db}" -eq 48 ] || fail "GITEA_DB_PASSWORD was not generated: '$db'"
[ "${#admin}" -eq 24 ] || fail "GITEA_ADMIN_PASSWORD was not appended: '$admin'"
[ "$(value "$env_file" JWT_SECRET)" = x ] && [ "$(value "$env_file" OTHER)" = 1 ] ||
  fail "other settings were changed"
[ "$(grep -c '^GITEA_DB_PASSWORD=' "$env_file")" -eq 1 ] || fail "GITEA_DB_PASSWORD is duplicated"

# A second run changes nothing: set values are never replaced.
bash "$script" "gitea" "$env_file" >/dev/null
[ "$(value "$env_file" GITEA_DB_PASSWORD)" = "$db" ] || fail "an existing secret was replaced"
[ "$(value "$env_file" GITEA_ADMIN_PASSWORD)" = "$admin" ] || fail "an existing secret was replaced"

# No env file is not an error (the stack's own check reports it).
bash "$script" "gitea" "$work/missing.env"

echo "selfhost-addon-secrets: ok"
