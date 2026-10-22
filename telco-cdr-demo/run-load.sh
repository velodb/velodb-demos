#!/usr/bin/env bash
# Load the generated telco CDR Parquet (s3.env: s3://<bucket>/<prefix>/<table>/<run_id>/) into the VeloDB cluster
# (VELODB_* from the environment or .env, see env.example), database VELODB_DATABASE (default telco_demo).
#
# START_MONTH (YYYYMM) is required: the generator's 90 day slots are spread over that month and the next two,
# e.g. 202606 -> 2026-06-01 .. 2026-08-31. Nothing in the files is tied to a date.
#
# Tables load one after the other (call_log, then data_stream_log). Within a table, its batch folders (one ~10GiB
# S3 Load job each) run --parallel N at a time (default 1 = one job at a time). Labels are deterministic
# (<table>_<START_MONTH>_<run>_<table creation time>_bNNNNN), so re-running skips finished batches, adopts batches
# still loading and resubmits cancelled ones; a batch cancelled while this script runs is retried up to MAX_RETRIES
# times. The creation time in the label means --create (new tables) starts every batch afresh, even though Doris
# keeps the labels of loads into the dropped tables.
#
# call_log also gets its duplicates: the same folders loaded again with a hash filter that keeps DUP_PERMILLE/1000
# of the CDRs (default 5 = 0.5%), exact copies like mediation re-delivering files, for the dedup step to remove.
# They share the call_log job pool, queued after the main batches.
#
#   nohup ./run-load.sh 202606 --create --parallel 16 > run-load.log 2>&1 &   # recreate the tables, load everything
#   ./run-load.sh 202606 --parallel 16       # load / resume everything
#   ./run-load.sh 202606 call_log            # one table (call_log incl. duplicates | data_stream_log | dups)
#   ./run-load.sh 202606 call_log --batch 00003
#   ./run-load.sh 202606 call_log --print    # print the LOAD statement for batch 00001 (secret masked) and exit
#   ./run-load.sh --status                   # load jobs in the database
#
# Doris runs at most async_loading_load_task_pool_size (50 on this cluster) loads at once and queues the rest as
# PENDING; each job is also split across backends by LOAD_PARALLELISM.
#
# Env overrides: VELODB_DATABASE (telco_demo), RUN_ID (q3), MONTHS (3), SLOTS (90, must match day_off in gen/*.spec),
# DUP_PERMILLE (5; 0 = no duplicates), PARALLEL (1, same as --parallel), LOAD_PARALLELISM (16, the per-job
# load_parallelism property), MAX_RETRIES (2), POLL seconds (20), LOAD_TIMEOUT seconds per job (28800).
set -euo pipefail
if (( BASH_VERSINFO[0] < 4 )); then echo "error: needs bash 4+ (associative arrays)" >&2; exit 1; fi
cd "$(dirname "$0")"
. ./env.sh
require_velodb
load_s3_env
. ./s3-alias.sh
DATABASE="$VELODB_DATABASE"
RUN_ID="${RUN_ID:-q3}"
MONTHS="${MONTHS:-3}"
SLOTS="${SLOTS:-90}"
DUP_PERMILLE="${DUP_PERMILLE:-5}"
LOAD_TIMEOUT="${LOAD_TIMEOUT:-28800}"
PARALLEL="${PARALLEL:-1}"
LOAD_PARALLELISM="${LOAD_PARALLELISM:-16}"
MAX_RETRIES="${MAX_RETRIES:-2}"
POLL="${POLL:-20}"

export MYSQL_PWD="$VELODB_PASSWORD"   # keeps the password off the command line and silences the CLI warning
db()  { mysql -h"$VELODB_HOST" -P"$VELODB_MYSQL_PORT" -u"$VELODB_USER" "$@"; }
sql() { db -D"$DATABASE" --batch --skip-column-names -e "$1"; }
usage() { sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }

