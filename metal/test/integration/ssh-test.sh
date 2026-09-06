#!/usr/bin/env bash
# Create a VM through the Metal API. Then, connect to it with the development SSH key.
# Run this command as root while `metald serve` runs in another terminal:
#   sudo test/integration/ssh-test.sh
set -euo pipefail

work_directory=${METALD_WORKDIR:-/tmp/metald}
listen_address=${METALD_ADDR:-127.0.0.1:8080}
private_key=${METALD_KEY:-$work_directory/keys/id_ed25519}
ssh_user=${METALD_SSH_USER:-root}
authentication_token=${METALD_AUTH_TOKEN:-metal-development-token}
bulk_directory=${METALD_BULK_DIR:-$work_directory}
host_architecture=$(uname -m)
image_base_url=https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/$host_architecture

case $host_architecture in
	x86_64) default_architecture=amd64 ;;
	aarch64) default_architecture=arm64 ;;
	*) echo "unsupported architecture: $host_architecture" >&2; exit 1 ;;
esac

image_url=${METALD_IMAGE_URL:-$image_base_url/ubuntu-22.04.ext4}
image_sha256=${METALD_IMAGE_SHA256:-$(sha256sum "$bulk_directory/downloads/ubuntu.ext4" | cut -d " " -f 1)}
kernel_url=${METALD_KERNEL_URL:-$image_base_url/vmlinux-5.10.223}
kernel_sha256=${METALD_KERNEL_SHA256:-$(sha256sum "$work_directory/images/ubuntu/vmlinux" | cut -d " " -f 1)}
architecture=${METALD_ARCHITECTURE:-$default_architecture}

call_metal() { curl -sS "http://$listen_address$1" -H "Authorization: Bearer $authentication_token" "${@:2}"; }

public_key=$(cat "$private_key.pub")
requested_virtual_machine_id="integration-$(cat /proc/sys/kernel/random/uuid)"
request_body=$(jq -n \
	--arg image_url "$image_url" --arg image_sha256 "$image_sha256" \
	--arg kernel_url "$kernel_url" --arg kernel_sha256 "$kernel_sha256" \
	--arg architecture "$architecture" --arg ssh_key "$public_key" \
	--arg hostname "$requested_virtual_machine_id" \
	'{
		compute: {
			virtual_cpu_count: 1,
			memory_mib: 256
		},
		disk: {
			size_mib: 1024,
			throughput_mibps: 0,
			iops: 0
		},
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
		guest: {
			hostname: $hostname,
			ssh_keys: [$ssh_key],
			metadata: {},
			user_data: ""
		}
	}')
response=$(call_metal "/v1/vms/$requested_virtual_machine_id" -X PUT -H 'content-type: application/json' -d "$request_body")
virtual_machine_id=$(echo "$response" | jq -r '.id // empty')
if [[ -z $virtual_machine_id ]]; then
	echo "create failed: $response" >&2
	exit 1
fi
echo "created VM $virtual_machine_id"
trap 'call_metal "/v1/vms/$virtual_machine_id" -X DELETE >/dev/null 2>&1 || true' EXIT

echo "waiting for ssh..."
for _ in $(seq 1 30); do
	if ip netns exec "metal-$virtual_machine_id" ssh -i "$private_key" \
		-o StrictHostKeyChecking=no -o ConnectTimeout=3 \
		"$ssh_user@172.16.0.2" 'echo metal-ok' 2>/dev/null | grep -q metal-ok; then
		echo "SSH OK - VM $virtual_machine_id is reachable"
		exit 0
	fi
	sleep 2
done
echo "FAILED: could not ssh into VM $virtual_machine_id" >&2
exit 1
