#!/bin/sh
# Build and run the simulator + dashboard. Extra args go to `finsim run` (e.g. ./run.sh -reset).
set -e
cd "$(dirname "$0")"
go build -o finsim . 
[ -d stocks ] && [ "$(ls stocks/*.toml 2>/dev/null | wc -l)" -gt 0 ] || ./finsim gen-stocks
exec ./finsim run "$@"