if [[ "${1:-}" == "--status" ]]; then
  db -D"$DATABASE" -e "SHOW LOAD\G" | grep -E "^ *(Label|State|Progress|ErrorMsg|LoadStartTime|LoadFinishTime|JobDetails):"
  exit 0
fi

START_MONTH="${1:-}"
[[ "$START_MONTH" =~ ^[0-9]{4}(0[1-9]|1[0-2])$ ]] || { echo "error: start month YYYYMM is required, e.g. ./run-load.sh 202606" >&2; usage; }
shift
TABLES="all"
if [[ $# -gt 0 && "$1" != --* ]]; then TABLES="$1"; shift; fi
CREATE=0; BATCH=""; PRINT=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --create) CREATE=1; shift ;;
    --batch)  BATCH="$2"; shift 2 ;;
    --print)  PRINT=1; BATCH="${BATCH:-00001}"; shift ;;
    --parallel) PARALLEL="$2"; shift 2 ;;
    *) echo "unknown option $1" >&2; usage ;;
  esac
done
case "$TABLES" in
  all) STAGES="call_log data_stream_log" ;;
  call_log|data_stream_log|dups) STAGES="$TABLES" ;;
  *) echo "unknown table $TABLES" >&2; usage ;;
esac
[[ "$PARALLEL" =~ ^[1-9][0-9]*$ ]] || { echo "error: --parallel needs a positive number" >&2; exit 1; }

read -r START_DATE END_DATE DAYS < <(python3 - "$START_MONTH" "$MONTHS" <<'EOF'
import sys, datetime
s, n = sys.argv[1], int(sys.argv[2])
y, m = int(s[:4]), int(s[4:])
start = datetime.date(y, m, 1)
ey, em = divmod(m - 1 + n, 12)
end = datetime.date(y + ey, em + 1, 1)
print(start, end, (end - start).days)
EOF
)

