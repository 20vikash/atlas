# Metal operations

For Go code, follow the repository [Go anti-pattern rules](../../llm/go-code-review-guide.md).

Use the virtual machine ID and operation ID to connect API state, JSON logs, systemd units, network namespaces, and ZFS resources. Do not print bearer tokens or signed URLs.

## Network convergence fails

- Symptom: A virtual machine stays failed or has no expected connectivity.
- Owner: `internal/network` reconciliation or host network configuration.
- Safe checks: Read `GET /v1/vms/{id}` and `observed.error`. Run `ip netns list`, `ip -n metal-<id> link`, and `journalctl -u metald --since "15 minutes ago"`.
- Expected evidence: Logs name the virtual machine, operation, and failed network step. The namespace and links show the partial state.
- Safe recovery: Correct the host interface, route, or firewall fault. Let reconciliation call `Network.Ensure` again.
- Do not: Do not create an alternate namespace for the same virtual machine. Do not change stored desired network JSON by hand.

## Firecracker and systemd report different states

- Symptom: Metal reports a state that does not match `metal-vm@<id>.service`.
- Owner: `firecracker.Runtime` inspection or the systemd unit.
- Safe checks: Run `systemctl status metal-vm@<id>.service`, `systemctl show metal-vm@<id>.service`, and `journalctl -u metal-vm@<id>.service`. Check the Firecracker socket link under `/run/metal`.
- Expected evidence: Metal inspection records each partial fault. Systemd remains the process owner.
- Safe recovery: Correct the unit or runtime-file fault. Wake reconciliation with the same desired request when required.
- Do not: Do not start Firecracker directly. Do not remove a running unit's files.

## Serial console is blank or unavailable

- Symptom: The console closes, or it has no output.
- Check: Confirm `/run/metal/consoles/<id>` and the `TTYPath`, `StandardInput`, and `StandardOutput` settings.
- Check: Read `NotifyAccess`, `FileDescriptorStoreMax`, and `FileDescriptorStorePreserve` for `metal.service`.
- Check: Run `metald version`. The build must support the descriptor store.
- Recovery: Correct the unit settings, run `systemctl daemon-reload`, and restart the VM through the API.
- Do not: Do not restart `metal-vm@<id>.service` or remove a running guest's console link.

## Virtual machines stop when metald stops

- Symptom: Every VM on the host fails with exit code 156 when `metal.service` stops or restarts.
- Cause: Firecracker holds the PTY slave as its controlling terminal. A closed PTY master sends SIGHUP to Firecracker.
- Cause: systemd keeps the master in the descriptor store, so the PTY survives a metald exit.
- Check: Run `systemctl show metal.service -p NFileDescriptorStore`. The count must be one for each running VM.
- Check: A count of 0 means that systemd holds no console master. Read the metald start log. `descriptor_store_available` must be true, and `adopted_console_count` must equal `running_unit_count`.
- Check: Confirm `NotifyAccess=main`, `FileDescriptorStoreMax`, and `FileDescriptorStorePreserve=yes` for `metal.service`.
- Check: Run `systemd-analyze log-level debug` to see why systemd drops a descriptor.
- Check: Start the VM through the API and read the journal for `Added fd` and `Received EPOLLHUP on stored fd`.
- Check: Run `systemd-analyze log-level info` to restore the log level.
- Recovery: Correct the unit settings and run `systemctl daemon-reload`.
- Recovery: Start each VM again through the API.
- Recovery: A running VM keeps its unprotected console until it starts again.
- Do not: Do not start or restart `metal-vm@<id>.service` by hand.

## Disk usage or ZFS inspection fails

- Symptom: `/v1/sync` fails or a virtual machine operation reports storage inspection failure.
- Owner: `internal/storage`, ZFS, or the host disk.
- Safe checks: Run `zpool status`, `zfs list`, and `journalctl -u metald --since "15 minutes ago"`. Check the configured pool name.
- Expected evidence: Metal returns no invented capacity value. Logs identify the failed inspection.
- Safe recovery: Restore the pool and retry synchronization or reconciliation.
- Do not: Do not publish a guessed capacity. Do not destroy a dataset until ownership is confirmed.

## Snapshot upload stays pending or completing

- Symptom: Atlas does not receive completed artifact hashes, or multipart completion does not finish.
- Owner: Metal upload worker, network access to the signed object storage URLs, or Atlas multipart completion.
- Safe checks: Read `GET /v1/snapshots/{id}`. Check the snapshot activity time and Metal logs. In Atlas, check image status and upload IDs.
- Expected evidence: Metal reports pending, uploading, completed, or failed with progress. Atlas keeps retry identifiers.
- Safe recovery: Correct network or object storage access. Retry from Atlas. The Metal start request and the multipart completion are safe to repeat.
- Do not: Do not log signed URLs. Do not remove staging during an active retry.

## Machine image cleanup fails

- Symptom: An image is Available or Cleaning, but Metal staging remains.
- Owner: The Metal snapshot store and staging reconciler.
- Safe checks: Read the Atlas image status. Call `GET /v1/snapshots/{id}`. Check Metal cleanup logs and ZFS state.
- Expected evidence: The public image objects are complete before Atlas requests staging deletion.
- Safe recovery: Send the same `DELETE /v1/snapshots/{id}` request or wait for stale-staging cleanup.
- Do not: Do not delete public image objects. Do not remove a ZFS snapshot that another clone uses.

## Metal cannot complete graceful shutdown

- Symptom: `metald` does not stop within the configured shutdown bound.
- Owner: The daemon lifecycle owner or an active snapshot upload worker.
- Safe checks: Read `journalctl -u metald`. Check shutdown, upload cancellation, and wait-group log entries. Check whether the process still accepts requests.
- Expected evidence: Shutdown cancels upload roots, stops background owners, and waits for their bounded completion. Guest systemd units remain active.
- Safe recovery: Allow the configured bound. Stop the daemon service again after the worker exits. Use process termination only after you preserve logs.
- Do not: Do not stop `metal-vm@*.service` units as part of daemon shutdown. Do not remove upload state while a worker is active.

## Automatic idle shutdown does not stop or restore a VM

- Symptom: An idle VM does not reach `stopped`, or new IP traffic does not restore it.
- Owner: `network/traffic.Monitor` tracks traffic and events. `vm.Manager` controls state changes.
- Safe checks: Read `GET /v1/vms/{id}`. Check `desired.compute.sleep_after_idle_seconds`, `observed.state`, and `observed.error`.
- Safe checks: Run `systemctl show -p MainPID --value metal-vm@<id>.service`. An automatically stopped VM has a value of `0`.
- Safe checks: Check `<base_dir>/machines/<id>/saved-state/metadata.json`, `bpftool map show`, `ip netns exec metal-<id> bpftool link show`, and the metald journal.
- Expected evidence: Host-to-guest IPv4 or IPv6 traffic updates `activity_by_user_id`. When observation is active, it also writes the VM user ID to `traffic_events`.
- Safe recovery: Retry the connection because the first packet can be lost during restoration. Correct the timeout or the host eBPF fault and let reconciliation retry.
- Do not: Do not delete a saved state that Metal must restore. Do not start Firecracker directly.
