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
- Downloads and imports a test image with its manifest.
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

The packet activity tests need root, the `ip` command, and a host kernel with TCX support, Linux 6.6 or newer. Run them while no other test holds the same namespaces:

```sh
sudo -E go test -tags integration -v ./internal/network/
```

These tests create a temporary namespace and a `tap0`, attach the eBPF program, and confirm the last-seen time advances for a frame in each direction. One test confirms the egress hook still runs with no tap reader, which network wake depends on. The test logs the host kernel and result.

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
| `sleep.enabled` | `false` | Turn on automatic sleep for sleepy VMs. |
| `sleep.idle_timeout` | none | One Metal-wide idle timeout. Required and positive when enabled. |

`idle_timeout` is one host value for every sleepy VM. A VM request or record carries only `is_sleepy` and never a timeout.

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

## Manual access

```sh
sudo ip netns exec metal-<id> ssh -i /tmp/metald/keys/id_ed25519 root@172.16.0.2
```

Use `journalctl -fu metal-vm@<id>.service` to read the guest console.
