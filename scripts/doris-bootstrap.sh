#!/usr/bin/env sh
# Doris bootstrap: wait for FE → register BE if missing → apply schema.
# Idempotent: safe to re-run after the cluster is healthy.
#
# The very first run uses empty root password (Doris ships unauthenticated);
# subsequent runs use $DORIS_PASSWORD set by 01-databases.sql.
set -eu

FE_HOST="${FE_HOST:-doris-fe}"
FE_QUERY_PORT="${FE_QUERY_PORT:-9030}"
FE_HTTP_PORT="${FE_HTTP_PORT:-8030}"
BE_HOST="${BE_HOST:-doris-be}"
BE_HEARTBEAT_PORT="${BE_HEARTBEAT_PORT:-9050}"
DORIS_USER="${DORIS_USER:-root}"
DORIS_PASSWORD="${DORIS_PASSWORD:-}"
INIT_DIR="${INIT_DIR:-/sql}"

ACTIVE_PASSWORD=""

mysql_try() {
  local pw="$1"; shift
  if [ -z "$pw" ]; then
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" -N "$@"
  else
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" -p"$pw" -N "$@"
  fi
}

mysql_exec() {
  if [ -z "$ACTIVE_PASSWORD" ]; then
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" -N -e "$1"
  else
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" -p"$ACTIVE_PASSWORD" -N -e "$1"
  fi
}

apply_sql_file() {
  local f="$1"
  if [ -z "$ACTIVE_PASSWORD" ]; then
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" < "$f"
  else
    mysql -h "$FE_HOST" -P "$FE_QUERY_PORT" -u "$DORIS_USER" -p"$ACTIVE_PASSWORD" < "$f"
  fi
}

echo "[bootstrap] waiting for FE http on $FE_HOST:$FE_HTTP_PORT ..."
i=0
until curl -sf "http://$FE_HOST:$FE_HTTP_PORT/api/health" >/dev/null 2>&1 \
   || curl -sf "http://$FE_HOST:$FE_HTTP_PORT/" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge 120 ]; then
    echo "[bootstrap] FE not reachable after 4m" >&2; exit 1
  fi
  sleep 2
done
echo "[bootstrap] FE is up"

echo "[bootstrap] picking working credentials ..."
i=0
while :; do
  if mysql_try "" -e "SELECT 1" >/dev/null 2>&1; then
    ACTIVE_PASSWORD=""
    echo "[bootstrap] using empty password (fresh cluster)"
    break
  fi
  if [ -n "$DORIS_PASSWORD" ] && mysql_try "$DORIS_PASSWORD" -e "SELECT 1" >/dev/null 2>&1; then
    ACTIVE_PASSWORD="$DORIS_PASSWORD"
    echo "[bootstrap] using configured password (already initialized)"
    break
  fi
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then
    echo "[bootstrap] FE MySQL never accepted credentials" >&2; exit 1
  fi
  sleep 2
done

# BE auto-registration check
echo "[bootstrap] checking backend registration ..."
count_be() { mysql_exec "SHOW BACKENDS" 2>/dev/null | grep -c . || true; }
i=0
while [ "$(count_be)" -lt 1 ]; do
  i=$((i + 1))
  if [ "$i" -ge 30 ]; then
    echo "[bootstrap] no BE auto-registered; registering $BE_HOST:$BE_HEARTBEAT_PORT manually"
    mysql_exec "ALTER SYSTEM ADD BACKEND \"$BE_HOST:$BE_HEARTBEAT_PORT\"" \
      || echo "[bootstrap] BE already registered (race) — continuing"
    break
  fi
  sleep 2
done
echo "[bootstrap] $(count_be) backend(s) registered"

echo "[bootstrap] waiting for BE to be Alive ..."
i=0
until mysql_exec "SHOW BACKENDS" | grep -E "[[:space:]]true[[:space:]]" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then
    echo "[bootstrap] BE never reported Alive" >&2
    mysql_exec "SHOW BACKENDS" >&2 || true
    exit 1
  fi
  sleep 2
done
echo "[bootstrap] BE is alive"

# Apply schema files in lexicographic order. After 01-databases.sql sets the
# password, switch to the configured password for subsequent files.
for f in $(ls "$INIT_DIR"/*.sql 2>/dev/null | sort); do
  echo "[bootstrap] applying $f"
  apply_sql_file "$f"
  if [ -z "$ACTIVE_PASSWORD" ] && [ -n "$DORIS_PASSWORD" ]; then
    if mysql_try "$DORIS_PASSWORD" -e "SELECT 1" >/dev/null 2>&1; then
      ACTIVE_PASSWORD="$DORIS_PASSWORD"
      echo "[bootstrap] root password is now set; using it for remaining files"
    fi
  fi
done

echo "[bootstrap] done"
