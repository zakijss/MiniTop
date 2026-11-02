#!/bin/sh
# Installed with MiniTop into /mnt/SDCARD/.tmp_update/startup/.
# At boot, sets the clock from verified HTTPS servers in the background
# (Onion's own time sync no longer works). Turn it off in MiniTop > Startup.
B=/mnt/SDCARD/App/MiniTop/clocksync
[ -x "$B" ] && setsid nohup "$B" > /tmp/clocksync.log 2>&1 &
exit 0
