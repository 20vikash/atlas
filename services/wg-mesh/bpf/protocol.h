/* SPDX-License-Identifier: AGPL-3.0 */
/*
 * Atlas WG Mesh protocol definitions.
 *
 * This file contains the values that must match on every Atlas WG Mesh host.
 * The Atlas NDP option is part of the on-wire neighbour advertisement.
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

/* Debug event operations for NDP handling. Values 1 to 4 are reserved for
 * the legacy discovery relay protocol. */
#define NDP_OPERATION_ANNOUNCE 5 /* Atlas option added to an outgoing NA. */
#define NDP_OPERATION_LEARN 6	/* Atlas option parsed from an incoming NA. */

/* The Atlas NDP option. NDP option type 253 is reserved for experiments. */
#define ATLAS_NDP_OPTION_TYPE 253
#define ATLAS_NDP_OPTION_LENGTH 3 /* Units of 8 bytes. */

/*
 * The option carries only the owning host WireGuard address. NDP options must
 * be a multiple of 8 bytes, so 6 bytes of padding follow the address.
 */
struct atlas_ndp_option
{
	__u8 type;
	__u8 length;
	struct in6_addr host;
	__u8 pad[6];
};

/* The fixed part of a neighbour solicitation or advertisement. */
struct ndp_message
{
	struct icmp6hdr icmp;
	struct in6_addr target;
};

#endif /* ATLAS_PROTOCOL_H */
