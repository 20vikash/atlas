# Atlas WG Mesh benchmarks

Bare metal. 1 GbE uplink. WireGuard MTU `1420`. VM interface MTU `1380`.

> These results do not cover the NDP-based implementation. Its performance can differ.

## Throughput

Single TCP stream.

| Path                   | Throughput | Line rate |
| ---------------------- | ---------: | --------: |
| Raw 1 GbE              | 941 Mbit/s |      100% |
| Host-to-host WireGuard | 885 Mbit/s |       94% |
| Atlas VM-to-VM         | 847 Mbit/s |       90% |

Atlas delivers 95% of WireGuard throughput. The main cost is the 40-byte tunnel header and smaller VM MTU. BPF encapsulation and decapsulation add little overhead.

| Path      | Single stream | Four streams |
| --------- | ------------: | -----------: |
| WireGuard |    885 Mbit/s |   885 Mbit/s |
| Atlas     |    847 Mbit/s |   844 Mbit/s |

One stream saturates the 1 GbE link. More streams do not help. On faster links, throughput should track WireGuard until tunnel encryption becomes the limit.

## Packet rate

With 200-byte UDP packets, Atlas reached about 230,000 packets/s and 322 Mbit/s. This is the worst case for per-packet BPF work.
