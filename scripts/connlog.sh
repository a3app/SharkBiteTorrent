#!/usr/bin/env bash
# Logs the running Shark BiteTorrent app's internet connections (anything not
# on this computer) every half second, then summarizes. Use it while clicking
# through the real app; leakcheck covers the engine without the UI.
# Windows (Git Bash).   scripts/connlog.sh [seconds]
set -euo pipefail
secs="${1:-120}"
pid=$(tasklist //FI "IMAGENAME eq SharkBiteTorrent.exe" //FO CSV | tail -n +2 | head -1 | cut -d'"' -f4)
[ -n "$pid" ] || { echo "Shark BiteTorrent isn't running" >&2; exit 1; }
log=$(mktemp)
echo "Watching PID $pid for ${secs}s; use the app now."
end=$(( $(date +%s) + secs ))
while [ "$(date +%s)" -lt "$end" ]; do
  netstat -ano -p TCP   | awk -v p="$pid" '$NF==p && $3 !~ /^(127\.0\.0\.1|0\.0\.0\.0):/ {print $3, $4}' >> "$log"
  netstat -ano -p TCPv6 | awk -v p="$pid" '$NF==p && $3 !~ /^\[(::1|::)\]:/ {print $3, $4}' >> "$log"
  sleep 0.5
done
if [ -s "$log" ]; then
  echo "FAIL: internet connections outside the tunnel:"; sort "$log" | uniq -c | sort -rn
  rm -f "$log"; exit 1
fi
rm -f "$log"
echo "PASS: no internet connections outside the tunnel"
