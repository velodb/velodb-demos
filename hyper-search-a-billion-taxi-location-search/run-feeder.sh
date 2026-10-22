#!/usr/bin/env bash
# Build and run the realtime feeder (Go): makes up the gap since the last ping in Doris at the
# measured rate, then keeps streaming pings in realtime through the Doris stream-load SDK.
#   ./run-feeder.sh                     # make up + realtime (Ctrl-C to stop; rerun fills the new gap)
#   ./run-feeder.sh --makeup-only       # just catch up and exit
#   ./run-feeder.sh --dry-run           # simulate without loading
#   ./run-feeder.sh --help
# Connection + table come from .env / the environment (see env.example).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
cd feeder
go build -o taxi-feeder .
exec ./taxi-feeder --env "$ENV_FILE" --stations ../mrt_stations.csv "$@"
