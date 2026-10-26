#!/usr/bin/env bash
# Build and run the taxi hyper-search web UI (Go). Reads .env / the environment (see env.example) and mrt_stations.csv.
#   ./run-web-server.sh                 # http://localhost:9727
#   LISTEN=:8080 ./run-web-server.sh
#   ./run-web-server.sh --dev-static webapp/static   # live-edit the frontend without rebuilding
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
cd webapp
go build -o taxi-search-web .
exec ./taxi-search-web --listen "${LISTEN:-:9727}" --env "$ENV_FILE" --stations ../mrt_stations.csv "$@"