# ---------------------------------------------------------------------------------------------
# Expressions shared by both tables. $1 = raw column holding the 0..99999999 subscriber draw.
# ---------------------------------------------------------------------------------------------
# 0.3u + 0.7u^2 skews activity without a runaway head: the lowest 10% of MSISDNs carry ~22% of the records,
# and the busiest subscriber sees ~5x the traffic of the quietest.
msisdn()  { echo "(6580000000 + CAST((0.3 * $1 / 100000000.0 + 0.7 * POW($1 / 100000000.0, 2)) * 5000000 AS BIGINT))"; }
# The generator's SLOTS (90) day slots are stretched over the calendar window: day = floor((day_off + u) * DAYS / SLOTS),
# where u is the (independent) position inside the hour, so every day of the 3 months is filled evenly and the
# hour-of-day curve is kept.
start_ts() { echo "milliseconds_add(hours_add(days_add(CAST('$START_DATE 00:00:00' AS DATETIME(3)), CAST(floor((day_off + ms_of_hour / 3600000.0) * $DAYS / $SLOTS) AS INT)), hour_of_day), ms_of_hour)"; }
imsi()    { echo "concat('52501', lpad(CAST($(msisdn "$1") - 6580000000 AS STRING), 10, '0'))"; }
# Each ~30-slot period (about a month), a different 4% of subscribers travel: one foreign network, a 3..7 day trip.
# (hashed on msisdn + period, so travellers are independent of the prepaid/postpaid and plan split, which use the low
# MSISDN digits, and differ from month to month)
is_roaming() { local m; m=$(msisdn "$1"); echo "(abs(murmur_hash3_32(concat(CAST($m AS STRING), '-', CAST(floor(day_off / 30) AS STRING)))) % 1000 < 40 AND day_off % 30 BETWEEN $m % 23 AND $m % 23 + 2 + $m % 5)"; }
visited_plmn() { local m; m=$(msisdn "$1"); echo "IF($(is_roaming "$1"), element_at(['50212','51010','52001','46000','310260','23415','44010','50501','45400'], CAST(($m + floor(day_off / 30)) % 9 + 1 AS INT)), '52501')"; }
sub_type()  { echo "IF($(msisdn "$1") % 100 < 62, 'PREPAID', 'POSTPAID')"; }
rate_plan() { local m; m=$(msisdn "$1"); echo "CASE WHEN $m % 100 < 62 THEN (CASE WHEN floor($m / 100) % 10 < 5 THEN 'PRE_BASIC' WHEN floor($m / 100) % 10 < 9 THEN 'PRE_DATA_PACK' ELSE 'PRE_TOURIST' END)
    ELSE (CASE WHEN floor($m / 100) % 10 < 3 THEN 'POST_LITE_20' WHEN floor($m / 100) % 10 < 6 THEN 'POST_PLUS_40' WHEN floor($m / 100) % 10 < 8 THEN 'POST_MAX_80' ELSE 'POST_BIZ_60' END) END"; }
cell_cols() { echo "cell_id = 100000 + cell_u,
    tac = CAST(1000 + floor(cell_u / 50) AS INT)"; }

# ---------------------------------------------------------------------------------------------
call_log_columns="call_id, day_off, hour_of_day, ms_of_hour, caller_u, call_type, direction, other_party_type,
    callee_rand, dest_cc, result_raw, dur_u, ring_ms, cell_u"
call_log_set() {
  local result ring dur
  result="(CASE WHEN call_type = 'SMS' THEN IF(result_raw = 'FAILED', 'FAILED', 'DELIVERED') WHEN other_party_type = 'EMERGENCY' THEN 'ANSWERED' ELSE result_raw END)"
  ring="(CASE WHEN call_type = 'SMS' THEN 0 WHEN other_party_type = 'EMERGENCY' THEN ring_ms % 3000
      WHEN result_raw = 'NO_ANSWER' THEN 30000 + ring_ms % 5000 WHEN result_raw = 'BUSY' THEN ring_ms % 2500
      WHEN result_raw = 'FAILED' THEN ring_ms % 4000 ELSE ring_ms END)"
  # exponential talk time: mean 150s domestic, 360s international, 420s video; dropped calls mean 60s
  dur="(CASE WHEN call_type = 'SMS' THEN 0
      WHEN result_raw = 'ANSWERED' OR other_party_type = 'EMERGENCY' THEN
        LEAST(14400, 1 + CAST(-(CASE WHEN call_type = 'VIDEO' THEN 420 WHEN other_party_type = 'INTERNATIONAL' THEN 360 ELSE 150 END) * LN(1 - dur_u / 1000000.0) AS INT))
      WHEN result_raw = 'DROPPED' THEN 1 + CAST(-60 * LN(1 - dur_u / 1000000.0) AS INT)
      ELSE 0 END)"
  cat <<EOF
    msisdn = $(msisdn caller_u),
    start_time = $(start_ts),
    end_time = milliseconds_add($(start_ts), $ring + $dur * 1000),
    imsi = $(imsi caller_u),
    other_party_number = CASE other_party_type
      WHEN 'ONNET'         THEN CAST(6580000000 + callee_rand % 5000000 AS STRING)
      WHEN 'OFFNET'        THEN concat('65', IF(callee_rand % 5 = 0, '6', '9'), lpad(CAST(callee_rand % 10000000 AS STRING), 7, '0'))
      WHEN 'INTERNATIONAL' THEN concat(CAST(dest_cc AS STRING), lpad(CAST(callee_rand % 1000000000 AS STRING), 9, '0'))
      WHEN 'TOLLFREE'      THEN concat('1800', lpad(CAST(callee_rand % 10000000 AS STRING), 7, '0'))
      WHEN 'PREMIUM'       THEN concat('1900', lpad(CAST(callee_rand % 10000000 AS STRING), 7, '0'))
      ELSE '995' END,
    dest_country_code = IF(other_party_type = 'INTERNATIONAL', dest_cc, 65),
    call_result = $result,
    release_cause = CASE $result WHEN 'ANSWERED' THEN 16 WHEN 'DELIVERED' THEN 16 WHEN 'NO_ANSWER' THEN 19
      WHEN 'BUSY' THEN 17 WHEN 'DROPPED' THEN 102 ELSE IF(ring_ms % 2 = 0, 41, 34) END,
    ring_duration_ms = $ring,
    duration_sec = $dur,
    rat = CASE call_type WHEN 'VOLTE' THEN 'LTE' WHEN 'VONR' THEN 'NR' WHEN 'CS' THEN IF(cell_u % 3 = 0, 'GSM', 'UMTS')
      WHEN 'VIDEO' THEN IF(cell_u % 2 = 0, 'NR', 'LTE') ELSE IF(cell_u % 5 < 3, 'LTE', 'NR') END,
    $(cell_cols),
    msc_id = concat('MSC', lpad(CAST(floor(cell_u / 7500) + 1 AS STRING), 2, '0')),
    roaming = $(is_roaming caller_u),
    visited_plmn = $(visited_plmn caller_u),
    subscriber_type = $(sub_type caller_u),
    rate_plan = $(rate_plan caller_u)
EOF
}

