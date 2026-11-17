#!/bin/sh
# Makes sure /etc/nginx/certs holds fullchain.pem and privkey.pem, then starts
# nginx through the image's own entrypoint (which renders the templates).
#
# PROXY_TLS:
#   selfsigned  (default) a private CA (ca.pem, made once) signs a certificate
#               for PROXY_HOST. Clients trust ca.pem once; a new PROXY_HOST
#               only re-issues the server certificate.
#   provided    the files are yours, e.g. from the company's internal CA; this
#               only checks that they exist.
set -eu

certs=/etc/nginx/certs
host="$PROXY_HOST"
mode="${PROXY_TLS:-selfsigned}"

case "$host" in
  *:*) san="IP:$host" ;;          # IPv6 address
  *[!0-9.]*) san="DNS:$host" ;;   # host name
  *) san="IP:$host" ;;            # IPv4 address
esac

issue_selfsigned() {
  if [ ! -s "$certs/ca.pem" ] || [ ! -s "$certs/ca-key.pem" ]; then
    openssl req -x509 -newkey rsa:3072 -nodes -days 3650 \
      -subj "/CN=Multica self-hosted CA" \
      -addext "basicConstraints=critical,CA:TRUE" \
      -addext "keyUsage=critical,keyCertSign,cRLSign" \
      -keyout "$certs/ca-key.pem" -out "$certs/ca.pem" 2>/dev/null
    echo "proxy: created private CA $certs/ca.pem"
  fi
  work=$(mktemp -d)
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$host" \
    -keyout "$work/privkey.pem" -out "$work/server.csr" 2>/dev/null
  printf 'subjectAltName=%s\nbasicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n' \
    "$san" > "$work/server.ext"
  # 825 days: the longest validity Apple platforms accept for a server certificate.
  openssl x509 -req -in "$work/server.csr" -CA "$certs/ca.pem" -CAkey "$certs/ca-key.pem" \
    -CAcreateserial -days 825 -extfile "$work/server.ext" -out "$work/server.pem" 2>/dev/null
  cat "$work/server.pem" "$certs/ca.pem" > "$work/fullchain.pem"
  mv "$work/privkey.pem" "$certs/privkey.pem"
  mv "$work/fullchain.pem" "$certs/fullchain.pem"
  printf '%s' "$host" > "$certs/selfsigned.host"
  rm -rf "$work"
  echo "proxy: issued a certificate for $san"
}

case "$mode" in
  selfsigned)
    if [ ! -s "$certs/fullchain.pem" ] || [ "$(cat "$certs/selfsigned.host" 2>/dev/null)" != "$host" ]; then
      issue_selfsigned
    fi
    ;;
  provided)
    for f in fullchain.pem privkey.pem; do
      [ -s "$certs/$f" ] || { echo "proxy: $certs/$f is missing (PROXY_TLS=provided)" >&2; exit 1; }
    done
    ;;
  *)
    echo "proxy: unknown PROXY_TLS=$mode (selfsigned or provided)" >&2
    exit 1
    ;;
esac

exec /docker-entrypoint.sh nginx -g 'daemon off;'
