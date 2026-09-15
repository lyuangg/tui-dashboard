#!/bin/sh
# Disk usage. Two outputs, selected by the first argument; each feeds a source that
# declares a different type:
#
#   ./scripts/sys_disk.sh          → "key: value" per line → type: map
#   ./scripts/sys_disk.sh mounts   → a tab-separated table → type: table
#
# Both read the data the same way (the same df | awk); only the final presentation
# differs. Platform dependency: macOS (df, the APFS volume layout).
#
# Keeps only the root "/", the user data volume /System/Volumes/Data, and external disks
# under /Volumes; excludes devfs and the read-only system snapshots, so the screen is not
# filled with trivial volumes.
#
# Mind APFS: every volume in one container reports the same total capacity and shares the
# same available space, so adding up each volume's used and capacity separately
# double-counts the container (e.g. 43%). The overall used_pct is therefore grouped by
# physical disk (the device name with the sN partition suffix removed) and computed as
# (capacity - available)/capacity, which is what comes close to the real usage.
df -k 2>/dev/null | awk -v mode="${1:-summary}" '
BEGIN { n=0 }
function keep(m) {
    return (m == "/" || m == "/System/Volumes/Data" || index(m, "/Volumes/") == 1)
}
function diskdev(d) {          # /dev/disk3s3s1 -> disk3 (the physical disk)
    sub(/^\/dev\//, "", d)
    while (d ~ /s[0-9]+$/) sub(/s[0-9]+$/, "", d)
    return d
}
$1 ~ /^\/dev\// && keep($NF) {
    fs[n]=$1; sz[n]=$2; us[n]=$3
    pct[n]=$5; sub(/%/, "", pct[n])
    mnt[n]=$NF; n++
    d = diskdev($1)
    if ($2 > cap[d]) cap[d] = $2                   # max capacity per physical disk (APFS container)
    if (!(d in free) || $4 < free[d]) free[d] = $4 # shared available space = min avail per volume
}
END {
    if (mode == "mounts") {          # tab-separated: mount points may hold spaces, split on tabs
        print "fs\tmount\tsize_gb\tused_gb\tuse_pct"
        for (i=0; i<n; i++)
            printf "%s\t%s\t%.1f\t%.1f\t%.0f\n", fs[i], mnt[i], sz[i]/1048576, us[i]/1048576, pct[i]
        exit
    }
    for (d in cap) { used += cap[d] - free[d]; total += cap[d]; avail += free[d] }
    printf "used_pct: %.0f\n", (total > 0 ? 100*used/total : 0)
    printf "total_gb: %.1f\n", total/1048576
    printf "free_gb: %.1f\n", avail/1048576
}'
