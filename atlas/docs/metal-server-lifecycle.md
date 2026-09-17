# Metal Server lifecycle

Manual creation creates one provider host before it inserts the matching Metal Server document. Placement first inserts a Pending document. Its background job then creates the provider host and runs setup.

## Creation

`Metal Server.before_validate` uses this sequence:

1. Validate Atlas Settings and the provider of each catalog record.
2. Stop if the document already has `provider_server_id`.
3. Set the architecture from Metal Server Size.
4. For manual creation, call `ensure_server` with the stable Metal Server name. Apply its values and record whether this request created the provider host.
5. For placement, insert the Pending document and let the setup job call `ensure_server`.

If document insertion fails, Atlas deletes the provider host only when this request created it. A reused host remains available for retry.

## Provisioning

`ServerProvisioner` uses this sequence:

1. Create or reuse the provider host with its stable Metal Server name.
2. Prepare the provider infrastructure and network attachment.
3. Wait for root Secure Shell access.
4. Configure the provider host network.
5. Configure the existing WireGuard interface.
6. Install Metal.
7. Set the Metal Server status to `Running`.

Each successful step stores the current Metal Server setup fields. The provisioner owns each commit during this long operation.

The setup job runs as Administrator even when a tenant request queued it.

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
- `sync_state`
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