# ---------------------------------------------------------------------------------------------
data_stream_log_columns="record_id, session_id, day_off, hour_of_day, ms_of_hour, sub_u, apn, rat, cell_u, ue_ip,
    app_raw, protocol, vol_u, ul_u, dur_u, term_raw"
data_stream_log_set() {
  local cat down dur
  cat="(CASE WHEN apn = 'ims' THEN 'VOICE' WHEN apn = 'mms' THEN 'MESSAGING' WHEN apn = 'iot.m2m' THEN 'IOT' WHEN apn = 'corp.vpn' THEN 'ENTERPRISE'
      WHEN app_raw IN ('YouTube', 'Netflix', 'Disney+', 'Twitch', 'TikTok') THEN 'VIDEO'
      WHEN app_raw IN ('Instagram', 'Facebook', 'X') THEN 'SOCIAL'
      WHEN app_raw IN ('WhatsApp', 'Telegram', 'WeChat') THEN 'MESSAGING'
      WHEN app_raw IN ('Spotify', 'Apple Music') THEN 'MUSIC'
      WHEN app_raw IN ('Web Browsing', 'Google Search', 'Shopee', 'Grab') THEN 'WEB'
      WHEN app_raw IN ('Mobile Legends', 'PUBG Mobile', 'Genshin Impact') THEN 'GAMING'
      WHEN app_raw IN ('Zoom', 'Microsoft Teams') THEN 'CONFERENCING'
      WHEN app_raw IN ('iCloud', 'Google Drive') THEN 'CLOUD'
      WHEN app_raw IN ('App Store', 'Google Play') THEN 'SOFTWARE_UPDATE'
      ELSE 'OTHER' END)"
  # log-uniform downlink volume, range depends on category: 10^lo .. 10^(lo+span) bytes. Sized for ~20 records per
  # subscriber per day at ~15GB per subscriber per month (a video record is 200KB..630MB, mean ~80MB).
  down="CAST(POW(10, (CASE $cat WHEN 'VIDEO' THEN 5.3 WHEN 'SOFTWARE_UPDATE' THEN 5.5 WHEN 'CONFERENCING' THEN 5.0 WHEN 'MUSIC' THEN 4.5
        WHEN 'SOCIAL' THEN 4.5 WHEN 'GAMING' THEN 4.0 WHEN 'CLOUD' THEN 4.5 WHEN 'WEB' THEN 4.0 WHEN 'VOICE' THEN 4.0
        WHEN 'ENTERPRISE' THEN 4.0 WHEN 'IOT' THEN 2.5 ELSE 3.0 END)
      + (CASE $cat WHEN 'VIDEO' THEN 3.5 WHEN 'SOFTWARE_UPDATE' THEN 3.2 WHEN 'CONFERENCING' THEN 2.7 WHEN 'MUSIC' THEN 2.5
        WHEN 'SOCIAL' THEN 3.0 WHEN 'GAMING' THEN 2.5 WHEN 'CLOUD' THEN 3.5 WHEN 'WEB' THEN 3.0 WHEN 'VOICE' THEN 2.5
        WHEN 'ENTERPRISE' THEN 3.5 WHEN 'IOT' THEN 2.0 ELSE 3.0 END) * vol_u / 1000000.0) AS BIGINT)"
  # duration = volume / log-uniform bitrate (80 kbps .. 25 Mbps), 1s .. 3600s (the PGW time-limit cut)
  dur="GREATEST(1, LEAST(3600, CAST($down / POW(10, 4.0 + 2.5 * dur_u / 1000000.0) AS INT)))"
  cat <<EOF
    msisdn = $(msisdn sub_u),
    start_time = $(start_ts),
    end_time = milliseconds_add($(start_ts), $dur * 1000 + ms_of_hour % 1000),
    imsi = $(imsi sub_u),
    $(cell_cols),
    app_name = CASE apn WHEN 'ims' THEN 'IMS Voice' WHEN 'mms' THEN 'MMS' WHEN 'iot.m2m' THEN 'IoT Telemetry'
      WHEN 'corp.vpn' THEN 'Enterprise VPN' ELSE app_raw END,
    app_category = $cat,
    downlink_bytes = $down,
    uplink_bytes = CAST($down * (CASE $cat
      WHEN 'CONFERENCING' THEN 0.6 + 0.6 * ul_u / 1000.0 WHEN 'VOICE' THEN 0.8 + 0.4 * ul_u / 1000.0
      WHEN 'GAMING' THEN 0.2 + 0.3 * ul_u / 1000.0 WHEN 'CLOUD' THEN 0.05 + 2.0 * ul_u / 1000.0
      WHEN 'IOT' THEN 0.5 + 2.5 * ul_u / 1000.0 WHEN 'ENTERPRISE' THEN 0.2 + 1.0 * ul_u / 1000.0
      ELSE 0.02 + 0.1 * ul_u / 1000.0 END) AS BIGINT),
    duration_sec = $dur,
    termination_cause = IF($dur >= 3600, 'TIME_LIMIT', term_raw),
    qos_5qi = CASE apn WHEN 'ims' THEN 5 WHEN 'corp.vpn' THEN 8 ELSE 9 END,
    rating_group = CASE WHEN apn = 'ims' THEN 99 WHEN apn = 'mms' THEN 50 WHEN $cat = 'VIDEO' THEN 20
      WHEN $cat IN ('SOCIAL', 'MESSAGING') THEN 30 WHEN $cat = 'MUSIC' THEN 40 ELSE 10 END,
    roaming = $(is_roaming sub_u),
    visited_plmn = $(visited_plmn sub_u),
    subscriber_type = $(sub_type sub_u),
    rate_plan = $(rate_plan sub_u)
