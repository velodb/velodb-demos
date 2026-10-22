#!/usr/bin/env bash
# Preflight: verify config, tools, the Python venv, VeloDB/Doris connectivity (MySQL + stream-load ports),
# the demo table and the local data files.
#   ./check_env.sh                                       # uses .env + environment
#   VELODB_DATABASE=hybrid_search_test ./check_env.sh    # check another database without editing .env
# Read-only (except creating ./venv on first use). Exit code = number of failed checks.
set -uo pipefail
cd "$(dirname "$0")"
set -e; . ./env.sh; require_velodb; set +e

fails=0
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; }
warn() { printf '  \033[33mwarn\033[0m  %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; fails=$((fails+1)); }

echo "config ($( [ -f "$ENV_FILE" ] && echo "$ENV_FILE + environment" || echo "environment only, no $ENV_FILE"))"
echo "  VELODB_HOST=$VELODB_HOST  VELODB_MYSQL_PORT=$VELODB_MYSQL_PORT  VELODB_STREAM_LOAD_PORT=$VELODB_STREAM_LOAD_PORT"
echo "  VELODB_USER=$VELODB_USER  VELODB_PASSWORD=$( [ -n "$VELODB_PASSWORD" ] && echo '(set)' || echo '(empty)')  VELODB_DATABASE=$VELODB_DATABASE"
case "$VELODB_HOST$VELODB_PASSWORD" in *example.com*|*change-me*) bad "placeholder values from env.example are still in use";; esac

echo "tools"
for t in curl jq go; do
  if command -v "$t" >/dev/null; then ok "$t"
  else case "$t" in go) warn "go not found (needed only for run-ui.sh)";; *) bad "$t not found (run-load.sh)";; esac; fi
done
if "$PYTHON" -c "import sentence_transformers, torch, numpy, pymysql" 2>/dev/null; then
  ok "venv: $("$PYTHON" --version), torch $("$PYTHON" -c 'import torch; print(torch.__version__)')"
else bad "venv is missing packages: rm -rf venv and rerun"; fi

echo "VeloDB MySQL protocol ${VELODB_HOST}:${VELODB_MYSQL_PORT}"
sql() {  # values without the header line; on error the error message and rc 1
  local out; out=$("$PYTHON" doris-sql.py -e "$1" 2>&1) || { echo "$out" | tail -1; return 1; }
  echo "$out" | grep -v '^-- ' | tail -n +2
}
if out=$(sql "SELECT concat(version(), ' / ', current_user())"); then ok "connected: $out"
else bad "cannot connect: $out"; fi

url="http://${VELODB_HOST}:${VELODB_STREAM_LOAD_PORT}/api/health"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$url")
[ "$code" = 200 ] && ok "stream load $url -> 200" || bad "stream load $url -> $code (check VELODB_STREAM_LOAD_PORT)"

echo "demo table ${VELODB_DATABASE}.products"
n=$(sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='${VELODB_DATABASE}' AND table_name='products'")
if [ "$n" = 1 ]; then ok "exists: $(sql "SELECT count(*) FROM ${VELODB_DATABASE}.products") rows"
else warn "missing: run ./run-load.sh"; fi

echo "local data"
[ -s products.db ] && ok "products.db ($(du -h products.db | cut -f1))" || warn "no products.db: run ./run-embedding.sh (needed by every run-*.sh except run-load.sh)"
ls export/*.jsonl >/dev/null 2>&1 && ok "export/: $(ls export/*.jsonl | wc -l | tr -d ' ') chunk(s)" || warn "no export/*.jsonl: run ./run-export.sh (needed only by run-load.sh)"

echo; [ "$fails" = 0 ] && echo "all checks passed" || echo "$fails check(s) failed"
exit "$fails"
