#!/usr/bin/env bash
# Idempotent: downloads the IMDB-WIKI faces dataset (~9.7 GB, 20 parquet shards)
# and embeds every face into faces.db. Safe to re-run at any time -- finished
# shards are skipped, so an interrupted run resumes. Offline: needs no Doris connection.
#
#   ./run-embedding.sh                          # download + embed everything (~30 min embed on an M3 Max)
#   ./run-embedding.sh --max-shards 2           # quick trial: first 2 shards (~51k photos)
#   ./run-embedding.sh --max-shards 1 --limit 20   # smoke test: first 20 photos of shard 0 (~490 MB download)
#   WORKERS=4 ./run-embedding.sh                # fewer worker processes (default 6)
set -euo pipefail
cd "$(dirname "$0")"
PY=$(./ensure-venv.sh)
./ensure-models.sh
exec nice -n 10 "$PY" face-gen.py --download --workers "${WORKERS:-6}" "$@"
