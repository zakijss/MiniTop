#!/bin/sh
# MiniTop: see and end running programs, and manage startup items.
cd "$(dirname "$0")"
# Keep the log small: start afresh once it passes 64 KB (the previous one
# is kept as minitop.log.old).
sz=$( { wc -c < minitop.log; } 2>/dev/null); [ $((${sz:-0})) -gt 65536 ] && mv -f minitop.log minitop.log.old
./minitop 2>> minitop.log
