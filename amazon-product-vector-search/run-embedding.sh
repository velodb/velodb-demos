#!/usr/bin/env bash
# Idempotent: downloads the ABO data, selects products and embeds every
# product into products.db (metadata + vectors). Safe to re-run at any time -- cached downloads,
# an existing product selection and already-embedded products are all skipped, so a
# second run on a finished dataset is a no-op and an interrupted run resumes.
#
#   ./run-embedding.sh                    # 100000 products, Apple GPU (MPS) if available
#   ./run-embedding.sh --count 1000000    # any product-gen.py flag passes through
#   ./run-embedding.sh --device cpu       # force CPU; THREADS caps its threads (default 6)
set -euo pipefail
cd "$(dirname "$0")"

THREADS="${THREADS:-6}"        # CPU fallback only: cap threads so the box doesn't overheat
BATCH_SIZE="${BATCH_SIZE:-128}"

. ./env.sh   # sets PYTHON (the project venv, created on first use)

exec nice -n 10 "$PYTHON" product-gen.py --threads "$THREADS" --batch-size "$BATCH_SIZE" "$@"
