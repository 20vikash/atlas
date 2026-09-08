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

/* Wake states for one VM user ID. A missing key means disarmed. */
#define WAKE_DISARMED 0
#define WAKE_ARMED 1
#define WAKE_NOTIFIED 2

/* wake_state_by_user_id holds the wake state of each VM user ID. A missing key
   means disarmed. Go arms a sleeping VM and disarms a running VM. metald sets the
   real capacity at load time. */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u32);
} wake_state_by_user_id SEC(".maps");

/* wake_event carries one wake signal to Go. It holds the VM user ID and the
   monotonic packet time only. It never holds packet bytes, addresses, or ports. */
struct wake_event {
	__u32 user_id;
	__u64 packet_time;
};

/* Force bpf2go to emit the Go type for wake_event. The ring buffer map carries
   no value type, so without this anchor the type is pruned from BTF. */
struct wake_event *unused_wake_event __attribute__((unused));

/* wake_events is the shared ring buffer that carries wake events to Go. The size
   is the byte length of the ring. One event per armed VM fits many times over. */
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 16);
} wake_events SEC(".maps");

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
   segment. When the VM wake state is armed, it submits one wake event to the ring
   buffer. The loader attaches it to the tap0 egress hook only, which carries
   host-to-guest traffic. It reads the ethertype and the IP protocol to select
   TCP, but it does not change the packet, and it always returns TCX_NEXT. */
SEC("tc")
int record_activity(struct __sk_buff *skb) {
	if (!is_tcp(skb))
		return TCX_NEXT;

	__u32 key = virtual_machine_user_id;
	__u64 now = bpf_ktime_get_ns();
	bpf_map_update_elem(&activity_by_user_id, &key, &now, BPF_ANY);

	__u32 *state = bpf_map_lookup_elem(&wake_state_by_user_id, &key);
	if (!state || *state != WAKE_ARMED)
		return TCX_NEXT;

	struct wake_event *event = bpf_ringbuf_reserve(&wake_events, sizeof(*event), 0);
	if (!event)
		return TCX_NEXT;

	event->user_id = key;
	event->packet_time = now;
	bpf_ringbuf_submit(event, 0);
	return TCX_NEXT;
}

char _license[] SEC("license") = "GPL";
