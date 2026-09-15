#!/bin/sh
# System memory: used ratio, available, wired, compressor, total capacity. Emits
# "key: value" per line → type: map. No arguments. Platform dependency: macOS (sysctl,
# vm_stat).
#
# The page size is read from the vm_stat header rather than hardcoded. The usage ratio is
# defined as: available = free + speculative + inactive (reclaimable cache), and
# used% = 100*(total - available)/total.
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

used_pct=$(awk -v t="$total" -v ps="$psize" -v f="$free_p" -v s="$spec_p" -v i="$inact_p" 'BEGIN{
    if (f=="") f=0; if (s=="") s=0; if (i=="") i=0
    avail=(f+s+i)*ps
    v=(t>0) ? 100*(t-avail)/t : 0
    if (v<0) v=0; if (v>100) v=100
    printf "%.1f", v
}')
free_mb=$(awk -v ps="$psize" -v n="$free_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
wired_mb=$(awk -v ps="$psize" -v n="$wire_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
comp_mb=$(awk -v ps="$psize" -v n="$comp_p" 'BEGIN{ if(n=="")n=0; printf "%.0f", n*ps/1048576 }')
total_mb=$(awk -v t="$total" 'BEGIN{ printf "%.0f", t/1048576 }')

printf 'used_pct: %s\n' "$used_pct"
printf 'free_mb: %s\n' "$free_mb"
printf 'wired_mb: %s\n' "$wired_mb"
printf 'compressed_mb: %s\n' "$comp_mb"
printf 'total_mb: %s\n' "$total_mb"
