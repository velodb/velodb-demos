#!/usr/bin/env bash
# Backfill 7 days of historical partitions for the OTEL tables in
# demo_observability. Required when OTEL_TIMEZONE is set to anything other
# than UTC, because the OTEL exporter only creates partitions for "today UTC"
# + "tomorrow UTC" but PT/CET/etc.-formatted datetime literals fall on
# *yesterday's* UTC date.
#
# See dist/docs/TIMEZONE.md for the why.
set -euo pipefail

DORIS_USER="${DORIS_USER:-root}"
DORIS_PASSWORD="${DORIS_PASSWORD:-VeloDB@demo123}"
DORIS_DB="${DORIS_DB:-demo_observability}"
DAYS_BACK="${DAYS_BACK:-7}"

TABLES=(
  otel_logs
  otel_traces
  otel_metrics_sum
  otel_metrics_gauge
  otel_metrics_histogram
  otel_metrics_summary
  otel_metrics_exponential_histogram
  otel_traces_graph
)

mysql_run() {
  docker compose exec -T doris-fe mysql -h127.0.0.1 -P9030 \
    -u"$DORIS_USER" -p"$DORIS_PASSWORD" -D "$DORIS_DB" -e "$1" 2>&1 \
    | grep -v "^mysql: \[Warning\]" || true
}

echo "[backfill] computing partition dates ($DAYS_BACK days back from today UTC)"
TODAY=$(date -u +%Y-%m-%d)

for tbl in "${TABLES[@]}"; do
  echo
  echo "== $tbl =="
  mysql_run "ALTER TABLE $tbl SET ('dynamic_partition.enable'='false')"
  for i in $(seq 1 "$DAYS_BACK"); do
    d=$(date -u -v-"$i"d +%Y-%m-%d 2>/dev/null || date -u -d "$i days ago" +%Y-%m-%d)
    next=$(date -u -v-"$((i-1))"d +%Y-%m-%d 2>/dev/null || date -u -d "$((i-1)) days ago" +%Y-%m-%d)
    p=$(echo "$d" | tr -d -)
    sql="ALTER TABLE $tbl ADD PARTITION p$p VALUES [('$d 00:00:00'), ('$next 00:00:00'))"
    mysql_run "$sql" | grep -iE "error|already" | head -1 || true
  done
  mysql_run "ALTER TABLE $tbl SET ('dynamic_partition.enable'='true', 'dynamic_partition.history_partition_num'='$DAYS_BACK')"
done

echo
echo "[backfill] partitions on otel_logs:"
mysql_run "SHOW PARTITIONS FROM otel_logs" | awk -F'\t' 'NR>1 {print "   " $2}'
echo
echo "[backfill] done. Doris App's 'Last 1 hour' should now find rows."
