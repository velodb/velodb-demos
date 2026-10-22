#!/usr/bin/env bash
# Bulk-load the pre-generated history from S3 with Broker Load, materializing
#   ts = milliseconds_add(BASE, offset_ms)      (offset_ms <= 0, so all rows land before BASE)
# BASE marker: load_base_<database>.<table>.txt. If it exists, its BASE is reused and the run simply continues where it left off
# (FINISHED chunks skipped, a LOADING chunk waited on, missing/CANCELLED chunks submitted) - rerun until all
# chunks are done. If it does not exist, BASE = now (UTC) and the marker is written.
# truncate_doris_table.sh deletes the marker, so the next load starts a fresh BASE.
#
#   ./load_into_doris.sh                          # default: 2 sequential chunks (~245 days each), wait for completion
#   ./load_into_doris.sh --chunks 5               # more, smaller loads (less BE memory: a load holds a memtable
#                                                 #   per tablet it touches; 494 partitions x 32 buckets at once OOMs a 32 GB BE)
#   ./load_into_doris.sh --recreate               # DROP + CREATE the table first (create_table_doris.sh); new BASE
#   ./load_into_doris.sh --base '2026-08-26 10:00:00'
#   ./load_into_doris.sh --glob 'day_00*.parquet' # subset (single load)
#   ./load_into_doris.sh --prefix hyper-search-test --glob day_489_01.parquet   # smoke test
#   ./load_into_doris.sh --status                 # show every load job of the current BASE (from the marker)
#   ./load_into_doris.sh --status --watch         # ... and keep polling until all of them are FINISHED/CANCELLED
#   ./load_into_doris.sh --status <label>         # follow one specific job
#   ./load_into_doris.sh --no-wait                # submit (first chunk) and return
#   ./load_into_doris.sh --append                 # allow loading into a non-empty table with no marker (default: refuse)
#   ./load_into_doris.sh --local data-small       # no S3: stream-load local parquet files (small test sets; same BASE/ts mapping)
#   LOAD_EXEC_MEM_LIMIT=8589934592 ./load_into_doris.sh   # set the per-BE exec_mem_limit property (bytes)
# Every run is a NEW load with new labels; rows are appended, never deduplicated.
set -euo pipefail
cd "$(dirname "$0")"

. ./env.sh
yaml() { [ -f s3.yaml ] || return 0; { grep -i "^$1:" s3.yaml || true; } | head -1 | sed -E 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"; }
S3_ENDPOINT=$(yaml Endpoint); S3_AK=$(yaml access_key); S3_SK=$(yaml secret_key)
S3_BUCKET=$(yaml Bucket); S3_PREFIX=$(yaml Prefix)
S3_REGION=$(yaml Region); S3_REGION="${S3_REGION:-us-east-1}"

BASE=""; RECREATE=0; APPEND=0; RESUME=0; GLOB="day_*.parquet"; WAIT=1; TIMEOUT="${LOAD_TIMEOUT:-14400}"
STATUS_LABEL=""; STATUS=0; WATCH=0; POLL="${POLL_SECS:-10}"; CHUNKS=2; DAYS=""; LOCAL_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="$2"; shift 2;;
    --recreate) RECREATE=1; shift;;
    --append) APPEND=1; shift;;
    --resume) shift;;   # implied by the marker file; kept for compatibility
    --chunks) CHUNKS="$2"; shift 2;;
    --days) DAYS="$2"; shift 2;;
    --glob) GLOB="$2"; shift 2;;
    --prefix) S3_PREFIX="$2"; shift 2;;
    --local) LOCAL_DIR="$2"; CHUNKS=1; shift 2;;
    --no-wait) WAIT=0; shift;;
    --timeout) TIMEOUT="$2"; shift 2;;
    --status) STATUS=1; if [ $# -gt 1 ] && [[ "$2" != --* ]]; then STATUS_LABEL="$2"; shift 2; else shift; fi;;
    --watch) WATCH=1; shift;;
    -h|--help) sed -n '2,22p' "$0"; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 1;;
  esac
done

PY=$(./ensure-venv.sh)
sql() { "$PY" doris_sql.py --env "$ENV_FILE" -q "$@"; }

