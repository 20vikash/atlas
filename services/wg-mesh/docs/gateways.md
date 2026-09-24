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
4. Open the public IPv6 prefix in **Public IP Pool**.
5. Create an **IPv6 Router Server** for this pool.
6. Wait until the router is Active.

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

A gateway can provide a public path to another VM. The application VM sends traffic for the configured destination through an Atlas gateway route.

```text
Inbound:  Internet -> Metal host -> gateway VM -> WG Mesh -> application VM
Outbound: application VM -> gateway route -> gateway VM -> Metal host -> Internet
```

The Metal host sends inbound traffic for the public prefix to the gateway VM. The gateway sends each packet to the application VM through WG Mesh. WG Mesh delivers an inbound packet only when the application VM has a gateway route to its source. Other inbound packets are dropped.

The gateway owns packet forwarding, address translation, and firewall policy. WG Mesh only transports packets between the gateway and the application VM.

This path is transparent to the application VM. The application VM only needs the Atlas gateway route. The [Atlas IPv6 router](../../ipv6-router/README.md) provides this path for all VMs in a region.

## Check the gateway

Run these commands inside the gateway VM:

```sh
sysctl net.ipv6.conf.all.forwarding
ip -6 address show dev eth0
ip -6 route show
```

Use the gateway software tools to check its forwarding and firewall state.

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
