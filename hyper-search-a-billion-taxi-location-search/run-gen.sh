#!/usr/bin/env bash
# Build (release) and run the generator. Extra args are passed through, e.g.:
#   ./run-gen.sh plan
#   ./run-gen.sh --days 495 --rows 1000000000 plan
#   ./run-gen.sh                       # generate everything per generator/generator.toml
#   ./run-gen.sh generate --from-day 0 --to-day 10
set -euo pipefail
cd "$(dirname "$0")/generator"
cargo build --release --quiet
exec ./target/release/taxi-gen --config generator.toml "$@"
