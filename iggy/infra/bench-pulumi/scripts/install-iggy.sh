#!/usr/bin/env bash
# Build Iggy from source with a CPU-targeted release build (raw binary, never a
# container). Role-aware via IGGY_BUILD:
#   broker   -> iggy-server + iggy-connectors + doris sink plugin (default)
#   producer -> iggy-bench (load generator)
#   all      -> everything
# Idempotent: clones if absent, otherwise checks out IGGY_REF and rebuilds.
set -euxo pipefail

IGGY_REPO="${IGGY_REPO:-https://github.com/apache/iggy.git}"
IGGY_REF="${IGGY_REF:-master}"
IGGY_SRC="${IGGY_SRC:-$HOME/iggy}"
TARGET_CPU="${TARGET_CPU:-native}"
IGGY_BUILD="${IGGY_BUILD:-broker}"

source "$HOME/.cargo/env"

# Iggy build deps (libhwloc-dev for thread-per-core pinning, libudev-dev for the
# -ludev link) are installed by common-setup.sh; nothing extra to install here.

if [ ! -d "$IGGY_SRC/.git" ]; then
  git clone "$IGGY_REPO" "$IGGY_SRC"
fi
cd "$IGGY_SRC"
git fetch --all --tags --prune
git checkout "$IGGY_REF"
git pull --ff-only origin "$IGGY_REF" 2>/dev/null || true

# Patch: on Linux, iggy sets compio thread_pool_limit(0) to force io_uring-only,
# but the SO_REUSEPORT socket bind (thread-per-core) has no io_uring opcode and
# falls back to the (now empty) blocking pool -> panic "thread pool is needed but
# no worker thread is running". Give it a small pool; io_uring-capable ops (the
# hot path) are unaffected. Masked upstream because the line is #[cfg]-disabled
# on macOS aarch64, where the team develops. Idempotent.
EXECUTOR="core/server_common/src/executor.rs"
if grep -q "thread_pool_limit(0)" "$EXECUTOR"; then
  sed -i 's/thread_pool_limit(0)/thread_pool_limit(256)/' "$EXECUTOR"
  echo "patched $EXECUTOR: thread_pool_limit(0) -> 256"
fi

export RUSTFLAGS="-C target-cpu=$TARGET_CPU"
case "$IGGY_BUILD" in
  broker)
    cargo build --release --bin iggy-server --bin iggy-connectors
    cargo build --release -p iggy_connector_doris_sink
    ;;
  producer)
    cargo build --release --bin iggy-bench
    ;;
  all)
    cargo build --release --bin iggy-server --bin iggy-connectors --bin iggy-bench
    cargo build --release -p iggy_connector_doris_sink
    ;;
  *) echo "unknown IGGY_BUILD=$IGGY_BUILD (use broker|producer|all)"; exit 1 ;;
esac

# Broker: prep data dirs on NVMe + connector config templates (fill VeloDB creds).
if [ "$IGGY_BUILD" = "broker" ] || [ "$IGGY_BUILD" = "all" ]; then
  sudo mkdir -p /mnt/nvme/local_data /mnt/nvme/connectors_state
  sudo chown -R "$USER" /mnt/nvme/local_data /mnt/nvme/connectors_state
  CONN_DIR="$HOME/iggy-bench-connectors"
  mkdir -p "$CONN_DIR/connectors"

  cat > "$CONN_DIR/runtime.toml" <<EOF
[iggy]
address = "127.0.0.1:8090"
username = "iggy"
password = "iggybench"

[state]
path = "/mnt/nvme/connectors_state"

[connectors]
config_type = "local"
config_dir = "$CONN_DIR/connectors"

[http]
enabled = false
EOF

  cat > "$CONN_DIR/connectors/doris.toml" <<EOF
type = "sink"
key = "doris"
enabled = true
version = 0
name = "Doris sink"
path = "$IGGY_SRC/target/release/libiggy_connector_doris_sink"
plugin_config_format = "toml"

[[streams]]
stream = "bench"
topics = ["clickstream"]
schema = "json"
# Throughput config. For the latency run use batch_length = 1000 / poll = "100ms".
batch_length = 20000
poll_interval = "500ms"
consumer_group = "doris_sink"

[plugin_config]
# VeloDB Cloud Stream Load. Point at the BE port :8460 directly; the connector
# does not follow the FE :8080 -> BE :8460 redirect (whose Location carries inline
# credentials). Host = cluster load balancer from `velocli cloud warehouse connections`.
fe_url = "http://CHANGE-ME.elb.us-east-1.amazonaws.com:8460"
database = "bench"
table = "events"
username = "admin@CHANGE-ME-cluster"
password = "CHANGE-ME"
label_prefix = "iggy"
batch_size = 20000
timeout = "60s"
# group_commit = "async_mode"   # needs the connector enhancement
EOF
  echo "connector config at $CONN_DIR (edit VeloDB creds in connectors/doris.toml)"
fi

ls -la "$IGGY_SRC/target/release/" | grep -E "iggy-server|iggy-connectors|iggy-bench|libiggy_connector_doris" || true
echo "install-iggy.sh: done (IGGY_BUILD=$IGGY_BUILD, ref=$IGGY_REF)"
