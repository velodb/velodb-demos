#!/usr/bin/env bash
# Broker-only setup (Ubuntu 24.04). Idempotent.
#   1. Mainline kernel 7.0.10  (Iggy's preferred fresh io_uring kernel)
#   2. io_uring + latency OS tuning (sysctls, hugepages)
#   3. Local NVMe instance store mounted at /mnt/nvme for the broker data dir
# Re-run after the post-kernel reboot: step 1 self-skips once running on 7.0.10.
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive

KERNEL_VER="${KERNEL_VER:-7.0.10}"
KERNEL_TAG="${KERNEL_TAG:-070010}"   # mainline package version stamp (no dots)

# --- 1. Mainline kernel ----------------------------------------------------
# Iggy prefers a fresh kernel: newer io_uring scheduling reached 1,232 MB/s here,
# ~3% over 6.1 with an identical tail. Ubuntu's mainline build ships prebuilt
# .debs; `apt-get install ./*.deb` resolves the maintainer deps (e.g.
# wireless-regdb) that bare dpkg trips on. A reboot is required afterwards.
if ! uname -r | grep -q "$KERNEL_TAG"; then
  base="https://kernel.ubuntu.com/mainline/v${KERNEL_VER}/amd64"
  tmp="$(mktemp -d)"; cd "$tmp"
  # Scrape the directory index for the generic amd64 image/modules/headers .debs.
  mapfile -t debs < <(curl -fsSL "$base/" \
    | grep -oE "linux-[a-z-]*${KERNEL_TAG}[^\"]*_(amd64|all)\.deb" \
    | grep -v lowlatency | sort -u)
  for d in "${debs[@]}"; do curl -fsSLO "$base/$d"; done
  sudo apt-get install -y ./linux-headers-*_all.deb \
    ./linux-headers-*-generic_*_amd64.deb \
    ./linux-modules-*-generic_*_amd64.deb \
    ./linux-image-unsigned-*-generic_*_amd64.deb
  sudo update-grub
  cd /; rm -rf "$tmp"
  echo "broker-setup.sh: kernel ${KERNEL_VER} installed. REBOOT, then re-run this script."
fi

# --- 2. io_uring + latency OS tuning --------------------------------------
# Disclosed in the README; applied before any run and checked by preflight.sh.
sudo tee /etc/sysctl.d/99-iggy.conf >/dev/null <<'EOF'
# io_uring must be unrestricted (kernel >= 6.6)
kernel.io_uring_disabled = 0
# keep hot pages resident; avoid swap stalls in the latency tail
vm.swappiness = 10
# bound dirty-page buildup so NVMe writeback stays smooth (no flush spikes)
vm.dirty_ratio = 30
vm.dirty_background_ratio = 10
# hugepages for the allocator: 2048 x 2 MB ~= 4 GB (mimalloc large/huge pages)
vm.nr_hugepages = 2048
EOF
sudo sysctl --system >/dev/null

# --- 3. Local NVMe instance store -----------------------------------------
# Only formats a blank device, never re-wipes an already-mounted one.
DEV=$(lsblk -dpno NAME,MODEL | awk '/Instance Storage/ {print $1; exit}')
if [ -z "$DEV" ]; then
  echo "broker-setup.sh: no instance-store NVMe found, skipping mount"
else
  if ! mountpoint -q /mnt/nvme; then
    if ! sudo blkid "$DEV" >/dev/null 2>&1; then
      sudo mkfs.xfs -f "$DEV"
    fi
    sudo mkdir -p /mnt/nvme
    sudo mount "$DEV" /mnt/nvme
    sudo chmod 1777 /mnt/nvme
  fi
  df -h /mnt/nvme
fi

# --- Readiness marker (records OS, kernel, and the tuned values) ----------
{
  . /etc/os-release 2>/dev/null && echo "os=$PRETTY_NAME"
  echo "kernel=$(uname -r)"
  echo "io_uring_disabled=$(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null || echo 'n/a')"
  echo "swappiness=$(cat /proc/sys/vm/swappiness)"
  echo "nr_hugepages=$(cat /proc/sys/vm/nr_hugepages)"
} | sudo tee /etc/iggy-bench-ready
echo "broker-setup.sh: done"