EOF
}

load_stmt() { # table batch label [where]
  local t=$1 b=$2 label=$3 where=${4:-} cols setf
  cols="${t}_columns"; setf="${t}_set"
  cat <<EOF
LOAD LABEL $DATABASE.$label
(
  DATA INFILE("s3://$S3_BUCKET/$S3_PREFIX/$t/$RUN_ID/batch-$b/*.parquet")
  INTO TABLE $t
  FORMAT AS "parquet"
  (${!cols})
  SET (
$($setf)
  )${where:+
  WHERE $where}
)
WITH S3 (
  "s3.endpoint"   = "$S3_ENDPOINT",
  "s3.region"     = "$S3_REGION",
  "s3.access_key" = "$S3_AK",
  "s3.secret_key" = "$S3_SK",
  "use_path_style" = "$S3_PATH_STYLE"
)
PROPERTIES (
  "timeout" = "$LOAD_TIMEOUT",
  "max_filter_ratio" = "0",
  "load_parallelism" = "$LOAD_PARALLELISM"
);
EOF
}



if [[ $PRINT == 1 ]]; then
  t=${TABLES/dups/call_log}; [[ $t == all ]] && t=call_log
  where=""; [[ $TABLES == dups ]] && where="abs(murmur_hash3_32(call_id)) % 1000 < $DUP_PERMILLE"
  load_stmt "$t" "$BATCH" "${t}_print" "$where" | sed -E 's/("s3.secret_key" = ")[^"]*/\1***/'
  exit 0
