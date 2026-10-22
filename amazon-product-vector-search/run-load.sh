#!/usr/bin/env bash
# Create the Doris table (create_doris_table.sql) and stream-load every chunk
# from export/ into $VELODB_DATABASE.products (default hybrid_search_demo).
#
#   ./run-load.sh              # create table if missing, load all chunks
#   ./run-load.sh --truncate   # empty the table first (table is duplicate-key,
#                              # so re-loading without this doubles the rows)
#   ./run-load.sh --recreate   # DROP and re-create the table (after a schema change)
#
# Connection: VELODB_* from the environment or .env (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
require_velodb

DB="$VELODB_DATABASE"
TABLE=products
EXPORT_DIR="${EXPORT_DIR:-export}"

if ! ls "$EXPORT_DIR"/*.jsonl >/dev/null 2>&1; then
    echo "no $EXPORT_DIR/*.jsonl files -- run ./run-export.sh first" >&2
    exit 1
fi

sql() { "$PYTHON" doris-sql.py --db "$DB" -e "$1"; }

if [ "${1:-}" = "--recreate" ]; then
    echo "==> dropping $DB.$TABLE"
    "$PYTHON" doris-sql.py -e "DROP TABLE IF EXISTS $DB.$TABLE"
fi
echo "==> creating $DB.$TABLE if needed"
"$PYTHON" doris-sql.py -e "$(sed "s/hybrid_search_demo/$DB/g" create_doris_table.sql)"

if [ "${1:-}" = "--truncate" ]; then
    echo "==> truncating $DB.$TABLE"
    sql "TRUNCATE TABLE $TABLE"
fi

URL="http://$VELODB_HOST:$VELODB_STREAM_LOAD_PORT/api/$DB/$TABLE/_stream_load"
n=0
for f in "$EXPORT_DIR"/*.jsonl; do
    n=$((n + 1))
    label="${TABLE}_$(basename "$f" .jsonl)_$(date +%Y%m%d_%H%M%S)"
    echo "==> loading $f (label $label)"
    resp=$(curl --silent --show-error --location-trusted \
        -u "$VELODB_USER:$VELODB_PASSWORD" \
        -H "Expect: 100-continue" \
        -H "label: $label" \
        -H "format: json" \
        -H "read_json_by_line: true" \
        -H "timeout: 1800" \
        -T "$f" -X PUT "$URL")
    status=$(printf '%s' "$resp" | jq -r '.Status // "NoStatus"')
    loaded=$(printf '%s' "$resp" | jq -r '.NumberLoadedRows // 0')
    filtered=$(printf '%s' "$resp" | jq -r '.NumberFilteredRows // 0')
    took=$(printf '%s' "$resp" | jq -r '.LoadTimeMs // 0')
    echo "    status=$status loaded=$loaded filtered=$filtered time=${took}ms"
    if [ "$status" != "Success" ]; then
        echo "$resp" | jq . >&2
        echo "load failed; error details: $(printf '%s' "$resp" | jq -r '.ErrorURL // empty')" >&2
        exit 1
    fi
done

echo "==> $n chunk(s) loaded; row count in $DB.$TABLE:"
sql "SELECT count(*) AS rows_in_doris FROM $TABLE"
