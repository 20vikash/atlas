#include "bpf_helpers.h"

#define ETH_HEADER_LEN 14
#define ETHERTYPE_IPV4 0x0800
#define ETHERTYPE_IPV6 0x86DD
#define IP_PROTOCOL_TCP 6
#define IPV4_PROTOCOL_OFFSET (ETH_HEADER_LEN + 9)
#define IPV6_NEXT_HEADER_OFFSET (ETH_HEADER_LEN + 6)

/* activity_by_user_id maps a VM user ID to its last packet time. */
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

/* wake_state_by_user_id holds each VM wake state. */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u32);
} wake_state_by_user_id SEC(".maps");

/* wake_event carries a VM user ID and packet time to Go. */
struct wake_event {
	__u32 user_id;
	__u64 packet_time;
};

/* Force bpf2go to emit wake_event. */
struct wake_event *unused_wake_event __attribute__((unused));

/* wake_events carries wake events to Go. */
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 16);
} wake_events SEC(".maps");

/* virtual_machine_user_id is rewritten for each VM. */
const volatile __u32 virtual_machine_user_id = 0;

/* is_tcp returns 1 for TCP over IPv4 or IPv6. */
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

/* record_activity stores the time of a host-to-guest TCP packet. */
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

	/* Change armed to notified once. */
	if (__sync_val_compare_and_swap(state, WAKE_ARMED, WAKE_NOTIFIED) != WAKE_ARMED) {
		bpf_ringbuf_discard(event, 0);
		return TCX_NEXT;
	}

	event->user_id = key;
	event->packet_time = now;
	bpf_ringbuf_submit(event, 0);
	return TCX_NEXT;
}

char _license[] SEC("license") = "GPL";
