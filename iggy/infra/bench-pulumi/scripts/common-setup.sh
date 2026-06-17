#!/usr/bin/env bash
# Base setup for both benchmark VMs. Idempotent: safe to re-run.
#   - io_uring-friendly limits (memlock for ring memory, high nofile)
#   - C + Rust toolchain so iggy-server/connectors build on-box (target-cpu=native)
# Works whether invoked as root (cloud-init user-data) or as ec2-user (ssh+sudo).
set -euxo pipefail

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

# --- Build toolchain (dnf install is a no-op if already present) ----------
# Includes librdkafka build deps (libcurl/sasl/zlib/zstd/lz4) so the rdkafka
# crate's vendored librdkafka build (iggy-kafka-bench Kafka client) succeeds.
sudo dnf -y install gcc gcc-c++ make git cmake openssl-devel pkgconf-pkg-config htop \
  libcurl-devel cyrus-sasl-devel zlib-devel libzstd-devel lz4-devel

# --- Rust for ec2-user (skip if cargo already there) ----------------------
if ! sudo -u ec2-user test -x /home/ec2-user/.cargo/bin/cargo; then
  sudo -u ec2-user bash -lc \
    "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"
fi

echo "common-setup.sh: done"
