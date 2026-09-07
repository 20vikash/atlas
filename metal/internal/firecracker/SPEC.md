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
set unit resource limits
wait for the API socket       the process belongs to systemd, so this is the only signal
    |
    +- cold: prepare the disk, then configure machine, kernel, drives, network, MMDS
    +- warm: prepare the disk from the warm snapshot, load state and memory, resume
```

A jail is never reused. Each launch discards the previous one, because leftover state is harder to reason about than a rebuild.

A start tries three sources in order: the VM's own memory snapshot, the shared warm image, and a cold boot. A VM-local snapshot is the most specific saved state, so a warm-stopped VM and a warm-image clone resume through this one start path. Warm launch is attempted only when the image asks for it and the VM shape matches the snapshot exactly. Any failure falls back to a cold boot, so warm boot can never make a VM unstartable. A restored guest receives its metadata before it resumes, so it never reads the values of the VM the snapshot came from.

A memory snapshot restores only into the Firecracker build that wrote it. The binary reports no version, so its size and modification time stand in for one.

## Memory snapshots

A memory snapshot is one full Firecracker snapshot: a `state` file and a `memory` file. Any VM can be snapshotted, not only a warm image builder or a sleepy VM. The create and publish path is the same for every VM, so the snapshot bytes and format are the same whatever the caller does with the result.

Resuming from a memory snapshot is likewise available to any VM. A VM whose image provides a memory snapshot resumes from it on the normal start path, independent of the sleep policy.

```text
pause the guest
createFullSnapshot   -> jail/snapshot-pending/{state,memory}
publish              -> move to machines/<id>/snapshots/generations/<n>/{state,memory}
                        write manifest.json last, then read it back and validate
```

The manifest is the only signal that a generation is complete. It records the VM ID, user ID, the specification and restart generations, the Firecracker build, the creation time, the fixed file names, and the file sizes. A restore derives every path from the VM ID and never takes a path from the manifest. A new generation is used for every publish, so a live process never has its memory file overwritten.

A published snapshot has more than one use. All uses share the same create and publish path:

- Warm image building runs on a throwaway builder VM. It publishes a snapshot, then promotes the files into the image store, keyed by image, shape, and build, so every VM of that image reuses them. The builder VM and its snapshot are then removed.
- A warm stop publishes the snapshot under the VM's own directory and keeps it, so a later start resumes it. It is never promoted and never leaves the host. The controller asks for a warm stop through the power API. Automatic sleep uses the same warm stop for an idle VM.

A throwaway builder VM is one caller of this path, not a limit on it. The same path can snapshot any VM.

A warm stop pauses the guest, publishes the snapshot, then terminates Firecracker. Publication before termination keeps an interrupted warm stop recoverable, because a later start restores the published snapshot. A normal start restores the newest valid snapshot for the VM and then removes that external generation, because the jail holds its own copy. A plain stop, a restart, and a remove discard every snapshot, so a start after them cold boots. A generation change also invalidates an old snapshot, because its manifest no longer matches the desired generation.

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
