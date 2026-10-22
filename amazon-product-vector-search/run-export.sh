#!/usr/bin/env bash
# Export products.db (metadata + vectors) to newline-delimited JSON chunks in
# export/ for Doris stream load. Re-runnable: the export/ directory is rebuilt.
#
#   ./run-export.sh                  # export/products-0001.jsonl, ... + manifest.json
#   ./run-export.sh --chunk 10000    # any product-export.py flag passes through
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -s products.db ]; then
    echo "products.db not found -- run ./run-embedding.sh first" >&2
    exit 1
fi

. ./env.sh   # sets PYTHON (the project venv, created on first use)
exec "$PYTHON" product-export.py "$@"
