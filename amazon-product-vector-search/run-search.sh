#!/usr/bin/env bash
# Interactive brute-force similarity search over products.db.
# Embeds what you type with the same model, computes cosine similarity
# against every product vector, and prints the top matches (best first).
#
#   ./run-search.sh                       # interactive prompt
#   ./run-search.sh "coffee maker"        # one-shot query
#   ./run-search.sh -k 10 "soft rug"      # top 10 instead of 20
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -s products.db ]; then
    echo "products.db not found -- run ./run-embedding.sh first" >&2
    exit 1
fi

. ./env.sh   # sets PYTHON (the project venv, created on first use)
exec "$PYTHON" product-search.py "$@"
