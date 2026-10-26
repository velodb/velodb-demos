#!/usr/bin/env bash
# Same loader as runBackFill.sh, throttled to a realtime trickle (default 100 rows/s).
#   ./runRealtime.sh
#   ROWS_PER_SECOND=1000 ./runRealtime.sh
# Connection + table come from .env (ENV_FILE=<path> for another file) and the environment;
# VELODB_* and tuning variables already exported win over the file (see env.example).
set -euo pipefail
cd "$(dirname "$0")"

export ROWS_PER_SECOND="${ROWS_PER_SECOND:-100}"
exec go run . --env "${ENV_FILE:-.env}" --threads "${THREADS:-10}" "$@"
