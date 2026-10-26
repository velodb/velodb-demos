#!/usr/bin/env bash
# faces.db -> export/faces-NNNN.jsonl (20,000 rows each) + export/people-0000.jsonl,
# with label_score (label consistency) and per-person centroid vectors computed.
# Offline: needs no Doris connection.
set -euo pipefail
cd "$(dirname "$0")"
PY=$(./ensure-venv.sh)
exec "$PY" face-export.py "$@"
