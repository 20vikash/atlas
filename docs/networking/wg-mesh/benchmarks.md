# WG Mesh benchmarks

These results come from one test host:

| Item | Value |
| --- | --- |
| Host | Scaleway `EM-A116X-SSD` bare metal in `pl-waw-3`, Intel Xeon E3 1220, kernel `7.0.0` |
| Network | 1 GbE private network, WireGuard MTU `1440`, VM interface MTU `1380` |
| Guest | Firecracker, 2 vCPU, 2 GiB, Ubuntu 24.04 |
| NDP mode | Unicast. The mode changes only discovery, so throughput is the same in both modes. |

## Throughput

TCP, `iperf3`, 10 seconds. A single-stream value is the median of 3 runs.

| Path                     | Single stream | Four streams | Line rate |
| ------------------------ | ------------: | -----------: | --------: |
| Raw private network      |    939 Mbit/s |   939 Mbit/s |      100% |
| Host-to-host WireGuard   |    886 Mbit/s |   887 Mbit/s |       94% |
| Atlas VM-to-VM, 2 hosts  |    847 Mbit/s |   834 Mbit/s |       90% |
| Atlas VM-to-VM, one host |   14.3 Gbit/s |            - |         - |

Atlas delivers 96% of WireGuard throughput between hosts. The main cost is the 40-byte tunnel header and the smaller VM MTU. The reverse direction measured 820 Mbit/s.

One stream saturates the 1 GbE link. More streams do not help. On faster links, throughput should track WireGuard until tunnel encryption becomes the limit.

Traffic between VMs on one host does not use WireGuard. Linux delivers it through the VM host route.

## Packet rate

200-byte UDP packets at the highest rate `iperf3` can send.

| Path                     | Received     | Loss  |
| ------------------------ | -----------: | ----: |
| Host-to-host WireGuard   |  221,000 pps |  2.4% |
| Atlas VM-to-VM, 2 hosts  |  113,000 pps | 28.5% |
| Atlas VM-to-VM, one host |  102,000 pps | 54.8% |

The same-host rate is close to the cross-host rate, so the guest network path limits small packets, not the BPF programs.

## Latency

`ping`, 200 packets, 10 ms interval.

| Path                     | Average | Minimum |
| ------------------------ | ------: | ------: |
| Raw private network      | 0.28 ms | 0.14 ms |
| Host-to-host WireGuard   | 0.76 ms | 0.28 ms |
| Atlas VM-to-VM, 2 hosts  | 1.75 ms | 0.82 ms |
| Atlas VM-to-VM, one host | 0.92 ms | 0.27 ms |

## Discovery and moves

Network time only, in unicast and multicast mode. A test endpoint in a network namespace is registered with `atlas-wg-mesh vm sync` while a peer VM on a third host sends a ping every 10 ms. The time starts when `vm sync` returns.

| Event                                    | Network time                                      |
| ---------------------------------------- | ------------------------------------------------- |
| NDP solicitation to advertisement        | 0.3 ms                                            |
| First packet to an unknown VM            | One packet lost. The second packet arrives.       |
| New VM reachable after registration      | 4.7 ms unicast, 5.1 ms multicast                  |
| VM moved to another host                 | 1.5 ms to 10.5 ms in 15 moves, within one ping    |
| Public IPv6 block moved to another host  | Traffic returns when the provider attaches the block to the new host |

The destination host announces a moved VM on the uplink, so peers learn the new host without a NOT_HERE round trip. The old host forwards traffic for a moved block through the mesh until the provider router uses the new host. See the [gateway design](index.md#gateways).
