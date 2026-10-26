#!/usr/bin/env bash
# Preflight: verify config, tools, VeloDB/Doris connectivity (MySQL + stream-load ports), the demo table and s3.yaml.
#   ./check_env.sh                                  # uses .env + environment
#   VELODB_DATABASE=hs_test ./check_env.sh          # check another database/table without editing .env
# Read-only: never creates, loads or deletes anything. Exit code = number of failed checks.
set -uo pipefail
cd "$(dirname "$0")"
set -e; . ./env.sh; set +e

fails=0
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; }
warn() { printf '  \033[33mwarn\033[0m  %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; fails=$((fails+1)); }

echo "config ($( [ -f "$ENV_FILE" ] && echo "$ENV_FILE + environment" || echo "environment only, no $ENV_FILE"))"
echo "  VELODB_HOST=$VELODB_HOST  VELODB_MYSQL_PORT=$VELODB_MYSQL_PORT  VELODB_STREAM_LOAD_URL=$VELODB_STREAM_LOAD_URL"
echo "  VELODB_USER=$VELODB_USER  VELODB_PASSWORD=$( [ -n "$VELODB_PASSWORD" ] && echo '(set)' || echo '(empty)')"
echo "  table ${DATABASE}.${TABLE}  buckets=$VELODB_BUCKETS  replication=$VELODB_REPLICATION_NUM  keep=${VELODB_PARTITION_KEEP_DAYS}d ahead=${VELODB_PARTITION_AHEAD_DAYS}d"
case "$VELODB_HOST$VELODB_PASSWORD" in *example.com*|*change-me*) bad "placeholder values from env.example are still in use";; esac

echo "tools"
for t in python3 curl go cargo; do
  if command -v "$t" >/dev/null; then ok "$t  $("$t" --version 2>/dev/null | head -1 || "$t" version | head -1)"
  else case "$t" in go) warn "go not found (needed for run-web-server.sh)";;
                    cargo) warn "cargo not found (needed only to generate data: run-gen.sh)";;
                    *) bad "$t not found";; esac; fi
done
command -v rs3 >/dev/null && ok "rs3 (upload.sh)" || warn "rs3 not found (needed only for upload.sh)"

echo "VeloDB MySQL protocol ${VELODB_HOST}:${VELODB_MYSQL_PORT}"
PY=$(./ensure-venv.sh) || { bad "python venv setup failed"; exit 1; }
sql() {  # value(s) without the header line; on error the error message and rc 1
  local out; out=$("$PY" doris_sql.py --env "$ENV_FILE" -D information_schema -q -e "$1" 2>/dev/null) \
    || { "$PY" doris_sql.py --env "$ENV_FILE" -D information_schema -q -e "$1" 2>&1 >/dev/null | tail -1; return 1; }
  echo "$out" | tail -n +2
}
if out=$(sql "SELECT concat(version(), ' / ', current_user())"); [ $? = 0 ]; then ok "connected: $out"
else bad "cannot connect: $out"; fi

echo "VeloDB stream load ${VELODB_STREAM_LOAD_URL}"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${VELODB_STREAM_LOAD_URL%/}/api/health")
[ "$code" = 200 ] && ok "/api/health -> 200" || bad "/api/health -> ${code} (check VELODB_STREAM_LOAD_PORT / VELODB_STREAM_LOAD_URL)"

echo "demo table ${DATABASE}.${TABLE}"
db=$(sql "SELECT count(*) FROM information_schema.schemata WHERE schema_name='${DATABASE}'")
tb=$(sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='${DATABASE}' AND table_name='${TABLE}'")
if [ "$tb" = 1 ]; then
  ok "exists: $(sql "SELECT concat(count(*), ' rows, ts ', ifnull(min(ts),'-'), ' .. ', ifnull(max(ts),'-')) FROM ${DATABASE}.${TABLE}")"
  ok "$(sql "SELECT count(*) FROM information_schema.partitions WHERE table_schema='${DATABASE}' AND table_name='${TABLE}'") partitions"
elif [ "$db" = 1 ]; then warn "database exists, table missing: run ./create_table_doris.sh"
else warn "database ${DATABASE} missing: run ./create_table_doris.sh"; fi
[ -s "$MARKER" ] && ok "base marker $MARKER: BASE $(cat "$MARKER") (UTC)" || echo "  -     no base marker $MARKER (next load starts a new BASE)"

echo "S3 (Broker Load of the 1B-row history)"
if [ -f s3.yaml ]; then
  for k in Endpoint Region access_key secret_key Bucket Prefix; do
    grep -qiE "^$k:[[:space:]]*[^[:space:]<]" s3.yaml && ok "s3.yaml $k set" || bad "s3.yaml $k missing"
  done
else warn "no s3.yaml (only needed for the S3 Broker Load; ./load_into_doris.sh --local DIR works without it)"; fi

echo; [ "$fails" = 0 ] && echo "all checks passed" || echo "$fails check(s) failed"
exit "$fails"
