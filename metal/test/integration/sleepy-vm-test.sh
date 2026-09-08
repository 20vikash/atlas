#!/usr/bin/env bash
# Prove the full sleepy VM path: run, idle sleep, packet wake, and guest state
# continuity. Run this as root while metald serves with automatic sleep on and a
# short idle timeout:
#   sudo env METALD_SLEEP_ENABLED=true METALD_SLEEP_IDLE_TIMEOUT=30s metald serve --config /tmp/metald/metald.toml
#   sudo test/integration/sleepy-vm-test.sh
set -euo pipefail

work_directory=${METALD_WORKDIR:-/tmp/metald}
listen_address=${METALD_ADDR:-127.0.0.1:8080}
private_key=${METALD_KEY:-$work_directory/keys/id_ed25519}
ssh_user=${METALD_SSH_USER:-root}
authentication_token=${METALD_AUTH_TOKEN:-metal-development-token}
# The poll budget must exceed the Metal-wide idle timeout with slack.
sleep_poll_seconds=${METALD_SLEEP_POLL_SECONDS:-120}
host_architecture=$(uname -m)
image_base_url=https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/$host_architecture

case $host_architecture in
	x86_64) default_architecture=amd64 ;;
	aarch64) default_architecture=arm64 ;;
	*) echo "unsupported architecture: $host_architecture" >&2; exit 1 ;;
esac

