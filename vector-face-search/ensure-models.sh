#!/usr/bin/env bash
# Download the InsightFace buffalo_l models (SCRFD detector + ArcFace R50, ~280 MB) into ./models if missing.
# Called by run-embedding.sh, run-web-server.sh and run-search.sh.
set -euo pipefail
cd "$(dirname "$0")"
if [ ! -s models/det_10g.onnx ] || [ ! -s models/w600k_r50.onnx ]; then
  echo "downloading InsightFace buffalo_l models (~280 MB) ..." >&2
  mkdir -p models
  curl -fsSL -o models/buffalo_l.zip https://github.com/deepinsight/insightface/releases/download/v0.7/buffalo_l.zip
  (cd models && unzip -o -q buffalo_l.zip det_10g.onnx w600k_r50.onnx && rm buffalo_l.zip)
fi
