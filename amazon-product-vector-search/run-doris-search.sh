#!/usr/bin/env bash
# Vector / hybrid search on Doris: embeds what you type with the same model and
# runs an ANN top-k query served by the HNSW index on $VELODB_DATABASE.products.
#
#   ./run-doris-search.sh                                  # interactive prompt
#   ./run-doris-search.sh "coffee maker"                   # one-shot query
#   ./run-doris-search.sh -k 5 "soft rug" --where "country = 'US' AND weight_lb < 10"
#   ./run-doris-search.sh "espresso" --where "product_description MATCH_ANY 'stainless steel'"
#   ./run-doris-search.sh "espresso" --exact               # brute force, no index
#   ./run-doris-search.sh "espresso" --show-sql            # print the SQL
#
# Connection: VELODB_* from the environment or .env (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
require_velodb

if [ ! -s products.db ]; then
    echo "products.db not found -- run ./run-embedding.sh first" >&2
    exit 1
fi

exec "$PYTHON" product-search-doris.py "$@"
