#!/usr/bin/env bash
# Start the face-search web UI (Python: face detection + ArcFace on the Apple GPU,
# ANN queries on Doris). Reads .env / the environment (see env.example).
#
#   ./run-web-server.sh                  # http://localhost:8091
#   PORT=9000 ./run-web-server.sh
#   ./run-web-server.sh --host 0.0.0.0   # listen on all interfaces (default 127.0.0.1)
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
PY=$(./ensure-venv.sh)
./ensure-models.sh
exec "$PY" face-server.py --env "$ENV_FILE" "$@"
