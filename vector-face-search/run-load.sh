#!/usr/bin/env bash
# Create the Doris tables (create_table_doris.sh) and stream-load every chunk from export/
# (face-export.py): faces-*.jsonl -> <database>.faces, people-*.jsonl -> <database>.people.
#
#   ./run-load.sh              # create tables if missing, load all chunks
#   ./run-load.sh --truncate   # empty the tables first (they are duplicate-key,
#                              # so re-loading without this doubles the rows)
#   ./run-load.sh --recreate   # DROP and re-create the tables (after a schema change)
#   FROM=faces-0005 ./run-load.sh          # resume: skip chunks sorting before this name
#   VELODB_DATABASE=face_test ./run-load.sh   # load into another database without editing .env
#
# Connection and table settings come from .env / the environment (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh

EXPORT_DIR="${EXPORT_DIR:-export}"
MODE="${1:-}"
case "$MODE" in --truncate|--recreate|"") ;; *) echo "usage: $0 [--truncate|--recreate]" >&2; exit 2;; esac
command -v jq >/dev/null || { echo "jq is required (brew install jq)" >&2; exit 1; }
if ! ls "$EXPORT_DIR"/*.jsonl >/dev/null 2>&1; then
    echo "no $EXPORT_DIR/*.jsonl files -- run ./run-export.sh first" >&2
    exit 1
fi

PY=$(./ensure-venv.sh)
sql() { "$PY" doris_sql.py --env "$ENV_FILE" -D "$DATABASE" -q -e "$1"; }

./create_table_doris.sh $( [ "$MODE" = --recreate ] && echo --recreate )

if [ "$MODE" = --truncate ]; then
    for TABLE in faces people; do
        echo "==> truncating $DATABASE.$TABLE"
        sql "TRUNCATE TABLE $TABLE"
    done
fi

n=0
for f in "$EXPORT_DIR"/*.jsonl; do
    if [ -n "${FROM:-}" ] && [[ "$(basename "$f")" < "$FROM" ]]; then continue; fi
    n=$((n + 1))
    TABLE=$(basename "$f" | sed 's/-.*//')
    URL="${VELODB_STREAM_LOAD_URL%/}/api/$DATABASE/$TABLE/_stream_load"
    label="${TABLE}_$(basename "$f" .jsonl)_$(date +%Y%m%d_%H%M%S)"
    echo "==> loading $f -> $DATABASE.$TABLE (label $label)"
    # Retry network failures with the SAME label: Doris commits a label at most
    # once, so a retry of a load that did commit answers "Label Already Exists".
    for attempt in 1 2 3 4 5; do
        resp=$(curl --silent --show-error --location-trusted \
            -u "$VELODB_USER:$VELODB_PASSWORD" \
            -H "Expect: 100-continue" \
            -H "label: $label" \
            -H "format: json" \
            -H "read_json_by_line: true" \
            -H "timeout: 1800" \
            -T "$f" -X PUT "$URL") || resp='{"Status":"CurlError"}'
        status=$(printf '%s' "$resp" | jq -r '.Status // "NoStatus"' 2>/dev/null || echo NoStatus)
        if [ "$status" = "Label Already Exists" ]; then
            echo "    label already committed by an earlier attempt"; status=Success; break
        fi
        [ "$status" = "CurlError" ] || [ "$status" = "NoStatus" ] || break
        echo "    attempt $attempt failed ($status), retrying in 10s"; sleep 10
    done
    loaded=$(printf '%s' "$resp" | jq -r '.NumberLoadedRows // 0' 2>/dev/null || echo 0)
    filtered=$(printf '%s' "$resp" | jq -r '.NumberFilteredRows // 0' 2>/dev/null || echo 0)
    took=$(printf '%s' "$resp" | jq -r '.LoadTimeMs // 0' 2>/dev/null || echo 0)
    echo "    status=$status loaded=$loaded filtered=$filtered time=${took}ms"
    if [ "$status" != "Success" ]; then
        printf '%s\n' "$resp" >&2
        echo "load failed; error details: $(printf '%s' "$resp" | jq -r '.ErrorURL // empty' 2>/dev/null)" >&2
        exit 1
    fi
done

echo "==> $n chunk(s) loaded; row counts:"
sql "SELECT (SELECT count(*) FROM faces) AS faces, (SELECT count(*) FROM people) AS people"
