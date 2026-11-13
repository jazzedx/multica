#!/usr/bin/env bash
# Installs the CLI from a self-hosted Multica server: the one built alongside
# that server.
#
# Usage:
#   curl -fsSL <server>/api/cli/install.sh | bash -s -- <server>
#
# Environment:
#   MULTICA_SERVER_URL   server URL, used when no argument is given
#   MULTICA_INSTALL_DIR  install directory, ~/.local/bin by default
set -euo pipefail

fail() {
  printf 'multica install: %s\n' "$*" >&2
  exit 1
}

server_url="${1:-${MULTICA_SERVER_URL:-}}"
server_url="${server_url%/}"
[ -n "$server_url" ] || fail "missing server URL. Usage: curl -fsSL <server>/api/cli/install.sh | bash -s -- <server>"

case "$(uname -s)" in
  Darwin) goos=darwin ;;
  Linux) goos=linux ;;
  *) fail "unsupported system $(uname -s). On Windows, use install.ps1 from the same server." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) goarch=amd64 ;;
  arm64 | aarch64) goarch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
if command -v sha256sum >/dev/null 2>&1; then
  sha256_of() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_of() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required"
fi

install_dir="${MULTICA_INSTALL_DIR:-$HOME/.local/bin}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

url="$server_url/api/cli/download/$goos/$goarch"
printf 'Downloading multica (%s/%s) from %s\n' "$goos" "$goarch" "$server_url"
curl -fsSL -D "$tmp/headers" -o "$tmp/multica.tar.gz" "$url" || fail "download failed: $url"

# Header case and line endings vary with the proxy in front of the server.
# After a redirect, the last response wins.
header() {
  tr -d '\r' <"$tmp/headers" | awk -F': *' -v name="$1" 'tolower($1) == name { value = $2 } END { print value }'
}
expected="$(header x-checksum-sha256)"
version="$(header x-multica-version)"
[ -n "$expected" ] || fail "the server did not send a checksum for $url"
actual="$(sha256_of "$tmp/multica.tar.gz")"
[ "$actual" = "$expected" ] || fail "checksum mismatch: expected $expected, got $actual"

tar -xzf "$tmp/multica.tar.gz" -C "$tmp" multica

mkdir -p "$install_dir"
target="$install_dir/multica"
# Write beside the target and rename, so a running multica never reads a
# half-written file.
staged="$install_dir/.multica.install.$$"
cp "$tmp/multica" "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$target"

printf 'Installed multica %s to %s\n' "${version:-(unknown version)}" "$target"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    printf '\n%s is not on your PATH. Add it for this shell with:\n  export PATH="%s:$PATH"\n' "$install_dir" "$install_dir"
    ;;
esac

resolved="$(command -v multica 2>/dev/null || true)"
if [ -n "$resolved" ] && [ "$resolved" != "$target" ]; then
  printf '\nAnother multica comes first on your PATH: %s\nRemove it or put %s ahead of it, otherwise that copy keeps running.\n' "$resolved" "$install_dir"
fi
