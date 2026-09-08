# Metal operations

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

## Sleepy VM does not sleep or wake

- Symptom: An idle sleepy VM does not reach `sleeping`, or a packet does not wake a sleeping VM.
- Owner: `network.ActivityMonitor` for activity and wake events, and `vm.Manager` for the sleep and wake decisions.
- Safe checks: Read `GET /v1/vms/{id}` and `observed.state`, `observed.sleeping_since`, `observed.last_network_activity_at`, and `observed.error`. Confirm `sleep.enabled` and a positive `sleep.idle_timeout`, and that the VM has `is_sleepy`. Run `systemctl show -p MainPID --value metal-vm@<id>.service`, which is `0` while asleep. List the sleep manifest with `ls <base_dir>/machines/<id>/snapshots/generations/*/manifest.json`. Inspect the eBPF state with `bpftool map show`, then `bpftool map dump name activity_by_user_id` and `bpftool map dump name wake_state_by_user_id` keyed by the VM user ID, and `bpftool link show` inside the namespace with `ip netns exec metal-<id> bpftool link show`. Read `journalctl -u metald --since "15 minutes ago"` for sleep, abort, wake, and failure logs.
- Expected evidence: Only a host-to-guest TCP segment updates activity and wakes a VM. A wake state of `1` is armed and `2` is notified. A missing wake event for a real packet is expected once and the client retries, because the first packet can be lost while no process has `tap0` open.
- Safe recovery: Send another TCP connection to retry the wake. A metald restart rearms every sleeping VM on its first reconcile pass. Correct a wrong `idle_timeout` or a missing `is_sleepy`.
- Do not: Do not delete a sleep manifest or a snapshot generation of a sleeping VM. Do not start Firecracker directly to wake a VM.
