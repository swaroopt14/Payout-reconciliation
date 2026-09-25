#!/bin/sh
# One-shot: creates the six harness databases (idempotent) and applies
# backend/relay/db/init.sql to zord_relay_db (relay does not migrate on
# boot; init.sql is CREATE ... IF NOT EXISTS and stays compatible with the
# relay goose migrations being added).
set -eu
export PGHOST="${PGHOST:-postgres}" PGUSER="${POSTGRES_USER}" PGPASSWORD="${POSTGRES_PASSWORD}"

until pg_isready -q -d postgres; do sleep 1; done

for db in zord_edge_db zord_intent_engine_db zord_relay_db zord_outcome_db zord_router zord_token_enclave_db; do
  if [ "$(psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$db'")" = "1" ]; then
    echo "pg-init: $db exists"
  else
    psql -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$db\""
    echo "pg-init: created $db"
  fi
done

psql -d zord_relay_db -v ON_ERROR_STOP=1 -q -f /init/relay-init.sql
echo "pg-init: applied relay init.sql to zord_relay_db"
psql -d zord_relay_db -tAc "SELECT string_agg(tablename, ',' ORDER BY tablename) FROM pg_tables WHERE schemaname='public'"
