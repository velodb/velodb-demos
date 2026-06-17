#!/usr/bin/env bash
# Base setup for both benchmark VMs (Ubuntu 24.04). Idempotent: safe to re-run.
#   - io_uring-friendly limits (memlock for ring memory, high nofile)
#   - C + Rust toolchain so iggy-server/connectors build on-box (target-cpu=native)
# Works whether invoked as root (cloud-init user-data) or as ubuntu (ssh+sudo).
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive

# --- Limits ---------------------------------------------------------------
sudo tee /etc/security/limits.d/99-iggy.conf >/dev/null <<'EOF'
* soft memlock unlimited
* hard memlock unlimited
* soft nofile 1048576
* hard nofile 1048576
EOF

sudo mkdir -p /etc/systemd/system.conf.d
sudo tee /etc/systemd/system.conf.d/99-iggy.conf >/dev/null <<'EOF'
[Manager]
DefaultLimitMEMLOCK=infinity
DefaultLimitNOFILE=1048576
EOF

# --- Build toolchain (apt install is a no-op if already present) ----------
# librdkafka build deps (libcurl/sasl/zlib/zstd/lz4) for the rdkafka crate
# (iggy-kafka-bench Kafka client); libhwloc-dev for Iggy's topology-aware core
# pinning; libudev-dev because iggy-server links -ludev on Linux.
sudo apt-get update -y
sudo apt-get install -y \
  build-essential git cmake pkg-config libssl-dev htop \
  libcurl4-openssl-dev libsasl2-dev zlib1g-dev libzstd-dev liblz4-dev \
  libhwloc-dev libudev-dev

# --- Rust for the ubuntu user (skip if cargo already there) ---------------
if ! sudo -u ubuntu test -x /home/ubuntu/.cargo/bin/cargo; then
  sudo -u ubuntu bash -lc \
    "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"
fi

echo "common-setup.sh: done"
