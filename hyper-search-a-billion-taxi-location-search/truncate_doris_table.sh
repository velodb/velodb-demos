#!/usr/bin/env bash
# Delete everything in the demo table.
#   ./truncate_doris_table.sh          # TRUNCATE TABLE (keeps schema and the static partitions, all emptied)
#   ./truncate_doris_table.sh --drop   # DROP + CREATE instead (static partitions re-anchored on BASE = now)
# Either way the BASE marker is reset: --drop writes the new BASE, TRUNCATE removes it.
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
PY=$(./ensure-venv.sh)
sql() { "$PY" doris_sql.py --env "$ENV_FILE" -q "$@" 2>/dev/null; }
running_loads() {  # labels of PENDING/ETL/LOADING broker loads on this table
  sql -e "SHOW LOAD FROM ${DATABASE} ORDER BY CreateTime DESC LIMIT 100" 2>/dev/null \
    | awk -F'\t' -v t="${TABLE}" 'NR>1 && index($2, t"_")==1 && $3 ~ /^(PENDING|ETL|LOADING)$/ {print $2" ("$3" "$4")"}'
}

running=$(running_loads)
if [ -n "$running" ] && [ "${FORCE:-0}" != 1 ]; then
  echo "refusing: a broker load is running on ${DATABASE}.${TABLE} (truncating now would make it fail at commit):" >&2
  echo "$running" >&2
  echo "cancel it first: venv/bin/python doris_sql.py -e \"CANCEL LOAD FROM ${DATABASE} WHERE LABEL='<label>'\"  (or FORCE=1 to override)" >&2
  exit 2
fi

before=$(sql -e "SELECT count(*) FROM ${DATABASE}.${TABLE}" | tail -1)
echo "${DATABASE}.${TABLE}: ${before} rows before"
if [ "${1:-}" = "--drop" ]; then
  ./create_table_doris.sh --base "$(date -u +'%Y-%m-%d %H:%M:%S')" >/dev/null
  echo "table dropped and recreated, base marker $MARKER: BASE $(cat "$MARKER") (UTC)"
else
  sql -e "TRUNCATE TABLE ${DATABASE}.${TABLE}" >/dev/null
  echo "table truncated"
  rm -f "$MARKER" && echo "base marker $MARKER removed (the next load uses BASE = now; it must fall on an existing day partition)"
fi
after=$(sql -e "SELECT count(*) FROM ${DATABASE}.${TABLE}" | tail -1)
parts=$(sql -e "SELECT count(*) FROM information_schema.partitions WHERE table_schema='${DATABASE}' AND table_name='${TABLE}'" | tail -1)
echo "${DATABASE}.${TABLE}: ${after} rows after, ${parts} partitions"
