#!/usr/bin/env bash
# Command-line face search: ./run-search.sh photo.jpg [--top 10] [--where "gender = 'female'"]
# Reads .env / the environment (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
PY=$(./ensure-venv.sh)
./ensure-models.sh
exec "$PY" face-search-doris.py --env "$ENV_FILE" "$@"
