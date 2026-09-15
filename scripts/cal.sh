#!/bin/sh
# The current month's calendar, today in reverse video.
#
# cal highlights today only on a terminal, so this re-emits the highlight out of cal's own
# output: \033[7m...\033[27m around today's cell, sliced from the fixed 3-column grid.
#
# Output: 8 lines of at most 20 display columns, for a panel with width: 24. Platform: macOS.
set -u

DAY="$(date +%d)"

cal | awk -v day="$DAY" '
    { sub(/[[:space:]]+$/, "") }        # strip trailing spaces
    NR <= 2 { print; next }             # the month and weekday lines hold no date cells
    {
        out = ""
        n = length($0)                  # per-character is fine: from here the lines are pure ASCII
        for (i = 1; i <= n; i += 3) {
            seg  = substr($0, i, 3)     # 3 columns = a 2-column date cell + a 1-column gap
            cell = substr(seg, 1, 2)
            rest = substr(seg, 3)
            v = cell; gsub(/ /, "", v)
            if (v != "" && v + 0 == day + 0) cell = "\033[7m" cell "\033[27m"
            out = out cell rest
        }
        print out
    }
'
