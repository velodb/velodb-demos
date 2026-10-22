#!/usr/bin/env bash
# Preflight: verify config, tools, VeloDB/Doris connectivity (MySQL + stream-load ports), the demo tables
# and the local pipeline state (models, faces.db, export/).
#   ./check_env.sh                                    # uses .env + environment
#   VELODB_DATABASE=face_test ./check_env.sh          # check another database without editing .env
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
echo "  database ${DATABASE}  faces buckets=$VELODB_FACES_BUCKETS  people buckets=$VELODB_PEOPLE_BUCKETS  replication=$VELODB_REPLICATION_NUM"
case "$VELODB_HOST$VELODB_PASSWORD" in *example.com*|*change-me*) bad "placeholder values from env.example are still in use";; esac

echo "tools"
for t in python3 curl jq unzip; do
  if command -v "$t" >/dev/null; then ok "$t  $( [ "$t" = unzip ] && unzip -v | head -1 || "$t" --version 2>&1 | head -1)"
  else case "$t" in jq) bad "jq not found (needed by run-load.sh: brew install jq)";;
                    *) bad "$t not found";; esac; fi
done

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

echo "demo tables ${DATABASE}.faces / ${DATABASE}.people"
db=$(sql "SELECT count(*) FROM information_schema.schemata WHERE schema_name='${DATABASE}'")
for t in faces people; do
  tb=$(sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='${DATABASE}' AND table_name='${t}'")
  if [ "$tb" = 1 ]; then ok "${t}: $(sql "SELECT count(*) FROM ${DATABASE}.${t}") rows"
  elif [ "$db" = 1 ]; then warn "${t} missing: run ./run-load.sh (or ./create_table_doris.sh)"
  else warn "database ${DATABASE} missing: run ./run-load.sh (or ./create_table_doris.sh)"; break; fi
done

echo "local pipeline"
[ -s models/det_10g.onnx ] && [ -s models/w600k_r50.onnx ] && ok "models/ (buffalo_l)" \
  || warn "models/ missing (downloaded on first ./run-embedding.sh / ./run-web-server.sh)"
if [ -f faces.db ]; then ok "faces.db: $("$PY" -c "import sqlite3; print(*sqlite3.connect('faces.db').execute('SELECT count(*), count(embedding) FROM faces').fetchone(), sep=' rows, ')") with a vector"
else warn "no faces.db (./run-embedding.sh)"; fi
n=$(ls export/*.jsonl 2>/dev/null | wc -l | tr -d ' ')
[ "$n" -gt 0 ] && ok "export/: $n jsonl chunk(s)" || warn "no export/*.jsonl (./run-export.sh)"

echo; [ "$fails" = 0 ] && echo "all checks passed" || echo "$fails check(s) failed"
exit "$fails"