running_loads() {  # labels of PENDING/ETL/LOADING broker loads on this table
  sql -e "SHOW LOAD FROM ${DATABASE} ORDER BY CreateTime DESC LIMIT 100" 2>/dev/null \
    | awk -F'\t' -v t="${TABLE}" 'NR>1 && index($2, t"_")==1 && $3 ~ /^(PENDING|ETL|LOADING)$/ {print $2" ("$3" "$4")"}'
}

show_load() {  # prints "STATE|PROGRESS|ERRORMSG|URL|LOADFINISHTIME|ETLINFO" for a label
  sql -G -e "SHOW LOAD FROM ${DATABASE} WHERE LABEL = '$1'" 2>/dev/null | awk -F': ' '
    /^ *State:/ {s=$2} /^ *Progress:/ {p=$2} /^ *ErrorMsg:/ {e=$2} /^ *URL:/ {u=$2} /^ *LoadFinishTime:/ {f=$2} /^ *EtlInfo:/ {i=$2}
    END {printf "%s|%s|%s|%s|%s|%s\n", s, p, e, u, f, i}'
}

load_state() { show_load "$1" | cut -d'|' -f1; }

wait_for() {
  local label="$1" t0=$(date +%s)
  while :; do
    IFS='|' read -r state progress errmsg url finish etl <<<"$(show_load "$label")"
    printf '[%5ss] %-24s %-9s %s  %s\n' "$(( $(date +%s) - t0 ))" "$label" "$state" "$progress" "$etl"
    case "$state" in
      FINISHED) return 0;;
      CANCELLED) echo "load CANCELLED: $errmsg"; [ -n "$url" ] && [ "$url" != "NULL" ] && echo "error url: $url"; return 1;;
      "") echo "label $label not found" >&2; return 1;;
    esac
    sleep "$POLL"
  done
}

summary() {
  echo "table summary:"
  sql -e "SELECT count(*) AS rows_total, min(ts) AS oldest, max(ts) AS newest, count(DISTINCT plate) AS plates FROM ${DATABASE}.${TABLE}" 2>/dev/null
  sql -e "SELECT count(*) AS partitions FROM information_schema.partitions WHERE table_schema='${DATABASE}' AND table_name='${TABLE}'" 2>/dev/null
}

status_all() {  # all jobs whose label belongs to the BASE in the marker; returns 1 while any is still running
  local base prefix rows
  base=$(cat "$MARKER" 2>/dev/null || true)
  [ -n "$base" ] || { echo "no base marker ($MARKER): nothing loaded since the last truncate" >&2; return 2; }
  prefix="${TABLE}_$(echo "$base" | tr -cd '0-9')"
  rows=$(sql -e "SHOW LOAD FROM ${DATABASE} WHERE LABEL LIKE '${prefix}%' ORDER BY CreateTime" 2>/dev/null     | awk -F'\t' 'NR>1 {printf "  %-40s %-9s %-18s %s\n", $2, $3, $4, ($8=="NULL"?"":substr($8,1,90))}')
  echo "BASE ${base} (UTC), jobs ${prefix}*  [$(date -u +%H:%M:%S)]"
  if [ -z "$rows" ]; then echo "  (no load jobs found for this BASE)"; return 0; fi
  echo "$rows"
  echo "$rows" | awk '{print $2}' | grep -qE '^(PENDING|ETL|LOADING)$'
  [ $? -ne 0 ]
}

if [ -n "$STATUS_LABEL" ]; then wait_for "$STATUS_LABEL" && summary; exit; fi
if [ "$STATUS" = 1 ]; then
  while :; do
    if status_all; then break; fi
    rc=$?; [ "$rc" = 2 ] && exit 2
    [ "$WATCH" = 1 ] || exit 0
    sleep "$POLL"
  done
  summary; exit 0
fi

# ---- BASE marker ----
if [ "$RECREATE" = 1 ]; then rm -f "$MARKER"; fi
if [ -n "$BASE" ]; then
  RESUME=0
elif [ -s "$MARKER" ]; then
  BASE=$(cat "$MARKER"); RESUME=1
  echo "base marker $MARKER found: continuing load with BASE = ${BASE} (UTC)"
