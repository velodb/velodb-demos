#!/usr/bin/env bash
# Run the Iggy server: bind all interfaces (default is localhost-only, which the
# producer VM can't reach) and store data on the NVMe.
set -euo pipefail
IGGY_SRC="${IGGY_SRC:-$HOME/iggy}"
export IGGY_TCP_ADDRESS="${IGGY_TCP_ADDRESS:-0.0.0.0:8090}"
export IGGY_SYSTEM_PATH="${IGGY_SYSTEM_PATH:-/mnt/nvme/local_data}"
echo "iggy-server: TCP=$IGGY_TCP_ADDRESS data=$IGGY_SYSTEM_PATH"
exec "$IGGY_SRC/target/release/iggy-server"
