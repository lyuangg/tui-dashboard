#!/bin/sh
# System memory: used ratio and used amount, free, wired, compressor, total capacity. Emits
# "key: value" per line → type: map. No arguments. Platform dependency: macOS (sysctl,
# vm_stat).
#
# The page size is read from the vm_stat header rather than hardcoded. The usage ratio is
# defined as: available = free + speculative + inactive (reclaimable cache), and
# used% = 100*(total - available)/total. used_gb is that same quantity as an amount, in GB
# (the unit a panel shows it in); it does not complement free_mb, which counts free pages
# alone, without the reclaimable cache.
total=$(sysctl -n hw.memsize 2>/dev/null); [ -z "$total" ] && total=0

info=$(vm_stat 2>/dev/null)
psize=$(printf '%s\n' "$info" | head -n 1 | sed -E 's/^.*page size of ([0-9]+) bytes.*$/\1/')
if [ -z "$psize" ] || [ "$psize" -eq 0 ] 2>/dev/null; then
    psize=4096
fi

pg() { # pg <vm_stat label> → page count (integer)
    printf '%s\n' "$info" | grep "$1" | sed -E 's/^.*: *([0-9]+)\.$/\1/' | head -n 1
}
free_p=$(pg 'Pages free:')
spec_p=$(pg 'Pages speculative:')
inact_p=$(pg 'Pages inactive:')
wire_p=$(pg 'Pages wired down:')
comp_p=$(pg 'Pages occupied by compressor:')

# The ratio and the amount come out of one awk run: both rest on the same definition of
# availability, and computing them apart would let the two drift.
used=$(awk -v t="$total" -v ps="$psize" -v f="$free_p" -v s="$spec_p" -v i="$inact_p" 'BEGIN{
    if (f=="") f=0; if (s=="") s=0; if (i=="") i=0
    avail=(f+s+i)*ps
    u=(t>avail) ? t-avail : 0          # u and t are in bytes, and u <= t, so v stays in [0,100]
    v=(t>0) ? 100*u/t : 0
    printf "%.1f %.1f", v, u/1073741824
}')
used_pct=${used% *}
used_gb=${used#* }
free_mb=$(awk -v ps="$psize" -v n="$free_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
wired_mb=$(awk -v ps="$psize" -v n="$wire_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
comp_mb=$(awk -v ps="$psize" -v n="$comp_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
total_mb=$(awk -v t="$total" 'BEGIN{ printf "%.0f", t/1048576 }')

printf 'used_pct: %s\n' "$used_pct"
printf 'used_gb: %s\n' "$used_gb"
printf 'free_mb: %s\n' "$free_mb"
printf 'wired_mb: %s\n' "$wired_mb"
printf 'compressed_mb: %s\n' "$comp_mb"
printf 'total_mb: %s\n' "$total_mb"
