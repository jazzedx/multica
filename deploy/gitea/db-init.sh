#!/bin/sh
# Creates Gitea's role and database inside Multica's PostgreSQL. Runs on every
# `up`: it only creates what is missing and keeps the role's password in step
# with GITEA_DB_PASSWORD. Gitea gets its own database so its tables never mix
# with Multica's schema or migrations.
set -eu

psql -v ON_ERROR_STOP=1 \
  -v name="$GITEA_DB_NAME" -v owner="$GITEA_DB_USER" -v pass="$GITEA_DB_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN', :'owner')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'owner') \gexec
ALTER ROLE :"owner" WITH LOGIN PASSWORD :'pass';
SELECT format('CREATE DATABASE %I OWNER %I', :'name', :'owner')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'name') \gexec
SQL
