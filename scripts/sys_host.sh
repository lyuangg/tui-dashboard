#!/bin/sh
# Host information: hostname, IP, OS version, CPU, memory, uptime, emitted as multiple
# lines of text aligned by label and shown directly by a text widget. No arguments.
# Platform dependency: macOS (sw_vers, sysctl, ipconfig, route); off macOS or with no
# default route it falls back to `hostname -I`, and when no IP resolves at all "-" is the
# placeholder.
osver=$(sw_vers -productVersion 2>/dev/null)
arch=$(uname -m 2>/dev/null)
host=$(hostname)
cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null)
cores=$(sysctl -n hw.ncpu 2>/dev/null)
mem=$(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%.0f", $1/1073741824}')
up=$(uptime 2>/dev/null | sed -E 's/^.* up +//; s/,.*$//; s/ +/ /g')

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

printf '主机  %s(%s)\nIP    %s\n系统  macOS %s\nCPU   %s (%s 核)\n内存  %d GB\n运行  %s\n' \
    "$host" "$arch" "$ip" "$osver" "$cpu" "$cores" "$mem" "$up"
