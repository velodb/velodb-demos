#!/usr/bin/env bash
# Log search web UI: http://localhost:9273/logsearch/
#   ./runWebServer.sh
#   ADDR=:8080 ./runWebServer.sh
# Connection + table come from .env (ENV_FILE=<path> for another file) and the environment;
# VELODB_* and tuning variables already exported win over the file (see env.example).
set -euo pipefail
cd "$(dirname "$0")"

exec go run . server --env "${ENV_FILE:-.env}" --addr "${ADDR:-:9273}" "$@"
