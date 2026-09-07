#include "bpf_helpers.h"

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

/* record_activity stores the current time for this VM on every frame in both
   directions. It does not read packet bytes and does not change the packet. */
SEC("tc")
int record_activity(struct __sk_buff *skb) {
	__u32 key = virtual_machine_user_id;
	__u64 now = bpf_ktime_get_ns();
	bpf_map_update_elem(&activity_by_user_id, &key, &now, BPF_ANY);
	return TCX_NEXT;
}

char _license[] SEC("license") = "GPL";
