# firecracker: VM runtime

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

This package does not own a VM process. systemd does. Each VM is a unit of the `metal-vm@` template, so a crash of metald leaves running guests alone, and a restart of metald finds them by asking systemd rather than by remembering anything.

What this package owns is everything inside that unit: the jail, the boot configuration, the guest metadata, and the API conversation with Firecracker. It implements `vm.Runtime` and holds no VM records.

## Types

| Type | Owns |
|---|---|
| `Runtime` | The `vm.Runtime` implementation and the host services it needs. |
| `machine` | One VM for the length of one operation. Binds a unit and an API client. |
| `Config` | Host directories and executable paths. |

`Runtime` also satisfies `vm.WarmRuntime`, which supplies Firecracker build identity and memory snapshot capture.

## Composition

```text
vm.Manager -> firecracker.Runtime -> systemd -> jailer -> Firecracker
                         |
                         +-> VM disk storage
                         +-> image storage
                         +-> serial broker
```

## Jail

Everything one VM owns lives under its own directory, so removing that directory removes the jail, the chroot, and the jailer environment together. The kernel is hard linked into the chroot, which is why the chroot base sits beside the VM files rather than on another file system.

The jailer arguments reach systemd through an `EnvironmentFile`. systemd word splits that value, so no argument may contain a space. Firecracker's API socket lives inside the chroot at a fixed relative path, and a symlink outside gives metald a short path to dial.

## Launch

Cold and warm launch share one preparation step:

```text
write the jailer environment and the socket link
open the console PTY          before the unit starts, so no output is lost
start the systemd unit
persist the console master    after the unit holds the PTY slave
set unit resource limits
wait for the API socket       the process belongs to systemd, so this is the only signal
    |
    +- cold: prepare the disk, then configure machine, kernel, drives, network, MMDS
    +- warm: prepare the disk from the warm snapshot, load state and memory, resume
```

A jail is never reused. Each launch discards the previous one, because leftover state is harder to reason about than a rebuild.

A start mode selects the source:

```text
StartNormal                  -> shared warm image -> failure -> cold boot
StartFromSleepSnapshot       -> VM snapshot -> resume
StartFromSleepSnapshotPaused -> VM snapshot -> stay paused
```

`StartNormal` uses a warm image only when the image and VM shape match. It cold boots if warm launch fails. A sleep snapshot restore returns an error instead of cold booting. This protects the saved guest state. Metadata is updated before a restored guest can run.

A memory snapshot restores only into the Firecracker build that wrote it. The binary reports no version, so its size and modification time stand in for one.

## Memory snapshots

A full snapshot contains a `state` file and a `memory` file. Warm image builds and warm stops use the same create path.

```text
running -> pause -> jail/memory-snapshot-pending/{state,memory}
                    |
                    v
          machines/<id>/memory-snapshots/<n>/
                    |
                    +-> write manifest.json last
                    |
                    +-> warm image: promote to image store
                    +-> warm stop: terminate Firecracker -> sleeping

sleeping -> copy snapshot to new jail -> load -> update metadata -> resume
```

The manifest marks a complete generation. It records VM identity, record generations, Firecracker compatibility, creation time, fixed file names, and file sizes. Restore paths come from the VM ID, not the manifest.

Each publish creates a new generation. It never overwrites memory used by a live process. A warm image build promotes its snapshot and removes its temporary VM. A warm stop keeps its snapshot on this host.

The snapshot is published before Firecracker stops. This makes a warm stop safe to retry:

| Runtime state | Valid snapshot | Result |
|---|---|---|
| stopped | yes | Warm stop is complete. |
| paused | yes | Terminate Firecracker. |
| running | yes | Report a conflict. |

A restore uses the newest valid snapshot. It removes the external generation only after the new jail has loaded its copy. A plain stop, restart, remove, or incompatible specification change discards the snapshot. `InspectSleepSnapshot` reports absent and invalid snapshots as different errors.

A requested snapshot restore never falls back to a cold boot. It can also load the guest in a paused state.

## State

State comes from two places. systemd owns whether the process runs, and Firecracker owns what the guest does inside it. Only an active unit is worth asking:

```text
failed                  -> failed
inactive, deactivating  -> stopped
active -> Firecracker instance state
             Not started -> created
             Running     -> running
             Paused      -> paused
```

Anything else reads as `unknown`, and an API fault returns `unknown` with an error rather than a guess.

Stop asks the guest to power off and kills it when it does not answer, because a guest with no ACPI handler never will. An intentional stop leaves the unit failed, so the state is cleared afterwards. A plain stop also discards the VM memory snapshot. A warm stop is the other stop mode, which keeps a snapshot.

`Remove` stops the unit and removes runtime-owned files, including every VM memory snapshot. The manager releases network and storage.

## Guest metadata

Firecracker serves MMDS to the guest. This package builds that document, so nothing per VM is baked into an image and one image serves every VM. Metadata is replaced in place on a live guest and rewritten on every launch.

## SSH console

An SSH session authorizes itself with a throwaway key pair: the public key is pushed into MMDS, `ssh` runs inside the VM network namespace on a PTY, and the key is removed when the session closes. No key outlives a session, and the guest image carries none. Sessions are capped host-wide.

## Related

- [internal/firecracker/api/SPEC.md](api/SPEC.md) describes the Firecracker client.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines the runtime interface and owns the temporary VM a warm build runs on.
- [internal/storage/SPEC.md](../storage/SPEC.md) owns warm artifacts and the key that selects them.
- [internal/platform/SPEC.md](../platform/SPEC.md) owns the systemd unit control.
- [docs/vm.md](../../docs/vm.md) gives the VM lifecycle.
