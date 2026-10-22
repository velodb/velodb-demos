#!/bin/sh
# Full reset of the demo on the deploy host: stop finsim, drop + recreate every table/MV/view in Doris,
# start finsim again (it seeds a fresh book because there is nothing to resume).
# Usage: sudo ./reset.sh   (asks for confirmation; pass -y to skip)
set -e
D=/opt/processmaster/dropin/finsim
export PMCTL_SOCK=/opt/processmaster/processmaster.sock
PMCTL="/opt/processmaster/pmctl"
if [ "$1" != "-y" ]; then
  printf 'This DROPS every demo table, MV and view in Doris, recreates them, and restarts finsim with a fresh book. Continue? [y/N] '
  read ans; [ "$ans" = "y" ] || { echo "aborted"; exit 1; }
fi
echo "stopping finsim..."; "$PMCTL" stop finsim
sleep 3
echo "resetting Doris..."; sudo -u service "$D/finsim" reset -dir "$D"
echo "starting finsim..."; "$PMCTL" start finsim
sleep 5; "$PMCTL" status finsim
tail -n 5 "$D/logs/stderr.log"