fi

if [[ $CREATE == 1 ]]; then
  echo "creating $DATABASE tables with fixed day partitions $START_DATE .. $END_DATE (drops call_log, data_stream_log, rate_plan)"
  sed -e "s/telco_cdr/$DATABASE/g; s/__START_DATE__/$START_DATE/g; s/__END_DATE__/$END_DATE/g" schema.sql | db
fi

echo "target  $VELODB_USER@$VELODB_HOST:$VELODB_MYSQL_PORT / $DATABASE"
echo "window  $START_DATE .. $(python3 -c "import datetime; print(datetime.date.fromisoformat('$END_DATE') - datetime.timedelta(days=1))")  ($DAYS days, $SLOTS generator day slots)"
echo "stages  $STAGES  (run $RUN_ID, $PARALLEL job(s) at a time per table, load_parallelism $LOAD_PARALLELISM, duplicates ${DUP_PERMILLE}/1000)"

batches_of() {
  if [[ -n "$BATCH" ]]; then echo "$BATCH"; return; fi
  rs3 ls "$S3_RS3_ALIAS/$S3_BUCKET/$S3_PREFIX/$1/$RUN_ID/" | grep -o 'batch-[0-9]*' | sed 's/batch-//' | sort -u
}

ts() { date +%T; }
declare -A STATE DETAIL
# refresh_states: latest job (highest JobId) for every label of this run, in one SHOW LOAD
refresh_states() {
  local rows label state detail
  rows=$(sql "SHOW LOAD WHERE LABEL LIKE '%${START_MONTH}%${RUN_ID}%'" | awk -F'\t' '
    { if (!($2 in id) || $1 + 0 > id[$2] + 0) { id[$2] = $1; line[$2] = $2 "\t" $3 "\t" $4 " " $8 } }
    END { for (l in line) print line[l] }')
  STATE=(); DETAIL=()
  while IFS=$'\t' read -r label state detail; do
    [[ -n "$label" ]] || continue
    STATE[$label]=$state; DETAIL[$label]=$detail
  done <<< "$rows"
}

# run_pool: run the queued tasks (Q_LABEL / Q_TABLE / Q_BATCH / Q_WHERE) with at most PARALLEL at once
run_pool() {
  local n=${#Q_LABEL[@]} next=0 done=0 failed=0 last_report=0 i l s
  local -a running=()
  local -A started=() tries=()
  submit() { # index
    local i=$1
    load_stmt "${Q_TABLE[$i]}" "${Q_BATCH[$i]}" "${Q_LABEL[$i]}" "${Q_WHERE[$i]}" | db -D"$DATABASE"
    started[${Q_LABEL[$i]}]=$SECONDS
    tries[${Q_LABEL[$i]}]=$(( ${tries[${Q_LABEL[$i]}]:-0} + 1 ))
  }
  while :; do
    refresh_states
    # settle finished / cancelled jobs
    local -a still=()
    for i in "${running[@]}"; do
      l=${Q_LABEL[$i]}; s=${STATE[$l]:-}
      case "$s" in
        FINISHED)
          done=$((done + 1))
          echo "$(ts) done   $l in $(( (SECONDS - ${started[$l]}) / 60 ))m  [$done/$n]" ;;
        CANCELLED)
          if (( ${tries[$l]:-0} <= MAX_RETRIES )); then
            echo "$(ts) retry  $l (attempt $(( ${tries[$l]} + 1 ))): ${DETAIL[$l]}" >&2
            submit "$i"; still+=("$i")
          else
            failed=$((failed + 1)); done=$((done + 1))
            echo "$(ts) FAILED $l after ${tries[$l]} attempt(s): ${DETAIL[$l]}" >&2
          fi ;;
        "") echo "$(ts) warn   $l not visible yet" >&2; still+=("$i") ;;
        *) still+=("$i") ;;
      esac
    done
    running=("${still[@]}")
    # fill free slots
    while (( ${#running[@]} < PARALLEL && next < n )); do
      i=$next; next=$((next + 1)); l=${Q_LABEL[$i]}; s=${STATE[$l]:-}
      case "$s" in
        FINISHED) done=$((done + 1)); echo "$(ts) skip   $l (already finished)  [$done/$n]" ;;
        PENDING|ETL|LOADING|COMMITTED)
          started[$l]=$SECONDS; tries[$l]=1; running+=("$i"); echo "$(ts) adopt  $l ($s, submitted earlier)" ;;
        *) submit "$i"; running+=("$i"); echo "$(ts) submit $l" ;;
      esac
    done
    (( ${#running[@]} == 0 && next >= n )) && break
    if (( SECONDS - last_report >= 300 )); then
      last_report=$SECONDS
      echo "$(ts) status $done/$n done, ${#running[@]} running, $((n - next)) queued"
      for i in "${running[@]}"; do l=${Q_LABEL[$i]}; echo "$(ts)          $l ${STATE[$l]:-?} ${DETAIL[$l]:-}"; done
    fi
    sleep "$POLL"
  done
  (( failed == 0 )) || { echo "$(ts) $failed job(s) failed" >&2; exit 1; }
}

queue() { Q_LABEL+=("$1"); Q_TABLE+=("$2"); Q_BATCH+=("$3"); Q_WHERE+=("${4:-}"); }
dup_where="abs(murmur_hash3_32(call_id)) % 1000 < $DUP_PERMILLE"

for stage in $STAGES; do
  Q_LABEL=(); Q_TABLE=(); Q_BATCH=(); Q_WHERE=()
  t=${stage/dups/call_log}
  tag=$(sql "SELECT DATE_FORMAT(create_time, '%Y%m%d%H%i%s') FROM information_schema.tables WHERE table_schema = '$DATABASE' AND table_name = '$t'")
  [[ -n "$tag" ]] || { echo "error: table $DATABASE.$t does not exist; use --create" >&2; exit 1; }
  bs=$(batches_of "$t")
  [[ -n "$bs" ]] || { echo "no batch folders under s3://$S3_BUCKET/$S3_PREFIX/$t/$RUN_ID/" >&2; exit 1; }
  if [[ $stage != dups ]]; then
    for b in $bs; do queue "${t}_${START_MONTH}_${RUN_ID}_${tag}_b${b}" "$t" "$b"; done
  fi
  if [[ $t == call_log && "$DUP_PERMILLE" -gt 0 ]]; then
    for b in $bs; do queue "call_log_${START_MONTH}_${RUN_ID}_${tag}_b${b}_dup" call_log "$b" "$dup_where"; done
  fi
  echo "$(ts) === $stage: ${#Q_LABEL[@]} job(s), $PARALLEL at a time (table created $tag)"
  run_pool
  echo "$(ts) === $stage finished"
done

echo "$(ts) all stages finished"
for t in call_log data_stream_log; do
  echo "$t: $(sql "SELECT CONCAT(COUNT(*), ' rows, ', MIN(start_time), ' .. ', MAX(start_time)) FROM $t WHERE start_time >= '$START_DATE' AND start_time < '$END_DATE'")"
done
