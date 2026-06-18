#!/usr/bin/env bash
# io_uring + limits + tuning preflight. Run on the broker before any Iggy
# benchmark to confirm the VM won't silently under-run Iggy. Read-only.
set -uo pipefail

echo "os:                $(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
echo "kernel:            $(uname -r)"
echo "io_uring_disabled: $(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null \
  || echo 'n/a (kernel <6.6 = unrestricted)')"
echo "memlock (ulimit):  $(ulimit -l)"
echo "nofile (ulimit):   $(ulimit -n)"
echo "swappiness:        $(cat /proc/sys/vm/swappiness)"
echo "dirty_ratio/bg:    $(cat /proc/sys/vm/dirty_ratio)/$(cat /proc/sys/vm/dirty_background_ratio)"
echo "nr_hugepages:      $(cat /proc/sys/vm/nr_hugepages) x $(awk '/Hugepagesize/{print $2$3}' /proc/meminfo)"
echo -n "nvme:              "; df -h /mnt/nvme 2>/dev/null | tail -1 || echo "/mnt/nvme NOT mounted"

maj=$(uname -r | cut -d. -f1)
min=$(uname -r | cut -d. -f2)
if [ "$maj" -gt 5 ] || { [ "$maj" -eq 5 ] && [ "$min" -ge 19 ]; }; then
  echo "OK: kernel >= 5.19, io_uring COOP_TASKRUN/TASKRUN_FLAG available, Iggy shards will start"
  if [ "$maj" -lt 7 ]; then
    echo "NOTE: Iggy prefers a fresher kernel (7.0.10 used here); older kernels run ~3% slower"
  fi
else
  echo "FAIL: kernel < 5.19, Iggy shard executor cannot init io_uring (no epoll fallback)"
  exit 1
fi
