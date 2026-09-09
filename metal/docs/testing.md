# Integration testing

Use [Metal development](development.md) for normal package checks. Use [Metal operations](operations.md) for fault recovery.

Metal integration tests need a Linux host with root access, KVM, ZFS, iptables, `curl`, `jq`, and `sha256sum`.

## Prepare the host

```sh
sudo env METALD_BULK_DIR=/path/to/large/disk metal/scripts/dev.sh
sudo metal/dist/metald serve --config /tmp/metald/metald.toml
```

Run the script again when required. It performs these actions:

- Downloads Firecracker and Jailer.
- Builds the Atlas guest image with `build_ubuntu_server_image.sh` and imports it with its manifest. The image bakes the sshd `AuthorizedKeysCommand`, the cloud-init datasource, the network, and the metadata service, so a VM reads its per-VM SSH key from MMDS. Set `METALD_IMAGE_VERSION` to pick 22.04 or 24.04.
- Creates the ZFS pool and parent datasets.
- Creates a Secure Shell key.
- Installs the systemd template unit.
- Enables host forwarding and NAT.
- Writes the Metal configuration and development token digest.

The default development token is `metal-development-token`. Set `METALD_AUTH_TOKEN` to use another value.
The setup script does not print the configured token.

## Secure Shell test

`dev.sh` prepares the default test image. Run the test while `metald` is active:

```sh
sudo metal/test/integration/ssh-test.sh
```

Use the `METALD_IMAGE_URL`, digest, kernel, and architecture variables to test another image.

The script reserves a VM, waits for reconciliation, and connects to `172.16.0.2` in the VM namespace. It requests termination when the test ends.

## Network activity tests

These tests need root, `ip`, and Linux 6.6 or newer. Run them when no other test uses the same namespaces:

```sh
sudo -E go test -tags integration -v ./internal/network/traffic/
```

The tests attach the eBPF program to `tap0` in a temporary namespace. They verify host-to-guest IP traffic and operation when no process reads `tap0`.

## Idle shutdown test

This test needs Linux 6.6 or newer. Start metald, then run the test with a short per-VM timeout:

```sh
sudo metal/dist/metald-linux-amd64 serve --config /tmp/metald/metald.toml
sudo metal/test/integration/idle-shutdown-test.sh
```

```text
running -> idle -> stopped (no Firecracker process)
					 |
			 IPv4 or IPv6 packet
					 v
				  running
```

The test runs this cycle twice. It verifies that the same guest token and process survive each restoration. It then sends an explicit stop and verifies a cold start.

## Configuration

| Key | Default | Meaning |
|---|---|---|
| `metald.base_dir` | `/var/lib/metal` | Host state directory. |
| `metald.listen` | `127.0.0.1:8080` | TCP address or `unix:/path`. |
| `metald.auth_token_hash` | none | Required lowercase SHA-256 token digest. |
| `firecracker.binary_path` | `/usr/bin/firecracker` | Firecracker binary. |
| `firecracker.sockets_dir` | `/run/metal` | Short VM socket links. |
| `jailer.binary_path` | `/usr/bin/jailer` | Jailer binary. |
| `zfs.pool` | `metal` | ZFS pool name. |
| `wg_mesh.enabled` | `true` | Enables Atlas WG Mesh on the host. |
| `wg_mesh.uplink` | none | Discovery interface. Required. |
| `traffic_monitor.enabled` | `true` | Enables VM packet monitoring and idle shutdown. |

The idle timeout is per VM. A create request and a compute request carry `sleep_after_idle_seconds`.

## Development environment

| Variable | Default | Meaning |
|---|---|---|
| `METALD_BULK_DIR` | `/tmp/metald` | Directory for the ZFS pool file. |
| `METALD_WORKDIR` | `/tmp/metald` | Directory for runtime files, images, keys, and configuration. |
| `METALD_POOL_SIZE` | 8 GiB to 30 GiB | ZFS pool file size. |
| `METALD_FC_VERSION` | `v1.16.1` | Firecracker release. |
| `METALD_POOL` | `metal` | ZFS pool name. |
| `METALD_LISTEN` | `127.0.0.1:8080` | API address in the generated configuration. |
| `METALD_AUTH_TOKEN` | `metal-development-token` | API bearer token. |
| `METALD_SLEEP_AFTER_IDLE_SECONDS` | `1` | The per-VM timeout that the idle shutdown test requests. |
| `METALD_IMAGE_VERSION` | `22.04` | Ubuntu version the guest image builder uses. |

## Manual access

```sh
sudo ip netns exec metal-<id> ssh -i /tmp/metald/keys/id_ed25519 root@172.16.0.2
```

Read the guest console with `GET /v1/vms/{id}/console`. Use `journalctl -fu metal-vm@<id>.service` for jailer and Firecracker errors.
