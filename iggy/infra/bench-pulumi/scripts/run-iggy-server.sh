#!/usr/bin/env bash
# Run the Iggy server: bind all interfaces (default is localhost-only, which the
# producer VM can't reach), store data on the NVMe, and let mimalloc draw from
# the reserved hugepages (see broker-setup.sh: vm.nr_hugepages).
set -euo pipefail
IGGY_SRC="${IGGY_SRC:-$HOME/iggy}"
export IGGY_TCP_ADDRESS="${IGGY_TCP_ADDRESS:-0.0.0.0:8090}"
export IGGY_SYSTEM_PATH="${IGGY_SYSTEM_PATH:-/mnt/nvme/local_data}"
# Modern Iggy generates a random root password on first boot; pin it so the
# producer/connector can sign in (also wipe IGGY_SYSTEM_PATH if it was set before).
export IGGY_ROOT_USERNAME="${IGGY_ROOT_USERNAME:-iggy}"
export IGGY_ROOT_PASSWORD="${IGGY_ROOT_PASSWORD:-iggybench}"
# mimalloc large/huge OS pages: pairs with vm.nr_hugepages for a steadier tail.
export MIMALLOC_ALLOW_LARGE_OS_PAGES="${MIMALLOC_ALLOW_LARGE_OS_PAGES:-1}"
export MIMALLOC_RESERVE_HUGE_OS_PAGES="${MIMALLOC_RESERVE_HUGE_OS_PAGES:-2048}"
echo "iggy-server: TCP=$IGGY_TCP_ADDRESS data=$IGGY_SYSTEM_PATH mimalloc-hugepages=on"
exec "$IGGY_SRC/target/release/iggy-server"
