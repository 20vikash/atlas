#!/usr/bin/env bash

set -eu

netplan_file=/etc/netplan/51-private-network.yaml
link_file=/etc/systemd/network/70-$DEVICE.link
network_file=/etc/systemd/network/71-$DEVICE.network

current=""
for path in /sys/class/net/*; do
	if [ "$(cat "$path/address")" = "$MAC_ADDRESS" ]; then
		current=$(basename "$path")
		break
	fi
done

if [ -z "$current" ]; then
	echo "no network interface has MAC address $MAC_ADDRESS" >&2
	exit 1
fi

# AWS gives no stable guest device name, so pin the mesh interface by MAC address.
mkdir -p /etc/systemd/network
umask 022
cat > "$link_file" <<EOF
[Match]
MACAddress=$MAC_ADDRESS

[Link]
Name=$DEVICE
MTUBytes=$MTU
EOF

if command -v netplan >/dev/null; then
	mkdir -p /etc/netplan
	umask 077
	cat > "$netplan_file" <<EOF
network:
  version: 2
  ethernets:
    $DEVICE:
      match:
        macaddress: $MAC_ADDRESS
      set-name: $DEVICE
      dhcp4: false
      dhcp6: false
      mtu: $MTU
      addresses:
        - $ADDRESS
      routes:
        - to: 224.0.0.0/4
          scope: link
EOF
	chmod 600 "$netplan_file"
	netplan generate
elif systemctl is-active --quiet systemd-networkd; then
	umask 022
	cat > "$network_file" <<EOF
[Match]
Name=$DEVICE

[Network]
Address=$ADDRESS

[Route]
Destination=224.0.0.0/4
Scope=link
EOF
else
	echo "netplan or systemd-networkd is required to configure the private network" >&2
	exit 1
fi

if [ "$current" != "$DEVICE" ]; then
	ip link set dev "$current" down
	ip link set dev "$current" name "$DEVICE"
fi
ip link set dev "$DEVICE" mtu "$MTU" up
ip addr replace "$ADDRESS" dev "$DEVICE"
ip route replace 224.0.0.0/4 dev "$DEVICE"

ip -4 -o addr show dev "$DEVICE" scope global | awk 'NR == 1 {print $4}' | cut -d/ -f1
