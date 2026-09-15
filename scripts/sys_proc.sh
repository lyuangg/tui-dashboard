#!/bin/sh
# The processes using the most CPU → type: table. ps is taken in descending CPU order (-r),
# top N rows, tab-separated; N comes from the first argument and defaults to 7. Platform
# dependency: macOS (ps -Arcww, BSD-style flags).
#
# With N=7 the output is 1 header row + 7 process rows, which together with a border is
# exactly the 10-row panel in the example layout. For a longer list, ./scripts/sys_proc.sh 20.
#
# ps yields a result in one command, but %CPU is an average since process start and does
# not reflect "right now". Tracking an instantaneous hotspot requires top: its %CPU comes
# from differencing two samples, at the cost of waiting for one more sample (-l 2, about
# 2s), plus having to pick that table out of the "overview section + aligned process
# table" and convert it to a separator.
#
# Why tabs and not spaces: the COMMAND column carries spaces of its own
# ("Google Chrome He"), so splitting on whitespace would misalign every column after it.
printf 'pid\tcpu\tmem\tcomm\n'
ps -Arcww -o pid=,%cpu=,%mem=,comm= 2>/dev/null | head -n "${1:-7}" | awk '
{
    pid=$1; cpu=$2; mem=$3
    comm=$0; sub(/^[ \t]*[0-9]+[ \t]*[0-9.]+[ \t]*[0-9.]+[ \t]*/, "", comm)
    if (comm == "") comm="(unknown)"
    gsub(/[[:cntrl:]]/, "", comm)      # a tab is a control char, splitting this row into columns
    if (length(comm) > 70) comm=substr(comm,1,70) "…"
    printf "%s\t%.1f\t%.1f\t%s\n", pid, cpu, mem, comm
}'
