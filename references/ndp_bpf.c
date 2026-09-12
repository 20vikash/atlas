#include "vmlinux.h"

#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>


#define TC_ACT_OK 0

#define ETH_P_IPV6     0x86DD
#define IPPROTO_ICMPV6 58

#define NDISC_NS 135
#define NDISC_NA 136


/*
 * Kernel kfunc.
 *
 * This MUST exactly match atlas_neigh.c.
 *
 * Five arguments is the maximum number of explicit
 * arguments available to a normal BPF function call.
 */
extern int atlas_register_neigh(
    __u32 ifindex,
    __u32 addr0,
    __u32 addr1,
    __u32 addr2,
    __u32 addr3
) __ksym;


/*
 * Ethernet header.
 */
struct ethhdr_atlas {
    __u8  dst[6];
    __u8  src[6];
    __be16 proto;
};


/*
 * IPv6 header.
 */
struct ipv6hdr_atlas {
    __u8    priority;
    __u8    flow_lbl[3];
    __be16  payload_len;
    __u8    nexthdr;
    __u8    hop_limit;

    struct in6_addr saddr;
    struct in6_addr daddr;
};


/*
 * ICMPv6 header.
 */
struct icmp6hdr_atlas {
    __u8   type;
    __u8   code;
    __be16 checksum;
    __be32 data;
};


/*
 * Beginning of both Neighbor Solicitation
 * and Neighbor Advertisement.
 *
 * Both contain:
 *
 *     ICMPv6 header
 *     target IPv6 address
 */
struct ndp_atlas {
    struct icmp6hdr_atlas icmp;
    struct in6_addr target;
};


SEC("tc")
int atlas_ndp(struct __sk_buff *skb)
{
    void *data = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;

    struct ethhdr_atlas *eth;
    struct ipv6hdr_atlas *ip6;
    struct ndp_atlas *ndp;


    /*
     * --------------------------------------------------
     * Ethernet
     * --------------------------------------------------
     */
    eth = data;

    if ((void *)(eth + 1) > data_end)
        return TC_ACT_OK;

    if (eth->proto != bpf_htons(ETH_P_IPV6))
        return TC_ACT_OK;


    /*
     * --------------------------------------------------
     * IPv6
     * --------------------------------------------------
     */
    ip6 = (void *)(eth + 1);

    if ((void *)(ip6 + 1) > data_end)
        return TC_ACT_OK;

    if (ip6->nexthdr != IPPROTO_ICMPV6)
        return TC_ACT_OK;


    /*
     * --------------------------------------------------
     * ICMPv6 / NDP
     * --------------------------------------------------
     */
    ndp = (void *)(ip6 + 1);

    if ((void *)(ndp + 1) > data_end)
        return TC_ACT_OK;


    /*
     * Only NS and NA are interesting to Atlas.
     */
    if (ndp->icmp.type != NDISC_NS &&
        ndp->icmp.type != NDISC_NA)
        return TC_ACT_OK;


    /*
     * --------------------------------------------------
     * Neighbor Solicitation
     * --------------------------------------------------
     *
     * NS is the neighbour-discovery probe.
     *
     * Do NOT create a neighbour from the NS.
     *
     * Just allow normal NDP processing to continue.
     */
    if (ndp->icmp.type == NDISC_NS)
        return TC_ACT_OK;


    /*
     * --------------------------------------------------
     * Neighbor Advertisement
     * --------------------------------------------------
     *
     * We have received the answer.
     *
     * Extract the advertised IPv6 address.
     */
    __u32 ifindex = skb->ifindex;

    __u32 addr0 = ndp->target.in6_u.u6_addr32[0];
    __u32 addr1 = ndp->target.in6_u.u6_addr32[1];
    __u32 addr2 = ndp->target.in6_u.u6_addr32[2];
    __u32 addr3 = ndp->target.in6_u.u6_addr32[3];


    /*
     * Tell the kernel neighbour subsystem that this
     * neighbour should be managed and externally learned.
     *
     * Normal Linux NDP processing still continues.
     */
    int ret = atlas_register_neigh(
        ifindex,
        addr0,
        addr1,
        addr2,
        addr3
    );


    bpf_printk(
        "ATLAS NDP: NA "
        "ifindex=%d "
        "target=%x:%x:%x:%x "
        "kfunc_ret=%d",
        ifindex,
        addr0,
        addr1,
        addr2,
        addr3,
        ret
    );


    /*
     * Do not consume or modify the NA.
     *
     * Let the normal IPv6 NDP stack process it.
     */
    return TC_ACT_OK;
}


char LICENSE[] SEC("license") = "GPL";
