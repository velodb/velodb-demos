#!/usr/bin/env bash
# Start the hybrid-search web UI: the Python embedding service (query text ->
# BGE vector, on the Apple GPU) and the Go server (UI + /api/search on Doris).
#
#   ./run-ui.sh                 # http://localhost:9121
#   LISTEN=:9000 ./run-ui.sh    # different port
#
# Needs products.db (for the model/prefix metadata), the Go toolchain, and the VELODB_* connection
# from the environment or .env (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
require_velodb

EMBED_PORT="${EMBED_PORT:-7234}"
export EMBED_URL="http://127.0.0.1:$EMBED_PORT"
export LISTEN="${LISTEN:-:9121}"

if [ ! -s products.db ]; then
    echo "products.db not found -- run ./run-embedding.sh first" >&2
    exit 1
fi

echo "==> starting embedding service on $EMBED_URL"
"$PYTHON" embed-server.py --port "$EMBED_PORT" &
EMBED_PID=$!
trap 'kill $EMBED_PID 2>/dev/null || true' EXIT INT TERM
for _ in $(seq 1 120); do
    curl -sf "$EMBED_URL/health" >/dev/null 2>&1 && break
    kill -0 $EMBED_PID 2>/dev/null || { echo "embedding service exited" >&2; exit 1; }
    sleep 0.5
done
curl -sf "$EMBED_URL/health" >/dev/null || { echo "embedding service did not become ready" >&2; exit 1; }

echo "==> building and starting UI on $LISTEN"
(cd ui && go build -o ../.ui-server .)
./.ui-server
