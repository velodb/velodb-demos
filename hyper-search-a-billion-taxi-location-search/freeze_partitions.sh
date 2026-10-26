#!/usr/bin/env bash
# Freeze the partitions of an already-loaded table, without reloading anything:
#   ALTER TABLE ... SET ("dynamic_partition.enable" = "false")
# stops the dynamic-partition scheduler, so old day partitions are no longer dropped and future ones no
# longer pre-created. (Tables made by an older create_table_doris.sh also have AUTO PARTITION; that only
# creates a partition when a row arrives for a day that has none, which does not happen without the feeder.)
#
#   ./freeze_partitions.sh        # disable dynamic partitioning and show the partition range
# Safe to rerun; undo with: ALTER TABLE <db>.<table> SET ("dynamic_partition.enable" = "true")
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
PY=$(./ensure-venv.sh)
sql() { "$PY" doris_sql.py --env "$ENV_FILE" -q "$@"; }

if sql -G -e "SHOW CREATE TABLE ${DATABASE}.${TABLE}" | grep -q '"dynamic_partition.enable" = "true"'; then
  sql -e "ALTER TABLE ${DATABASE}.${TABLE} SET (\"dynamic_partition.enable\" = \"false\")"
  echo "${DATABASE}.${TABLE}: dynamic partitioning disabled"
else
  echo "${DATABASE}.${TABLE}: dynamic partitioning already off"
fi
sql -e "SELECT count(*) AS partitions, min(PARTITION_NAME) AS first, max(PARTITION_NAME) AS last FROM information_schema.partitions WHERE table_schema='${DATABASE}' AND table_name='${TABLE}'"
sql -e "SELECT count(*) AS rows_total, min(ts) AS oldest, max(ts) AS newest FROM ${DATABASE}.${TABLE}"
