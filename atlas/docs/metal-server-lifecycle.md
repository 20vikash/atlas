# Metal Server lifecycle

Atlas creates one provider host before it inserts the matching Metal Server document. It then runs setup in a background job.

## Creation

`Metal Server.before_validate` uses this sequence:

1. Validate Atlas Settings and both provider catalog records.
2. Stop if the document already has `provider_server_id`.
3. Call `ensure_server` with the stable Metal Server name.
4. Apply the returned provider values to the document.
5. Record whether this request created the provider host.

If document insertion fails, Atlas deletes the provider host only when this request created it. A reused host remains available for retry.

## Provisioning

`ServerProvisioner` uses this sequence:

1. Prepare the provider infrastructure and network attachment.
2. Wait for root Secure Shell access.
3. Configure the provider host network.
4. Configure the existing WireGuard interface.
5. Install Metal.
6. Set the Metal Server status to `Running`.

Each successful step stores the current Metal Server setup fields. The provisioner owns each commit during this long operation.

A retry runs the sequence again. Each external operation must be safe to repeat.

## Failure behavior

The provisioner keeps useful fields and sets the Metal Server status to `Failed`. It writes the Metal Server name, operation, and failed phase to logs.

The Error Log gives the failed phase. It does not contain credentials, signed URLs, or command output.

Use the existing Setup Metal Server action after you correct the cause. Atlas does not add an operation DocType for setup progress.

## Desk methods

The existing whitelisted methods remain the public boundary:

- `ping_server`
- `setup_server`
- `configure_wireguard`
- `reboot_server`
- `poweroff_server`
- `poweron_server`
- `archive_server`
- `sync_disks`
- `install_metald`
- `upgrade_metald`

These methods check permissions and local state. Dedicated server objects perform the long operations.

## Metald upgrade

`install_metald` writes the host configuration and systemd units. It installs missing binaries and does not replace a running daemon.

`upgrade_metald` downloads the metald build from Atlas Settings, saves the current binary at `/usr/bin/metald.previous`, installs the new binary, and restarts `metal.service`.

Both methods use one job lock per server. Setup and upgrade cannot run together on the same host.

A restart keeps systemd's console descriptors. The virtual machines stay up.

If the descriptor store is empty, every virtual machine on the host stops during the upgrade. The script reports the descriptor count before the restart.

The script checks the service five times. If the new binary fails, the script restores the earlier binary, restarts the service, and reports the failure.

Run **Re-configure Metald** if the script reports an old unit. This action adds `FileDescriptorStorePreserve=yes`. A restart is safe before this update, but a service stop is not. The host systemd version must support this setting.
