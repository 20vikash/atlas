# IPv6 router component specification

[Root specification](../../SPEC.md)

## Purpose

Each IPv6 router maps one public IPv6 block to the WG Mesh addresses of one region. See the [overview](./).

## Layout

```text
bpf/          tc eBPF program: address.h, packet.h, router.c
nftables/     Forward filter template
systemd/      Service unit
setup.sh      Installer
```

## Interfaces

- `setup.sh` reads `REGION_ID` and `PUBLIC_IPV6_PREFIX`.
- The host part layout in `bpf/address.h` must match `atlas/service/core/ipv6_router/address.py`.

## Ownership

Atlas owns the router VM, the block, and the gateway routes of each VM. The router owns only the translation.

## Validation

Compile the program with a test `config.h`:

```sh
clang -O2 -g -Wall -Werror -target bpf \
  -I<directory with config.h> -I/usr/include/$(uname -m)-linux-gnu \
  -c bpf/router.c -o router.bpf.o
```
