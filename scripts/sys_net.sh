#!/bin/sh
# Network up/down rate. Emits "key: value" per line (rx_kbs, tx_kbs, iface) → type: map.
# No arguments. Platform dependency: macOS (route, netstat -ib).
#
# Two netstat -ib samples are taken on the default route's interface (about 1 second apart),
# and the byte delta divided by the actual interval gives KB/s; when no default route
# resolves, en0 is used.
IF=$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')
[ -z "$IF" ] && IF=en0

sample() { # → "<ibytes> <obytes>" (Ibytes/Obytes of that interface's <Link> row, fields 7 and 10)
    netstat -ib -n 2>/dev/null | awk -v i="$IF" '$1==i && $3 ~ /^<Link/ {print $7, $10; exit}'
}
t1=$(date +%s)
r1=$(sample)
sleep 1
t2=$(date +%s)
r2=$(sample)
dt=$((t2 - t1)); [ "$dt" -lt 1 ] && dt=1

ia=$(printf '%s\n' "$r1" | awk '{print $1}')
oa=$(printf '%s\n' "$r1" | awk '{print $2}')
ib=$(printf '%s\n' "$r2" | awk '{print $1}')
ob=$(printf '%s\n' "$r2" | awk '{print $2}')

rx_kbs=$(awk -v a="$ia" -v b="$ib" -v dt="$dt" 'BEGIN{
    if (a=="") a=0; if (b=="") b=0
    d=b-a; if (d<0) d=0; printf "%.1f", d/dt/1024
}')
tx_kbs=$(awk -v a="$oa" -v b="$ob" -v dt="$dt" 'BEGIN{
    if (a=="") a=0; if (b=="") b=0
    d=b-a; if (d<0) d=0; printf "%.1f", d/dt/1024
}')

printf 'rx_kbs: %s\n' "$rx_kbs"
printf 'tx_kbs: %s\n' "$tx_kbs"
printf 'iface: %s\n' "$IF"
