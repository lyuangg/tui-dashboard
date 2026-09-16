#!/bin/sh
# Demo data for the heatmap panel: one "<Unix seconds> <value>" per line, one line per day of
# the last 365 days → type: timeseries. No arguments.
#
# The values are not measurements: each one is a pure arithmetic hash of its date, so a given
# day always reports the same value however often the script runs, and the grid looks the same
# from one run to the next. Weekends are scaled down and a few days fall to zero, so the panel
# shows every level of the ramp. Platform: macOS (date -v).
set -u

days=365
i=$((days - 1))
while [ "$i" -ge 0 ]; do
    stamp=$(date -v-"${i}"d '+%s %w') # this time of day, i days ago (calendar arithmetic)
    ts=${stamp% *}
    wd=${stamp#* }                    # 0 Sunday .. 6 Saturday
    seed=$((ts / 86400))              # the day the sample belongs to, as a hash seed
    h=$(( (seed * 1103515245 + 12345) / 65536 % 1000 ))
    v=$((h % 100))
    if [ "$v" -lt 8 ]; then
        v=0                           # an idle day: the panel's no-data glyph
    fi
    if [ "$wd" -eq 0 ] || [ "$wd" -eq 6 ]; then
        v=$((v * 35 / 100))           # weekends are quieter
    fi
    printf '%s %s\n' "$ts" "$v"
    i=$((i - 1))
done
