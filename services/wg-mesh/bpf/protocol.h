/* SPDX-License-Identifier: AGPL-3.0 */
/*
 * Atlas WG Mesh protocol definitions.
 *
 * This file contains the values that must match on every Atlas WG Mesh host.
 * Changing atlas_msg changes the on-wire protocol.
 */

#ifndef ATLAS_PROTOCOL_H
#define ATLAS_PROTOCOL_H

#include <linux/bpf.h>
#include <linux/icmpv6.h>
#include <linux/if_ether.h>
#include <linux/in.h>
#include <linux/ipv6.h>
#include <linux/pkt_cls.h>

#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

/*
 * VM address layout: fd aa | region 16 | tenant 32 | VM ID 64.
 * The tenant occupies bytes 4 to 7.
 */
#define VM_PREFIX0 0xfd
#define VM_PREFIX1 0xaa

/*
 * Host WireGuard addresses live in fdab::/16.
 */
#define UNDERLAY_PREFIX0 0xfd
#define UNDERLAY_PREFIX1 0xab

/* ICMPv6 neighbour discovery message types. */
#define NDISC_NEIGHBOUR_SOLICITATION 135
#define NDISC_NEIGHBOUR_ADVERTISEMENT 136

/* Recovery messages travel inside WireGuard as an experimental IPv6 next header. */
#define ATLAS_CONTROL_NEXT_HEADER 253
#define ATLAS_VER 1
#define MESSAGE_OPERATION_NOT_HERE 3

/* The WireGuard recovery message. */
struct atlas_msg
{
	__u8 ver;
	__u8 op;
	__u8 pad[2];
	struct in6_addr vm;
	struct in6_addr host;
};

/* WireGuard recovery packets have no Ethernet header. */
#define WIREGUARD_CONTROL_MESSAGE_OFFSET sizeof(struct ipv6hdr)
#define WIREGUARD_CONTROL_PACKET_LENGTH (WIREGUARD_CONTROL_MESSAGE_OFFSET + sizeof(struct atlas_msg))

/* The fixed part of a neighbour solicitation or advertisement. */
struct ndp_message
{
	struct icmp6hdr icmp;
	struct in6_addr target;
};

#endif /* ATLAS_PROTOCOL_H */
