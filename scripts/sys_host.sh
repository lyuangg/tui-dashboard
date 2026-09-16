#!/bin/sh
# Host information: hostname, IP, OS version, CPU, memory, disk space, uptime, emitted as
# multiple lines of text aligned by label and shown directly by a text widget. No arguments.
# Platform dependency: macOS (sw_vers, sysctl, ipconfig, route); off macOS or with no
# default route it falls back to `hostname -I`, and when no IP resolves at all "-" is the
# placeholder. The disk line uses df alone, so it works off macOS too.
osver=$(sw_vers -productVersion 2>/dev/null)
arch=$(uname -m 2>/dev/null)
host=$(hostname)
cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null)
cores=$(sysctl -n hw.ncpu 2>/dev/null)
mem=$(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%.0f", $1/1073741824}')
up=$(uptime 2>/dev/null | sed -E 's/^.* up +//; s/,.*$//; s/ +/ /g')

# Free space on the boot volume, as free/total GB. Every volume of an APFS container reports
# the container's own capacity and the space it shares, so the root volume's figures already
# are the whole disk's — free space stays honest even though the system snapshot also lives
# here. df -k counts KiB, hence the 1G divisor.
disk=$(df -k / 2>/dev/null | awk 'NR==2 {printf "%.0f/%.0f GB", $4/1048576, $2/1048576}')
if [ -n "$disk" ]; then
    disk="$disk 可用"
else
    disk="-"                              # df failed → placeholder, not a bogus 0/0 GB
fi

# The IP is taken from the default route's outgoing interface (the same way sys_net.sh does
# it), so with several NICs the vmnet/docker virtual ports are not picked up; the interface
# name is shown alongside, making it clear which NIC the traffic goes through.
iface=$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')
ip=$(ipconfig getifaddr "$iface" 2>/dev/null)
if [ -z "$ip" ]; then                     # not macOS or no default route → the generic fallback
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    iface=""                              # the generic form yields no interface name, only the IP
fi
if [ -n "$ip" ] && [ -n "$iface" ]; then
    ip="$ip($iface)"
elif [ -z "$ip" ]; then
    ip="-"                                # nothing resolves (offline) → placeholder, not blank
fi

printf '主机  %s(%s)\nIP    %s\n系统  macOS %s\nCPU   %s (%s 核)\n内存  %d GB\n磁盘  %s\n运行  %s\n' \
    "$host" "$arch" "$ip" "$osver" "$cpu" "$cores" "$mem" "$disk" "$up"
