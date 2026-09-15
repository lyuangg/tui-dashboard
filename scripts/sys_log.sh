#!/bin/sh
# Unified system log → type: logs. Taken from /usr/bin/log stream (macOS), which is a live
# event stream rather than a file, so all parsing happens in this script. No arguments; the
# LOG_TAIL environment variable controls how many lines are kept (default 20). Platform
# dependency: macOS.
#
# Each output line is "<timestamp> <level> <process>: <message>"; awk does three things:
#   1. Drop the milliseconds (stream gives "2026-09-14 20:10:42.969"; trimming to seconds
#      is what matches the program's timestamp layout).
#   2. Translate the level abbreviations as-is: Df default / I info / Er error / F fault /
#      Db debug → INFO/ERROR/DEBUG (normalizeLevel recognizes only these names, anything
#      else counts as INFO).
#   3. Keep only the last LOG_TAIL lines.
#
# --timeout makes stream exit on its own when the time is up (about 3 seconds); --predicate
# filters out the few processes that flood the log hardest.
# The first frame is empty: stream only gives events "from now on", so empty output = no
# new content this frame, and the program keeps the previous value.
LOG=/usr/bin/log
[ -x "$LOG" ] || exit 0

"$LOG" stream --style compact --timeout 3s \
    --predicate 'process != "syspolicyd" AND process != "kernel" AND process != "dasd"' \
    2>/dev/null |
    tail -n "${LOG_TAIL:-20}" |
    awk '
$1 ~ /^[12][0-9][0-9][0-9]-/ {          # no recognizable timestamp (headers, hints): dropped
    ts = $1 " " $2
    sub(/\.[0-9]+$/, "", ts)            # drop the milliseconds
    lvl = "INFO"
    if ($3 ~ /^(Er|F)/) lvl = "ERROR"
    else if ($3 ~ /^Db/) lvl = "DEBUG"
    proc = $4
    sub(/:[0-9a-f]*\]$/, "]", proc)     # keep only pid from [pid:tid] in the process name
    msg = ""
    for (i = 5; i <= NF; i++) msg = msg (msg == "" ? "" : " ") $i
    sub(/^\[[^]]*\] */, "", msg)        # strip the [subsystem:category] prefix
    printf "%s %s %s: %s\n", ts, lvl, proc, msg
}'
