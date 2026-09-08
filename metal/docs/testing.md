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
sudo -E go test -tags integration -v ./internal/network/
```

The tests attach the eBPF program to `tap0` in a temporary namespace. They verify that only host-to-guest TCP traffic updates activity. They also verify wake delivery when no process reads `tap0`.

## Sleepy VM test

This test needs Linux 6.6 or newer. Start metald with a short sleep timeout, then run the test:

```sh
sudo env METALD_SLEEP_ENABLED=true METALD_SLEEP_IDLE_TIMEOUT=30s metald serve --config /tmp/metald/metald.toml
sudo metal/test/integration/sleepy-vm-test.sh
```

```text
running -> idle -> sleeping (no Firecracker process)
                      |
                 TCP packet
                      v
                   running
```

The test runs this cycle twice. It verifies that the same guest token and process survive each wake. It removes the VM when it exits.

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
| `wg_mesh.enabled` | `true` | Atlas WG Mesh integration. Set `false` for a test host with no mesh; VMs then get no mesh connectivity. |
| `wg_mesh.uplink` | none | Discovery interface. Required when mesh is enabled. |
| `sleep.enabled` | `false` | Enable automatic sleep for sleepy VMs. |
| `sleep.idle_timeout` | none | Idle timeout for all sleepy VMs. Must be positive when sleep is enabled. |

A VM request and record contain only `is_sleepy`. The timeout applies to all sleepy VMs on the host.

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
| `METALD_WG_MESH_ENABLED` | `false` | Write `wg_mesh.enabled`. The development host boots without the mesh CLI by default. |
| `METALD_SLEEP_ENABLED` | `false` | Write `sleep.enabled`. Enable it for a sleep test. |
| `METALD_SLEEP_IDLE_TIMEOUT` | `30m` | Write `sleep.idle_timeout`. Use `1m` to watch a VM sleep. |
| `METALD_IMAGE_VERSION` | `22.04` | Ubuntu version the guest image builder uses. |

## Manual access

```sh
sudo ip netns exec metal-<id> ssh -i /tmp/metald/keys/id_ed25519 root@172.16.0.2
```

Use `journalctl -fu metal-vm@<id>.service` to read the guest console.