else
  BASE=$(date -u +'%Y-%m-%d %H:%M:%S')
fi
LABEL_BASE="${TABLE}_$(echo "$BASE" | tr -cd '0-9')"

# ---- chunk plan: list of "label|infile-list" ----
if [ "$CHUNKS" -gt 1 ]; then
  if [ -z "$DAYS" ]; then
    DAYS=$("$PY" -c "import json,sys; print(json.load(open('data/manifest.json'))['config']['days'])" 2>/dev/null || true)
    [ -n "$DAYS" ] || { echo "--chunks needs --days N (day count; data/manifest.json not found)" >&2; exit 1; }
  fi
  PLAN=()
  per=$(( (DAYS + CHUNKS - 1) / CHUNKS ))
  for ((c = 0; c < CHUNKS; c++)); do
    from=$(( c * per )); to=$(( from + per )); [ "$to" -gt "$DAYS" ] && to=$DAYS
    [ "$from" -ge "$to" ] && break
    files=""
    for ((d = from; d < to; d++)); do files+="$(printf ',\n    "s3://%s/%s/day_%03d_*.parquet"' "$S3_BUCKET" "$S3_PREFIX" "$d")"; done
    PLAN+=("$(printf '%s_c%02d|%s|days %d..%d' "$LABEL_BASE" "$((c+1))" "${files#,$'\n'    }" "$from" "$((to-1))")")
  done
else
  PLAN=("${LABEL_BASE}|\"s3://${S3_BUCKET}/${S3_PREFIX}/${GLOB}\"|${GLOB}")
fi

MEM_PROP=""
[ -n "${LOAD_EXEC_MEM_LIMIT:-}" ] && MEM_PROP=$(printf ',\n  "exec_mem_limit" = "%s"' "$LOAD_EXEC_MEM_LIMIT")

write_load_sql() {  # $1 label, $2 infile list, $3 description -> start_broker_load.sql
  cat > start_broker_load.sql <<SQL
-- Generated by load_into_doris.sh on $(date -u +'%Y-%m-%dT%H:%M:%SZ'); BASE = ${BASE} (UTC); $3
LOAD LABEL ${DATABASE}.$1
(
  DATA INFILE($2)
  INTO TABLE ${TABLE}
  FORMAT AS "parquet"
  (offset_ms, taxi_id, plate, company, geohash6, geohash8, lat, lon, status, speed_kmh, heading)
  SET (ts = milliseconds_add('${BASE}', offset_ms))
)
WITH S3 (
  "s3.endpoint"   = "${S3_ENDPOINT}",
  "s3.region"     = "${S3_REGION}",
  "s3.access_key" = "${S3_AK}",
  "s3.secret_key" = "${S3_SK}"
)
PROPERTIES (
  "timeout" = "${TIMEOUT}",
  "max_filter_ratio" = "0"${MEM_PROP}
);
SQL
}

# ---- guards (a fresh BASE must start from an empty, idle table; a marker means we are continuing) ----
if [ "$RECREATE" = 1 ]; then
  ./create_table_doris.sh
elif [ "$RESUME" != 1 ] && [ "$APPEND" != 1 ]; then
  running=$(running_loads)
  if [ -n "$running" ]; then
    echo "refusing: a load is already running on ${DATABASE}.${TABLE}:" >&2; echo "$running" >&2
    echo "wait for it (./load_into_doris.sh --status <label>) or CANCEL LOAD it; --append to force." >&2
    exit 2
  fi
  existing=$(sql -e "SELECT count(*) FROM ${DATABASE}.${TABLE}" 2>/dev/null | tail -1)
  if [ "${existing:-0}" != 0 ]; then
    echo "refusing: ${DATABASE}.${TABLE} already has ${existing} rows." >&2
    echo "run ./truncate_doris_table.sh first, or use --recreate, or --append to load on top." >&2
    exit 2
  fi
fi

if [ -z "$LOCAL_DIR" ]; then
  [ -n "$S3_BUCKET" ] && [ -n "$S3_AK" ] || { echo "s3.yaml (Bucket, access_key, secret_key, ...) is required for Broker Load; see s3.yaml.example (or use --local DIR)" >&2; exit 1; }
fi
echo "BASE   = ${BASE} (UTC)   -> marker $MARKER"
echo "$BASE" > "$MARKER"
# ---- local mode: one stream load per parquet file (label per file, so reruns skip what is loaded) ----
if [ -n "$LOCAL_DIR" ]; then
  shopt -s nullglob; files=("$LOCAL_DIR"/$GLOB); shopt -u nullglob
  [ ${#files[@]} -gt 0 ] || { echo "no $GLOB in $LOCAL_DIR" >&2; exit 1; }
  url="${VELODB_STREAM_LOAD_URL%/}/api/${DATABASE}/${TABLE}/_stream_load"
  echo "SOURCE = ${LOCAL_DIR}/${GLOB} (${#files[@]} files) -> stream load ${url}"
  cols="offset_ms,taxi_id,plate,company,geohash6,geohash8,lat,lon,status,speed_kmh,heading,ts=milliseconds_add('${BASE}', offset_ms)"
  esc() { printf %s "$1" | sed 's/[\\"]/\\&/g'; }
  auth="user = \"$(esc "$VELODB_USER"):$(esc "$VELODB_PASSWORD")\""   # via curl -K -, keeps the password out of ps
  n=0; failed=0
  for f in "${files[@]}"; do
    n=$((n+1)); label="${LABEL_BASE}_$(basename "$f" .parquet)"
    resp=$(curl -sS --location-trusted -K - -H "label: ${label}" -H "format: parquet" -H "columns: ${cols}" \
      -H "max_filter_ratio: 0" -H "timeout: ${TIMEOUT}" -T "$f" "$url" <<<"$auth") || { failed=1; break; }
    IFS='|' read -r st loaded msg <<<"$("$PY" -c 'import json,sys; d=json.load(sys.stdin); print(d.get("Status",""), d.get("NumberLoadedRows",0), d.get("Message",""), sep="|")' <<<"$resp" 2>/dev/null || echo "?|0|$resp")"
    case "$st" in
      Success|"Publish Timeout") printf '[%d/%d] %-40s %-8s %s rows\n' "$n" "${#files[@]}" "$label" "$st" "$loaded";;
      "Label Already Exists") printf '[%d/%d] %-40s already loaded - skipping\n' "$n" "${#files[@]}" "$label";;
      *) echo "[$n/${#files[@]}] $label FAILED: $st $msg" >&2; echo "$resp" >&2; failed=1; break;;
    esac
  done
  summary
  [ "$failed" = 0 ] && echo "all ${#files[@]} files loaded for BASE ${BASE}; marker $MARKER kept (truncate_doris_table.sh removes it)"
  exit "$failed"
fi

echo "SOURCE = s3://${S3_BUCKET}/${S3_PREFIX}/   chunks = ${#PLAN[@]}"

# ---- run chunks sequentially ----
n=0; failed=0
for entry in "${PLAN[@]}"; do
  n=$((n+1))
  label="${entry%%|*}"; rest="${entry#*|}"; infiles="${rest%|*}"; desc="${rest##*|}"
  state=$(load_state "$label")
  case "$state" in
    FINISHED) echo "[$n/${#PLAN[@]}] $label already FINISHED ($desc) - skipping"; continue;;
    PENDING|ETL|LOADING) echo "[$n/${#PLAN[@]}] $label is $state ($desc) - waiting";;
    *)
      write_load_sql "$label" "$infiles" "$desc"
      echo "[$n/${#PLAN[@]}] submitting $label ($desc) ..."
      sql start_broker_load.sql
      ;;
  esac
  if [ "$WAIT" != 1 ]; then echo "submitted; poll with: ./load_into_doris.sh --status ${label}   (rerun ./load_into_doris.sh to continue)"; exit 0; fi
  if ! wait_for "$label"; then failed=1; echo "chunk $n failed; fix the cause and rerun:  ./load_into_doris.sh --chunks ${CHUNKS}   (marker keeps BASE; finished chunks are skipped)" >&2; break; fi
done
summary
if [ "$failed" = 0 ]; then echo "all ${#PLAN[@]} chunks FINISHED for BASE ${BASE}; marker $MARKER kept (truncate_doris_table.sh removes it)"; fi
[ "$failed" = 0 ]
