#!/usr/bin/env bash
# Run the Iggy connectors runtime (loads the Doris sink plugin). Edit the VeloDB
# credentials in ~/iggy-bench-connectors/connectors/doris.toml first.
set -euo pipefail
IGGY_SRC="${IGGY_SRC:-$HOME/iggy}"
export IGGY_CONNECTORS_CONFIG_PATH="${IGGY_CONNECTORS_CONFIG_PATH:-$HOME/iggy-bench-connectors/runtime.toml}"
echo "iggy-connectors: config=$IGGY_CONNECTORS_CONFIG_PATH"
exec "$IGGY_SRC/target/release/iggy-connectors"
