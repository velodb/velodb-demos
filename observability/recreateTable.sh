#!/usr/bin/env bash
# DROP and re-CREATE the demo table from create.sql. Deletes all loaded rows!
#   ./recreateTable.sh
#   VELODB_TABLE=log_events_test ./recreateTable.sh
# Connection + table come from .env (ENV_FILE=<path> for another file) and the environment;
# VELODB_* and tuning variables already exported win over the file (see env.example).
set -euo pipefail
cd "$(dirname "$0")"

exec go run . recreate-table --env "${ENV_FILE:-.env}" "$@"
