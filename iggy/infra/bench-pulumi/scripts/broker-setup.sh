#!/usr/bin/env bash
# Broker-only setup: mount the local NVMe instance store at /mnt/nvme for the
# broker data dir (low-latency, consistent writes; ephemeral is fine). Idempotent:
# only formats a blank device, never re-wipes an already-mounted one.
set -euxo pipefail

DEV=$(lsblk -dpno NAME,MODEL | awk '/Instance Storage/ {print $1; exit}')
if [ -z "$DEV" ]; then
  echo "broker-setup.sh: no instance-store NVMe found, skipping mount"
else
  if ! mountpoint -q /mnt/nvme; then
    # Only mkfs a device with no filesystem, so a re-run can't wipe live data.
    if ! sudo blkid "$DEV" >/dev/null 2>&1; then
      sudo mkfs.xfs -f "$DEV"
    fi
    sudo mkdir -p /mnt/nvme
    sudo mount "$DEV" /mnt/nvme
    sudo chmod 1777 /mnt/nvme
  fi
  df -h /mnt/nvme
fi

# Readiness marker (also records the io_uring restriction state).
io_uring_state=$(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null \
  || echo 'n/a (kernel <6.6: unrestricted)')
echo "io_uring_disabled=${io_uring_state}" | sudo tee /etc/iggy-bench-ready
echo "broker-setup.sh: done"
