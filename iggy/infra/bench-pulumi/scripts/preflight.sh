#!/usr/bin/env bash
# io_uring + limits preflight. Run on the broker before any Iggy benchmark to
# confirm the VM won't silently under-run Iggy. Read-only.
set -uo pipefail

echo "kernel:            $(uname -r)"
echo "io_uring_disabled: $(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null \
  || echo 'n/a (kernel <6.6 = unrestricted)')"
echo "memlock (ulimit):  $(ulimit -l)"
echo "nofile (ulimit):   $(ulimit -n)"
echo -n "nvme:              "; df -h /mnt/nvme 2>/dev/null | tail -1 || echo "/mnt/nvme NOT mounted"

maj=$(uname -r | cut -d. -f1)
min=$(uname -r | cut -d. -f2)
if [ "$maj" -gt 5 ] || { [ "$maj" -eq 5 ] && [ "$min" -ge 19 ]; }; then
  echo "OK: kernel >= 5.19 — io_uring COOP_TASKRUN/TASKRUN_FLAG available, Iggy shards will start"
else
  echo "FAIL: kernel < 5.19 — Iggy shard executor cannot init io_uring (no epoll fallback)"
  exit 1
fi
