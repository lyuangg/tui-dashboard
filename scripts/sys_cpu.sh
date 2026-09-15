#!/bin/sh
# Whole-machine CPU usage, load, and process count. Two outputs, selected by the first
# argument:
#
#   (default)  "key: value" per line            → type: map
#   history    "<Unix seconds> <CPU%>" per line → type: timeseries
#
# CPU% comes from iostat rather than top. Both read the same kernel counters and report the
# same us/sy/id trio, but top costs about 0.5s of CPU per sample on this platform (~1.2s for
# `top -l 2`), which at a 2s interval is most of a core; iostat takes the same sample in
# about 7ms. iostat's first frame is the average since boot (too high), so it is dropped,
# exactly as the first top frame used to be. When iostat fails the script degrades to
# summing ps, same as before.
#
# -n 0 is required: without it iostat also prints per-disk columns, whose count follows the
# number of attached disks, so us/sy/id would not sit at a fixed column.
#
# Why map uses -c 2 and history -c 4: map needs only one current value and gets it from two
# frames (about 1 second); history wants a curve, and after dropping the first frame it uses
# the rest — four frames give three points (about 3 seconds).
#
# The process count still needs ps: iostat does not report it. `-o pid=` asks for the pid
# column alone; the default listing reads every process's argument block instead and costs
# about four times as much for the same number (it also emits a header row, which would be
# counted as a process).
#
# Platform dependency: macOS (iostat, sysctl, ps).
if [ "${1:-}" = "history" ]; then
    start=$(date +%s)
    iostat -c 4 -n 0 2>/dev/null | awk -v t="$start" '
    {
        # Header lines carry no numbers; the first data line is the since-boot average.
        if (NF < 3 || $1 !~ /^[0-9.]+$/ || $3 !~ /^[0-9.]+$/) next
        if (!dropped++) next
        pct = 100 - $3
        if (pct < 0) pct = 0
        # The timestamp is derived from the sampling instant: frame 1 is dropped, so the nth
        # point lands at start+n seconds (error < 1s)
        printf "%d %.1f\n", t + n + 1, pct
        n++
    }'
    exit 0
fi

# Last numeric row = the current 1-second sample (the row before it is the since-boot one)
cpu_line=$(iostat -c 2 -n 0 2>/dev/null | awk '
    NF >= 3 && $1 ~ /^[0-9.]+$/ && $2 ~ /^[0-9.]+$/ && $3 ~ /^[0-9.]+$/ { last = $1 " " $2 " " $3 }
    END { print last }')

user=$(printf '%s' "$cpu_line" | awk '{print $1}')
sysp=$(printf '%s' "$cpu_line" | awk '{print $2}')
idle=$(printf '%s' "$cpu_line" | awk '{print $3}')

if [ -z "$idle" ]; then
    cpu=$(ps -A -o %cpu= 2>/dev/null | awk '{s+=$1} END {printf "%.1f", s}')
else
    cpu=$(printf '%s' "$idle" | awk '{v=100-$1; if(v<0)v=0; printf "%.1f", v}')
fi
[ -z "$user" ] && user=0
[ -z "$sysp" ] && sysp=0

load=$(sysctl -n vm.loadavg 2>/dev/null | tr -d '{}')   # " 5.32 3.75 4.58 "
l1=$(printf '%s' "$load" | awk '{print $1}')
l5=$(printf '%s' "$load" | awk '{print $2}')
l15=$(printf '%s' "$load" | awk '{print $3}')
[ -z "$l1" ] && l1=0
[ -z "$l5" ] && l5=0
[ -z "$l15" ] && l15=0

procs=$(ps -A -o pid= 2>/dev/null | wc -l | tr -d ' ')
[ -z "$procs" ] && procs=0

printf 'cpu: %s\n' "$cpu"
printf 'user: %.1f\n' "$user"
printf 'sys: %.1f\n' "$sysp"
printf 'load1: %.2f\n' "$l1"
printf 'load5: %.2f\n' "$l5"
printf 'load15: %.2f\n' "$l15"
printf 'procs: %d\n' "$procs"
