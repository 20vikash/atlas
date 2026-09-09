#!/usr/bin/env bash
# Verify automatic idle shutdown, traffic restore, and explicit hard stop.
# Run as root while `metald serve` runs in another terminal.
set -euo pipefail

work_directory=${METALD_WORKDIR:-/tmp/metald}
listen_address=${METALD_ADDR:-127.0.0.1:8080}
private_key=${METALD_KEY:-$work_directory/keys/id_ed25519}
ssh_user=${METALD_SSH_USER:-root}
authentication_token=${METALD_AUTH_TOKEN:-metal-development-token}
idle_seconds=${METALD_IDLE_SECONDS:-10}
wait_seconds=${METALD_IDLE_WAIT_SECONDS:-180}
host_architecture=$(uname -m)
image_base_url=https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/$host_architecture

case $host_architecture in
	x86_64) architecture=amd64 ;;
	aarch64) architecture=arm64 ;;
	*) echo "unsupported architecture: $host_architecture" >&2; exit 1 ;;
esac

manifest=$work_directory/images/ubuntu/manifest.json
image_url=${METALD_IMAGE_URL:-$image_base_url/ubuntu-22.04.ext4}
image_sha256=${METALD_IMAGE_SHA256:-$(jq -r .rootfs_sha256 "$manifest")}
kernel_url=${METALD_KERNEL_URL:-$image_base_url/vmlinux-5.10.223}
kernel_sha256=${METALD_KERNEL_SHA256:-$(jq -r .kernel_sha256 "$manifest")}
architecture=${METALD_ARCHITECTURE:-$architecture}

call_metal() { curl -sS "http://$listen_address$1" -H "Authorization: Bearer $authentication_token" "${@:2}"; }

observed_state() { call_metal "/v1/vms/$1" | jq -r '.observed.state // empty'; }

guest_command() {
	local virtual_machine_id=$1
	local command=$2
	ip netns exec "metal-$virtual_machine_id" ssh -i "$private_key" \
		-o StrictHostKeyChecking=no -o ConnectTimeout=5 \
		"$ssh_user@172.16.0.2" "$command" 2>/dev/null
}

wait_for_state() {
	local virtual_machine_id=$1
	local expected_state=$2
	local deadline=$((SECONDS + wait_seconds))
	while (( SECONDS < deadline )); do
		if [[ $(observed_state "$virtual_machine_id") == "$expected_state" ]]; then
			return 0
		fi
		sleep 2
	done
	echo "VM did not reach $expected_state within ${wait_seconds}s" >&2
	return 1
}

wait_for_guest() {
	local virtual_machine_id=$1
	local deadline=$((SECONDS + wait_seconds))
	while (( SECONDS < deadline )); do
		if [[ $(guest_command "$virtual_machine_id" 'echo ready' || true) == ready ]]; then
			return 0
		fi
		sleep 2
	done
	echo "VM did not accept SSH within ${wait_seconds}s" >&2
	return 1
}

public_key=$(cat "$private_key.pub")
virtual_machine_id="idle-$(cat /proc/sys/kernel/random/uuid)"
request_body=$(jq -n \
	--arg image_url "$image_url" --arg image_sha256 "$image_sha256" \
	--arg kernel_url "$kernel_url" --arg kernel_sha256 "$kernel_sha256" \
	--arg architecture "$architecture" --arg ssh_key "$public_key" \
	--arg hostname "$virtual_machine_id" --argjson idle_seconds "$idle_seconds" \
	'{
		compute: {
			virtual_cpu_count: 1,
			memory_mib: 256,
			sleep_after_idle_seconds: $idle_seconds
		},
		disk: {size_mib: 1024, throughput_mibps: 0, iops: 0},
		image: {
			ref: "ubuntu",
			architecture: $architecture,
			rootfs: {url: $image_url, sha256: $image_sha256},
			kernel: {url: $kernel_url, sha256: $kernel_sha256},
			cache_image: false,
			memory_snapshot: false
		},
		network: {
			public_ipv4: "",
			wireguard_mesh_ipv6: "fdaa::2",
			private_network_throughput_mibps: 0,
			public_network_throughput_mibps: 0,
			egress: "uplink"
		},
		guest: {hostname: $hostname, ssh_keys: [$ssh_key], metadata: {}, user_data: ""}
	}')
response=$(call_metal "/v1/vms/$virtual_machine_id" -X PUT -H 'content-type: application/json' -d "$request_body")
if [[ $(echo "$response" | jq -r '.id // empty') != "$virtual_machine_id" ]]; then
	echo "create failed: $response" >&2
	exit 1
fi
trap 'call_metal "/v1/vms/$virtual_machine_id" -X DELETE >/dev/null 2>&1 || true' EXIT

namespace=metal-$virtual_machine_id
saved_state_directory=$work_directory/machines/$virtual_machine_id/saved-state
guest_mac=${METALD_GUEST_MAC:-06:00:ac:10:00:02}

for _ in $(seq 1 60); do
	if ip netns list | grep -qw "$namespace"; then
		ip -n "$namespace" neigh replace 172.16.0.2 lladdr "$guest_mac" dev tap0 nud permanent
		break
	fi
	sleep 1
done

wait_for_guest "$virtual_machine_id"
marker="metal-$(cat /proc/sys/kernel/random/uuid)"
marker_process=$(guest_command "$virtual_machine_id" "echo $marker > /dev/shm/metal-idle-marker; nohup sleep 100000 >/dev/null 2>&1 </dev/null & echo \$!")

for cycle in 1 2; do
	wait_for_state "$virtual_machine_id" stopped
	test -f "$saved_state_directory/metadata.json"

	for _ in $(seq 1 60); do
		result=$(guest_command "$virtual_machine_id" "cat /dev/shm/metal-idle-marker; kill -0 $marker_process && echo alive" || true)
		if [[ $result == *"$marker"* && $result == *alive* ]]; then
			break
		fi
		sleep 2
	done
	if [[ $result != *"$marker"* || $result != *alive* ]]; then
		echo "guest state was not restored during cycle $cycle" >&2
		exit 1
	fi
	wait_for_state "$virtual_machine_id" running
done

call_metal "/v1/vms/$virtual_machine_id/power" -X PUT -H 'content-type: application/json' -d '{"state":"stopped"}' >/dev/null
wait_for_state "$virtual_machine_id" stopped
if [[ -e $saved_state_directory ]]; then
	echo "explicit stop kept saved state" >&2
	exit 1
fi

call_metal "/v1/vms/$virtual_machine_id/power" -X PUT -H 'content-type: application/json' -d '{"state":"running"}' >/dev/null
wait_for_state "$virtual_machine_id" running
wait_for_guest "$virtual_machine_id"
if guest_command "$virtual_machine_id" "test -e /dev/shm/metal-idle-marker || kill -0 $marker_process"; then
	echo "explicit stop restored guest memory" >&2
	exit 1
fi

echo "idle shutdown integration test passed"
