# Gateways

A gateway virtual machine carries traffic that the mesh does not own.

The mesh moves packets to the gateway, but it does not configure the gateway software.

## Capabilities

| Capability | Result |
| --- | --- |
| Own a prefix | The host routes the prefix to the virtual machine and answers neighbor requests. |
| Carry traffic | The gateway can send a client source address into the mesh. |
| Route a destination | One virtual machine sends the destination range to a gateway address. |

Atlas permits the gateway role only for a privileged tenant-0 virtual machine.

## Route selection

The virtual machine hook checks mesh destinations before gateway routes.

For an external destination, the longest configured prefix wins.

A destination without a route stays with the normal Linux path.

## Prerequisites

Make sure that these items are ready:

- The provider routes a public IPv6 prefix to the public network of the Metal host.
- Neighbor Discovery Protocol requests for the prefix can reach the public interface.
- The gateway VM uses tenant `0` and has the privileged flag.
- The gateway image contains `ip`, `sysctl`, and `nft`.

The examples use `2001:db8:100::/64`, which is a documentation prefix. Replace it with the prefix from your provider.

## Configure Atlas

Use Atlas Desk for all gateway and route settings:

1. Open the tenant-0 gateway VM.
2. Select **Dangerous Actions > Grant Privilege**.
3. Select **Actions > Make Network Gateway**.
4. Open the public IPv6 prefix in **Metal Server IP Address**.
5. Select **Actions > Attach to Virtual Machine**.
6. Select the gateway VM and click **Attach**.

Atlas now assigns the public prefix to the gateway VM.

## Use a public address on the gateway

Use this option when the public service runs on the gateway VM.

Add one address from the public prefix to the gateway interface:

```sh
sudo ip -6 address add 2001:db8:100::10/128 dev eth0
```

Add the default route through the Metal host:

```sh
sudo ip -6 route replace default via fe80::1 dev eth0
```

The host answers public neighbor requests and sends the prefix to the gateway VM.

The gateway role lets the VM send packets that have a public source address.

Store these interface settings in the network configuration of the gateway image.

## Send a public address to another VM

This example maps `2001:db8:100::10` to the mesh address `fdaa:1:10:20::5`.

```text
Internet -> Metal public interface -> gateway VM
         -> destination NAT -> WG Mesh -> application VM
         <- reverse NAT <- gateway route <- reply
```

Configure the application VM in Atlas Desk:

1. Open the application VM.
2. Select **Actions > Edit Gateway Routes**.
3. Add a route with destination `2000::/3`.
4. Select the gateway VM in the **Gateway** field.
5. Click **Save**.

This setting is enough in Atlas. Do not run an `atlas-wg-mesh` command on the host.

Enable IPv6 forwarding in the gateway VM:

```sh
sudo tee /etc/sysctl.d/90-atlas-ipv6-gateway.conf >/dev/null <<'EOF'
net.ipv6.conf.all.forwarding = 1
EOF
sudo sysctl -p /etc/sysctl.d/90-atlas-ipv6-gateway.conf
```

Add this `nftables` configuration to the firewall configuration of the gateway VM:

```text
table ip6 atlas_gateway {
  chain prerouting {
    type nat hook prerouting priority dstnat; policy accept;
    ip6 daddr 2001:db8:100::10 dnat to fdaa:1:10:20::5
  }

  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip6 saddr fdaa:1:10:20::5 snat to 2001:db8:100::10
  }

  chain forward {
    type filter hook forward priority filter; policy drop;
    ct state established,related counter accept
    ip6 daddr fdaa:1:10:20::5 counter accept
    ip6 saddr fdaa:1:10:20::5 counter accept
  }
}
```

Load the configuration with the method for your Linux distribution.

For a temporary check, save the rules in `/tmp/atlas-gateway.nft` and run this command:

```sh
sudo nft -f /tmp/atlas-gateway.nft
```

The destination network address translation rule sends new public connections to the application VM.

The source network address translation rule gives outbound application traffic the same public address.

The gateway firewall permits forwarded traffic only for this application VM.

## Check the gateway

Run these commands inside the gateway VM:

```sh
sysctl net.ipv6.conf.all.forwarding
ip -6 address show dev eth0
ip -6 route show
sudo nft list table ip6 atlas_gateway
```

Run these checks from a system outside the public prefix:

```sh
ping -6 2001:db8:100::10
curl -6 'http://[2001:db8:100::10]/'
```

The application VM firewall must permit the service and Internet Control Message Protocol version 6 traffic.

If inbound traffic fails, check the provider route, public neighbor entry, gateway counters, and application firewall in that order.

## Limits

| Limit | Reason |
| --- | --- |
| One owner per prefix | Public NDP must have one destination interface. |

Read [Design](design.md#gateways) for the packet behavior.
