#!/usr/bin/env bash
# Create ./venv with the Python deps from requirements.txt if it's missing or stale, then print the interpreter path.
# The venv is git-ignored and disposable: `rm -rf venv` and the next script rebuilds it (torch is ~1 GB to download).
#   PYTHON=python3.12 ./ensure-venv.sh      # pick the interpreter for a new venv
set -euo pipefail
cd "$(dirname "$0")"
if [ -z "${PYTHON:-}" ]; then
  # torch has no wheels for brand-new Python releases yet, so prefer the newest supported one
  for PYTHON in python3.14 python3.13 python3.12 python3.11 python3; do command -v "$PYTHON" >/dev/null && break; done
fi
if [ ! -x venv/bin/python ] || [ requirements.txt -nt venv/.installed ]; then
  echo "setting up venv/ ($("$PYTHON" --version 2>&1)), this downloads torch the first time ..." >&2
  [ -x venv/bin/python ] || "$PYTHON" -m venv venv
  venv/bin/python -m pip install --quiet --upgrade pip >&2
  venv/bin/python -m pip install --quiet -r requirements.txt >&2
  touch venv/.installed
fi
echo "$(pwd)/venv/bin/python"
