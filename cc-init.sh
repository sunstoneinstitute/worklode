#!/usr/bin/env bash
# Build cmd/lode and install it to /usr/local/bin, bootstrap a local Postgres
# + pgvector so `go test ./internal/store` works out of the box, then log in
# to the Sunstone worklode instance. Idempotent: safe to re-run.
set -euo pipefail
cd "$(dirname "$0")"

go build -o /usr/local/bin/lode ./cmd/lode

# --- Postgres + pgvector, matching internal/store's default TEST_POSTGRES_DSN ---
if ! command -v pg_lsclusters >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y postgresql
fi

pg_version="$(pg_lsclusters --no-header | awk '{print $1; exit}')"
if ! dpkg -s "postgresql-${pg_version}-pgvector" >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y "postgresql-${pg_version}-pgvector"
fi

if [ "$(pg_lsclusters --no-header | awk '{print $4; exit}')" != "online" ]; then
  service postgresql start
fi

# Matches the docker-compose postgres service password store/testhelpers.go
# defaults to (local dev/test only).
sudo -u postgres psql -c "ALTER USER postgres WITH PASSWORD 'postgres';" >/dev/null
psql "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" \
  -c "CREATE EXTENSION IF NOT EXISTS vector;" >/dev/null

# --- lode Claude Code plugin, sourced from this checkout (not GitHub) ---
claude plugin marketplace add "$(pwd)" --scope local
claude plugin install lode@worklode --scope local -y

lode login --server https://worklode.dev.sunstoneinstitute.ai
lode install
