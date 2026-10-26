#!/usr/bin/env bash
# Create ./venv with the Python deps from requirements.txt if it's missing or stale.
# Sourced or executed by the other scripts; prints the interpreter path.
set -euo pipefail
cd "$(dirname "$0")"
# onnxruntime ships wheels for CPython <= 3.14 only: when PYTHON is not set, prefer the newest supported python3.x.
if [ -z "${PYTHON:-}" ]; then
  for c in python3.14 python3.13 python3.12 python3.11 python3; do command -v "$c" >/dev/null && { PYTHON=$c; break; }; done
fi
PY="${PYTHON:-python3}"
if [ ! -x venv/bin/python ] || [ requirements.txt -nt venv/.installed ]; then
  echo "setting up venv/ ($($PY --version 2>&1)) ..." >&2
  [ -x venv/bin/python ] || "$PY" -m venv venv
  venv/bin/python -m pip install --quiet --upgrade pip >&2
  venv/bin/python -m pip install --quiet -r requirements.txt >&2
  touch venv/.installed
fi
echo "$(pwd)/venv/bin/python"