# The wake path needs TCX, which is Linux 6.6 or newer. Fail before a VM is made.
kernel_release=$(uname -r)
kernel_major=${kernel_release%%.*}
kernel_rest=${kernel_release#*.}
kernel_minor=${kernel_rest%%.*}
if (( kernel_major < 6 || (kernel_major == 6 && kernel_minor < 6) )); then
	echo "kernel $kernel_release is too old: sleepy VM wake needs Linux 6.6 or newer" >&2
	exit 1
fi

manifest=$work_directory/images/ubuntu/manifest.json
image_url=${METALD_IMAGE_URL:-$image_base_url/ubuntu-22.04.ext4}
image_sha256=${METALD_IMAGE_SHA256:-$(jq -r .rootfs_sha256 "$manifest")}
kernel_url=${METALD_KERNEL_URL:-$image_base_url/vmlinux-5.10.223}
kernel_sha256=${METALD_KERNEL_SHA256:-$(jq -r .kernel_sha256 "$manifest")}
architecture=${METALD_ARCHITECTURE:-$default_architecture}

call_metal() { curl -sS "http://$listen_address$1" -H "Authorization: Bearer $authentication_token" "${@:2}"; }

observed_state() { call_metal "/v1/vms/$1" | jq -r '.observed.state // empty'; }

# guest_ssh runs one command in the guest and prints its output. It retries,
# because the first packet after sleep can be lost and only wakes the VM.
guest_ssh() {
	local id=$1 command=$2
	ip netns exec "metal-$id" ssh -i "$private_key" \
		-o StrictHostKeyChecking=no -o ConnectTimeout=5 \
		"$ssh_user@172.16.0.2" "$command" 2>/dev/null
}

public_key=$(cat "$private_key.pub")
requested_virtual_machine_id="sleepy-$(cat /proc/sys/kernel/random/uuid)"
request_body=$(jq -n \
	--arg image_url "$image_url" --arg image_sha256 "$image_sha256" \
	--arg kernel_url "$kernel_url" --arg kernel_sha256 "$kernel_sha256" \
	--arg architecture "$architecture" --arg ssh_key "$public_key" \
	--arg hostname "$requested_virtual_machine_id" \
	'{
		is_sleepy: true,
		compute: {virtual_cpu_count: 1, memory_mib: 256},
		disk: {size_mib: 1024, throughput_mibps: 0, iops: 0},
		image: {
			ref: "ubuntu", architecture: $architecture,
			rootfs: {url: $image_url, sha256: $image_sha256},
			kernel: {url: $kernel_url, sha256: $kernel_sha256},
			cache_image: false, memory_snapshot: false
		},
		network: {
			public_ipv4: "", wireguard_mesh_ipv6: "fdaa::2",
			private_network_throughput_mibps: 0, public_network_throughput_mibps: 0,
			egress: "uplink"
		},
		guest: {hostname: $hostname, ssh_keys: [$ssh_key], metadata: {}, user_data: ""}
	}')

response=$(call_metal "/v1/vms/$requested_virtual_machine_id" -X PUT -H 'content-type: application/json' -d "$request_body")
virtual_machine_id=$(echo "$response" | jq -r '.id // empty')
if [[ -z $virtual_machine_id ]]; then
	echo "create failed: $response" >&2
	exit 1
fi
echo "created sleepy VM $virtual_machine_id"
trap 'call_metal "/v1/vms/$virtual_machine_id" -X DELETE >/dev/null 2>&1 || true' EXIT

unit=metal-vm@$virtual_machine_id.service
machine_directory=$work_directory/machines/$virtual_machine_id

echo "waiting for ssh..."
for _ in $(seq 1 30); do
	if [[ $(guest_ssh "$virtual_machine_id" 'echo metal-ok') == metal-ok ]]; then
		break
	fi
	sleep 2
done
if [[ $(guest_ssh "$virtual_machine_id" 'echo metal-ok') != metal-ok ]]; then
	echo "FAILED: VM never became reachable" >&2
	exit 1
fi

# Put a unique token and a long-running process in the guest. Record the guest
# PID, so a wake can prove the same process survived.
token="metal-$(cat /proc/sys/kernel/random/uuid)"
guest_pid=$(guest_ssh "$virtual_machine_id" \
	"echo $token > /dev/shm/metal-sleep-token; nohup sleep 100000 >/dev/null 2>&1 </dev/null & echo \$!")
if [[ -z $guest_pid ]]; then
	echo "FAILED: could not start the guest marker process" >&2
	exit 1
fi
echo "guest token $token, marker PID $guest_pid"

# wait_for_sleeping stops deliberate traffic and waits for the sleeping state and
# a terminated Firecracker process.
wait_for_sleeping() {
	echo "waiting up to ${sleep_poll_seconds}s for the VM to sleep..."
	local deadline=$((SECONDS + sleep_poll_seconds))
	while (( SECONDS < deadline )); do
		if [[ $(observed_state "$virtual_machine_id") == sleeping ]]; then
			local main_pid
			main_pid=$(systemctl show -p MainPID --value "$unit" 2>/dev/null || echo 0)
			if [[ ${main_pid:-0} == 0 ]]; then
				return 0
			fi
		fi
		sleep 3
	done
	echo "FAILED: the VM did not sleep within ${sleep_poll_seconds}s" >&2
	exit 1
}

# wake_and_verify wakes the VM with a TCP connection and proves the guest state
# survived: the same marker PID and the same token.
wake_and_verify() {
	echo "waking the VM with a TCP connection..."
	local result
	for _ in $(seq 1 30); do
		result=$(guest_ssh "$virtual_machine_id" "cat /dev/shm/metal-sleep-token; kill -0 $guest_pid && echo alive")
		if [[ $result == *"$token"* && $result == *alive* ]]; then
			break
		fi
		sleep 2
	done
	if [[ $result != *"$token"* || $result != *alive* ]]; then
		echo "FAILED: the guest state did not survive the wake: $result" >&2
		exit 1
	fi
	if [[ $(observed_state "$virtual_machine_id") != running ]]; then
		echo "FAILED: the VM did not report running after wake" >&2
		exit 1
	fi
	echo "woke the VM: token and marker PID $guest_pid survived"
}

for cycle in 1 2; do
	echo "== sleep and wake cycle $cycle =="
	wait_for_sleeping

	# The ZFS disk and the sleep manifest must remain while asleep.
	if ! compgen -G "$machine_directory/snapshots/generations/*/manifest.json" >/dev/null; then
		echo "FAILED: no sleep manifest under $machine_directory" >&2
		exit 1
	fi
	echo "sleep manifest present; VM is asleep with no Firecracker process"

	wake_and_verify

	# A new period of traffic must keep the VM running, not re-sleep at once.
	for _ in $(seq 1 5); do
		guest_ssh "$virtual_machine_id" 'true' || true
		sleep 1
	done
	if [[ $(observed_state "$virtual_machine_id") != running ]]; then
		echo "FAILED: fresh traffic did not keep the VM running" >&2
		exit 1
	fi
done

echo "PASS: sleepy VM ran, slept, woke on a packet, and kept its guest state across two cycles"
