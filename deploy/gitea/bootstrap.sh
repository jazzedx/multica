#!/bin/sh
# One-shot setup once Gitea is healthy. Runs on every `up` and only creates
# what is missing:
#   1. the first administrator (the web installer is locked, and with open
#      registration nobody else should get to the empty instance first);
#   2. when GITEA_RUNNER_ORG is set, that organization, so the runner can be
#      scoped to it;
#   3. a runner registration token in /runner-token/token for gitea-runner;
#   4. a service account for the Multica server and its token in
#      /multica-token/token. The server uses it to give each workspace an
#      organization, a connection and a webhook, so it has to be an admin.
set -eu

api="http://gitea:3000/api/v1"
auth="$GITEA_ADMIN_USER:$GITEA_ADMIN_PASSWORD"

gitea_cli() {
  su-exec git gitea --config /data/gitea/conf/app.ini "$@"
}

if gitea_cli admin user list | awk 'NR > 1 { print $2 }' | grep -qxF "$GITEA_ADMIN_USER"; then
  echo "bootstrap: user $GITEA_ADMIN_USER already exists"
else
  gitea_cli admin user create --admin \
    --username "$GITEA_ADMIN_USER" \
    --email "$GITEA_ADMIN_EMAIL" \
    --password "$GITEA_ADMIN_PASSWORD" \
    --must-change-password=false
fi

service_user="${GITEA_MULTICA_USER:-multica-service}"
service_token_file=/multica-token/token
service_token_ok() {
  [ -s "$service_token_file" ] &&
    curl -fsS -o /dev/null -H "Authorization: token $(cat "$service_token_file")" "$api/user"
}
if service_token_ok; then
  echo "bootstrap: service token for $service_user is valid"
else
  if ! gitea_cli admin user list | awk 'NR > 1 { print $2 }' | grep -qxF "$service_user"; then
    gitea_cli admin user create --admin \
      --username "$service_user" \
      --email "$service_user@multica.local" \
      --random-password --must-change-password=false >/dev/null
    echo "bootstrap: created service account $service_user"
  fi
  # Token names are unique per user and a lost token cannot be read back, so
  # a replacement gets a new name.
  token=$(gitea_cli admin user generate-access-token --username "$service_user" \
    --token-name "multica-server-$(date +%s)" --scopes all --raw)
  (umask 077 && printf '%s' "$token" > "$service_token_file")
  echo "bootstrap: service token for $service_user written"
fi

token_path="$api/admin/actions/runners/registration-token"
if [ -n "${GITEA_RUNNER_ORG:-}" ]; then
  if ! curl -fsS -o /dev/null -u "$auth" "$api/orgs/$GITEA_RUNNER_ORG"; then
    curl -fsS -o /dev/null -u "$auth" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$GITEA_RUNNER_ORG\",\"visibility\":\"limited\"}" "$api/orgs"
    echo "bootstrap: created organization $GITEA_RUNNER_ORG"
  fi
  token_path="$api/orgs/$GITEA_RUNNER_ORG/actions/runners/registration-token"
fi

token=$(curl -fsS -X POST -u "$auth" "$token_path" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$token" ]; then
  echo "bootstrap: no runner registration token in the response from $token_path" >&2
  exit 1
fi
umask 077
printf '%s' "$token" > /runner-token/token
echo "bootstrap: runner registration token written (scope: ${GITEA_RUNNER_ORG:-instance})"
