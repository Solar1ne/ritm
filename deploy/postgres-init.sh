#!/bin/sh
set -eu
# Runs only on an empty PostgreSQL volume. psql quotes the password as a literal.
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 --set=app_password="$RHYTHM_APP_PASSWORD" <<'SQL'
CREATE ROLE rhythm_app LOGIN PASSWORD :'app_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
ALTER DATABASE rhythm OWNER TO rhythm_app;
GRANT USAGE, CREATE ON SCHEMA public TO rhythm_app;
SQL
