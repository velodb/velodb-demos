#!/usr/bin/env bash
# Generate log events and stream-load them into Doris (creates the table from create.sql if missing).
#   ./runBackFill.sh                                   # ROWS_PER_SECOND / TOTAL_ROWS from .env or the environment
#   ROWS_PER_SECOND=0 TOTAL_ROWS=1000000 ./runBackFill.sh   # unthrottled one-off load
#   THREADS=16 ./runBackFill.sh --inspect
# Connection + table come from .env (ENV_FILE=<path> for another file) and the environment;
# VELODB_* and tuning variables already exported win over the file (see env.example).
set -euo pipefail
cd "$(dirname "$0")"

exec go run . --env "${ENV_FILE:-.env}" --threads "${THREADS:-10}" "$@"
