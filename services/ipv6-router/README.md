# Atlas IPv6 router

A gateway VM program that gives VMs public IPv6 addresses from one block by rewriting addresses between the block and the WG Mesh.

- How it works: [IPv6 router](../../docs/networking/ipv6-router.md)
- Code contract: [SPEC.md](SPEC.md)
- Install by hand: `REGION_ID=1 PUBLIC_IPV6_PREFIX=2001:db8:1:2:3::/80 ./setup.sh`

Atlas IPv6 router uses the [AGPL-3.0 license](../../license.txt).
