#include "bpf_helpers.h"

#define ETH_HEADER_LEN 14
#define ETHERTYPE_IPV4 0x0800
#define ETHERTYPE_IPV6 0x86DD
#define IP_PROTOCOL_TCP 6
#define IPV4_PROTOCOL_OFFSET (ETH_HEADER_LEN + 9)
#define IPV6_NEXT_HEADER_OFFSET (ETH_HEADER_LEN + 6)

/* activity_by_user_id maps one VM user ID to the last packet time. The value is
   a monotonic nanosecond clock from bpf_ktime_get_ns. Go converts it to a
   wall-clock time. metald sets the real capacity at load time. */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} activity_by_user_id SEC(".maps");

/* virtual_machine_user_id is the map key for this program instance. metald
   rewrites it before load, so one instance serves one VM. */
const volatile __u32 virtual_machine_user_id = 0;

/* is_tcp returns 1 when skb carries a TCP segment over IPv4 or IPv6. It reads
   only the ethertype and the IP protocol field. It does not change the packet.
   A short or unknown frame reads as not TCP. */
static __always_inline int is_tcp(struct __sk_buff *skb) {
	__u16 ethertype;
	if (bpf_skb_load_bytes(skb, 12, &ethertype, sizeof(ethertype)) < 0)
		return 0;

	__u8 protocol;
	if (ethertype == __builtin_bswap16(ETHERTYPE_IPV4)) {
		if (bpf_skb_load_bytes(skb, IPV4_PROTOCOL_OFFSET, &protocol, sizeof(protocol)) < 0)
			return 0;
	} else if (ethertype == __builtin_bswap16(ETHERTYPE_IPV6)) {
		if (bpf_skb_load_bytes(skb, IPV6_NEXT_HEADER_OFFSET, &protocol, sizeof(protocol)) < 0)
			return 0;
	} else {
		return 0;
	}

	return protocol == IP_PROTOCOL_TCP;
}

/* record_activity stores the current time for one VM on each host-to-guest TCP
   segment. The loader attaches it to the tap0 egress hook only, which carries
   host-to-guest traffic. It reads the ethertype and the IP protocol to select
   TCP, but it does not change the packet, and it always returns TCX_NEXT. */
SEC("tc")
int record_activity(struct __sk_buff *skb) {
	if (!is_tcp(skb))
		return TCX_NEXT;

	__u32 key = virtual_machine_user_id;
	__u64 now = bpf_ktime_get_ns();
	bpf_map_update_elem(&activity_by_user_id, &key, &now, BPF_ANY);
	return TCX_NEXT;
}

char _license[] SEC("license") = "GPL";
